import Foundation

public struct ProjectInfo: Codable, Identifiable, Sendable {
    public let name: String
    public let path: String
    /// Most recent of: an mclaude session here, a Claude transcript here, the last git commit.
    /// Nil if unknown or the server predates this field.
    public let lastUsed: Date?
    /// True if the folder is a git repo. Nil if the server predates this field.
    public let isGit: Bool?

    public var id: String { path }
}
