import Foundation

public enum APIError: Error, Sendable {
    /// The server answered with a non-2xx status.
    case http(status: Int)
    /// The server reported `{"status":"failed"}` for an input.
    case sendFailed

    /// 404: e.g. a queued message that was already delivered.
    public var isNotFound: Bool {
        if case .http(404) = self { return true }
        return false
    }
}

/// A non-2xx answer with the server's `{"error": "..."}` text, when it sent one (MCP endpoints).
public struct APIServerError: Error, LocalizedError, Sendable {
    public let status: Int
    public let message: String?

    public init(status: Int, message: String?) {
        self.status = status
        self.message = message
    }

    public var isNotFound: Bool { status == 404 }
    public var errorDescription: String? { message ?? "The server answered \(status)." }
}

public struct APIClient: Sendable {
    private let config: ServerConfig
    private let session: URLSession
    private let decoder: JSONDecoder

    public init(config: ServerConfig) {
        self.config = config

        let urlConfig = URLSessionConfiguration.default
        urlConfig.timeoutIntervalForRequest = 15
        self.session = URLSession(configuration: urlConfig)

        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        self.decoder = decoder
    }

    /// Create a URLRequest with auth header if token is set
    private func authedRequest(url: URL, method: String = "GET") -> URLRequest {
        var request = URLRequest(url: url)
        request.httpMethod = method
        if let token = config.token {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        return request
    }

    /// Every request goes through here so the app's network telemetry sees it (NetworkObserver).
    private func observedData(for request: URLRequest) async throws -> (Data, URLResponse) {
        guard let start = NetworkObserver.start else { return try await session.data(for: request) }
        var traced = request
        let observation = start(request.httpMethod ?? "GET", request.url?.path ?? "")
        traced.setValue(observation.traceparent, forHTTPHeaderField: "traceparent")
        do {
            let result = try await session.data(for: traced)
            observation.finish((result.1 as? HTTPURLResponse)?.statusCode, nil)
            return result
        } catch {
            observation.finish(nil, error)
            throw error
        }
    }

    /// GET with auth
    private func authedData(from url: URL) async throws -> (Data, URLResponse) {
        let request = authedRequest(url: url)
        return try await observedData(for: request)
    }

    /// Fetch an image the server saved under /tmp/mclaude-screenshots (tool-result images, uploads).
    public func fetchImage(path: String) async throws -> Data {
        let name = (path as NSString).lastPathComponent
        let url = config.baseURL.appendingPathComponent("screenshots").appendingPathComponent(name)
        let (data, response) = try await authedData(from: url)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw URLError(.fileDoesNotExist) }
        return data
    }

    /// URL + auth headers for streaming a file under /tmp/mclaude-screenshots (e.g. an uploaded
    /// video in AVPlayer, which can't use this URLSession).
    public func mediaRequest(path: String) -> (url: URL, headers: [String: String]) {
        let name = (path as NSString).lastPathComponent
        let url = config.baseURL.appendingPathComponent("screenshots").appendingPathComponent(name)
        return (url, config.token.map { ["Authorization": "Bearer \($0)"] } ?? [:])
    }

    public func fetchUsage(id: String) async throws -> SessionUsage {
        let url = config.baseURL.appendingPathComponent("sessions/\(id)/usage")
        let (data, _) = try await authedData(from: url)
        return try decoder.decode(SessionUsage.self, from: data)
    }

