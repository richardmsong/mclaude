import Foundation
import Hummingbird
import HummingbirdTLS
import HummingbirdWebSocket
import NIOSSL
import NIOCore

let host = ProcessInfo.processInfo.environment["MCLAUDE_HOST"] ?? "0.0.0.0"
let port = Int(ProcessInfo.processInfo.environment["MCLAUDE_PORT"] ?? "8377") ?? 8377
let signalRecipient = ProcessInfo.processInfo.environment["MCLAUDE_SIGNAL_RECIPIENT"]
let pollInterval: UInt64 = UInt64(ProcessInfo.processInfo.environment["MCLAUDE_POLL_INTERVAL"] ?? "1") ?? 1
let stateDir = ProcessInfo.processInfo.environment["MCLAUDE_STATE_DIR"] ?? "\(NSHomeDirectory())/.mclaude"

// Disable stdout buffering for log visibility
setbuf(stdout, nil)
print("mclaude-server starting...")
print("  Host: \(host)")
print("  Port: \(port)")
print("  Poll interval: \(pollInterval)s (k8s only)")
print("  State dir: \(stateDir)")

// Sessions are headless Claude Code processes owned by this server (stream-json over stdio),
// replacing the tmux-scraping monitor. See SessionManager.swift.
let monitor = SessionManager(stateDir: stateDir)
let notifier = SignalNotifier(recipientPhone: signalRecipient)
let broadcaster = WSBroadcaster()
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
await monitor.setOnSessionsUpdated { hostSessions in
    let seq = SessionsSeq.shared.next()   // numbered when the snapshot is taken
    Task {
        let k8sSessions = await capturedK8s.getCachedSessions()
        let allSessions = hostSessions + k8sSessions
        await broadcaster.broadcastFiltered { userId in
            let filtered = await capturedStore.filterSessions(allSessions, userId: userId)
            return encodeWSMessage(type: "sessions", data: filtered, seq: seq)
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
    let seq = SessionsSeq.shared.next()
    Task {
        let hostSessions = await monitor.getCachedSessions()
        let allSessions = hostSessions + k8sSessions
        await broadcaster.broadcastFiltered { userId in
            let filtered = await capturedStore.filterSessions(allSessions, userId: userId)
            return encodeWSMessage(type: "sessions", data: filtered, seq: seq)
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

// Structured session events, straight from Claude's stdout (per-user filtered)
await monitor.setOnEvent { id, event in
    Task {
        // Peer messages: PeerRefs are filled in per user (links only to sessions they can see)
        if event.peerMessages != nil {
            let directory = await monitor.peerDirectory()
            await broadcaster.broadcastFiltered { userId in
                guard await capturedStore.userOwns(sessionId: id, userId: userId) else { return nil }
                let owner = await capturedStore.ownerUserId
                let laptopOwner = userId == nil || userId == "owner" || userId == owner
                let filled = await directory.refresh([event], canSee: { sid in
                    if laptopOwner { return true }
                    return await capturedStore.userOwns(sessionId: sid, userId: userId)
                })
                guard let data = try? JSONEncoder().encode(filled[0]), let json = String(data: data, encoding: .utf8) else { return nil }
                return encodeWSMessage(type: "event", rawData: "{\"id\":\"\(id)\",\"event\":\(json)}")
            }
            return
        }
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

let pushNotifier = PushNotifier(dir: stateDir + "/apns")
await pushNotifier.load()
await monitor.setPushNotifier(pushNotifier)
await monitor.start()

// OpenTelemetry to the local collector, plus heartbeat gauges every 15 s (docs/plan-observability.md)  // telemetry
Telemetry.shared.start()  // telemetry
let telemetryHeartbeatTask = Task {  // telemetry
    while !Task.isCancelled {  // telemetry
        let beat = await monitor.telemetryHeartbeat()  // telemetry
        Telemetry.shared.gauge("mclaude.server.up", 1)  // telemetry
        Telemetry.shared.gauge("mclaude.server.ws_clients", await broadcaster.clientCount)  // telemetry
        for (state, n) in beat.processStates { Telemetry.shared.gauge("mclaude.server.sessions", n, attributes: ["process": state]) }  // telemetry
        Telemetry.shared.gauge("mclaude.server.turns_running", beat.turnsRunning)  // telemetry
        try? await Task.sleep(nanoseconds: 15_000_000_000)  // telemetry
    }  // telemetry
}  // telemetry

// K8s sessions are still polled
let pollTask = Task {
    while !Task.isCancelled {
        _ = await capturedK8s.getSessions()
        try? await Task.sleep(nanoseconds: pollInterval * 1_000_000_000)
    }
}

// Graceful drain on SIGTERM (launchd stop/restart): let running turns finish, then stop every
// Claude process. Sessions resume from their transcripts on the next start.
signal(SIGTERM, SIG_IGN)
let sigtermSource = DispatchSource.makeSignalSource(signal: SIGTERM, queue: .main)
sigtermSource.setEventHandler {
    print("[shutdown] SIGTERM: draining sessions")
    Task {
        await monitor.drain(timeout: 300)
        print("[shutdown] done")
        exit(0)
    }
}
sigtermSource.resume()

// Self-health-check: exit if we can't serve requests (launchd will restart us)
let healthCheckPort = port
let healthCheckTLS = FileManager.default.fileExists(atPath:
    FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent("mclaude-certs/home-server.taild44daa.ts.net.crt").path)
/// The TLS cert is issued for the Tailscale hostname, so a loopback request can never validate
/// it. Before this, every self-check failed and the server exited every ~100s (launchd restarted
/// it), which with server-owned Claude processes would kill every running turn.
final class LoopbackTrust: NSObject, URLSessionDelegate {
    func urlSession(_ session: URLSession, didReceive challenge: URLAuthenticationChallenge,
                    completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
        if challenge.protectionSpace.host == "127.0.0.1", let trust = challenge.protectionSpace.serverTrust {
            completionHandler(.useCredential, URLCredential(trust: trust))
        } else {
            completionHandler(.performDefaultHandling, nil)
        }
    }
}
let loopbackTrust = LoopbackTrust()
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
            let session = URLSession(configuration: config, delegate: loopbackTrust, delegateQueue: nil)
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
let router = buildRouter(monitor: monitor, broadcaster: broadcaster, sessionStore: sessionStore, k8s: k8sManager, push: pushNotifier)

// Check for TLS certs
let certsDir = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("mclaude-certs")
let certFile = certsDir.appendingPathComponent("home-server.taild44daa.ts.net.crt")
let keyFile = certsDir.appendingPathComponent("home-server.taild44daa.ts.net.key")

let useTLS = FileManager.default.fileExists(atPath: certFile.path)
    && FileManager.default.fileExists(atPath: keyFile.path)

/// Keeps the HTTP listener up. Hummingbird ends its accept loop for good on any accept error
/// (HummingbirdCore `Server.run`), and on macOS a client that resets mid-accept makes SwiftNIO
/// throw `NIOFcntlFailedError` there. On 2026-11-04 that took the whole process down and launchd's
/// restart killed every running turn. Instead, when the listening socket closes unexpectedly,
/// cancel that Application (closing its connections; clients reconnect) and bind a fresh one.
/// Sessions and Claude processes live outside it and keep running. More than 5 restarts in a
/// minute means something is really wrong: exit and let launchd take over.
final class ListenerRun: @unchecked Sendable {
    private let lock = NSLock()
    private var task: Task<Void, Error>?
    func set(_ t: Task<Void, Error>) { lock.lock(); task = t; lock.unlock() }
    func cancel() { lock.lock(); let t = task; lock.unlock(); t?.cancel() }
}

func serveForever<App: ApplicationProtocol>(
    _ makeApp: (_ onServerRunning: @escaping @Sendable (Channel) async -> Void) throws -> App
) async {
    var restarts: [Date] = []
    while true {
        let run = ListenerRun()
        do {
            let app = try makeApp { channel in
                // Fires when the listening socket closes: on an accept error, or when this
                // instance is cancelled below (then it's a no-op).
                channel.closeFuture.whenComplete { _ in run.cancel() }
                // Test hook: close the listening socket after N seconds, which is what an accept
                // error does (e.g. MCLAUDE_TEST_CLOSE_LISTENER_AFTER=8 on a :8399 test server).
                if let s = ProcessInfo.processInfo.environment["MCLAUDE_TEST_CLOSE_LISTENER_AFTER"], let secs = UInt64(s) {
                    Task {
                        try? await Task.sleep(nanoseconds: secs * 1_000_000_000)
                        print("[test] closing the listening socket")
                        try? await channel.close()
                    }
                }
            }
            let task = Task { try await app.run() }
            run.set(task)
            if case .failure(let error) = await task.result { print("Server error: \(error)") }
        } catch {
            print("Server error: \(error)")
        }
        restarts = restarts.filter { $0 > Date().addingTimeInterval(-60) } + [Date()]
        if restarts.count > 5 {
            print("[server] listener failed \(restarts.count) times in a minute; exiting for launchd")
            exit(1)
        }
        print("[server] listener stopped; restarting it (sessions keep running)")
        try? await Task.sleep(nanoseconds: 1_000_000_000)
    }
}

if useTLS {
    let certs = try NIOSSLCertificate.fromPEMFile(certFile.path)
    let privateKey = try NIOSSLPrivateKey(file: keyFile.path, format: .pem)
    let tlsConfig = TLSConfiguration.makeServerConfiguration(
        certificateChain: certs.map { .certificate($0) },
        privateKey: .privateKey(privateKey)
    )
    print("mclaude-server listening on \(host):\(port) (HTTPS + WSS)")
    await serveForever { onServerRunning in
        Application(
            router: router,
            server: try .tls(.http1WebSocketUpgrade(webSocketRouter: router), tlsConfiguration: tlsConfig),
            configuration: .init(address: .hostname(host, port: port)),
            onServerRunning: onServerRunning
        )
    }
} else {
    print("mclaude-server listening on \(host):\(port) (HTTP + WebSocket)")
    print("  TLS disabled: no certs found at \(certsDir.path)")
    await serveForever { onServerRunning in
        Application(
            router: router,
            server: .http1WebSocketUpgrade(webSocketRouter: router),
            configuration: .init(address: .hostname(host, port: port)),
            onServerRunning: onServerRunning
        )
    }
}
