import Foundation

public struct SkillSuggestion: Codable, Identifiable, Sendable {
    public let name: String
    public let description: String
    public let source: String  // "builtin", "global", or project name

    public var id: String { "\(source):\(name)" }

    /// Display name with "/" prefix
    public var command: String { "/\(name)" }
}
