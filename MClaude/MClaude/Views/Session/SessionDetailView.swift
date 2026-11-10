import SwiftUI

struct SessionDetailView: View {
    let sessionId: String
    var onOpenDrawer: () -> Void = {}
    var onNewSession: () -> Void = {}
    var onNewSessionInProject: ((String) -> Void)? = nil

    @Environment(AppState.self) private var appState
    @Environment(\.scenePhase) private var scenePhase
    @State private var viewModel = SessionDetailViewModel()
    @State private var showStructured = true
    @State private var planExpanded = false
    @State private var planContent: String?
    @State private var planFileName: String?
    @State private var showInfo = false
    @State private var showMcp = false
    @State private var exporting = false
    @State private var exportedFile: ExportedFile?
    @Environment(\.colorScheme) private var colorScheme

    private var session: ClaudeSession? {
        appState.sessions.first { $0.id == sessionId }
    }

    private var output: String {
        appState.outputCache[sessionId] ?? session?.lastOutput ?? ""
    }

    private var events: [SessionEvent] {
        appState.eventsCache[sessionId] ?? []
    }

    var body: some View {
        VStack(spacing: 0) {
            if showStructured {
                ConversationView(
                    events: events, sessionId: sessionId,
                    pendingMessages: viewModel.pendingMessages,
                    onRefresh: { await appState.loadEvents(sessionId: sessionId) },
                    onSendInput: { text in
                        Task { try? await appState.client.sendInput(id: sessionId, text: text, sendEnter: true) }
                    },
                    onSendKey: { key in
                        Task { await viewModel.sendKey(client: appState.client, id: sessionId, key: key) }
                    },
                    onAnswer: { answers in
                        await viewModel.answerQuestion(client: appState.client, id: sessionId, answers: answers)
                    },
                    hasPendingPrompt: session?.prompt != nil,
                    sessionWorking: session?.status == .working,
                    sessionCompacting: session?.compacting == true,
                    queue: session?.queue ?? [],
                    delivered: session?.delivered ?? [],
                    onEditQueued: { item, text in
                        Task {
                            do {
                                try await appState.client.editQueued(sessionId: sessionId, queueId: item.id, text: text)
                            } catch {
                                viewModel.actionError = (error as? APIError)?.isNotFound == true
                                    ? "Couldn't edit — it was already sent" : "Couldn't edit the queued message"
                            }
                        }
                    },
                    onDeleteQueued: { item in
                        Task {
                            do {
                                try await appState.client.deleteQueued(sessionId: sessionId, queueId: item.id)
                            } catch {
                                viewModel.actionError = (error as? APIError)?.isNotFound == true
                                    ? "Couldn't delete — it was already sent" : "Couldn't delete the queued message"
                            }
                        }
                    },
                    onFlushQueue: {
                        Task {
                            do {
                                try await appState.client.flushQueue(sessionId: sessionId)
                            } catch {
                                viewModel.actionError = "Couldn't send the queue now"
                            }
                        }
                    },
                    onLoadOlder: { await appState.loadOlderEvents(sessionId: sessionId) },
                    historyComplete: appState.historyComplete.contains(sessionId),
                    focusFinding: appState.findingFocus.flatMap { $0.sessionId == sessionId ? $0 : nil },
                    onLocateFinding: { await appState.locateFinding($0, sessionId: sessionId) },
                    onFocusFindingHandled: { focus in
                        if appState.findingFocus == focus { appState.findingFocus = nil }
                    }
                )
            } else {
                TerminalOutputView(
                    output: output,
                    sessionId: sessionId,
                    onLoadMore: { appState.loadMoreOutput(sessionId: sessionId) }
                )
            }

            // Action error banner
            if let error = viewModel.actionError {
                HStack(spacing: 8) {
                    Image(systemName: "exclamationmark.circle")
                        .foregroundStyle(Theme.danger)
                    Text(error)
                        .foregroundStyle(Theme.textPrimary)
                    Spacer()
                    Button("Dismiss") { viewModel.actionError = nil }
                        .font(.claudeCaptionSemibold)
                        .foregroundStyle(Theme.textSecondary)
                }
                .font(.claudeCaption)
                .padding(.horizontal, 14)
                .padding(.vertical, 9)
                .background(Theme.danger.opacity(0.10), in: RoundedRectangle(cornerRadius: Theme.radiusCard, style: .continuous))
                .padding(.horizontal, 10)
                .padding(.top, 4)
            }

            // Plan summary (when awaiting approval)
            if let session, session.status == .planMode {
                planSummaryView
            }

            // Quick actions
            if let session, session.status.needsAttention {
                quickActions
            }

            // Input bar. The draft is bound inside ComposerHost so a keystroke only redraws the
            // composer: bound from here, every keystroke redrew this screen and rebuilt the whole
            // transcript (docs/design-claude-uplift.md → Implementation notes).
            ComposerHost(model: viewModel) { draft in InputBarView(
                text: draft,
                isSending: viewModel.isSending,
                skills: appState.skills.filter { $0.source == "builtin" || $0.source == "global" || $0.source == session?.projectName },
                onSend: { text in
                    Task { await viewModel.send(client: appState.client, id: sessionId, text: text) }
                },
                onPhotos: { photos, caption in
                    Task {
                        await viewModel.sendPhotos(client: appState.client, id: sessionId, laptop: session?.laptop,
                                                   attachments: photos, caption: caption)
                    }
                },
                onFile: { data, filename in
                    Task { await viewModel.sendFile(client: appState.client, id: sessionId, laptop: session?.laptop, data: data, filename: filename) }
                },
                onVoiceSend: { transcript in
                    Task { await viewModel.sendVoice(client: appState.client, id: sessionId, text: transcript) }
                },
                onKey: { key in
                    Task { await viewModel.sendKey(client: appState.client, id: sessionId, key: key) }
                },
                onHistoryBack: { viewModel.historyBack() },
                onHistoryForward: { viewModel.historyForward() }
            ) }
        }
        .background(Theme.bgPage)
        .navigationTitle(session?.displayName ?? "Session \(sessionId)")
        .navigationBarTitleDisplayMode(.inline)
        .toolbarBackground(.hidden, for: .navigationBar)
        .sheet(isPresented: $showInfo) {
            SessionInfoView(sessionId: sessionId)
        }
        .sheet(isPresented: $showMcp) {
            SessionMcpSheet(sessionId: sessionId)
        }
        .sheet(item: $exportedFile) { file in
            ShareSheet(url: file.url)
                .presentationDetents([.medium, .large])
        }
        .task {
            await appState.loadEvents(sessionId: sessionId)
        }
        .onAppear {
            appState.visibleSessionId = sessionId
            installFullPageScreenshot()
        }
        .onDisappear {
            if appState.visibleSessionId == sessionId { appState.visibleSessionId = nil }
            FullPageScreenshot.shared.deactivate(for: sessionId)
        }
        .onChange(of: events.count) {
            viewModel.clearMatchedPending(events: events)
        }
        .onChange(of: appState.findingFocus, initial: true) { _, focus in
            // Landing on a finding's card needs the conversation, not the terminal view
            if focus?.sessionId == sessionId { showStructured = true }
        }
        .onChange(of: session?.status, initial: true) { _, status in
            viewModel.sessionBusy = status == .working
        }
        .onReceive(Timer.publish(every: 5, on: .main, in: .common).autoconnect()) { _ in
            viewModel.clearMatchedPending(events: events)
        }
        .onChange(of: scenePhase) { _, newPhase in
            if newPhase == .active {
                Task { await appState.loadEvents(sessionId: sessionId) }
            }
        }
        .toolbar {
            ToolbarItem(placement: .topBarLeading) {
                DrawerButton(action: onOpenDrawer)
            }
            ToolbarItem(placement: .principal) {
                HStack(spacing: 6) {
                    // Same dot as the drawer, minus unread: the session on screen is read
                    if let dot = session?.dot, dot != .unread {
                        SessionDotView(dot: dot, size: 7)
                    }
                    // Tap the name for the Info screen (full title, rename, usage)
                    Button { showInfo = true } label: {
                        HStack(spacing: 5) {
                            if session?.isOps == true { OpsMark() }   // the Ops session (design-claude-uplift → Drawer)
                            Text(session?.displayName ?? "Session \(sessionId)")
                                .font(.claudeUISemibold)
                                .foregroundStyle(session?.isOps == true ? Theme.ops : Theme.textPrimary)
                                .lineLimit(1)
                        }
                    }
                    .buttonStyle(.plain)
                    .accessibilityHint("Shows the full title and session info")
                    if let fraction = session?.contextFraction {
                        // Context used vs. the model's limit; tap for the Info screen
                        Button { showInfo = true } label: {
                            ContextGauge(fraction: fraction, size: 13, showLabel: true)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Context \(Int((fraction * 100).rounded())) percent used")
                    }
                }
                .accessibilityElement(children: .contain)
            }
            ToolbarItemGroup(placement: .topBarTrailing) {
                NewSessionButton(action: onNewSession, currentProject: session?.cwd, onNewInProject: onNewSessionInProject)
                Menu {
                    // Same toggle as the old header button: conversation vs. plain-text output
                    Picker("View", selection: $showStructured) {
                        Label("Conversation", systemImage: "text.bubble").tag(true)
                        Label("Terminal output", systemImage: "terminal").tag(false)
                    }
                    Button {
                        showInfo = true
                    } label: {
                        Label("Info", systemImage: "info.circle")
                    }
                    Button {
                        showMcp = true
                    } label: {
                        Label("MCP servers", systemImage: "puzzlepiece.extension")
                    }
                    Menu {
                        Button("Markdown") { Task { await export(.markdown) } }
                        Button("Plain text") { Task { await export(.plainText) } }
                        Button("PDF") { Task { await export(.pdf) } }
                    } label: {
                        Label("Export", systemImage: "square.and.arrow.up")
                    }
                    .disabled(exporting)
                    if let session {
                        Section {
                            Label(session.statusLabel, systemImage: session.processDown ? "arrow.clockwise.circle" : session.status.icon)
                            Label(session.cwd, systemImage: "folder")
                        }
                    }
                } label: {
                    if exporting {
                        ProgressView().controlSize(.small)
                    } else {
                        Image(systemName: "ellipsis")
                            .font(.system(size: 17, weight: .regular))
                            .foregroundStyle(Theme.textSecondary)
                    }
                }
                .accessibilityLabel(exporting ? "Exporting" : "More")
            }
        }
    }

    // MARK: - Export (docs/design-claude-uplift.md → Export)

    private enum ExportFormat { case markdown, plainText, pdf }

    private func export(_ format: ExportFormat) async {
        guard !exporting else { return }
        exporting = true
        defer { exporting = false }
        let all = await appState.fullHistory(sessionId: sessionId)
        let title = session?.displayName ?? "Session \(sessionId)"
        let cwd = session?.cwd ?? ""
        let data: Data?
        let ext: String
        switch format {
        // Markdown and plain text are built off the main thread: for a long session (1,000+ events)
        // building them froze the app for a second or more (Ops F-23).
        case .markdown:
            data = await Task.detached(priority: .userInitiated) {
                Data(TranscriptExport.markdown(events: all, title: title, cwd: cwd).utf8)
            }.value
            ext = "md"
        case .plainText:
            data = await Task.detached(priority: .userInitiated) {
                Data(TranscriptExport.plainText(events: all, title: title, cwd: cwd).utf8)
            }.value
            ext = "txt"
        case .pdf:
            let screen = currentScreenBounds
            data = TranscriptPDF.paged(events: all, session: session, sessionId: sessionId, width: screen.width,
                                       screenHeight: screen.height, scheme: colorScheme, appState: appState)
            ext = "pdf"
        }
        guard let data, let url = try? TranscriptExport.writeTempFile(data, title: title, ext: ext) else {
            viewModel.actionError = "Couldn't export the conversation"
            return
        }
        exportedFile = ExportedFile(url: url)
    }

    private var currentScreenBounds: CGRect {
        let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first
        return scene?.screen.bounds ?? CGRect(x: 0, y: 0, width: 393, height: 852)
    }

    /// Offer "Full Page" in the system screenshot editor while this session is on screen.
    private func installFullPageScreenshot() {
        defer {
            #if DEBUG
            // Simulator check (the screenshot editor can't be driven there): launch with
            // `-MClaudeDumpFullPage YES` to write the Full Page PDF to tmp/exports/fullpage.pdf.
            // `-MClaudeDumpExport YES`: export Markdown 4 s after opening (Ops F-23 stall check).
            if UserDefaults.standard.bool(forKey: "MClaudeDumpExport") {
                Task {
                    try? await Task.sleep(for: .seconds(4))
                    await export(.markdown)
                }
            }
            if UserDefaults.standard.bool(forKey: "MClaudeDumpFullPage") {
                Task {
                    try? await Task.sleep(for: .seconds(4))
                    if let page = await FullPageScreenshot.shared.provider?() {
                        _ = try? TranscriptExport.writeTempFile(page.data, title: "fullpage", ext: "pdf")
                    }
                }
            }
            #endif
        }
        FullPageScreenshot.shared.activate(for: sessionId) { [appState, sessionId] in
            // The whole conversation, like an export (older pages load first if needed)
            let all = await appState.fullHistory(sessionId: sessionId)
            let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first
            let width = scene?.screen.bounds.width ?? 393
            let scheme: ColorScheme = scene?.traitCollection.userInterfaceStyle == .dark ? .dark : .light
            let session = appState.sessions.first { $0.id == sessionId }
            return await TranscriptPDF.fullPage(events: all, session: session, sessionId: sessionId,
                                          width: width, scheme: scheme, appState: appState)
        }
    }

    private func loadPlan() async {
        do {
            let resp = try await appState.client.fetchPlan(id: sessionId)
            planContent = resp.plan
            planFileName = resp.fileName
        } catch {
            planContent = nil
            planFileName = nil
        }
    }

    private var planSummaryView: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button {
                withAnimation(.easeInOut(duration: 0.2)) {
                    planExpanded.toggle()
                }
            } label: {
                HStack(spacing: 8) {
                    Image(systemName: "list.clipboard")
                        .foregroundStyle(Theme.textSecondary)
                        .font(.system(size: 15, weight: .regular))
                    Text("View Plan")
                        .font(.claudeUISemibold)
                        .foregroundStyle(Theme.textPrimary)
                    if let name = planFileName {
                        Text(name)
                            .font(.claudeMono(11))
                            .foregroundStyle(Theme.textMuted)
                            .lineLimit(1)
                            .truncationMode(.middle)
                    }
                    Spacer()
                    Image(systemName: planExpanded ? "chevron.up" : "chevron.down")
                        .font(.caption)
                        .foregroundStyle(Theme.textMuted)
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 10)
            }
            .buttonStyle(.plain)

            if planExpanded, let content = planContent {
                Rectangle().fill(Theme.border).frame(height: 0.5)
                ScrollView {
                    Text(content)
                        .font(.claudeMono(12))
                        .foregroundStyle(Theme.textSecondary)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 10)
                }
                .frame(maxHeight: 300)
            }
        }
        .claudeCard()
        .padding(.horizontal, 10)
        .padding(.top, 6)
        .task { await loadPlan() }
    }

    private var quickActions: some View {
        HStack(spacing: 16) {
            Button {
                Task { await viewModel.approve(client: appState.client, id: sessionId) }
            } label: {
                Label("Approve", systemImage: "checkmark")
            }
            .buttonStyle(ClayCapsuleButtonStyle())

            Button {
                Task { await viewModel.cancel(client: appState.client, id: sessionId) }
            } label: {
                Label("Cancel", systemImage: "xmark")
            }
            .buttonStyle(SunkenCapsuleButtonStyle())
            .disabled(viewModel.cancelCooldown)
            .opacity(viewModel.cancelCooldown ? 0.5 : 1)
        }
        .padding(.horizontal)
        .padding(.top, 8)
    }
}

/// Owns the binding to the composer draft. Building the composer here (not in SessionDetailView's
/// body) keeps reads and writes of `inputText` out of the session screen's dependencies, so typing
/// re-renders only this view and the input bar.
private struct ComposerHost<Content: View>: View {
    let model: SessionDetailViewModel
    @ViewBuilder let content: (Binding<String>) -> Content

    var body: some View {
        content(Bindable(model).inputText)
    }
}
