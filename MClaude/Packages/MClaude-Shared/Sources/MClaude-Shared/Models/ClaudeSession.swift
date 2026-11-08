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

    /// The process isn't up and ready, so `status` doesn't describe the session.
    public var processDown: Bool { process != nil && process != "running" }

    /// Lowercase status word for the drawer: the process state while it's down, else the status.
    public var statusWord: String {
        switch process {
        case "starting": return "starting"
        case "restarting": return "restarting"
        case "stopped": return "not running"
        default: return status.label.lowercased()
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
