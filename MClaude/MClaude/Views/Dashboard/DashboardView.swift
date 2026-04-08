import SwiftUI

struct DashboardView: View {
    @Environment(AppState.self) private var appState
    @State private var viewModel = DashboardViewModel()
    @State private var showNewSession = false
    @State private var projects: [ProjectInfo] = []
    @State private var isLoadingProjects = false

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

    private var sessionList: some View {
        List(appState.sessions) { session in
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
                    List(projects) { project in
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
        Task {
            try? await appState.client.createSession(cwd: cwd)
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
