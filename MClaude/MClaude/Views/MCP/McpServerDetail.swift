import SwiftUI

/// One server in Settings → MCP servers: its config (secrets masked), every session's status,
/// sign-in, edit and delete. docs/plan-mcp-servers.md → Global screen → Detail screen.
struct McpServerDetail: View {
    let model: McpGlobalModel
    let name: String
    var onOpenSession: (String) -> Void

    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss
    @State private var revealed: Set<String> = []
    @State private var showEdit = false
    @State private var confirmDelete = false
    @State private var deleting = false
    @State private var deleteError: String?
    @State private var startingSignIn = false
    @State private var signInError: String?
    @State private var signIn: McpSignInTarget?

    private var server: GlobalMcpServer? { model.list?.servers.first { $0.name == name } }

    var body: some View {
        Form {
            if let server {
                content(server)
            } else {
                McpStateMessage(text: "\(name) isn't in the MCP config anymore.")
                    .listRowBackground(Color.clear)
            }
        }
        .font(.claudeUI)
        .foregroundStyle(Theme.textPrimary)
        .scrollContentBackground(.hidden)
        .background(Theme.bgPage)
        .navigationTitle(mcpDisplayName(name))
        .navigationBarTitleDisplayMode(.inline)
        .refreshable { await model.load(appState.client) }
        .toolbar {
            if server?.editable == true {
                ToolbarItem(placement: .primaryAction) {
                    Button("Edit") { showEdit = true }
                }
            }
        }
        .sheet(isPresented: $showEdit) {
            if let config = server?.config {
                McpServerForm(mode: .edit(name: name, config: config)) { fresh in
                    if let fresh { model.apply(fresh) } else { Task { await model.load(appState.client) } }
                }
            }
        }
        .sheet(item: $signIn) { target in
            McpSignInSheet(target: target) { _ in
                Task { await model.load(appState.client) }
            }
        }
        .alert("Delete \(name)?", isPresented: $confirmDelete) {
            Button("Delete", role: .destructive) { Task { await delete() } }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Sessions keep it until they restart.")
        }
    }

    @ViewBuilder
    private func content(_ server: GlobalMcpServer) -> some View {
        let isUser = server.group != "plugin"

        if isUser, let config = server.config {
            configSections(config, summary: server.summary)
        } else if isUser {
            Section {
                Text(server.summary ?? "No config")
                    .font(.claudeMono(13))
                    .foregroundStyle(Theme.textSecondary)
            } header: { header("Config") }
            .listRowBackground(Theme.bgRaised)
        } else {
            Section {
                Text("Provided by a plugin")
                    .foregroundStyle(Theme.textMuted)
                if let summary = server.summary, !summary.isEmpty {
                    Text(summary)
                        .font(.claudeMono(12))
                        .foregroundStyle(Theme.textMuted)
                        .textSelection(.enabled)
                }
            }
            .listRowBackground(Theme.bgRaised)
        }

        if let first = server.sessions.first(where: { $0.status == "needs-auth" }) {
            Section {
                HStack {
                    Button {
                        Task { await startSignIn(sessionId: first.id) }
                    } label: {
                        if startingSignIn {
                            ProgressView().controlSize(.mini).tint(.white)
                        } else {
                            Text("Sign in")
                        }
                    }
                    .buttonStyle(LinkCapsuleButtonStyle())
                    .disabled(startingSignIn)
                    Spacer()
                }
                if let signInError {
                    Text(signInError)
                        .font(.claudeCaption)
                        .foregroundStyle(Theme.danger)
                }
            }
            .listRowBackground(Theme.bgRaised)
        }

        Section {
            if server.sessions.isEmpty {
                Text("No running sessions")
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.textMuted)
            }
            ForEach(server.sessions) { report in
                Button { onOpenSession(report.id) } label: { sessionRow(report) }
                    .buttonStyle(.plain)
                    .accessibilityHint("Opens this session")
            }
        } header: { header("Sessions") }
        .listRowBackground(Theme.bgRaised)

