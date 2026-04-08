import Foundation

public enum SessionEventType: String, Codable, Sendable {
    case user
    case text
    case thinking
    case toolUse = "tool_use"
    case toolResult = "tool_result"
    case system
    case compaction
}

public struct ToolUseBlock: Codable, Sendable {
    public let id: String
    public let name: String
    public let inputSummary: String
    public let fullInput: String?

    public init(id: String, name: String, inputSummary: String, fullInput: String? = nil) {
        self.id = id
        self.name = name
        self.inputSummary = inputSummary
        self.fullInput = fullInput
    }
}

public struct ToolResultBlock: Codable, Sendable {
    public let toolUseId: String
    public let content: String
    public let isError: Bool

    public init(toolUseId: String, content: String, isError: Bool) {
        self.toolUseId = toolUseId
        self.content = content
        self.isError = isError
    }
}

public struct SubagentInfo: Codable, Sendable {
    public let agentId: String
    public let agentType: String
    public let description: String
    public let parentToolUseId: String

    public init(agentId: String, agentType: String, description: String, parentToolUseId: String) {
        self.agentId = agentId
        self.agentType = agentType
        self.description = description
        self.parentToolUseId = parentToolUseId
    }
}

public struct SessionEvent: Codable, Sendable, Identifiable {
    public let uuid: String
    public let timestamp: String
    public let type: SessionEventType
    public let text: String?
    public let thinking: String?
    public let toolUse: ToolUseBlock?
    public let toolResults: [ToolResultBlock]?
    public let model: String?
    public let durationMs: Int?
    public let subagentInfo: SubagentInfo?

    public var id: String { uuid }

    public init(uuid: String, timestamp: String, type: SessionEventType, text: String?, thinking: String?, toolUse: ToolUseBlock?, toolResults: [ToolResultBlock]?, model: String?, durationMs: Int?, subagentInfo: SubagentInfo? = nil) {
        self.uuid = uuid
        self.timestamp = timestamp
        self.type = type
        self.text = text
        self.thinking = thinking
        self.toolUse = toolUse
        self.toolResults = toolResults
        self.model = model
        self.durationMs = durationMs
        self.subagentInfo = subagentInfo
    }
}
