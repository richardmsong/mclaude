import Foundation
import Observation
import UserNotifications
import WatchConnectivity

/// The watch's view of MClaude: the latest snapshot relayed by the iPhone app, refreshed every
/// 15 seconds while the app is on screen, plus read state and actions that go back through the
/// iPhone. docs/plan-watch-app.md → Relay, Watch app.
@MainActor @Observable
final class WatchStore: NSObject {
    enum LinkProblem { case phoneUnreachable }

    private(set) var snapshot: WatchSnapshot?
    /// Set when the latest refresh didn't get a reply from the iPhone app.
    private(set) var linkProblem: LinkProblem?
    /// Sessions marked read on the watch, so they read as read before a newer snapshot confirms.
    private(set) var localSeen: [String: Date] = [:]
    /// A tapped notification asks the list to open this session.
    var openRequest: String?

    private static let savedKey = "lastSnapshot"
    private static let seenKey = "localSeen"
    private var activated = false
    private var inFlight = false
    private var currentAttempt: UUID?

    override init() {
        super.init()
        if let data = UserDefaults.standard.data(forKey: Self.savedKey) {
            snapshot = WatchSnapshot.decode(data)
        }
        if let saved = UserDefaults.standard.dictionary(forKey: Self.seenKey) as? [String: Double] {
            localSeen = saved.mapValues { Date(timeIntervalSince1970: $0) }
        }
        UNUserNotificationCenter.current().delegate = self
        if WCSession.isSupported() {
            WCSession.default.delegate = self
            WCSession.default.activate()
        }
    }

    // MARK: - List (docs/plan-watch-app.md → Session list)

    /// In the snapshot's order: newest last input first, Ops last (the iPhone sorts it).
    var sessions: [WatchSession] { snapshot?.sessions ?? [] }

    /// The session's dot, counting a read made on the watch after the snapshot was fetched.
    func dot(_ s: WatchSession) -> SessionDot? {
        let readLocally = localSeen[s.id].map { $0 > (snapshot?.fetchedAt ?? .distantPast) } ?? false
        return s.dot(readLocally: readLocally)
    }

    /// Sessions with a dot (needs you, working, background, unread); status never reorders them.
    var listed: [WatchSession] { sessions.filter { dot($0) != nil } }
    var read: [WatchSession] { sessions.filter { dot($0) == nil } }

    var needsYouCount: Int { sessions.filter { dot($0) == .needsYou }.count }
    var workingCount: Int { sessions.filter { dot($0) == .working || dot($0) == .background }.count }
    var unreadCount: Int { sessions.filter { dot($0) == .unread }.count }

    func session(_ id: String) -> WatchSession? { sessions.first { $0.id == id } }

    // MARK: - Refresh

