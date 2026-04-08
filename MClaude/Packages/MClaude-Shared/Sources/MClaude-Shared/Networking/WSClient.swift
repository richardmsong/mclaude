import Foundation

/// WebSocket client that receives real-time session and output updates.
public final class WSClient: Sendable {
    private let config: ServerConfig
    private let onSessions: @Sendable ([ClaudeSession]) -> Void
    private let onOutput: @Sendable (String, String) -> Void
    private let onFullOutput: @Sendable (String, String) -> Void
    private let onConnectionChange: @Sendable (Bool, String?) -> Void  // connected, error reason
    private let onEvent: @Sendable (String, SessionEvent) -> Void

    private let urlSession = URLSession(configuration: .default)
    private let taskHolder = TaskHolder()
    private let wsHolder = WSTaskHolder()

    private actor TaskHolder {
        var task: Task<Void, Never>?

        func setTask(_ t: Task<Void, Never>?) {
            task?.cancel()
            task = t
        }

        func cancel() {
            task?.cancel()
            task = nil
        }
    }

    private actor WSTaskHolder {
        var wsTask: URLSessionWebSocketTask?

        func set(_ task: URLSessionWebSocketTask?) {
            wsTask = task
        }

        func send(_ message: URLSessionWebSocketTask.Message) async throws {
            try await wsTask?.send(message)
        }
    }

    public init(
        config: ServerConfig,
        onSessions: @escaping @Sendable ([ClaudeSession]) -> Void,
        onOutput: @escaping @Sendable (String, String) -> Void,
        onFullOutput: @escaping @Sendable (String, String) -> Void,
        onConnectionChange: @escaping @Sendable (Bool, String?) -> Void,
        onEvent: @escaping @Sendable (String, SessionEvent) -> Void = { _, _ in }
    ) {
        self.config = config
        self.onSessions = onSessions
        self.onOutput = onOutput
        self.onFullOutput = onFullOutput
        self.onConnectionChange = onConnectionChange
        self.onEvent = onEvent
    }

    public func connect() {
        Task {
            await taskHolder.setTask(Task { [weak self] in
                guard let self else { return }
                await self.runLoop()
            })
        }
    }

    public func disconnect() {
        Task { await taskHolder.cancel() }
    }

    public func requestMoreOutput(id: String, lines: Int) {
        Task {
            let cmd = "{\"type\":\"load_more\",\"id\":\"\(id)\",\"lines\":\(lines)}"
            try? await wsHolder.send(.string(cmd))
        }
    }

    private func runLoop() async {
        var retryCount = 0
        while !Task.isCancelled {
            do {
                retryCount = 0
                try await connectAndListen()
            } catch let error as URLError {
                if Task.isCancelled { break }
                let reason: String
                switch error.code {
                case .cannotConnectToHost:
                    reason = "Cannot connect to \(config.host):\(config.port)"
                case .timedOut:
                    reason = "Connection timed out"
                case .networkConnectionLost:
                    reason = "Network connection lost"
                case .notConnectedToInternet:
                    reason = "No internet connection"
                case .dnsLookupFailed:
                    reason = "DNS lookup failed for \(config.host)"
                default:
                    reason = "URLError \(error.code.rawValue): \(error.localizedDescription)"
                }
                onConnectionChange(false, reason)
            } catch is CancellationError {
                break
            } catch let error as NSError {
                if Task.isCancelled { break }
                // POSIXError 57 = socket not connected (common on background wake)
                if error.domain == NSPOSIXErrorDomain && error.code == 57 {
                    onConnectionChange(false, "Socket closed (app was backgrounded)")
                } else {
                    onConnectionChange(false, "\(error.domain) \(error.code): \(error.localizedDescription)")
                }
            } catch {
                if Task.isCancelled { break }
                onConnectionChange(false, String(describing: error))
            }
            await wsHolder.set(nil)
            retryCount += 1
            let delay = min(retryCount, 5) // cap at 5s
            try? await Task.sleep(nanoseconds: UInt64(delay) * 1_000_000_000)
        }
    }

    private func connectAndListen() async throws {
        var components = URLComponents()
        components.scheme = "ws"
        components.host = config.host
        components.port = config.port
        components.path = "/ws"
        if let token = config.token {
            components.queryItems = [URLQueryItem(name: "token", value: token)]
        }

        guard let url = components.url else {
            onConnectionChange(false, "Invalid URL: \(config.host):\(config.port)")
            return
        }

        let wsTask = urlSession.webSocketTask(with: url)
        wsTask.resume()
        await wsHolder.set(wsTask)

        onConnectionChange(true, nil)

        // Keepalive ping every 15s
        let pingTask = Task {
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 15_000_000_000)
                wsTask.sendPing { error in
                    if let error {
                        wsTask.cancel(with: .goingAway, reason: "Ping failed: \(error.localizedDescription)".data(using: .utf8))
                    }
                }
            }
        }
        defer { pingTask.cancel() }

        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601

        while !Task.isCancelled {
            let message = try await wsTask.receive()

            switch message {
            case .string(let text):
                guard let data = text.data(using: .utf8) else { continue }

                struct WSMessage: Codable {
                    let type: String
                    let data: String
                }

                guard let msg = try? decoder.decode(WSMessage.self, from: data),
                      let payload = msg.data.data(using: .utf8) else { continue }

                switch msg.type {
                case "sessions":
                    if let sessions = try? decoder.decode([ClaudeSession].self, from: payload) {
                        onSessions(sessions)
                    }
                case "output":
                    if let dict = try? decoder.decode([String: String].self, from: payload),
                       let id = dict["id"], let content = dict["output"] {
                        onOutput(id, content)
                    }
                case "more_output":
                    if let dict = try? decoder.decode([String: String].self, from: payload),
                       let id = dict["id"], let content = dict["output"] {
                        onFullOutput(id, content)
                    }
                case "event":
                    // payload is: {"id":"...","event":{...}}
                    struct EventEnvelope: Codable {
                        let id: String
                        let event: SessionEvent
                    }
                    if let envelope = try? decoder.decode(EventEnvelope.self, from: payload) {
                        onEvent(envelope.id, envelope.event)
                    }
                default:
                    break
                }

            case .data:
                break

            @unknown default:
                break
            }
        }

        wsTask.cancel(with: .goingAway, reason: nil)
    }
}
