import SwiftUI

/// "MCP servers" sheet (session ••• menu): live status of every MCP server in this session's
/// Claude process, with reconnect, enable/disable, sign-in and restart.
/// docs/plan-mcp-servers.md → iOS app → Per-session sheet.
struct SessionMcpSheet: View {
    let sessionId: String

    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss

    private enum LoadState: Equatable { case loading, loaded, notRunning, unavailable, failed(String) }
    private enum RestartPhase: Equatable { case idle, requesting, waiting, timedOut }

    @State private var list: SessionMcpList?
    @State private var loadState: LoadState = .loading
    /// Bumped whenever a list arrives from a load or an action (not from polling): restarts the poll window.
    @State private var pollGeneration = 0

    @State private var reconnecting: Set<String> = []
    /// Switch value requested while its toggle call runs.
    @State private var toggling: [String: Bool] = [:]
    @State private var rowErrors: [String: String] = [:]
    @State private var expanded: Set<String> = []
    @State private var expandedErrors: Set<String> = []
    @State private var startingSignIn: String?
    @State private var signIn: McpSignInTarget?

    @State private var reconnectingAll = false
    @State private var reconnectAllError: String?

    @State private var restart: RestartPhase = .idle
    @State private var restartError: String?

    private var session: ClaudeSession? { appState.sessions.first { $0.id == sessionId } }

    /// Working, or waiting on you (question, permission, plan): the server refuses a restart.
    private var sessionBusy: Bool {
        guard let session else { return false }
        switch session.status {
        case .working, .waitingForInput, .needsPermission, .planMode: return true
        case .idle, .unknown: return session.prompt != nil
        }
    }

    private var hasPending: Bool { list?.servers.contains { $0.status == "pending" } == true }

