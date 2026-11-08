import SwiftUI

/// Project picker for a new session: Recent (last 30 days), git Projects, collapsed Other
/// (non-git folders), and name search across everything.
struct NewSessionSheet: View {
    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss
    /// Called with the new session id (nil if the server didn't return one).
    let onCreated: (String?) -> Void

    @State private var projects: [ProjectInfo] = []
    @State private var isLoading = true
    @State private var searchText = ""
    @State private var showOther = false

    private static let recentLimit = 5
    private static let recentWindow: TimeInterval = 30 * 86400

    private var query: String { searchText.trimmingCharacters(in: .whitespaces) }

    private func alphabetical(_ list: [ProjectInfo]) -> [ProjectInfo] {
        list.sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
    }

    /// Up to 5 projects used in the last 30 days, newest first.
    private var recent: [ProjectInfo] {
        let cutoff = Date().addingTimeInterval(-Self.recentWindow)
        let used = projects.filter { ($0.lastUsed ?? .distantPast) >= cutoff }
        return Array(used.sorted { ($0.lastUsed ?? .distantPast) > ($1.lastUsed ?? .distantPast) }
            .prefix(Self.recentLimit))
    }

    /// Git repos not in Recent. `isGit == nil` (older server) counts as a project.
    private var gitProjects: [ProjectInfo] {
        let recentIds = Set(recent.map(\.id))
        return alphabetical(projects.filter { $0.isGit != false && !recentIds.contains($0.id) })
    }

    /// Non-git folders not in Recent.
    private var otherFolders: [ProjectInfo] {
        let recentIds = Set(recent.map(\.id))
        return alphabetical(projects.filter { $0.isGit == false && !recentIds.contains($0.id) })
    }

    private var searchResults: [ProjectInfo] {
        alphabetical(projects.filter { $0.name.localizedCaseInsensitiveContains(query) })
    }

    var body: some View {
        NavigationStack {
            Group {
                if isLoading {
                    ProgressView("Loading projects…")
                        .tint(Theme.textMuted)
                        .foregroundStyle(Theme.textMuted)
                } else if projects.isEmpty {
                    VStack(spacing: 10) {
                        Image(systemName: "folder")
                            .font(.system(size: 28, weight: .light))
                            .foregroundStyle(Theme.textMuted)
                        Text("No projects")
                            .font(.system(size: 20, design: .serif))
                            .foregroundStyle(Theme.textPrimary)
                        Text("No directories found in ~/work")
                            .font(.claudeCaption)
                            .foregroundStyle(Theme.textMuted)
                    }
                } else {
                    List {
                        if !query.isEmpty {
                            searchSection
                        } else {
                            sections
                        }
                    }
                    .scrollContentBackground(.hidden)
                    .searchable(text: $searchText,
                                placement: .navigationBarDrawer(displayMode: .always),
                                prompt: "Search projects")
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Theme.bgPage)
            .navigationTitle("New Session")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                        .foregroundStyle(Theme.textSecondary)
                }
            }
        }
        .tint(Theme.clay)
        .presentationDetents([.medium, .large])
        .presentationBackground(Theme.bgPage)
        .task { await load() }
    }

    // MARK: - Sections

    @ViewBuilder
    private var searchSection: some View {
        let results = searchResults
        Section {
            if results.isEmpty {
                Text("No projects match \u{201C}\(query)\u{201D}")
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.textMuted)
                    .listRowBackground(Theme.bgRaised)
            } else {
                ForEach(results) { project in
                    projectRow(project, showTime: false)
                }
            }
        } header: {
            sectionHeader("Results")
        }
    }

    @ViewBuilder
    private var sections: some View {
        let recent = recent
        let gitProjects = gitProjects
        let otherFolders = otherFolders

        if !recent.isEmpty {
            Section {
                ForEach(recent) { project in
                    projectRow(project, showTime: true)
                }
            } header: {
                sectionHeader("Recent")
            }
        }

        if !gitProjects.isEmpty {
            Section {
                ForEach(gitProjects) { project in
                    projectRow(project, showTime: false)
                }
            } header: {
                sectionHeader("Projects")
            }
        }

        if !otherFolders.isEmpty {
            Section {
                DisclosureGroup(isExpanded: $showOther) {
                    ForEach(otherFolders) { project in
                        projectRow(project, showTime: false)
                    }
                } label: {
                    HStack(spacing: 6) {
                        Text("Other folders")
                            .font(.claudeUI)
                            .foregroundStyle(Theme.textSecondary)
                        Text("\(otherFolders.count)")
                            .font(.claudeFootnote)
                            .foregroundStyle(Theme.textMuted)
                    }
                }
                .tint(Theme.textMuted)
                .listRowBackground(Theme.bgRaised)
            } header: {
                sectionHeader("Other")
            }
        }
    }

    private func sectionHeader(_ title: String) -> some View {
        Text(title)
            .font(.claudeCaptionSemibold)
            .foregroundStyle(Theme.textMuted)
    }

    private func projectRow(_ project: ProjectInfo, showTime: Bool) -> some View {
        Button {
            create(cwd: project.path)
        } label: {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(project.name)
                        .font(.claudeUISemibold)
                        .foregroundStyle(Theme.textPrimary)
                    Text(project.path)
                        .font(.claudeMono(11))
                        .foregroundStyle(Theme.textMuted)
                }
                Spacer(minLength: 8)
                if showTime, let lastUsed = project.lastUsed {
                    Text(relativeTimeLabel(lastUsed, now: Date()))
                        .font(.claudeFootnote)
                        .foregroundStyle(Theme.textMuted)
                }
            }
            .padding(.vertical, 2)
            .contentShape(Rectangle())
        }
        .listRowBackground(Theme.bgRaised)
    }

    // MARK: - Actions

    private func load() async {
        isLoading = true
        projects = (try? await appState.client.fetchProjects()) ?? []
        isLoading = false
    }

    private func create(cwd: String) {
        Task {
            let id = try? await appState.client.createSession(cwd: cwd)
            onCreated(id ?? nil)
            dismiss()
        }
    }
}
