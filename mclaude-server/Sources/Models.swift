import Foundation

enum SessionStatus: String, Codable, Sendable {
    case working
    case waitingForInput = "waiting_for_input"
    case needsPermission = "needs_permission"
    case planMode = "plan_mode"
    case idle
    case unknown
}

struct DetectedPrompt: Codable, Sendable {
    let question: String
    let options: [String]?  // nil for free-text, array for selection
    /// "question" | "plan" | "tool" (docs/plan-watch-app.md → Pending prompt detail)
    var kind: String? = nil
    /// kind "question" only: every question of the pending AskUserQuestion
    var questions: [PromptQuestion]? = nil

    /// A pending AskUserQuestion: `question`/`options` stay the first question (older builds).
    static func question(_ qs: [PromptQuestion]) -> DetectedPrompt? {
        guard let first = qs.first else { return nil }
        return DetectedPrompt(question: first.question, options: first.options, kind: "question", questions: qs)
    }

    static let plan = DetectedPrompt(question: "Approve this plan?", options: nil, kind: "plan")

    static func tool(_ name: String) -> DetectedPrompt {
        DetectedPrompt(question: "Allow \(name)?", options: ["Yes", "No"], kind: "tool")
    }
}

struct PromptQuestion: Codable, Sendable, Equatable {
    let question: String
    let options: [String]   // option labels
    let multiSelect: Bool
}

/// Short "where does this session stand" summary (docs/plan-watch-app.md → Recap).
struct SessionRecap: Codable, Sendable, Equatable {
    let goal: String       // ≤ 80 chars
    let progress: String   // ≤ 120 chars
    let next: String       // ≤ 120 chars
    let nextBy: String     // "you" | "claude" | "none"
    let at: Date
}

struct ClaudeSession: Codable, Sendable {
    let id: String
    let pid: Int
    let sessionId: String
    let cwd: String
    let startedAt: Date
    let name: String         // display label (custom name or cwd basename)
    let status: SessionStatus
    let statusSince: Date?
    let projectName: String
    let lastOutput: String
    let prompt: DetectedPrompt?
    var queue: [QueuedMessage] = []  // held while Claude is busy; editable until delivered
    var contextTokens: Int? = nil   // tokens in Claude's current context (main thread)
    var contextWindow: Int? = nil   // the model's context limit
    var compacting: Bool? = nil     // true while /compact (or auto-compact) runs; omitted otherwise
    var process: String? = nil      // starting | running | restarting | stopped (managed sessions only)
    var recap: SessionRecap? = nil  // managed sessions only; omitted until the first recap exists
}

/// A message the user sent while Claude was mid-turn. The server holds it (so it can still be
/// edited or deleted) and delivers the whole queue as one message at Claude's next step.
struct QueuedMessage: Codable, Sendable {
    let id: String
    var text: String
    let queuedAt: Date
}

enum SendOutcome: Sendable {
    case sent
    case queued(String)
    /// The text answered a pending prompt (question, plan or tool approval); it won't appear as a user message.
    case answered
    case failed

    var ok: Bool { if case .failed = self { return false } else { return true } }
}

struct SessionInput: Codable, Sendable {
    let text: String
    let sendEnter: Bool?
    /// Posted by a program (the Ops dispatcher), not typed: always delivered as a message, never as
    /// the answer to a pending prompt (docs/plan-observability.md → Ops session → Delivery).
    let automated: Bool?
}

struct ClaudeSessionFile: Codable {
    let pid: Int
    let sessionId: String
    let cwd: String
    let startedAt: Double
}

struct SkillInfo: Codable, Sendable {
    let name: String
    let description: String
    let source: String  // "builtin", "global", or project name
}

struct ProjectInfo: Codable, Sendable {
    let name: String
    let path: String
    var lastUsed: Date? = nil   // newest of: mclaude session there, Claude transcript, git activity
    var isGit: Bool? = nil
}

struct CreateSessionRequest: Codable, Sendable {
    let cwd: String
    let token: String?
    let runtime: String?      // omitted (default: headless claude on this host) or "k8s"
    let name: String?         // custom display label (default: cwd basename)
}

// MARK: - Multi-user

struct MCUser: Codable, Sendable {
    let id: String          // stable userId
    let name: String        // display name (from Google profile)
    let email: String?      // Google email
    let googleId: String?   // Google sub claim — stable across token rotations
    let tokenHash: String?  // legacy: Claude OAuth token hash (deprecated)
    let createdAt: Date
}

// MARK: - Google OAuth

struct GoogleTokenResponse: Codable {
    let access_token: String
    let id_token: String
    let token_type: String
    let expires_in: Int?
    let refresh_token: String?
}

struct GoogleUserInfo: Codable {
    let sub: String        // stable Google account ID
    let email: String
    let name: String?
    let picture: String?
}

// MARK: - Structured session events (from JSONL)

enum SessionEventType: String, Codable, Sendable {
    case user
    case text
    case thinking
    case toolUse = "tool_use"
    case toolResult = "tool_result"
    case system
    case compaction
}

struct ToolUseBlock: Codable, Sendable {
    let id: String
    let name: String
    let inputSummary: String
    let fullInput: String?
}

struct ToolResultBlock: Codable, Sendable {
    let toolUseId: String
    let content: String
    let isError: Bool
    /// Server paths of images in the result (e.g. Read of a .png), saved under
    /// /tmp/mclaude-screenshots and served by GET /screenshots/<file>. Nil when none.
    var images: [String]? = nil
}

struct SubagentInfo: Codable, Sendable {
    let agentId: String
    let agentType: String
    let description: String
    let parentToolUseId: String
}

struct SessionEvent: Codable, Sendable {
    let uuid: String
    let timestamp: String
    let type: SessionEventType
    let text: String?
    let thinking: String?
    let toolUse: ToolUseBlock?
    let toolResults: [ToolResultBlock]?
    let model: String?
    let durationMs: Int?
    let subagentInfo: SubagentInfo?
    /// Compaction only: context tokens before and after (from compact_boundary metadata).
    var compactPreTokens: Int? = nil
    var compactPostTokens: Int? = nil
    /// Peer messages (docs/plan-peer-messages.md): one per <cross-session-message> on a user
    /// event, or the one sent by a SendMessage tool_use. `text`/`toolUse` are unchanged.
    var peerMessages: [PeerMessage]? = nil
    /// User event with peerMessages: its text outside the tags and wrapper, when non-empty.
    var peerOtherText: String? = nil
    /// tool_result of a peer SendMessage.
    var peerDelivery: PeerDelivery? = nil

    init(uuid: String, timestamp: String, type: SessionEventType,
         text: String?, thinking: String?, toolUse: ToolUseBlock?,
         toolResults: [ToolResultBlock]?, model: String?, durationMs: Int?,
         subagentInfo: SubagentInfo? = nil, compactPreTokens: Int? = nil, compactPostTokens: Int? = nil) {
        self.uuid = uuid; self.timestamp = timestamp; self.type = type
        self.text = text; self.thinking = thinking; self.toolUse = toolUse
        self.toolResults = toolResults; self.model = model; self.durationMs = durationMs
        self.subagentInfo = subagentInfo
        self.compactPreTokens = compactPreTokens; self.compactPostTokens = compactPostTokens
    }
}

/// GET /sessions/:id/history response.
struct HistoryPage: Codable, Sendable {
    let events: [SessionEvent]
    let hasMore: Bool
}
