import SwiftUI
#if os(watchOS)
import WatchKit
#endif

/// docs/plan-watch-app.md → Session screen. One conversation-style screen that never swaps
/// layouts: the latest turn streams in while Claude works and stops growing when it's done.
struct SessionDetailView: View {
    let sessionId: String
    @Environment(WatchStore.self) private var store
    @Environment(\.dismiss) private var dismiss

    /// Per question: ticked option indexes, and the Other text (Other counts when non-blank).
    @State private var picks: [Int: Set<Int>] = [:]
    @State private var otherText: [Int: String] = [:]
    /// Single-select questions whose Other is the chosen answer.
    @State private var otherChosen: Set<Int> = []
    @State private var sending: String?
    @State private var failed = false
    /// A Talk message that failed to send, offered again by Type.
    @State private var retryText: String?
    @State private var promptExpanded = false
    /// Keep the newest line in view (scrolling up pauses it, watchOS 11+).
    @State private var following = true

    private var session: WatchSession? { store.session(sessionId) }

    var body: some View {
        Group {
            if let session {
                screen(session)
                    .navigationTitle(session.name)
            } else {
                Color.clear
            }
        }
        // Subscribed to the live turn the whole time the screen is open.
        .task { await store.followLive(sessionId) }
        .onAppear { store.markRead(sessionId) }
        .onChange(of: session?.since) { _, _ in if session != nil { store.markRead(sessionId) } }
        .onChange(of: session == nil) { _, gone in if gone { dismiss() } }
        // A new pending request starts with fresh selections; a new turn starts collapsed.
        .onChange(of: session?.questions ?? []) { _, _ in picks = [:]; otherText = [:]; otherChosen = [] }
        // A new turn starts collapsed and is followed, even if you'd scrolled up in the last one.
        .onChange(of: store.live[sessionId]?.turn) { _, _ in promptExpanded = false; following = true }
    }

    // MARK: - The screen

