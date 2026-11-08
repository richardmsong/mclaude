import SwiftUI

/// State shared by the global MCP list, its detail screens and the add/edit form.
@MainActor @Observable
final class McpGlobalModel {
    enum LoadState: Equatable { case loading, loaded, unavailable, forbidden, failed(String) }

    var list: GlobalMcpList?
    var state: LoadState = .loading
    var toast: String?
    var restartingAll = false
    var restartError: String?

    func apply(_ fresh: GlobalMcpList) {
        list = fresh
        state = .loaded
    }

    /// Load `GET /mcp`. A failed reload keeps the list already on screen.
    func load(_ client: APIClient) async {
        do {
            apply(try await client.fetchMcp())
        } catch {
            if McpText.isCancellation(error) { return }   // e.g. the screen went away mid-load
            if McpText.isUnavailable(error) { state = .unavailable }
            else if McpText.status(of: error) == 403 { state = .forbidden }
            else if list == nil { state = .failed(McpText.message(for: error)) }
            else { toast = McpText.message(for: error) }
        }
    }

    /// Restart every session's Claude, toast the counts, and reload after 5 s.
    func restartAll(_ client: APIClient) async {
        guard !restartingAll else { return }
        restartingAll = true
        restartError = nil
        defer { restartingAll = false }
        do {
            let result = try await client.restartAllSessions()
            toast = "Restarting \(result.restarting) now, \(result.afterTurn) after their turn"
            Task { [weak self] in
                try? await Task.sleep(for: .seconds(5))
                await self?.load(client)
            }
        } catch {
            restartError = McpText.message(for: error)
        }
    }
}

/// Status of one server across every reporting session (list row dot and detail).
struct McpAggregate {
    let total: Int
    let connected: Int
    let failed: Int
    let needsAuth: Int
    let pending: Int
    let disabled: Int

    init(_ reports: [McpSessionReport]) {
        total = reports.count
        connected = reports.filter { $0.status == "connected" }.count
        failed = reports.filter { $0.status == "failed" }.count
        needsAuth = reports.filter { $0.status == "needs-auth" }.count
        pending = reports.filter { $0.status == "pending" }.count
        disabled = reports.filter { $0.status == "disabled" }.count
    }

    var dot: Color? {
        if total == 0 { return nil }
        if failed > 0 { return Theme.danger }
        if needsAuth > 0 { return Theme.link }
        if pending > 0 { return Theme.clay }
        if connected == total { return Theme.success }
        return nil
    }

    var detail: String {
        if total == 0 { return "no running sessions" }
        if failed > 0 { return "\(connected)/\(total) ⚠︎" }
        if needsAuth > 0 { return "needs sign-in" }
        if disabled > 0 { return "off in \(disabled)" }
        return "\(connected)/\(total)"
    }

    var spoken: String {
        if total == 0 { return "No running sessions" }
        var parts = ["connected in \(connected) of \(total)"]
        if failed > 0 { parts.append("failed in \(failed)") }
        if needsAuth > 0 { parts.append("needs sign-in") }
        if pending > 0 { parts.append("connecting in \(pending)") }
        if disabled > 0 { parts.append("off in \(disabled)") }
        return parts.joined(separator: ", ")
    }
}

/// Settings → MCP servers: the user-level MCP config and each server's status across sessions.
/// docs/plan-mcp-servers.md → iOS app → Global screen.
struct McpSettingsList: View {
    /// Close Settings and open a session (tapping a session row in a detail screen).
    var onOpenSession: (String) -> Void

    @Environment(AppState.self) private var appState
    @State private var model = McpGlobalModel()
    @State private var showAdd = false
    @State private var confirmRestart = false

