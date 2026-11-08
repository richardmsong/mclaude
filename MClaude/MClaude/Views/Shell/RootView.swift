import SwiftUI

/// App shell: one conversation at a time with a Claude-style slide-over session drawer
/// (no tab bar). Opens the most recent session on launch; empty state when there are none.
struct RootView: View {
    @Environment(AppState.self) private var appState
    @Environment(\.colorScheme) private var scheme
    @State private var selectedSessionId: String?
    @State private var drawerOpen = false
    @State private var drawerDrag: CGFloat = 0
    @State private var showNewSession = false
    @State private var showResume = false
    @State private var showSystem = false
    @State private var showUsage = false
    @State private var showSettings = false
    @State private var didAutoSelect = false
    @State private var isCreatingInProject = false

    private var selectedSession: ClaudeSession? {
        guard let id = selectedSessionId else { return nil }
        return appState.sessions.first { $0.id == id }
    }

    var body: some View {
        GeometryReader { geo in
            let drawerWidth = min(330, geo.size.width * 0.84)
            ZStack(alignment: .leading) {
                NavigationStack {
                    content
                }
                .tint(Theme.clay)
                // A rightward swipe anywhere opens the drawer (like the Claude app): mostly
                // horizontal, ≥70pt (or a quick flick). Simultaneous, so vertical scrolling is
                // untouched. Ignored when it starts on selected text (a selection-handle drag) or
                // on horizontally scrollable content (a table / code block scroll).
                .simultaneousGesture(
                    DragGesture(minimumDistance: 20, coordinateSpace: .global)
                        .onEnded { value in
                            TranscriptProbe.shared.note(.drawer, [   // telemetry
                                "event": "page_drag_end", "drawer_open": drawerOpen,
                                "dx": Double(value.translation.width), "dy": Double(value.translation.height),
                                "start_x": Double(value.startLocation.x), "start_y": Double(value.startLocation.y)])
                            guard !drawerOpen,
                                  !TextSelectionState.shared.selection(contains: value.startLocation),
                                  !Self.isOnHorizontalScroller(value.startLocation) else { return }
                            let dx = value.translation.width, dy = abs(value.translation.height)
                            let flick = value.predictedEndTranslation.width > 220 && dx > 30
                            if (dx > 70 || flick), dy < dx * 0.6 {
                                TextSelectionState.shared.clearAll()
                                setDrawer(true)
                            }
                        }
                )

                // Left-edge swipe opens the drawer
                if !drawerOpen {
                    Color.clear
                        .frame(width: 18)
                        .contentShape(Rectangle())
                        .gesture(
                            DragGesture(minimumDistance: 12)
                                .onEnded { value in
                                    if value.translation.width > 50 { setDrawer(true) }
                                }
                        )
                        .ignoresSafeArea()
                }

                if drawerOpen {
                    Color.black.opacity(scheme == .dark ? 0.45 : 0.22)
                        .ignoresSafeArea()
                        .onTapGesture { setDrawer(false) }
                        .accessibilityLabel("Close menu")
                        .accessibilityAddTraits(.isButton)
                        .transition(.opacity)

                    SessionDrawer(
                        selectedSessionId: selectedSessionId,
                        onSelect: { id in
                            selectedSessionId = id
                            setDrawer(false)
                        },
                        onNewSession: {
                            setDrawer(false)
                            showNewSession = true
                        },
                        onResume: {
                            setDrawer(false)
                            showResume = true
                        },
                        onSystem: {
                            setDrawer(false)
                            showSystem = true
                        },
                        onUsage: {
                            setDrawer(false)
                            showUsage = true
                        },
                        onSettings: {
                            setDrawer(false)
                            showSettings = true
                        }
                    )
                    .frame(width: drawerWidth)
                    .offset(x: min(0, drawerDrag))
                    .gesture(
                        DragGesture(minimumDistance: 12)
                            .onChanged { drawerDrag = min(0, $0.translation.width) }
                            .onEnded { value in
                                if value.translation.width < -60 { setDrawer(false) }
                                withAnimation(.snappy) { drawerDrag = 0 }
                            }
                    )
                    .transition(.move(edge: .leading))
                    .zIndex(1)
                }
            }
        }
        .background(Theme.bgPage.ignoresSafeArea())
        .sheet(isPresented: $showNewSession) {
            NewSessionSheet { newId in
                if let newId { selectedSessionId = newId }
            }
        }
        .sheet(isPresented: $showResume) {
            ResumeSessionSheet { id in selectedSessionId = id }
        }
        .fullScreenCover(isPresented: $showSystem) {
            SystemMapView()
        }
        .sheet(isPresented: $showUsage) {
            UsageView()
        }
        .sheet(isPresented: $showSettings) {
            SettingsView()
        }
        .onAppear { appState.connectWebSocket() }
        .onChange(of: appState.openSessionRequest) { _, requested in
            // A push notification was tapped: show that session
            guard let requested else { return }
            selectedSessionId = requested
            didAutoSelect = true
            setDrawer(false)
            appState.openSessionRequest = nil
        }
        .onChange(of: appState.sessions.map(\.id)) { _, ids in
            // Launch: open the most recent session once sessions arrive.
            if !didAutoSelect, !ids.isEmpty {
                didAutoSelect = true
                if selectedSessionId == nil { selectedSessionId = mostRecentSessionId }
            }
            // Selected session went away (ended): fall back to the most recent one.
            if let id = selectedSessionId, !ids.isEmpty, !ids.contains(id) {
                selectedSessionId = mostRecentSessionId
            }
        }
        .task {
            if selectedSessionId == nil, !appState.sessions.isEmpty {
                didAutoSelect = true
                selectedSessionId = mostRecentSessionId
            }
        }
    }