    /// Ask the iPhone app for a fresh snapshot (no-op while one is in flight).
    func refresh() {
        guard activated, !inFlight else { return }
        let session = WCSession.default
        guard session.isReachable else { linkProblem = .phoneUnreachable; return }
        inFlight = true
        let attempt = UUID()
        currentAttempt = attempt
        session.sendMessage([WatchSnapshot.requestKey: WatchSnapshot.requestRefresh], replyHandler: { reply in
            let data = reply[WatchSnapshot.messageKey] as? Data
            Task { @MainActor in self.finish(attempt, data: data) }
        }, errorHandler: { _ in
            Task { @MainActor in self.finish(attempt, data: nil) }
        })
        // No reply within 10 seconds: show "iPhone not reachable" (a late reply still applies).
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(10))
            if self.currentAttempt == attempt, self.inFlight {
                self.inFlight = false
                self.linkProblem = .phoneUnreachable
            }
        }
    }

    private func finish(_ attempt: UUID, data: Data?) {
        if currentAttempt == attempt { inFlight = false }
        guard let data, let snap = WatchSnapshot.decode(data) else {
            if currentAttempt == attempt { linkProblem = .phoneUnreachable }
            return
        }
        apply(snap, data: data)
        linkProblem = nil
    }

    private func apply(_ snap: WatchSnapshot, data: Data) {
        // Never let an older snapshot (e.g. a late application context) replace a newer one.
        if let current = snapshot, current.fetchedAt > snap.fetchedAt { return }
        snapshot = snap
        UserDefaults.standard.set(data, forKey: Self.savedKey)
    }

    // MARK: - Read state and actions

    /// Opening a session on the watch marks it read: here at once, and on the server via the iPhone.
    func markRead(_ id: String) {
        let now = Date()
        localSeen[id] = now
        let ids = Set(sessions.map(\.id))
        if !ids.isEmpty { localSeen = localSeen.filter { ids.contains($0.key) } }
        UserDefaults.standard.set(localSeen.mapValues(\.timeIntervalSince1970), forKey: Self.seenKey)
        guard activated, WCSession.default.isReachable else { return }
        WCSession.default.sendMessage([WatchSnapshot.requestKey: WatchSnapshot.requestMarkRead, "id": id],
                                      replyHandler: { reply in
            guard let data = reply[WatchSnapshot.messageKey] as? Data, let snap = WatchSnapshot.decode(data) else { return }
            Task { @MainActor in self.apply(snap, data: data) }
        }, errorHandler: nil)
    }

    /// Send an action (answer, approve, deny, continue, reply) through the iPhone app. True on success.
    func perform(_ action: String, on id: String, extra: [String: Any] = [:]) async -> Bool {
        guard activated, WCSession.default.isReachable else { linkProblem = .phoneUnreachable; return false }
        var message = extra
        message[WatchSnapshot.requestKey] = WatchSnapshot.requestAction
        message["id"] = id
        message["action"] = action
        // Give up after 20 seconds (the iPhone may be slow to wake and call the server).
        guard let reply = await request(message, timeout: 20) else { return false }
        if let data = reply[WatchSnapshot.messageKey] as? Data, let snap = WatchSnapshot.decode(data) {
            apply(snap, data: data)
            linkProblem = nil
        }
        localSeen[id] = Date()
        return reply["ok"] as? Bool == true
    }

    /// Send one request to the iPhone app and wait for its reply (nil: unreachable, error or timeout).
    private func request(_ message: [String: Any], timeout: TimeInterval) async -> [String: Any]? {
        guard activated, WCSession.default.isReachable else { return nil }
        return await withCheckedContinuation { continuation in
            let once = Once(continuation)
            WCSession.default.sendMessage(message, replyHandler: { once.resume($0) },
                                          errorHandler: { _ in once.resume(nil) })
            DispatchQueue.main.asyncAfter(deadline: .now() + timeout) { once.resume(nil) }
        }
    }

    enum FullMessage { case text(String), none, failed }

    /// Claude's last answer in full (docs/plan-watch-app.md → Last answer).
    func fullMessage(_ id: String) async -> FullMessage {
        guard let reply = await request([WatchSnapshot.requestKey: WatchSnapshot.requestMessage, "id": id],
                                        timeout: 20) else { return .failed }
        if let text = reply["text"] as? String { return .text(text) }
        return reply["error"] == nil ? .none : .failed
    }

    fileprivate func activationDone() {
        activated = true
        if let data = WCSession.default.receivedApplicationContext[WatchSnapshot.messageKey] as? Data,
           let snap = WatchSnapshot.decode(data) {
            apply(snap, data: data)
        }
        refresh()
    }

    fileprivate func received(context data: Data) {
        if let snap = WatchSnapshot.decode(data) { apply(snap, data: data) }
    }
}

/// Resumes a continuation at most once (reply, error and timeout race).
private final class Once: @unchecked Sendable {
    private var continuation: CheckedContinuation<[String: Any]?, Never>?
    private let lock = NSLock()
    init(_ c: CheckedContinuation<[String: Any]?, Never>) { continuation = c }
    func resume(_ value: [String: Any]?) {
        let c: CheckedContinuation<[String: Any]?, Never>? = lock.withLock {
            defer { continuation = nil }
            return continuation
        }
        c?.resume(returning: value)
    }
}

extension WatchStore: WCSessionDelegate {
    nonisolated func session(_ session: WCSession, activationDidCompleteWith state: WCSessionActivationState,
                             error: Error?) {
        Task { @MainActor in self.activationDone() }
    }

    nonisolated func session(_ session: WCSession, didReceiveApplicationContext context: [String: Any]) {
        guard let data = context[WatchSnapshot.messageKey] as? Data else { return }
        Task { @MainActor in self.received(context: data) }
    }

    nonisolated func sessionReachabilityDidChange(_ session: WCSession) {
        Task { @MainActor in if session.isReachable { self.refresh() } }
    }

    #if os(iOS)
    // Only reached when /deploy-ios's simulator build compiles this target with the iPhone SDK.
    nonisolated func sessionDidBecomeInactive(_ session: WCSession) {}
    nonisolated func sessionDidDeactivate(_ session: WCSession) {}
    #endif
}

extension WatchStore: UNUserNotificationCenterDelegate {
    // Tapping a MClaude notification opens that session (docs/plan-watch-app.md → Notifications).
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                            withCompletionHandler completionHandler: @escaping () -> Void) {
        let id = response.notification.request.content.userInfo["sessionId"] as? String
        Task { @MainActor in
            if let id, !id.isEmpty { self.openRequest = id }
            self.refresh()
        }
        completionHandler()
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                            withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        Task { @MainActor in self.refresh() }
        completionHandler([.banner, .sound, .list])
    }
}
