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

/// POST /videos size cap.
let videoUploadLimit = 500 * 1024 * 1024

/// A single HTTP byte range ("bytes=a-b", "bytes=a-", "bytes=-n") clamped to a file of `size`
/// bytes, or nil if it's malformed or unsatisfiable. Multi-range requests aren't supported (nil).
func parseByteRange(_ header: String, size: Int) -> ClosedRange<Int>? {
    let spec = header.trimmingCharacters(in: .whitespaces)
    guard size > 0, spec.hasPrefix("bytes="), !spec.contains(",") else { return nil }
    let parts = spec.dropFirst(6).split(separator: "-", maxSplits: 1, omittingEmptySubsequences: false)
        .map { $0.trimmingCharacters(in: .whitespaces) }
    guard parts.count == 2 else { return nil }
    if parts[0].isEmpty {                          // suffix: last n bytes
        guard let n = Int(parts[1]), n > 0 else { return nil }
        return max(0, size - n)...(size - 1)
    }
    guard let start = Int(parts[0]), start >= 0, start < size else { return nil }
    if parts[1].isEmpty { return start...(size - 1) }
    guard let end = Int(parts[1]), end >= start else { return nil }
    return start...min(end, size - 1)
}

func buildRouter(monitor: SessionManager, broadcaster: WSBroadcaster, sessionStore: SessionStore? = nil, k8s: K8sSessionManager? = nil, push: PushNotifier? = nil) -> Router<BasicWebSocketRequestContext> {
    let router = Router(context: BasicWebSocketRequestContext.self)
    router.add(middleware: TelemetryMiddleware())  // telemetry: a server span per request (before any route)
    let capturedMonitor = monitor
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
            sessionId = await capturedMonitor.createSession(cwd: req.cwd, token: token, name: req.name)
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

    // List known project directories
    router.get("/projects") { _, _ in
        let workDir = NSHomeDirectory() + "/work"
        let fm = FileManager.default
        let sessionUse = await capturedMonitor.lastUsedByCwd()
        func mtime(_ path: String) -> Date? {
            (try? fm.attributesOfItem(atPath: path))?[.modificationDate] as? Date
        }
        var projects: [ProjectInfo] = []
        if let entries = try? fm.contentsOfDirectory(atPath: workDir) {
            for entry in entries.sorted() {
                let fullPath = workDir + "/" + entry
                var isDir: ObjCBool = false
                if fm.fileExists(atPath: fullPath, isDirectory: &isDir), isDir.boolValue,
                   !entry.hasPrefix(".") {
                    let isGit = fm.fileExists(atPath: fullPath + "/.git")
                    // Recency signals: mclaude sessions, Claude transcripts (terminal use too),
                    // and git activity (reflog/index mtime: commits, checkouts, staging)
                    let transcriptDir = ClaudePaths.projectsDir + "/" + ClaudePaths.encode(fullPath)
                    let newestTranscript = ((try? fm.contentsOfDirectory(atPath: transcriptDir)) ?? [])
                        .filter { $0.hasSuffix(".jsonl") }
                        .compactMap { mtime(transcriptDir + "/" + $0) }.max()
                    let gitUse = isGit ? [mtime(fullPath + "/.git/logs/HEAD"), mtime(fullPath + "/.git/index")]
                        .compactMap { $0 }.max() : nil
                    let lastUsed = [sessionUse[fullPath], newestTranscript, gitUse].compactMap { $0 }.max()
                    projects.append(ProjectInfo(name: entry, path: fullPath, lastUsed: lastUsed, isGit: isGit))
                }
            }
        }
        let projectEncoder = JSONEncoder()
        projectEncoder.dateEncodingStrategy = .iso8601
        let data = try projectEncoder.encode(projects)
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
            let outcome = await capturedMonitor.sendInputOutcome(id: id, text: input.text, sendEnter: input.sendEnter ?? true,
                                                                 automated: input.automated ?? false)
            if case .queued(let queueId) = outcome {
                return Response(status: .ok, headers: [.contentType: "application/json"],
                                body: .init(byteBuffer: .init(string: "{\"status\":\"queued\",\"queueId\":\"\(queueId)\"}")))
            }
            if case .answered = outcome {
                return Response(status: .ok, headers: [.contentType: "application/json"],
                                body: .init(byteBuffer: .init(string: "{\"status\":\"answered\"}")))
            }
            sent = outcome.ok
        }

        let result = sent ? "{\"status\":\"sent\"}" : "{\"status\":\"failed\"}"
        return Response(
            status: sent ? .ok : .internalServerError,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(string: result))
        )
    }

    // Answer a pending AskUserQuestion: {"answers": {"<question text>": "<answer>"}}
    router.post("/sessions/:id/answer") { request, context in
        guard let id = context.parameters.get("id") else {
            return Response(status: .badRequest)
        }
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        if userId != "owner" {
            guard await capturedStore!.userOwns(sessionId: id, userId: userId) else {
                return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
            }
        }
        struct AnswerBody: Decodable { let answers: [String: String] }
        let body = try await request.body.collect(upTo: 1_048_576)
        guard let parsed = try? JSONDecoder().decode(AnswerBody.self, from: body) else {
            return Response(status: .badRequest, headers: [.contentType: "application/json"],
                            body: .init(byteBuffer: .init(string: "{\"error\":\"expected {\\\"answers\\\": {question: answer}}\"}")))
        }
        guard !id.hasPrefix("k8s-"), await capturedMonitor.getSession(id: id) != nil else {
            return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
        }
        let (status, json): (HTTPResponse.Status, String) = switch await capturedMonitor.answerQuestion(id: id, answers: parsed.answers) {
        case .answered: (.ok, "{\"status\":\"answered\"}")
        case .noPendingQuestion: (.conflict, "{\"error\":\"no question is pending\"}")
        case .missingAnswer: (.badRequest, "{\"error\":\"every question needs an answer\"}")
        case .failed: (.internalServerError, "{\"status\":\"failed\"}")
        }
        return Response(status: status, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(string: json)))
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
            sent = await capturedMonitor.approve(id: id)
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
            sent = await capturedMonitor.cancel(id: id)
        }
        let result = sent ? "{\"status\":\"cancelled\"}" : "{\"status\":\"failed\"}"
        return Response(
            status: sent ? .ok : .internalServerError,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(string: result))
        )
    }

    // End a session: stops its Claude process and forgets it (the transcript stays on disk)
    router.delete("/sessions/:id") { request, context in
        guard let id = context.parameters.get("id") else { return Response(status: .badRequest) }
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        if userId != "owner" {
            guard await capturedStore!.userOwns(sessionId: id, userId: userId) else {
                return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
            }
        }
        guard await capturedMonitor.deleteSession(id: id) else {
            return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
        }
        await capturedStore?.removeOwner(sessionId: id)
        return Response(status: .ok, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(string: "{\"status\":\"deleted\"}")))
    }

    // Resume sheet: Claude Code transcripts on this laptop that aren't open sessions. Owner only
    // (they're the owner's files): anyone else gets [] from the list and 403 from resume.
    let transcripts = TranscriptIndex()
    func isLaptopOwner(_ userId: String) async -> Bool {
        if userId == "owner" { return true }
        let owner = await capturedStore?.ownerUserId ?? "owner"
        return userId == owner
    }
    func jsonResponse(_ status: HTTPResponse.Status, _ body: String) -> Response {
        Response(status: status, headers: [.contentType: "application/json"],
                 body: .init(byteBuffer: .init(string: body)))
    }

    router.get("/transcripts") { request, _ in
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        guard await isLaptopOwner(userId) else { return jsonResponse(.ok, "[]") }
        let state = capturedMonitor.resumeState.read()
        let list = await transcripts.list(open: Set(state.open.keys), endedNames: state.endedNames)
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        let data = try encoder.encode(list)
        return Response(status: .ok, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(data: data)))
    }

    struct ResumeBody: Decodable { let token: String? }
    router.post("/transcripts/:id/resume") { request, context in
        guard let id = context.parameters.get("id") else { return Response(status: .badRequest) }
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        guard await isLaptopOwner(userId) else { return jsonResponse(.forbidden, "{\"error\":\"forbidden\"}") }
        let body = try await request.body.collect(upTo: 65_536)
        let token = (try? JSONDecoder().decode(ResumeBody.self, from: body))?.token ?? extractToken(from: request)
        let key = id.lowercased()
        if let open = capturedMonitor.resumeState.read().open[key] {
            return jsonResponse(.ok, "{\"status\":\"open\",\"id\":\"\(open)\"}")
        }
        guard let transcript = await transcripts.find(id: key) else {
            return jsonResponse(.notFound, "{\"error\":\"not found\"}")
        }
        switch await capturedMonitor.resumeTranscript(transcript, token: token) {
        case .resumed(let sessionId):
            await capturedStore?.setOwner(sessionId: sessionId, userId: userId)
            return jsonResponse(.ok, "{\"status\":\"resumed\",\"id\":\"\(sessionId)\"}")
        case .alreadyOpen(let sessionId):
            return jsonResponse(.ok, "{\"status\":\"open\",\"id\":\"\(sessionId)\"}")
        case .directoryMissing:
            return jsonResponse(.unprocessableContent, "{\"error\":\"directory missing\"}")
        case .unavailable:
            return jsonResponse(.serviceUnavailable, "{\"error\":\"server unavailable\"}")
        }
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
        struct PlanResponse: Codable { let plan: String; let fileName: String }
        // A plan awaiting approval comes straight from Claude's ExitPlanMode request
        if let pending = await capturedMonitor.pendingPlan(id: id) {
            let data = try JSONEncoder().encode(PlanResponse(plan: pending, fileName: "Plan awaiting approval"))
            return Response(status: .ok, headers: [.contentType: "application/json"], body: .init(byteBuffer: .init(data: data)))
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

    // Usage page: per-day tokens and list-price cost by model and project, from every transcript
    // on this laptop. Owner only (like /transcripts); anyone else gets no days.
    let usageIndex = UsageIndex()
    router.get("/usage/summary") { request, _ in
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        guard await isLaptopOwner(userId) else {
            return Response(status: .ok, headers: [.contentType: "application/json"],
                            body: .init(byteBuffer: .init(data: try encoder.encode(UsageSummary.empty))))
        }
        let query = request.uri.queryParameters
        switch UsageIndex.parseQuery(tz: query["tz"].map(String.init), from: query["from"].map(String.init)) {
        case .failure(.badTimeZone):
            return jsonResponse(.badRequest, "{\"error\":\"invalid tz\"}")
        case .failure(.badFrom):
            return jsonResponse(.badRequest, "{\"error\":\"invalid from (yyyy-MM-dd)\"}")
        case .success(let (zone, start)):
            let summary = await usageIndex.summary(timeZone: zone, from: start)
            return Response(status: .ok, headers: [.contentType: "application/json"],
                            body: .init(byteBuffer: .init(data: try encoder.encode(summary))))
        }
    }

    // Get recent structured events for a session
    // Push notifications: the app registers its APNs device token here
    struct DeviceRegistration: Decodable { let token: String; let environment: String? }
    router.post("/devices") { request, context in
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        let body = try await request.body.collect(upTo: 65_536)
        let reg = try JSONDecoder().decode(DeviceRegistration.self, from: body)
        await push?.register(token: reg.token, environment: reg.environment ?? "production", userId: userId)
        return Response(status: .ok, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(string: "{\"status\":\"ok\"}")))
    }
    // Send a test notification to registered devices (Settings → "Send test notification")
    // Push a notification to the owner's devices (the Ops API, docs/plan-observability.md → Ops session).
    // Body: {"title", "body", "sessionId"}; tapping it opens that session. Owner only.
    struct PushRequest: Decodable { let title: String; let body: String; let sessionId: String? }
    router.post("/push/notify") { request, context in
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        let owner = await capturedStore?.ownerUserId ?? "owner"
        guard userId == owner || userId == "owner" else { return Response(status: .forbidden) }
        guard let push, await push.isConfigured else {
            return Response(status: .serviceUnavailable, body: .init(byteBuffer: .init(string: "{\"error\":\"push not configured\"}")))
        }
        let body = try await request.body.collect(upTo: 65_536)
        let req = try JSONDecoder().decode(PushRequest.self, from: body)
        await push.notify(title: req.title, body: req.body, sessionId: req.sessionId ?? "", userId: userId)
        return Response(status: .ok, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(string: "{\"status\":\"sent\"}")))
    }

    router.post("/push/test") { request, context in
        guard await requireAuth(request: request) != nil else { return unauthorized }
        guard let push, await push.isConfigured else {
            return Response(status: .serviceUnavailable, body: .init(byteBuffer: .init(string: "{\"error\":\"push not configured\"}")))
        }
        await push.notify(title: "MClaude", body: "Test notification from mclaude-server", sessionId: "", userId: nil)
        return Response(status: .ok, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(string: "{\"status\":\"sent\"}")))
    }

    // Queued messages (held while Claude is busy): edit, delete, or deliver now
    struct QueueEdit: Decodable { let text: String }
    func queueRoute(_ request: Request, _ context: some RequestContext,
                    _ op: (String, String?) async throws -> Bool) async throws -> Response {
        guard let id = context.parameters.get("id") else { return Response(status: .badRequest) }
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        guard await capturedStore!.userOwns(sessionId: id, userId: userId) else { return Response(status: .notFound) }
        let ok = try await op(id, context.parameters.get("qid"))
        return Response(status: ok ? .ok : .notFound, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(string: ok ? "{\"status\":\"ok\"}" : "{\"error\":\"not queued\"}")))
    }
    router.put("/sessions/:id/queue/:qid") { request, context in
        let body = try await request.body.collect(upTo: 1_048_576)
        let edit = try JSONDecoder().decode(QueueEdit.self, from: body)
        return try await queueRoute(request, context) { id, qid in
            await capturedMonitor.editQueued(id: id, queueId: qid ?? "", text: edit.text)
        }
    }
    router.delete("/sessions/:id/queue/:qid") { request, context in
        try await queueRoute(request, context) { id, qid in
            await capturedMonitor.deleteQueued(id: id, queueId: qid ?? "")
        }
    }
    router.post("/sessions/:id/queue/flush") { request, context in
        try await queueRoute(request, context) { id, _ in
            await capturedMonitor.flushQueue(id: id)
        }
    }

    // Rename a session (app's Info screen): {"name": "..."}
    struct RenameBody: Decodable { let name: String }
    router.patch("/sessions/:id") { request, context in
        guard let id = context.parameters.get("id") else { return Response(status: .badRequest) }
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        guard await capturedStore!.userOwns(sessionId: id, userId: userId) else { return Response(status: .notFound) }
        let body = try await request.body.collect(upTo: 65_536)
        guard let req = try? JSONDecoder().decode(RenameBody.self, from: body),
              let name = SessionManager.normalizedName(req.name) else {
            return Response(status: .badRequest, headers: [.contentType: "application/json"],
                            body: .init(byteBuffer: .init(string: "{\"error\":\"name required\"}")))
        }
        guard let stored = await capturedMonitor.setName(id: id, name: name) else {
            return Response(status: .notFound, headers: [.contentType: "application/json"],
                            body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
        }
        let data = try JSONSerialization.data(withJSONObject: ["status": "ok", "name": stored])
        return Response(status: .ok, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(data: data)))
    }

    // Mark a session read (docs/design-claude-uplift.md → Read state): owner or laptop owner, else 404
    router.post("/sessions/:id/read") { request, context in
        guard let id = context.parameters.get("id") else { return Response(status: .badRequest) }
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        var allowed = await isLaptopOwner(userId)
        if !allowed { allowed = await capturedStore?.userOwns(sessionId: id, userId: userId) ?? true }
        guard allowed, !id.hasPrefix("k8s-"), await capturedMonitor.markRead(id: id) else {
            return Response(status: .notFound, headers: [.contentType: "application/json"],
                            body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
        }
        return Response(status: .ok, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(string: "{\"ok\":true}")))
    }

    // Token usage, context size and cost for one conversation (app's Info screen)
    router.get("/sessions/:id/usage") { request, context in
        guard let id = context.parameters.get("id") else { return Response(status: .badRequest) }
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        guard await capturedStore!.userOwns(sessionId: id, userId: userId),
              let usage = await capturedMonitor.getUsage(id: id) else {
            return Response(status: .notFound)
        }
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        return Response(status: .ok, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(data: try encoder.encode(usage))))
    }

    // MARK: MCP servers (docs/plan-mcp-servers.md)

    func jsonObjectResponse(_ status: HTTPResponse.Status, _ object: Any) -> Response {
        let data = (try? JSONSerialization.data(withJSONObject: object)) ?? Data("{}".utf8)
        return Response(status: status, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(data: data)))
    }
    func mcpError(_ e: SessionManager.McpCallError) -> Response {
        switch e {
        case .notFound: return jsonObjectResponse(.notFound, ["error": "not found"])
        case .notRunning: return jsonObjectResponse(.conflict, ["error": "Claude isn't running in this session"])
        case .timeout: return jsonObjectResponse(.gatewayTimeout, ["error": "Claude didn't answer"])
        case .claude(let message): return jsonObjectResponse(.badGateway, ["error": message])
        }
    }
    /// Body for GET /sessions/:id/mcp (also returned by the per-session actions)
    func sessionMcpBody(_ id: String) async -> Response {
        switch await capturedMonitor.mcpStatus(id: id) {
        case .failure(let e): return mcpError(e)
        case .success(let (cwd, servers)):
            return jsonObjectResponse(.ok, [
                "project": ["name": (cwd as NSString).lastPathComponent, "cwd": cwd],
                "servers": McpConfig.sortedSessionServers(servers.map(\.value)),
            ])
        }
    }
    func readJSONObject(_ request: Request) async throws -> [String: Any]? {
        let body = try await request.body.collect(upTo: 262_144)
        if body.readableBytes == 0 { return [:] }
        return (try? JSONSerialization.jsonObject(with: Data(buffer: body))) as? [String: Any]
    }
    /// Owner check shared by the per-session MCP routes; nil means go ahead.
    func mcpSessionGuard(_ request: Request, _ context: some RequestContext) async -> (String, Response?) {
        guard let id = context.parameters.get("id") else { return ("", Response(status: .badRequest)) }
        guard let userId = await requireAuth(request: request) else { return (id, unauthorized) }
        guard await capturedStore?.userOwns(sessionId: id, userId: userId) ?? true else {
            return (id, jsonObjectResponse(.notFound, ["error": "not found"]))
        }
        return (id, nil)
    }

    router.get("/sessions/:id/mcp") { request, context in
        let (id, denied) = await mcpSessionGuard(request, context)
        if let denied { return denied }
        return await sessionMcpBody(id)
    }

    router.post("/sessions/:id/mcp/reconnect") { request, context in
        let (id, denied) = await mcpSessionGuard(request, context)
        if let denied { return denied }
        guard let body = try await readJSONObject(request) else { return jsonObjectResponse(.badRequest, ["error": "invalid JSON"]) }
        let names: [String]
        if let name = body["name"] as? String {
            names = [name]
        } else {
            // Every server that isn't disabled
            switch await capturedMonitor.mcpStatus(id: id) {
            case .failure(let e): return mcpError(e)
            case .success(let (_, servers)):
                names = servers.compactMap { s in
                    s.value["status"] as? String == "disabled" ? nil : s.value["name"] as? String
                }
            }
        }
        if let e = await capturedMonitor.mcpReconnect(id: id, names: names) { return mcpError(e) }
        return await sessionMcpBody(id)
    }

    router.post("/sessions/:id/mcp/toggle") { request, context in
        let (id, denied) = await mcpSessionGuard(request, context)
        if let denied { return denied }
        guard let body = try await readJSONObject(request), let name = body["name"] as? String,
              let enabled = body["enabled"] as? Bool else {
            return jsonObjectResponse(.badRequest, ["error": "name and enabled required"])
        }
        if let e = await capturedMonitor.mcpToggle(id: id, name: name, enabled: enabled) { return mcpError(e) }
        return await sessionMcpBody(id)
    }

    router.post("/sessions/:id/mcp/auth") { request, context in
        let (id, denied) = await mcpSessionGuard(request, context)
        if let denied { return denied }
        guard let body = try await readJSONObject(request), let name = body["name"] as? String else {
            return jsonObjectResponse(.badRequest, ["error": "name required"])
        }
        switch await capturedMonitor.mcpAuthenticate(id: id, name: name) {
        case .failure(let e): return mcpError(e)
        case .success(let box):
            guard let url = box.value["authUrl"] as? String else {
                return jsonObjectResponse(.badGateway, ["error": "Claude didn't return a sign-in link"])
            }
            var out: [String: Any] = ["authUrl": url]
            if let port = box.value["callbackPort"] as? Int { out["callbackPort"] = port }
            return jsonObjectResponse(.ok, out)
        }
    }

    router.post("/sessions/:id/mcp/auth/callback") { request, context in
        let (id, denied) = await mcpSessionGuard(request, context)
        if let denied { return denied }
        guard let body = try await readJSONObject(request), let name = body["name"] as? String,
              let callbackUrl = body["callbackUrl"] as? String else {
            return jsonObjectResponse(.badRequest, ["error": "name and callbackUrl required"])
        }
        if let e = await capturedMonitor.mcpAuthCallback(id: id, name: name, callbackUrl: callbackUrl) {
            if case .claude(let message) = e { return jsonObjectResponse(.badRequest, ["error": message]) }
            return mcpError(e)
        }
        return await sessionMcpBody(id)
    }

    router.post("/sessions/:id/restart") { request, context in
        let (id, denied) = await mcpSessionGuard(request, context)
        if let denied { return denied }
        switch await capturedMonitor.restart(id: id) {
        case .restarting: return jsonObjectResponse(.ok, ["status": "restarting"])
        case .busy: return jsonObjectResponse(.conflict, ["error": "Finish or stop the current turn first"])
        case .notRunning: return jsonObjectResponse(.conflict, ["error": "Claude isn't running in this session"])
        case .notFound: return jsonObjectResponse(.notFound, ["error": "not found"])
        }
    }

    // Global (laptop owner only): user MCP config + every session's statuses
    func mcpOwnerGuard(_ request: Request) async -> Response? {
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        guard await isLaptopOwner(userId) else { return jsonObjectResponse(.forbidden, ["error": "forbidden"]) }
        return nil
    }
    func globalMcpBody() async -> Response {
        let config = McpConfig.userServers()
        let ready = await capturedMonitor.mcpReadySessions()
        let monitor = capturedMonitor
        let reports = await withTaskGroup(of: (Int, String, String, [[String: Any]])?.self) { group in
            for (i, s) in ready.enumerated() {
                group.addTask {
                    guard case .success(let (_, servers)) = await monitor.mcpStatus(id: s.id, timeout: 5) else { return nil }
                    return (i, s.id, s.name, servers.map(\.value))
                }
            }
            var out: [(Int, String, String, [[String: Any]])] = []
            for await r in group { if let r { out.append(r) } }
            return out.sorted { $0.0 < $1.0 }.map { (id: $0.1, name: $0.2, servers: $0.3) }
        }
        let stale = await capturedMonitor.mcpStaleSessionCount(current: McpConfig.userFingerprint())
        return jsonObjectResponse(.ok, [
            "servers": McpConfig.globalServers(config: config, reports: reports),
            "sessionCount": reports.count,
            "staleSessions": stale,
        ])
    }

    router.get("/mcp") { request, _ in
        if let denied = await mcpOwnerGuard(request) { return denied }
        return await globalMcpBody()
    }

    router.post("/mcp/servers") { request, _ in
        if let denied = await mcpOwnerGuard(request) { return denied }
        guard let body = try await readJSONObject(request), let name = body["name"] as? String,
              let config = body["config"] as? [String: Any] else {
            return jsonObjectResponse(.badRequest, ["error": "name and config required"])
        }
        guard McpConfig.isValidName(name) else {
            return jsonObjectResponse(.badRequest, ["error": "Letters, numbers, dot, dash and underscore only."])
        }
        if McpConfig.userServers()[name] != nil {
            return jsonObjectResponse(.conflict, ["error": "A server named \(name) already exists"])
        }
        let clean = McpConfig.merged(old: [:], new: config)
        if let e = await McpConfig.add(claudePath: capturedMonitor.claudePath, name: name, config: clean) {
            return jsonObjectResponse(.badRequest, ["error": e.message])
        }
        return await globalMcpBody()
    }

    router.put("/mcp/servers/:name") { request, context in
        if let denied = await mcpOwnerGuard(request) { return denied }
        guard let name = context.parameters.get("name") else { return Response(status: .badRequest) }
        guard let old = McpConfig.userServers()[name] else { return jsonObjectResponse(.notFound, ["error": "not found"]) }
        guard let body = try await readJSONObject(request), let config = body["config"] as? [String: Any] else {
            return jsonObjectResponse(.badRequest, ["error": "config required"])
        }
        let new = McpConfig.merged(old: old, new: config)
        let claude = capturedMonitor.claudePath
        if let e = await McpConfig.remove(claudePath: claude, name: name) {
            return jsonObjectResponse(.badRequest, ["error": e.message])
        }
        if let e = await McpConfig.add(claudePath: claude, name: name, config: new) {
            // Put the old config back before answering
            if let restoreError = await McpConfig.add(claudePath: claude, name: name, config: old) {
                print("[mcp] WARNING: failed to restore \(name) after a failed edit: \(restoreError.message)")
            }
            return jsonObjectResponse(.badRequest, ["error": e.message])
        }
        return await globalMcpBody()
    }

    router.delete("/mcp/servers/:name") { request, context in
        if let denied = await mcpOwnerGuard(request) { return denied }
        guard let name = context.parameters.get("name") else { return Response(status: .badRequest) }
        guard McpConfig.userServers()[name] != nil else { return jsonObjectResponse(.notFound, ["error": "not found"]) }
        if let e = await McpConfig.remove(claudePath: capturedMonitor.claudePath, name: name) {
            return jsonObjectResponse(.badRequest, ["error": e.message])
        }
        return await globalMcpBody()
    }

    router.post("/mcp/restart-all") { request, _ in
        if let denied = await mcpOwnerGuard(request) { return denied }
        let (now, later) = await capturedMonitor.restartAll()
        return jsonObjectResponse(.ok, ["restarting": now, "afterTurn": later])
    }

    // Peer messages (docs/plan-peer-messages.md). Visible like /usage: the caller must see :id
    // (owner or laptop owner). A PeerRef links a session only when the caller can see that one too.
    func canSeeSession(_ sessionId: String, _ userId: String) async -> Bool {
        if await isLaptopOwner(userId) { return true }
        return await capturedStore?.userOwns(sessionId: sessionId, userId: userId) ?? true
    }
    func withPeerRefs(_ events: [SessionEvent], userId: String) async -> [SessionEvent] {
        guard events.contains(where: { $0.peerMessages != nil }) else { return events }
        return await capturedMonitor.peerDirectory().refresh(events, canSee: { await canSeeSession($0, userId) })
    }
    /// (Claude UUID of :id, directory, visible session ids), or nil → 404
    func peerContext(_ id: String, _ userId: String) async -> (String, PeerDirectory, Set<String>)? {
        guard await canSeeSession(id, userId), let uuid = await capturedMonitor.claudeSessionId(id: id) else { return nil }
        let directory = await capturedMonitor.peerDirectory()
        let visible = await PeerDirectory.visible(Set(directory.open.values.map(\.id)), canSee: { await canSeeSession($0, userId) })
        return (uuid, directory, visible)
    }

    router.get("/sessions/:id/peers") { request, context in
        guard let id = context.parameters.get("id") else { return Response(status: .badRequest) }
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        guard let (uuid, directory, visible) = await peerContext(id, userId) else {
            return jsonResponse(.notFound, "{\"error\":\"not found\"}")
        }
        let list = PeerAPI.peers(this: uuid, records: capturedMonitor.peerStore.records(involving: uuid),
                                 directory: directory, visible: { visible.contains($0) })
        return Response(status: .ok, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(data: try JSONEncoder().encode(list))))
    }

    router.get("/sessions/:id/peers/:peerUuid/thread") { request, context in
        guard let id = context.parameters.get("id"), let peer = context.parameters.get("peerUuid")?.lowercased() else {
            return Response(status: .badRequest)
        }
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        guard let (uuid, directory, visible) = await peerContext(id, userId),
              let thread = PeerAPI.thread(this: uuid, peer: peer, records: capturedMonitor.peerStore.records(involving: uuid),
                                          directory: directory, registry: PeerRegistry.read(dir: capturedMonitor.peerRegistryDir),
                                          visible: { visible.contains($0) }) else {
            return jsonResponse(.notFound, "{\"error\":\"not found\"}")
        }
        return Response(status: .ok, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(data: try JSONEncoder().encode(thread))))
    }

    router.get("/sessions/:id/events") { request, context in
        guard let id = context.parameters.get("id") else {
            return Response(status: .badRequest)
        }
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        guard await capturedStore!.userOwns(sessionId: id, userId: userId) else {
            return Response(status: .notFound, body: .init(byteBuffer: .init(string: "[]")))
        }
        guard let events = await capturedMonitor.recentEvents(id: id, count: 200) else {
            return Response(status: .notFound, body: .init(byteBuffer: .init(string: "[]")))
        }
        let data = try JSONEncoder().encode(await withPeerRefs(events, userId: userId))
        return Response(
            status: .ok,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(data: data))
        )
    }

    // The live turn as it stands (docs/plan-watch-app.md → Server → Live stream)
    router.get("/sessions/:id/live") { request, context in
        guard let id = context.parameters.get("id") else {
            return Response(status: .badRequest)
        }
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        guard await capturedStore!.userOwns(sessionId: id, userId: userId),
              var snapshot = await capturedMonitor.liveSnapshot(id: id) else {
            return jsonResponse(.notFound, "{\"error\":\"not found\"}")
        }
        if let prompt = snapshot.prompt, prompt.peer != nil {       // the peer's name, as this caller may see it
            snapshot.prompt = await prompt.named(by: capturedMonitor.peerDirectory(),
                                                 canSee: { await canSeeSession($0, userId) })
        }
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        return Response(status: .ok, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(data: try encoder.encode(snapshot))))
    }

    // Older conversation history, paged from the transcript on disk:
    // GET /sessions/:id/history?before=<uuid>&beforeTs=<iso8601>&limit=<n>  ->  {"events":[…],"hasMore":bool}
    router.get("/sessions/:id/history") { request, context in
        guard let id = context.parameters.get("id") else {
            return Response(status: .badRequest)
        }
        guard let userId = await requireAuth(request: request) else { return unauthorized }
        guard await capturedStore!.userOwns(sessionId: id, userId: userId) else {
            return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
        }
        let query = request.uri.queryParameters
        let limit = min(max(Int(query.get("limit") ?? "") ?? 200, 1), 1000)
        guard let page = await capturedMonitor.historyPage(id: id, before: query.get("before"),
                                                           beforeTimestamp: query.get("beforeTs"), limit: limit) else {
            return Response(status: .notFound, body: .init(byteBuffer: .init(string: "{\"error\":\"not found\"}")))
        }
        let data = try JSONEncoder().encode(HistoryPage(events: await withPeerRefs(page.events, userId: userId),
                                                        hasMore: page.hasMore))
        return Response(status: .ok, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(data: data)))
    }

    // Upload screenshot
    // Serve an uploaded photo or tool-result image by file name (basename only: no traversal).
    router.get("/screenshots/:name") { request, context in
        guard await requireAuth(request: request) != nil else { return unauthorized }
        guard let name = context.parameters.get("name"),
              name == (name as NSString).lastPathComponent, !name.hasPrefix("."),
              let attrs = try? FileManager.default.attributesOfItem(atPath: "/tmp/mclaude-screenshots/\(name)"),
              attrs[.type] as? FileAttributeType == .typeRegular,
              let size = (attrs[.size] as? NSNumber)?.intValue else {
            return Response(status: .notFound)
        }
        let path = "/tmp/mclaude-screenshots/\(name)"
        let ext = (name as NSString).pathExtension.lowercased()
        let type = ["png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp",
                    "mov": "video/quicktime", "mp4": "video/mp4", "wav": "audio/wav"][ext]
            ?? "application/octet-stream"
        var headers: HTTPFields = [.contentType: type, .cacheControl: "private, max-age=86400", .acceptRanges: "bytes"]
        // Streamed from disk (videos can be hundreds of MB). A single byte range is honoured:
        // AVPlayer only plays over HTTP when the server answers Range requests with 206.
        if let rangeHeader = request.headers[.range] {
            guard let range = parseByteRange(rangeHeader, size: size) else {
                return Response(status: .rangeNotSatisfiable, headers: [.contentRange: "bytes */\(size)"])
            }
            headers[.contentRange] = "bytes \(range.lowerBound)-\(range.upperBound)/\(size)"
            let body = try await FileIO().loadFile(path: path, range: range, context: context)
            return Response(status: .partialContent, headers: headers, body: body)
        }
        let body = try await FileIO().loadFile(path: path, context: context)
        return Response(status: .ok, headers: headers, body: body)
    }

    router.post("/screenshots") { request, context in
        let body = try await request.body.collect(upTo: 10_485_760)
        let imageData = Data(buffer: body)
        guard !imageData.isEmpty else {
            return Response(status: .badRequest, body: .init(byteBuffer: .init(string: "{\"error\":\"empty body\"}")))
        }

        let dir = "/tmp/mclaude-screenshots"
        try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)

        // Extension from magic bytes: the app uploads JPEG (photos) or PNG; content(for:) uses it as media type
        let ext = imageData.starts(with: [0xFF, 0xD8, 0xFF]) ? "jpg" : "png"
        // Concurrent uploads (multi-photo) can land in the same millisecond: claim a unique
        // name with an exclusive create instead of overwriting. Names stay digits-only so the
        // app's screenshot-\d+ pattern keeps matching.
        let base = Int(Date().timeIntervalSince1970 * 1000) * 1000
        var filePath = ""
        for n in 0..<1000 {
            let candidate = "\(dir)/screenshot-\(base + n).\(ext)"
            do {
                try imageData.write(to: URL(fileURLWithPath: candidate), options: .withoutOverwriting)
                filePath = candidate
                break
            } catch CocoaError.fileWriteFileExists {
                continue
            }
        }
        guard !filePath.isEmpty else {
            return Response(status: .internalServerError, body: .init(byteBuffer: .init(string: "{\"error\":\"could not allocate file name\"}")))
        }

        let payload = ["path": filePath]
        let data = try JSONEncoder().encode(payload)
        return Response(
            status: .ok,
            headers: [.contentType: "application/json"],
            body: .init(byteBuffer: .init(data: data))
        )
    }

    // Upload a video (screen recording); the server extracts still frames Claude can Read.
    // Body: the raw .mov/.mp4, streamed to disk (max 500 MB).
    // -> {"path","input","poster","duration","frames","changes"}; "input" is "<path> [video: …]".
    router.post("/videos") { request, context in
        guard await requireAuth(request: request) != nil else { return unauthorized }
        let dir = "/tmp/mclaude-screenshots"
        try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        func fail(_ status: HTTPResponse.Status, _ message: String) -> Response {
            Response(status: status, headers: [.contentType: "application/json"],
                     body: .init(byteBuffer: .init(string: "{\"error\":\"\(message)\"}")))
        }

        // Stream into a hidden temp file; it gets its final name once the type is known.
        let tmpPath = "\(dir)/.upload-\(UUID().uuidString)"
        guard FileManager.default.createFile(atPath: tmpPath, contents: nil),
              let handle = FileHandle(forWritingAtPath: tmpPath) else {
            return fail(.internalServerError, "could not create upload file")
        }
        var total = 0
        var head = Data()
        do {
            for try await buffer in request.body {
                total += buffer.readableBytes
                if total > videoUploadLimit { break }
                let chunk = Data(buffer: buffer)
                if head.count < 12 { head.append(chunk.prefix(12 - head.count)) }
                try handle.write(contentsOf: chunk)
            }
            try handle.close()
        } catch {
            try? handle.close()
            try? FileManager.default.removeItem(atPath: tmpPath)
            throw error
        }
        guard total <= videoUploadLimit else {
            try? FileManager.default.removeItem(atPath: tmpPath)
            return fail(.contentTooLarge, "video is over 500 MB")
        }
        guard total > 0 else {
            try? FileManager.default.removeItem(atPath: tmpPath)
            return fail(.badRequest, "empty body")
        }
        // ISO-BMFF: bytes 4..<8 are "ftyp", 8..<12 the major brand ("qt  " = QuickTime)
        guard head.count >= 12, head[4..<8] == Data("ftyp".utf8) else {
            try? FileManager.default.removeItem(atPath: tmpPath)
            return fail(.unsupportedMediaType, "not a .mov or .mp4 video")
        }
        let ext = head[8..<12] == Data("qt  ".utf8) ? "mov" : "mp4"

        // Claim a unique digits-only name (the app matches video-\d+), as /screenshots does
        let base = Int(Date().timeIntervalSince1970 * 1000) * 1000
        var stem = ""
        for n in 0..<1000 {
            let candidate = "video-\(base + n)"
            if link(tmpPath, "\(dir)/\(candidate).\(ext)") == 0 { stem = candidate; break }  // atomic, fails if taken
            if errno != EEXIST { break }
        }
        try? FileManager.default.removeItem(atPath: tmpPath)
        guard !stem.isEmpty else { return fail(.internalServerError, "could not allocate file name") }

        let videoPath = "\(dir)/\(stem).\(ext)"
        let frameDir = "\(dir)/\(stem)"
        let posterPath = "\(dir)/\(stem)-poster.jpg"
        let result: VideoFrames.Result
        do {
            result = try await VideoFrames.process(videoPath: videoPath, frameDir: frameDir, posterPath: posterPath)
        } catch {
            print("[videos] \(stem).\(ext): \(error)")
            for p in [videoPath, frameDir, posterPath] { try? FileManager.default.removeItem(atPath: p) }
            return fail(.unprocessableContent, "could not extract frames from this video")
        }

        struct Payload: Encodable {
            let path: String, input: String, poster: String, duration: Double, frames: Int, changes: Int
        }
        let data = try JSONEncoder().encode(Payload(path: result.videoPath, input: result.input, poster: result.posterPath,
                                                    duration: (result.duration * 10).rounded() / 10,
                                                    frames: result.framePaths.count, changes: result.changeCount))
        return Response(status: .ok, headers: [.contentType: "application/json"],
                        body: .init(byteBuffer: .init(data: data)))
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
        let seq = SessionsSeq.shared.next()
        if let k8s = capturedK8s {
            currentSessions += await k8s.getCachedSessions()
        }
        currentSessions = await capturedStore?.filterSessions(currentSessions, userId: userId) ?? currentSessions
        if let sessionsMsg = encodeWSMessage(type: "sessions", data: currentSessions, seq: seq) {
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
