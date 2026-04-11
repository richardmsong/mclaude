import SwiftUI

struct SessionDetailView: View {
    let sessionId: String

    @Environment(AppState.self) private var appState
    @Environment(\.scenePhase) private var scenePhase
    @State private var viewModel = SessionDetailViewModel()
    @State private var showStructured = true
    @State private var planExpanded = false
    @State private var planContent: String?
    @State private var planFileName: String?

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
                    }
                )
            } else {
                TerminalOutputView(
                    output: output,
                    sessionId: sessionId,
                    onLoadMore: { appState.loadMoreOutput(sessionId: sessionId) }
                )
            }

            Divider()

            // Detected prompt UI (AskUserQuestion)
            if let prompt = session?.prompt, let options = prompt.options, !options.isEmpty {
                let questions = [AskQuestion(
                    header: "",
                    question: prompt.question,
                    options: options.map { ($0, "") }
                )]
                AskUserQuestionView(questions: questions) { key in
                    Task { await viewModel.sendKey(client: appState.client, id: sessionId, key: key) }
                }
                .padding(.horizontal)
                .padding(.vertical, 8)
                Divider()
            }

            // Action error banner
            if let error = viewModel.actionError {
                HStack {
                    Image(systemName: "xmark.circle.fill")
                    Text(error)
                    Spacer()
                    Button("Dismiss") { viewModel.actionError = nil }
                        .font(.caption)
                }
                .font(.caption)
                .foregroundStyle(.white)
                .padding(.horizontal)
                .padding(.vertical, 4)
                .background(.red)
            }

            // Plan summary (when awaiting approval)
            if let session, session.status == .planMode {
                planSummaryView
            }

            // Quick actions
            if let session, session.status.needsAttention {
                quickActions
                Divider()
            }

            // Input bar
            InputBarView(
                text: $viewModel.inputText,
                isSending: viewModel.isSending,
                skills: appState.skills.filter { $0.source == "builtin" || $0.source == "global" || $0.source == session?.projectName },
                onSend: {
                    Task { await viewModel.send(client: appState.client, id: sessionId) }
                },
                onPhoto: { data in
                    Task { await viewModel.sendScreenshot(client: appState.client, id: sessionId, laptop: session?.laptop, imageData: data) }
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
            )
        }
        .navigationTitle(session?.projectName ?? "Session \(sessionId)")
        .navigationBarTitleDisplayMode(.inline)
        .task {
            await appState.loadEvents(sessionId: sessionId)
        }
        .onChange(of: events.count) {
            viewModel.clearMatchedPending(events: events)
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
            ToolbarItem(placement: .topBarTrailing) {
                HStack(spacing: 12) {
                    Button {
                        showStructured.toggle()
                    } label: {
                        Image(systemName: showStructured ? "text.bubble" : "terminal")
                            .font(.subheadline)
                    }
                    if let session {
                        Image(systemName: session.status.icon)
                            .foregroundStyle(session.status.color)
                    }
                }
            }
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
                    Image(systemName: "list.clipboard.fill")
                        .foregroundStyle(.purple)
                        .font(.subheadline)
                    Text("View Plan")
                        .font(.subheadline)
                        .fontWeight(.medium)
                        .foregroundStyle(.primary)
                    if let name = planFileName {
                        Text(name)
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                            .truncationMode(.middle)
                    }
                    Spacer()
                    Image(systemName: planExpanded ? "chevron.up" : "chevron.down")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 10)
            }
            .buttonStyle(.plain)

            if planExpanded, let content = planContent {
                Divider()
                ScrollView {
                    Text(content)
                        .font(.system(.caption, design: .monospaced))
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 10)
                }
                .frame(maxHeight: 300)
            }
        }
        .background(Color(.systemGray6))
        .task { await loadPlan() }
    }

    private var quickActions: some View {
        HStack(spacing: 16) {
            Button {
                Task { await viewModel.approve(client: appState.client, id: sessionId) }
            } label: {
                Label("Approve", systemImage: "checkmark.circle.fill")
                    .font(.subheadline)
                    .fontWeight(.medium)
            }
            .buttonStyle(.borderedProminent)
            .tint(.green)

            Button {
                Task { await viewModel.cancel(client: appState.client, id: sessionId) }
            } label: {
                Label("Cancel", systemImage: "xmark.circle.fill")
                    .font(.subheadline)
                    .fontWeight(.medium)
            }
            .buttonStyle(.borderedProminent)
            .tint(.red)
            .disabled(viewModel.cancelCooldown)
        }
        .padding(.horizontal)
        .padding(.vertical, 8)
    }
}
