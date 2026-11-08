import SwiftUI

/// Claude-style session drawer (docs/design-claude-uplift.md → Drawer): New session, Resume
/// session, project chips, one list of open sessions (Active, then Recent; newest activity first)
/// with a pull-down search field, swipe right to end, and Settings at the bottom.
struct SessionDrawer: View {
    @Environment(AppState.self) private var appState
    let selectedSessionId: String?
    let onSelect: (String) -> Void
    let onNewSession: () -> Void
    let onResume: () -> Void
    let onSystem: () -> Void
    let onUsage: () -> Void
    let onSettings: () -> Void

    /// Saved project chip ("" = All).
    @AppStorage("drawerProjectFilter") private var savedProject = ""
    @State private var searchText = ""
    @State private var renaming: ClaudeSession?
    @State private var renameText = ""
    @State private var toast: EndedToast?
    @State private var errorMessage: String?

    private struct EndedToast: Equatable {
        let name: String
        let claudeSessionId: String
        let token = UUID()
    }

    // MARK: - Data

    private static func isActive(_ s: ClaudeSession) -> Bool {
        if s.processDown { return false }
        return switch s.status {
        case .working, .waitingForInput, .needsPermission, .planMode: true
        case .idle, .unknown: false
        }
    }

    /// Projects with an open session, most recent activity first.
    private var projects: [String] {
        projectsByRecency(appState.sessions.map { ($0.projectName, $0.lastActivity) })
    }

    /// The saved chip, unless that project has no open sessions left.
    private var projectFilter: String {
        projects.contains(savedProject) ? savedProject : ""
    }

    private var query: String { searchText.trimmingCharacters(in: .whitespaces) }

    private var visible: [ClaudeSession] {
        appState.sessions
            .filter { projectFilter.isEmpty || $0.projectName == projectFilter }
            .filter {
                query.isEmpty || $0.displayName.localizedCaseInsensitiveContains(query)
                    || $0.projectName.localizedCaseInsensitiveContains(query)
            }
            .sorted { $0.lastActivity > $1.lastActivity }
    }

    // MARK: - Body

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            brandRow

            drawerAction("New session", icon: "plus.bubble", tint: Theme.clay, semibold: true, action: onNewSession)
            drawerAction("Resume session", icon: "clock.arrow.circlepath", tint: Theme.textSecondary, semibold: false,
                         action: onResume)

            if projects.count > 1 {
                ProjectChips(projects: projects, selection: $savedProject)
                    .padding(.top, 8)
                    .padding(.bottom, 2)
            }

            sessionList
                .overlay(alignment: .bottom) { toastView }

            Rectangle().fill(Theme.border).frame(height: 0.5)