    /// Register this iPhone's APNs token so the server can push "long turn finished" notifications.
    public func registerDevice(token: String, environment: String) async throws {
        let url = config.baseURL.appendingPathComponent("devices")
        var request = authedRequest(url: url, method: "POST")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONSerialization.data(withJSONObject: ["token": token, "environment": environment])
        let (_, response) = try await observedData(for: request)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw URLError(.badServerResponse) }
    }

    /// Ask the server to send a test push to the registered devices.
    public func sendTestPush() async throws {
        let url = config.baseURL.appendingPathComponent("push/test")
        let request = authedRequest(url: url, method: "POST")
        let (_, response) = try await observedData(for: request)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw URLError(.badServerResponse) }
    }

    public func fetchSessions() async throws -> [ClaudeSession] {
        let url = config.baseURL.appendingPathComponent("sessions")
        let (data, _) = try await authedData(from: url)
        return try decoder.decode([ClaudeSession].self, from: data)
    }

    public func fetchSession(id: String) async throws -> ClaudeSession {
        let url = config.baseURL.appendingPathComponent("sessions/\(id)")
        let (data, _) = try await authedData(from: url)
        return try decoder.decode(ClaudeSession.self, from: data)
    }

    public func fetchOutput(id: String) async throws -> String {
        let url = config.baseURL.appendingPathComponent("sessions/\(id)/output")
        let (data, _) = try await authedData(from: url)
        let payload = try decoder.decode([String: String].self, from: data)
        return payload["output"] ?? ""
    }

    /// Sends input to Claude. Returns the server queue id when the server held the message in
    /// the session's queue (Claude is mid-turn), nil when it was handed to Claude immediately.
    /// Throws on network errors, a non-2xx response, or `{"status":"failed"}`.
    /// What the server did with an input.
    public enum SendInputResult: Equatable, Sendable {
        /// Handed to Claude (or a key acted on). Also an older server without a status.
        case sent
        /// Held in the session's queue for Claude's next step.
        case queued(id: String)
        /// Answered the pending question, plan or tool approval; it won't come back as a user message.
        case answered

        /// The local placeholder can go now: the queue row or the prompt card shows it instead.
        public var leavesNoEcho: Bool { self != .sent }
    }

    @discardableResult
    public func sendInput(id: String, text: String, sendEnter: Bool = true) async throws -> SendInputResult {
        let url = config.baseURL.appendingPathComponent("sessions/\(id)/input")
        var request = authedRequest(url: url, method: "POST")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")

        struct InputBody: Encodable {
            let text: String
            let sendEnter: Bool
        }
        request.httpBody = try JSONEncoder().encode(InputBody(text: text, sendEnter: sendEnter))
        let (data, response) = try await observedData(for: request)
        try Self.requireSuccess(response)
        let payload = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        switch payload?["status"] as? String {
        case "failed": throw APIError.sendFailed
        case "queued": return (payload?["queueId"] as? String).map { .queued(id: $0) } ?? .sent
        case "answered": return .answered
        default: return .sent   // "sent", or an older server without a status
        }
    }

    /// Replace the text of a queued message. Throws `APIError.http(404)` if it was already delivered.
    public func editQueued(sessionId: String, queueId: String, text: String) async throws {
        let url = config.baseURL.appendingPathComponent("sessions/\(sessionId)/queue/\(queueId)")
        var request = authedRequest(url: url, method: "PUT")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        struct Body: Encodable { let text: String }
        request.httpBody = try JSONEncoder().encode(Body(text: text))
        let (_, response) = try await observedData(for: request)
        try Self.requireSuccess(response)
    }

    /// Remove a queued message. Throws `APIError.http(404)` if it was already delivered.
    public func deleteQueued(sessionId: String, queueId: String) async throws {
        let url = config.baseURL.appendingPathComponent("sessions/\(sessionId)/queue/\(queueId)")
        let request = authedRequest(url: url, method: "DELETE")
        let (_, response) = try await observedData(for: request)
        try Self.requireSuccess(response)
    }

    /// Rename a session (Info screen). Throws `APIError.http` on 400 (empty name) / 404.
    public func renameSession(id: String, name: String) async throws {
        let url = config.baseURL.appendingPathComponent("sessions/\(id)")
        var request = authedRequest(url: url, method: "PATCH")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        struct Body: Encodable { let name: String }
        request.httpBody = try JSONEncoder().encode(Body(name: name))
        let (_, response) = try await observedData(for: request)
        try Self.requireSuccess(response)
    }

    /// Mark a session read on the server, for every device
    /// (docs/design-claude-uplift.md → Session order, read state and background work).
    public func markRead(id: String) async throws {
        let url = config.baseURL.appendingPathComponent("sessions/\(id)/read")
        let request = authedRequest(url: url, method: "POST")
        let (_, response) = try await observedData(for: request)
        try Self.requireSuccess(response)
    }

    /// Interrupt Claude's current step and deliver the whole queue now.
    public func flushQueue(sessionId: String) async throws {
        let url = config.baseURL.appendingPathComponent("sessions/\(sessionId)/queue/flush")
        let request = authedRequest(url: url, method: "POST")
        let (_, response) = try await observedData(for: request)
        try Self.requireSuccess(response)
    }

    private static func requireSuccess(_ response: URLResponse) throws {
        guard let http = response as? HTTPURLResponse else { return }
        guard (200..<300).contains(http.statusCode) else { throw APIError.http(status: http.statusCode) }
    }

    /// Answer the pending AskUserQuestion: question text -> answer (option label(s) joined with
    /// ", ", or free "Other" text). Throws on a non-2xx response (409: nothing pending).
    public func answerQuestion(id: String, answers: [String: String]) async throws {
        let url = config.baseURL.appendingPathComponent("sessions/\(id)/answer")
        var request = authedRequest(url: url, method: "POST")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        struct Body: Encodable { let answers: [String: String] }
        request.httpBody = try JSONEncoder().encode(Body(answers: answers))
        let (_, response) = try await observedData(for: request)
        try Self.requireSuccess(response)
    }

    public func approve(id: String) async throws {
        let url = config.baseURL.appendingPathComponent("sessions/\(id)/approve")
        let request = authedRequest(url: url, method: "POST")
        let (_, _) = try await observedData(for: request)
    }

    public func cancel(id: String) async throws {
        let url = config.baseURL.appendingPathComponent("sessions/\(id)/cancel")
        let request = authedRequest(url: url, method: "POST")
        let (_, _) = try await observedData(for: request)
    }

    public func uploadScreenshot(imageData: Data, laptop: String? = nil) async throws -> String {
        let url = config.baseURL.appendingPathComponent("screenshots")
        var request = authedRequest(url: url, method: "POST")
        request.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
        if let laptop { request.setValue(laptop, forHTTPHeaderField: "X-Laptop-ID") }
        request.httpBody = imageData
        request.timeoutInterval = 30
        let (data, _) = try await observedData(for: request)
        let payload = try decoder.decode([String: String].self, from: data)
        return payload["path"] ?? ""
    }

    public struct VideoUpload: Decodable, Sendable {
        public let path: String
        /// "<path> [video: 12.4s, N frames in <dir>/ named by timestamp, …]": what goes into the message.
        public let input: String
        public let poster: String?
        public let duration: Double?
        public let frames: Int?
    }

    /// Upload a video straight from disk (never loaded into memory). The server extracts frames
    /// before answering, so the timeout covers upload + ffmpeg.
    public func uploadVideo(fileURL: URL, laptop: String? = nil) async throws -> VideoUpload {
        let url = config.baseURL.appendingPathComponent("videos")
        var request = authedRequest(url: url, method: "POST")
        request.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
        if let laptop { request.setValue(laptop, forHTTPHeaderField: "X-Laptop-ID") }
        request.timeoutInterval = 600
        let (data, response) = try await session.upload(for: request, fromFile: fileURL)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard status == 200 else { throw APIError.http(status: status) }
        return try decoder.decode(VideoUpload.self, from: data)
    }

    public func uploadFile(data: Data, filename: String, laptop: String? = nil) async throws -> String {
        let url = config.baseURL.appendingPathComponent("files")
        var request = authedRequest(url: url, method: "POST")
        request.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
        request.setValue(filename, forHTTPHeaderField: "X-Filename")
        if let laptop { request.setValue(laptop, forHTTPHeaderField: "X-Laptop-ID") }
        request.httpBody = data
        request.timeoutInterval = 60
        let (responseData, _) = try await observedData(for: request)
        let payload = try decoder.decode([String: String].self, from: responseData)
        return payload["path"] ?? ""
    }

    public func fetchSkills() async throws -> [SkillSuggestion] {
        let url = config.baseURL.appendingPathComponent("skills")
        let (data, _) = try await authedData(from: url)
        return try decoder.decode([SkillSuggestion].self, from: data)
    }

    /// Creates a session and returns its id (nil if the server didn't report one).
    @discardableResult
    public func createSession(cwd: String) async throws -> String? {
        let url = config.baseURL.appendingPathComponent("sessions")
        var request = authedRequest(url: url, method: "POST")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")

        struct Body: Encodable { let cwd: String }
        request.httpBody = try JSONEncoder().encode(Body(cwd: cwd))
        let (data, _) = try await observedData(for: request)
        return (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["id"] as? String
    }

    /// Sessions this one has exchanged peer messages with, most recent first (Info → Messages).
    /// Throws `APIError.http(404)` on an older server or a session it doesn't run.
    public func fetchPeers(sessionId: String) async throws -> [PeerSummary] {
        let url = config.baseURL.appendingPathComponent("sessions/\(sessionId)/peers")
        let (data, response) = try await authedData(from: url)
        try Self.requireSuccess(response)
        return try decoder.decode([PeerSummary].self, from: data)
    }

    /// Every message between this session and one peer (Claude session UUID), oldest first.
    public func fetchPeerThread(sessionId: String, peerUuid: String) async throws -> PeerThread {
        let url = config.baseURL.appendingPathComponent("sessions/\(sessionId)/peers/\(peerUuid)/thread")
        let (data, response) = try await authedData(from: url)
        try Self.requireSuccess(response)
        return try decoder.decode(PeerThread.self, from: data)
    }

    /// Daily usage from `from` (yyyy-MM-dd in `timeZone`) through today, for the Usage page.
    public func fetchUsageSummary(from: String, timeZone: TimeZone) async throws -> UsageSummary {
        var components = URLComponents(url: config.baseURL.appendingPathComponent("usage/summary"),
                                       resolvingAgainstBaseURL: false)!
        components.queryItems = [URLQueryItem(name: "tz", value: timeZone.identifier),
                                 URLQueryItem(name: "from", value: from)]
        var request = authedRequest(url: components.url!)
        request.timeoutInterval = 60   // a cold scan of every transcript can take a few seconds
        let (data, response) = try await observedData(for: request)
        try Self.requireSuccess(response)
        return try decoder.decode(UsageSummary.self, from: data)
    }

    /// End a session: the server stops Claude and forgets it (the transcript stays on disk).
    public func endSession(id: String) async throws {
        let url = config.baseURL.appendingPathComponent("sessions/\(id)")
        let request = authedRequest(url: url, method: "DELETE")
        let (_, response) = try await observedData(for: request)
        try Self.requireSuccess(response)
    }

    /// Transcripts on the laptop that aren't open sessions, newest first (Resume sheet).
    public func fetchTranscripts() async throws -> [TranscriptInfo] {
        let url = config.baseURL.appendingPathComponent("transcripts")
        let (data, response) = try await authedData(from: url)
        try Self.requireSuccess(response)
        return try decoder.decode([TranscriptInfo].self, from: data)
    }

    /// Resume a transcript as a session. Returns the session id (an existing one if it's already open).
    /// Throws `APIError.http` on 404 (unknown), 422 (directory missing), 403 (not the owner).
    public func resumeTranscript(id: String) async throws -> String {
        let url = config.baseURL.appendingPathComponent("transcripts/\(id)/resume")
        let request = authedRequest(url: url, method: "POST")
        let (data, response) = try await observedData(for: request)
        try Self.requireSuccess(response)
        guard let sid = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["id"] as? String else {
            throw APIError.sendFailed
        }
        return sid
    }

    // MARK: - MCP servers (docs/plan-mcp-servers.md)
    //
    // Errors are `APIServerError` with the server's `error` text (404 = not available for this
    // session / older server), or a URLError on a network failure.

    /// Status of every MCP server in a session's Claude process. 409: Claude isn't running.
    public func fetchSessionMcp(sessionId: String) async throws -> SessionMcpList {
        try await mcpCall("sessions/\(sessionId)/mcp")
    }

    /// Reconnect one server, or every server that isn't disabled (`name == nil`). Returns the fresh list.
    public func reconnectMcp(sessionId: String, name: String?) async throws -> SessionMcpList {
        var body: [String: JSONValue] = [:]
        if let name { body["name"] = .string(name) }
        return try await mcpCall("sessions/\(sessionId)/mcp/reconnect", method: "POST", body: body)
    }

    /// Enable or disable a server for the session's project (every session in that cwd).
    public func toggleMcp(sessionId: String, name: String, enabled: Bool) async throws -> SessionMcpList {
        try await mcpCall("sessions/\(sessionId)/mcp/toggle", method: "POST",
                          body: ["name": .string(name), "enabled": .bool(enabled)])
    }

    /// Start an OAuth sign-in in this session's process.
    public func startMcpAuth(sessionId: String, name: String) async throws -> McpAuthStart {
        try await mcpCall("sessions/\(sessionId)/mcp/auth", method: "POST", body: ["name": .string(name)])
    }

    /// Hand the provider's localhost callback URL to Claude. 400: Claude rejected it.
    public func finishMcpAuth(sessionId: String, name: String, callbackUrl: String) async throws -> SessionMcpList {
        try await mcpCall("sessions/\(sessionId)/mcp/auth/callback", method: "POST",
                          body: ["name": .string(name), "callbackUrl": .string(callbackUrl)])
    }

    /// Restart this session's Claude process (`--resume`). 409 while a turn runs / a prompt is pending.
    public func restartSession(id: String) async throws {
        _ = try await mcpData("sessions/\(id)/restart", method: "POST")
    }

    /// User-level MCP config plus each server's status across sessions. 403: not the laptop owner.
    public func fetchMcp() async throws -> GlobalMcpList {
        try await mcpCall("mcp")
    }

    /// Add a user-scope server. 400: bad name / CLI failure; 409: name taken.
    public func addMcpServer(name: String, config: [String: JSONValue]) async throws -> GlobalMcpList {
        try await mcpCall("mcp/servers", method: "POST", body: ["name": .string(name), "config": .object(config)])
    }

    /// Replace the submitted keys of a user-scope server's config. Returns the fresh list when the
    /// server sends one (nil: reload it).
    public func updateMcpServer(name: String, config: [String: JSONValue]) async throws -> GlobalMcpList? {
        let data = try await mcpData("mcp/servers/\(name)", method: "PUT", body: ["config": .object(config)])
        return try? decoder.decode(GlobalMcpList.self, from: data)
    }

    /// Remove a user-scope server. Returns the fresh list when the server sends one (nil: reload it).
    public func deleteMcpServer(name: String) async throws -> GlobalMcpList? {
        let data = try await mcpData("mcp/servers/\(name)", method: "DELETE")
        return try? decoder.decode(GlobalMcpList.self, from: data)
    }

    /// Restart every managed session's Claude process (idle now, working ones after their turn).
    public func restartAllSessions() async throws -> McpRestartAllResult {
        try await mcpCall("mcp/restart-all", method: "POST")
    }

    private func mcpCall<T: Decodable>(_ path: String, method: String = "GET",
                                       body: [String: JSONValue]? = nil) async throws -> T {
        let data = try await mcpData(path, method: method, body: body)
        return try decoder.decode(T.self, from: data)
    }

    /// Send an MCP request; throws `APIServerError` (with the server's `error` text) on non-2xx.
    private func mcpData(_ path: String, method: String = "GET", body: [String: JSONValue]? = nil) async throws -> Data {
        var request = authedRequest(url: config.baseURL.appendingPathComponent(path), method: method)
        // Claude's control requests take up to 15 s server-side; GET /mcp queries every session.
        request.timeoutInterval = 30
        if let body {
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try JSONEncoder().encode(body)
        }
        let (data, response) = try await observedData(for: request)
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            let payload = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
            let message = (payload?["error"] as? String).flatMap { $0.isEmpty ? nil : $0 }
            throw APIServerError(status: http.statusCode, message: message)
        }
        return data
    }

    public struct PlanResponse: Codable, Sendable {
        public let plan: String?
        public let fileName: String?
    }

    public func fetchPlan(id: String) async throws -> PlanResponse {
        let url = config.baseURL.appendingPathComponent("sessions/\(id)/plan")
        let (data, _) = try await authedData(from: url)
        return try decoder.decode(PlanResponse.self, from: data)
    }

    public func fetchEvents(id: String) async throws -> [SessionEvent] {
        let url = config.baseURL.appendingPathComponent("sessions/\(id)/events")
        let (data, _) = try await authedData(from: url)
        return try decoder.decode([SessionEvent].self, from: data)
    }

    public struct HistoryPage: Decodable, Sendable {
        public let events: [SessionEvent]
        public let hasMore: Bool
    }

    /// Events older than `before` (the oldest loaded event), straight from the transcript on disk.
    /// Matched by uuid, falling back to its timestamp. With no cursor, the newest `limit` events.
    public func fetchHistory(id: String, before: SessionEvent?, limit: Int) async throws -> HistoryPage {
        var comps = URLComponents(url: config.baseURL.appendingPathComponent("sessions/\(id)/history"),
                                  resolvingAgainstBaseURL: false)!
        var items = [URLQueryItem(name: "limit", value: String(limit))]
        if let before {
            items.append(URLQueryItem(name: "before", value: before.uuid))
            items.append(URLQueryItem(name: "beforeTs", value: before.timestamp))
        }
        comps.queryItems = items
        let (data, _) = try await authedData(from: comps.url!)
        return try decoder.decode(HistoryPage.self, from: data)
    }

    public func fetchProjects() async throws -> [ProjectInfo] {
        let url = config.baseURL.appendingPathComponent("projects")
        let (data, _) = try await authedData(from: url)
        return try decoder.decode([ProjectInfo].self, from: data)
    }

    public func healthCheck() async -> Bool {
        let url = config.baseURL.appendingPathComponent("health")
        do {
            let (_, response) = try await authedData(from: url)
            return (response as? HTTPURLResponse)?.statusCode == 200
        } catch {
            return false
        }
    }
}
