import Foundation

/// A Claude Code transcript on the server's laptop that isn't an open session (`GET /transcripts`):
/// an ended MClaude session or one started from a terminal. Shown in the Resume sheet.
public struct TranscriptInfo: Codable, Identifiable, Sendable, Hashable {
    /// Claude session UUID (the transcript's file name).
    public let id: String
    public let cwd: String
    public let projectName: String
    /// Remembered MClaude name, else Claude Code's ai-title. Nil when neither exists.
    public let name: String?
    /// First real user prompt, whitespace-collapsed, capped at 200 characters.
    public let firstPrompt: String?
    public let modifiedAt: Date
    /// Written in the last 2 minutes: may still be open in a terminal.
    public let inUse: Bool

    public init(id: String, cwd: String, projectName: String, name: String?, firstPrompt: String?,
                modifiedAt: Date, inUse: Bool) {
        self.id = id; self.cwd = cwd; self.projectName = projectName; self.name = name
        self.firstPrompt = firstPrompt; self.modifiedAt = modifiedAt; self.inUse = inUse
    }

    /// Name, else first prompt, else project.
    public var displayName: String {
        if let name, !name.isEmpty { return name }
        if let prompt = readablePrompt, !prompt.isEmpty { return prompt }
        return projectName
    }

    /// First line of the first prompt, when it adds something beyond the name.
    public var promptPreview: String? {
        guard let line = readablePrompt?.split(separator: "\n").first.map(String.init), !line.isEmpty,
              line != displayName else { return nil }
        return line
    }

    /// The first prompt without upload paths (`/tmp/mclaude-screenshots/…`) or the `[video: …]` note
    /// the app adds to attachment messages. A message that was only attachments reads "Photo" or "Video".
    public var readablePrompt: String? {
        Self.readable(firstPrompt)
    }

    static func readable(_ prompt: String?) -> String? {
        guard var text = prompt, !text.isEmpty else { return nil }
        let hadVideo = text.contains("[video:")
        text = text.replacingOccurrences(of: #"\[video:[^\]]*(\]|$)"#, with: " ", options: .regularExpression)
        let words = text.split(separator: " ", omittingEmptySubsequences: true)
        let kept = words.filter { !$0.hasPrefix("/tmp/mclaude-screenshots/") }
        let cleaned = kept.joined(separator: " ").trimmingCharacters(in: .whitespacesAndNewlines)
        if !cleaned.isEmpty { return cleaned }
        if hadVideo { return "Video" }
        return kept.count < words.count ? "Photo" : nil
    }
}
