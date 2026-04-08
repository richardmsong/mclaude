import Foundation
import Hummingbird
import HummingbirdWebSocket

let host = ProcessInfo.processInfo.environment["MCLAUDE_HOST"] ?? "0.0.0.0"
let port = Int(ProcessInfo.processInfo.environment["MCLAUDE_PORT"] ?? "8377") ?? 8377
let signalRecipient = ProcessInfo.processInfo.environment["MCLAUDE_SIGNAL_RECIPIENT"]
let pollInterval: UInt64 = UInt64(ProcessInfo.processInfo.environment["MCLAUDE_POLL_INTERVAL"] ?? "1") ?? 1

// Disable stdout buffering for log visibility
setbuf(stdout, nil)
print("mclaude-server starting...")
print("  Host: \(host)")
print("  Port: \(port)")
print("  Poll interval: \(pollInterval)s")

let monitor = TmuxMonitor()
let notifier = SignalNotifier(recipientPhone: signalRecipient)
let broadcaster = WSBroadcaster()
let jsonlTailer = JSONLTailer()
let sessionStore = SessionStore()

// Wire up status change notifications
await monitor.setOnStatusChange { session, oldStatus, newStatus in
    print("[\(session.projectName)] \(oldStatus.rawValue) -> \(newStatus.rawValue)")
    Task {
        await notifier.notify(session: session, from: oldStatus, to: newStatus)
    }
}

// Wire up WebSocket broadcasts (per-user filtered)
let capturedStore = sessionStore
await monitor.setOnSessionsUpdated { sessions in
    Task {
        await broadcaster.broadcastFiltered { userId in
            let filtered = await capturedStore.filterSessions(sessions, userId: userId)
            return encodeWSMessage(type: "sessions", data: filtered)
        }
    }
}

await monitor.setOnOutputChanged { id, content in
    Task {
        await broadcaster.broadcastFiltered { userId in
            // Check if this user owns the session
            let owns = await capturedStore.userOwns(sessionId: id, userId: userId)
            guard owns else { return nil }
            let payload = ["id": id, "output": content]
            return encodeWSMessage(type: "output", data: payload)
        }
    }
}

// Wire up JSONL structured events (per-user filtered)
await jsonlTailer.setOnEvent { id, event in
    Task {
        print("[event] id=\(id) type=\(event.type.rawValue) uuid=\(event.uuid.prefix(8))")
        if let eventData = try? JSONEncoder().encode(event),
           let eventJson = String(data: eventData, encoding: .utf8) {
            let combined = "{\"id\":\"\(id)\",\"event\":\(eventJson)}"
            await broadcaster.broadcastFiltered { userId in
                let owns = await capturedStore.userOwns(sessionId: id, userId: userId)
                guard owns else { return nil }
                return encodeWSMessage(type: "event", rawData: combined)
            }
        }
    }
}

// Start background polling — drives cache + broadcasts + JSONL watching
let capturedTailer = jsonlTailer
let pollTask = Task {
    while !Task.isCancelled {
        // Collect JSONL working states and idle timestamps for all watched sessions
        var jsonlIdleSince: [String: Date] = [:]
        var jsonlWorking: Set<String> = []
        for session in await monitor.getCachedSessions() {
            if let idleDate = await capturedTailer.lastIdleSince(id: session.id) {
                jsonlIdleSince[session.id] = idleDate
            }
            if await capturedTailer.isWorking(id: session.id) {
                jsonlWorking.insert(session.id)
            }
        }
        let sessions = await monitor.getSessions(jsonlIdleSince: jsonlIdleSince, jsonlWorking: jsonlWorking)
        // Start JSONL watchers for any new sessions
        for session in sessions {
            await capturedTailer.watchSession(id: session.id, sessionId: session.sessionId, cwd: session.cwd)
        }
        try? await Task.sleep(nanoseconds: pollInterval * 1_000_000_000)
    }
}

// Build single router for HTTP + WebSocket
let router = buildRouter(monitor: monitor, broadcaster: broadcaster, jsonlTailer: jsonlTailer, sessionStore: sessionStore)

let app = Application(
    router: router,
    server: .http1WebSocketUpgrade(webSocketRouter: router),
    configuration: .init(address: .hostname(host, port: port))
)

print("mclaude-server listening on \(host):\(port) (HTTP + WebSocket)")

do {
    try await app.run()
} catch {
    print("Server error: \(error)")
    pollTask.cancel()
}
