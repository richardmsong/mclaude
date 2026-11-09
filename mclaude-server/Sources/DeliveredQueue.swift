import Foundation

/// Queued messages already written to Claude's stdin, kept until Claude's copy of them reaches
/// the transcript (docs/design-claude-uplift.md → Queue group → Delivered messages). Claude only
/// takes stdin at its next step, which can be minutes later; until then the app shows them as
/// "Delivered" so they never vanish from the screen.
struct DeliveredQueue: Sendable {
    private(set) var items: [QueuedMessage] = []

    /// Dropped at the latest this long after delivery.
    static let safetyCap: TimeInterval = 600
    /// Still here this long after a turn ended: Claude won't echo it (e.g. some slash commands).
    static let turnEndGrace: TimeInterval = 10

    var isEmpty: Bool { items.isEmpty }
    var ids: [String] { items.map(\.id) }

    mutating func add(_ batch: [QueuedMessage], at date: Date) {
        items += batch.map { var m = $0; m.deliveredAt = date; return m }
    }

    /// A top-level user message from Claude: remove and return the items it carries. Each item
    /// consumes its own occurrence of its text, oldest first, so one message holding a whole
    /// delivered batch clears all of it, and a repeated short text only clears one item.
    mutating func read(_ text: String) -> [QueuedMessage] {
        guard !items.isEmpty else { return [] }
        var rest = Self.normalize(text)
        guard !rest.isEmpty else { return [] }
        var matched: [QueuedMessage] = []
        var kept: [QueuedMessage] = []
        for item in items {
            let wanted = Self.normalize(item.text)
            // Exact text, or a shared 30-character start (as the app matches placeholders)
            let needle = rest.contains(wanted) ? wanted : (wanted.count >= 30 ? String(wanted.prefix(30)) : nil)
            if !wanted.isEmpty, let needle, let range = rest.range(of: needle) {
                rest.removeSubrange(range)
                matched.append(item)
            } else {
                kept.append(item)
            }
        }
        items = kept
        return matched
    }

    /// Remove the items with these ids (those still here) and return them.
    mutating func remove(ids: Set<String>) -> [QueuedMessage] {
        let gone = items.filter { ids.contains($0.id) }
        items.removeAll { ids.contains($0.id) }
        return gone
    }

    mutating func removeAll() -> [QueuedMessage] {
        defer { items = [] }
        return items
    }

    private static let imageTag = try! NSRegularExpression(pattern: #"\[Image #\d+\]"#)

    /// Text as compared: "[Image #N]" tags dropped, whitespace runs collapsed to one space, trimmed.
    static func normalize(_ text: String) -> String {
        let untagged = imageTag.stringByReplacingMatches(in: text, range: NSRange(text.startIndex..., in: text),
                                                         withTemplate: " ")
        return untagged.split(whereSeparator: { $0.isWhitespace }).joined(separator: " ")
    }
}
