import SwiftUI

enum WatchRoute: Hashable {
    case session(String)
    case read
    case lastAnswer(String)
}

/// docs/plan-watch-app.md → Session list.
struct SessionListView: View {
    @Environment(WatchStore.self) private var store
    @Environment(\.scenePhase) private var scenePhase
    @State private var path: [WatchRoute] = []

    var body: some View {
        NavigationStack(path: $path) {
            content
                .navigationTitle("MClaude")
                .navigationDestination(for: WatchRoute.self) { route in
                    switch route {
                    case .session(let id): SessionDetailView(sessionId: id)
                    case .read: ReadListView()
                    case .lastAnswer(let id): LastAnswerView(sessionId: id)
                    }
                }
        }
        // Refresh on appear and every 15 seconds, only while on screen.
        .task(id: scenePhase) {
            guard scenePhase == .active else { return }
            while !Task.isCancelled {
                store.refresh()
                try? await Task.sleep(for: .seconds(15))
            }
        }
        // A tapped notification opens that session.
        .onChange(of: store.openRequest) { _, id in
            guard let id else { return }
            store.openRequest = nil
            path = [.session(id)]
        }
    }

    @ViewBuilder private var content: some View {
        if let snap = store.snapshot {
            if snap.error == .signedOut {
                message("Sign in to MClaude on your iPhone.")
            } else {
                TimelineView(.periodic(from: .now, by: 5)) { context in
                    List {
                        if snap.sessions.isEmpty {
                            Text("No sessions")
                                .foregroundStyle(WatchTheme.textMuted)
                                .listRowBackground(Color.clear)
                        } else {
                            Text(summary)
                                .font(.caption2)
                                .foregroundStyle(WatchTheme.textSecondary)
                                .listRowBackground(Color.clear)
                            section("Needs you", store.needsYou, now: context.date)
                            section("Working", store.working, now: context.date)
                            section("Unread", store.unread, now: context.date)
                            let readCount = store.read.count
                            if readCount > 0 {
                                NavigationLink(value: WatchRoute.read) {
                                    Text("Read · \(readCount)")
                                        .foregroundStyle(WatchTheme.textSecondary)
                                }
                            }
                        }
                        footer(snap)
                            .listRowBackground(Color.clear)
                    }
                }
            }
        } else if store.linkProblem == .phoneUnreachable {
            message("iPhone not reachable")
        } else {
            VStack(spacing: 8) {
                ProgressView()
                Text("Connecting to iPhone…").font(.footnote).foregroundStyle(WatchTheme.textMuted)
            }
        }
    }

    /// "1 needs you · 2 working · 3 unread", or "All caught up".
    private var summary: String {
        let parts = [(store.needsYou.count, "needs you"), (store.working.count, "working"), (store.unread.count, "unread")]
            .filter { $0.0 > 0 }
            .map { "\($0.0) \($0.1)" }
        return parts.isEmpty ? "All caught up" : parts.joined(separator: " · ")
    }

    @ViewBuilder
    private func section(_ title: String, _ sessions: [WatchSession], now: Date) -> some View {
        if !sessions.isEmpty {
            Section {
                ForEach(sessions) { session in
                    NavigationLink(value: WatchRoute.session(session.id)) {
                        SessionRow(session: session, unread: store.isUnread(session), now: now)
                    }
                }
            } header: {
                Text(title.uppercased())
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(WatchTheme.textMuted)
            }
        }
    }

    private func message(_ text: String) -> some View {
        Text(text)
            .font(.footnote)
            .multilineTextAlignment(.center)
            .foregroundStyle(WatchTheme.textSecondary)
            .padding()
    }

    private func footer(_ snap: WatchSnapshot) -> some View {
        let time = snap.fetchedAt.formatted(date: .omitted, time: .shortened)
        let text: String
        if store.linkProblem == .phoneUnreachable {
            text = "iPhone not reachable · updated \(time)"
        } else if snap.error == .server {
            text = "Can't reach the Mac · updated \(time)"
        } else {
            text = "Updated \(time)"
        }
        return Text(text)
            .font(.caption2)
            .foregroundStyle(WatchTheme.textMuted)
            .frame(maxWidth: .infinity, alignment: .center)
    }
}

/// docs/plan-watch-app.md → Read list.
struct ReadListView: View {
    @Environment(WatchStore.self) private var store

    var body: some View {
        TimelineView(.periodic(from: .now, by: 5)) { context in
            List {
                if store.read.isEmpty {
                    Text("Nothing here").foregroundStyle(WatchTheme.textMuted).listRowBackground(Color.clear)
                }
                ForEach(store.read) { session in
                    NavigationLink(value: WatchRoute.session(session.id)) {
                        SessionRow(session: session, unread: false, now: context.date)
                    }
                }
            }
        }
        .navigationTitle("Read")
    }
}

/// Two lines: dot + name, then the age of the last activity.
struct SessionRow: View {
    let session: WatchSession
    let unread: Bool
    let now: Date

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 6) {
                StatusDot(state: session.state, unread: unread)
                Text(session.name)
                    .font(.headline)
                    .foregroundStyle(WatchTheme.textPrimary)
                    .lineLimit(1)
            }
            Text(session.age(now: now))
                .font(.caption2)
                .foregroundStyle(WatchTheme.textMuted)
        }
        .padding(.vertical, 2)
        .accessibilityElement(children: .combine)
    }
}

struct StatusDot: View {
    let state: WatchSession.State
    var unread: Bool = false

    var body: some View {
        if let color = WatchTheme.dot(state, unread: unread) {
            Circle().fill(color).frame(width: 8, height: 8).accessibilityHidden(true)
        }
    }
}
