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
}

struct ClaudeSession: Codable, Sendable {
    let id: String
    let pid: Int
    let sessionId: String
    let cwd: String
    let startedAt: Date
    let tmuxWindow: Int
    let tmuxSession: String  // which tmux session this window lives in
    let windowName: String   // tmux window name (custom label or cwd basename)
    let status: SessionStatus
    let statusSince: Date?
    let projectName: String
    let lastOutput: String
    let prompt: DetectedPrompt?
}

struct SessionInput: Codable, Sendable {
    let text: String
    let sendEnter: Bool?
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
}

struct CreateSessionRequest: Codable, Sendable {
    let cwd: String
    let token: String?
    let runtime: String?      // "tmux" (default) or "k8s"
    let tmuxSession: String?  // target tmux session name (default: server's configured session)
    let windowName: String?   // custom tmux window name (default: cwd basename)
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

    init(uuid: String, timestamp: String, type: SessionEventType,
         text: String?, thinking: String?, toolUse: ToolUseBlock?,
         toolResults: [ToolResultBlock]?, model: String?, durationMs: Int?,
         subagentInfo: SubagentInfo? = nil) {
        self.uuid = uuid; self.timestamp = timestamp; self.type = type
        self.text = text; self.thinking = thinking; self.toolUse = toolUse
        self.toolResults = toolResults; self.model = model; self.durationMs = durationMs
        self.subagentInfo = subagentInfo
    }
}
