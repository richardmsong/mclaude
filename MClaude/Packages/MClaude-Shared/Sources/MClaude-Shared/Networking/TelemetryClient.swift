import Foundation

/// Queues error reports and retries sending until the server acknowledges.
public actor TelemetryClient {
    private let config: ServerConfig
    private var queue: [TelemetryEvent] = []
    private var isFlushing = false

    public struct TelemetryEvent: Codable, Sendable {
        public let timestamp: String
        public let error: String
        public let context: String
    }

    public init(config: ServerConfig) {
        self.config = config
    }

    public func report(error: String, context: String = "") {
        let formatter = ISO8601DateFormatter()
        let event = TelemetryEvent(
            timestamp: formatter.string(from: Date()),
            error: error,
            context: context
        )
        queue.append(event)
        if !isFlushing {
            Task { await flush() }
        }
    }

    private func flush() {
        isFlushing = true
        Task {
            while !queue.isEmpty {
                let event = queue[0]
                if await send(event) {
                    queue.removeFirst()
                } else {
                    // Retry after 10s
                    try? await Task.sleep(nanoseconds: 10_000_000_000)
                }
            }
            isFlushing = false
        }
    }

    private func send(_ event: TelemetryEvent) async -> Bool {
        guard let url = config.url(path: "/telemetry") else { return false }
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try? JSONEncoder().encode(event)
        request.timeoutInterval = 10

        do {
            let (_, response) = try await URLSession.shared.data(for: request)
            return (response as? HTTPURLResponse)?.statusCode == 200
        } catch {
            return false
        }
    }
}
