import Foundation
import Hummingbird
import HummingbirdWebSocket

private struct WSCommand: Codable {
    let type: String
    let id: String?
    let lines: Int?
}

/// Extract bearer token from Authorization header
private func extractToken(from request: Request) -> String? {
    guard let auth = request.headers[.authorization] else { return nil }
    let prefix = "Bearer "
    guard auth.hasPrefix(prefix) else { return nil }
    return String(auth.dropFirst(prefix.count))
}

/// Extract token from WebSocket query string (?token=xxx)
private func extractWSToken(from request: Request) -> String? {
    guard let uri = request.uri.query else { return nil }
    for param in uri.split(separator: "&") {
        let parts = param.split(separator: "=", maxSplits: 1)
        if parts.count == 2 && parts[0] == "token" {
            return String(parts[1])
        }
    }
    return nil
}

func buildRouter(monitor: TmuxMonitor, broadcaster: WSBroadcaster, jsonlTailer: JSONLTailer? = nil, sessionStore: SessionStore? = nil) -> Router<BasicWebSocketRequestContext> {
    let router = Router(context: BasicWebSocketRequestContext.self)
    let capturedMonitor = monitor
    let capturedTailer = jsonlTailer
    let capturedStore = sessionStore

    // Health check
    router.get("/health") { _, _ in
        return Response(status: .ok, body: .init(byteBuffer: .init(string: "{\"status\":\"ok\"}")))
    }

    // List all sessions (reads from cache, filtered by user)
    router.get("/sessions") { request, _ in
        var sessions = await capturedMonitor.getCachedSessions()
        if let store = capturedStore, let token = extractToken(from: request) {
            let userId = await store.authenticate(token: token)
            sessions = await store.filterSessions(sessions, userId: userId)
        }
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        let data = try encoder.encode(sessions)
        return Response(
            status: .ok,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(data: data))
        )
    }

    // Get single session (reads from cache, checks ownership)
    router.get("/sessions/:id") { request, context in
        guard let id = context.parameters.get("id") else {
            return Response(status: .badRequest)
        }
        // Check ownership
        if let store = capturedStore, let token = extractToken(from: request) {
            let userId = await store.authenticate(token: token)
            guard await store.userOwns(sessionId: id, userId: userId) else {
                return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
            }
        }
        let sessions = await capturedMonitor.getCachedSessions()
        guard let session = sessions.first(where: { $0.id == id }) else {
            return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
        }
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        let data = try encoder.encode(session)
        return Response(
            status: .ok,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(data: data))
        )
    }

    // Get session output (reads from cache)
    router.get("/sessions/:id/output") { request, context in
        guard let id = context.parameters.get("id") else {
            return Response(status: .badRequest)
        }
        let content = await capturedMonitor.getCachedOutput(id: id) ?? ""
        let payload = ["output": content]
        let data = try JSONEncoder().encode(payload)
        return Response(
            status: .ok,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(data: data))
        )
    }

    // List available skills (builtins + global + per-project)
    router.get("/skills") { _, _ in
        let sessions = await capturedMonitor.getCachedSessions()
        let cwds = sessions.map(\.cwd)
        let skills = SkillsScanner.scanAll(sessionCwds: cwds)
        let data = try JSONEncoder().encode(skills)
        return Response(
            status: .ok,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(data: data))
        )
    }

    // Create a new session
    router.post("/sessions") { request, context in
        let body = try await request.body.collect(upTo: 1_048_576)
        let req = try JSONDecoder().decode(CreateSessionRequest.self, from: body)

        // Use token from request body or Authorization header
        let token = req.token ?? extractToken(from: request)
        let windowId = await capturedMonitor.createSession(cwd: req.cwd, token: token)

        if let windowId {
            // Track ownership
            if let store = capturedStore, let token {
                let userId = await store.authenticate(token: token)
                await store.setOwner(sessionId: windowId, userId: userId)
            }
            let result = "{\"status\":\"created\",\"id\":\"\(windowId)\"}"
            return Response(
                status: .ok,
                headers: [.contentType: "application/json"],
                body: .init(byteBuffer: .init(string: result))
            )
        } else {
            return Response(
                status: .internalServerError,
                headers: [.contentType: "application/json"],
                body: .init(byteBuffer: .init(string: "{\"status\":\"failed\"}"))
            )
        }
    }

    // List known project directories
    router.get("/projects") { _, _ in
        let workDir = NSHomeDirectory() + "/work"
        let fm = FileManager.default
        var projects: [ProjectInfo] = []
        if let entries = try? fm.contentsOfDirectory(atPath: workDir) {
            for entry in entries.sorted() {
                let fullPath = workDir + "/" + entry
                var isDir: ObjCBool = false
                if fm.fileExists(atPath: fullPath, isDirectory: &isDir), isDir.boolValue,
                   !entry.hasPrefix(".") {
                    projects.append(ProjectInfo(name: entry, path: fullPath))
                }
            }
        }
        let data = try JSONEncoder().encode(projects)
        return Response(
            status: .ok,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(data: data))
        )
    }

    // Send input to session
    router.post("/sessions/:id/input") { request, context in
        guard let id = context.parameters.get("id") else {
            return Response(status: .badRequest)
        }
        if let store = capturedStore, let token = extractToken(from: request) {
            let userId = await store.authenticate(token: token)
            guard await store.userOwns(sessionId: id, userId: userId) else {
                return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
            }
        }
        guard let session = await capturedMonitor.getSession(id: id) else {
            return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
        }
        let body = try await request.body.collect(upTo: 1_048_576)
        let input = try JSONDecoder().decode(SessionInput.self, from: body)

        let window = session.tmuxWindow
        let sent = await capturedMonitor.sendKeys(window: window, keys: input.text)
        if input.sendEnter ?? true {
            _ = await capturedMonitor.sendEnter(window: window)
        }

        let result = sent ? "{\"status\":\"sent\"}" : "{\"status\":\"failed\"}"
        return Response(
            status: sent ? .ok : .internalServerError,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(string: result))
        )
    }

    // Quick approve
    router.post("/sessions/:id/approve") { request, context in
        guard let id = context.parameters.get("id") else {
            return Response(status: .badRequest)
        }
        if let store = capturedStore, let token = extractToken(from: request) {
            let userId = await store.authenticate(token: token)
            guard await store.userOwns(sessionId: id, userId: userId) else {
                return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
            }
        }
        guard let session = await capturedMonitor.getSession(id: id) else {
            return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
        }
        let sent = await capturedMonitor.sendEnter(window: session.tmuxWindow)
        let result = sent ? "{\"status\":\"approved\"}" : "{\"status\":\"failed\"}"
        return Response(
            status: sent ? .ok : .internalServerError,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(string: result))
        )
    }

    // Cancel
    router.post("/sessions/:id/cancel") { request, context in
        guard let id = context.parameters.get("id") else {
            return Response(status: .badRequest)
        }
        if let store = capturedStore, let token = extractToken(from: request) {
            let userId = await store.authenticate(token: token)
            guard await store.userOwns(sessionId: id, userId: userId) else {
                return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
            }
        }
        guard let session = await capturedMonitor.getSession(id: id) else {
            return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
        }
        let sent = await capturedMonitor.sendKeys(window: session.tmuxWindow, keys: "Escape")
        let result = sent ? "{\"status\":\"cancelled\"}" : "{\"status\":\"failed\"}"
        return Response(
            status: sent ? .ok : .internalServerError,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(string: result))
        )
    }

    // Get recent structured events for a session
    router.get("/sessions/:id/events") { request, context in
        guard let id = context.parameters.get("id") else {
            return Response(status: .badRequest)
        }
        if let store = capturedStore, let token = extractToken(from: request) {
            let userId = await store.authenticate(token: token)
            guard await store.userOwns(sessionId: id, userId: userId) else {
                return Response(status: .notFound, body: .init(byteBuffer: .init(string: "[]")))
            }
        }
        guard let session = await capturedMonitor.getSession(id: id),
              let tailer = capturedTailer else {
            return Response(status: .notFound, body: .init(byteBuffer: .init(string: "[]")))
        }
        let events = await tailer.getRecentEvents(id: id, sessionId: session.sessionId, cwd: session.cwd, count: 200)
        let data = try JSONEncoder().encode(events)
        return Response(
            status: .ok,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(data: data))
        )
    }

    // Upload screenshot
    router.post("/screenshots") { request, context in
        let body = try await request.body.collect(upTo: 10_485_760)
        let imageData = Data(buffer: body)
        guard !imageData.isEmpty else {
            return Response(status: .badRequest, body: .init(byteBuffer: .init(string: "{\"error\":\"empty body\"}")))
        }

        let dir = "/tmp/mclaude-screenshots"
        try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)

        let timestamp = Int(Date().timeIntervalSince1970 * 1000)
        let filePath = "\(dir)/screenshot-\(timestamp).png"
        try imageData.write(to: URL(fileURLWithPath: filePath))

        let payload = ["path": filePath]
        let data = try JSONEncoder().encode(payload)
        return Response(
            status: .ok,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(data: data))
        )
    }

    // Upload arbitrary file (preserves original filename)
    router.post("/files") { request, context in
        let body = try await request.body.collect(upTo: 52_428_800)  // 50MB limit
        let fileData = Data(buffer: body)
        guard !fileData.isEmpty else {
            return Response(status: .badRequest, body: .init(byteBuffer: .init(string: "{\"error\":\"empty body\"}")))
        }

        let filename = request.headers[.init("X-Filename")!] ?? "file-\(Int(Date().timeIntervalSince1970 * 1000))"
        let dir = "/tmp/mclaude-files"
        try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)

        // Sanitize filename and ensure uniqueness
        let sanitized = filename.replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "..", with: "_")
        let timestamp = Int(Date().timeIntervalSince1970 * 1000)
        let filePath = "\(dir)/\(timestamp)-\(sanitized)"
        try fileData.write(to: URL(fileURLWithPath: filePath))

        let payload = ["path": filePath]
        let data = try JSONEncoder().encode(payload)
        return Response(
            status: .ok,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(data: data))
        )
    }

    // Telemetry endpoint — receives error reports from the iOS app
    router.post("/telemetry") { request, context in
        let body = try await request.body.collect(upTo: 1_048_576)
        let data = Data(buffer: body)
        if let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
            let timestamp = json["timestamp"] as? String ?? "unknown"
            let error = json["error"] as? String ?? "unknown"
            let context = json["context"] as? String ?? ""
            print("[TELEMETRY] \(timestamp) | \(error) | \(context)")
        }
        return Response(status: .ok, body: .init(byteBuffer: .init(string: "{\"status\":\"received\"}")))
    }

    // WebSocket endpoint
    let capturedBroadcaster = broadcaster
    router.ws("/ws") { inbound, outbound, context in
        let clientId = UUID()

        // Extract token from query string for per-user filtering
        var clientUserId: String? = nil
        if let store = capturedStore, let token = extractWSToken(from: context.request) {
            clientUserId = await store.authenticate(token: token)
        }

        print("[WS] Client connected: \(clientId) userId=\(clientUserId ?? "owner")")

        // If user has a token, filter broadcasts to only their sessions
        let userId = clientUserId
        await capturedBroadcaster.addClient(id: clientId, userId: userId) { message in
            try? await outbound.write(.text(message))
        }

        // Send current state immediately on connect (filtered)
        var currentSessions = await capturedMonitor.getCachedSessions()
        if let store = capturedStore, let userId {
            currentSessions = await store.filterSessions(currentSessions, userId: userId)
        }
        if let sessionsMsg = encodeWSMessage(type: "sessions", data: currentSessions) {
            try? await outbound.write(.text(sessionsMsg))
        }
        for session in currentSessions {
            if let content = await capturedMonitor.getCachedOutput(id: session.id) {
                let payload = ["id": session.id, "output": content]
                if let outputMsg = encodeWSMessage(type: "output", data: payload) {
                    try? await outbound.write(.text(outputMsg))
                }
            }
        }

        // Handle client commands
        for try await frame in inbound {
            if case .text = frame.opcode {
                let text = String(buffer: frame.data)
                guard let data = text.data(using: .utf8),
                      let cmd = try? JSONDecoder().decode(WSCommand.self, from: data),
                      cmd.type == "load_more",
                      let id = cmd.id else { continue }
                // Verify ownership for load_more
                if let store = capturedStore, let userId {
                    guard await store.userOwns(sessionId: id, userId: userId) else { continue }
                }
                let lines = cmd.lines ?? 160
                let content = await capturedMonitor.getMoreOutput(id: id, lines: lines) ?? ""
                let payload = ["id": id, "output": content]
                if let msg = encodeWSMessage(type: "more_output", data: payload) {
                    try? await outbound.write(.text(msg))
                }
            }
        }

        await capturedBroadcaster.removeClient(id: clientId)
        print("[WS] Client disconnected: \(clientId)")
    }

    return router
}
