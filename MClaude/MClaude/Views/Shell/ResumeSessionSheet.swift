import SwiftUI

/// Resume sheet (docs/design-claude-uplift.md → Resume sheet): every Claude Code transcript on the
/// laptop that isn't an open session (ended MClaude sessions and terminal sessions), newest first,
/// with search and project chips. Tap to resume. Nothing can be deleted here.
struct ResumeSessionSheet: View {
    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss
    /// Called with the resumed session's id, after which the sheet closes.
    let onResumed: (String) -> Void

    private enum LoadState: Equatable { case loading, loaded, failed(String) }

    @State private var transcripts: [TranscriptInfo] = []
    @State private var loadState: LoadState = .loading
    @State private var searchText = ""
    @State private var project = ""
    @State private var resumingId: String?
    @State private var confirmInUse: TranscriptInfo?
    @State private var errorMessage: String?

    private var query: String { searchText.trimmingCharacters(in: .whitespaces) }

    private var projects: [String] {
        projectsByRecency(transcripts.map { ($0.projectName, $0.modifiedAt) })
    }

    private var visible: [TranscriptInfo] {
        transcripts
            .filter { project.isEmpty || $0.projectName == project }
            .filter {
                query.isEmpty || $0.displayName.localizedCaseInsensitiveContains(query)
                    || ($0.readablePrompt ?? "").localizedCaseInsensitiveContains(query)
                    || $0.projectName.localizedCaseInsensitiveContains(query)
            }
            .sorted { $0.modifiedAt > $1.modifiedAt }
    }

    var body: some View {
        NavigationStack {
            content
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(Theme.bgPage)
                .navigationTitle("Resume session")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .confirmationAction) {
                        Button("Done") { dismiss() }
                            .foregroundStyle(Theme.textSecondary)
                    }
                }
        }
        .tint(Theme.clay)
        .presentationDetents([.large])
        .presentationBackground(Theme.bgPage)
        .task { await load() }
        .confirmationDialog("This session may still be open in a terminal. Resuming it here too can mix up both copies.",
                            isPresented: Binding(get: { confirmInUse != nil }, set: { if !$0 { confirmInUse = nil } }),
                            titleVisibility: .visible,
                            presenting: confirmInUse) { transcript in
            Button("Resume") { resume(transcript) }
            Button("Cancel", role: .cancel) {}
        }
        .alert("Couldn't resume", isPresented: Binding(get: { errorMessage != nil }, set: { if !$0 { errorMessage = nil } })) {
            Button("OK", role: .cancel) { errorMessage = nil }
        } message: {
            Text(errorMessage ?? "")
        }
    }

    @ViewBuilder
    private var content: some View {
        switch loadState {
        case .loading:
            ProgressView("Loading sessions…")
                .tint(Theme.textMuted)
                .foregroundStyle(Theme.textMuted)
        case .failed(let message):
            VStack(spacing: 10) {
                Text("Couldn't load sessions")
                    .font(.system(size: 20, design: .serif))
                    .foregroundStyle(Theme.textPrimary)
                Text(message)
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.textMuted)
                    .multilineTextAlignment(.center)
                Button("Retry") { Task { await load() } }
                    .font(.claudeUISemibold)
                    .foregroundStyle(Theme.clay)
                    .padding(.top, 4)
            }
            .padding(.horizontal, 32)
        case .loaded where transcripts.isEmpty:
            VStack(spacing: 10) {
                Image(systemName: "clock.arrow.circlepath")
                    .font(.system(size: 28, weight: .light))
                    .foregroundStyle(Theme.textMuted)
                Text("No other sessions on this laptop")
                    .font(.system(size: 20, design: .serif))
                    .foregroundStyle(Theme.textPrimary)
                    .multilineTextAlignment(.center)
            }
            .padding(.horizontal, 32)
        case .loaded:
            list
        }
    }

    private var list: some View {
        let rows = visible
        return VStack(spacing: 0) {
            DrawerSearchField(text: $searchText, prompt: "Search")
                .padding(.horizontal, 16)
                .padding(.top, 8)
            if projects.count > 1 {
                ProjectChips(projects: projects, selection: $project)
                    .padding(.top, 10)
            }
            List {
                if rows.isEmpty {
                    Text("No matching sessions")
                        .font(.claudeCaption)
                        .foregroundStyle(Theme.textMuted)
                        .listRowBackground(Theme.bgRaised)
                } else {
                    ForEach(rows) { row($0) }
                }
            }
            .scrollContentBackground(.hidden)
            .scrollDismissesKeyboard(.immediately)
            .contentMargins(.top, 8, for: .scrollContent)
        }
    }

    private func row(_ t: TranscriptInfo) -> some View {
        Button {
            if t.inUse { confirmInUse = t } else { resume(t) }
        } label: {
            HStack(alignment: .center, spacing: 10) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(t.displayName)
                        .font(.claudeUI)
                        .foregroundStyle(Theme.textPrimary)
                        .lineLimit(1)
                    HStack(spacing: 0) {
                        Text(relativeTimeLabel(t.modifiedAt, now: Date()) + " · " + t.projectName)
                            .foregroundStyle(Theme.textMuted)
                        if t.inUse {
                            Text(" · In use").foregroundStyle(Theme.clay)
                        }
                    }
                    .font(.claudeFootnote)
                    .lineLimit(1)
                    if let preview = t.promptPreview {
                        Text(preview)
                            .font(.claudeFootnote)
                            .foregroundStyle(Theme.textMuted)
                            .lineLimit(1)
                    }
                }
                Spacer(minLength: 8)
                if resumingId == t.id {
                    ProgressView().controlSize(.small).tint(Theme.textMuted)
                }
            }
            .padding(.vertical, 2)
            .contentShape(Rectangle())
        }
        .disabled(resumingId != nil)
        .listRowBackground(Theme.bgRaised)
        .accessibilityHint("Resumes this session")
    }

    private func load() async {
        if transcripts.isEmpty { loadState = .loading }
        do {
            transcripts = try await appState.client.fetchTranscripts()
            loadState = .loaded
        } catch {
            loadState = .failed(Self.message(for: error))
        }
    }

    private func resume(_ t: TranscriptInfo) {
        guard resumingId == nil else { return }
        resumingId = t.id
        Task {
            defer { resumingId = nil }
            do {
                let id = try await appState.resumeTranscript(claudeSessionId: t.id)
                onResumed(id)
                dismiss()
            } catch {
                errorMessage = Self.message(for: error)
            }
        }
    }

    private static func message(for error: Error) -> String {
        if case APIError.http(let status) = error {
            switch status {
            case 404: return "That session's transcript is gone, or this server doesn't support resuming yet."
            case 422: return "Its project folder no longer exists."
            case 403: return "Only the laptop's owner can resume sessions."
            default: return "The server answered \(status)."
            }
        }
        return error.localizedDescription
    }
}