    var body: some View {
        Form {
            switch model.state {
            case .loading:
                stateRow { ProgressView().tint(Theme.textMuted) }
            case .unavailable:
                stateRow { McpStateMessage(text: McpText.unavailable).padding(.bottom, 24) }
            case .forbidden:
                stateRow { McpStateMessage(text: "Only the laptop's owner can manage MCP servers.").padding(.bottom, 24) }
            case .failed(let message):
                stateRow {
                    McpStateMessage(text: message) { Task { await model.load(appState.client) } }
                        .padding(.bottom, 24)
                }
            case .loaded:
                loaded
            }
        }
        .font(.claudeUI)
        .foregroundStyle(Theme.textPrimary)
        .scrollContentBackground(.hidden)
        .background(Theme.bgPage)
        .navigationTitle("MCP servers")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            if model.state == .loaded {
                ToolbarItem(placement: .primaryAction) {
                    Button { showAdd = true } label: {
                        Image(systemName: "plus")
                    }
                    .accessibilityLabel("Add MCP server")
                }
            }
        }
        .refreshable { await model.load(appState.client) }
        .task { await model.load(appState.client) }
        .sheet(isPresented: $showAdd) {
            McpServerForm(mode: .add) { fresh in
                if let fresh { model.apply(fresh) } else { Task { await model.load(appState.client) } }
            }
        }
        .alert(restartTitle, isPresented: $confirmRestart) {
            Button("Restart") { Task { await model.restartAll(appState.client) } }
            Button("Cancel", role: .cancel) {}
        }
        .mcpToast($model.toast)
        .animation(.snappy, value: model.toast)
    }

    private var restartTitle: String {
        "Restart Claude in \(McpText.sessions(model.list?.sessionCount ?? 0))?"
    }

    private func stateRow<Content: View>(@ViewBuilder _ content: () -> Content) -> some View {
        content()
            .frame(maxWidth: .infinity)
            .listRowBackground(Color.clear)
    }

    @ViewBuilder
    private var loaded: some View {
        let servers = model.list?.servers ?? []
        let stale = model.list?.staleSessions ?? 0

        if stale > 0 {
            Section {
                HStack(spacing: 12) {
                    Text("MCP config changed. \(McpText.sessions(stale)) \(stale == 1 ? "uses" : "use") the old config.")
                        .font(.claudeCaption)
                        .foregroundStyle(Theme.textPrimary)
                        .fixedSize(horizontal: false, vertical: true)
                    Spacer(minLength: 8)
                    Button {
                        Task { await model.restartAll(appState.client) }
                    } label: {
                        if model.restartingAll {
                            ProgressView().controlSize(.small).tint(.white)
                        } else {
                            Text("Apply")
                        }
                    }
                    .buttonStyle(ClayCapsuleButtonStyle())
                    .disabled(model.restartingAll)
                }
            }
            .listRowBackground(Theme.bgRaised)
        }

        let user = servers.filter { $0.group != "plugin" }
        let plugins = servers.filter { $0.group == "plugin" }
        if servers.isEmpty {
            Section {
                Text("No MCP servers. Tap ＋ to add one.")
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.textMuted)
            }
            .listRowBackground(Theme.bgRaised)
        }
        if !user.isEmpty {
            Section { rows(user) } header: { sectionHeader("User") }
                .listRowBackground(Theme.bgRaised)
        }
        if !plugins.isEmpty {
            Section { rows(plugins) } header: { sectionHeader("Plugins") }
                .listRowBackground(Theme.bgRaised)
        }

        Section {
            Button {
                confirmRestart = true
            } label: {
                HStack {
                    Text("Restart all sessions")
                        .foregroundStyle(Theme.clay)
                    Spacer()
                    if model.restartingAll { ProgressView() }
                }
            }
            .disabled(model.restartingAll)
            if let error = model.restartError {
                Text(error)
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.danger)
            }
        } footer: {
            Text("Restarts Claude in every session to reload MCP servers and config. Working sessions restart when their turn ends.")
                .font(.claudeFootnote)
                .foregroundStyle(Theme.textMuted)
        }
        .listRowBackground(Theme.bgRaised)
    }

    private func rows(_ servers: [GlobalMcpServer]) -> some View {
        ForEach(servers) { server in
            NavigationLink {
                McpServerDetail(model: model, name: server.name, onOpenSession: onOpenSession)
            } label: {
                McpGlobalRow(server: server)
            }
        }
    }

    private func sectionHeader(_ title: String) -> some View {
        Text(title)
            .font(.claudeCaptionSemibold)
            .foregroundStyle(Theme.textMuted)
    }
}

private struct McpGlobalRow: View {
    let server: GlobalMcpServer

    var body: some View {
        let agg = McpAggregate(server.sessions)
        let summary = server.group != "plugin" ? server.summary.flatMap { $0.isEmpty ? nil : $0 } : nil
        HStack(spacing: 10) {
            McpStatusDot(color: agg.dot, label: agg.spoken)
            Text(mcpDisplayName(server.name))
                .font(.claudeUI)
                .foregroundStyle(Theme.textPrimary)
                .lineLimit(1)
                .layoutPriority(1)
            Spacer(minLength: 8)
            Text(summary.map { "\(agg.detail) · \($0)" } ?? agg.detail)
                .font(.claudeCaption)
                .foregroundStyle(Theme.textMuted)
                .lineLimit(1)
                .truncationMode(.tail)
        }
        .accessibilityElement(children: .combine)
    }
}
