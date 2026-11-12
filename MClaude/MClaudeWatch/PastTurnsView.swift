import SwiftUI

/// Earlier turns to review (docs/plan-watch-app.md → Past turns).
struct PastTurnsView: View {
    let sessionId: String
    @Environment(WatchStore.self) private var store

    enum Load { case loading, loaded([PastTurn]), failed }
    @State private var load: Load = .loading

    var body: some View {
        Group {
            switch load {
            case .loading:
                ProgressView()
            case .loaded(let turns) where turns.isEmpty:
                Text("No earlier turns").font(.footnote).foregroundStyle(WatchTheme.textMuted)
            case .loaded(let turns):
                TimelineView(.periodic(from: .now, by: 30)) { context in
                    List(turns) { turn in
                        NavigationLink { PastTurnDetailView(turn: turn) } label: {
                            row(turn, now: context.date)
                        }
                    }
                }
            case .failed:
                VStack(spacing: 8) {
                    Text("Couldn't load past turns")
                        .font(.footnote)
                        .multilineTextAlignment(.center)
                        .foregroundStyle(WatchTheme.textSecondary)
                    Button("Retry") { Task { await fetch() } }
                }
            }
        }
        .navigationTitle("Past turns")
        .task { await fetch() }
    }

    private func fetch() async {
        load = .loading
        if let turns = await store.pastTurns(sessionId) { load = .loaded(turns) } else { load = .failed }
    }

    private func row(_ t: PastTurn, now: Date) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(promptLine(t))
                .font(.footnote)
                .foregroundStyle(WatchTheme.textPrimary)
                .lineLimit(2)
            Text(t.reply.map { WatchMarkdown.inline(plainStart($0)) } ?? AttributedString("No reply"))
                .font(.caption2)
                .foregroundStyle(WatchTheme.textMuted)
                .lineLimit(2)
            if let at = t.at {
                Text(age(at, now: now)).font(.caption2).foregroundStyle(WatchTheme.textMuted)
            }
        }
        .padding(.vertical, 2)
    }

    private func promptLine(_ t: PastTurn) -> AttributedString {
        var s = AttributedString()
        if let from = t.from {
            var f = AttributedString("From \(from): ")
            f.foregroundColor = WatchTheme.textMuted
            s += f
        }
        s += WatchMarkdown.inline(t.prompt)
        return s
    }

    /// The reply's first lines without block Markdown marks, for a two-line preview.
    private func plainStart(_ md: String) -> String {
        md.split(separator: "\n").map { line in
            var l = line.trimmingCharacters(in: .whitespaces)
            while l.hasPrefix("#") || l.hasPrefix(">") { l.removeFirst() }
            if l.hasPrefix("- ") || l.hasPrefix("* ") { l.removeFirst(2) }
            return l.trimmingCharacters(in: .whitespaces)
        }.filter { !$0.isEmpty && !$0.hasPrefix("```") }.prefix(4).joined(separator: " ")
    }

    private func age(_ d: Date, now: Date) -> String {
        let s = Int(max(0, now.timeIntervalSince(d)))
        if s < 60 { return "now" }
        if s < 3600 { return "\(s / 60)m ago" }
        if s < 86400 { return "\(s / 3600)h ago" }
        return "\(s / 86400)d ago"
    }
}

/// One past turn: the prompt bubble, then Claude's reply rendered in full.
struct PastTurnDetailView: View {
    let turn: PastTurn

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 10) {
                VStack(alignment: .trailing, spacing: 2) {
                    if let from = turn.from {
                        Text("From \(from)").font(.caption2).foregroundStyle(WatchTheme.textMuted)
                    }
                    Text(WatchMarkdown.inline(turn.prompt))
                        .font(.footnote)
                        .foregroundStyle(WatchTheme.textPrimary)
                        .padding(8)
                        .background(WatchTheme.raised, in: RoundedRectangle(cornerRadius: 12))
                }
                .frame(maxWidth: .infinity, alignment: .trailing)
                .padding(.leading, 16)
                if let reply = turn.reply {
                    WatchMarkdown(blocks: WatchMarkdown.parse(reply))
                } else {
                    Text("No reply").font(.footnote).foregroundStyle(WatchTheme.textMuted)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}
