import Foundation

/// A tool call Claude Code's auto mode classifier denied (docs/design-claude-uplift.md →
/// Permissions (auto mode)). Headless Claude never turns a block into a permission request; it
/// returns an error tool result like "Permission for this action was denied by the Claude Code
/// auto mode classifier. Reason: [Git Destructive]. …" and carries on.
enum AutoModeBlock {
    static func isBlock(_ result: ToolResultBlock) -> Bool {
        result.isError && result.content.contains("auto mode classifier")
    }

    /// The rule name from "Reason: [<rule>]", or nil when the text has none.
    static func rule(_ content: String) -> String? {
        guard let start = content.range(of: "Reason: [") else { return nil }
        let rest = content[start.upperBound...]
        guard let end = rest.firstIndex(of: "]") else { return nil }
        let rule = rest[..<end].trimmingCharacters(in: .whitespaces)
        return rule.isEmpty ? nil : rule
    }

    /// Push body: "Blocked: <tool> · <rule>", or "Blocked: <tool>" with no rule name.
    static func pushBody(toolName: String, content: String) -> String {
        guard let rule = rule(content) else { return "Blocked: \(toolName)" }
        return "Blocked: \(toolName) · \(rule)"
    }
}
