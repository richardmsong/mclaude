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

func buildRouter(monitor: TmuxMonitor, broadcaster: WSBroadcaster, jsonlTailer: JSONLTailer? = nil, sessionStore: SessionStore? = nil, k8s: K8sSessionManager? = nil) -> Router<BasicWebSocketRequestContext> {
    let router = Router(context: BasicWebSocketRequestContext.self)
    let capturedMonitor = monitor
    let capturedTailer = jsonlTailer
    let capturedStore = sessionStore
    let capturedK8s = k8s

    // Health check
    router.get("/health") { _, _ in
        return Response(status: .ok, body: .init(byteBuffer: .init(string: "{\"status\":\"ok\"}")))
    }

    // Google OAuth: get auth URL
    router.get("/auth/google/login") { _, _ in
        guard let store = capturedStore else {
            return Response(status: .internalServerError, body: .init(byteBuffer: .init(string: "{\"error\":\"auth not configured\"}")))
        }
        let url = await store.googleAuthURL()
        let payload = ["url": url]
        let data = try JSONEncoder().encode(payload)
        return Response(
            status: .ok,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(data: data))
        )
    }

    // Google OAuth: callback (exchanges code, redirects app with session token)
    router.get("/auth/google/callback") { request, _ in
        guard let store = capturedStore else {
            return Response(status: .internalServerError, body: .init(byteBuffer: .init(string: "Auth not configured")))
        }
        // Extract code from query
        guard let query = request.uri.query else {
            return Response(status: .badRequest, body: .init(byteBuffer: .init(string: "Missing code")))
        }
        var code: String?
        for param in query.split(separator: "&") {
            let parts = param.split(separator: "=", maxSplits: 1)
            if parts.count == 2 && parts[0] == "code" {
                code = String(parts[1]).removingPercentEncoding
            }
        }
        guard let authCode = code else {
            return Response(status: .badRequest, body: .init(byteBuffer: .init(string: "Missing code")))
        }

        // Exchange code for Google identity
        guard let googleInfo = await store.exchangeGoogleCode(authCode) else {
            return Response(status: .unauthorized, body: .init(byteBuffer: .init(string: "Google auth failed")))
        }

        // Create/find user and generate session token
        let (userId, sessionToken) = await store.authenticateGoogle(
            googleId: googleInfo.googleId,
            email: googleInfo.email,
            name: googleInfo.name
        )

        print("[auth] Google callback: \(googleInfo.email) -> userId=\(userId)")

        // Redirect to app with session token
        let redirectURL = "mclaude://auth?token=\(sessionToken)&name=\(googleInfo.name.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? "")&email=\(googleInfo.email.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? "")"
        return Response(
            status: .found,
            headers: [.location: redirectURL]
        )
    }

    // Get current user info
    router.get("/auth/me") { request, _ in
        guard let store = capturedStore, let token = extractToken(from: request) else {
            return Response(status: .unauthorized, body: .init(byteBuffer: .init(string: "{\"error\":\"not authenticated\"}")))
        }
        let userId = await store.authenticate(token: token)
        if let user = await store.getUser(userId: userId) {
            let payload: [String: String?] = ["id": user.id, "name": user.name, "email": user.email]
            let data = try JSONEncoder().encode(payload)
            return Response(
                status: .ok,
                headers: [.contentType: "application/json"],
                body: .init(byteBuffer: .init(data: data))
            )
        }
        return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"user not found\"}")))
    }

    // Auth helper: returns userId or nil (401)
    // If no token is provided, treat as owner (local/connector bypass).
    func requireAuth(request: Request) async -> String? {
        guard let token = extractToken(from: request) else {
            return await capturedStore?.ownerUserId ?? "owner"
        }
        guard let store = capturedStore else { return "owner" }
        let userId = await store.authenticate(token: token)
        return userId
    }

    let unauthorized = Response(status: .unauthorized, body: .init(byteBuffer: .init(string: "{\"error\":\"authentication required\"}")))

    // List all sessions (reads from cache, filtered by user)
    router.get("/sessions") { request, _ in
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        var sessions = await capturedMonitor.getCachedSessions()
        // Merge K8s sessions
        if let k8s = capturedK8s {
            sessions += await k8s.getCachedSessions()
        }
        sessions = await capturedStore!.filterSessions(sessions, userId: userId)
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
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        // Skip ownership check for local access (no token = "owner")
        if userId != "owner" {
            guard await capturedStore!.userOwns(sessionId: id, userId: userId) else {
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
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        // Skip ownership check for local access (no token = "owner")
        if userId != "owner" {
            guard await capturedStore!.userOwns(sessionId: id, userId: userId) else {
                return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
            }
        }
        let content: String
        if id.hasPrefix("k8s-"), let k8s = capturedK8s {
            content = await k8s.getCachedOutput(id: id) ?? ""
        } else {
            content = await capturedMonitor.getCachedOutput(id: id) ?? ""
        }
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
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        let body = try await request.body.collect(upTo: 1_048_576)
        let req = try JSONDecoder().decode(CreateSessionRequest.self, from: body)

        // Use token from request body or Authorization header
        let token = req.token ?? extractToken(from: request)

        let sessionId: String?
        if req.runtime == "k8s", let k8s = capturedK8s {
            let podName = await k8s.createSession(cwd: req.cwd, token: token)
            sessionId = podName.map { "k8s-\($0)" }
        } else {
            sessionId = await capturedMonitor.createSession(cwd: req.cwd, token: token, tmuxSession: req.tmuxSession, windowName: req.windowName)
        }

        if let sessionId {
            await capturedStore!.setOwner(sessionId: sessionId, userId: userId)
            let result = "{\"status\":\"created\",\"id\":\"\(sessionId)\"}"
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

    // List monitored tmux sessions (for webapp dropdown/filter)
    router.get("/tmux-sessions") { _, _ in
        let sessions = await capturedMonitor.getMonitoredSessions()
        let data = try JSONEncoder().encode(sessions)
        return Response(
            status: .ok,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(data: data))
        )
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
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        // Skip ownership check for local access (no token = "owner")
        if userId != "owner" {
            guard await capturedStore!.userOwns(sessionId: id, userId: userId) else {
                return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
            }
        }
        let body = try await request.body.collect(upTo: 1_048_576)
        let input = try JSONDecoder().decode(SessionInput.self, from: body)

        let sent: Bool
        if id.hasPrefix("k8s-"), let k8s = capturedK8s {
            let podName = await k8s.podName(from: id)
            sent = await k8s.sendKeys(podName: podName, keys: input.text)
            if input.sendEnter ?? true {
                _ = await k8s.sendEnter(podName: podName)
            }
        } else {
            guard await capturedMonitor.getSession(id: id) != nil else {
                return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
            }
            sent = await capturedMonitor.sendKeysToSession(id: id, keys: input.text)
            if input.sendEnter ?? true {
                _ = await capturedMonitor.sendEnterToSession(id: id)
            }
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
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        // Skip ownership check for local access (no token = "owner")
        if userId != "owner" {
            guard await capturedStore!.userOwns(sessionId: id, userId: userId) else {
                return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
            }
        }
        let sent: Bool
        if id.hasPrefix("k8s-"), let k8s = capturedK8s {
            sent = await k8s.sendEnter(podName: await k8s.podName(from: id))
        } else {
            guard await capturedMonitor.getSession(id: id) != nil else {
                return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
            }
            sent = await capturedMonitor.sendEnterToSession(id: id)
        }
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
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        // Skip ownership check for local access (no token = "owner")
        if userId != "owner" {
            guard await capturedStore!.userOwns(sessionId: id, userId: userId) else {
                return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
            }
        }
        let sent: Bool
        if id.hasPrefix("k8s-"), let k8s = capturedK8s {
            sent = await k8s.sendKeys(podName: await k8s.podName(from: id), keys: "Escape")
        } else {
            guard await capturedMonitor.getSession(id: id) != nil else {
                return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
            }
            sent = await capturedMonitor.sendKeysToSession(id: id, keys: "Escape")
        }
        let result = sent ? "{\"status\":\"cancelled\"}" : "{\"status\":\"failed\"}"
        return Response(
            status: sent ? .ok : .internalServerError,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(string: result))
        )
    }

    // Get the most recent plan file content for a session
    router.get("/sessions/:id/plan") { request, context in
        guard let id = context.parameters.get("id") else {
            return Response(status: .badRequest)
        }
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        if userId != "owner" {
            guard await capturedStore!.userOwns(sessionId: id, userId: userId) else {
                return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
            }
        }
        guard let session = await capturedMonitor.getSession(id: id) else {
            return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
        }
        let fm = FileManager.default
        // Check both .claude/plans/ (Claude Code default) and plans/ (project-root)
        let candidateDirs = ["\(session.cwd)/.claude/plans", "\(session.cwd)/plans"]
        let plansDir = candidateDirs.first { fm.fileExists(atPath: $0) } ?? candidateDirs[0]
        guard fm.fileExists(atPath: plansDir),
              let files = try? fm.contentsOfDirectory(atPath: plansDir) else {
            return Response(status: .ok, headers: [.contentType: "application/json"],
                            body: .init(byteBuffer: .init(string: "{\"plan\":null}")))
        }
        let mdFiles = files.filter { $0.hasSuffix(".md") }
        guard !mdFiles.isEmpty else {
            return Response(status: .ok, headers: [.contentType: "application/json"],
                            body: .init(byteBuffer: .init(string: "{\"plan\":null}")))
        }
        var newest: (path: String, date: Date)? = nil
        for file in mdFiles {
            let fullPath = "\(plansDir)/\(file)"
            if let attrs = try? fm.attributesOfItem(atPath: fullPath),
               let mod = attrs[.modificationDate] as? Date {
                if newest == nil || mod > newest!.date {
                    newest = (fullPath, mod)
                }
            }
        }
        guard let best = newest, let content = try? String(contentsOfFile: best.path, encoding: .utf8) else {
            return Response(status: .ok, headers: [.contentType: "application/json"],
                            body: .init(byteBuffer: .init(string: "{\"plan\":null}")))
        }
        let fileName = (best.path as NSString).lastPathComponent
        struct PlanResponse: Codable { let plan: String; let fileName: String }
        let resp = PlanResponse(plan: content, fileName: fileName)
        let data = try JSONEncoder().encode(resp)
        return Response(status: .ok, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(data: data)))
    }

    // Token usage timeline across all sessions (for usage dashboard)
    router.get("/usage/timeline") { request, context in
        guard let userId = await requireAuth(request: request) else { return unauthorized }

        var hours = 24
        if let q = request.uri.query {
            for param in q.split(separator: "&") {
                let parts = param.split(separator: "=", maxSplits: 1)
                if parts.count == 2 && parts[0] == "hours", let h = Int(parts[1]) {
                    hours = min(max(h, 1), 720)
                }
            }
        }

        let cutoff = Date().addingTimeInterval(-Double(hours) * 3600)
        let projectsDir = "\(NSHomeDirectory())/.claude/projects"
        let fm = FileManager.default

        struct DataPoint: Encodable {
            let ts: String
            let input: Int
            let output: Int
            let cacheRead: Int
            let cacheCreate: Int
            let model: String
        }

        var points: [DataPoint] = []

        let isoFull = ISO8601DateFormatter()
        isoFull.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let isoBasic = ISO8601DateFormatter()
        isoBasic.formatOptions = [.withInternetDateTime]

        func parseDate(_ s: String) -> Date? {
            return isoFull.date(from: s) ?? isoBasic.date(from: s)
        }

        func scanJSONL(path: String) {
            guard let data = fm.contents(atPath: path),
                  let content = String(data: data, encoding: .utf8) else { return }
            for line in content.components(separatedBy: "\n") where !line.isEmpty {
                guard let ld = line.data(using: .utf8),
                      let json = try? JSONSerialization.jsonObject(with: ld) as? [String: Any],
                      json["type"] as? String == "assistant",
                      let tsStr = json["timestamp"] as? String,
                      let date = parseDate(tsStr), date >= cutoff,
                      let message = json["message"] as? [String: Any],
                      let usage = message["usage"] as? [String: Any] else { continue }
                points.append(DataPoint(
                    ts: tsStr,
                    input: usage["input_tokens"] as? Int ?? 0,
                    output: usage["output_tokens"] as? Int ?? 0,
                    cacheRead: usage["cache_read_input_tokens"] as? Int ?? 0,
                    cacheCreate: usage["cache_creation_input_tokens"] as? Int ?? 0,
                    model: message["model"] as? String ?? ""
                ))
            }
        }

        // Scan all project JSONL dirs — any authenticated user can see their full usage history
        if let projectDirs = try? fm.contentsOfDirectory(atPath: projectsDir) {
            for dir in projectDirs {
                let dirPath = "\(projectsDir)/\(dir)"
                if let files = try? fm.contentsOfDirectory(atPath: dirPath) {
                    for file in files where file.hasSuffix(".jsonl") {
                        scanJSONL(path: "\(dirPath)/\(file)")
                    }
                }
            }
        }

        points.sort { $0.ts < $1.ts }
        struct TimelineResponse: Encodable { let points: [DataPoint] }
        let respData = try JSONEncoder().encode(TimelineResponse(points: points))
        return Response(status: .ok, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(data: respData)))
    }

    // Get aggregated token usage for a session (scans JSONL for message.usage fields)
    router.get("/sessions/:id/usage") { request, context in
        guard let id = context.parameters.get("id") else {
            return Response(status: .badRequest)
        }
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        if userId != "owner" {
            guard await capturedStore!.userOwns(sessionId: id, userId: userId) else {
                return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
            }
        }
        guard let session = await capturedMonitor.getSession(id: id) else {
            return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
        }

        let projectsDir = "\(NSHomeDirectory())/.claude/projects"
        let encodedPath = session.cwd.replacingOccurrences(of: "/", with: "-")
        let jsonlPath = "\(projectsDir)/\(encodedPath)/\(session.sessionId).jsonl"

        var inputTokens = 0, outputTokens = 0, cacheCreationTokens = 0, cacheReadTokens = 0
        var turnCount = 0
        var lastModel = ""

        if let data = FileManager.default.contents(atPath: jsonlPath),
           let content = String(data: data, encoding: .utf8) {
            for line in content.components(separatedBy: "\n") where !line.isEmpty {
                guard let lineData = line.data(using: .utf8),
                      let json = try? JSONSerialization.jsonObject(with: lineData) as? [String: Any],
                      json["type"] as? String == "assistant",
                      let message = json["message"] as? [String: Any],
                      let usage = message["usage"] as? [String: Any] else { continue }
                inputTokens += usage["input_tokens"] as? Int ?? 0
                outputTokens += usage["output_tokens"] as? Int ?? 0
                cacheCreationTokens += usage["cache_creation_input_tokens"] as? Int ?? 0
                cacheReadTokens += usage["cache_read_input_tokens"] as? Int ?? 0
                turnCount += 1
                if let model = message["model"] as? String, !model.isEmpty {
                    lastModel = model
                }
            }
        }

        struct UsageResponse: Codable {
            let inputTokens: Int
            let outputTokens: Int
            let cacheCreationTokens: Int
            let cacheReadTokens: Int
            let turnCount: Int
            let model: String
        }
        let resp = UsageResponse(
            inputTokens: inputTokens, outputTokens: outputTokens,
            cacheCreationTokens: cacheCreationTokens, cacheReadTokens: cacheReadTokens,
            turnCount: turnCount, model: lastModel
        )
        let respData = try JSONEncoder().encode(resp)
        return Response(status: .ok, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(data: respData)))
    }

    // Get recent structured events for a session
    router.get("/sessions/:id/events") { request, context in
        guard let id = context.parameters.get("id") else {
            return Response(status: .badRequest)
        }
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        guard await capturedStore!.userOwns(sessionId: id, userId: userId) else {
            return Response(status: .notFound, body: .init(byteBuffer: .init(string: "[]")))
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

        // Auth — fall back to owner if no token provided
        let userId: String
        if let token = extractWSToken(from: context.request),
           let store = capturedStore,
           let authedId = await store.authenticateSessionToken(token) {
            userId = authedId
        } else {
            userId = await capturedStore?.ownerUserId ?? "owner"
        }

        print("[WS] Client connected: \(clientId) userId=\(userId)")
        await capturedBroadcaster.addClient(id: clientId, userId: userId) { message in
            try? await outbound.write(.text(message))
        }

        // Send current state immediately on connect (filtered)
        var currentSessions = await capturedMonitor.getCachedSessions()
        if let k8s = capturedK8s {
            currentSessions += await k8s.getCachedSessions()
        }
        currentSessions = await capturedStore?.filterSessions(currentSessions, userId: userId) ?? currentSessions
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
                guard await capturedStore?.userOwns(sessionId: id, userId: userId) ?? true else { continue }
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
