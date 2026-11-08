import SwiftUI

/// Claude's last answer in full, rendered for the watch (docs/plan-watch-app.md → Last answer).
struct LastAnswerView: View {
    let sessionId: String
    @Environment(WatchStore.self) private var store

    enum Load { case loading, text(String), none, failed }
    @State private var load: Load = .loading

    var body: some View {
        Group {
            switch load {
            case .loading:
                ProgressView()
            case .text(let markdown):
                ScrollView {
                    WatchMarkdown(blocks: WatchMarkdown.parse(markdown))
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            case .none:
                Text("No message yet").font(.footnote).foregroundStyle(WatchTheme.textMuted)
            case .failed:
                VStack(spacing: 8) {
                    Text("Couldn't load the message")
                        .font(.footnote)
                        .multilineTextAlignment(.center)
                        .foregroundStyle(WatchTheme.textSecondary)
                    Button("Retry") { Task { await fetch() } }
                }
            }
        }
        .navigationTitle("Last answer")
        .task { await fetch() }
    }

    private func fetch() async {
        load = .loading
        switch await store.fullMessage(sessionId) {
        case .text(let t): load = .text(t)
        case .none: load = .none
        case .failed: load = .failed
        }
    }
}

/// A small block-level Markdown renderer: headings, paragraphs (inline Markdown via
/// AttributedString), list items, quotes, code blocks, tables and rules.
struct WatchMarkdown: View {
    enum Block: Equatable {
        case heading(String, level: Int)
        case paragraph(String)
        case item(marker: String, text: String, indent: Int)
        case quote(String)
        case code(String)
        case table([String])
        case rule
    }

    let blocks: [Block]

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            ForEach(Array(blocks.enumerated()), id: \.offset) { _, block in view(block) }
        }
    }

    @ViewBuilder private func view(_ block: Block) -> some View {
        switch block {
        case .heading(let text, let level):
            Text(Self.inline(text))
                .font(level <= 2 ? .headline : .subheadline.weight(.semibold))
                .foregroundStyle(WatchTheme.textPrimary)
        case .paragraph(let text):
            Text(Self.inline(text))
                .font(.footnote)
                .foregroundStyle(WatchTheme.textPrimary)
                .fixedSize(horizontal: false, vertical: true)
        case .item(let marker, let text, let indent):
            HStack(alignment: .firstTextBaseline, spacing: 4) {
                Text(marker).font(.footnote).foregroundStyle(WatchTheme.textMuted)
                Text(Self.inline(text))
                    .font(.footnote)
                    .foregroundStyle(WatchTheme.textPrimary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(.leading, CGFloat(indent) * 10)
        case .quote(let text):
            HStack(spacing: 6) {
                Rectangle().fill(WatchTheme.textMuted).frame(width: 2)
                Text(Self.inline(text))
                    .font(.footnote)
                    .foregroundStyle(WatchTheme.textSecondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        case .code(let text):
            Text(text)
                .font(.system(size: 11, design: .monospaced))
                .foregroundStyle(WatchTheme.textSecondary)
                .fixedSize(horizontal: false, vertical: true)
                .padding(6)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(WatchTheme.raised, in: RoundedRectangle(cornerRadius: 8))
        case .table(let rows):
            VStack(alignment: .leading, spacing: 2) {
                ForEach(Array(rows.enumerated()), id: \.offset) { i, row in
                    Text(row)
                        .font(.system(size: 11, design: .monospaced).weight(i == 0 ? .semibold : .regular))
                        .foregroundStyle(WatchTheme.textSecondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
        case .rule:
            Rectangle().fill(WatchTheme.raised).frame(height: 1)
        }
    }

    /// Inline Markdown (bold, italic, code, links); the raw text if it doesn't parse.
    static func inline(_ text: String) -> AttributedString {
        let options = AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        return (try? AttributedString(markdown: text, options: options)) ?? AttributedString(text)
    }

    static func parse(_ markdown: String) -> [Block] {
        var blocks: [Block] = []
        var paragraph: [String] = []
        var code: [String]? = nil
        var table: [String] = []

        func flushParagraph() {
            if !paragraph.isEmpty { blocks.append(.paragraph(paragraph.joined(separator: " "))) }
            paragraph = []
        }
        func flushTable() {
            if !table.isEmpty { blocks.append(.table(table)) }
            table = []
        }

        for raw in markdown.components(separatedBy: "\n") {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("```") {
                if let open = code { blocks.append(.code(open.joined(separator: "\n"))); code = nil }
                else { flushParagraph(); flushTable(); code = [] }
                continue
            }
            if code != nil { code?.append(raw); continue }

            if line.hasPrefix("|") {
                flushParagraph()
                // Skip the |---|---| separator row.
                if line.allSatisfy({ "|-: ".contains($0) }) { continue }
                let cells = line.trimmingCharacters(in: CharacterSet(charactersIn: "|")).split(separator: "|")
                    .map { $0.trimmingCharacters(in: .whitespaces) }
                table.append(cells.joined(separator: " · "))
                continue
            }
            flushTable()

            if line.isEmpty { flushParagraph(); continue }
            if line == "---" || line == "***" || line == "___" { flushParagraph(); blocks.append(.rule); continue }
            if line.hasPrefix("#") {
                flushParagraph()
                let level = line.prefix { $0 == "#" }.count
                blocks.append(.heading(line.dropFirst(level).trimmingCharacters(in: .whitespaces), level: level))
                continue
            }
            if line.hasPrefix(">") {
                flushParagraph()
                blocks.append(.quote(line.dropFirst().trimmingCharacters(in: .whitespaces)))
                continue
            }
            let indent = (raw.prefix { $0 == " " }.count) / 2
            if line.hasPrefix("- ") || line.hasPrefix("* ") || line.hasPrefix("+ ") {
                flushParagraph()
                blocks.append(.item(marker: "•", text: String(line.dropFirst(2)), indent: indent))
                continue
            }
            if let dot = line.firstIndex(of: "."), line[..<dot].allSatisfy(\.isNumber), !line[..<dot].isEmpty,
               line[line.index(after: dot)...].hasPrefix(" ") {
                flushParagraph()
                blocks.append(.item(marker: String(line[...dot]),
                                    text: line[line.index(after: dot)...].trimmingCharacters(in: .whitespaces),
                                    indent: indent))
                continue
            }
            paragraph.append(line)
        }
        if let open = code { blocks.append(.code(open.joined(separator: "\n"))) }
        flushParagraph()
        flushTable()
        return blocks
    }
}
