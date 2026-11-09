import SwiftUI
import BackgroundTasks
import AuthenticationServices

@Observable
final class AppState {
    static let backgroundTaskId = "com.rsong.mclaude.app.refresh"
    var serverHost: String {
        didSet { UserDefaults.standard.set(serverHost, forKey: "serverHost"); rebuildClient() }
    }
    var serverPort: Int {
        didSet { UserDefaults.standard.set(serverPort, forKey: "serverPort"); rebuildClient() }
    }
    var authToken: String {
        didSet { UserDefaults.standard.set(authToken, forKey: "authToken"); rebuildClient() }
    }

    // Push notifications: a tapped notification asks the shell to open this session;
    // visibleSessionId suppresses banners for the session already on screen.
    var openSessionRequest: String?
    var visibleSessionId: String? {
        // Read state for the watch (docs/plan-watch-app.md → Read state): seen on open and on leave.
        didSet {
            if let oldValue { SeenStore.shared.markSeen(oldValue) }
            if let visibleSessionId { SeenStore.shared.markSeen(visibleSessionId) }
        }
    }

    // Google SSO state
    var userName: String {
        didSet { UserDefaults.standard.set(userName, forKey: "userName") }
    }
    var userEmail: String {
        didSet { UserDefaults.standard.set(userEmail, forKey: "userEmail") }
    }
    var isSignedIn: Bool { !authToken.isEmpty }

    private(set) var client: APIClient
    private(set) var isConnected: Bool = false
    var lastDisconnectReason: String?
    private(set) var telemetry: TelemetryClient

    // WebSocket
    private(set) var wsClient: WSClient?
    var sessions: [ClaudeSession] = []
    var outputCache: [String: String] = [:]  // displayId -> output
    var loadedLines: [String: Int] = [:]  // displayId -> lines loaded so far
    var eventsCache: [String: [SessionEvent]] = [:]  // displayId -> structured events
    private var sessionIdMap: [String: String] = [:]  // displayId -> sessionId (tracks /clear)
    /// Saved transcripts (docs/design-claude-uplift.md → Saved transcripts).
    private(set) var transcriptStore: TranscriptStore
    /// Sessions already looked up on disk in this process (restore at most once).
    private var restoreTried: Set<String> = []
    /// Sessions showing a restored copy that no fresh load has joined yet.
    private var restoredFromDisk: Set<String> = []
    /// Sessions whose events changed since they were last saved.
    private var unsavedTranscripts: Set<String> = []

    // Skills autocomplete
    var skills: [SkillSuggestion] = []
    private var lastSkillsCwds: Set<String> = []
    private var lastSkillsFetch: Date = .distantPast

    init() {
        let host = UserDefaults.standard.string(forKey: "serverHost") ?? "100.90.85.91"
        var port = UserDefaults.standard.integer(forKey: "serverPort")
        if port == 8080 { port = 8377; UserDefaults.standard.set(port, forKey: "serverPort") }
        let resolvedPort = port > 0 ? port : 8377
        let token = UserDefaults.standard.string(forKey: "authToken") ?? ""

        self.serverHost = host
        self.serverPort = resolvedPort
        self.authToken = token
        self.userName = UserDefaults.standard.string(forKey: "userName") ?? ""
        self.userEmail = UserDefaults.standard.string(forKey: "userEmail") ?? ""
        let config = ServerConfig(host: host, port: resolvedPort, token: token.isEmpty ? nil : token)
        self.client = APIClient(config: config)
        self.telemetry = TelemetryClient(config: ServerConfig(host: host, port: resolvedPort))
        self.transcriptStore = TranscriptStore(host: host, port: resolvedPort)
        configureObservability()
    }

    /// OTLP telemetry to the collector on the Mac (docs/plan-observability.md).
    private func configureObservability() {
        let api = client
        Telemetry.shared.uploadScreenshot = { data in try? await api.uploadScreenshot(imageData: data) }
        Telemetry.shared.configure(serverHost: serverHost)
        Task { @MainActor [weak self] in
            AppVitals.shared.start(connected: { [weak self] in self?.isConnected ?? false })
        }
    }

    // MARK: - Google SSO

