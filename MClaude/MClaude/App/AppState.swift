import SwiftUI
import BackgroundTasks
import AuthenticationServices

@Observable
final class AppState {
    static let backgroundTaskId = "io.mclaude.app.refresh"
    var serverHost: String {
        didSet { UserDefaults.standard.set(serverHost, forKey: "serverHost"); rebuildClient() }
    }
    var serverPort: Int {
        didSet { UserDefaults.standard.set(serverPort, forKey: "serverPort"); rebuildClient() }
    }
    var authToken: String {
        didSet { UserDefaults.standard.set(authToken, forKey: "authToken"); rebuildClient() }
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

    // Skills autocomplete
    var skills: [SkillSuggestion] = []
    private var lastSkillsCwds: Set<String> = []
    private var lastSkillsFetch: Date = .distantPast

    init() {
        let host = UserDefaults.standard.string(forKey: "serverHost") ?? "127.0.0.1"
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
                        self.outputCache.removeValue(forKey: session.id)
                        self.loadedLines.removeValue(forKey: session.id)
                    }
                    self.sessionIdMap[session.id] = session.sessionId
                }

                // Keep cached sessions during brief reconnects (don't replace with empty)
                if !sessions.isEmpty || self.sessions.isEmpty {
                    self.sessions = sessions.sorted { $0.tmuxWindow < $1.tmuxWindow }
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
                    // Insert in timestamp-sorted position
                    let insertIdx = events.firstIndex { $0.timestamp > event.timestamp } ?? events.endIndex
                    events.insert(event, at: insertIdx)
                    // Cap at 200 events per session
                    if events.count > 200 {
                        events = Array(events.suffix(200))
                    }
                    self.eventsCache[id] = events
                }
            }
        }

        let capturedTelemetry = telemetry
        let setConnection: @Sendable (Bool, String?) -> Void = { [weak self] connected, reason in
            guard let self else { return }
            Task { @MainActor [weak self] in
                let wasConnected = self?.isConnected ?? false
                self?.isConnected = connected
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
        // Always fetch latest events from server (merges with any WS events already cached)
        if let freshEvents = try? await client.fetchEvents(id: sessionId) {
            var merged = eventsCache[sessionId] ?? []
            for event in freshEvents {
                if !merged.contains(where: { $0.uuid == event.uuid }) {
                    merged.append(event)
                }
            }
            // Sort by timestamp
            merged.sort { $0.timestamp < $1.timestamp }
            if merged.count > 200 {
                merged = Array(merged.suffix(200))
            }
            eventsCache[sessionId] = merged
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
                        self.sessions = freshSessions.sorted { $0.tmuxWindow < $1.tmuxWindow }
                    }
                }
                task.setTaskCompleted(success: true)
            } catch {
                task.setTaskCompleted(success: false)
            }
        }

        task.expirationHandler = { fetchTask.cancel() }
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
