import Foundation

public struct DetectedPrompt: Codable, Sendable {
    public let question: String
    public let options: [String]?  // nil for free-text, array for selection

    public init(question: String, options: [String]?) {
        self.question = question
        self.options = options
    }
}

public struct ClaudeSession: Codable, Identifiable, Sendable {
    public let id: String
    public let pid: Int
    public let sessionId: String
    public let cwd: String
    public let startedAt: Date
    public let tmuxWindow: Int
    public let status: SessionStatus
    public let statusSince: Date?
    public let projectName: String
    public let lastOutput: String
    public let prompt: DetectedPrompt?

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
