import Foundation

public struct DetectedPrompt: Codable, Sendable {
    public let question: String
    public let options: [String]?  // nil for free-text, array for selection
    /// "question", "plan" or "tool" (nil on older servers). docs/plan-watch-app.md → Pending prompt detail.
    public let kind: String?
    /// Every question of a pending AskUserQuestion (nil on older servers / other kinds).
    public let questions: [PromptQuestion]?

    public init(question: String, options: [String]?, kind: String? = nil, questions: [PromptQuestion]? = nil) {
        self.question = question
        self.options = options
        self.kind = kind
        self.questions = questions
    }
}

public struct PromptQuestion: Codable, Sendable, Equatable {
    public let question: String
    public let options: [String]
    public let multiSelect: Bool

    public init(question: String, options: [String], multiSelect: Bool) {
        self.question = question
        self.options = options
        self.multiSelect = multiSelect
    }
}

/// Where a session stands, made by the server after each turn (docs/plan-watch-app.md → Recap).
public struct SessionRecap: Codable, Sendable, Equatable {
    public let goal: String
    public let progress: String
    public let next: String
    /// "you", "claude" or "none".
    public let nextBy: String
    public let at: Date?

    public init(goal: String, progress: String, next: String, nextBy: String, at: Date?) {
        self.goal = goal
        self.progress = progress
        self.next = next
        self.nextBy = nextBy
        self.at = at
    }
}

/// A message held in the server's per-session queue, delivered to Claude at its next step
/// (or at the end of the turn). `text` is the exact message (photo paths first, then caption).
public struct QueuedMessage: Codable, Identifiable, Sendable, Equatable {
    public let id: String
    public let text: String
    public let queuedAt: Date

    public init(id: String, text: String, queuedAt: Date) {
        self.id = id
        self.text = text
        self.queuedAt = queuedAt
    }
}

/// The status dot shown for a session (docs/design-claude-uplift.md → Drawer → Session list).
public enum SessionDot: String, Codable, Sendable {
    /// Blue, filled: waiting for input, a permission or a plan.
    case needsYou
    /// Clay, filled.
    case working
    /// Clay ring: idle, background work still running.
    case background
    /// Green, filled: a finished turn you haven't seen.
    case unread
}

public struct ClaudeSession: Codable, Identifiable, Sendable {
    public let id: String
    public let pid: Int
    public let sessionId: String
    public let cwd: String
    public let startedAt: Date
    public let status: SessionStatus
    public let statusSince: Date?
    public let projectName: String
    /// Display label from the server (custom name or cwd basename). Optional for older servers.
    public let name: String?
    public let lastOutput: String
    public let prompt: DetectedPrompt?
    public let laptop: String?
    /// Tokens in Claude's current context and the model's limit (nil on older servers).
    public let contextTokens: Int?
    public let contextWindow: Int?
    /// Messages the server is holding for Claude's next step (nil on older servers = empty).
    public let queue: [QueuedMessage]?
    /// True while /compact (or auto-compact) runs (nil = not compacting / older server).
    public let compacting: Bool?
    /// Server-side process state: "starting", "running", "restarting" or "stopped" (nil = k8s
    /// session or older server). docs/design-claude-uplift.md → Session processes.
    public let process: String?
    /// Server-made recap (nil until the first one exists, or on older servers).
    public let recap: SessionRecap?
    /// The last time you gave this session input (nil on older servers / k8s).
    /// docs/design-claude-uplift.md → Session order, read state and background work.
    public let lastInputAt: Date?
    /// A turn finished that you haven't seen (server-side read state; nil = read).
    public let unread: Bool?
    /// During a turn a peer message started: the peer's display name.
    public let peerTurn: String?
    /// …and the peer's Claude session id, when resolved (so its session name can be shown).
    public let peerTurnPeer: String?
    /// Background tasks Claude started that are still running (nil = none).
    public let backgroundTasks: Int?

    /// What the list is ordered by: your last input (older servers: the last status change).
    public var orderDate: Date { lastInputAt ?? lastActivity }

    public var isUnread: Bool { unread == true }

    public var runningBackgroundTasks: Int { max(0, backgroundTasks ?? 0) }

    /// The drawer/header dot (first that applies), or nil for none. No dot while the process is down.
    public var dot: SessionDot? {
        if processDown { return nil }
        switch status {
        case .waitingForInput, .needsPermission, .planMode: return .needsYou
        case .working: return .working
        case .idle, .unknown: break
        }
        if runningBackgroundTasks > 0 { return .background }
        if isUnread { return .unread }
        return nil
    }

    /// The process isn't up and ready, so `status` doesn't describe the session.
    public var processDown: Bool { process != nil && process != "running" }

    /// Lowercase status word for the drawer: the process state while it's down, else the status.
    public var statusWord: String { statusWord(peerName: nil) }

    /// `statusWord`, naming the peer of a peer turn with `peerName` (its open session's name).
    public func statusWord(peerName: String?) -> String {
        switch process {
        case "starting": return "starting"
        case "restarting": return "restarting"
        case "stopped": return "not running"
        default:
            if status == .working, let peer = peerName ?? peerTurn, !peer.isEmpty { return "replying to \(peer)" }
            if status == .idle, runningBackgroundTasks > 0 { return "\(runningBackgroundTasks) in background" }
            return status.label.lowercased()
        }
    }

    /// Capitalized form of `statusWord`, for menus.
    public var statusLabel: String { processDown ? statusWord.prefix(1).uppercased() + statusWord.dropFirst() : status.label }

    /// 0...1 share of the context window in use, if known.
    public var contextFraction: Double? {
        guard let used = contextTokens, let limit = contextWindow, limit > 0 else { return nil }
        return min(1, Double(used) / Double(limit))
    }

    public var displayName: String { (name?.isEmpty == false ? name : nil) ?? projectName }

    /// Best available "last activity" time (status change, else start).
    public var lastActivity: Date { statusSince ?? startedAt }

    public var uptime: String {
        let interval = Date().timeIntervalSince(startedAt)
        let hours = Int(interval) / 3600
        let minutes = (Int(interval) % 3600) / 60
        if hours > 0 {
            return "\(hours)h \(minutes)m"
        }
        return "\(minutes)m"
    }

    public var statusDuration: String? {
        guard let since = statusSince else { return nil }
        let interval = Date().timeIntervalSince(since)
        let hours = Int(interval) / 3600
        let minutes = (Int(interval) % 3600) / 60
        let seconds = Int(interval) % 60
        if hours > 0 {
            return "\(hours)h \(minutes)m"
        }
        if minutes > 0 {
            return "\(minutes)m \(seconds)s"
        }
        return "\(seconds)s"
    }
}
