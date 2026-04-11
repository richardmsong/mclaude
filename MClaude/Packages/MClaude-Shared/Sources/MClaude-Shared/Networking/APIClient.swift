import Foundation

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

    /// GET with auth
    private func authedData(from url: URL) async throws -> (Data, URLResponse) {
        let request = authedRequest(url: url)
        return try await session.data(for: request)
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

    public func sendInput(id: String, text: String, sendEnter: Bool = true) async throws {
        let url = config.baseURL.appendingPathComponent("sessions/\(id)/input")
        var request = authedRequest(url: url, method: "POST")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")

        struct InputBody: Encodable {
            let text: String
            let sendEnter: Bool
        }
        request.httpBody = try JSONEncoder().encode(InputBody(text: text, sendEnter: sendEnter))
        let (_, _) = try await session.data(for: request)
    }

    public func approve(id: String) async throws {
        let url = config.baseURL.appendingPathComponent("sessions/\(id)/approve")
        let request = authedRequest(url: url, method: "POST")
        let (_, _) = try await session.data(for: request)
    }

    public func cancel(id: String) async throws {
        let url = config.baseURL.appendingPathComponent("sessions/\(id)/cancel")
        let request = authedRequest(url: url, method: "POST")
        let (_, _) = try await session.data(for: request)
    }

    public func uploadScreenshot(imageData: Data, laptop: String? = nil) async throws -> String {
        let url = config.baseURL.appendingPathComponent("screenshots")
        var request = authedRequest(url: url, method: "POST")
        request.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
        if let laptop { request.setValue(laptop, forHTTPHeaderField: "X-Laptop-ID") }
        request.httpBody = imageData
        request.timeoutInterval = 30
        let (data, _) = try await session.data(for: request)
        let payload = try decoder.decode([String: String].self, from: data)
        return payload["path"] ?? ""
    }

    public func uploadFile(data: Data, filename: String, laptop: String? = nil) async throws -> String {
        let url = config.baseURL.appendingPathComponent("files")
        var request = authedRequest(url: url, method: "POST")
        request.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
        request.setValue(filename, forHTTPHeaderField: "X-Filename")
        if let laptop { request.setValue(laptop, forHTTPHeaderField: "X-Laptop-ID") }
        request.httpBody = data
        request.timeoutInterval = 60
        let (responseData, _) = try await session.data(for: request)
        let payload = try decoder.decode([String: String].self, from: responseData)
        return payload["path"] ?? ""
    }

    public func fetchSkills() async throws -> [SkillSuggestion] {
        let url = config.baseURL.appendingPathComponent("skills")
        let (data, _) = try await authedData(from: url)
        return try decoder.decode([SkillSuggestion].self, from: data)
    }

    public func createSession(cwd: String, runtime: String = "tmux") async throws {
        let url = config.baseURL.appendingPathComponent("sessions")
        var request = authedRequest(url: url, method: "POST")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")

        struct Body: Encodable { let cwd: String; let runtime: String }
        request.httpBody = try JSONEncoder().encode(Body(cwd: cwd, runtime: runtime))
        let (_, _) = try await session.data(for: request)
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
