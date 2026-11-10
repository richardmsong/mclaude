import SwiftUI

/// The live turn as it's written: thinking muted, text bright, tool runs as one quiet line
/// (docs/plan-watch-app.md → Live view).
struct LiveFeedView: View {
    let turn: LiveTurn?

    var body: some View {
        let items = turn?.feed ?? []
        VStack(alignment: .leading, spacing: 8) {
            if items.isEmpty {
                Text("Starting…").font(.footnote).foregroundStyle(WatchTheme.textMuted)
            }
            ForEach(items) { item in
                row(item).id(item.id)
            }
        }
    }

    @ViewBuilder private func row(_ item: LiveTurn.FeedItem) -> some View {
        switch item {
        case .thinking(_, let text, let growing):
            streamed(LiveFormat.tail(text), color: WatchTheme.textMuted, growing: growing)
        case .text(_, let text, let growing):
            // Claude's text keeps its formatting (headings, bullets) as it streams.
            VStack(alignment: .leading, spacing: 2) {
                WatchMarkdown(blocks: WatchMarkdown.parse(text))
                if growing {
                    RoundedRectangle(cornerRadius: 1).fill(WatchTheme.clay).frame(width: 7, height: 13)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        case .tools(_, let start, let end, let running):
            TimelineView(.periodic(from: .now, by: 1)) { context in
                Text(toolLine(start: start, end: end, running: running, now: context.date))
                    .font(.caption2)
                    .foregroundStyle(WatchTheme.textMuted)
                    .frame(maxWidth: .infinity)
            }
        }
    }

    private func streamed(_ text: String, color: Color, growing: Bool) -> some View {
        var s = WatchMarkdown.inline(text)
        s.foregroundColor = color
        if growing {
            var cursor = AttributedString(" ▍")
            cursor.foregroundColor = WatchTheme.clay
            s += cursor
        }
        return Text(s)
            .font(.footnote)
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// "working ⋯ 12s" while it runs, "worked 41s" after (just "worked" if the end isn't known).
    private func toolLine(start: Date?, end: Date?, running: Bool, now: Date) -> String {
        guard let start else { return running ? "working ⋯" : "worked" }
        if running { return "working ⋯ \(LiveFormat.duration(now.timeIntervalSince(start)))" }
        guard let end else { return "worked" }
        return "worked \(LiveFormat.duration(end.timeIntervalSince(start)))"
    }
}
