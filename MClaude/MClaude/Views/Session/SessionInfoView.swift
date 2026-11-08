import SwiftUI

/// Small ring showing how much of the context window is in use (header + Info screen).
struct ContextGauge: View {
    let fraction: Double
    var size: CGFloat = 13
    var showLabel = false
    var lineWidth: CGFloat? = nil

    static func color(for fraction: Double) -> Color {
        fraction >= 0.85 ? Theme.danger : fraction >= 0.6 ? Theme.warning : Theme.textMuted
    }

    var body: some View {
        HStack(spacing: 4) {
            ZStack {
                Circle().stroke(Theme.border, lineWidth: lineWidth ?? max(1.5, size * 0.16))
                Circle()
                    .trim(from: 0, to: max(0.02, fraction))
                    .stroke(Self.color(for: fraction),
                            style: StrokeStyle(lineWidth: lineWidth ?? max(1.5, size * 0.16), lineCap: .round))
                    .rotationEffect(.degrees(-90))
            }
            .frame(width: size, height: size)
            if showLabel {
                Text("\(Int((fraction * 100).rounded()))%")
                    .font(.claudeFootnote)
                    .monospacedDigit()
                    .foregroundStyle(Self.color(for: fraction))
            }
        }
    }
}

/// "Info" screen (••• menu, or tap the header's session name or gauge): the full session title
/// (editable), then context in use and the tokens spent in this conversation, with the CLI's
/// list-price cost. See docs/design-claude-uplift.md → Session titles and the Info screen.
struct SessionInfoView: View {
    let sessionId: String
    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss
    @State private var usage: SessionUsage?
    @State private var failed = false
    @State private var editingTitle = false
    @State private var draftTitle = ""
    @State private var savingTitle = false
    @State private var renameFailed = false
    /// Cross-session peers (Messages card); empty hides the card (also on 404 / error / while loading).
    @State private var peers: [PeerSummary] = []
    @FocusState private var titleFocused: Bool

