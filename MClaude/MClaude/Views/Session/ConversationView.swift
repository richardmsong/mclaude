import SwiftUI

struct AskQuestion: Identifiable {
    let id = UUID()
    let header: String
    let question: String
    let options: [(label: String, description: String)]
}

struct ConversationView: View {
    let events: [SessionEvent]
    let sessionId: String
    var pendingMessages: [(id: UUID, text: String, sentAt: Date, delivered: Bool)] = []
    var onRefresh: (() async -> Void)?
    var onSendInput: ((String) -> Void)?
    /// Send a key sequence (e.g. "Down", "Enter") to the tmux session
    var onSendKey: ((String) -> Void)?

    @State private var selectedEvent: SessionEvent?
    @State private var isNearBottom = true

    /// Merge events and pending messages into a single timeline.
    /// Pending messages appear at the bottom until the matching JSONL user event
    /// arrives, at which point the pending is hidden and the JSONL event renders
    /// at its correct server-time position (when Claude actually received it).
    /// Strip server-added "[Image #N] " prefix so dedup compares the user's original text.
    private static let imageTagPattern = try! NSRegularExpression(pattern: #"^\[Image #\d+\]\s*"#)

    /// Normalize user text for dedup: strip [Image #N] prefix and screenshot paths
    private static func normalizeForDedup(_ text: String) -> String {
        var result = text
        let range1 = NSRange(result.startIndex..., in: result)
        result = imageTagPattern.stringByReplacingMatches(in: result, range: range1, withTemplate: "")
        let range2 = NSRange(result.startIndex..., in: result)
        result = screenshotPathPattern.stringByReplacingMatches(in: result, range: range2, withTemplate: "")
        return result.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private var timelineItems: [(id: String, kind: TimelineKind)] {
        // Collect user event texts for dedup (normalize whitespace for voice transcripts)
        let userEventTexts: [String] = events.compactMap { event in
            guard event.type == .user, let t = event.text, !t.isEmpty else { return nil }
            return Self.normalizeForDedup(t)
        }

        var items: [(id: String, kind: TimelineKind)] = []

        // Group subagent events by their parent tool_use ID
        var subagentEvents: [String: [SessionEvent]] = [:]  // parentToolUseId -> events
        for event in events {
            if let info = event.subagentInfo {
                subagentEvents[info.parentToolUseId, default: []].append(event)
            }
        }

        // Events in server-time order, with subagent events grouped under Agent blocks
        for event in events {
            // Skip individual subagent events (they're rendered inside the group)
            if event.subagentInfo != nil { continue }

            // If this is an Agent tool_use with subagent events, bundle them
            if event.type == .toolUse, event.toolUse?.name == "Agent",
               let subs = subagentEvents[event.toolUse?.id ?? ""], !subs.isEmpty {
                items.append((id: event.uuid, kind: .agentGroup(toolUse: event, subEvents: subs)))
            } else {
                items.append((id: event.uuid, kind: .event(event)))
            }
        }

        // Pending messages at the bottom — hidden when matching JSONL event exists
        for msg in pendingMessages.sorted(by: { $0.sentAt < $1.sentAt }) {
            let normalPending = Self.normalizeForDedup(msg.text)
            // Check exact match, or prefix match (voice transcripts may differ)
            let matched = userEventTexts.contains { eventText in
                eventText == normalPending
                    || (normalPending.count >= 30 && eventText.hasPrefix(String(normalPending.prefix(30))))
                    || (eventText.count >= 30 && normalPending.hasPrefix(String(eventText.prefix(30))))
            }
            if !matched {
                items.append((id: msg.id.uuidString, kind: .pending(text: msg.text, delivered: msg.delivered)))
            }
        }

        return items
    }

    private enum TimelineKind {
        case event(SessionEvent)
        case pending(text: String, delivered: Bool)
        case agentGroup(toolUse: SessionEvent, subEvents: [SessionEvent])
    }

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 2) {
                    ForEach(timelineItems, id: \.id) { item in
                        switch item.kind {
                        case .event(let event):
                            eventRow(event)
                        case .pending(let text, let delivered):
                            pendingBubble(text, delivered: delivered)
                        case .agentGroup(let toolUse, let subEvents):
                            agentGroupBlock(toolUse: toolUse, subEvents: subEvents)
                        }
                    }
                    Color.clear.frame(height: 1).id("bottom")
                        .onAppear { isNearBottom = true }
                        .onDisappear { isNearBottom = false }
                }
                .padding(.horizontal, 10)
                .padding(.vertical, 8)
            }
            .defaultScrollAnchor(.bottom)
            .background(Color(.systemBackground))
            .refreshable {
                await onRefresh?()
            }
            .onChange(of: events.count) {
                if isNearBottom {
                    withAnimation {
                        proxy.scrollTo("bottom")
                    }
                }
            }
        }
        .sheet(item: $selectedEvent) { event in
            EventDetailSheet(event: event)
        }
    }

    @ViewBuilder
    private func eventRow(_ event: SessionEvent) -> some View {
        switch event.type {
        case .user:
            // Only show blue bubble for user messages with actual text
            // Skip tool-result-only user events (no visible content)
            if event.text != nil && !(event.text?.isEmpty ?? true) {
                userBubble(event)
            }
        case .text:
            assistantTextBlock(event)
        case .thinking:
            thinkingBlock(event)
        case .toolUse:
            if event.toolUse?.name == "AskUserQuestion" {
                askUserQuestionBlock(event)
            } else {
                toolUseBlock(event)
            }
        case .toolResult:
            toolResultBlock(event)
        case .system:
            systemBlock(event)
        case .compaction:
            compactionBlock(event)
        }
    }

    // MARK: - Pending message (sent, waiting for event stream)

    private func pendingBubble(_ text: String, delivered: Bool) -> some View {
        HStack {
            Spacer(minLength: 40)
            VStack(alignment: .trailing, spacing: 3) {
                Text(text)
                    .font(.subheadline)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 8)
                    .background(Color.accentColor.opacity(delivered ? 1.0 : 0.4))
                    .foregroundStyle(.white)
                    .clipShape(RoundedRectangle(cornerRadius: 16))
                if !delivered {
                    HStack(spacing: 3) {
                        ProgressView()
                            .scaleEffect(0.6)
                        Text("Sending...")
                    }
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                }
            }
        }
        .padding(.top, 8)
        .padding(.bottom, 4)
    }

    // MARK: - User message

    private static let screenshotPathPattern = try! NSRegularExpression(
        pattern: #"/tmp/mclaude-screenshots/screenshot-\d+\.png"#
    )

    /// Split user text into the file path (if any) and remaining message text.
    private static func parseUserContent(_ text: String) -> (screenshotPath: String?, message: String) {
        let range = NSRange(text.startIndex..., in: text)
        if let match = screenshotPathPattern.firstMatch(in: text, range: range) {
            let path = String(text[Range(match.range, in: text)!])
            let remaining = screenshotPathPattern.stringByReplacingMatches(in: text, range: range, withTemplate: "")
                .trimmingCharacters(in: .whitespacesAndNewlines)
            return (path, remaining)
        }
        return (nil, text)
    }

    private func userBubble(_ event: SessionEvent) -> some View {
        let parsed = Self.parseUserContent(event.text ?? "")
        return HStack {
            Spacer(minLength: 40)
            VStack(alignment: .trailing, spacing: 4) {
                if parsed.screenshotPath != nil {
                    HStack(spacing: 4) {
                        Image(systemName: "photo.fill")
                            .font(.caption)
                        Text("Screenshot")
                            .font(.caption)
                    }
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(Color.white.opacity(0.2))
                    .clipShape(RoundedRectangle(cornerRadius: 8))
                }
                if !parsed.message.isEmpty {
                    Text(parsed.message)
                        .font(.subheadline)
                }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(Color.accentColor)
            .foregroundStyle(.white)
            .clipShape(RoundedRectangle(cornerRadius: 16))
            .onTapGesture { selectedEvent = event }
        }
        .padding(.top, 8)
        .padding(.bottom, 4)
    }

    // MARK: - Assistant text

    private func assistantTextBlock(_ event: SessionEvent) -> some View {
        let raw = event.text ?? ""
        let segments = splitMarkdownSegments(raw)
        return VStack(alignment: .leading, spacing: 4) {
            ForEach(Array(segments.enumerated()), id: \.offset) { _, segment in
                switch segment {
                case .text(let str):
                    if let md = try? AttributedString(markdown: str, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)) {
                        Text(md)
                            .font(.subheadline)
                            .foregroundStyle(.primary)
                    } else {
                        Text(str)
                            .font(.subheadline)
                            .foregroundStyle(.primary)
                    }
                case .header(let level, let text):
                    Text(text)
                        .font(level <= 1 ? .title3 : level == 2 ? .headline : .subheadline)
                        .fontWeight(.bold)
                        .foregroundStyle(.primary)
                        .padding(.top, level <= 2 ? 6 : 2)
                case .table(let headers, let rows):
                    markdownTableView(headers: headers, rows: rows)
                case .codeBlock(let language, let code):
                    codeBlockView(language: language, code: code)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.vertical, 4)
        .onTapGesture { selectedEvent = event }
    }

    // MARK: - Markdown table parsing & rendering

    private enum MarkdownSegment {
        case text(String)
        case header(level: Int, text: String)
        case table(headers: [String], rows: [[String]])
        case codeBlock(language: String, code: String)
    }

    private func splitMarkdownSegments(_ text: String) -> [MarkdownSegment] {
        let lines = text.components(separatedBy: "\n")
        var segments: [MarkdownSegment] = []
        var textLines: [String] = []
        var i = 0

        while i < lines.count {
            let trimmed = lines[i].trimmingCharacters(in: .whitespaces)

            // Detect fenced code block: ```lang
            if trimmed.hasPrefix("```") {
                // Flush accumulated text
                if !textLines.isEmpty {
                    let joined = textLines.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
                    if !joined.isEmpty { segments.append(.text(joined)) }
                    textLines = []
                }

                let language = String(trimmed.dropFirst(3)).trimmingCharacters(in: .whitespaces)
                var codeLines: [String] = []
                i += 1
                while i < lines.count {
                    if lines[i].trimmingCharacters(in: .whitespaces).hasPrefix("```") {
                        i += 1
                        break
                    }
                    codeLines.append(lines[i])
                    i += 1
                }
                let code = codeLines.joined(separator: "\n")
                if !code.isEmpty {
                    segments.append(.codeBlock(language: language, code: code))
                }
            }
            // Detect table: line with pipes, followed by separator line (|---|---|)
            else if i + 1 < lines.count,
               lines[i].contains("|"),
               lines[i + 1].contains("|"),
               lines[i + 1].contains("---") {
                // Flush accumulated text
                if !textLines.isEmpty {
                    let joined = textLines.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
                    if !joined.isEmpty { segments.append(.text(joined)) }
                    textLines = []
                }

                let headers = parseTableRow(lines[i])
                var rows: [[String]] = []
                i += 2  // skip header + separator
                while i < lines.count, lines[i].contains("|") {
                    let row = parseTableRow(lines[i])
                    if !row.isEmpty { rows.append(row) }
                    i += 1
                }
                segments.append(.table(headers: headers, rows: rows))
            }
            // Detect headers: # H1, ## H2, ### H3, etc.
            else if trimmed.hasPrefix("#") {
                // Flush accumulated text
                if !textLines.isEmpty {
                    let joined = textLines.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
                    if !joined.isEmpty { segments.append(.text(joined)) }
                    textLines = []
                }
                let level = trimmed.prefix(while: { $0 == "#" }).count
                let headerText = String(trimmed.dropFirst(level)).trimmingCharacters(in: .whitespaces)
                if !headerText.isEmpty {
                    segments.append(.header(level: min(level, 6), text: headerText))
                }
                i += 1
            } else {
                textLines.append(lines[i])
                i += 1
            }
        }

        if !textLines.isEmpty {
            let joined = textLines.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
            if !joined.isEmpty { segments.append(.text(joined)) }
        }

        return segments
    }

    private func parseTableRow(_ line: String) -> [String] {
        line.split(separator: "|", omittingEmptySubsequences: false)
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }  // drop empty leading/trailing from outer pipes
    }

    private func markdownTableView(headers: [String], rows: [[String]]) -> some View {
        ScrollView(.horizontal, showsIndicators: true) {
            Grid(alignment: .leading, horizontalSpacing: 0, verticalSpacing: 0) {
                // Header row
                GridRow {
                    ForEach(Array(headers.enumerated()), id: \.offset) { _, header in
                        Text(header)
                            .font(.caption)
                            .fontWeight(.bold)
                            .foregroundStyle(.primary)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 8)
                    }
                }
                .background(Color(.systemGray4).opacity(0.6))

                Divider()

                // Data rows
                ForEach(Array(rows.enumerated()), id: \.offset) { rowIdx, row in
                    GridRow {
                        ForEach(Array(row.enumerated()), id: \.offset) { _, cell in
                            if let md = try? AttributedString(markdown: cell, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)) {
                                Text(md)
                                    .font(.caption)
                                    .padding(.horizontal, 10)
                                    .padding(.vertical, 6)
                            } else {
                                Text(cell)
                                    .font(.caption)
                                    .padding(.horizontal, 10)
                                    .padding(.vertical, 6)
                            }
                        }
                    }
                    .background(rowIdx % 2 == 1 ? Color(.systemGray6).opacity(0.5) : Color.clear)

                    if rowIdx < rows.count - 1 {
                        Divider().opacity(0.3)
                    }
                }
            }
            .clipShape(RoundedRectangle(cornerRadius: 8))
            .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color(.systemGray4), lineWidth: 1))
        }
        .padding(.vertical, 4)
    }

    private func codeBlockView(language: String, code: String) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            if !language.isEmpty {
                Text(language)
                    .font(.caption2)
                    .fontWeight(.medium)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 4)
                    .frame(maxWidth: .infinity, alignment: .trailing)
                    .background(Color(.systemGray5))
            }
            ScrollView(.horizontal, showsIndicators: true) {
                Text(code)
                    .font(.system(.caption, design: .monospaced))
                    .foregroundStyle(.primary)
                    .padding(10)
            }
            .background(Color(.systemGray6))
        }
        .clipShape(RoundedRectangle(cornerRadius: 8))
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color(.systemGray4), lineWidth: 0.5))
        .padding(.vertical, 4)
    }

    // MARK: - Thinking (collapsible)

    private func thinkingBlock(_ event: SessionEvent) -> some View {
        DisclosureGroup {
            Text(event.thinking ?? "")
                .font(.caption)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(8)
                .background(Color(.systemGray6))
                .clipShape(RoundedRectangle(cornerRadius: 6))
        } label: {
            HStack(spacing: 4) {
                Image(systemName: "brain")
                    .font(.caption)
                Text("Thinking")
                    .font(.caption)
                    .fontWeight(.medium)
            }
            .foregroundStyle(.purple)
        }
        .padding(.vertical, 2)
    }

    // MARK: - Tool use

    private func toolUseBlock(_ event: SessionEvent) -> some View {
        HStack(spacing: 6) {
            Image(systemName: toolIcon(event.toolUse?.name ?? ""))
                .font(.caption)
                .foregroundStyle(.orange)
            VStack(alignment: .leading, spacing: 1) {
                Text(event.toolUse?.name ?? "unknown")
                    .font(.caption)
                    .fontWeight(.semibold)
                    .foregroundStyle(.orange)
                if let summary = event.toolUse?.inputSummary, !summary.isEmpty {
                    Text(summary)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
            }
            Spacer()
        }
        .padding(8)
        .background(Color.orange.opacity(0.08))
        .clipShape(RoundedRectangle(cornerRadius: 8))
        .padding(.vertical, 2)
        .onTapGesture { selectedEvent = event }
    }

    // MARK: - AskUserQuestion

    func parseQuestions(from event: SessionEvent) -> [AskQuestion] {
        guard let input = event.toolUse?.fullInput,
              let data = input.data(using: .utf8),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let questions = json["questions"] as? [[String: Any]] else { return [] }

        return questions.compactMap { q in
            let question = q["question"] as? String ?? ""
            let header = q["header"] as? String ?? ""
            let opts = (q["options"] as? [[String: Any]])?.compactMap { o -> (String, String)? in
                guard let label = o["label"] as? String else { return nil }
                return (label, o["description"] as? String ?? "")
            } ?? []
            return AskQuestion(header: header, question: question, options: opts)
        }
    }

    private func askUserQuestionBlock(_ event: SessionEvent) -> some View {
        let questions = parseQuestions(from: event)
        return AskUserQuestionView(questions: questions, onSendKey: onSendKey)
    }

    // MARK: - Tool result

    private func toolResultBlock(_ event: SessionEvent) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            if let results = event.toolResults {
                ForEach(results, id: \.toolUseId) { result in
                    HStack(alignment: .top, spacing: 4) {
                        Image(systemName: result.isError ? "xmark.circle.fill" : "checkmark.circle.fill")
                            .font(.caption2)
                            .foregroundStyle(result.isError ? .red : .green)
                        Text(result.content.isEmpty ? "(empty)" : String(result.content.prefix(300)))
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .lineLimit(4)
                    }
                }
            }
        }
        .padding(6)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(.systemGray6))
        .clipShape(RoundedRectangle(cornerRadius: 6))
        .padding(.vertical, 1)
        .onTapGesture { selectedEvent = event }
    }

    // MARK: - System (turn duration)

    private func systemBlock(_ event: SessionEvent) -> some View {
        Group {
            if let ms = event.durationMs {
                HStack {
                    Spacer()
                    Text("Turn: \(formattedDuration(ms))")
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                    Spacer()
                }
                .padding(.vertical, 4)
            }
        }
    }

    private func compactionBlock(_ event: SessionEvent) -> some View {
        HStack(spacing: 6) {
            Image(systemName: "arrow.triangle.2.circlepath")
                .font(.caption2)
                .foregroundStyle(.orange)
            Text("Context compacted")
                .font(.caption2)
                .foregroundStyle(.secondary)
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .background(Color.orange.opacity(0.08))
        .clipShape(RoundedRectangle(cornerRadius: 6))
        .padding(.vertical, 2)
    }

    // MARK: - Agent group (subagent events nested under Agent tool_use)

    @State private var expandedAgents: Set<String> = []

    private func agentGroupBlock(toolUse: SessionEvent, subEvents: [SessionEvent]) -> some View {
        let agentId = toolUse.uuid
        let isExpanded = expandedAgents.contains(agentId)
        let agentType = subEvents.first?.subagentInfo?.agentType ?? "Agent"

        return VStack(alignment: .leading, spacing: 0) {
            // Agent header (tappable to expand/collapse)
            Button {
                withAnimation(.easeInOut(duration: 0.2)) {
                    if isExpanded { expandedAgents.remove(agentId) }
                    else { expandedAgents.insert(agentId) }
                }
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: "person.2")
                        .font(.caption)
                        .foregroundStyle(.orange)
                    VStack(alignment: .leading, spacing: 1) {
                        HStack(spacing: 4) {
                            Text("Agent")
                                .font(.caption)
                                .fontWeight(.semibold)
                                .foregroundStyle(.orange)
                            Text(agentType)
                                .font(.caption2)
                                .padding(.horizontal, 5)
                                .padding(.vertical, 1)
                                .background(Color.orange.opacity(0.15))
                                .clipShape(Capsule())
                        }
                        if let summary = toolUse.toolUse?.inputSummary, !summary.isEmpty {
                            Text(summary)
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                                .lineLimit(2)
                        }
                    }
                    Spacer()
                    Image(systemName: isExpanded ? "chevron.up" : "chevron.down")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                    Text("\(subEvents.count)")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                .padding(8)
                .background(Color.orange.opacity(0.08))
                .clipShape(RoundedRectangle(cornerRadius: 8))
            }
            .buttonStyle(.plain)

            // Nested subagent events
            if isExpanded {
                VStack(alignment: .leading, spacing: 1) {
                    ForEach(subEvents, id: \.uuid) { sub in
                        eventRow(sub)
                            .padding(.leading, 8)
                    }
                }
                .padding(.leading, 4)
                .overlay(
                    Rectangle()
                        .fill(Color.orange.opacity(0.3))
                        .frame(width: 2),
                    alignment: .leading
                )
            }
        }
        .padding(.vertical, 2)
        .onTapGesture { selectedEvent = toolUse }
    }

    // MARK: - Helpers

    private func toolIcon(_ name: String) -> String {
        switch name {
        case "Read": return "doc.text"
        case "Write": return "square.and.pencil"
        case "Edit": return "pencil.line"
        case "Bash": return "terminal"
        case "Glob": return "magnifyingglass"
        case "Grep": return "text.magnifyingglass"
        case "Agent": return "person.2"
        case "Skill": return "star"
        case "!": return "apple.terminal"
        default: return "wrench"
        }
    }

    private func formattedDuration(_ ms: Int) -> String {
        if ms < 1000 { return "\(ms)ms" }
        let seconds = Double(ms) / 1000.0
        if seconds < 60 { return String(format: "%.1fs", seconds) }
        let minutes = Int(seconds) / 60
        let secs = Int(seconds) % 60
        return "\(minutes)m \(secs)s"
    }
}

