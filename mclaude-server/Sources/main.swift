import Foundation
import Hummingbird
import HummingbirdTLS
import HummingbirdWebSocket
import NIOSSL

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

let tmuxTarget = ProcessInfo.processInfo.environment["MCLAUDE_TMUX_TARGET"] ?? "mclaude"
let monitor = TmuxMonitor(tmuxTarget: tmuxTarget)
let notifier = SignalNotifier(recipientPhone: signalRecipient)
let broadcaster = WSBroadcaster()
let jsonlTailer = JSONLTailer()
let sessionStore = SessionStore()
let k8sManager = K8sSessionManager()

// Wire up status change notifications
await monitor.setOnStatusChange { session, oldStatus, newStatus in
    print("[\(session.projectName)] \(oldStatus.rawValue) -> \(newStatus.rawValue)")
    Task {
        await notifier.notify(session: session, from: oldStatus, to: newStatus)
    }
}

// Wire up WebSocket broadcasts (per-user filtered)
let capturedStore = sessionStore
await monitor.setOnSessionsUpdated { tmuxSessions in
    Task {
        let k8sSessions = await capturedK8s.getCachedSessions()
        let allSessions = tmuxSessions + k8sSessions
        await broadcaster.broadcastFiltered { userId in
            let filtered = await capturedStore.filterSessions(allSessions, userId: userId)
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

// Wire up K8s session broadcasts (per-user filtered)
let capturedK8s = k8sManager
await k8sManager.setOnSessionsUpdated { k8sSessions in
    Task {
        let tmuxSessions = await monitor.getCachedSessions()
        let allSessions = tmuxSessions + k8sSessions
        await broadcaster.broadcastFiltered { userId in
            let filtered = await capturedStore.filterSessions(allSessions, userId: userId)
            return encodeWSMessage(type: "sessions", data: filtered)
        }
    }
}

await k8sManager.setOnOutputChanged { id, content in
    Task {
        await broadcaster.broadcastFiltered { userId in
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
        // Poll K8s sessions
        _ = await capturedK8s.getSessions()
        try? await Task.sleep(nanoseconds: pollInterval * 1_000_000_000)
    }
}

// Self-health-check: exit if we can't serve requests (launchd will restart us)
let healthCheckPort = port
let healthCheckTLS = FileManager.default.fileExists(atPath:
    FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent("mclaude-certs/home-server.taild44daa.ts.net.crt").path)
let healthTask = Task {
    // Wait for server to start up
    try? await Task.sleep(nanoseconds: 10_000_000_000)
    var consecutiveFailures = 0
    while !Task.isCancelled {
        try? await Task.sleep(nanoseconds: 30_000_000_000)
        let scheme = healthCheckTLS ? "https" : "http"
        guard let url = URL(string: "\(scheme)://127.0.0.1:\(healthCheckPort)/health") else { continue }
        do {
            let config = URLSessionConfiguration.ephemeral
            config.timeoutIntervalForRequest = 5
            let session = URLSession(configuration: config)
            let (_, response) = try await session.data(from: url)
            if let http = response as? HTTPURLResponse, http.statusCode == 200 {
                consecutiveFailures = 0
            } else {
                consecutiveFailures += 1
                print("[health-check] Non-200 response, failure \(consecutiveFailures)/3")
            }
        } catch {
            consecutiveFailures += 1
            print("[health-check] Failed (\(consecutiveFailures)/3): \(error.localizedDescription)")
        }
        if consecutiveFailures >= 3 {
            print("[health-check] 3 consecutive failures, exiting for launchd restart")
            exit(1)
        }
    }
}

// Build single router for HTTP + WebSocket
let router = buildRouter(monitor: monitor, broadcaster: broadcaster, jsonlTailer: jsonlTailer, sessionStore: sessionStore, k8s: k8sManager)

// Check for TLS certs
let certsDir = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("mclaude-certs")
let certFile = certsDir.appendingPathComponent("home-server.taild44daa.ts.net.crt")
let keyFile = certsDir.appendingPathComponent("home-server.taild44daa.ts.net.key")

let useTLS = FileManager.default.fileExists(atPath: certFile.path)
    && FileManager.default.fileExists(atPath: keyFile.path)

if useTLS {
    let certData = try Data(contentsOf: certFile)
    let keyData = try Data(contentsOf: keyFile)
    let certificate = try NIOSSLCertificate(bytes: [UInt8](certData), format: .pem)
    let privateKey = try NIOSSLPrivateKey(bytes: [UInt8](keyData), format: .pem)
    let tlsConfig = TLSConfiguration.makeServerConfiguration(
        certificateChain: [.certificate(certificate)],
        privateKey: .privateKey(privateKey)
    )
    let app = Application(
        router: router,
        server: try .tls(.http1WebSocketUpgrade(webSocketRouter: router), tlsConfiguration: tlsConfig),
        configuration: .init(address: .hostname(host, port: port))
    )
    print("mclaude-server listening on \(host):\(port) (HTTPS + WSS)")
    do {
        try await app.run()
    } catch {
        print("Server error: \(error)")
        pollTask.cancel()
    }
} else {
    let app = Application(
        router: router,
        server: .http1WebSocketUpgrade(webSocketRouter: router),
        configuration: .init(address: .hostname(host, port: port))
    )
    print("mclaude-server listening on \(host):\(port) (HTTP + WebSocket)")
    print("  TLS disabled: no certs found at \(certsDir.path)")
    do {
        try await app.run()
    } catch {
        print("Server error: \(error)")
        pollTask.cancel()
    }
}
