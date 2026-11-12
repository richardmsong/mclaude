import Foundation
import UIKit
import WatchConnectivity
import os

private let liveLog = Logger(subsystem: "com.rsong.mclaude.app", category: "watch-live")

/// Answers the watch app's refresh requests: fetches sessions (and each one's last assistant
/// message) from the server and replies with a snapshot. docs/plan-watch-app.md → Data path.
/// The watch never talks to the network or holds the token; this runs even when the iPhone app
/// was woken in the background, so it reads the saved server settings itself.
final class WatchRelay: NSObject, WCSessionDelegate, @unchecked Sendable {
    static let shared = WatchRelay()

    private let lock = NSLock()
    /// Last message per session, with the activity time and state it was read at.
    private var lastTextCache: [String: (since: Date, state: WatchSession.State, text: String?)] = [:]
    /// Last snapshot whose session list was fetched successfully (served on server errors).
    private var lastGood: [WatchSession] = []

    func activate() {
        guard WCSession.isSupported() else { return }
        WCSession.default.delegate = self
        WCSession.default.activate()
    }

    // MARK: - WCSessionDelegate

    func session(_ session: WCSession, activationDidCompleteWith state: WCSessionActivationState, error: Error?) {}
    func sessionDidBecomeInactive(_ session: WCSession) {}
    func sessionDidDeactivate(_ session: WCSession) { WCSession.default.activate() }

    func session(_ session: WCSession, didReceiveMessage message: [String: Any],
                 replyHandler: @escaping ([String: Any]) -> Void) {
        let type = message[WatchSnapshot.requestKey] as? String
        if type == WatchSnapshot.requestMessage { handleFullMessage(message, replyHandler: replyHandler); return }
        if type == WatchSnapshot.requestLive { handleLive(message, replyHandler: replyHandler); return }
        if type == WatchSnapshot.requestTurns { handleTurns(message, replyHandler: replyHandler); return }
        guard type == WatchSnapshot.requestRefresh || type == WatchSnapshot.requestAction
                || type == WatchSnapshot.requestMarkRead else {
            replyHandler([:]); return
        }
        // Keep running if iOS woke us in the background just for this request.
        let task = UIApplication.shared.beginBackgroundTask(withName: "watch-relay")
        Task {
            var reply: [String: Any] = [:]
            if type == WatchSnapshot.requestAction { reply["ok"] = await perform(message) }
            if type == WatchSnapshot.requestMarkRead { reply["ok"] = await markRead(message["id"] as? String) }
            let snapshot = await buildSnapshot()
            let data = snapshot.encoded() ?? Data()
            reply[WatchSnapshot.messageKey] = data
            replyHandler(reply)
            // The watch shows this "last known state" the next time it opens.
            try? WCSession.default.updateApplicationContext([WatchSnapshot.messageKey: data])
            if task != .invalid { UIApplication.shared.endBackgroundTask(task) }
        }
    }

    // MARK: - Live (docs/plan-watch-app.md → Relay → Live)

    /// Sessions the watch is watching, until when (renewed by each `live` request).
    private var liveSubscriptions: [String: Date] = [:]
    /// Our own WebSocket while anything is subscribed: it pushes `live` frames to the watch.
    private var liveSocket: WSClient?
    private var liveTask: UIBackgroundTaskIdentifier = .invalid
    private var liveSweep: Task<Void, Never>?
    static let liveSubscription: TimeInterval = 10

    private func handleLive(_ message: [String: Any], replyHandler: @escaping ([String: Any]) -> Void) {
        guard let config = serverConfig() else { replyHandler(["error": WatchSnapshot.Failure.signedOut.rawValue]); return }
        guard let id = message["id"] as? String else { replyHandler(["error": "server"]); return }
        let client = APIClient(config: config)
        subscribe(id, config: config)
        Task {
            if let data = try? await client.fetchLive(id: id) { replyHandler(["live": data]) }
            else { replyHandler(["error": "server"]) }
        }
    }

