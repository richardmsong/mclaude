import Foundation

public struct ProjectInfo: Codable, Identifiable, Sendable {
    public let name: String
    public let path: String

    public var id: String { path }
}