            systemRow
            usageRow
            settingsRow
        }
        .frame(maxHeight: .infinity, alignment: .top)
        .background(Theme.bgPage.ignoresSafeArea())
        .overlay(alignment: .trailing) {
            Rectangle().fill(Theme.border).frame(width: 0.5).ignoresSafeArea()
        }
        .alert("Rename session", isPresented: Binding(get: { renaming != nil }, set: { if !$0 { renaming = nil } })) {
            TextField("Name", text: $renameText)
            Button("Save") { saveRename() }
            Button("Cancel", role: .cancel) { renaming = nil }
        }
        .alert("Couldn't do that", isPresented: Binding(get: { errorMessage != nil }, set: { if !$0 { errorMessage = nil } })) {
            Button("OK", role: .cancel) { errorMessage = nil }
        } message: {
            Text(errorMessage ?? "")
        }
    }

    private var brandRow: some View {
        HStack(spacing: 10) {
            MClaudeMark(size: 22)
            Text("MClaude")
                .font(.system(size: 20, weight: .semibold, design: .serif))
                .foregroundStyle(Theme.textPrimary)
            Spacer()
            Circle()
                .fill(appState.isConnected ? Theme.success : Theme.danger)
                .frame(width: 7, height: 7)
                .accessibilityLabel(appState.isConnected ? "Connected" : "Disconnected")
        }
        .padding(.horizontal, 20)
        .padding(.top, 14)
        .padding(.bottom, 14)
    }

    private func drawerAction(_ title: String, icon: String, tint: Color, semibold: Bool,
                              action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 12) {
                Image(systemName: icon)
                    .font(.system(size: 16, weight: .regular))
                    .foregroundStyle(tint)
                    .frame(width: 22)
                Text(title)
                    .font(semibold ? .claudeUISemibold : .claudeUI)
                    .foregroundStyle(Theme.textPrimary)
                Spacer()
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 11)
            .contentShape(Rectangle())
        }
        .buttonStyle(DrawerRowStyle(selected: false))
        .padding(.horizontal, 8)
    }

    // MARK: - List

    private static let topId = "drawer-top"

    private var sessionList: some View {
        let rows = visible
        let active = rows.filter(Self.isActive)
        let recent = rows.filter { !Self.isActive($0) }
        return ScrollViewReader { proxy in
            List {
                DrawerSearchField(text: $searchText, prompt: "Search sessions")
                    .drawerListRow(EdgeInsets(top: 6, leading: 14, bottom: 4, trailing: 14))

                Color.clear.frame(height: 0).id(Self.topId)
                    .drawerListRow(EdgeInsets())

                if !active.isEmpty {
                    sectionHeader("Active")
                    ForEach(active) { sessionRow($0) }
                }
                if !recent.isEmpty {
                    sectionHeader("Recent")
                    ForEach(recent) { sessionRow($0) }
                }
                if rows.isEmpty {
                    Text(emptyText)
                        .font(.claudeCaption)
                        .foregroundStyle(Theme.textMuted)
                        .drawerListRow(EdgeInsets(top: 18, leading: 20, bottom: 0, trailing: 20))
                }
                Color.clear.frame(height: toast == nil ? 12 : 64)
                    .drawerListRow(EdgeInsets())
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .environment(\.defaultMinListRowHeight, 0)
            .scrollDismissesKeyboard(.immediately)
            .onAppear {
                // Tuck the search field just above the top; pulling down reveals it.
                DispatchQueue.main.async { proxy.scrollTo(Self.topId, anchor: .top) }
            }
        }
    }

    private var emptyText: String {
        if !appState.isConnected && appState.sessions.isEmpty { return "Not connected" }
        if appState.sessions.isEmpty { return "No sessions yet" }
        return "No matching sessions"
    }

    private func sectionHeader(_ title: String) -> some View {
        Text(title)
            .font(.claudeCaptionSemibold)
            .foregroundStyle(Theme.textMuted)
            .drawerListRow(EdgeInsets(top: 14, leading: 20, bottom: 4, trailing: 12))
    }

    private func sessionRow(_ session: ClaudeSession) -> some View {
        Button { onSelect(session.id) } label: {
            DrawerSessionRow(session: session)
        }
        .buttonStyle(DrawerRowStyle(selected: session.id == selectedSessionId))
        .drawerListRow(EdgeInsets(top: 1, leading: 8, bottom: 1, trailing: 8))
        .swipeActions(edge: .leading, allowsFullSwipe: true) {
            Button(role: .destructive) { end(session) } label: {
                Label("End", systemImage: "xmark.circle")
            }
            .tint(Theme.danger)
        }
        .contextMenu {
            Button {
                renameText = session.displayName
                renaming = session
            } label: {
                Label("Rename", systemImage: "pencil")
            }
            Button(role: .destructive) { end(session) } label: {
                Label("End", systemImage: "xmark.circle")
            }
        }
    }

    // MARK: - Toast

    @ViewBuilder
    private var toastView: some View {
        if let toast {
            HStack(spacing: 12) {
                Text("Ended")
                    .font(.claudeUISemibold)
                    .foregroundStyle(Theme.textPrimary)
                Text(toast.name)
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.textMuted)
                    .lineLimit(1)
                Spacer(minLength: 8)
                Button("Undo") { undo(toast) }
                    .font(.claudeUISemibold)
                    .foregroundStyle(Theme.clay)
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 11)
            .background(
                RoundedRectangle(cornerRadius: Theme.radiusCard, style: .continuous)
                    .fill(Theme.bgRaised)
                    .shadow(color: .black.opacity(0.08), radius: 8, y: 2)
            )
            .overlay(
                RoundedRectangle(cornerRadius: Theme.radiusCard, style: .continuous)
                    .stroke(Theme.border, lineWidth: 0.5)
            )
            .padding(.horizontal, 12)
            .padding(.bottom, 10)
            .transition(.move(edge: .bottom).combined(with: .opacity))
            .task(id: toast.token) {
                try? await Task.sleep(nanoseconds: 4_000_000_000)
                guard !Task.isCancelled, self.toast?.token == toast.token else { return }
                withAnimation(.snappy) { self.toast = nil }
            }
        }
    }

    // MARK: - Actions

    private func end(_ session: ClaudeSession) {
        let ended = EndedToast(name: session.displayName, claudeSessionId: session.sessionId)
        Task {
            do {
                try await appState.endSession(id: session.id)
                withAnimation(.snappy) { toast = ended }
            } catch {
                errorMessage = "The session couldn't be ended. \(error.localizedDescription)"
            }
        }
    }

    private func undo(_ ended: EndedToast) {
        withAnimation(.snappy) { toast = nil }
        Task {
            do {
                _ = try await appState.resumeTranscript(claudeSessionId: ended.claudeSessionId)
            } catch {
                errorMessage = "The session couldn't be resumed. \(error.localizedDescription)"
            }
        }
    }

    private func saveRename() {
        guard let session = renaming else { return }
        renaming = nil
        let name = renameText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, name != session.displayName else { return }
        Task {
            do {
                try await appState.renameSession(id: session.id, name: name)
            } catch {
                errorMessage = "The session couldn't be renamed. \(error.localizedDescription)"
            }
        }
    }

    /// System map (docs/plan-observability.md → Map screen), directly above Usage. The red dot is
    /// the last known Live poll: any node failing.
    private var systemRow: some View {
        footerRow("System", icon: "point.3.connected.trianglepath.dotted", action: onSystem,
                  alert: SystemStatus.shared.anyFailing)
            .padding(.top, 6)
            .accessibilityValue(SystemStatus.shared.anyFailing ? "Something is failing" : "")
    }

    private var usageRow: some View {
        footerRow("Usage", icon: "chart.bar", action: onUsage)
    }

    private func footerRow(_ title: String, icon: String, action: @escaping () -> Void,
                           alert: Bool = false) -> some View {
        Button(action: action) {
            HStack(spacing: 12) {
                Image(systemName: icon)
                    .font(.system(size: 16, weight: .regular))
                    .foregroundStyle(Theme.textSecondary)
                    .frame(width: 22)
                Text(title)
                    .font(.claudeUI)
                    .foregroundStyle(Theme.textPrimary)
                Spacer()
                if alert {
                    Circle().fill(Theme.danger).frame(width: 7, height: 7)
                }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .contentShape(Rectangle())
        }
        .buttonStyle(DrawerRowStyle(selected: false))
        .padding(.horizontal, 8)
    }

    private var settingsRow: some View {
        Button(action: onSettings) {
            HStack(spacing: 12) {
                Image(systemName: "gearshape")
                    .font(.system(size: 16, weight: .regular))
                    .foregroundStyle(Theme.textSecondary)
                    .frame(width: 22)
                VStack(alignment: .leading, spacing: 1) {
                    Text("Settings")
                        .font(.claudeUI)
                        .foregroundStyle(Theme.textPrimary)
                    if !appState.userName.isEmpty {
                        Text(appState.userName)
                            .font(.claudeFootnote)
                            .foregroundStyle(Theme.textMuted)
                    }
                }
                Spacer()
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .contentShape(Rectangle())
        }
        .buttonStyle(DrawerRowStyle(selected: false))
        .padding(.horizontal, 8)
        .padding(.bottom, 6)
    }
}

private struct DrawerSessionRow: View {
    let session: ClaudeSession

    var body: some View {
        HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                Text(session.displayName)
                    .font(.claudeUI)
                    .foregroundStyle(Theme.textPrimary)
                    .lineLimit(1)
                TimelineView(.periodic(from: .now, by: 30)) { context in
                    Text([relativeTimeLabel(session.lastActivity, now: context.date),
                          session.projectName,
                          session.statusWord].joined(separator: " · "))
                        .font(.claudeFootnote)
                        .foregroundStyle(Theme.textMuted)
                        .lineLimit(1)
                }
            }
            Spacer(minLength: 8)
            if !session.processDown, let dot = session.status.themeDot {
                Circle().fill(dot).frame(width: 8, height: 8)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
    }
}

// MARK: - Shared with the Resume sheet

/// Distinct project names ordered by their most recent date.
func projectsByRecency(_ items: [(project: String, date: Date)]) -> [String] {
    var newest: [String: Date] = [:]
    for item in items { newest[item.project] = max(newest[item.project] ?? .distantPast, item.date) }
    return newest.keys.sorted { (newest[$0]!, $1) > (newest[$1]!, $0) }
}

/// Capsule chips: All, then one per project. Tapping the selected chip goes back to All.
struct ProjectChips: View {
    let projects: [String]
    /// "" = All.
    @Binding var selection: String

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                chip("All", selected: selection.isEmpty || !projects.contains(selection)) { selection = "" }
                ForEach(projects, id: \.self) { project in
                    chip(project, selected: selection == project) {
                        selection = selection == project ? "" : project
                    }
                }
            }
            .padding(.horizontal, 16)
        }
    }

    private func chip(_ title: String, selected: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(title)
                .font(.claudeCaption)
                .foregroundStyle(selected ? Theme.textPrimary : Theme.textSecondary)
                .lineLimit(1)
                .padding(.horizontal, 12)
                .padding(.vertical, 6)
                .background(Capsule().fill(selected ? Theme.bgStrong : Theme.bgSunken))
                .overlay(Capsule().stroke(selected ? Theme.textMuted.opacity(0.5) : Color.clear, lineWidth: 0.5))
                .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

/// Sunken rounded search field with a magnifying glass and a clear button.
struct DrawerSearchField: View {
    @Binding var text: String
    let prompt: String

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 14))
                .foregroundStyle(Theme.textMuted)
            TextField(prompt, text: $text)
                .font(.claudeUI)
                .foregroundStyle(Theme.textPrimary)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .submitLabel(.search)
            if !text.isEmpty {
                Button { text = "" } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.system(size: 14))
                        .foregroundStyle(Theme.textMuted)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Clear search")
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .background(RoundedRectangle(cornerRadius: 10, style: .continuous).fill(Theme.bgSunken))
    }
}

extension View {
    /// A plain, transparent, separator-less List row with the given insets.
    func drawerListRow(_ insets: EdgeInsets) -> some View {
        listRowInsets(insets)
            .listRowBackground(Color.clear)
            .listRowSeparator(.hidden)
    }
}

/// Compact relative time: "now", "5m ago", "2h ago", "3d ago". Shared with NewSessionSheet.
func relativeTimeLabel(_ date: Date, now: Date) -> String {
    let s = max(0, Int(now.timeIntervalSince(date)))
    if s < 60 { return "now" }
    if s < 3600 { return "\(s / 60)m ago" }
    if s < 86400 { return "\(s / 3600)h ago" }
    return "\(s / 86400)d ago"
}

/// Rounded row highlight: sunken when selected, strong while pressed.
struct DrawerRowStyle: ButtonStyle {
    let selected: Bool
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .background(
                RoundedRectangle(cornerRadius: 10, style: .continuous)
                    .fill(configuration.isPressed ? Theme.bgStrong : (selected ? Theme.bgSunken : Color.clear))
            )
    }
}
