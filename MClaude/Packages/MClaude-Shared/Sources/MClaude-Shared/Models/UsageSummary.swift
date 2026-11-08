import Foundation

/// Tokens and estimated list-price cost (GET /usage/summary). Addable, so days roll up into windows.
public struct UsageTotals: Codable, Sendable, Hashable {
    public var inputTokens: Int
    public var outputTokens: Int
    public var cacheReadTokens: Int
    public var cacheWriteTokens: Int
    public var costUSD: Double

    public init(inputTokens: Int = 0, outputTokens: Int = 0, cacheReadTokens: Int = 0,
                cacheWriteTokens: Int = 0, costUSD: Double = 0) {
        self.inputTokens = inputTokens; self.outputTokens = outputTokens
        self.cacheReadTokens = cacheReadTokens; self.cacheWriteTokens = cacheWriteTokens; self.costUSD = costUSD
    }

    public var totalTokens: Int { inputTokens + outputTokens + cacheReadTokens + cacheWriteTokens }

    public static func + (a: UsageTotals, b: UsageTotals) -> UsageTotals {
        UsageTotals(inputTokens: a.inputTokens + b.inputTokens, outputTokens: a.outputTokens + b.outputTokens,
                    cacheReadTokens: a.cacheReadTokens + b.cacheReadTokens,
                    cacheWriteTokens: a.cacheWriteTokens + b.cacheWriteTokens, costUSD: a.costUSD + b.costUSD)
    }
}

public struct UsageDay: Codable, Sendable {
    /// "yyyy-MM-dd" in the requested time zone.
    public let date: String
    public let byModel: [String: UsageTotals]
    public let byProject: [String: UsageTotals]

    public var total: UsageTotals { byModel.values.reduce(UsageTotals(), +) }
}

public struct UsageSummary: Codable, Sendable {
    /// Oldest first; days without usage are omitted.
    public let days: [UsageDay]
    public let unpricedModels: [String]
    public let byProvider: [String: UsageTotals]?
}
