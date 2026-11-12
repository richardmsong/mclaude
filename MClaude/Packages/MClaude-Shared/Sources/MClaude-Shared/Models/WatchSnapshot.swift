import Foundation

/// What the iPhone app relays to the watch app (docs/plan-watch-app.md → Snapshot).
public struct WatchSnapshot: Codable, Sendable, Equatable {
    public enum Failure: String, Codable, Sendable { case signedOut, server }

    public var fetchedAt: Date
    public var error: Failure?
    public var sessions: [WatchSession]

    public init(fetchedAt: Date, error: Failure?, sessions: [WatchSession]) {
        self.fetchedAt = fetchedAt
        self.error = error
        self.sessions = sessions
    }

    /// WatchConnectivity carries property-list values, so the snapshot travels as JSON data under this key.
    public static let messageKey = "snapshot"
    /// Watch → iPhone request types, under `requestKey` (docs/plan-watch-app.md → Relay).
    public static let requestKey = "type"
    public static let requestRefresh = "refresh"
    public static let requestMarkRead = "markRead"
    public static let requestAction = "action"
    public static let requestMessage = "message"
    /// Live view (docs/plan-watch-app.md → Relay → Live): request, and the iPhone's pushed frames.
    public static let requestLive = "live"
    public static let pushLiveFrame = "liveFrame"
    /// Past turns (docs/plan-watch-app.md → Relay → Past turns).
    public static let requestTurns = "turns"

    private static let encoder: JSONEncoder = {
        let e = JSONEncoder(); e.dateEncodingStrategy = .iso8601; return e
    }()
    private static let decoder: JSONDecoder = {
        let d = JSONDecoder(); d.dateDecodingStrategy = .iso8601; return d
    }()

    public func encoded() -> Data? { try? Self.encoder.encode(self) }
    public static func decode(_ data: Data) -> WatchSnapshot? { try? decoder.decode(WatchSnapshot.self, from: data) }
}

public struct WatchSession: Codable, Sendable, Equatable, Identifiable {
    public enum State: String, Codable, Sendable {
        case waiting, working, idle, starting, restarting, notRunning

        /// Lowercase word for list rows.
        public var word: String {
            switch self {
            case .waiting: "waiting"
            case .working: "working"
            case .idle: "idle"
            case .starting: "starting"
            case .restarting: "restarting"
            case .notRunning: "not running"
            }
        }

        /// Capitalized label for the detail screen.
        public var label: String {
            switch self {
            case .waiting: "Waiting on you"
            case .working: "Working"
            case .idle: "Idle"
            case .starting: "Starting"
            case .restarting: "Restarting"
            case .notRunning: "Not running"
            }
        }

    }

    public var id: String
    public var name: String
    public var project: String
    public var state: State
    /// When the state began (last status change).
    public var since: Date
    /// Your last input (the server's lastInputAt); nil from older iPhone builds.
    public var inputAt: Date?
    /// The server's read state: a finished turn you haven't seen.
    public var unread: Bool?
    /// Background tasks still running (nil or 0 = none).
    public var background: Int?
    /// "question", "plan" or "tool" while something is pending.
    public var kind: String?
    public var question: String?
    public var options: [String]?
    public var questions: [PromptQuestion]?
    public var recap: SessionRecap?
    public var lastText: String?
    /// The server's queue for the session, oldest first (nil or empty = nothing queued).
    public var queue: [Queued]?

    public struct Queued: Codable, Sendable, Equatable, Identifiable {
        public let id: String
        public let text: String
        public init(id: String, text: String) { self.id = id; self.text = text }
    }

    public init(id: String, name: String, project: String, state: State, since: Date, inputAt: Date? = nil,
                unread: Bool? = nil, background: Int? = nil,
                kind: String? = nil, question: String?, options: [String]?, questions: [PromptQuestion]? = nil,
                recap: SessionRecap? = nil, lastText: String?, queue: [Queued]? = nil) {
        self.id = id
        self.name = name
        self.project = project
        self.state = state
        self.since = since
        self.inputAt = inputAt
        self.unread = unread
        self.background = background
        self.kind = kind
        self.question = question
        self.options = options
        self.questions = questions
        self.recap = recap
        self.lastText = lastText
        self.queue = queue
    }