    /// Subscribe the watch to `id` for 10 s, opening the WebSocket and keeping the app running.
    private func subscribe(_ id: String, config: ServerConfig) {
        let needsSocket: Bool = lock.withLock {
            liveSubscriptions[id] = Date().addingTimeInterval(Self.liveSubscription)
            return liveSocket == nil
        }
        DispatchQueue.main.async { [self] in
            // One background task while watching; iOS may still suspend us (the watch then polls).
            if liveTask == .invalid {
                liveTask = UIApplication.shared.beginBackgroundTask(withName: "watch-live") { [weak self] in
                    self?.endLiveTask()
                }
            }
        }
        if needsSocket {
            let socket = WSClient(config: config, onSessions: { _, _ in }, onOutput: { _, _ in },
                                  onFullOutput: { _, _ in },
                                  onConnectionChange: { up, reason in
                                      liveLog.info("live socket \(up ? "connected" : "down", privacy: .public) \(reason ?? "", privacy: .public)")
                                  },
                                  onLive: { [weak self] data in self?.forwardLive(data) })
            lock.withLock { liveSocket = socket }
            socket.connect()
        }
        startSweep()
    }

    /// Push a frame to the watch the moment it arrives, if the watch is watching that session.
    private func forwardLive(_ data: Data) {
        guard let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let id = json["id"] as? String else { return }
        let watching = lock.withLock { (liveSubscriptions[id] ?? .distantPast) > Date() }
        guard watching, WCSession.default.isReachable else {
            liveLog.info("live frame for \(id, privacy: .public) not forwarded: watching=\(watching) reachable=\(WCSession.default.isReachable)")
            return
        }
        WCSession.default.sendMessage([WatchSnapshot.requestKey: WatchSnapshot.pushLiveFrame, "frame": data],
                                      replyHandler: nil, errorHandler: nil)
    }

