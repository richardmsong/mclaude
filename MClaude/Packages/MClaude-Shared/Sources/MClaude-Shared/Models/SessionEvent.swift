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
    /// Server paths of images in the result (e.g. Read of a .png); fetch with APIClient.fetchImage.
    public let images: [String]?

    public init(toolUseId: String, content: String, isError: Bool, images: [String]? = nil) {
        self.toolUseId = toolUseId
        self.content = content
        self.isError = isError
        self.images = images
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
    /// Compaction only: context tokens before and after (nil on older servers).
    public var compactPreTokens: Int? = nil
    public var compactPostTokens: Int? = nil
    /// Peer messages (docs/plan-peer-messages.md → Event enrichment; nil on older servers):
    /// on a `user` event, one per `<cross-session-message>` tag; on a peer `SendMessage`
    /// tool_use, one `sent` entry.
    public var peerMessages: [PeerMessage]? = nil
    /// `user` events with `peerMessages` only: the text outside the tags (shown as a bubble).
    public var peerOtherText: String? = nil
    /// On the tool_result of a peer `SendMessage`.
    public var peerDelivery: PeerDelivery? = nil
    /// The server marked this user line as the start of a quiet peer turn (docs/plan-peer-messages.md
    /// → Event enrichment): the transcript folds it. Mid-turn deliveries and replies to your requests aren't.
    public var quiet: Bool? = nil

    public var id: String { uuid }

    public init(uuid: String, timestamp: String, type: SessionEventType, text: String?, thinking: String?, toolUse: ToolUseBlock?, toolResults: [ToolResultBlock]?, model: String?, durationMs: Int?, subagentInfo: SubagentInfo? = nil, compactPreTokens: Int? = nil, compactPostTokens: Int? = nil) {
        self.compactPreTokens = compactPreTokens
        self.compactPostTokens = compactPostTokens
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