    func signInWithGoogle() async throws {
        // Get Google auth URL from server
        let config = ServerConfig(host: serverHost, port: serverPort)
        let url = URL(string: "\(config.baseURL)/auth/google/login")!
        let (data, _) = try await URLSession.shared.data(from: url)
        guard let json = try JSONSerialization.jsonObject(with: data) as? [String: String],
              let authURLString = json["url"],
              let authURL = URL(string: authURLString) else {
            throw URLError(.badServerResponse)
        }

        // Open Google sign-in in browser sheet
        let callbackURL = try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<URL, Error>) in
            let session = ASWebAuthenticationSession(
                url: authURL,
                callbackURLScheme: "mclaude"
            ) { url, error in
                if let error { continuation.resume(throwing: error); return }
                guard let url else { continuation.resume(throwing: URLError(.badURL)); return }
                continuation.resume(returning: url)
            }
            session.prefersEphemeralWebBrowserSession = false
            session.presentationContextProvider = GoogleSignInPresenter.shared
            session.start()
        }

        // Parse the callback URL: mclaude://auth?token=XXX&name=YYY&email=ZZZ
        guard let components = URLComponents(url: callbackURL, resolvingAgainstBaseURL: false) else {
            throw URLError(.badURL)
        }
        let items = components.queryItems ?? []
        let token = items.first { $0.name == "token" }?.value ?? ""
        let name = items.first { $0.name == "name" }?.value ?? ""
        let email = items.first { $0.name == "email" }?.value ?? ""

        guard !token.isEmpty else { throw URLError(.userAuthenticationRequired) }

        await MainActor.run {
            self.authToken = token
            self.userName = name
            self.userEmail = email
        }
    }

    func signOut() {
        TranscriptStore.removeAll()
        authToken = ""
        userName = ""
        userEmail = ""
    }

    func connectWebSocket() {
        wsClient?.disconnect()
        let token = authToken.isEmpty ? nil : authToken
        let config = ServerConfig(host: serverHost, port: serverPort, token: token)

        let capturedClient = client
        let setSessions: @Sendable ([ClaudeSession]) -> Void = { [weak self] sessions in
            guard let self else { return }
            Task { @MainActor [weak self] in
                guard let self else { return }
                // Clear caches for sessions whose sessionId changed (e.g. /clear)
                for session in sessions {
                    if let oldSessionId = self.sessionIdMap[session.id], oldSessionId != session.sessionId {
                        self.eventsCache.removeValue(forKey: session.id)
                        self.transcriptStore.remove(session.id)
                        self.restoredFromDisk.remove(session.id)
                        self.outputCache.removeValue(forKey: session.id)
                        self.loadedLines.removeValue(forKey: session.id)
                    }
                    self.sessionIdMap[session.id] = session.sessionId
                }

                // Keep cached sessions during brief reconnects (don't replace with empty)
                if !sessions.isEmpty || self.sessions.isEmpty {
                    self.sessions = Self.sortedSessions(sessions)
                }
                // An update to the session on screen (app in front) is seen as it arrives.
                if let visible = self.visibleSessionId, UIApplication.shared.applicationState == .active {
                    SeenStore.shared.markSeen(visible)
                }
                self.isConnected = true

                // Refresh skills when session cwds change or every 30s
                let cwds = Set(sessions.map(\.cwd))
                let stale = Date().timeIntervalSince(self.lastSkillsFetch) > 30
                if cwds != self.lastSkillsCwds || stale {
                    self.lastSkillsCwds = cwds
                    self.lastSkillsFetch = Date()
                    Task {
                        if let skills = try? await capturedClient.fetchSkills() {
                            await MainActor.run { self.skills = skills }
                        }
                    }
                }
            }
        }

        let setOutput: @Sendable (String, String) -> Void = { [weak self] id, content in
            guard let self else { return }
            Task { @MainActor [weak self] in
                self?.outputCache[id] = content
                self?.loadedLines[id] = 80
            }
        }

        let setMoreOutput: @Sendable (String, String) -> Void = { [weak self] id, content in
            guard let self else { return }
            Task { @MainActor [weak self] in
                self?.outputCache[id] = content
            }
        }

        let setEvent: @Sendable (String, SessionEvent) -> Void = { [weak self] id, event in
            guard let self else { return }
            Task { @MainActor [weak self] in
                guard let self else { return }
                var events = self.eventsCache[id] ?? []
                // Deduplicate by uuid
                if !events.contains(where: { $0.uuid == event.uuid }) {
                    // Insert in timestamp-sorted position. No cap: older pages loaded with
                    // loadOlderEvents must not be trimmed away by live events.
                    let insertIdx = events.lastIndex { $0.timestamp <= event.timestamp }.map { $0 + 1 } ?? 0
                    events.insert(event, at: insertIdx)
                    self.eventsCache[id] = events
                    self.unsavedTranscripts.insert(id)
                }
            }
        }

        let capturedTelemetry = telemetry
        let setConnection: @Sendable (Bool, String?) -> Void = { [weak self] connected, reason in
            guard let self else { return }
            Task { @MainActor [weak self] in
                let wasConnected = self?.isConnected ?? false
                self?.isConnected = connected
                if wasConnected != connected {   // telemetry: blank-screen evidence
                    TranscriptProbe.shared.note(.connection, ["connected": connected, "reason": reason ?? ""])
                }
                if connected {
                    self?.lastDisconnectReason = nil
                } else {
                    self?.lastDisconnectReason = reason
                    if wasConnected {
                        Task { await capturedTelemetry.report(error: "WebSocket disconnected", context: reason ?? "unknown") }
                    }
                }
            }
        }

        wsClient = WSClient(
            config: config,
            onSessions: setSessions,
            onOutput: setOutput,
            onFullOutput: setMoreOutput,
            onConnectionChange: setConnection,
            onEvent: setEvent
        )
        wsClient?.connect()
    }

    func loadEvents(sessionId: String) async {
        await restoreSavedTranscript(sessionId: sessionId)
        let loadStart = Date()   // telemetry: blank-screen evidence (docs/plan-observability.md)
        Task { @MainActor in
            TranscriptProbe.shared.note(.loads, ["event": "load_start", "session_id": sessionId, "at": loadStart])
        }
        let fetched = try? await client.fetchEvents(id: sessionId)
        let loadEnd = Date()
        let loadMs = Int(loadEnd.timeIntervalSince(loadStart) * 1000)
        Task { @MainActor in
            TranscriptProbe.shared.note(.loads, ["event": "load_end", "session_id": sessionId, "ms": loadMs, "at": loadEnd,
                                                 "ok": fetched != nil, "events": fetched?.count ?? -1])
        }
        // Always fetch latest events from server (merges with any WS events already cached)
        if let freshEvents = fetched {
            var merged = eventsCache[sessionId] ?? []
            if restoredFromDisk.remove(sessionId) != nil {
                // First fresh page over a restored copy (docs/design-claude-uplift.md → Saved transcripts)
                merged = TranscriptStore.merge(restored: merged, fresh: freshEvents)
            }
            var known = Set(merged.map(\.uuid))
            for event in freshEvents where known.insert(event.uuid).inserted {
                merged.append(event)
            }
            // Sort by timestamp (stable, so older pages keep their order). No cap; see setEvent.
            merged = merged.enumerated()
                .sorted { ($0.element.timestamp, $0.offset) < ($1.element.timestamp, $1.offset) }
                .map(\.element)
            eventsCache[sessionId] = merged
            // Loaded with nothing at all (a new chat): there's no older history to page, so the
            // top row shows nothing instead of a spinner (docs/design-claude-uplift.md → History).
            if merged.isEmpty { historyComplete.insert(sessionId) }
            saveTranscript(sessionId: sessionId)
        }
    }

    // MARK: - Saved transcripts (docs/design-claude-uplift.md → Saved transcripts)

    /// A session opening with nothing in memory shows its saved copy right away, before the
    /// events request goes out.
    private func restoreSavedTranscript(sessionId: String) async {
        let shouldTry = await MainActor.run { () -> Bool in
            guard (eventsCache[sessionId] ?? []).isEmpty else { return false }
            return restoreTried.insert(sessionId).inserted
        }
        guard shouldTry else { return }
        let store = transcriptStore
        let started = Date()
        let saved = await Task.detached(priority: .userInitiated) { store.load(sessionId) }.value
        await MainActor.run {
            guard let saved, !saved.events.isEmpty, (eventsCache[sessionId] ?? []).isEmpty else { return }
            if let current = sessions.first(where: { $0.id == sessionId })?.sessionId,
               let savedId = saved.claudeSessionId, savedId != current {
                store.remove(sessionId)   // /clear'ed while the app wasn't running
                return
            }
            eventsCache[sessionId] = saved.events
            restoredFromDisk.insert(sessionId)
            TranscriptProbe.shared.note(.loads, ["event": "disk_restore", "session_id": sessionId,
                                                 "events": saved.events.count,
                                                 "ms": Int(Date().timeIntervalSince(started) * 1000)])
        }
    }

    private func saveTranscript(sessionId: String) {
        unsavedTranscripts.remove(sessionId)
        guard let events = eventsCache[sessionId], !events.isEmpty else { return }
        let store = transcriptStore
        let claudeId = sessions.first { $0.id == sessionId }?.sessionId ?? sessionIdMap[sessionId]
        let newest = Array(events.suffix(TranscriptStore.keep))
        Task.detached(priority: .utility) { store.save(sessionId, claudeSessionId: claudeId, events: newest) }
    }

    /// App going to the background: save every session whose events changed, and prune old files.
    func saveTranscripts() {
        for id in unsavedTranscripts { saveTranscript(sessionId: id) }
        Task.detached(priority: .utility) { TranscriptStore.pruneOld() }
    }

    /// Rename a session (Info screen), then refresh it right away instead of waiting for the
    /// next WebSocket sessions update.
    func renameSession(id: String, name: String) async throws {
        try await client.renameSession(id: id, name: name)
        if let fresh = try? await client.fetchSession(id: id),
           let i = sessions.firstIndex(where: { $0.id == id }) {
            sessions[i] = fresh
        }
    }

    // MARK: - End and resume (docs/design-claude-uplift.md → Drawer, Resume sheet)

    /// End a session. It leaves the list right away; the WebSocket update confirms it.
    /// Restored to the list if the server refuses.
    func endSession(id: String) async throws {
        let removed = sessions.first { $0.id == id }
        sessions.removeAll { $0.id == id }
        do {
            try await client.endSession(id: id)
            transcriptStore.remove(id)
        } catch {
            if let removed, !sessions.contains(where: { $0.id == id }) {
                sessions = Self.sortedSessions(sessions + [removed])
            }
            throw error
        }
    }

    /// Resume a transcript (by Claude session id) as a session and return its id. The session is
    /// fetched into the list right away so it can be selected before the next WebSocket update.
    func resumeTranscript(claudeSessionId: String) async throws -> String {
        let id = try await client.resumeTranscript(id: claudeSessionId)
        if !sessions.contains(where: { $0.id == id }), let fresh = try? await client.fetchSession(id: id),
           !sessions.contains(where: { $0.id == id }) {
            sessions = Self.sortedSessions(sessions + [fresh])
        }
        return id
    }

    // MARK: - Older history (docs/design-claude-uplift.md → History)

    /// Sessions whose history is fully loaded back to the first event.
    var historyComplete: Set<String> = []
    /// Sessions with an older-history request in flight.
    var historyLoading: Set<String> = []

    /// Prepend the next page of older events. Returns false if nothing was added (complete, error,
    /// or already loading).
    @discardableResult
    func loadOlderEvents(sessionId: String, limit: Int = 100) async -> Bool {
        guard !historyComplete.contains(sessionId), !historyLoading.contains(sessionId) else { return false }
        historyLoading.insert(sessionId)
        defer { historyLoading.remove(sessionId) }

        let cached = eventsCache[sessionId] ?? []
        var cursor = cached.first
        var novel: [SessionEvent] = []
        // Pages can overlap the loaded window (live events have different uuids/times than the
        // transcript), so step past pages that were all duplicates.
        for _ in 0..<5 {
            guard let page = try? await client.fetchHistory(id: sessionId, before: cursor, limit: limit) else {
                return false
            }
            novel = Self.dropDuplicates(page.events, of: eventsCache[sessionId] ?? [])
            if !page.hasMore { historyComplete.insert(sessionId) }
            if !novel.isEmpty || !page.hasMore { break }
            guard let first = page.events.first, first.uuid != cursor?.uuid else { break }
            cursor = first
        }
        guard !novel.isEmpty else { return false }
        eventsCache[sessionId] = novel + (eventsCache[sessionId] ?? [])
        unsavedTranscripts.insert(sessionId)
        return true
    }

    /// Load everything back to the start (for exports).
    func loadFullHistory(sessionId: String) async {
        for _ in 0..<200 where !historyComplete.contains(sessionId) {
            let added = await loadOlderEvents(sessionId: sessionId, limit: 500)
            if !added && !historyComplete.contains(sessionId) {
                // In flight elsewhere or failed: wait briefly, then try again once more
                try? await Task.sleep(for: .milliseconds(300))
                if !(await loadOlderEvents(sessionId: sessionId, limit: 500)) { break }
            }
        }
    }

    /// History events not already loaded: same uuid, or the same user/assistant text within a
    /// minute (the live copy of an event can carry a different uuid and the server's receive time).
    static func dropDuplicates(_ page: [SessionEvent], of loaded: [SessionEvent]) -> [SessionEvent] {
        let uuids = Set(loaded.map(\.uuid))
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        func date(_ s: String) -> Date? { iso.date(from: s) ?? ISO8601DateFormatter().date(from: s) }
        // Only the oldest loaded events can overlap a page of older history
        let edge = loaded.prefix(60).compactMap { e -> (SessionEventType, String, Date)? in
            guard e.type == .user || e.type == .text, let t = e.text, let d = date(e.timestamp) else { return nil }
            return (e.type, t.trimmingCharacters(in: .whitespacesAndNewlines), d)
        }
        return page.filter { e in
            if uuids.contains(e.uuid) { return false }
            guard e.type == .user || e.type == .text, let t = e.text, let d = date(e.timestamp) else { return true }
            let text = t.trimmingCharacters(in: .whitespacesAndNewlines)
            return !edge.contains { $0.0 == e.type && $0.1 == text && abs($0.2.timeIntervalSince(d)) < 60 }
        }
    }

    func loadMoreOutput(sessionId: String) {
        let current = loadedLines[sessionId] ?? 80
        let next = current + 80
        loadedLines[sessionId] = next
        wsClient?.requestMoreOutput(id: sessionId, lines: next)
    }

    func disconnectWebSocket() {
        wsClient?.disconnect()
        wsClient = nil
    }

    private func rebuildClient() {
        let token = authToken.isEmpty ? nil : authToken
        let config = ServerConfig(host: serverHost, port: serverPort, token: token)
        client = APIClient(config: config)
        telemetry = TelemetryClient(config: ServerConfig(host: serverHost, port: serverPort))
        transcriptStore = TranscriptStore(host: serverHost, port: serverPort)
        configureObservability()
        connectWebSocket()
    }

    func checkConnection() async {
        isConnected = await client.healthCheck()
    }

    // MARK: - Background Refresh

    func registerBackgroundTask() {
        BGTaskScheduler.shared.register(forTaskWithIdentifier: Self.backgroundTaskId, using: nil) { task in
            guard let task = task as? BGAppRefreshTask else { return }
            self.handleBackgroundRefresh(task: task)
        }
    }

    func scheduleBackgroundRefresh() {
        let request = BGAppRefreshTaskRequest(identifier: Self.backgroundTaskId)
        request.earliestBeginDate = Date(timeIntervalSinceNow: 60)
        try? BGTaskScheduler.shared.submit(request)
    }

    private func handleBackgroundRefresh(task: BGAppRefreshTask) {
        scheduleBackgroundRefresh() // reschedule next refresh

        let fetchTask = Task {
            do {
                let freshSessions = try await client.fetchSessions()
                await MainActor.run {
                    if !freshSessions.isEmpty {
                        self.sessions = Self.sortedSessions(freshSessions)
                    }
                }
                task.setTaskCompleted(success: true)
            } catch {
                task.setTaskCompleted(success: false)
            }
        }

        task.expirationHandler = { fetchTask.cancel() }
    }

    /// Oldest first (creation order), id as a stable tie-breaker.
    static func sortedSessions(_ sessions: [ClaudeSession]) -> [ClaudeSession] {
        sessions.sorted { ($0.startedAt, $0.id) < ($1.startedAt, $1.id) }
    }
}

// MARK: - ASWebAuthenticationSession presenter

final class GoogleSignInPresenter: NSObject, ASWebAuthenticationPresentationContextProviding, @unchecked Sendable {
    static let shared = GoogleSignInPresenter()
    func presentationAnchor(for session: ASWebAuthenticationSession) -> ASPresentationAnchor {
        UIApplication.shared.connectedScenes
            .compactMap { $0 as? UIWindowScene }
            .flatMap(\.windows)
            .first { $0.isKeyWindow } ?? ASPresentationAnchor()
    }
}
