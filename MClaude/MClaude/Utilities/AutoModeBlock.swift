import Foundation

/// Recognizes a tool call that Claude Code's auto mode classifier denied
/// (docs/design-claude-uplift.md → Permissions (auto mode)). The denial arrives as an error
/// tool result, e.g. "Permission for this action was denied by the Claude Code auto mode
/// classifier. Reason: [Git Destructive]. …", identical live and in the transcript file.
enum AutoModeBlock {
    static func isBlock(_ result: ToolResultBlock) -> Bool {
        result.isError && result.content.contains("auto mode classifier")
    }

    static func inEvent(_ event: SessionEvent) -> Bool {
        event.type == .toolResult && (event.toolResults ?? []).contains(where: isBlock)
    }

    /// The rule name from "Reason: [<rule>]", or nil when the text has none.
    static func rule(_ content: String) -> String? {
        guard let start = content.range(of: "Reason: [") else { return nil }
        let rest = content[start.upperBound...]
        guard let end = rest.firstIndex(of: "]") else { return nil }
        let rule = rest[..<end].trimmingCharacters(in: .whitespaces)
        return rule.isEmpty ? nil : rule
    }
}