    /// Every 2 s: drop lapsed subscriptions; with none left, close the socket and end the task.
    private func startSweep() {
        let running = lock.withLock { liveSweep != nil }
        guard !running else { return }
        let sweep = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(2))
                guard let self else { return }
                let socket: WSClient? = self.lock.withLock {
                    self.liveSubscriptions = self.liveSubscriptions.filter { $0.value > Date() }
                    guard self.liveSubscriptions.isEmpty else { return nil }
                    let s = self.liveSocket
                    self.liveSocket = nil
                    self.liveSweep = nil
                    return s
                }
                if let socket {
                    socket.disconnect()
                    DispatchQueue.main.async { self.endLiveTask() }
                    return
                }
            }
        }
        lock.withLock { liveSweep = sweep }
    }

    private func endLiveTask() {
        if liveTask != .invalid { UIApplication.shared.endBackgroundTask(liveTask) }
        liveTask = .invalid
    }

    // MARK: - Past turns (docs/plan-watch-app.md → Relay → Past turns)

    private func handleTurns(_ message: [String: Any], replyHandler: @escaping ([String: Any]) -> Void) {
        guard let client = client() else { replyHandler(["error": WatchSnapshot.Failure.signedOut.rawValue]); return }
        guard let id = message["id"] as? String else { replyHandler(["error": "server"]); return }
        let task = UIApplication.shared.beginBackgroundTask(withName: "watch-turns")
        Task {
            defer { if task != .invalid { UIApplication.shared.endBackgroundTask(task) } }
            guard let page = try? await client.fetchHistory(id: id, before: nil, limit: 300) else {
                replyHandler(["error": "server"]); return
            }
            let encoder = JSONEncoder()
            encoder.dateEncodingStrategy = .iso8601
            let data = (try? encoder.encode(PastTurn.group(page.events))) ?? Data("[]".utf8)
            replyHandler(["turns": data])
        }
    }

    // MARK: - Full message (docs/plan-watch-app.md → Relay)

    /// Claude's last answer in full (Markdown as written), capped at 20,000 characters.
    private func handleFullMessage(_ message: [String: Any], replyHandler: @escaping ([String: Any]) -> Void) {
        guard let client = client() else { replyHandler(["error": WatchSnapshot.Failure.signedOut.rawValue]); return }
        guard let id = message["id"] as? String else { replyHandler(["error": "server"]); return }
        let task = UIApplication.shared.beginBackgroundTask(withName: "watch-message")
        Task {
            defer { if task != .invalid { UIApplication.shared.endBackgroundTask(task) } }
            guard let page = try? await client.fetchHistory(id: id, before: nil, limit: 40) else {
                replyHandler(["error": "server"]); return
            }
            let last = page.events.last { $0.type == .text && !($0.text ?? "").allSatisfy(\.isWhitespace) }
            guard var text = last?.text else { replyHandler([:]); return }
            if text.count > 20_000 { text = String(text.prefix(20_000)) + "…" }
            replyHandler(["text": text])
        }
    }

    /// Run a watch action against the server (docs/plan-watch-app.md → Relay → Action).
    private func perform(_ message: [String: Any]) async -> Bool {
        guard let client = client(), let id = message["id"] as? String,
              let action = message["action"] as? String else { return false }
        _ = await markRead(id)   // an action also marks it read
        do {
            switch action {
            case "answer":
                guard let answers = message["answers"] as? [String: String], !answers.isEmpty else { return false }
                try await client.answerQuestion(id: id, answers: answers)
            case "approve":
                try await client.approve(id: id)
            case "deny":
                try await client.cancel(id: id)
            case "continue":
                _ = try await client.sendInput(id: id, text: "Continue.")
            case "stop":
                try await client.cancel(id: id)
            case "sendNow":
                try await client.flushQueue(sessionId: id)
            case "reply":
                let text = (message["text"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                guard !text.isEmpty else { return false }
                _ = try await client.sendInput(id: id, text: text)
            default:
                return false
            }
            return true
        } catch {
            return false
        }
    }

    /// Server-side read state (docs/plan-watch-app.md → Read state).
    private func markRead(_ id: String?) async -> Bool {
        guard let client = client(), let id else { return false }
        do { try await client.markRead(id: id); return true } catch { return false }
    }

    // MARK: - Snapshot

    private func client() -> APIClient? { serverConfig().map { APIClient(config: $0) } }

    /// The saved server settings (nil when not signed in).
    private func serverConfig() -> ServerConfig? {
        let d = UserDefaults.standard
        let token = d.string(forKey: "authToken") ?? ""
        guard !token.isEmpty else { return nil }
        let host = d.string(forKey: "serverHost") ?? "100.90.85.91"
        let port = d.integer(forKey: "serverPort")
        return ServerConfig(host: host, port: port > 0 ? port : 8377, token: token)
    }

    func buildSnapshot() async -> WatchSnapshot {
        guard let client = client() else {
            return WatchSnapshot(fetchedAt: Date(), error: .signedOut, sessions: [])
        }
        guard let sessions = try? await client.fetchSessions() else {
            return WatchSnapshot(fetchedAt: Date(), error: .server, sessions: lock.withLock { lastGood })
        }
        // The drawer's order: newest last input first, the Ops session last
        let ordered = sessions.filter { !$0.isOps }.sorted(by: sessionOrder) + sessions.filter(\.isOps)
        let base = ordered.map { WatchSession($0, lastText: nil) }
        let cached = lock.withLock { lastTextCache }

        // Reuse a session's last message unless it's working or its activity time changed.
        let texts = await withTaskGroup(of: (String, String??).self) { group in
            for s in base {
                if s.state != .working, let c = cached[s.id], c.since == s.since, c.state == s.state {
                    group.addTask { (s.id, .some(c.text)) }
                    continue
                }
                group.addTask {
                    guard let page = try? await client.fetchHistory(id: s.id, before: nil, limit: 40) else {
                        return (s.id, nil)   // failed: keep the previous value
                    }
                    let last = page.events.last { $0.type == .text && !($0.text ?? "").allSatisfy(\.isWhitespace) }
                    return (s.id, .some(last?.text.map { WatchText.markdownExcerpt($0) }))
                }
            }
            var out: [String: String??] = [:]
            for await (id, text) in group { out[id] = text }
            return out
        }

        let result: [WatchSession] = lock.withLock {
            var next: [String: (since: Date, state: WatchSession.State, text: String?)] = [:]
            let merged = base.map { s -> WatchSession in
                var s = s
                if let fetched = texts[s.id], let text = fetched {
                    s.lastText = text
                    next[s.id] = (s.since, s.state, text)
                } else {
                    s.lastText = lastTextCache[s.id]?.text
                    if let old = lastTextCache[s.id] { next[s.id] = old }
                }
                return s
            }
            lastTextCache = next
            lastGood = merged
            return lastGood
        }
        return WatchSnapshot(fetchedAt: Date(), error: nil, sessions: result)
    }
}