// MARK: - Event Detail Sheet

struct EventDetailSheet: View {
    let event: SessionEvent
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    // Header
                    HStack {
                        Label(headerTitle, systemImage: headerIcon)
                            .font(.headline)
                            .foregroundStyle(headerColor)
                        Spacer()
                        Text(event.timestamp)
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                    }

                    if let model = event.model {
                        Text(model)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 3)
                            .background(Color(.systemGray5))
                            .clipShape(Capsule())
                    }

                    Divider()

                    // Content
                    switch event.type {
                    case .user:
                        contentText(event.text ?? "")
                    case .text:
                        markdownContent(event.text ?? "")
                    case .thinking:
                        contentText(event.thinking ?? "")
                    case .toolUse:
                        toolUseDetail
                    case .toolResult:
                        toolResultDetail
                    case .system:
                        if let ms = event.durationMs {
                            Label("Duration: \(ms)ms", systemImage: "clock")
                                .font(.subheadline)
                        }
                    case .compaction:
                        contentText(event.text ?? "Context was compacted")
                    }
                }
                .padding()
            }
            .navigationTitle("Event Detail")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { dismiss() }
                }
            }
        }
        .presentationDetents([.medium, .large])
        .presentationDragIndicator(.visible)
    }

    private var headerTitle: String {
        switch event.type {
        case .user: "User Message"
        case .text: "Assistant"
        case .thinking: "Thinking"
        case .toolUse: event.toolUse?.name ?? "Tool Use"
        case .toolResult: "Tool Result"
        case .system: "System"
        case .compaction: "Context Compacted"
        }
    }

    private var headerIcon: String {
        switch event.type {
        case .user: "person.fill"
        case .text: "bubble.left.fill"
        case .thinking: "brain"
        case .toolUse: "wrench"
        case .toolResult: "checkmark.circle"
        case .system: "gearshape"
        case .compaction: "arrow.triangle.2.circlepath"
        }
    }

    private var headerColor: Color {
        switch event.type {
        case .user: .accentColor
        case .text: .primary
        case .thinking: .purple
        case .toolUse: .orange
        case .toolResult: .green
        case .system: .secondary
        case .compaction: .orange
        }
    }

    private func contentText(_ text: String) -> some View {
        Text(text)
            .font(.system(.body, design: .monospaced))
            .textSelection(.enabled)
            .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func markdownContent(_ text: String) -> some View {
        Group {
            if let md = try? AttributedString(markdown: text, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)) {
                Text(md)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            } else {
                contentText(text)
            }
        }
    }

    private var toolUseDetail: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let tool = event.toolUse {
                Label(tool.name, systemImage: "wrench")
                    .font(.headline)
                    .foregroundStyle(.orange)

                if !tool.id.isEmpty {
                    HStack {
                        Text("ID:")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        Text(tool.id)
                            .font(.system(.caption, design: .monospaced))
                            .textSelection(.enabled)
                    }
                }

                if let fullInput = tool.fullInput, let parsed = parseToolInput(fullInput) {
                    formattedToolInput(name: tool.name, input: parsed)
                } else if !tool.inputSummary.isEmpty {
                    Text("Input")
                        .font(.subheadline)
                        .fontWeight(.semibold)
                    Text(tool.fullInput ?? tool.inputSummary)
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                        .padding(10)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Color(.systemGray6))
                        .clipShape(RoundedRectangle(cornerRadius: 8))
                }
            }
        }
    }

    private func parseToolInput(_ json: String) -> [String: String]? {
        guard let data = json.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return nil }
        var result: [String: String] = [:]
        for (key, value) in obj {
            if let str = value as? String { result[key] = str }
            else if let bool = value as? Bool { result[key] = bool ? "true" : "false" }
            else { result[key] = "\(value)" }
        }
        return result
    }

    @ViewBuilder
    private func formattedToolInput(name: String, input: [String: String]) -> some View {
        switch name {
        case "Edit":
            if let filePath = input["file_path"] {
                Label(filePath, systemImage: "doc")
                    .font(.system(.caption, design: .monospaced))
                    .foregroundStyle(.secondary)
            }
            if let replaceAll = input["replace_all"], replaceAll == "true" {
                Text("Replace all occurrences")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            unifiedDiffView(old: input["old_string"] ?? "", new: input["new_string"] ?? "")

        case "Write":
            if let filePath = input["file_path"] {
                Label(filePath, systemImage: "doc.badge.plus")
                    .font(.system(.caption, design: .monospaced))
                    .foregroundStyle(.secondary)
            }
            if let content = input["content"] {
                codeBlock(text: content)
            }

        case "Read":
            if let filePath = input["file_path"] {
                Label(filePath, systemImage: "doc.text")
                    .font(.system(.caption, design: .monospaced))
                    .foregroundStyle(.secondary)
            }
            if let offset = input["offset"] { paramRow("Offset", offset) }
            if let limit = input["limit"] { paramRow("Limit", limit) }

        case "Bash":
            if let command = input["command"] {
                codeBlock(text: command)
            }

        case "Grep":
            if let pattern = input["pattern"] { paramRow("Pattern", pattern) }
            if let path = input["path"] { paramRow("Path", path) }
            if let glob = input["glob"] { paramRow("Glob", glob) }

        case "Glob":
            if let pattern = input["pattern"] { paramRow("Pattern", pattern) }
            if let path = input["path"] { paramRow("Path", path) }

        default:
            // Generic: show all fields
            ForEach(input.keys.sorted(), id: \.self) { key in
                paramRow(key, input[key] ?? "")
            }
        }
    }

    // MARK: - Unified Diff View with character-level highlighting

    private enum DiffLineType { case context, removed, added }

    private struct DiffEntry {
        let type: DiffLineType
        let text: String
        // Character-level: ranges that are specifically changed (highlighted stronger)
        let highlightRanges: [Range<String.Index>]

        init(_ type: DiffLineType, _ text: String, highlights: [Range<String.Index>] = []) {
            self.type = type
            self.text = text
            self.highlightRanges = highlights
        }
    }

    private func unifiedDiffView(old: String, new: String) -> some View {
        let entries = computeDiff(old: old, new: new)
        return VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(entries.enumerated()), id: \.offset) { _, entry in
                diffEntryView(entry)
            }
        }
        .clipShape(RoundedRectangle(cornerRadius: 6))
        .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color(.systemGray4), lineWidth: 0.5))
    }

    private func diffEntryView(_ entry: DiffEntry) -> some View {
        let prefix: String
        let lineColor: Color
        let bgColor: Color
        let strongBg: Color

        switch entry.type {
        case .context:
            prefix = " "; lineColor = .secondary; bgColor = .clear; strongBg = .clear
        case .removed:
            prefix = "−"; lineColor = .red; bgColor = Color.red.opacity(0.08); strongBg = Color.red.opacity(0.25)
        case .added:
            prefix = "+"; lineColor = .green; bgColor = Color.green.opacity(0.08); strongBg = Color.green.opacity(0.25)
        }

        return HStack(spacing: 0) {
            // Gutter
            Text(prefix)
                .font(.system(.caption2, design: .monospaced))
                .foregroundStyle(lineColor)
                .frame(width: 16, alignment: .center)
                .padding(.vertical, 3)
                .background(lineColor.opacity(0.15))

            // Line content with inline highlights
            buildHighlightedText(entry.text, highlights: entry.highlightRanges, baseColor: entry.type == .context ? .primary : lineColor, strongBg: strongBg)
                .font(.system(.caption2, design: .monospaced))
                .padding(.horizontal, 6)
                .padding(.vertical, 3)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(bgColor)
        }
        .textSelection(.enabled)
    }

    private func buildHighlightedText(_ text: String, highlights: [Range<String.Index>], baseColor: Color, strongBg: Color) -> Text {
        guard !highlights.isEmpty, !text.isEmpty else {
            return Text(text.isEmpty ? " " : text).foregroundColor(baseColor)
        }

        var attr = AttributedString(text)
        attr.foregroundColor = baseColor

        for range in highlights {
            guard let attrLower = AttributedString.Index(range.lowerBound, within: attr),
                  let attrUpper = AttributedString.Index(range.upperBound, within: attr) else { continue }
            attr[attrLower..<attrUpper].backgroundColor = UIColor(strongBg)
        }

        return Text(attr)
    }

    /// LCS-based line diff, then character-level diff for paired changed lines
    private func computeDiff(old: String, new: String) -> [DiffEntry] {
        let oldLines = old.components(separatedBy: "\n")
        let newLines = new.components(separatedBy: "\n")

        // Build LCS table
        let m = oldLines.count, n = newLines.count
        var dp = Array(repeating: Array(repeating: 0, count: n + 1), count: m + 1)
        for i in 1...max(m, 1) where i <= m {
            for j in 1...max(n, 1) where j <= n {
                if oldLines[i - 1] == newLines[j - 1] {
                    dp[i][j] = dp[i - 1][j - 1] + 1
                } else {
                    dp[i][j] = max(dp[i - 1][j], dp[i][j - 1])
                }
            }
        }

        // Backtrack to produce raw diff
        enum RawDiff { case ctx(String), rem(String), add(String) }
        var raw: [RawDiff] = []
        var i = m, j = n
        while i > 0 || j > 0 {
            if i > 0 && j > 0 && oldLines[i - 1] == newLines[j - 1] {
                raw.append(.ctx(oldLines[i - 1])); i -= 1; j -= 1
            } else if j > 0 && (i == 0 || dp[i][j - 1] >= dp[i - 1][j]) {
                raw.append(.add(newLines[j - 1])); j -= 1
            } else {
                raw.append(.rem(oldLines[i - 1])); i -= 1
            }
        }
        raw.reverse()

        // Post-process: pair up adjacent removed/added runs for character-level diff
        var result: [DiffEntry] = []
        var idx = 0
        while idx < raw.count {
            switch raw[idx] {
            case .ctx(let s):
                result.append(DiffEntry(.context, s))
                idx += 1
            case .rem:
                // Collect consecutive removed lines
                var removed: [String] = []
                while idx < raw.count, case .rem(let s) = raw[idx] {
                    removed.append(s); idx += 1
                }
                // Collect consecutive added lines that follow
                var added: [String] = []
                while idx < raw.count, case .add(let s) = raw[idx] {
                    added.append(s); idx += 1
                }
                // Pair them up for character-level diff
                let pairs = max(removed.count, added.count)
                for p in 0..<pairs {
                    if p < removed.count && p < added.count {
                        let (remHL, addHL) = charDiff(old: removed[p], new: added[p])
                        result.append(DiffEntry(.removed, removed[p], highlights: remHL))
                        result.append(DiffEntry(.added, added[p], highlights: addHL))
                    } else if p < removed.count {
                        result.append(DiffEntry(.removed, removed[p]))
                    } else {
                        result.append(DiffEntry(.added, added[p]))
                    }
                }
            case .add(let s):
                result.append(DiffEntry(.added, s))
                idx += 1
            }
        }
        return result
    }

    /// Character-level LCS diff between two strings. Returns highlight ranges for each.
    private func charDiff(old: String, new: String) -> ([Range<String.Index>], [Range<String.Index>]) {
        let oldChars = Array(old)
        let newChars = Array(new)
        let m = oldChars.count, n = newChars.count

        // Skip if too large (O(m*n) space)
        guard m * n < 500_000 else { return ([], []) }

        // LCS
        var dp = Array(repeating: Array(repeating: 0, count: n + 1), count: m + 1)
        for i in 1...max(m, 1) where i <= m {
            for j in 1...max(n, 1) where j <= n {
                if oldChars[i - 1] == newChars[j - 1] {
                    dp[i][j] = dp[i - 1][j - 1] + 1
                } else {
                    dp[i][j] = max(dp[i - 1][j], dp[i][j - 1])
                }
            }
        }

        // Backtrack to find which chars are NOT in LCS (those are the changes)
        var oldChanged = Array(repeating: true, count: m)
        var newChanged = Array(repeating: true, count: n)
        var ci = m, cj = n
        while ci > 0 && cj > 0 {
            if oldChars[ci - 1] == newChars[cj - 1] {
                oldChanged[ci - 1] = false
                newChanged[cj - 1] = false
                ci -= 1; cj -= 1
            } else if dp[ci - 1][cj] >= dp[ci][cj - 1] {
                ci -= 1
            } else {
                cj -= 1
            }
        }

        // Convert bool arrays to string ranges
        func toRanges(_ changed: [Bool], _ str: String) -> [Range<String.Index>] {
            var ranges: [Range<String.Index>] = []
            var idx = str.startIndex
            var runStart: String.Index?
            for (i, ch) in changed.enumerated() {
                if ch {
                    if runStart == nil { runStart = idx }
                } else {
                    if let start = runStart {
                        ranges.append(start..<idx)
                        runStart = nil
                    }
                }
                if i < changed.count - 1 {
                    idx = str.index(after: idx)
                } else if ch, let start = runStart {
                    ranges.append(start..<str.index(after: idx))
                }
            }
            return ranges
        }

        return (toRanges(oldChanged, old), toRanges(newChanged, new))
    }

    private func codeBlock(text: String) -> some View {
        Text(highlightBash(text))
            .font(.system(.caption, design: .monospaced))
            .textSelection(.enabled)
            .padding(8)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color(.systemGray6))
            .clipShape(RoundedRectangle(cornerRadius: 6))
    }

    /// Simple bash syntax highlighting using AttributedString
    private func highlightBash(_ command: String) -> AttributedString {
        var result = AttributedString(command)
        result.foregroundColor = .primary

        // Operators: &&, ||, |, ;, >, >>, <, 2>&1
        let operators = [
            (#"&&"#, UIColor.systemOrange),
            (#"\|\|"#, UIColor.systemOrange),
            (#"\|"#, UIColor.systemOrange),
            (#";"#, UIColor.systemOrange),
            (#">>"#, UIColor.systemOrange),
            (#"2>&1"#, UIColor.systemOrange),
            (#">"#, UIColor.systemOrange),
            (#"<"#, UIColor.systemOrange),
        ]

        // Keywords
        let keywords = [
            (#"\b(if|then|else|fi|for|do|done|while|case|esac|in)\b"#, UIColor.systemPurple),
            (#"\b(cd|echo|export|source|return|exit)\b"#, UIColor.systemPurple),
        ]

        // Flags: -something or --something
        let flags = [(#"\s(--?[\w][\w-]*)"#, UIColor.systemCyan)]

        // Strings (double and single quoted)
        let strings = [
            (#"\"[^\"]*\""#, UIColor.systemGreen),
            (#"'[^']*'"#, UIColor.systemGreen),
        ]

        // Variables: $VAR, ${VAR}, $(...)
        let variables = [
            (#"\$\{[^}]+\}"#, UIColor.systemRed),
            (#"\$\([^)]+\)"#, UIColor.systemRed),
            (#"\$[A-Za-z_]\w*"#, UIColor.systemRed),
        ]

        // Numbers
        let numbers = [(#"\b\d+\b"#, UIColor.systemYellow)]

        // Comments: # to end of line (but not inside strings or ${ })
        let comments = [(#"(?:^|(?<=\s))#[^\n]*"#, UIColor.systemGray)]

        // Apply all patterns (order matters — later patterns override)
        // Comments last so they override everything within the comment
        let allPatterns: [(String, UIColor)] = operators + keywords + numbers + flags + strings + variables + comments

        for (pattern, color) in allPatterns {
            guard let regex = try? NSRegularExpression(pattern: pattern) else { continue }
            let nsString = command as NSString
            let matches = regex.matches(in: command, range: NSRange(location: 0, length: nsString.length))
            for match in matches {
                // Use capture group 1 if it exists (for flags pattern), else group 0
                let rangeIdx = match.numberOfRanges > 1 && match.range(at: 1).location != NSNotFound ? 1 : 0
                let nsRange = match.range(at: rangeIdx)
                guard let swiftRange = Range(nsRange, in: command),
                      let attrLower = AttributedString.Index(swiftRange.lowerBound, within: result),
                      let attrUpper = AttributedString.Index(swiftRange.upperBound, within: result) else { continue }
                result[attrLower..<attrUpper].foregroundColor = Color(color)
            }
        }

        return result
    }

    private func paramRow(_ label: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(label)
                .font(.caption2)
                .foregroundStyle(.secondary)
            Text(value)
                .font(.system(.caption, design: .monospaced))
                .textSelection(.enabled)
        }
    }

    private var toolResultDetail: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let results = event.toolResults {
                ForEach(results, id: \.toolUseId) { result in
                    VStack(alignment: .leading, spacing: 6) {
                        HStack {
                            Image(systemName: result.isError ? "xmark.circle.fill" : "checkmark.circle.fill")
                                .foregroundStyle(result.isError ? .red : .green)
                            Text(result.isError ? "Error" : "Success")
                                .font(.subheadline)
                                .fontWeight(.semibold)
                            Spacer()
                            Text(result.toolUseId)
                                .font(.system(.caption2, design: .monospaced))
                                .foregroundStyle(.tertiary)
                        }

                        Group {
                            if result.content.isEmpty {
                                Text("(empty)")
                                    .font(.system(.caption, design: .monospaced))
                            } else if let md = try? AttributedString(markdown: result.content, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)) {
                                Text(md)
                                    .font(.caption)
                            } else {
                                Text(result.content)
                                    .font(.system(.caption, design: .monospaced))
                            }
                        }
                        .textSelection(.enabled)
                        .padding(10)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Color(.systemGray6))
                        .clipShape(RoundedRectangle(cornerRadius: 8))
                    }
                }
            }
        }
    }
}

// MARK: - AskUserQuestion (stateful selection)

struct AskUserQuestionView: View {
    let questions: [AskQuestion]
    var onSendKey: ((String) -> Void)?

    // Track selected option index per question group (by question ID)
    @State private var selections: [UUID: Int] = [:]
    @State private var submitted = false

    /// Compute the global 1-based index for an option within a question group.
    /// Options are numbered sequentially across all groups.
    private func globalIndex(questionIndex: Int, optionIndex: Int) -> Int {
        var base = 0
        for i in 0..<questionIndex {
            base += questions[i].options.count
        }
        return base + optionIndex + 1
    }

    private var allGroupsSelected: Bool {
        questions.allSatisfy { q in
            q.options.isEmpty || selections[q.id] != nil
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 6) {
                Image(systemName: "questionmark.circle.fill")
                    .foregroundStyle(.blue)
                Text("Question")
                    .font(.caption)
                    .fontWeight(.semibold)
                    .foregroundStyle(.blue)
            }

            ForEach(Array(questions.enumerated()), id: \.element.id) { qIdx, q in
                VStack(alignment: .leading, spacing: 6) {
                    if !q.header.isEmpty {
                        Text(q.header)
                            .font(.caption)
                            .fontWeight(.bold)
                            .foregroundStyle(.primary)
                    }
                    Text(q.question)
                        .font(.callout)
                        .foregroundStyle(.primary)

                    if !q.options.isEmpty {
                        VStack(spacing: 4) {
                            ForEach(Array(q.options.enumerated()), id: \.element.label) { optIdx, opt in
                                let isSelected = selections[q.id] == optIdx
                                Button {
                                    if !submitted {
                                        if isSelected {
                                            selections.removeValue(forKey: q.id)
                                        } else {
                                            selections[q.id] = optIdx
                                        }
                                    }
                                } label: {
                                    HStack(spacing: 6) {
                                        Image(systemName: isSelected ? "checkmark.circle.fill" : "circle")
                                            .font(.caption)
                                            .foregroundStyle(isSelected ? .blue : .secondary)
                                        VStack(alignment: .leading, spacing: 2) {
                                            Text(opt.label)
                                                .font(.subheadline)
                                                .fontWeight(.medium)
                                            if !opt.description.isEmpty {
                                                Text(opt.description)
                                                    .font(.caption2)
                                                    .foregroundStyle(.secondary)
                                            }
                                        }
                                    }
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .padding(8)
                                    .background(isSelected ? Color.blue.opacity(0.15) : Color.blue.opacity(0.05))
                                    .clipShape(RoundedRectangle(cornerRadius: 8))
                                }
                                .buttonStyle(.plain)
                                .disabled(submitted)
                            }
                        }
                    }
                }

                if q.id != questions.last?.id {
                    Divider()
                }
            }

            if !submitted {
                Button {
                    guard allGroupsSelected else { return }
                    submitted = true
                    // Send each selected option's number key in order
                    for (qIdx, q) in questions.enumerated() {
                        if let optIdx = selections[q.id] {
                            let num = globalIndex(questionIndex: qIdx, optionIndex: optIdx)
                            onSendKey?("\(num)")
                        }
                    }
                    // Submit the final selection
                    onSendKey?("Enter")
                } label: {
                    Label(
                        allGroupsSelected ? "Submit answers" : "Select one per group",
                        systemImage: "arrow.up.circle.fill"
                    )
                    .font(.subheadline)
                    .fontWeight(.medium)
                    .frame(maxWidth: .infinity)
                    .padding(10)
                    .background(allGroupsSelected ? Color.blue : Color.gray.opacity(0.3))
                    .foregroundStyle(allGroupsSelected ? .white : .secondary)
                    .clipShape(RoundedRectangle(cornerRadius: 8))
                }
                .buttonStyle(.plain)
                .disabled(!allGroupsSelected)
            } else {
                HStack(spacing: 4) {
                    Image(systemName: "checkmark.circle.fill")
                        .foregroundStyle(.green)
                    Text("Submitted")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .padding(10)
        .background(Color.blue.opacity(0.05))
        .clipShape(RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color.blue.opacity(0.2), lineWidth: 1))
        .padding(.vertical, 4)
    }
}