    /// What the list is ordered by and its age shows: your last input.
    public var orderDate: Date { inputAt ?? since }

    /// The status dot, first that applies (docs/plan-watch-app.md → Session list). `readLocally`:
    /// the watch marked it read after this snapshot was fetched, before the server confirmed.
    public func dot(readLocally: Bool = false) -> SessionDot? {
        switch state {
        case .waiting: return .needsYou
        case .working: return .working
        case .idle:
            if (background ?? 0) > 0 { return .background }
            return unread == true && !readLocally ? .unread : nil
        case .starting, .restarting, .notRunning: return nil
        }
    }

    /// What's pending: "question", "plan", "tool" or nil. Older servers send no `kind`, so it's
    /// inferred from the prompt text the server has always used.
    public var pendingKind: String? {
        if let kind { return kind }
        guard let question else { return nil }
        if question == "Approve this plan?" { return "plan" }
        if question.hasPrefix("Allow "), options == ["Yes", "No"] { return "tool" }
        return "question"
    }

    /// The pending questions, falling back to the single first question for older servers.
    public var pendingQuestions: [PromptQuestion] {
        guard pendingKind == "question" else { return [] }
        if let questions, !questions.isEmpty { return questions }
        guard let question else { return [] }
        return [PromptQuestion(question: question, options: options ?? [], multiSelect: false)]
    }

    /// Build from the server's session, following the drawer's status rules.
    public init(_ s: ClaudeSession, lastText: String?) {
        let state: State
        switch s.process {
        case "starting": state = .starting
        case "restarting": state = .restarting
        case "stopped": state = .notRunning
        default:
            switch s.status {
            case .working: state = .working
            case .waitingForInput, .needsPermission, .planMode: state = .waiting
            case .idle, .unknown: state = .idle
            }
        }
        self.init(id: s.id, name: s.displayName, project: s.projectName, state: state, since: s.lastActivity,
                  inputAt: s.orderDate, unread: s.isUnread, background: s.runningBackgroundTasks, kind: s.prompt?.kind, question: s.prompt?.question,
                  options: s.prompt?.options.flatMap { $0.isEmpty ? nil : $0 },
                  questions: s.prompt?.questions.flatMap { $0.isEmpty ? nil : $0 }, recap: s.recap, lastText: lastText,
                  queue: s.queue.flatMap { q in q.isEmpty ? nil : q.map { Queued(id: $0.id, text: $0.text) } })
    }

    /// Short age of your last input (list rows): now, 14s, 2m, 3h, 2d.
    public func age(now: Date = Date()) -> String { Self.shortAge(from: orderDate, now: now) }

    /// How long it has been in its state (detail header).
    public func stateAge(now: Date = Date()) -> String { Self.shortAge(from: since, now: now) }

    private static func shortAge(from date: Date, now: Date) -> String {
        let s = Int(max(0, now.timeIntervalSince(date)))
        if s < 5 { return "now" }
        if s < 60 { return "\(s)s" }
        if s < 3600 { return "\(s / 60)m" }
        if s < 86400 { return "\(s / 3600)h" }
        return "\(s / 86400)d"
    }
}

public enum WatchText {
    /// The start of a Markdown message, kept as Markdown (docs/plan-watch-app.md → Snapshot): past
    /// `limit` characters it's cut at the last line break (else word boundary) and ends with "…",
    /// and a code block the cut left open is closed.
    public static func markdownExcerpt(_ markdown: String, limit: Int = 600) -> String {
        let text = markdown.trimmingCharacters(in: .whitespacesAndNewlines)
        guard text.count > limit else { return text }
        let cut = text.prefix(limit)
        let head: Substring
        if let nl = cut.lastIndex(of: "\n"), cut.distance(from: cut.startIndex, to: nl) > limit / 2 {
            head = cut[..<nl]
        } else if let sp = cut.lastIndex(of: " ") {
            head = cut[..<sp]
        } else {
            head = cut
        }
        var out = head.trimmingCharacters(in: .whitespacesAndNewlines)
        let fences = out.components(separatedBy: "\n").filter { $0.trimmingCharacters(in: .whitespaces).hasPrefix("```") }.count
        if fences % 2 == 1 { out += "\n```\n…" } else { out += "…" }
        return out
    }
}
