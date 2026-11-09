import Foundation

/// Infers a short session title from the first prompt with a one-shot `claude -p --model haiku`
/// call. Runs in a scratch directory with --no-session-persistence so it leaves no transcript in
/// the project's history. Best effort: any failure or timeout just keeps the default name.
enum SessionTitler {
    static let timeout: TimeInterval = 45
    static let maxLength = 60
    static let maxWords = 8
    /// Used when the model's title has no emoji, so every inferred title carries one.
    static let fallbackEmoji = "💬"

    /// Phrases that mean the model answered (refused, explained, asked back) instead of titling.
    static let replyMarkers = [
        "i can't", "i cannot", "i can not", "i'm unable", "i am unable", "i'm sorry", "i apologize",
        "i'd be happy", "i need", "i don't", "this request", "as an ai", "please provide", "could you",
    ]

    /// True if the line reads like a sentence/refusal rather than a title.
    static func looksLikeReply(_ line: String) -> Bool {
        if line.split(separator: " ").count > maxWords { return true }
        let lower = line.lowercased().replacingOccurrences(of: "’", with: "'")
        return replyMarkers.contains { lower.contains($0) }
    }

    /// The message with uploaded photo/file/video paths and video notes ("[video: …]") removed
    /// (what the user actually typed).
    static func typedText(of message: String) -> String {
        message
            .replacingOccurrences(of: #"\[video:[^\]]*\]"#, with: " ", options: .regularExpression)
            .split(whereSeparator: { $0 == " " || $0 == "\n" })
            .filter { !($0.hasPrefix("/tmp/mclaude-screenshots/") || $0.hasPrefix("/tmp/mclaude-files/")) }
            .joined(separator: " ")
    }

    /// Slash commands (/clear, /model …) aren't real prompts, so they don't name a session.
    /// Photo messages start with "/tmp/…" paths, which must not count as a slash command.
    static func shouldInferTitle(fromFirstMessage message: String) -> Bool {
        let typed = typedText(of: message)
        let isAttachmentOnly = typed.isEmpty && (message.contains("/tmp/mclaude-screenshots/") || message.contains("/tmp/mclaude-files/"))
        return isAttachmentOnly || (!typed.isEmpty && !typed.hasPrefix("/"))
    }

    /// Prompt text sent to the title model (image/file paths removed, long prompts truncated).
    static func titlePrompt(for message: String) -> String {
        let cleaned = typedText(of: message)
        let request = cleaned.isEmpty ? "(the user attached images without any text)" : String(cleaned.prefix(2000))
        return """
        Write a short title for a conversation that starts with the message below: one emoji \
        that fits the topic, a space, then 3 to 6 words. The conversation can be about anything: \
        code, writing, questions, personal topics. Always reply with a title that names the \
        topic, never a refusal, explanation or question. Use Title Case. No quotes, no other \
        emoji, no trailing punctuation. Reply with only the title, e.g. "🐛 Fix Login Token Refresh".

        Message:
        \(request)
        """
    }

    /// True for a character that renders as an emoji (not plain digits, `#` or `*`).
    static func isEmoji(_ c: Character) -> Bool {
        guard let first = c.unicodeScalars.first else { return false }
        return first.properties.isEmojiPresentation || (first.properties.isEmoji && c.unicodeScalars.count > 1)
    }

    /// Normalise model output into `<emoji> <Title>`, or nil if unusable (empty, or a reply/refusal).
    /// The first emoji anywhere in the line becomes the prefix; others are dropped. No emoji → 💬.
    static func sanitize(_ raw: String) -> String? {
        guard var line = raw.split(whereSeparator: \.isNewline)
            .map({ $0.trimmingCharacters(in: .whitespaces) })
            .first(where: { !$0.isEmpty }) else { return nil }
        for prefix in ["Title:", "title:", "TITLE:"] where line.hasPrefix(prefix) {
            line = String(line.dropFirst(prefix.count)).trimmingCharacters(in: .whitespaces)
        }
        let emoji = line.first(where: isEmoji).map(String.init) ?? fallbackEmoji
        // Drop emoji plus the variation selectors / joiners left around them.
        line = String(line.filter { !isEmoji($0) && !$0.unicodeScalars.allSatisfy { $0.value == 0xFE0F || $0.value == 0x200D } })
        let strip = CharacterSet(charactersIn: "\"'`*_#“”‘’ ")
        // Quotes and trailing punctuation can nest ("“Title”!"), so strip until stable.
        var previous = ""
        while previous != line {
            previous = line
            line = line.trimmingCharacters(in: strip)
            while let last = line.last, ".!?:;,".contains(last) { line.removeLast() }
        }
        line = line.split(separator: " ", omittingEmptySubsequences: true).joined(separator: " ")
        guard !line.isEmpty, !looksLikeReply(line) else { return nil }
        if line.count > maxLength {
            line = String(line.prefix(maxLength)).trimmingCharacters(in: .whitespaces) + "…"
        }
        return "\(emoji) \(line)"
    }

    /// Run the title model. Returns nil on error/timeout.
    static func infer(from message: String, claudePath: String, oauthToken: String?) async -> String? {
        let prompt = titlePrompt(for: message)
        return await withCheckedContinuation { continuation in
            DispatchQueue.global(qos: .utility).async {
                continuation.resume(returning: run(prompt: prompt, claudePath: claudePath, oauthToken: oauthToken))
            }
        }
    }

    private static func run(prompt: String, claudePath: String, oauthToken: String?) -> String? {
        let scratch = NSTemporaryDirectory() + "mclaude-titles"
        try? FileManager.default.createDirectory(atPath: scratch, withIntermediateDirectories: true)

        let process = Process()
        process.executableURL = URL(fileURLWithPath: claudePath)
        process.arguments = ["-p", prompt, "--model", "haiku", "--output-format", "text", "--no-session-persistence"]
        process.currentDirectoryURL = URL(fileURLWithPath: scratch)
        var env = ProcessInfo.processInfo.environment
        if let oauthToken { env["CLAUDE_CODE_OAUTH_TOKEN"] = oauthToken }
        let extra = ["/opt/homebrew/bin", "/usr/local/bin", "\(NSHomeDirectory())/.local/bin", "/usr/bin", "/bin"]
        env["PATH"] = (extra + (env["PATH"]?.split(separator: ":").map(String.init) ?? [])).joined(separator: ":")
        process.environment = env
        let out = Pipe()
        process.standardOutput = out
        process.standardError = FileHandle.nullDevice
        process.standardInput = FileHandle.nullDevice

        do { try process.run() } catch { return nil }
        let deadline = DispatchTime.now() + timeout
        let done = DispatchSemaphore(value: 0)
        process.terminationHandler = { _ in done.signal() }
        if done.wait(timeout: deadline) == .timedOut {
            process.terminate()
            print("[titles] title inference timed out")
            return nil
        }
        guard process.terminationStatus == 0 else { return nil }
        let data = out.fileHandleForReading.readDataToEndOfFile()
        return sanitize(String(decoding: data, as: UTF8.self))
    }
}
