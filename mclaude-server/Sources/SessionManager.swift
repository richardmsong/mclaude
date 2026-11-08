import Foundation

/// Persisted identity of a server-owned session. Everything else is rebuilt at runtime.
struct ManagedSessionRecord: Codable, Sendable {
    let id: String               // display id used by the API and ownership store ("s12")
    let index: Int               // stable ordering (creation order)
    let claudeSessionId: String  // passed to --session-id / --resume
    let cwd: String
    var windowName: String       // display label; persisted key kept for state-file compatibility
    let createdAt: Date
    var oauthToken: String?
    var hasConversation: Bool    // true once a user message was sent, so --resume will find the transcript
    var turnInProgress: Bool?    // written as turns start/end; true at startup = the server died mid-turn
    var usage: SessionUsage?     // token/context/cost totals (nil for sessions that predate tracking)
    /// Resumed from an In use transcript: no process until its first message (it may be live in a terminal)
    var startOnFirstMessage: Bool?
    /// Latest recap (docs/plan-watch-app.md → Recap); nil until the first one is made
    var recap: SessionRecap?
}

private struct ManagedSessionsFile: Codable {
    var nextIndex: Int
    var sessions: [ManagedSessionRecord]
    /// Context windows the CLI reported, by canonical model ("claude-opus-5-5": 1000000)
    var modelWindows: [String: Int]?
    /// Claude session id -> name of a session that was ended, for the Resume sheet
    var endedNames: [String: String]?
}

/// A `can_use_tool` request Claude is waiting on.
private struct PendingRequest {
    enum Kind { case question, plan, tool }
    let requestId: String
    let kind: Kind
    let toolName: String
    let inputJSON: Data
    let questions: [(question: String, options: [String], multiSelect: Bool)]
    let planText: String?
}

/// Runtime state of one session (actor-isolated inside SessionManager).
private final class LiveSession {
    var record: ManagedSessionRecord
    var process: ClaudeProcess?
    var generation = 0                   // bumps on every launch; stale processes' output is ignored
    var status: SessionStatus = .idle
    var statusSince = Date()
    var turnActive = false
    var pending: PendingRequest?
    var selectedOptions: [Int] = []      // option numbers typed while a question is pending
    var submitRequested = false          // Enter seen for the pending question
    var submitToken = 0                  // debounce: only the latest scheduled submit may fire
    var draft = ""                       // text sent without Enter, flushed on Enter
    var events: [SessionEvent] = []
    var transcript: [String] = []
    var subagents: [String: SubagentInfo] = [:]   // Agent tool_use id -> info
    var shownTaskNotices: [String: Int] = [:]    // task id -> cards shown from stdout system lines, not yet replayed
    var foregroundTasks: Set<String> = []          // task ids started in the foreground (no notification card)
    var lastActivity = Date()
    var crashTimes: [Date] = []
    var ready = false                    // the current process has reported in (init or any output)
    var restartPending = false           // exited unexpectedly or failed to launch; a restart is scheduled
    var stopping = false                 // deliberate stop: exit is not a crash
    var deleted = false
    var lastEnd = ""                     // why the last process ended, shown in the transcript
    /// Messages sent while a turn is running. Held here (not written to Claude's stdin) so they
    /// stay editable, then delivered together at the next step boundary or when the turn ends.
    var queue: [QueuedMessage] = []
    var lastAssistantText = ""            // latest main-thread reply text (push notification body)
    var compacting = false                // /compact (or auto-compact) is running
    var blockPushedThisTurn = false       // an auto-mode "Blocked" push went out this turn
    /// compact_boundary seen this turn; its marker is emitted at the turn's result, once the
    /// summary is in the transcript.
    var pendingCompaction: (uuid: String, json: [String: Any], timestamp: String)?
    /// Summary text, when the CLI also sends it on stdout (it does on resumed sessions).
    var pendingCompactionSummary: String?
    /// Peer SendMessage tool_uses waiting for their tool_result (tool_use id -> send)
    var peerSends: [String: PendingPeerSend] = [:]
    /// User MCP config the current process started with (docs/plan-mcp-servers.md → staleSessions)
    var mcpConfigFingerprint: String?
    /// Restart requested while a turn was running: restart when it ends (docs/plan-mcp-servers.md)
    var restartAfterTurn = false
    /// Recap runs: at most one at a time, plus one coalesced re-run (docs/plan-watch-app.md → Recap)
    var recapRunning = false
    var recapRerun = false
    var firstPrompt: String?             // first real prompt, read from the transcript once

    init(record: ManagedSessionRecord) { self.record = record }
}

