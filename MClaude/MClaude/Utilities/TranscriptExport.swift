import Foundation

/// Text exports of a conversation (docs/design-claude-uplift.md → Export).
enum TranscriptExport {
    private static let photoPathPattern = try! NSRegularExpression(
        pattern: #"/tmp/mclaude-screenshots/screenshot-\d+\.(?:png|jpe?g)"#
    )
    private static let videoPattern = try! NSRegularExpression(
        pattern: #"/tmp/mclaude-screenshots/video-\d+\.(?:mov|mp4)(?: \[video:[^\]]*\])?"#
    )

    // MARK: - Single event

    /// Raw text of one event, as shown in the Event Detail sheet.
    static func rawText(of event: SessionEvent) -> String {
        switch event.type {
        case .user, .text, .compaction: return event.text ?? ""
        case .thinking: return event.thinking ?? ""
        case .toolUse: return event.toolUse?.fullInput ?? event.toolUse?.inputSummary ?? ""
        case .toolResult: return (event.toolResults ?? []).map(\.content).joined(separator: "\n\n")
        case .system: return event.durationMs.map { "Duration: \($0)ms" } ?? ""
        }
    }

    // MARK: - Whole conversation

    static func markdown(events: [SessionEvent], title: String, cwd: String, exportedAt: Date = Date()) -> String {
        var out = ["# \(title)", "", "\(cwd) · exported \(timestamp(exportedAt))", ""]
        for e in topLevel(events) {
            switch e.type {
            case .user:
                guard let msg = userMessage(e) else { continue }
                out += ["**You:**", msg, ""]
            case .text:
                guard let t = e.text, !t.isEmpty else { continue }
                out += [t, ""]
            case .toolUse:
                guard let tool = e.toolUse else { continue }
                out += ["> 🔧 `\(tool.name)` \(oneLine(tool.inputSummary))", ""]
            case .compaction:
                out += ["--- Context compacted ---", ""]
            case .thinking:
                guard let t = e.thinking?.trimmingCharacters(in: .whitespacesAndNewlines), !t.isEmpty else { continue }
                out += ["> 💭 *Thinking*", ">"] + t.components(separatedBy: "\n").map { $0.isEmpty ? ">" : "> \($0)" } + [""]
            case .toolResult, .system:
                continue
            }
        }
        return out.joined(separator: "\n")
    }

    static func plainText(events: [SessionEvent], title: String, cwd: String, exportedAt: Date = Date()) -> String {
        var out = [title, "\(cwd) · exported \(timestamp(exportedAt))", ""]
        for e in topLevel(events) {
            switch e.type {
            case .user:
                guard let msg = userMessage(e) else { continue }
                out += ["You:", plain(msg), ""]
            case .text:
                guard let t = e.text, !t.isEmpty else { continue }
                out += ["Claude:", plain(t), ""]
            case .toolUse:
                guard let tool = e.toolUse else { continue }
                out += ["[\(tool.name)] \(oneLine(tool.inputSummary))", ""]
            case .compaction:
                out += ["--- Context compacted ---", ""]
            case .thinking:
                guard let t = e.thinking?.trimmingCharacters(in: .whitespacesAndNewlines), !t.isEmpty else { continue }
                out += ["Thinking:", t, ""]
            case .toolResult, .system:
                continue
            }
        }
        return out.joined(separator: "\n")
    }

    /// Markdown resolved to plain text: inline markup removed (links keep their text), fenced code
    /// keeps its content without the fences, heading markers dropped.
    static func plain(_ markdown: String) -> String {
        var lines: [String] = []
        var inFence = false
        var prose: [String] = []
        func flushProse() {
            guard !prose.isEmpty else { return }
            let joined = prose.joined(separator: "\n")
            if let attr = try? AttributedString(markdown: joined,
                                                options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)) {
                lines.append(String(attr.characters))
            } else {
                lines.append(joined)
            }
            prose = []
        }
        for line in markdown.components(separatedBy: "\n") {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if trimmed.hasPrefix("```") {
                flushProse()
                inFence.toggle()
                continue
            }
            if inFence {
                lines.append(line)
            } else if let heading = trimmed.range(of: #"^#{1,6}\s+"#, options: .regularExpression) {
                prose.append(String(trimmed[heading.upperBound...]))
            } else {
                prose.append(line)
            }
        }
        flushProse()
        return lines.joined(separator: "\n")
    }

    // MARK: - Files

    /// Write `data` to a temp file named "<title> <yyyy-MM-dd>.<ext>" for the share sheet.
    static func writeTempFile(_ data: Data, title: String, ext: String, date: Date = Date()) throws -> URL {
        let day = DateFormatter()
        day.dateFormat = "yyyy-MM-dd"
        let unsafe = CharacterSet(charactersIn: "/\\:?%*|\"<>").union(.newlines).union(.controlCharacters)
        let safeTitle = title.components(separatedBy: unsafe).joined(separator: "-")
            .trimmingCharacters(in: .whitespaces)
        let name = "\(safeTitle.isEmpty ? "Conversation" : safeTitle) \(day.string(from: date)).\(ext)"
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("exports", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let url = dir.appendingPathComponent(name)
        try data.write(to: url, options: .atomic)
        return url
    }

    // MARK: - Helpers

    private static func topLevel(_ events: [SessionEvent]) -> [SessionEvent] {
        events.filter { $0.subagentInfo == nil }
    }

    /// User-visible message text: nil for tool-result-only user events and interruption notices;
    /// photo paths become "[photo]", videos (path + frame note) "[video]".
    private static func userMessage(_ e: SessionEvent) -> String? {
        guard var text = e.text, !text.isEmpty, !text.hasPrefix("[Request interrupted") else { return nil }
        let range = NSRange(text.startIndex..., in: text)
        text = photoPathPattern.stringByReplacingMatches(in: text, range: range, withTemplate: "[photo]")
        text = videoPattern.stringByReplacingMatches(in: text, range: NSRange(text.startIndex..., in: text),
                                                     withTemplate: "[video]")
        text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        return text.isEmpty ? nil : text
    }

    private static func oneLine(_ s: String) -> String {
        s.replacingOccurrences(of: "\n", with: " ").trimmingCharacters(in: .whitespaces)
    }

    private static func timestamp(_ date: Date) -> String {
        let f = DateFormatter()
        f.dateStyle = .medium
        f.timeStyle = .short
        return f.string(from: date)
    }
}
