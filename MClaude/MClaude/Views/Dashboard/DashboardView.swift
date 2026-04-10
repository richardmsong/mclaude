import SwiftUI

struct DashboardView: View {
    @Environment(AppState.self) private var appState
    @State private var viewModel = DashboardViewModel()
    @State private var showNewSession = false
    @State private var projects: [ProjectInfo] = []
    @State private var isLoadingProjects = false
    @State private var useTmux = true
    @State private var tmuxFilter: String? = nil  // nil = default, "all" = all, or specific name

    var body: some View {
        NavigationStack {
            Group {
                if viewModel.isLoading {
                    ProgressView("Connecting...")
                } else if !appState.isConnected && appState.sessions.isEmpty {
                    ContentUnavailableView(
                        "Disconnected",
                        systemImage: "wifi.slash",
                        description: Text(appState.lastDisconnectReason ?? "Cannot reach mclaude-server at \(appState.serverHost):\(appState.serverPort)")
                    )
                } else if appState.sessions.isEmpty {
                    ContentUnavailableView(
                        "No Sessions",
                        systemImage: "terminal",
                        description: Text("No Claude sessions detected in tmux")
                    )
                } else {
                    VStack(spacing: 0) {
                        if !appState.isConnected {
                            HStack(spacing: 6) {
                                ProgressView()
                                    .controlSize(.small)
                                Text("Reconnecting...")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 6)
                            .background(.ultraThinMaterial)
                        }
                        if tmuxSessions.count > 1 {
                            tmuxFilterBar
                        }
                        sessionList
                    }
                }
            }
            .navigationTitle("MClaude")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    HStack(spacing: 8) {
                        Button {
                            showNewSession = true
                            loadProjects()
                        } label: {
                            Image(systemName: "plus")
                        }
                        connectionIndicator
                    }
                }
            }
            .sheet(isPresented: $showNewSession) {
                newSessionSheet
            }
            .onAppear { viewModel.startListening(appState: appState) }
            .onDisappear { viewModel.stopListening(appState: appState) }
        }
    }

    private var tmuxSessions: [String] {
        let names = Set(appState.sessions.compactMap { $0.tmuxSession })
        return names.sorted()
    }

    private var defaultTmuxSession: String {
        let names = tmuxSessions
        if names.contains("mclaude") { return "mclaude" }
        return names.first ?? "mclaude"
    }

    private var filteredSessions: [ClaudeSession] {
        if tmuxFilter == "all" { return appState.sessions }
        let target = tmuxFilter ?? defaultTmuxSession
        return appState.sessions.filter { ($0.tmuxSession ?? defaultTmuxSession) == target }
    }

    private var tmuxFilterBar: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                ForEach(tmuxSessions, id: \.self) { name in
                    let isActive = tmuxFilter != "all" && (tmuxFilter ?? defaultTmuxSession) == name
                    Button {
                        tmuxFilter = name == defaultTmuxSession && tmuxFilter == nil ? nil : name
                    } label: {
                        Text(name)
                            .font(.subheadline)
                            .fontWeight(.medium)
                            .padding(.horizontal, 12)
                            .padding(.vertical, 6)
                            .background(isActive ? Color.accentColor : Color(.systemGray5))
                            .foregroundStyle(isActive ? .white : .secondary)
                            .clipShape(Capsule())
                    }
                    .buttonStyle(.plain)
                }
                Button {
                    tmuxFilter = "all"
                } label: {
                    Text("All")
                        .font(.subheadline)
                        .fontWeight(.medium)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 6)
                        .background(tmuxFilter == "all" ? Color.accentColor : Color(.systemGray5))
                        .foregroundStyle(tmuxFilter == "all" ? .white : .secondary)
                        .clipShape(Capsule())
                }
                .buttonStyle(.plain)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 8)
        }
    }

    private var sessionList: some View {
        List(filteredSessions) { session in
            NavigationLink(value: session.id) {
                SessionRowView(session: session)
            }
        }
        .navigationDestination(for: String.self) { sessionId in
            SessionDetailView(sessionId: sessionId)
        }
    }

    private var newSessionSheet: some View {
        NavigationStack {
            Group {
                if isLoadingProjects {
                    ProgressView("Loading projects...")
                } else if projects.isEmpty {
                    ContentUnavailableView(
                        "No Projects",
                        systemImage: "folder",
                        description: Text("No directories found in ~/work")
                    )
                } else {
                    List {
                        Section {
                            Toggle("tmux", isOn: $useTmux)
                        } header: {
                            Text("Runtime")
                        } footer: {
                            Text(useTmux ? "Runs on the host machine in tmux" : "Runs in a Kubernetes pod")
                        }

                        Section("Projects") {
                            ForEach(projects) { project in
                                Button {
                                    createSession(cwd: project.path)
                                } label: {
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(project.name)
                                            .font(.body)
                                            .fontWeight(.medium)
                                        Text(project.path)
                                            .font(.caption)
                                            .foregroundStyle(.secondary)
                                    }
                                }
                            }
                        }
                    }
                }
            }
            .navigationTitle("New Session")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { showNewSession = false }
                }
            }
        }
        .presentationDetents([.medium, .large])
    }

    private func loadProjects() {
        isLoadingProjects = true
        Task {
            do {
                projects = try await appState.client.fetchProjects()
            } catch {
                projects = []
            }
            isLoadingProjects = false
        }
    }

    private func createSession(cwd: String) {
        let runtime = useTmux ? "tmux" : "k8s"
        Task {
            try? await appState.client.createSession(cwd: cwd, runtime: runtime)
            showNewSession = false
        }
    }

    private var connectionIndicator: some View {
        HStack(spacing: 4) {
            Circle()
                .fill(appState.isConnected ? .green : .red)
                .frame(width: 8, height: 8)

            let attentionCount = appState.sessions.filter { $0.status.needsAttention }.count
            if attentionCount > 0 {
                Text("\(attentionCount)")
                    .font(.caption)
                    .fontWeight(.bold)
                    .foregroundStyle(.orange)
            }
        }
    }
}