    private var projectName: String { list?.project?.name ?? session?.projectName ?? "this project" }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    content
                }
                .padding(16)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .refreshable { await load() }
            .background(Theme.bgPage)
            .navigationTitle("MCP servers")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                        .fontWeight(.semibold)
                        .foregroundStyle(Theme.clay)
                }
            }
        }
        .presentationDetents([.large])
        .presentationBackground(Theme.bgPage)
        .task { await load() }
        // While any server is pending, reload every 2 s for up to 30 s.
        .task(id: "\(hasPending)-\(pollGeneration)") { await pollWhilePending() }
        .task(id: restart == .waiting) { await waitForRestart() }
        .sheet(item: $signIn) { target in
            McpSignInSheet(target: target) { fresh in apply(fresh) }
        }
    }

    // MARK: Content

    @ViewBuilder
    private var content: some View {
        if restart == .waiting {
            HStack(spacing: 10) {
                ProgressView().tint(Theme.textMuted)
                Text("Restarting Claude…")
                    .font(.claudeUI)
                    .foregroundStyle(Theme.textSecondary)
            }
            .frame(maxWidth: .infinity)
            .padding(.top, 32)
        } else if restart == .timedOut {
            McpStateMessage(text: "Claude hasn't come back yet.") { retryLoad() }
        } else {
            switch loadState {
            case .loading:
                ProgressView().tint(Theme.textMuted).frame(maxWidth: .infinity).padding(.top, 32)
            case .notRunning:
                McpStateMessage(text: "Claude isn't running in this session yet.") { retryLoad() }
            case .unavailable:
                McpStateMessage(text: McpText.unavailable)
            case .failed(let message):
                McpStateMessage(text: message) { retryLoad() }
            case .loaded:
                loaded
            }
        }
    }

    @ViewBuilder
    private var loaded: some View {
        let servers = list?.servers ?? []
        Text(projectName)
            .font(.claudeCaption)
            .foregroundStyle(Theme.textMuted)

        if servers.isEmpty {
            Text("No MCP servers in this session.")
                .font(.claudeUI)
                .foregroundStyle(Theme.textMuted)
                .frame(maxWidth: .infinity)
                .padding(.vertical, 16)
        } else {
            Button {
                Task { await reconnectAll() }
            } label: {
                McpWideButtonLabel(title: "Reconnect all", systemImage: "arrow.clockwise", busy: reconnectingAll)
            }
            .buttonStyle(SunkenCapsuleButtonStyle())
            .disabled(reconnectingAll)
            if let reconnectAllError {
                errorCaption(reconnectAllError)
            }

            ForEach([("user", "User"), ("project", "This project"), ("plugin", "Plugins")], id: \.0) { group, title in
                let rows = servers.filter { Self.group(of: $0) == group }
                if !rows.isEmpty {
                    sectionHeader(title)
                    ForEach(rows) { server in row(server) }
                }
            }

            Text("Turning a server off applies to every session in \(projectName) until it's turned back on.")
                .font(.claudeFootnote)
                .foregroundStyle(Theme.textMuted)
                .fixedSize(horizontal: false, vertical: true)
        }

        Button {
            restartClaude()
        } label: {
            McpWideButtonLabel(title: "Restart Claude", busy: restart == .requesting)
        }
        .buttonStyle(SunkenCapsuleButtonStyle())
        .disabled(sessionBusy || restart != .idle)
        .opacity(sessionBusy ? 0.5 : 1)
        .padding(.top, 6)
        if let restartError {
            errorCaption(restartError)
        }
        Text(sessionBusy
             ? "Finish or stop the current turn first"
             : "Restarts this session's Claude to pick up MCP config changes. The conversation continues.")
            .font(.claudeFootnote)
            .foregroundStyle(Theme.textMuted)
            .fixedSize(horizontal: false, vertical: true)
    }

    /// Unknown groups go under User (the server's own fallback).
    private static func group(of server: SessionMcpServer) -> String {
        ["project", "plugin"].contains(server.group) ? server.group : "user"
    }

    private func row(_ server: SessionMcpServer) -> some View {
        SessionMcpRow(
            server: server,
            projectName: projectName,
            isExpanded: expanded.contains(server.name),
            errorExpanded: expandedErrors.contains(server.name),
            isReconnecting: reconnecting.contains(server.name),
            switchValue: toggling[server.name] ?? (server.status != "disabled"),
            isToggling: toggling[server.name] != nil,
            isStartingSignIn: startingSignIn == server.name,
            actionError: rowErrors[server.name],
            onTap: { flip(&expanded, server.name) },
            onTapError: { flip(&expandedErrors, server.name) },
            onReconnect: { Task { await reconnect(server.name) } },
            onToggle: { enabled in Task { await toggle(server.name, enabled: enabled) } },
            onSignIn: { Task { await startSignIn(server.name) } }
        )
    }

    private func flip(_ set: inout Set<String>, _ name: String) {
        withAnimation(.easeInOut(duration: 0.2)) {
            if set.contains(name) { set.remove(name) } else { set.insert(name) }
        }
    }

    private func sectionHeader(_ title: String) -> some View {
        Text(title.uppercased())
            .font(.claudeCaptionSemibold)
            .foregroundStyle(Theme.textMuted)
            .padding(.top, 6)
    }

    private func errorCaption(_ text: String) -> some View {
        Text(text)
            .font(.claudeCaption)
            .foregroundStyle(Theme.danger)
            .fixedSize(horizontal: false, vertical: true)
    }

    // MARK: Loading

    private func apply(_ fresh: SessionMcpList) {
        list = fresh
        loadState = .loaded
        pollGeneration += 1
    }

    private func load() async {
        do {
            apply(try await appState.client.fetchSessionMcp(sessionId: sessionId))
        } catch {
            fail(error)
        }
    }

    private func fail(_ error: Error) {
        if McpText.isCancellation(error) { return }
        if McpText.isUnavailable(error) { loadState = .unavailable }
        else if McpText.status(of: error) == 409 { loadState = .notRunning }
        else { loadState = .failed(McpText.message(for: error)) }
    }

    private func retryLoad() {
        restart = .idle
        loadState = .loading
        Task { await load() }
    }

    private func pollWhilePending() async {
        guard hasPending else { return }
        let start = Date()
        while Date().timeIntervalSince(start) < 30 {
            try? await Task.sleep(for: .seconds(2))
            guard !Task.isCancelled, restart == .idle else { return }
            // Polling doesn't restart the 30 s window, and a failed poll keeps the current list.
            if let fresh = try? await appState.client.fetchSessionMcp(sessionId: sessionId) {
                list = fresh
            }
            if !hasPending { return }
        }
    }

    // MARK: Actions

    private func reconnect(_ name: String) async {
        reconnecting.insert(name)
        rowErrors[name] = nil
        defer { reconnecting.remove(name) }
        do {
            apply(try await appState.client.reconnectMcp(sessionId: sessionId, name: name))
        } catch {
            rowErrors[name] = McpText.message(for: error)
        }
    }

    private func reconnectAll() async {
        reconnectingAll = true
        reconnectAllError = nil
        defer { reconnectingAll = false }
        do {
            apply(try await appState.client.reconnectMcp(sessionId: sessionId, name: nil))
        } catch {
            reconnectAllError = McpText.message(for: error)
        }
    }

    private func toggle(_ name: String, enabled: Bool) async {
        guard toggling[name] == nil else { return }
        toggling[name] = enabled
        rowErrors[name] = nil
        do {
            let fresh = try await appState.client.toggleMcp(sessionId: sessionId, name: name, enabled: enabled)
            toggling[name] = nil
            apply(fresh)
        } catch {
            // The switch falls back to the server's last known state.
            toggling[name] = nil
            rowErrors[name] = McpText.message(for: error)
        }
    }

    private func startSignIn(_ name: String) async {
        guard startingSignIn == nil else { return }
        startingSignIn = name
        rowErrors[name] = nil
        defer { startingSignIn = nil }
        do {
            let auth = try await appState.client.startMcpAuth(sessionId: sessionId, name: name)
            guard let url = URL(string: auth.authUrl) else {
                rowErrors[name] = "Claude sent a sign-in link the app can't open."
                return
            }
            signIn = McpSignInTarget(sessionId: sessionId, serverName: name, authUrl: url, callbackPort: auth.callbackPort)
        } catch {
            rowErrors[name] = McpText.message(for: error)
        }
    }

    private func restartClaude() {
        guard restart == .idle, !sessionBusy else { return }
        restartError = nil
        restart = .requesting
        Task {
            do {
                try await appState.client.restartSession(id: sessionId)
                restart = .waiting
            } catch {
                restart = .idle
                restartError = McpText.message(for: error)
            }
        }
    }

    /// After a restart: wait until the session's process is `running` again (60 s), then reload.
    private func waitForRestart() async {
        guard restart == .waiting else { return }
        let start = Date()
        var sawDown = false
        while !Task.isCancelled, restart == .waiting {
            let process = session?.process
            if let process, process != "running" { sawDown = true }
            let elapsed = Date().timeIntervalSince(start)
            // The sessions update may skip the restarting/starting states if the restart is quick,
            // so after a few seconds "running" alone counts.
            if (process == "running" || process == nil) && (sawDown || elapsed > 5) {
                // Load first: leaving .waiting changes this task's id, which would cancel the request.
                loadState = .loading
                await load()
                restart = .idle
                return
            }
            if elapsed >= 60 {
                restart = .timedOut
                return
            }
            try? await Task.sleep(for: .milliseconds(500))
        }
    }
}

