import SwiftUI

/// A background-task notification Claude Code injects into the conversation
/// (`<task-notification>…</task-notification>`): agents, background shell commands, monitors.
struct TaskNotice: Identifiable, Equatable {
    let id: String          // task id (stable), falls back to the block's index
    let status: String      // completed / failed / stopped / running …
    let summary: String
    let result: String?
    let outputFile: String?

    var icon: String {
        switch status.lowercased() {
        case "completed", "succeeded", "success": "checkmark.circle"
        case "failed", "error": "xmark.circle"
        case "stopped", "killed", "cancelled", "canceled", "expired": "stop.circle"
        default: "clock"
        }
    }

    var tint: Color {
        switch status.lowercased() {
        case "completed", "succeeded", "success": Theme.success
        case "failed", "error": Theme.danger
        default: Theme.textMuted
        }
    }
}

enum TaskNotificationParser {
    private static let block = try! NSRegularExpression(
        pattern: #"<task-notification>(.*?)</task-notification>"#, options: [.dotMatchesLineSeparators])
    private static let wrappers = try! NSRegularExpression(
        pattern: #"</?system-reminder>|\[SYSTEM NOTIFICATION - NOT USER INPUT\][^\n]*\n?"#)

    static func containsNotice(_ text: String) -> Bool { text.contains("<task-notification>") }

    /// Notices in the text plus whatever real text is left once they (and their wrappers) are removed.
    static func parse(_ text: String) -> (notices: [TaskNotice], remainder: String) {
        let ns = text as NSString
        var notices: [TaskNotice] = []
        for (i, match) in block.matches(in: text, range: NSRange(location: 0, length: ns.length)).enumerated() {
            let body = ns.substring(with: match.range(at: 1))
            let summary = tag("summary", in: body) ?? "Background task update"
            notices.append(TaskNotice(
                id: tag("task-id", in: body) ?? "notice-\(i)",
                status: tag("status", in: body) ?? "",
                summary: summary,
                result: tag("result", in: body),
                outputFile: tag("output-file", in: body)))
        }
        var rest = block.stringByReplacingMatches(in: text, range: NSRange(location: 0, length: ns.length), withTemplate: "")
        rest = wrappers.stringByReplacingMatches(in: rest, range: NSRange(location: 0, length: (rest as NSString).length), withTemplate: "")
        return (notices, rest.trimmingCharacters(in: .whitespacesAndNewlines))
    }

    private static func tag(_ name: String, in body: String) -> String? {
        guard let start = body.range(of: "<\(name)>"),
              let end = body.range(of: "</\(name)>", range: start.upperBound..<body.endIndex) else { return nil }
        let value = body[start.upperBound..<end.lowerBound].trimmingCharacters(in: .whitespacesAndNewlines)
        return value.isEmpty ? nil : value
    }
}

/// Compact, muted row for a task notification; tap to see the task's result.
struct TaskNotificationCard: View {
    let notice: TaskNotice
    @State private var expanded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Button {
                withAnimation(.snappy(duration: 0.2)) { expanded.toggle() }
            } label: {
                HStack(spacing: 8) {
                    Image(systemName: notice.icon)
                        .font(.system(size: 13, weight: .regular))
                        .foregroundStyle(notice.tint)
                    VStack(alignment: .leading, spacing: 1) {
                        Text("Background task")
                            .font(.claudeFootnote)
                            .foregroundStyle(Theme.textMuted)
                        Text(notice.summary)
                            .font(.claudeCaption)
                            .foregroundStyle(Theme.textSecondary)
                            .lineLimit(expanded ? nil : 2)
                            .multilineTextAlignment(.leading)
                    }
                    Spacer(minLength: 6)
                    if notice.result != nil || notice.outputFile != nil {
                        Image(systemName: expanded ? "chevron.up" : "chevron.down")
                            .font(.system(size: 10, weight: .semibold))
                            .foregroundStyle(Theme.textMuted)
                    }
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            if expanded {
                if let result = notice.result {
                    Text(result)
                        .font(.claudeMono(11))
                        .foregroundStyle(Theme.textSecondary)
                        .lineLimit(40)
                        .textSelection(.enabled)
                        .padding(8)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Theme.bgPage, in: RoundedRectangle(cornerRadius: Theme.radiusSmall, style: .continuous))
                }
                if let file = notice.outputFile {
                    Text(file)
                        .font(.claudeMono(10))
                        .foregroundStyle(Theme.textMuted)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.bgSunken, in: RoundedRectangle(cornerRadius: Theme.radiusSmall, style: .continuous))
        .padding(.vertical, 2)
        .accessibilityElement(children: .combine)
    }
}