/// Owns every Claude Code process the server runs. Port of mclaude v2's session agent:
/// stream-json driver, explicit state machine, crash watcher with backoff, resume from a
/// persisted session id, idle reaping, and a graceful drain on shutdown.
actor SessionManager {
    private let stateFile: String
    nonisolated let claudePath: String
    private let history: JSONLTailer
    /// Peer messages seen in managed sessions (docs/plan-peer-messages.md)
    nonisolated let peerStore: PeerStore
    private let peers: PeerObserver
    nonisolated var peerRegistryDir: String { peers.registryDir }
    private var sessions: [String: LiveSession] = [:]
    private var nextIndex = 1
    private var queueCounter = 0
    private var modelWindows: [String: Int] = [:]   // learned from CLI results
    private var endedNames: [String: String] = [:]  // Claude session id -> name, for ended sessions
    /// Open Claude session ids and ended names, readable off this actor (GET /transcripts)
    nonisolated let resumeState = ResumeState()
    private var push: PushNotifier?

    func setPushNotifier(_ p: PushNotifier) { push = p }

    private var onSessionsUpdated: (@Sendable ([ClaudeSession]) -> Void)?
    private var onOutputChanged: (@Sendable (String, String) -> Void)?
    private var onEvent: (@Sendable (String, SessionEvent) -> Void)?
    private var onStatusChange: (@Sendable (ClaudeSession, SessionStatus, SessionStatus) -> Void)?
    private var outputFlushScheduled: Set<String> = []
    private var draining = false
    /// Open `claude.turn` span per session id (docs/plan-observability.md → Turn spans)  // telemetry
    private var turnSpans: [String: Telemetry.Span] = [:]  // telemetry
    /// The HTTP request whose input is being routed right now: parent of the turn it starts  // telemetry
    private var inputRequest: Telemetry.SpanContext?  // telemetry

    static let maxEvents = 500
    static let maxTranscriptLines = 4000
    static let crashRetryInterval: TimeInterval = 600
    static let crashWindow: TimeInterval = 300
    static let maxCrashesInWindow = 5

    init(stateDir: String = "\(NSHomeDirectory())/.mclaude", registryDir: String = PeerRegistry.defaultDir) {
        try? FileManager.default.createDirectory(atPath: stateDir, withIntermediateDirectories: true)
        stateFile = "\(stateDir)/managed-sessions.json"
        history = JSONLTailer()
        peerStore = PeerStore(path: "\(stateDir)/peer-messages.jsonl")
        peers = PeerObserver(store: peerStore, registryDir: registryDir)
        claudePath = SessionManager.resolveClaude()
    }

    private static func resolveClaude() -> String {
        let (_, which) = Shell.syncExec("/usr/bin/which", arguments: ["claude"])
        let resolved = which.trimmingCharacters(in: .whitespacesAndNewlines)
        if !resolved.isEmpty { return resolved }
        let candidates = ["\(NSHomeDirectory())/.local/bin/claude", "/opt/homebrew/bin/claude", "/usr/local/bin/claude"]
        return candidates.first { FileManager.default.fileExists(atPath: $0) } ?? "claude"
    }

    func setOnSessionsUpdated(_ h: @escaping @Sendable ([ClaudeSession]) -> Void) { onSessionsUpdated = h }
    func setOnOutputChanged(_ h: @escaping @Sendable (String, String) -> Void) { onOutputChanged = h }
    func setOnEvent(_ h: @escaping @Sendable (String, SessionEvent) -> Void) { onEvent = h }
    func setOnStatusChange(_ h: @escaping @Sendable (ClaudeSession, SessionStatus, SessionStatus) -> Void) { onStatusChange = h }

    // MARK: - Startup and persistence

    /// Load persisted sessions. Processes start lazily: a session gets a Claude process the first
    /// time it receives input (resumed from its transcript), so a restart never spawns a crowd of
    /// idle processes and an unhealthy session can't stop the server from coming up.
    func start() async {
        if let data = FileManager.default.contents(atPath: stateFile),
           let file = try? JSONDecoder.iso.decode(ManagedSessionsFile.self, from: data) {
            nextIndex = file.nextIndex
            modelWindows = file.modelWindows ?? [:]
            endedNames = file.endedNames ?? [:]
            for record in file.sessions {
                let live = LiveSession(record: record)
                live.statusSince = record.createdAt
                if live.record.usage == nil, record.hasConversation {
                    // Sessions from before usage tracking: rebuild tokens/context from the transcript
                    let path = ClaudePaths.transcript(cwd: record.cwd, sessionId: record.claudeSessionId)
                    live.record.usage = SessionUsage.backfill(transcriptPath: path)
                }
                live.record.usage?.refreshContextWindow(known: modelWindows)
                sessions[record.id] = live
                // Rebuild the visible history from Claude's own transcript on disk
                let past = peers.enrichHistory(
                    await history.getRecentEvents(id: record.id, sessionId: record.claudeSessionId, cwd: record.cwd, count: 200),
                    observing: record.claudeSessionId)
                for event in past { append(event, to: live, broadcast: false) }
                if let last = past.last(where: { $0.type == .system }), let date = Date.fromISO(last.timestamp) {
                    live.statusSince = date
                }
            }
            print("[sessions] Loaded \(file.sessions.count) managed sessions")
            await reapOrphans()
        }
        publishSessions()
        // Every open session keeps a running process (docs/design-claude-uplift.md → Session
        // processes), so a restart is invisible: start them all, 1 s apart, in session order.
        let order = sessions.values.sorted(by: { $0.record.index < $1.record.index }).map(\.record.id)
        Task {
            for (i, id) in order.enumerated() {
                if i > 0 { try? await Task.sleep(nanoseconds: 1_000_000_000) }
                await self.startAtBoot(id: id)
            }
        }
    }

    /// The message a mid-turn session gets after a server restart. It's hidden from the
    /// transcript (live and history): the restart must not show.
    static let restartResumeText = "The previous turn was interrupted by a server restart. Continue where you left off."

    private func startAtBoot(id: String) {
        guard let live = sessions[id], live.process == nil, !live.deleted, !draining,
              live.record.startOnFirstMessage != true else { return }
        let midTurn = live.record.turnInProgress == true && live.record.hasConversation
        guard launch(live) else { scheduleRestart(live, resumeTurn: midTurn); return }
        if midTurn {
            print("[sessions] \(live.record.id): was mid-turn when the server stopped; resuming")
            let turn = startTurnSpan(live, trigger: "server_restart")  // telemetry
            live.process?.sendUserMessage(uuid: UUID().uuidString.lowercased(), content: SessionManager.restartResumeText, traceparent: turn?.traceparent)  // telemetry: + traceparent
            setTurn(live, true)
            setStatus(live, .working)
        }
    }

    /// If the previous server died hard, its Claude children outlive it (reparented to launchd)
    /// and would race a resumed process on the same transcript. Stop them before resuming.
    private func reapOrphans() async {
        let ids = Set(sessions.values.map(\.record.claudeSessionId))
        let (_, ps) = Shell.syncExec("/bin/ps", arguments: ["-eo", "pid=,args="])
        var orphans: [Int32] = []
        for line in ps.split(separator: "\n") {
            let parts = line.trimmingCharacters(in: .whitespaces).split(separator: " ", maxSplits: 1)
            guard parts.count == 2, let pid = Int32(parts[0]), pid != getpid() else { continue }
            let args = String(parts[1])
            guard args.contains("--permission-prompt-tool stdio"),
                  ids.contains(where: { args.contains($0) }) else { continue }
            orphans.append(pid)
        }
        guard !orphans.isEmpty else { return }
        print("[sessions] Stopping \(orphans.count) orphaned claude process(es) from a previous run: \(orphans)")
        for pid in orphans { kill(pid, SIGTERM) }
        try? await Task.sleep(nanoseconds: 3_000_000_000)
        for pid in orphans where kill(pid, 0) == 0 { kill(pid, SIGKILL) }
    }

    private func persist() {
        let file = ManagedSessionsFile(nextIndex: nextIndex,
                                       sessions: sessions.values.map(\.record).sorted { $0.index < $1.index },
                                       modelWindows: modelWindows,
                                       endedNames: endedNames.isEmpty ? nil : endedNames)
        guard let data = try? JSONEncoder.iso.encode(file) else { return }
        let tmp = stateFile + ".tmp"
        FileManager.default.createFile(atPath: tmp, contents: data, attributes: [.posixPermissions: 0o600])
        if rename(tmp, stateFile) != 0 { print("[sessions] WARNING: failed to persist \(stateFile)") }
    }

    // MARK: - API surface

    func createSession(cwd: String, token: String?, name: String?) async -> String? {
        guard !draining else { return nil }
        var isDir: ObjCBool = false
        guard FileManager.default.fileExists(atPath: cwd, isDirectory: &isDir), isDir.boolValue else {
            print("[sessions] create refused: \(cwd) is not a directory")
            return nil
        }
        let index = nextIndex
        nextIndex += 1
        let record = ManagedSessionRecord(
            id: "s\(index)", index: index, claudeSessionId: UUID().uuidString.lowercased(), cwd: cwd,
            windowName: name ?? (cwd as NSString).lastPathComponent, createdAt: Date(),
            oauthToken: token, hasConversation: false)
        let live = LiveSession(record: record)
        sessions[record.id] = live
        persist()
        guard launch(live) else {
            sessions.removeValue(forKey: record.id)
            persist()
            return nil
        }
        publishSessions()
        print("[sessions] Created \(record.id) in \(cwd) (claude session \(record.claudeSessionId))")
        return record.id
    }

    enum ResumeOutcome: Sendable, Equatable {
        case resumed(String), alreadyOpen(String), directoryMissing, unavailable
    }

    /// Register an existing Claude transcript as a session (Resume sheet). Claude starts right
    /// away with --resume, unless the transcript is In use (maybe live in a terminal): then it
    /// waits for the first message.
    func resumeTranscript(_ t: TranscriptSummary, token: String?) async -> ResumeOutcome {
        if let open = sessions.values.first(where: { $0.record.claudeSessionId == t.id }) {
            return .alreadyOpen(open.record.id)
        }
        guard !draining else { return .unavailable }
        var isDir: ObjCBool = false
        guard FileManager.default.fileExists(atPath: t.cwd, isDirectory: &isDir), isDir.boolValue else {
            return .directoryMissing
        }
        let index = nextIndex
        nextIndex += 1
        let projectName = (t.cwd as NSString).lastPathComponent
        // Same rule as the Resume sheet's In use tag: maybe still live in a terminal
        let inUse = Date().timeIntervalSince(t.modifiedAt) < TranscriptIndex.inUseWindow
        let name = endedNames[t.id] ?? t.aiTitle ?? t.firstPrompt.map { String($0.prefix(60)) } ?? projectName
        let record = ManagedSessionRecord(
            id: "s\(index)", index: index, claudeSessionId: t.id, cwd: t.cwd, windowName: name,
            createdAt: Date(), oauthToken: token, hasConversation: true,
            startOnFirstMessage: inUse ? true : nil)
        let live = LiveSession(record: record)
        live.statusSince = t.modifiedAt
        live.lastActivity = t.modifiedAt
        // Registered before any await, so a concurrent resume of the same transcript sees it open
        sessions[record.id] = live
        endedNames.removeValue(forKey: t.id)
        let path = t.path
        live.record.usage = await Task.detached { SessionUsage.backfill(transcriptPath: path) }.value
        live.record.usage?.refreshContextWindow(known: modelWindows)
        // Rebuild the visible history from the transcript, as start() does
        let past = peers.enrichHistory(await history.getRecentEvents(id: record.id, sessionId: t.id, cwd: t.cwd, count: 200),
                                       observing: t.id)
        for event in past { append(event, to: live, broadcast: false) }
        if let last = past.last, let date = Date.fromISO(last.timestamp) {
            live.statusSince = date
            live.lastActivity = date
        }
        guard sessions[record.id] === live else { return .unavailable }   // ended while loading
        persist()
        if !inUse, !launch(live) { scheduleRestart(live, resumeTurn: false) }
        publishSessions()
        print("[sessions] Resumed \(record.id) from transcript \(t.id) in \(t.cwd)")
        return .resumed(record.id)
    }

    func getCachedSessions() -> [ClaudeSession] {
        sessions.values.sorted { $0.record.index < $1.record.index }.map(snapshot)
    }

    func getSession(id: String) -> ClaudeSession? { sessions[id].map(snapshot) }

    /// A turn that ran past the threshold (default 5 min) ended: push its last reply to the phone.
    /// Interrupted turns are skipped (the user is clearly already looking).
    private func notifyIfLongTurn(_ live: LiveSession, result: [String: Any]) {
        guard let push, let ms = result["duration_ms"] as? Int,
              result["subtype"] as? String != "error_during_execution" else { return }
        let title = live.record.windowName
        let reply = live.lastAssistantText
        let sessionId = live.record.id
        let minutes = ms / 60_000
        Task {
            guard ms >= (await push.minTurnSeconds) * 1000 else { return }
            let body = reply.isEmpty ? "Finished after \(minutes) min" : PushNotifier.summarize(reply)
            await push.notify(title: title, body: body, sessionId: sessionId, userId: nil)
        }
    }

    /// Remember the context window the CLI reports per model and apply it to every session on it.
    private func learnContextWindows(from result: [String: Any]) {
        guard let perModel = (result["model_usage_since_last_result"] ?? result["modelUsage"]) as? [String: [String: Any]] else { return }
        var changed = false
        for (name, m) in perModel {
            guard let w = m["contextWindow"] as? Int else { continue }
            let key = (m["canonicalModel"] as? String) ?? name
            if modelWindows[key] != w { modelWindows[key] = w; changed = true }
        }
        guard changed else { return }
        for live in sessions.values { live.record.usage?.refreshContextWindow(known: modelWindows) }
        publishSessions()
    }

    func getUsage(id: String) -> SessionUsage? {
        guard let live = sessions[id] else { return nil }
        return live.record.usage ?? SessionUsage()
    }

    /// cwd -> most recent session activity there (for the project picker's Recent section).
    func lastUsedByCwd() -> [String: Date] {
        var result: [String: Date] = [:]
        for live in sessions.values {
            let used = max(live.record.createdAt, live.lastActivity, live.statusSince)
            result[live.record.cwd] = max(result[live.record.cwd] ?? .distantPast, used)
        }
        return result
    }

    func getCachedOutput(id: String) -> String? {
        guard let live = sessions[id] else { return nil }
        return live.transcript.suffix(200).joined(separator: "\n")
    }

    func getMoreOutput(id: String, lines: Int) -> String? {
        guard let live = sessions[id] else { return nil }
        return live.transcript.suffix(max(lines, 200)).joined(separator: "\n")
    }

    /// Older history straight from Claude's transcript on disk (the in-memory buffer is capped).
    func historyPage(id: String, before: String?, beforeTimestamp: String?,
                     limit: Int) async -> (events: [SessionEvent], hasMore: Bool)? {
        guard let live = sessions[id] else { return nil }
        let page = await history.historyPage(sessionId: live.record.claudeSessionId, cwd: live.record.cwd,
                                             before: before, beforeTimestamp: beforeTimestamp, limit: limit)
        return (peers.enrichHistory(page.events, observing: live.record.claudeSessionId), page.hasMore)
    }

    /// Claude session UUID of a session this server runs (nil for k8s or unknown ids).
    func claudeSessionId(id: String) -> String? { sessions[id]?.record.claudeSessionId }

    /// Open sessions and ended names, for filling in PeerRefs.
    func peerDirectory() -> PeerDirectory {
        var open: [String: PeerDirectory.OpenSession] = [:]
        for live in sessions.values { open[live.record.claudeSessionId] = .init(id: live.record.id, name: live.record.windowName) }
        return PeerDirectory(open: open, ended: endedNames)
    }

    func recentEvents(id: String, count: Int = 200) -> [SessionEvent]? {
        guard let live = sessions[id] else { return nil }
        return Array(live.events.suffix(count))
    }

    func pendingPlan(id: String) -> String? {
        guard let p = sessions[id]?.pending, p.kind == .plan else { return nil }
        return p.planText
    }

    /// Input from the app. The app still speaks "terminal": text + Enter, number keys and Enter
    /// for AskUserQuestion, Escape to cancel. Translate that into stream-json actions.
    func sendInput(id: String, text: String, sendEnter: Bool) async -> Bool {
        await sendInputOutcome(id: id, text: text, sendEnter: sendEnter).ok
    }

    /// Like sendInput, but says whether the message went to Claude now or into the queue.
    func sendInputOutcome(id: String, text: String, sendEnter: Bool, automated: Bool = false) async -> SendOutcome {
        guard let live = sessions[id] else { return .failed }
        // Automated input (the Ops dispatcher) is always a message, queued while Claude is busy, and
        // never taken as the answer to a pending question, plan or tool prompt.
        if automated {
            inputRequest = Telemetry.currentRequest  // telemetry
            defer { inputRequest = nil }  // telemetry
            live.lastActivity = Date()
            return sendMessage(text, to: live)
        }
        live.lastActivity = Date()
        inputRequest = Telemetry.currentRequest  // telemetry
        defer { inputRequest = nil }  // telemetry

        // A typed message while a question is pending is queued, not taken as the answer: the
        // question is answered on its card, and the queue is delivered once Claude moves on.
        if let pending = live.pending, !(pending.kind == .question && sendEnter) {
            defer { inputRouted(live, pending.kind != .question && sendEnter ? "feedback" : "answer", ["mclaude.prompt.kind": "\(pending.kind)", "mclaude.input.key": !sendEnter]) }  // telemetry
            guard answer(pending, in: live, text: text, sendEnter: sendEnter) else { return .failed }
            // Typed text (Enter) is the answer itself; keys (option numbers, Enter, Escape) just act
            return sendEnter ? .answered : .sent
        }

        if !sendEnter {
            return handleKey(text, in: live) ? .sent : .failed
        }

        let message = live.draft + text
        live.draft = ""
        return sendMessage(message, to: live)
    }

    private func handleKey(_ text: String, in live: LiveSession) -> Bool {
        do {
            switch text {
            case "Escape", "C-c":
                if live.turnActive { live.process?.interrupt() }
                return true
            case "SendNow":
                return flushNow(live)
            case "Enter":
                let message = live.draft
                live.draft = ""
                return message.isEmpty ? true : sendMessage(message, to: live).ok
            case "Tab", "BTab", "Up", "Down", "Left", "Right", "S-Up", "S-Down", "S-Left", "S-Right":
                return true                      // terminal navigation has no meaning headless
            default:
                if text.hasPrefix("C-") { return true }
                live.draft += text               // typed without Enter: hold until Enter
                return true
            }
        }
    }

    enum AnswerOutcome: Equatable { case answered, noPendingQuestion, missingAnswer, failed }

    /// Answer the pending AskUserQuestion with one answer per question (option labels joined
    /// with ", " for multi-select, or free "Other" text). Keys are the question texts.
    func answerQuestion(id: String, answers: [String: String]) -> AnswerOutcome {
        guard let live = sessions[id] else { return .failed }
        guard let pending = live.pending, pending.kind == .question else { return .noPendingQuestion }
        guard let process = live.process else { return .failed }
        var cleaned: [String: String] = [:]
        for q in pending.questions {
            let a = answers[q.question]?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            guard !a.isEmpty else { return .missingAnswer }
            cleaned[q.question] = a
        }
        live.lastActivity = Date()
        live.draft = ""
        var input = (try? JSONSerialization.jsonObject(with: pending.inputJSON)) as? [String: Any] ?? [:]
        input["answers"] = cleaned
        resolvePending(live, process: process, allow: true, updatedInput: input, message: nil)
        inputRouted(live, "answer", ["mclaude.prompt.kind": "question", "mclaude.input.key": false])  // telemetry
        return .answered
    }

    /// "Approve" in the app = press Enter at the prompt.
    func approve(id: String) -> Bool {
        guard let live = sessions[id] else { return false }
        guard let pending = live.pending else { return true }
        return answer(pending, in: live, text: "Enter", sendEnter: false)
    }

    /// "Cancel" = Escape: decline a pending prompt, or interrupt the running turn.
    func cancel(id: String) -> Bool {
        guard let live = sessions[id] else { return false }
        if let pending = live.pending { return answer(pending, in: live, text: "Escape", sendEnter: false) }
        if live.turnActive { live.process?.interrupt() }
        return true
    }

    func deleteSession(id: String) -> Bool {
        guard let live = sessions.removeValue(forKey: id) else { return false }
        // Remember the name so the Resume sheet can show it (the transcript stays on disk)
        if live.record.hasConversation { endedNames[live.record.claudeSessionId] = live.record.windowName }
        live.deleted = true
        live.stopping = true
        live.process?.stop()
        endTurnSpan(id, outcome: "deleted")  // telemetry
        persist()
        publishSessions()
        print("[sessions] Deleted \(id)")
        return true
    }

    // MARK: - Messages

    private func sendMessage(_ raw: String, to live: LiveSession) -> SendOutcome {
        let message = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !message.isEmpty else { return .sent }
        if message == "/exit" || message == "/quit" { return deleteSession(id: live.record.id) ? .sent : .failed }
        if (live.turnActive || live.pending != nil), live.process != nil {
            // Claude is mid-turn or waiting on a prompt: hold it (editable) and deliver at the next step
            queueCounter += 1
            let item = QueuedMessage(id: "q\(queueCounter)", text: message, queuedAt: Date())
            live.queue.append(item)
            publishSessions()
            inputRouted(live, "queued", ["mclaude.queue_id": item.id])  // telemetry
            return .queued(item.id)
        }
        inputRouted(live, "message")  // telemetry
        return deliver(message, to: live) ? .sent : .failed
    }

    /// Write a message to Claude's stdin now.
    private func deliver(_ message: String, to live: LiveSession) -> Bool {
        guard ensureRunning(live) else { return false }
        if live.record.startOnFirstMessage == true {
            live.record.startOnFirstMessage = nil
            persist()
        }
        if !live.record.hasConversation {
            live.record.hasConversation = true
            persist()
            inferTitleIfDefault(live, firstMessage: message)
        }
        let turn = startTurnSpan(live, trigger: inputRequest != nil ? "input" : "queue")  // telemetry
        live.process?.sendUserMessage(uuid: UUID().uuidString.lowercased(), content: SessionManager.content(for: message), traceparent: turn?.traceparent)  // telemetry: + traceparent
        // Claude echoes the message back (--replay-user-messages) when it actually takes it;
        // the turn starts then. Show "working" now so the app reacts immediately.
        setTurn(live, true)
        setStatus(live, .working)
        return true
    }

    /// Deliver the whole queue as ONE message. Called at a step boundary (Claude reads stdin
    /// before its next model call) and when a turn ends (it becomes the next turn).
    private func flushQueue(_ live: LiveSession, reason: String) {
        guard let first = live.queue.first else { return }
        // A slash command (e.g. /compact) is its own turn: never joined with other messages (it
        // would swallow them as arguments, or stop being a command) and never slipped in
        // mid-turn. Later messages wait for the next turn end.
        let isCommand: (QueuedMessage) -> Bool = { Self.isSlashCommand($0.text) }
        if isCommand(first), reason == "step boundary" { return }
        let batch = isCommand(first) ? [first] : Array(live.queue.prefix { !isCommand($0) })
        let text = batch.map(\.text).joined(separator: "\n\n")
        let count = batch.count
        live.queue.removeFirst(count)
        print("[sessions] \(live.record.id): delivering \(count) queued message(s) (\(reason))")
        _ = deliver(text, to: live)
        publishSessions()
    }

    /// "Send now": deliver the queue immediately. Mid-turn that means interrupting the current
    /// step; the queue is delivered as soon as the interrupted turn ends.
    private func flushNow(_ live: LiveSession) -> Bool {
        if live.turnActive, live.process != nil {
            live.process?.interrupt()
        } else {
            flushQueue(live, reason: "send now")
        }
        return true
    }

    func flushQueue(id: String) -> Bool {
        guard let live = sessions[id] else { return false }
        return flushNow(live)
    }

    static let maxNameLength = 500

    /// A user-set name: trimmed, whitespace runs (newlines included) collapsed, capped at 500.
    /// Nil when nothing is left.
    nonisolated static func normalizedName(_ raw: String) -> String? {
        let collapsed = raw.split(whereSeparator: { $0.isWhitespace }).joined(separator: " ")
        guard !collapsed.isEmpty else { return nil }
        return String(collapsed.prefix(maxNameLength))
    }

    /// Rename from the app's Info screen. Returns the stored name, or nil for an unknown session.
    /// An in-flight title inference won't overwrite it (it only applies over the default name).
    func setName(id: String, name: String) -> String? {
        guard let live = sessions[id] else { return nil }
        live.record.windowName = name
        persist()
        publishSessions()
        print("[sessions] \(id): renamed \"\(name)\"")
        return name
    }

    func editQueued(id: String, queueId: String, text: String) -> Bool {
        guard let live = sessions[id], let i = live.queue.firstIndex(where: { $0.id == queueId }) else { return false }
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty { live.queue.remove(at: i) } else { live.queue[i].text = trimmed }
        publishSessions()
        return true
    }

    func deleteQueued(id: String, queueId: String) -> Bool {
        guard let live = sessions[id], let i = live.queue.firstIndex(where: { $0.id == queueId }) else { return false }
        live.queue.remove(at: i)
        publishSessions()
        return true
    }

    /// First prompt of a session: replace the default name (cwd basename) with an inferred title.
    /// Runs in the background; a name the user set (create with "name") is never overwritten.
    private func inferTitleIfDefault(_ live: LiveSession, firstMessage: String) {
        let defaultName = (live.record.cwd as NSString).lastPathComponent
        guard live.record.windowName == defaultName,
              SessionTitler.shouldInferTitle(fromFirstMessage: firstMessage) else { return }
        let id = live.record.id, path = claudePath, token = live.record.oauthToken
        Task {
            guard let title = await SessionTitler.infer(from: firstMessage, claudePath: path, oauthToken: token) else { return }
            self.applyInferredTitle(id: id, title: title, ifStill: defaultName)
        }
    }

    private func applyInferredTitle(id: String, title: String, ifStill defaultName: String) {
        guard let live = sessions[id], live.record.windowName == defaultName else { return }
        live.record.windowName = title
        persist()
        publishSessions()
        print("[sessions] \(id): titled \"\(title)\"")
    }

    /// Image paths in a message (uploaded screenshots) become real image blocks, like pasting
    /// an image into the terminal; the path stays in the text so Claude can also Read it.
    static func content(for message: String) -> Any {
        let tokens = message.split(whereSeparator: { $0 == " " || $0 == "\n" }).map(String.init)
        let exts = ["png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp"]
        var blocks: [[String: Any]] = []
        for token in tokens where token.hasPrefix("/") {
            let ext = (token as NSString).pathExtension.lowercased()
            guard let mediaType = exts[ext],
                  let data = FileManager.default.contents(atPath: token),
                  data.count <= 3_700_000 else { continue }     // API limit is ~5MB base64
            blocks.append(["type": "image", "source": ["type": "base64", "media_type": mediaType, "data": data.base64EncodedString()]])
        }
        guard !blocks.isEmpty else { return message }
        blocks.append(["type": "text", "text": message])
        return blocks
    }

    // MARK: - Prompts (can_use_tool)

    private func answer(_ pending: PendingRequest, in live: LiveSession, text: String, sendEnter: Bool) -> Bool {
        guard let process = live.process else { return false }
        let input = (try? JSONSerialization.jsonObject(with: pending.inputJSON)) as? [String: Any] ?? [:]
        let declineKeys: Set<String> = ["Escape", "C-c", "n", "N", "no"]

        func resolve(allow: Bool, updatedInput: [String: Any]? = nil, message: String? = nil) -> Bool {
            resolvePending(live, process: process, allow: allow, updatedInput: updatedInput ?? input, message: message)
            return true
        }

        switch pending.kind {
        case .question:
            if !sendEnter, declineKeys.contains(text) {
                return resolve(allow: false, message: "The user dismissed the question without answering.")
            }
            // The app sends each option number and the final Enter as separate, concurrent
            // requests, so they can arrive out of order: submit after a short quiet period.
            if !sendEnter, let n = Int(text) {             // option number, global across questions
                live.selectedOptions.append(n)
                if live.submitRequested { scheduleQuestionSubmit(live, requestId: pending.requestId) }
                return true
            }
            if !sendEnter, text == "Enter" {
                live.submitRequested = true
                scheduleQuestionSubmit(live, requestId: pending.requestId)
                return true
            }
            // Typed text never reaches here: it's queued (see sendInputOutcome). Free-text
            // answers go through answerQuestion (the card's Other).
            return true

        case .plan, .tool:
            if !sendEnter, ["Enter", "1", "2", "y", "Y", "yes"].contains(text) {
                return resolve(allow: true)
            }
            if !sendEnter, declineKeys.contains(text) || text == "3" {
                return resolve(allow: false, message: pending.kind == .plan
                               ? "The user rejected the plan. Keep planning; do not start implementing."
                               : "The user denied this tool call.")
            }
            if sendEnter {                                 // feedback instead of a yes/no
                let feedback = (live.draft + text).trimmingCharacters(in: .whitespacesAndNewlines)
                live.draft = ""
                guard !feedback.isEmpty else { return true }
                return resolve(allow: false, message: "The user said: \(feedback)")
            }
            return true
        }
    }

    private func resolvePending(_ live: LiveSession, process: ClaudeProcess, allow: Bool, updatedInput: [String: Any], message: String?) {
        guard let pending = live.pending else { return }
        process.respond(requestId: pending.requestId, allow: allow, updatedInput: updatedInput, denyMessage: message)
        live.pending = nil
        live.selectedOptions = []
        live.submitRequested = false
        setStatus(live, live.turnActive ? .working : .idle)
    }

    private func scheduleQuestionSubmit(_ live: LiveSession, requestId: String) {
        live.submitToken += 1
        let token = live.submitToken
        let id = live.record.id
        Task {
            try? await Task.sleep(nanoseconds: 600_000_000)
            await self.submitQuestion(id: id, requestId: requestId, token: token)
        }
    }

    private func submitQuestion(id: String, requestId: String, token: Int) {
        guard let live = sessions[id], let pending = live.pending, pending.requestId == requestId,
              live.submitToken == token, live.submitRequested, !live.selectedOptions.isEmpty,
              let process = live.process else { return }
        var answers: [String: String] = [:]
        var base = 0
        for q in pending.questions {
            let picked = Array(Set(live.selectedOptions)).sorted().compactMap { n -> String? in
                let i = n - base - 1
                return (0..<q.options.count).contains(i) ? q.options[i] : nil
            }
            if !picked.isEmpty { answers[q.question] = picked.joined(separator: ", ") }
            base += q.options.count
        }
        var input = (try? JSONSerialization.jsonObject(with: pending.inputJSON)) as? [String: Any] ?? [:]
        input["answers"] = answers
        resolvePending(live, process: process, allow: true, updatedInput: input, message: nil)
    }

    /// Track whether a turn is running, persisting changes so a hard server crash can be
    /// recovered: sessions that were mid-turn get resumed and nudged to continue on startup.
    private func setTurn(_ live: LiveSession, _ active: Bool) {
        if active, !live.turnActive { live.blockPushedThisTurn = false }   // a new turn
        live.turnActive = active
        if (live.record.turnInProgress ?? false) != active {
            live.record.turnInProgress = active
            persist()
        }
    }

    // MARK: - Process lifecycle

    @discardableResult
    private func ensureRunning(_ live: LiveSession) -> Bool {
        if let p = live.process, p.process.isRunning { return true }
        return launch(live)
    }

    private func launch(_ live: LiveSession) -> Bool {
        live.generation += 1
        let generation = live.generation
        let resume = live.record.hasConversation
        let proc = ClaudeProcess(claudePath: claudePath, sessionId: live.record.claudeSessionId, resume: resume,
                                 cwd: live.record.cwd, oauthToken: live.record.oauthToken)
        proc.setTelemetrySession(live.record.id)  // telemetry
        do {
            try proc.start(onExit: { _ in })
        } catch {
            print("[sessions] \(live.record.id): failed to launch claude: \(error)")
            return false
        }
        live.process = proc
        endTurnSpan(live.record.id, outcome: "process_relaunched")  // telemetry
        Telemetry.shared.log("mclaude.process.started", attributes: ["mclaude.session_id": live.record.id, "process.pid": Int(proc.pid), "mclaude.resume": resume, "mclaude.generation": generation])  // telemetry
        live.mcpConfigFingerprint = McpConfig.userFingerprint()
        live.restartAfterTurn = false        // a fresh process already has the current config
        live.stopping = false
        live.turnActive = false
        live.pending = nil
        live.ready = false
        live.restartPending = false
        publishSessions()
        print("[sessions] \(live.record.id): claude pid \(proc.pid) (\(resume ? "resume" : "new"))")
        let id = live.record.id
        // One consumer per process keeps stdout lines strictly ordered.
        Task {
            for await line in proc.lines {
                await self.handleLine(id: id, generation: generation, line: line)
            }
            let status: Int32 = await Task.detached { proc.process.waitUntilExit(); return proc.process.terminationStatus }.value
            await self.noteProcessExit(id: id, generation: generation, pid: proc.pid, status: status)  // telemetry
            await self.processExited(id: id, generation: generation, status: status, stderr: proc.recentStderr)
        }
        return true
    }

    /// Crash watcher (v2 ADR-0051): an exit we didn't ask for restarts the session with --resume,
    /// backing off exponentially; after repeated crashes it retries every 10 minutes (a message
    /// retries right away).
    private func processExited(id: String, generation: Int, status: Int32, stderr: String) {
        guard let live = sessions[id], live.generation == generation else { return }
        live.process = nil
        let wasTurnActive = live.turnActive
        live.turnActive = false
        live.pending = nil
        if live.stopping || live.deleted {
            setStatus(live, .idle)
            return
        }
        print("[sessions] \(id): claude exited unexpectedly (status \(status))\(stderr.isEmpty ? "" : "\n\(stderr.suffix(600))")")
        let now = Date()
        live.crashTimes = live.crashTimes.filter { now.timeIntervalSince($0) < SessionManager.crashWindow } + [now]
        addTranscript(live, "⚠︎ Claude exited (status \(status)).")
        if live.crashTimes.count > SessionManager.maxCrashesInWindow {
            addTranscript(live, "⚠︎ Crashed \(live.crashTimes.count) times in 5 min; retrying every 10 min (sending a message retries now).")
        }
        scheduleRestart(live, resumeTurn: wasTurnActive)
    }

    /// Restart a session whose process died or failed to launch: exponential backoff (1, 2, 4,
    /// 8, 16 s), or every 10 minutes after more than 5 crashes in 5 minutes.
    private func scheduleRestart(_ live: LiveSession, resumeTurn: Bool) {
        let id = live.record.id
        let generation = live.generation
        let crashes = max(1, live.crashTimes.count)
        let delay = crashes > SessionManager.maxCrashesInWindow
            ? SessionManager.crashRetryInterval : min(16.0, pow(2.0, Double(crashes - 1)))
        live.restartPending = true
        setStatus(live, .unknown)
        publishSessions()
        Telemetry.shared.log("mclaude.process.restart_scheduled", attributes: ["mclaude.session_id": id, "mclaude.backoff_s": delay, "mclaude.crashes": crashes, "mclaude.resume_turn": resumeTurn], severity: 13)  // telemetry
        Task {
            try? await Task.sleep(nanoseconds: UInt64(delay * 1_000_000_000))
            await self.restartAfterCrash(id: id, generation: generation, resumeTurn: resumeTurn)
        }
    }

    private func restartAfterCrash(id: String, generation: Int, resumeTurn: Bool) {
        guard let live = sessions[id], live.generation == generation, live.process == nil, !draining else { return }
        guard launch(live) else {
            // launch() bumped the generation; count it as a crash and try again later
            live.crashTimes = live.crashTimes.filter { Date().timeIntervalSince($0) < SessionManager.crashWindow } + [Date()]
            scheduleRestart(live, resumeTurn: resumeTurn)
            return
        }
        addTranscript(live, "↻ Restarted Claude (resumed session).")
        if resumeTurn {
            // The turn died with the process; ask Claude to pick it back up from the transcript.
            let turn = startTurnSpan(live, trigger: "crash_resume")  // telemetry
            live.process?.sendUserMessage(uuid: UUID().uuidString.lowercased(),
                content: "The previous turn was interrupted by a crash. Continue where you left off.", traceparent: turn?.traceparent)  // telemetry: + traceparent
            setTurn(live, true)
            setStatus(live, .working)
        } else {
            setStatus(live, .idle)
        }
    }

    // MARK: - Deliberate restart (docs/plan-mcp-servers.md → Restart)

    enum RestartOutcome: Sendable { case restarting, busy, notRunning, notFound }

    /// "Restart Claude" on one session: refused while a turn runs or a prompt is pending.
    func restart(id: String) -> RestartOutcome {
        guard let live = sessions[id] else { return .notFound }
        if live.turnActive || live.pending != nil { return .busy }
        guard let p = live.process, p.process.isRunning, !live.stopping else { return .notRunning }
        restartProcess(live)
        return .restarting
    }

    /// Restart every session: idle ones now (1 s apart), busy ones when their turn ends.
    /// Stopped (In use) and crash-restarting sessions are skipped.
    func restartAll() -> (restarting: Int, afterTurn: Int) {
        var now: [String] = []
        var later = 0
        for live in sessions.values.sorted(by: { $0.record.index < $1.record.index }) {
            guard let p = live.process, p.process.isRunning, !live.stopping else { continue }
            if live.turnActive || live.pending != nil {
                live.restartAfterTurn = true
                later += 1
            } else {
                now.append(live.record.id)
            }
        }
        Task {
            for (i, id) in now.enumerated() {
                if i > 0 { try? await Task.sleep(nanoseconds: 1_000_000_000) }
                await self.restartIfIdle(id: id)
            }
        }
        return (now.count, later)
    }

    private func restartIfIdle(id: String) {
        guard let live = sessions[id], let p = live.process, p.process.isRunning, !live.stopping else { return }
        if live.turnActive || live.pending != nil { live.restartAfterTurn = true; return }
        restartProcess(live)
    }

    /// Stop the process (not a crash: no backoff, no crash notice) and launch it again.
    private func restartProcess(_ live: LiveSession) {
        guard let proc = live.process, proc.process.isRunning else { return }
        live.stopping = true
        live.restartAfterTurn = false
        let id = live.record.id
        let generation = live.generation
        publishSessions()
        print("[sessions] \(id): restarting claude (MCP reload)")
        proc.stop(grace: 5)
        Task {
            await Task.detached { proc.process.waitUntilExit() }.value
            await self.relaunchAfterRestart(id: id, generation: generation)
        }
    }

    private func relaunchAfterRestart(id: String, generation: Int) {
        guard let live = sessions[id], live.generation == generation, !live.deleted, !draining else { return }
        live.process = nil
        guard launch(live) else {
            live.crashTimes = live.crashTimes.filter { Date().timeIntervalSince($0) < SessionManager.crashWindow } + [Date()]
            scheduleRestart(live, resumeTurn: false)
            return
        }
        addTranscript(live, "↻ Restarted Claude to reload MCP servers.")
        setStatus(live, .idle)
        flushQueue(live, reason: "restarted")
    }

    // MARK: - MCP control (docs/plan-mcp-servers.md)

    enum McpCallError: Error, Sendable { case notFound, notRunning, timeout, claude(String) }

    /// A ready process for session `id`, or why there isn't one.
    private func mcpProcess(_ id: String) -> Result<(ClaudeProcess, String), McpCallError> {
        guard let live = sessions[id] else { return .failure(.notFound) }
        guard let p = live.process, p.process.isRunning, live.ready, !live.stopping else { return .failure(.notRunning) }
        return .success((p, live.record.cwd))
    }

    private static func call(_ p: ClaudeProcess, _ subtype: String, _ fields: [String: Any] = [:],
                             timeout: TimeInterval) async -> Result<JSONBox, McpCallError> {
        switch await p.controlRequest(subtype, fields, timeout: timeout) {
        case .success(let box): return .success(box)
        case .error(let message): return .failure(.claude(message))
        case .timeout: return .failure(.timeout)
        case .notRunning: return .failure(.notRunning)
        }
    }

    /// Raw `mcp_status` entries for one session.
    func mcpStatus(id: String, timeout: TimeInterval = 10) async -> Result<(cwd: String, servers: [JSONBox]), McpCallError> {
        let proc: ClaudeProcess, cwd: String
        switch mcpProcess(id) {
        case .failure(let e): return .failure(e)
        case .success(let v): (proc, cwd) = v
        }
        switch await Self.call(proc, "mcp_status", timeout: timeout) {
        case .failure(let e): return .failure(e)
        case .success(let box):
            let list = (box.value["mcpServers"] as? [[String: Any]] ?? []).map(JSONBox.init)
            return .success((cwd, list))
        }
    }

    /// Reconnect `names` in session `id`, in parallel. Individual failures are ignored: the
    /// next status shows them.
    func mcpReconnect(id: String, names: [String]) async -> McpCallError? {
        let proc: ClaudeProcess
        switch mcpProcess(id) {
        case .failure(let e): return e
        case .success(let v): proc = v.0
        }
        let results = await withTaskGroup(of: McpCallError?.self) { group in
            for name in names {
                group.addTask {
                    if case .failure(let e) = await Self.call(proc, "mcp_reconnect", ["serverName": name], timeout: 30) { return e }
                    return nil
                }
            }
            var errors: [McpCallError?] = []
            for await r in group { errors.append(r) }
            return errors
        }
        // A single named reconnect reports its own error; "reconnect all" never fails as a whole
        return names.count == 1 ? results.first ?? nil : nil
    }

    /// Toggle in this session, then in every other running session in the same cwd (the
    /// setting is saved per project, so they should agree).
    func mcpToggle(id: String, name: String, enabled: Bool) async -> McpCallError? {
        let proc: ClaudeProcess, cwd: String
        switch mcpProcess(id) {
        case .failure(let e): return e
        case .success(let v): (proc, cwd) = v
        }
        if case .failure(let e) = await Self.call(proc, "mcp_toggle", ["serverName": name, "enabled": enabled], timeout: 10) {
            return e
        }
        let others = sessions.values.compactMap { live -> ClaudeProcess? in
            guard live.record.id != id, live.record.cwd == cwd, live.ready, !live.stopping,
                  let p = live.process, p.process.isRunning else { return nil }
            return p
        }
        for p in others {
            _ = await Self.call(p, "mcp_toggle", ["serverName": name, "enabled": enabled], timeout: 10)
        }
        return nil
    }

    /// Start an OAuth sign-in for `name` in session `id`: `authUrl` and `callbackPort`.
    func mcpAuthenticate(id: String, name: String) async -> Result<JSONBox, McpCallError> {
        switch mcpProcess(id) {
        case .failure(let e): return .failure(e)
        case .success(let v): return await Self.call(v.0, "mcp_authenticate", ["serverName": name], timeout: 15)
        }
    }

    /// Finish a sign-in with the browser's callback URL, then reconnect that server everywhere
    /// else (the credential is stored once, per user).
    func mcpAuthCallback(id: String, name: String, callbackUrl: String) async -> McpCallError? {
        let proc: ClaudeProcess
        switch mcpProcess(id) {
        case .failure(let e): return e
        case .success(let v): proc = v.0
        }
        if case .failure(let e) = await Self.call(proc, "mcp_oauth_callback_url",
                                                  ["serverName": name, "callbackUrl": callbackUrl], timeout: 15) {
            return e
        }
        let others = sessions.values.compactMap { live -> ClaudeProcess? in
            guard live.record.id != id, live.ready, !live.stopping, let p = live.process, p.process.isRunning else { return nil }
            return p
        }
        for p in others {
            Task { _ = await Self.call(p, "mcp_reconnect", ["serverName": name], timeout: 30) }
        }
        return nil
    }

    /// Ids and names of sessions whose process can answer MCP requests (global screen).
    func mcpReadySessions() -> [(id: String, name: String)] {
        sessions.values.sorted(by: { $0.record.index < $1.record.index }).compactMap { live in
            guard live.ready, !live.stopping, let p = live.process, p.process.isRunning else { return nil }
            return (live.record.id, live.record.windowName)
        }
    }

    /// Running sessions whose process started with a different user MCP config.
    func mcpStaleSessionCount(current: String?) -> Int {
        sessions.values.filter { live in
            guard let p = live.process, p.process.isRunning, !live.stopping else { return false }
            return live.mcpConfigFingerprint != current
        }.count
    }

    /// Graceful drain (v2 ADR-0043): stop taking new sessions, let running turns finish (a prompt
    /// waiting on the user doesn't block), then stop every process. Recovery resumes them later.
    func drain(timeout: TimeInterval) async {
        draining = true
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            let busy = sessions.values.filter { $0.turnActive && $0.pending == nil }
            if busy.isEmpty { break }
            print("[sessions] drain: waiting for \(busy.map(\.record.id).joined(separator: ", "))")
            try? await Task.sleep(nanoseconds: 2_000_000_000)
        }
        for live in sessions.values {
            live.stopping = true
            live.process?.stop(grace: 5)
        }
        try? await Task.sleep(nanoseconds: 1_000_000_000)
    }

    // MARK: - Telemetry (docs/plan-observability.md → System map → mclaude-server)  // telemetry

    /// Opens the session's `claude.turn` span, or returns the open one (a message delivered at a  // telemetry
    /// step boundary joins the running turn). A turn started by a phone request is its child.  // telemetry
    private func startTurnSpan(_ live: LiveSession, trigger: String) -> Telemetry.SpanContext? {  // telemetry
        let id = live.record.id  // telemetry
        if let open = turnSpans[id] { return open.context }  // telemetry
        let span = Telemetry.shared.startSpan("claude.turn", parent: inputRequest, attributes: [  // telemetry
            "mclaude.session_id": id, "mclaude.turn.trigger": trigger, "claude.session_id": live.record.claudeSessionId])  // telemetry
        turnSpans[id] = span  // telemetry
        return span.context  // telemetry
    }  // telemetry

    private func endTurnSpan(_ id: String, outcome: String, error: Bool = false, attributes: [String: Any] = [:]) {  // telemetry
        guard let span = turnSpans.removeValue(forKey: id) else { return }  // telemetry
        span.end(attributes: attributes.merging(["mclaude.turn.outcome": outcome]) { _, b in b }, error: error)  // telemetry
    }  // telemetry

    /// `mclaude.decision.input_routed`, inside the request that sent the input.  // telemetry
    private func inputRouted(_ live: LiveSession, _ route: String, _ extra: [String: Any] = [:]) {  // telemetry
        let attrs = extra.merging(["mclaude.session_id": live.record.id, "mclaude.route": route]) { _, b in b }  // telemetry
        Telemetry.shared.log("mclaude.decision.input_routed", attributes: attrs, in: inputRequest ?? Telemetry.currentRequest)  // telemetry
    }  // telemetry

    /// `mclaude.process.exited` for every exit, including stale and deliberate ones.  // telemetry
    private func noteProcessExit(id: String, generation: Int, pid: Int32, status: Int32) {  // telemetry
        let live = sessions[id]  // telemetry
        let current = live?.generation == generation  // telemetry
        let expected = live == nil || !current || live!.stopping || live!.deleted || draining  // telemetry
        Telemetry.shared.log("mclaude.process.exited", attributes: ["mclaude.session_id": id, "process.pid": Int(pid), "process.exit.code": Int(status), "mclaude.expected": expected], severity: expected ? 9 : 17)  // telemetry
        if current { endTurnSpan(id, outcome: "process_exited", error: !expected) }  // telemetry
    }  // telemetry

    /// Heartbeat numbers: sessions by process state, and running turns.  // telemetry
    func telemetryHeartbeat() -> (processStates: [String: Int], turnsRunning: Int) {  // telemetry
        var states = ["starting": 0, "running": 0, "restarting": 0, "stopped": 0]  // telemetry
        for live in sessions.values { states[processState(live), default: 0] += 1 }  // telemetry
        return (states, sessions.values.filter(\.turnActive).count)  // telemetry
    }  // telemetry

    // MARK: - Event loop

    private func handleLine(id: String, generation: Int, line: String) {
        guard let live = sessions[id], live.generation == generation,
              let data = line.data(using: .utf8),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let type = json["type"] as? String else { return }
        if !live.ready {
            live.ready = true
            publishSessions()
        }
        let now = ISO8601DateFormatter.fractional.string(from: Date())
        let uuid = json["uuid"] as? String ?? UUID().uuidString.lowercased()
        let parentToolUse = json["parent_tool_use_id"] as? String

        switch type {
        case "system":
            let subtype = json["subtype"] as? String
            let taskId = json["task_id"] as? String
            if subtype == "task_started", let taskId, json["is_backgrounded"] as? Bool == false {
                live.foregroundTasks.insert(taskId)
            }
            if subtype == "task_updated", let taskId,
               (json["patch"] as? [String: Any])?["is_backgrounded"] as? Bool == true {
                live.foregroundTasks.remove(taskId)
            }
            if subtype == "task_notification", let taskId, live.foregroundTasks.remove(taskId) != nil {
                // A foreground command is tracked as a task too, but Claude only gets its tool result
            } else if subtype == "task_notification" {
                // The transcript's <task-notification> user line never reaches stdout; rebuild it
                // so the app shows the card live. It doesn't start a turn: Claude takes it later.
                let agent = (json["tool_use_id"] as? String).flatMap { live.subagents[$0] }
                if let event = JSONLParser.taskNotificationEvent(json: json, uuid: uuid, timestamp: now,
                                                                 agentDescription: agent?.description) {
                    if let taskId { live.shownTaskNotices[taskId, default: 0] += 1 }
                    append(tag(event, parentToolUse, live), to: live)
                }
            }
            if subtype == "init", live.pending == nil, !live.turnActive {
                setStatus(live, .idle)
            }
            if subtype == "status" {
                // {"status":"compacting"} … {"status":null,"compact_result":"success"}
                let compacting = json["status"] as? String == "compacting"
                if compacting != live.compacting {
                    live.compacting = compacting
                    publishSessions()
                }
            }
            if subtype == "compact_boundary" {
                live.compacting = false
                live.pendingCompaction = (uuid, json, now)
                // The context is now the summary: move the gauge right away instead of waiting
                // for the next reply's usage.
                if let meta = json["compact_metadata"] as? [String: Any], let post = meta["post_tokens"] as? Int {
                    var u = live.record.usage ?? SessionUsage()
                    u.contextTokens = post
                    live.record.usage = u
                    persist()
                }
                publishSessions()
            }

        case "user":
            guard var event = JSONLParser.parseUserEvent(json: json, uuid: uuid, timestamp: now) else { return }
            if event.type == .compaction {
                // The summary belongs to the compaction marker emitted at the turn's result
                live.pendingCompactionSummary = event.text
                return
            }
            if parentToolUse == nil, event.type == .user {
                setTurn(live, true)                         // Claude took a message: a turn is running
                if live.pending == nil { setStatus(live, .working) }
            }
            // A notification Claude takes mid-turn is also replayed here; its card is already shown
            if event.type == .user, let text = event.text, !live.shownTaskNotices.isEmpty,
               let rest = JSONLParser.removingTaskNotifications(from: text, where: { id in
                   guard let n = live.shownTaskNotices[id] else { return false }
                   live.shownTaskNotices[id] = n > 1 ? n - 1 : nil
                   return true
               }) {
                guard !rest.isEmpty else { return }
                event = SessionEvent(uuid: event.uuid, timestamp: event.timestamp, type: .user, text: rest,
                                     thinking: nil, toolUse: nil, toolResults: nil, model: nil, durationMs: nil)
            }
            // A mid-turn peer message is replayed when Claude takes it; its timestamp is the enqueue
            append(observePeers(tag(event, parentToolUse, live), live, receivedAt: json["timestamp"] as? String), to: live)
            if event.type == .toolResult { notifyBlocked(live, event) }
            if parentToolUse == nil, event.type == .toolResult {
                // A step finished; Claude reads stdin before its next model call
                flushQueue(live, reason: "step boundary")
            }

        case "assistant":
            guard let message = json["message"] as? [String: Any],
                  let blocks = message["content"] as? [[String: Any]] else { return }
            // A local command (/reload-plugins …) isn't replayed as a user line; its output comes as a
            // synthetic assistant message. Echo the command first so the pending bubble clears.
            if parentToolUse == nil, let echo = JSONLParser.localCommandEcho(json),
               !Self.echoedThisTurn(echo, in: live.events) {
                // 1 ms before the output, so a timestamp sort keeps the command above it
                let base = ISO8601DateFormatter.fractional.date(from: now) ?? Date()
                let at = ISO8601DateFormatter.fractional.string(from: base.addingTimeInterval(-0.001))
                append(SessionEvent(uuid: "\(uuid)-cmd", timestamp: at, type: .user, text: echo, thinking: nil,
                                    toolUse: nil, toolResults: nil, model: nil, durationMs: nil), to: live)
            }
            for (i, block) in blocks.enumerated() {
                var single = json
                var msg = message
                msg["content"] = [block]
                single["message"] = msg
                let blockUuid = blocks.count == 1 ? uuid : "\(uuid)-\(i)"
                guard let event = JSONLParser.parseAssistantEvent(json: single, uuid: blockUuid, timestamp: now) else { continue }
                if event.type == .toolUse, event.toolUse?.name == "Agent" || event.toolUse?.name == "Task",
                   let toolId = event.toolUse?.id {
                    let input = block["input"] as? [String: Any] ?? [:]
                    live.subagents[toolId] = SubagentInfo(
                        agentId: toolId, agentType: input["subagent_type"] as? String ?? "general-purpose",
                        description: input["description"] as? String ?? "", parentToolUseId: toolId)
                }
                append(observePeers(tag(event, parentToolUse, live), live), to: live)
                if parentToolUse == nil, event.type == .text, let text = event.text, !text.isEmpty {
                    live.lastAssistantText = text
                }
            }
            if parentToolUse == nil, let usage = message["usage"] as? [String: Any] {
                var u = live.record.usage ?? SessionUsage()
                func percent(_ x: SessionUsage) -> Int? {
                    guard let t = x.contextTokens, let w = x.contextWindow, w > 0 else { return nil }
                    return t * 100 / w
                }
                let before = percent(u)
                u.noteAssistantMessage(usage: usage, model: message["model"] as? String)
                live.record.usage = u
                // Only broadcast when the app's gauge would change (whole percent), not per block
                if percent(u) != before { publishSessions() }
            }
            if parentToolUse == nil, live.pending == nil, live.status != .working {
                setTurn(live, true)
                setStatus(live, .working)
            }

        case "result":
            // Turn finished (success, error, or interrupted). The app reads system events with
            // durationMs as "turn ended", exactly what the JSONL turn_duration entries gave it.
            var usage = live.record.usage ?? SessionUsage()
            usage.addResult(json)
            live.record.usage = usage
            learnContextWindows(from: json)
            persist()
            setTurn(live, false)
            endTurnSpan(id, outcome: json["subtype"] as? String ?? "unknown", error: json["is_error"] as? Bool ?? false, attributes: ["claude.duration_ms": json["duration_ms"] as? Int ?? 0, "claude.num_turns": json["num_turns"] as? Int ?? 0, "claude.total_cost_usd": json["total_cost_usd"] as? Double ?? 0])  // telemetry
            live.crashTimes = []
            if let pc = live.pendingCompaction {
                let path = ClaudePaths.transcript(cwd: live.record.cwd, sessionId: live.record.claudeSessionId)
                let summary = live.pendingCompactionSummary
                    ?? Self.compactionSummary(transcriptPath: path, boundaryUuid: pc.uuid)
                // Stamped now (not at the boundary) so it sorts after the "/compact" echo
                append(JSONLParser.compactionMarker(json: pc.json, uuid: pc.uuid, timestamp: now, summary: summary),
                       to: live)
            } else if let summary = live.pendingCompactionSummary {
                // A summary with no boundary this turn: still show one marker
                append(SessionEvent(uuid: UUID().uuidString.lowercased(), timestamp: now, type: .compaction, text: summary,
                                    thinking: nil, toolUse: nil, toolResults: nil, model: nil, durationMs: nil),
                       to: live)
            }
            live.pendingCompaction = nil
            live.pendingCompactionSummary = nil
            live.compacting = false
            let event = SessionEvent(uuid: uuid, timestamp: now, type: .system, text: nil, thinking: nil,
                                     toolUse: nil, toolResults: nil, model: nil, durationMs: json["duration_ms"] as? Int)
            append(event, to: live)
            if json["subtype"] as? String == "error_during_execution" { addTranscript(live, "⏹ Interrupted.") }
            if live.pending == nil { setStatus(live, .idle) }
            notifyIfLongTurn(live, result: json)
            requestRecap(live)
            if live.restartAfterTurn, live.pending == nil {
                // Restart all ran mid-turn; queued messages go to the new process
                live.restartAfterTurn = false
                restartProcess(live)
                return
            }
            flushQueue(live, reason: "turn ended")      // queued messages become the next turn

        case "control_request":
            guard let requestId = json["request_id"] as? String,
                  let request = json["request"] as? [String: Any] else { return }
            if request["subtype"] as? String == "can_use_tool" {
                registerPending(requestId: requestId, request: request, in: live)
            } else {
                // Hooks/MCP callbacks we don't implement: answer so Claude never hangs on us.
                live.process?.send(["type": "control_response",
                                    "response": ["subtype": "error", "request_id": requestId, "error": "not supported by mclaude-server"]])
            }

        case "control_cancel_request":
            if let rid = json["request_id"] as? String, live.pending?.requestId == rid {
                live.pending = nil
                setStatus(live, live.turnActive ? .working : .idle)
            }

        default:
            break                                           // stream_event, control_response, status...
        }
    }

    private func registerPending(requestId: String, request: [String: Any], in live: LiveSession) {
        let toolName = request["tool_name"] as? String ?? "tool"
        let input = request["input"] as? [String: Any] ?? [:]
        let inputJSON = (try? JSONSerialization.data(withJSONObject: input)) ?? Data("{}".utf8)
        switch toolName {
        case "AskUserQuestion":
            let qs = (input["questions"] as? [[String: Any]] ?? []).map { q in
                (question: q["question"] as? String ?? "",
                 options: (q["options"] as? [[String: Any]] ?? []).compactMap { $0["label"] as? String },
                 multiSelect: q["multiSelect"] as? Bool ?? false)
            }
            live.pending = PendingRequest(requestId: requestId, kind: .question, toolName: toolName,
                                          inputJSON: inputJSON, questions: qs, planText: nil)
            setStatus(live, .waitingForInput)
        case "ExitPlanMode":
            live.pending = PendingRequest(requestId: requestId, kind: .plan, toolName: toolName, inputJSON: inputJSON,
                                          questions: [], planText: input["plan"] as? String)
            addTranscript(live, "📋 Plan ready for approval.")
            setStatus(live, .planMode)
        default:
            live.pending = PendingRequest(requestId: requestId, kind: .tool, toolName: toolName, inputJSON: inputJSON,
                                          questions: [], planText: nil)
            setStatus(live, .needsPermission)
        }
        live.selectedOptions = []
        live.submitRequested = false
        notifyNeedsYou(live)
        requestRecap(live)
    }

    /// "Needs you" push (docs/plan-watch-app.md): one per pending request.
    /// "Blocked" push (docs/design-claude-uplift.md → Permissions (auto mode)): the first
    /// auto-mode denial in a turn. Nothing waits on it; Claude carries on or explains.
    private func notifyBlocked(_ live: LiveSession, _ event: SessionEvent) {
        guard let block = (event.toolResults ?? []).first(where: AutoModeBlock.isBlock) else { return }
        let toolName = live.events.lazy.compactMap(\.toolUse).last { $0.id == block.toolUseId }?.name ?? "tool"
        let body = AutoModeBlock.pushBody(toolName: toolName, content: block.content)
        print("[sessions] \(live.record.id): auto mode \(body)")
        guard !live.blockPushedThisTurn, let push else { return }
        live.blockPushedThisTurn = true
        let title = live.record.windowName
        let sessionId = live.record.id
        Task { await push.notify(title: title, body: body, sessionId: sessionId, userId: nil) }
    }

    private func notifyNeedsYou(_ live: LiveSession) {
        guard let push, let p = live.pending else { return }
        let body: String
        switch p.kind {
        case .question:
            let q = p.questions.first?.question.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            body = q.isEmpty ? "Claude has a question" : PushNotifier.summarize(q)
        case .plan: body = "Approve the plan?"
        case .tool: body = "Allow \(p.toolName)?"
        }
        let title = live.record.windowName
        let sessionId = live.record.id
        Task { await push.notify(title: title, body: body, sessionId: sessionId, userId: nil) }
    }

    // MARK: - Recap (docs/plan-watch-app.md → Recap)

    /// A turn ended or a request started waiting: make a new recap, or queue one re-run if one
    /// is already running.
    private func requestRecap(_ live: LiveSession) {
        guard live.record.hasConversation, !live.deleted else { return }
        if live.recapRunning { live.recapRerun = true; return }
        startRecap(live)
    }

    private func startRecap(_ live: LiveSession) {
        live.recapRunning = true
        let id = live.record.id, path = claudePath, token = live.record.oauthToken
        let transcriptPath = ClaudePaths.transcript(cwd: live.record.cwd, sessionId: live.record.claudeSessionId)
        let known = live.firstPrompt
        let fallback = live.events.first { $0.type == .user && $0.subagentInfo == nil }?.text
            .map { String(SessionTitler.typedText(of: $0).prefix(SessionRecapper.firstPromptLimit)) }
        // Answers to questions and plan approvals are tool results, but they're the user speaking:
        // without them the model can't tell that Claude's follow-up is based on real answers.
        let promptToolIds = Set(live.events.compactMap { e -> String? in
            guard let t = e.toolUse, t.name == "AskUserQuestion" || t.name == "ExitPlanMode" else { return nil }
            return t.id
        })
        let recent = SessionRecapper.recentMessages(live.events.compactMap { e -> (fromUser: Bool, text: String)? in
            guard e.subagentInfo == nil else { return nil }
            if e.type == .toolResult {
                let answers = (e.toolResults ?? []).filter { promptToolIds.contains($0.toolUseId) }.map(\.content)
                return answers.isEmpty ? nil : (true, "(answering Claude's prompt) " + answers.joined(separator: " "))
            }
            guard let text = e.text else { return nil }
            switch e.type {
            case .user: return text.hasPrefix("The previous turn was interrupted by") ? nil : (true, SessionTitler.typedText(of: text))
            case .text: return (false, text)
            default: return nil
            }
        })
        let pending = live.pending.map(Self.recapPendingText)
        Task.detached(priority: .utility) {
            let fromDisk = known == nil ? SessionRecapper.firstPrompt(transcriptPath: transcriptPath) : nil
            let recap = await SessionRecapper.generate(firstPrompt: known ?? fromDisk ?? fallback, recent: recent,
                                                       pending: pending, claudePath: path, oauthToken: token)
            await self.finishRecap(id: id, firstPrompt: fromDisk, recap: recap)
        }
    }

    private func finishRecap(id: String, firstPrompt: String?, recap: SessionRecap?) {
        guard let live = sessions[id] else { return }       // ended: its recap is gone with it
        live.recapRunning = false
        if live.firstPrompt == nil, let firstPrompt { live.firstPrompt = firstPrompt }
        if let recap {
            live.record.recap = recap
            persist()
            publishSessions()
        } else {
            print("[sessions] \(id): recap failed; keeping the previous one")
        }
        if live.recapRerun {
            live.recapRerun = false
            startRecap(live)
        }
    }

    /// The pending request, as the recap model sees it.
    private static func recapPendingText(_ p: PendingRequest) -> String {
        switch p.kind {
        case .question:
            return "Claude asked the user:\n" + p.questions.map { q in
                q.question + (q.options.isEmpty ? "" : " (options: \(q.options.joined(separator: " / ")))")
            }.joined(separator: "\n")
        case .plan:
            return "Claude finished planning and is waiting for the user to approve this plan:\n"
                + String((p.planText ?? "").prefix(1500))
        case .tool:
            let input = String(decoding: p.inputJSON, as: UTF8.self)
            return "Claude is waiting for the user's permission to use the tool \(p.toolName) with input: "
                + String(input.prefix(300))
        }
    }

    /// Peer messages in a live event: record them and attach peerMessages / peerDelivery.
    private func observePeers(_ event: SessionEvent, _ live: LiveSession, receivedAt: String? = nil) -> SessionEvent {
        let e = peers.observeLive(event, observing: live.record.claudeSessionId, now: event.timestamp,
                                  receivedAt: receivedAt, pending: &live.peerSends)
        // The buffered tool_use card stops saying "sending" once its result is in (GET /events)
        if let d = e.peerDelivery,
           let i = live.events.lastIndex(where: { $0.type == .toolUse && $0.toolUse?.id == d.toolUseId }),
           var msgs = live.events[i].peerMessages, !msgs.isEmpty {
            msgs[0].status = d.status
            msgs[0].error = d.error
            live.events[i].peerMessages = msgs
        }
        return e
    }

    private func tag(_ event: SessionEvent, _ parentToolUse: String?, _ live: LiveSession) -> SessionEvent {
        guard let parent = parentToolUse, let info = live.subagents[parent] else { return event }
        return SessionEvent(uuid: "sa-\(parent.suffix(8))-\(event.uuid)", timestamp: event.timestamp, type: event.type,
                            text: event.text, thinking: event.thinking, toolUse: event.toolUse,
                            toolResults: event.toolResults, model: event.model, durationMs: event.durationMs,
                            subagentInfo: info)
    }

    // MARK: - State, transcript and broadcasting

    private func append(_ event: SessionEvent, to live: LiveSession, broadcast: Bool = true) {
        live.events.append(event)
        if live.events.count > SessionManager.maxEvents { live.events.removeFirst(live.events.count - SessionManager.maxEvents) }
        for line in SessionManager.render(event) { addTranscript(live, line, broadcast: broadcast) }
        if broadcast { onEvent?(live.record.id, event) }
    }

    private func addTranscript(_ live: LiveSession, _ line: String, broadcast: Bool = true) {
        live.transcript.append(line)
        if live.transcript.count > SessionManager.maxTranscriptLines {
            live.transcript.removeFirst(live.transcript.count - SessionManager.maxTranscriptLines)
        }
        if broadcast { scheduleOutputFlush(live.record.id) }
    }

    /// Plain-text rendering for the app's terminal view (it used to show the tmux pane).
    static func render(_ e: SessionEvent) -> [String] {
        let indent = e.subagentInfo == nil ? "" : "    "
        func clip(_ s: String, _ n: Int) -> String { s.count > n ? String(s.prefix(n)) + "…" : s }
        switch e.type {
        case .user:
            return ["", "\(indent)> " + (e.text ?? "")]
        case .text:
            return [""] + (e.text ?? "").components(separatedBy: "\n").map { indent + $0 }
        case .thinking:
            return []
        case .toolUse:
            guard let t = e.toolUse else { return [] }
            return ["\(indent)⏺ \(t.name)(\(clip(t.inputSummary, 120)))"]
        case .toolResult:
            return (e.toolResults ?? []).flatMap { r -> [String] in
                let lines = r.content.components(separatedBy: "\n")
                return lines.prefix(4).map { "\(indent)  ⎿ " + clip($0, 160) } + (lines.count > 4 ? ["\(indent)    … +\(lines.count - 4) lines"] : [])
            }
        case .system:
            guard let ms = e.durationMs else { return [] }
            return ["\(indent)✻ Done in \(ms / 1000)s"]
        case .compaction:
            return ["", "── conversation compacted ──"]
        }
    }

    private func scheduleOutputFlush(_ id: String) {
        guard !outputFlushScheduled.contains(id) else { return }
        outputFlushScheduled.insert(id)
        Task {
            try? await Task.sleep(nanoseconds: 300_000_000)    // coalesce bursts of events
            await self.flushOutput(id)
        }
    }

    private func flushOutput(_ id: String) {
        outputFlushScheduled.remove(id)
        if let output = getCachedOutput(id: id) { onOutputChanged?(id, output) }
    }

    private func setStatus(_ live: LiveSession, _ status: SessionStatus) {
        guard live.status != status else { return }
        let old = live.status
        live.status = status
        live.statusSince = Date()
        onStatusChange?(snapshot(live), old, status)
        publishSessions()
    }

    private func publishSessions() {
        resumeState.update(open: Dictionary(sessions.values.map { ($0.record.claudeSessionId, $0.record.id) },
                                            uniquingKeysWith: { a, _ in a }),
                           endedNames: endedNames)
        onSessionsUpdated?(getCachedSessions())
    }

    private func snapshot(_ live: LiveSession) -> ClaudeSession {
        var prompt: DetectedPrompt?
        if let p = live.pending {
            switch p.kind {
            case .question:
                // The app's prompt UI only handles one question; ConversationView renders all of
                // them from the AskUserQuestion tool_use event and numbers options globally.
                // `questions` carries all of them (with kind) for the watch.
                prompt = .question(p.questions.map {
                    PromptQuestion(question: $0.question, options: $0.options, multiSelect: $0.multiSelect)
                })
            case .plan:
                prompt = .plan
            case .tool:
                prompt = .tool(p.toolName)
            }
        }
        return ClaudeSession(
            id: live.record.id, pid: Int(live.process?.pid ?? 0), sessionId: live.record.claudeSessionId,
            cwd: live.record.cwd, startedAt: live.record.createdAt,
            name: live.record.windowName, status: live.status,
            statusSince: live.statusSince, projectName: (live.record.cwd as NSString).lastPathComponent,
            lastOutput: "", prompt: prompt, queue: live.queue,
            contextTokens: live.record.usage?.contextTokens, contextWindow: live.record.usage?.contextWindow,
            compacting: live.compacting ? true : nil,
            process: processState(live), recap: live.record.recap)
    }

    /// docs/design-claude-uplift.md → Session processes → Process state.
    private func processState(_ live: LiveSession) -> String {
        if let p = live.process, p.process.isRunning {
            if live.stopping, !live.deleted, !draining { return "restarting" }   // deliberate restart
            return live.ready ? "running" : "starting"
        }
        if live.restartPending { return "restarting" }
        if live.record.startOnFirstMessage == true { return "stopped" }
        return "starting"   // waiting for its turn in the startup queue
    }

    /// "/compact", "/compact keep X", "/plugin:skill args": a command word, not a path like
    /// "/tmp/mclaude-screenshots/x.jpg caption" (photo messages start with their path).
    /// Whether the newest events (user events only, nothing from Claude after them) include a
    /// top-level user event with `text`: the CLI already echoed this command itself.
    static func echoedThisTurn(_ text: String, in events: [SessionEvent]) -> Bool {
        for e in events.reversed() {
            guard e.type == .user else { return false }
            if e.subagentInfo == nil, e.text?.trimmingCharacters(in: .whitespacesAndNewlines) == text { return true }
        }
        return false
    }

    static func isSlashCommand(_ text: String) -> Bool {
        text.range(of: #"^/[A-Za-z][A-Za-z0-9_:.-]*(\s|$)"#, options: .regularExpression) != nil
    }

    /// The summary Claude Code wrote after a compact_boundary: the transcript's isCompactSummary
    /// user line whose parent is the boundary. Reads only the file's tail.
    static func compactionSummary(transcriptPath: String, boundaryUuid: String) -> String? {
        guard let handle = FileHandle(forReadingAtPath: transcriptPath) else { return nil }
        defer { try? handle.close() }
        let size = (try? handle.seekToEnd()) ?? 0
        let window: UInt64 = 8 << 20
        try? handle.seek(toOffset: size > window ? size - window : 0)
        guard let data = try? handle.readToEnd(), let text = String(data: data, encoding: .utf8) else { return nil }
        for line in text.split(separator: "\n").reversed() where line.contains("isCompactSummary") {
            guard let json = try? JSONSerialization.jsonObject(with: Data(line.utf8)) as? [String: Any],
                  json["isCompactSummary"] as? Bool == true, json["parentUuid"] as? String == boundaryUuid,
                  let message = json["message"] as? [String: Any] else { continue }
            if let s = message["content"] as? String { return s }
            if let blocks = message["content"] as? [[String: Any]] {
                return blocks.compactMap { $0["text"] as? String }.joined(separator: "\n")
            }
        }
        return nil
    }
}

extension JSONEncoder {
    static let iso: JSONEncoder = { let e = JSONEncoder(); e.dateEncodingStrategy = .iso8601; e.outputFormatting = [.prettyPrinted]; return e }()
}

extension JSONDecoder {
    static let iso: JSONDecoder = { let d = JSONDecoder(); d.dateDecodingStrategy = .iso8601; return d }()
}

extension ISO8601DateFormatter {
    static let fractional: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()
}

extension Date {
    static func fromISO(_ s: String) -> Date? {
        ISO8601DateFormatter.fractional.date(from: s) ?? ISO8601DateFormatter().date(from: s)
    }
}