// MARK: - Row

private struct SessionMcpRow: View {
    let server: SessionMcpServer
    let projectName: String
    let isExpanded: Bool
    let errorExpanded: Bool
    let isReconnecting: Bool
    let switchValue: Bool
    let isToggling: Bool
    let isStartingSignIn: Bool
    let actionError: String?
    let onTap: () -> Void
    let onTapError: () -> Void
    let onReconnect: () -> Void
    let onToggle: (Bool) -> Void
    let onSignIn: () -> Void

    private var detail: String {
        switch server.status {
        case "connected":
            let tools = McpText.tools(server.tools.count)
            guard let v = server.version, !v.isEmpty else { return "connected · \(tools)" }
            return "\(v.hasPrefix("v") ? v : "v" + v) · \(tools)"
        default:
            return McpText.statusWord(server.status)
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 10) {
                McpStatusDot(color: McpText.dotColor(for: server.status), pending: server.status == "pending",
                             label: McpText.statusLabel(server.status))
                VStack(alignment: .leading, spacing: 2) {
                    Text(mcpDisplayName(server.name))
                        .font(.claudeUISemibold)
                        .foregroundStyle(Theme.textPrimary)
                        .lineLimit(1)
                    Text(detail)
                        .font(.claudeCaption)
                        .foregroundStyle(Theme.textMuted)
                        .lineLimit(1)
                }
                .frame(maxWidth: .infinity, alignment: .leading)

                if server.status == "needs-auth" {
                    Button(action: onSignIn) {
                        if isStartingSignIn {
                            ProgressView().controlSize(.mini).tint(.white)
                        } else {
                            Text("Sign in")
                        }
                    }
                    .buttonStyle(LinkCapsuleButtonStyle())
                    .disabled(isStartingSignIn)
                }

                if server.status != "disabled" {
                    Button(action: onReconnect) {
                        Group {
                            if isReconnecting {
                                ProgressView().controlSize(.small).tint(Theme.textSecondary)
                            } else {
                                Image(systemName: "arrow.clockwise")
                                    .font(.system(size: 15))
                                    .foregroundStyle(Theme.textSecondary)
                            }
                        }
                        .frame(width: 32, height: 32)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .disabled(isReconnecting)
                    .accessibilityLabel(isReconnecting ? "Reconnecting \(mcpDisplayName(server.name))" : "Reconnect \(mcpDisplayName(server.name))")
                }

                Toggle("", isOn: Binding(get: { switchValue }, set: { onToggle($0) }))
                    .labelsHidden()
                    .tint(Theme.clay)
                    .disabled(isToggling)
                    .accessibilityLabel("\(mcpDisplayName(server.name)) on in \(projectName)")
            }

            if server.status == "failed", let error = server.error, !error.isEmpty {
                Text(error)
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.danger)
                    .lineLimit(errorExpanded ? nil : 3)
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
                    .padding(.leading, 24)
                    .contentShape(Rectangle())
                    .onTapGesture(perform: onTapError)
                    .accessibilityHint(errorExpanded ? "" : "Shows the full error")
            }

            if let actionError {
                Text(actionError)
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.danger)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.leading, 24)
            }

            if isExpanded {
                VStack(alignment: .leading, spacing: 6) {
                    if let summary = server.summary, !summary.isEmpty {
                        Text(summary)
                            .font(.claudeMono(11))
                            .foregroundStyle(Theme.textMuted)
                            .textSelection(.enabled)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    if !server.tools.isEmpty {
                        Text(server.tools.joined(separator: ", "))
                            .font(.claudeCaption)
                            .foregroundStyle(Theme.textMuted)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                .padding(.leading, 24)
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .claudeCard()
        .contentShape(Rectangle())
        .onTapGesture(perform: onTap)
        .accessibilityAction(named: isExpanded ? "Hide details" : "Show details", onTap)
    }
}