    private func screen(_ s: WatchSession) -> some View {
        let turn = store.live[sessionId]
        let queued = s.queue ?? []
        let working = s.state == .working || turn?.active == true
        let questionPending = !s.pendingQuestions.isEmpty
        return ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 10) {
                    TimelineView(.periodic(from: .now, by: 5)) { context in header(s, now: context.date) }
                    latestTurn(s, turn)
                    queue(queued)
                    pending(s)
                    if !working, s.state != .waiting, s.recap?.nextBy == "claude" {
                        actionButton("Continue", action: "continue", primary: false)
                    }
                    if failed {
                        Text("Couldn't send. Try again.").font(.caption2).foregroundStyle(WatchTheme.danger)
                    }
                    // Room for the bottom toolbar, so the newest line sits above the buttons.
                    Color.clear.frame(height: 52).id("end")
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .modifier(FollowTracker(following: $following))
            // While Claude works, open on the newest line; otherwise at the top of the latest turn.
            .onAppear { if working { proxy.scrollTo("end", anchor: .bottom) } else { following = false } }
            .onChange(of: working) { _, nowWorking in
                if nowWorking { following = true; proxy.scrollTo("end", anchor: .bottom) }
            }
            .onChange(of: store.liveRevision[sessionId] ?? 0) { _, _ in
                if following { withAnimation(.easeOut(duration: 0.15)) { proxy.scrollTo("end", anchor: .bottom) } }
            }
        }
        .toolbar {
            // Past turns: a small button beside the title, so the conversation starts at the top.
            ToolbarItem(placement: .topBarTrailing) {
                NavigationLink(value: WatchRoute.pastTurns(s.id)) {
                    Image(systemName: "clock.arrow.circlepath").foregroundStyle(.white)
                }
                .accessibilityLabel("Past turns")
            }
            ToolbarItemGroup(placement: .bottomBar) {
                if working {
                    // One button: Stop, or Send now when something is queued (stopping delivers the queue anyway).
                    Button { send(queued.isEmpty ? "stop" : "sendNow", extra: [:], spinner: "stop") } label: {
                        if sending == "stop" { ProgressView() }
                        else { Image(systemName: queued.isEmpty ? "stop.fill" : "paperplane.fill").foregroundStyle(.white) }
                    }
                    .tint(queued.isEmpty ? WatchTheme.danger : WatchTheme.clay)
                    .accessibilityLabel(queued.isEmpty ? "Stop" : "Send now")
                    .disabled(sending != nil)
                }
                if !questionPending {
                    Button(action: typeReply) { Image(systemName: "keyboard").foregroundStyle(.white) }
                        .accessibilityLabel("Type")
                        .disabled(sending != nil)
                    Button(action: talk) {
                        if sending == "talk" { ProgressView() } else { Image(systemName: "mic.fill").foregroundStyle(.white) }
                    }
                    .tint(WatchTheme.clay)
                    .accessibilityLabel("Talk")
                    .disabled(sending != nil)
                }
            }
        }
    }

    // MARK: - Header

    /// One small status line: dot, state · age.
    private func header(_ s: WatchSession, now: Date) -> some View {
        HStack(spacing: 5) {
            if let dot = s.dot(readLocally: true) { StatusDot(dot: dot) }
            Text("\(s.state == .waiting ? "Needs you" : s.state.label) · \(s.stateAge(now: now))")
                .font(.caption2)
                .foregroundStyle(WatchTheme.textMuted)
        }
    }

    private func label(_ text: String) -> some View {
        Text(text.uppercased())
            .font(.caption2.weight(.semibold))
            .foregroundStyle(WatchTheme.textMuted)
    }

    // MARK: - The latest turn

    @ViewBuilder private func latestTurn(_ s: WatchSession, _ turn: LiveTurn?) -> some View {
        if let turn, turn.turn > 0 {
            if let p = turn.prompt { promptBubble(p) }
            LiveFeedView(turn: turn)
            let cut = turn.blocks.last(where: { $0.kind == "text" })?.text.hasPrefix("…") == true
            if !turn.active, cut { fullMessageButton(s) }
        } else if let text = s.lastText {
            // No turn since the server started: Claude's last message.
            WatchMarkdown(blocks: WatchMarkdown.parse(text))
            fullMessageButton(s)
        }
    }

    /// What started the turn: a right-aligned bubble (tap to see all of it).
    private func promptBubble(_ p: LiveTurn.Prompt) -> some View {
        VStack(alignment: .trailing, spacing: 2) {
            if let from = p.from {
                Text("From \(from)").font(.caption2).foregroundStyle(WatchTheme.textMuted)
            }
            Button { promptExpanded.toggle() } label: {
                Text(p.text.isEmpty ? AttributedString("Attachment") : WatchMarkdown.inline(p.text))
                    .font(.footnote)
                    .foregroundStyle(p.text.isEmpty ? WatchTheme.textMuted : WatchTheme.textPrimary)
                    .lineLimit(promptExpanded ? nil : 4)
                    .multilineTextAlignment(.leading)
                    .padding(8)
                    .background(WatchTheme.raised, in: RoundedRectangle(cornerRadius: 12))
            }
            .buttonStyle(.plain)
        }
        .frame(maxWidth: .infinity, alignment: .trailing)
        .padding(.leading, 16)
    }

    private func fullMessageButton(_ s: WatchSession) -> some View {
        NavigationLink(value: WatchRoute.lastAnswer(s.id)) {
            Label("Full message", systemImage: "doc.text")
                .foregroundStyle(WatchTheme.textPrimary)
                .frame(maxWidth: .infinity)
        }
        .buttonBorderShape(.capsule)
    }

    @ViewBuilder private func queue(_ queued: [WatchSession.Queued]) -> some View {
        if !queued.isEmpty {
            VStack(alignment: .leading, spacing: 4) {
                label("Queued")
                ForEach(queued) { q in
                    Text(WatchMarkdown.inline(q.text))
                        .font(.footnote)
                        .foregroundStyle(WatchTheme.textSecondary)
                        .padding(6)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .overlay(RoundedRectangle(cornerRadius: 10)
                            .stroke(WatchTheme.textMuted, style: StrokeStyle(lineWidth: 1, dash: [3, 3])))
                }
                Text("Claude reads these at its next step")
                    .font(.caption2)
                    .foregroundStyle(WatchTheme.textMuted)
            }
        }
    }

    // MARK: - Waiting on you

    @ViewBuilder private func pending(_ s: WatchSession) -> some View {
        let kind = s.pendingKind
        let questions = s.pendingQuestions
        if !questions.isEmpty {
            ForEach(Array(questions.enumerated()), id: \.offset) { i, q in questionCard(i, q) }
            actionButton("Send", action: "answer", primary: true, enabled: allAnswered(questions)) {
                ["answers": answers(questions)]
            }
        } else if kind == "plan" || kind == "tool" {
            Text(WatchMarkdown.inline(s.question ?? (kind == "plan" ? "Approve the plan?" : "Allow this tool?")))
                .font(.footnote)
                .foregroundStyle(WatchTheme.textPrimary)
            actionButton("Approve", action: "approve", primary: true)
            actionButton("Deny", action: "deny", primary: false)
        }
    }

    /// Type: the watch's text input (keyboard, Scribble or dictation), sent as a reply. A Talk
    /// message that failed is offered as a suggestion.
    private func typeReply() {
        let suggestions = retryText.map { [$0] } ?? []
        _ = Dictation.present(typing: true, suggestions: suggestions) { text in
            let message = (text ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            guard !message.isEmpty else { return }
            send("reply", extra: ["text": message], spinner: "talk", onSuccess: { retryText = nil })
        }
    }

    private func actionButton(_ title: String, action: String, primary: Bool, enabled: Bool = true,
                              extra: @escaping () -> [String: Any] = { [:] }) -> some View {
        Button {
            send(action, extra: extra())
        } label: {
            HStack {
                Spacer()
                if sending == action { ProgressView().frame(width: 16, height: 16) } else { Text(title) }
                Spacer()
            }
        }
        .tint(primary ? WatchTheme.clay : nil)
        .buttonStyle(.borderedProminent)
        .buttonBorderShape(.capsule)
        .disabled(!enabled || sending != nil)
    }

    /// Talk: Apple's dictation on the watch, sent as soon as you tap Done. If dictation can't be
    /// opened directly, the text input opens instead.
    private func talk() {
        let shown = Dictation.present { text in
            let message = (text ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            guard !message.isEmpty else { return }
            send("reply", extra: ["text": message], spinner: "talk",
                 onSuccess: { retryText = nil }, onFailure: { retryText = message })
        }
        if !shown { typeReply() }
    }

    private func send(_ action: String, extra: [String: Any], spinner: String? = nil,
                      onSuccess: @escaping () -> Void = {}, onFailure: @escaping () -> Void = {}) {
        sending = spinner ?? action
        failed = false
        Task {
            let ok = await store.perform(action, on: sessionId, extra: extra)
            sending = nil
            haptic(success: ok)
            if ok { onSuccess() } else { failed = true; onFailure() }
        }
    }

    private func haptic(success: Bool) {
        #if os(watchOS)
        WKInterfaceDevice.current().play(success ? .success : .failure)
        #endif
    }

    // MARK: - Questions

    private func questionCard(_ i: Int, _ q: PromptQuestion) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(WatchMarkdown.inline(q.question))
                .font(.footnote)
                .foregroundStyle(WatchTheme.textPrimary)
                .fixedSize(horizontal: false, vertical: true)
            ForEach(Array(q.options.enumerated()), id: \.offset) { j, label in
                Button { toggle(i, j, multi: q.multiSelect) } label: {
                    optionRow(label, on: isPicked(i, j, q), multi: q.multiSelect)
                }
                .buttonStyle(.plain)
                .disabled(sending != nil)
            }
            otherRow(i, q)
        }
        .padding(8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(WatchTheme.raised, in: RoundedRectangle(cornerRadius: 12))
    }

    private func optionRow(_ label: String, on: Bool, multi: Bool) -> some View {
        HStack(spacing: 6) {
            Image(systemName: multi ? (on ? "checkmark.square.fill" : "square") : (on ? "largecircle.fill.circle" : "circle"))
                .foregroundStyle(on ? WatchTheme.clay : WatchTheme.textMuted)
            Text(WatchMarkdown.inline(label))
                .font(.footnote)
                .foregroundStyle(WatchTheme.textSecondary)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .contentShape(Rectangle())
    }

    /// "Other…": a text field (dictation, Scribble or keyboard) that selects Other with its text.
    private func otherRow(_ i: Int, _ q: PromptQuestion) -> some View {
        let text = Binding(get: { otherText[i] ?? "" }, set: { otherText[i] = $0 })
        return HStack(spacing: 6) {
            let on = otherCounts(i, q)
            Image(systemName: q.multiSelect ? (on ? "checkmark.square.fill" : "square") : (on ? "largecircle.fill.circle" : "circle"))
                .foregroundStyle(on ? WatchTheme.clay : WatchTheme.textMuted)
            TextField("Other…", text: text)
                .font(.footnote)
                .disabled(sending != nil)
                .onSubmit {
                    let blank = (otherText[i] ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                    guard !q.multiSelect else { return }
                    if blank { otherChosen.remove(i) } else { otherChosen.insert(i); picks[i] = [] }
                }
        }
    }

    private func toggle(_ i: Int, _ j: Int, multi: Bool) {
        if multi {
            var set = picks[i] ?? []
            if set.contains(j) { set.remove(j) } else { set.insert(j) }
            picks[i] = set
        } else {
            picks[i] = [j]
            otherChosen.remove(i)
        }
    }

    private func isPicked(_ i: Int, _ j: Int, _ q: PromptQuestion) -> Bool {
        if !q.multiSelect, otherChosen.contains(i) { return false }
        return picks[i]?.contains(j) == true
    }

    private func trimmedOther(_ i: Int) -> String {
        (otherText[i] ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func otherCounts(_ i: Int, _ q: PromptQuestion) -> Bool {
        guard !trimmedOther(i).isEmpty else { return false }
        return q.multiSelect || otherChosen.contains(i)
    }

    private func allAnswered(_ qs: [PromptQuestion]) -> Bool {
        qs.indices.allSatisfy { !answer($0, qs[$0]).isEmpty }
    }

    /// Single-select: the label or the Other text. Multi-select: ticked labels in option order,
    /// Other text last, joined with ", ".
    private func answer(_ i: Int, _ q: PromptQuestion) -> String {
        if !q.multiSelect {
            if otherCounts(i, q) { return trimmedOther(i) }
            guard let j = picks[i]?.first, q.options.indices.contains(j) else { return "" }
            return q.options[j]
        }
        var parts = q.options.indices.filter { picks[i]?.contains($0) == true }.map { q.options[$0] }
        if otherCounts(i, q) { parts.append(trimmedOther(i)) }
        return parts.joined(separator: ", ")
    }

    private func answers(_ qs: [PromptQuestion]) -> [String: String] {
        var out: [String: String] = [:]
        for (i, q) in qs.enumerated() { out[q.question] = answer(i, q) }
        return out
    }
}

/// Following the live feed: on watchOS 11+, your own scrolling (a drag or the Digital Crown)
/// pauses following, and coming to rest at the bottom resumes it. The app's own scrolling to the
/// newest line doesn't count. watchOS 10 always follows.
private struct FollowTracker: ViewModifier {
    @Binding var following: Bool

    func body(content: Content) -> some View {
        if #available(watchOS 11.0, iOS 18.0, *) {
            content.onScrollPhaseChange { _, phase, context in
                switch phase {
                case .interacting, .decelerating:
                    following = false
                case .idle:
                    let g = context.geometry
                    if g.contentSize.height - (g.contentOffset.y + g.containerSize.height) < 100 { following = true }
                default:
                    break
                }
            }
        } else {
            content
        }
    }
}
