import Foundation

/// Token usage for one conversation, from GET /sessions/:id/usage.
public struct ModelUsage: Codable, Sendable {
    public let inputTokens: Int
    public let outputTokens: Int
    public let cacheReadTokens: Int
    public let cacheWriteTokens: Int
    public let costUSD: Double?
    public let contextWindow: Int?

    public var totalTokens: Int { inputTokens + outputTokens + cacheReadTokens + cacheWriteTokens }
}

public struct SessionUsage: Codable, Sendable {
    public let inputTokens: Int
    public let outputTokens: Int
    public let cacheReadTokens: Int
    public let cacheWriteTokens: Int
    /// CLI-reported list-price cost; nil when unknown.
    public let costUSD: Double?
    public let turns: Int
    public let contextTokens: Int?
    public let contextWindow: Int?
    public let model: String?
    public let byModel: [String: ModelUsage]
    public let updatedAt: Date?
    /// Part of the history was rebuilt from the transcript, so cost covers only later turns.
    public let partial: Bool?

    public var totalTokens: Int { inputTokens + outputTokens + cacheReadTokens + cacheWriteTokens }
}
