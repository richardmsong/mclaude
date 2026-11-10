import SwiftUI
#if os(watchOS)
import WatchKit
#endif

/// docs/plan-watch-app.md → Session detail. Updates in place; pops when the session ends.
struct SessionDetailView: View {
    let sessionId: String
    @Environment(WatchStore.self) private var store
    @Environment(\.dismiss) private var dismiss

    /// Per question: ticked option indexes, and the Other text (Other counts when non-blank).
    @State private var picks: [Int: Set<Int>] = [:]
    @State private var otherText: [Int: String] = [:]
    /// Single-select questions whose Other is the chosen answer.
    @State private var otherChosen: Set<Int> = []
    @State private var reply = ""
    @State private var sending: String?
    @State private var failed = false
    @FocusState private var typing: Bool
    /// Live view: keep the newest line in view (scrolling up pauses it, watchOS 11+).
    @State private var following = true

    private var session: WatchSession? { store.session(sessionId) }

    /// While Claude works, the screen is the live view (docs/plan-watch-app.md → Live view).
    private var isLive: Bool {
        guard let s = session else { return false }
        return s.state == .working || store.live[sessionId]?.active == true
    }

    var body: some View {
        Group {
            if let session, isLive {
                liveView(session)
                    .navigationTitle(session.name)
            } else if let session {
                TimelineView(.periodic(from: .now, by: 5)) { context in
                    ScrollView {
                        VStack(alignment: .leading, spacing: 10) {
                            header(session, now: context.date)
                            recap(session)
                            actions(session)
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
                .navigationTitle(session.name)
            } else {
                Color.clear
            }
        }
        // Subscribed to the live turn the whole time the screen is open, so a turn that starts
        // switches to the live view as soon as Claude writes (not at the next 15 s refresh).
        .task { await store.followLive(sessionId) }
        .onAppear { store.markRead(sessionId) }
        .onChange(of: session?.since) { _, _ in if session != nil { store.markRead(sessionId) } }
        .onChange(of: session == nil) { _, gone in if gone { dismiss() } }
        // A new pending request starts with fresh selections.
        .onChange(of: session?.questions ?? []) { _, _ in picks = [:]; otherText = [:]; otherChosen = [] }
    }

    // MARK: - Live view

    private func liveView(_ s: WatchSession) -> some View {
        let turn = store.live[sessionId]
        let queued = s.queue ?? []
        return ScrollViewReader { proxy in ScrollView {
            VStack(alignment: .leading, spacing: 10) {
                header(s, now: Date()).id("header")
                LiveFeedView(turn: turn).id("feed")
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
                    .id("queue")
                }
                if failed {
                    Text("Couldn't send. Try again.").font(.caption2).foregroundStyle(WatchTheme.danger).id("failed")
                }
                // Room for the bottom toolbar, so the newest line sits above the buttons.
                Color.clear.frame(height: 52).id("end")
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .modifier(FollowTracker(following: $following))
        .onAppear { proxy.scrollTo("end", anchor: .bottom) }
        .onChange(of: store.liveRevision[sessionId] ?? 0) { _, _ in
            if following { withAnimation(.easeOut(duration: 0.15)) { proxy.scrollTo("end", anchor: .bottom) } }
        }
        }
        .toolbar {
            ToolbarItemGroup(placement: .bottomBar) {
                // One button: Stop, or Send now when something is queued (stopping delivers the queue anyway).
                Button { send(queued.isEmpty ? "stop" : "sendNow", extra: [:], spinner: "stop") } label: {
                    if sending == "stop" { ProgressView() }
                    else { Image(systemName: queued.isEmpty ? "stop.fill" : "paperplane.fill").foregroundStyle(.white) }
                }
                .tint(queued.isEmpty ? WatchTheme.danger : WatchTheme.clay)
                .accessibilityLabel(queued.isEmpty ? "Stop" : "Send now")
                .disabled(sending != nil)

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

    /// Type: the watch's text input (keyboard, Scribble or dictation), sent as a reply.
    private func typeReply() {
        _ = Dictation.present(typing: true) { text in
            let message = (text ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            guard !message.isEmpty else { return }
            send("reply", extra: ["text": message], spinner: "talk")
        }
    }

    // MARK: - Header and recap

    private func header(_ s: WatchSession, now: Date) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 6) {
                if let dot = s.dot(readLocally: true) { StatusDot(dot: dot) }
                Text("\(s.state == .waiting ? "Needs you" : s.state.label) · \(s.stateAge(now: now))")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(WatchTheme.textPrimary)
            }
            Text(s.project)
                .font(.caption2)
                .foregroundStyle(WatchTheme.textMuted)
        }
    }

    @ViewBuilder private func recap(_ s: WatchSession) -> some View {
        if let r = s.recap {
            block("Goal", r.goal)
            block("Where it is", r.progress)
            block("Next", r.next)
        } else if let text = s.lastText {
            VStack(alignment: .leading, spacing: 1) {
                label("Last message")
                WatchMarkdown(blocks: WatchMarkdown.parse(text))
            }
        }
        if s.lastText != nil {
            NavigationLink(value: WatchRoute.lastAnswer(s.id)) {
                Label("Full message", systemImage: "doc.text")
                    .foregroundStyle(WatchTheme.textPrimary)
                    .frame(maxWidth: .infinity)
            }
            .buttonBorderShape(.capsule)
        }
    }

    private func label(_ text: String) -> some View {
        Text(text.uppercased())
            .font(.caption2.weight(.semibold))
            .foregroundStyle(WatchTheme.textMuted)
    }

    /// A recap field: label, then the text with inline Markdown.
    private func block(_ title: String, _ text: String) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            label(title)
            Text(WatchMarkdown.inline(text))
                .font(.footnote)
                .foregroundStyle(WatchTheme.textPrimary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    // MARK: - Actions

    @ViewBuilder private func actions(_ s: WatchSession) -> some View {
        let kind = s.pendingKind
        let questions = s.pendingQuestions
        VStack(alignment: .leading, spacing: 8) {
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
            if s.state != .working, s.state != .waiting, s.recap?.nextBy == "claude" {
                actionButton("Continue", action: "continue", primary: false)
            }
            if questions.isEmpty {
                Button(action: talk) {
                    HStack {
                        Spacer()
                        if sending == "talk" { ProgressView().frame(width: 16, height: 16) }
                        else { Label("Talk", systemImage: "mic.fill") }
                        Spacer()
                    }
                }
                .tint(WatchTheme.clay)
                .buttonStyle(.borderedProminent)
                .buttonBorderShape(.capsule)
                .disabled(sending != nil)
                TextField("Type…", text: $reply)
                    .focused($typing)
                    .font(.footnote)
                    .disabled(sending != nil)
                    .onSubmit {
                        let text = reply.trimmingCharacters(in: .whitespacesAndNewlines)
                        guard !text.isEmpty else { return }
                        send("reply", extra: ["text": text]) { reply = "" }
                    }
            }
            if failed {
                Text("Couldn't send. Try again.")
                    .font(.caption2)
                    .foregroundStyle(WatchTheme.danger)
            }
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
    /// opened directly, the Type field's text input opens instead.
    private func talk() {
        let shown = Dictation.present { text in
            let message = (text ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            guard !message.isEmpty else { return }
            send("reply", extra: ["text": message], spinner: "talk", onFailure: { reply = message })
        }
        if !shown { typing = true }
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
