import Foundation
import UIKit
import WatchConnectivity

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

    private func client() -> APIClient? {
        let d = UserDefaults.standard
        let token = d.string(forKey: "authToken") ?? ""
        guard !token.isEmpty else { return nil }
        let host = d.string(forKey: "serverHost") ?? "100.90.85.91"
        let port = d.integer(forKey: "serverPort")
        return APIClient(config: ServerConfig(host: host, port: port > 0 ? port : 8377, token: token))
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