    private var session: ClaudeSession? { appState.sessions.first { $0.id == sessionId } }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    titleCard
                    if !peers.isEmpty { PeerListCard(peers: peers) }
                    if let usage {
                        contextCard(usage)
                        tokensCard(usage)
                        if usage.byModel.count > 0 { modelsCard(usage) }
                    } else if failed {
                        Text("Usage isn't available for this session yet.")
                            .font(.claudeUI)
                            .foregroundStyle(Theme.textMuted)
                            .frame(maxWidth: .infinity)
                            .padding(.top, 24)
                    } else {
                        ProgressView().tint(Theme.textMuted).frame(maxWidth: .infinity).padding(.top, 24)
                    }
                }
                .padding(16)
            }
            .background(Theme.bgPage)
            .navigationDestination(for: PeerThreadTarget.self) { target in
                PeerThreadView(sessionId: sessionId, target: target) { id in openPeerSession(id) }
            }
            .navigationTitle("Info")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                        .fontWeight(.semibold)
                        .foregroundStyle(Theme.clay)
                }
            }
        }
        .presentationDetents([.medium, .large])
        .presentationBackground(Theme.bgPage)
        // Reload when a turn starts/ends or the context changes
        .task(id: "\(session?.status.rawValue ?? "")-\(session?.contextTokens ?? 0)") { await load() }
        .task { await loadPeers() }
    }

    private func loadPeers() async {
        let list = (try? await appState.client.fetchPeers(sessionId: sessionId)) ?? []
        peers = list.filter { $0.peer.isResolved }
    }

    /// "Open <peer>" in a thread pushed here: close the Info sheet, then switch (push-tap path).
    private func openPeerSession(_ id: String) {
        dismiss()
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(350))
            appState.openSessionRequest = id
        }
    }

    private func load() async {
        do {
            usage = try await appState.client.fetchUsage(id: sessionId)
            failed = false
        } catch {
            if usage == nil { failed = true }
        }
    }

    // MARK: Title

    private var displayName: String { session?.displayName ?? "Session \(sessionId)" }

    private var trimmedDraft: String { draftTitle.trimmingCharacters(in: .whitespacesAndNewlines) }

    private var canSave: Bool { !savingTitle && !trimmedDraft.isEmpty && trimmedDraft != displayName }

    private var titleCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                cardTitle("Title")
                Spacer()
                if !editingTitle {
                    Button {
                        draftTitle = displayName
                        renameFailed = false
                        editingTitle = true
                        titleFocused = true
                    } label: {
                        Image(systemName: "pencil")
                            .font(.system(size: 15))
                            .foregroundStyle(Theme.textSecondary)
                            .frame(width: 32, height: 28, alignment: .trailing)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Rename session")
                }
            }
            if editingTitle {
                TextField("Session title", text: $draftTitle, axis: .vertical)
                    .font(.system(size: 20, design: .serif))
                    .foregroundStyle(Theme.textPrimary)
                    .lineLimit(1...4)
                    .focused($titleFocused)
                    .submitLabel(.done)
                    .onSubmit { if canSave { Task { await saveTitle() } } }
                    .onChange(of: draftTitle) { _, new in
                        // Some keyboards insert a newline on Return instead of submitting; treat it as Save
                        guard new.contains("\n") else { return }
                        draftTitle = new.replacingOccurrences(of: "\n", with: "")
                        if canSave { Task { await saveTitle() } }
                    }
                    .padding(10)
                    .background(Theme.bgSunken, in: RoundedRectangle(cornerRadius: Theme.radiusSmall, style: .continuous))
                if renameFailed {
                    Text("Couldn't rename this session")
                        .font(.claudeCaption)
                        .foregroundStyle(Theme.danger)
                }
                HStack(spacing: 16) {
                    Spacer()
                    Button("Cancel") {
                        editingTitle = false
                        renameFailed = false
                        titleFocused = false
                    }
                    .font(.claudeUI)
                    .foregroundStyle(Theme.textSecondary)
                    .disabled(savingTitle)
                    Button {
                        Task { await saveTitle() }
                    } label: {
                        Group {
                            if savingTitle {
                                ProgressView().tint(.white)
                            } else {
                                Text("Save").font(.claudeUISemibold)
                            }
                        }
                        .foregroundStyle(.white)
                        .padding(.horizontal, 18)
                        .frame(height: 34)
                        .background(Theme.clay.opacity(canSave || savingTitle ? 1 : 0.4), in: Capsule())
                    }
                    .buttonStyle(.plain)
                    .disabled(!canSave)
                }
            } else {
                Text(displayName)
                    .font(.system(size: 20, design: .serif))
                    .foregroundStyle(Theme.textPrimary)
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            if let cwd = session?.cwd, !cwd.isEmpty {
                Text(cwd)
                    .font(.claudeMono(11))
                    .foregroundStyle(Theme.textMuted)
                    .lineLimit(2)
                    .truncationMode(.middle)
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .claudeCard()
    }

    private func saveTitle() async {
        guard canSave else { return }
        savingTitle = true
        renameFailed = false
        defer { savingTitle = false }
        do {
            try await appState.renameSession(id: sessionId, name: trimmedDraft)
            editingTitle = false
            titleFocused = false
        } catch {
            renameFailed = true
        }
    }

    // MARK: Cards

    private func contextCard(_ u: SessionUsage) -> some View {
        let used = session?.contextTokens ?? u.contextTokens
        let limit = session?.contextWindow ?? u.contextWindow
        let fraction: Double? = {
            guard let used, let limit, limit > 0 else { return nil }
            return min(1, Double(used) / Double(limit))
        }()
        return VStack(alignment: .leading, spacing: 12) {
            cardTitle("Context")
            HStack(spacing: 18) {
                ZStack {
                    ContextGauge(fraction: fraction ?? 0, size: 84, lineWidth: 8)
                    Text(fraction.map { "\(Int(($0 * 100).rounded()))%" } ?? "—")
                        .font(.system(size: 22, weight: .semibold, design: .serif))
                        .foregroundStyle(Theme.textPrimary)
                }
                VStack(alignment: .leading, spacing: 4) {
                    Text(used.map(Self.tokens) ?? "—")
                        .font(.system(size: 24, weight: .regular, design: .serif))
                        .foregroundStyle(Theme.textPrimary)
                    Text("of \(limit.map(Self.tokens) ?? "—") tokens")
                        .font(.claudeCaption)
                        .foregroundStyle(Theme.textMuted)
                    if let model = u.model {
                        Text(Self.shortModel(model))
                            .font(.claudeMono(11))
                            .foregroundStyle(Theme.textMuted)
                    }
                }
            }
            Text("What Claude currently has in view: system prompt, conversation and tool results. Claude Code compacts the conversation automatically as it nears the limit.")
                .font(.claudeFootnote)
                .foregroundStyle(Theme.textMuted)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .claudeCard()
    }

    private func tokensCard(_ u: SessionUsage) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            cardTitle("This conversation")
            HStack(alignment: .firstTextBaseline) {
                Text(Self.tokens(u.totalTokens))
                    .font(.system(size: 30, weight: .regular, design: .serif))
                    .foregroundStyle(Theme.textPrimary)
                Text("tokens")
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.textMuted)
                Spacer()
                VStack(alignment: .trailing, spacing: 2) {
                    Text(u.costUSD.map(Self.cost) ?? "—")
                        .font(.system(size: 22, weight: .regular, design: .serif))
                        .foregroundStyle(Theme.textPrimary)
                    Text(u.partial == true ? "list price, partial" : "list price")
                        .font(.claudeFootnote)
                        .foregroundStyle(Theme.textMuted)
                }
            }
            LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: 10) {
                tile("Input", u.inputTokens)
                tile("Output", u.outputTokens)
                tile("Cache write", u.cacheWriteTokens)
                tile("Cache read", u.cacheReadTokens)
            }
            if u.turns > 0 {
                Text(u.turns == 1 ? "1 turn" : "\(u.turns) turns")
                    .font(.claudeFootnote)
                    .foregroundStyle(Theme.textMuted)
            }
            if u.partial == true {
                Text("Earlier turns were counted from the saved transcript, which has tokens but no cost.")
                    .font(.claudeFootnote)
                    .foregroundStyle(Theme.textMuted)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .claudeCard()
    }

    private func modelsCard(_ u: SessionUsage) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            cardTitle("By model")
            ForEach(u.byModel.keys.sorted(), id: \.self) { name in
                if let m = u.byModel[name] {
                    HStack {
                        Text(Self.shortModel(name))
                            .font(.claudeMono(12))
                            .foregroundStyle(Theme.textSecondary)
                        Spacer()
                        Text(Self.tokens(m.totalTokens))
                            .font(.claudeCaption)
                            .monospacedDigit()
                            .foregroundStyle(Theme.textPrimary)
                        Text(m.costUSD.map(Self.cost) ?? "—")
                            .font(.claudeCaption)
                            .monospacedDigit()
                            .foregroundStyle(Theme.textMuted)
                            .frame(minWidth: 56, alignment: .trailing)
                    }
                }
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .claudeCard()
    }

    private func tile(_ label: String, _ value: Int) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(Self.tokens(value))
                .font(.system(size: 18, weight: .semibold))
                .monospacedDigit()
                .foregroundStyle(Theme.textPrimary)
            Text(label)
                .font(.claudeFootnote)
                .foregroundStyle(Theme.textMuted)
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.bgSunken, in: RoundedRectangle(cornerRadius: Theme.radiusSmall, style: .continuous))
    }

    private func cardTitle(_ title: String) -> some View {
        Text(title)
            .font(.claudeCaptionSemibold)
            .foregroundStyle(Theme.textMuted)
    }

    // MARK: Formatting

    static func tokens(_ n: Int) -> String {
        switch n {
        case ..<1_000: return "\(n)"
        case ..<1_000_000: return String(format: n < 10_000 ? "%.1fK" : "%.0fK", Double(n) / 1_000)
        default: return String(format: "%.2fM", Double(n) / 1_000_000)
        }
    }

    static func cost(_ usd: Double) -> String {
        usd < 0.01 ? "<$0.01" : String(format: "$%.2f", usd)
    }

    /// "us.anthropic.claude-opus-4-6-v1:0" → "claude-opus-4-6"
    static func shortModel(_ name: String) -> String {
        var s = name
        for prefix in ["us.anthropic.", "anthropic.", "eu.anthropic."] where s.hasPrefix(prefix) { s.removeFirst(prefix.count) }
        if let r = s.range(of: #"-\d{8}"#, options: .regularExpression) { s = String(s[..<r.lowerBound]) }
        if let r = s.range(of: "-v1:0") { s = String(s[..<r.lowerBound]) }
        return s
    }
}