        if isUser {
            Section {
                Button(role: .destructive) {
                    confirmDelete = true
                } label: {
                    HStack {
                        Text("Delete server")
                            .foregroundStyle(Theme.danger)
                        Spacer()
                        if deleting { ProgressView() }
                    }
                }
                .disabled(deleting)
                if let deleteError {
                    Text(deleteError)
                        .font(.claudeCaption)
                        .foregroundStyle(Theme.danger)
                }
            }
            .listRowBackground(Theme.bgRaised)
        }
    }

    @ViewBuilder
    private func configSections(_ config: McpConfig, summary: String?) -> some View {
        let type = config.type ?? "stdio"
        Section {
            labeled("Type", type)
            if let command = config.command {
                labeled("Command", command, mono: true)
            }
            if let url = config.url {
                labeled("URL", url, mono: true)
            }
            if config.command == nil && config.url == nil, let summary, !summary.isEmpty {
                labeled("Details", summary, mono: true)
            }
        } header: { header("Config") }
        .listRowBackground(Theme.bgRaised)

        if !config.args.isEmpty {
            Section {
                ForEach(Array(config.args.enumerated()), id: \.offset) { _, arg in
                    Text(arg)
                        .font(.claudeMono(13))
                        .foregroundStyle(Theme.textSecondary)
                        .textSelection(.enabled)
                }
            } header: { header("Arguments") }
            .listRowBackground(Theme.bgRaised)
        }
        if !config.env.isEmpty {
            secretSection("Environment", prefix: "env", config.env)
        }
        if !config.headers.isEmpty {
            secretSection("Headers", prefix: "header", config.headers)
        }
    }

    private func secretSection(_ title: String, prefix: String, _ values: [String: String]) -> some View {
        Section {
            ForEach(values.keys.sorted(), id: \.self) { key in
                let id = "\(prefix):\(key)"
                let shown = revealed.contains(id)
                HStack(spacing: 10) {
                    Text(key)
                        .font(.claudeMono(13))
                        .foregroundStyle(Theme.textPrimary)
                        .lineLimit(1)
                    Spacer(minLength: 8)
                    Text(shown ? (values[key] ?? "") : "••••••••")
                        .font(.claudeMono(13))
                        .foregroundStyle(Theme.textMuted)
                        .lineLimit(shown ? nil : 1)
                        .multilineTextAlignment(.trailing)
                        .textSelection(.enabled)
                    Button {
                        if shown { revealed.remove(id) } else { revealed.insert(id) }
                    } label: {
                        Image(systemName: shown ? "eye.slash" : "eye")
                            .font(.system(size: 14))
                            .foregroundStyle(Theme.textSecondary)
                            .frame(width: 28, height: 28)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(shown ? "Hide \(key)" : "Show \(key)")
                }
            }
        } header: { header(title) }
        .listRowBackground(Theme.bgRaised)
    }

    private func sessionRow(_ report: McpSessionReport) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 10) {
                McpStatusDot(color: McpText.dotColor(for: report.status), label: McpText.statusLabel(report.status))
                Text(report.name)
                    .foregroundStyle(Theme.textPrimary)
                    .lineLimit(1)
                Spacer(minLength: 8)
                Text(McpText.statusWord(report.status))
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.textMuted)
                Image(systemName: "chevron.right")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(Theme.textMuted)
                    .accessibilityHidden(true)
            }
            if let error = report.error, !error.isEmpty {
                Text(error)
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.danger)
                    .lineLimit(3)
                    .padding(.leading, 24)
            }
        }
        .contentShape(Rectangle())
    }

    private func labeled(_ label: String, _ value: String, mono: Bool = false) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(label)
                .font(.claudeCaption)
                .foregroundStyle(Theme.textMuted)
            Text(value)
                .font(mono ? .claudeMono(13) : .claudeUI)
                .foregroundStyle(Theme.textPrimary)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private func header(_ title: String) -> some View {
        Text(title)
            .font(.claudeCaptionSemibold)
            .foregroundStyle(Theme.textMuted)
    }

    // MARK: Actions

    private func startSignIn(sessionId: String) async {
        guard !startingSignIn else { return }
        startingSignIn = true
        signInError = nil
        defer { startingSignIn = false }
        do {
            let auth = try await appState.client.startMcpAuth(sessionId: sessionId, name: name)
            guard let url = URL(string: auth.authUrl) else {
                signInError = "Claude sent a sign-in link the app can't open."
                return
            }
            signIn = McpSignInTarget(sessionId: sessionId, serverName: name, authUrl: url, callbackPort: auth.callbackPort)
        } catch {
            signInError = McpText.message(for: error)
        }
    }

    private func delete() async {
        guard !deleting else { return }
        deleting = true
        deleteError = nil
        defer { deleting = false }
        do {
            let fresh = try await appState.client.deleteMcpServer(name: name)
            dismiss()
            if let fresh { model.apply(fresh) } else { await model.load(appState.client) }
        } catch {
            deleteError = McpText.message(for: error)
        }
    }
}