    @ViewBuilder
    private var content: some View {
        if let id = selectedSessionId, selectedSession != nil || !appState.sessions.isEmpty {
            SessionDetailView(
                sessionId: id,
                onOpenDrawer: { setDrawer(true) },
                onNewSession: { showNewSession = true },
                onNewSessionInProject: { cwd in createSession(in: cwd) }
            )
            .id(id)  // fresh per-session state (draft, pending messages), as with the old push navigation
        } else {
            EmptySessionsView(
                onOpenDrawer: { setDrawer(true) },
                onNewSession: { showNewSession = true }
            )
        }
    }

    private var mostRecentSessionId: String? {
        appState.sessions.max { $0.lastActivity < $1.lastActivity }?.id
    }

    /// Whether `point` (window coordinates) is on a scroll view whose content is wider than its
    /// frame, so a horizontal swipe there scrolls it rather than opening the drawer.
    private static func isOnHorizontalScroller(_ point: CGPoint) -> Bool {
        let window = UIApplication.shared.connectedScenes
            .compactMap { ($0 as? UIWindowScene)?.keyWindow }.first
        var view = window?.hitTest(point, with: nil)
        while let v = view {
            if let sv = v as? UIScrollView, sv.contentSize.width > sv.bounds.width + 1 { return true }
            view = v.superview
        }
        return false
    }

    private func setDrawer(_ open: Bool) {
        TranscriptProbe.shared.note(.drawer, ["event": open ? "open" : "close"])   // telemetry
        withAnimation(.snappy(duration: 0.28)) { drawerOpen = open }
    }

    /// Long-press shortcut on the header new-session button: creates a session in the given cwd
    /// immediately, without showing the project picker sheet. Concurrent calls are no-ops
    /// (`isCreatingInProject` guard). Selects the new session once the server confirms it.
    private func createSession(in cwd: String) {
        guard !isCreatingInProject else { return }
        isCreatingInProject = true
        Task {
            let result = try? await appState.client.createSession(cwd: cwd)
            isCreatingInProject = false
            if let newId = result { selectedSessionId = newId }
        }
    }
}

// MARK: - Shared header buttons

/// ☰ button used in the header of every root screen.
struct DrawerButton: View {
    let action: () -> Void
    var body: some View {
        Button(action: action) {
            Image(systemName: "line.3.horizontal")
                .font(.system(size: 17, weight: .regular))
                .foregroundStyle(Theme.textSecondary)
        }
        .accessibilityLabel("Menu")
    }
}

/// Header "new session" button.
/// - Tap: opens the project picker sheet via `action()`.
/// - Long-press (~0.5 s) when a current project is known: calls `onNewInProject(cwd)`
///   immediately (no menu, no sheet) and fires a light haptic. The two gestures are
///   exclusive in SwiftUI — when the long press succeeds, the tap does not fire.
/// - When no current project is known: behaves as a plain button; long-press is a no-op.
struct NewSessionButton: View {
    let action: () -> Void
    /// cwd of the current session, if any.
    var currentProject: String? = nil
    var onNewInProject: ((String) -> Void)? = nil

    private var icon: some View {
        Image(systemName: "plus.bubble")
            .font(.system(size: 17, weight: .regular))
            .foregroundStyle(Theme.textSecondary)
    }

    var body: some View {
        if let cwd = currentProject, let onNewInProject {
            let projectName = (cwd as NSString).lastPathComponent
            // Non-Button view so gestures behave reliably inside a toolbar.
            icon
                .contentShape(Rectangle())
                .onTapGesture {
                    action()
                }
                .onLongPressGesture(minimumDuration: 0.5) {
                    UIImpactFeedbackGenerator(style: .light).impactOccurred()
                    onNewInProject(cwd)
                }
                .accessibilityAddTraits(.isButton)
                .accessibilityLabel("New session")
                .accessibilityAction(named: "New session in \(projectName)") {
                    onNewInProject(cwd)
                }
        } else {
            Button(action: action) { icon }
                .accessibilityLabel("New session")
        }
    }
}

// MARK: - Empty state

struct EmptySessionsView: View {
    @Environment(AppState.self) private var appState
    let onOpenDrawer: () -> Void
    let onNewSession: () -> Void

    private var disconnected: Bool { !appState.isConnected && appState.sessions.isEmpty }

    var body: some View {
        VStack(spacing: 18) {
            Spacer()
            MClaudeMark(size: 44)
            Text(disconnected ? "Can't reach your server" : "What should we work on?")
                .font(.system(size: 26, weight: .regular, design: .serif))
                .foregroundStyle(Theme.textPrimary)
                .multilineTextAlignment(.center)
            if disconnected {
                Text(appState.lastDisconnectReason ?? "mclaude-server at \(appState.serverHost):\(appState.serverPort) isn't responding.")
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.textMuted)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 40)
            } else {
                Button("New session", action: onNewSession)
                    .buttonStyle(ClayCapsuleButtonStyle())
            }
            Spacer()
            Spacer()
        }
        .frame(maxWidth: .infinity)
        .background(Theme.bgPage)
        .toolbar {
            ToolbarItem(placement: .topBarLeading) { DrawerButton(action: onOpenDrawer) }
            ToolbarItem(placement: .topBarTrailing) { NewSessionButton(action: onNewSession) }
        }
        .toolbarBackground(.hidden, for: .navigationBar)
        .navigationBarTitleDisplayMode(.inline)
    }
}
