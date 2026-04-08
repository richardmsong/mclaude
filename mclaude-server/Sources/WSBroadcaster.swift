import Foundation

/// Manages WebSocket message broadcasting to connected clients.
actor WSBroadcaster {
    private struct ClientInfo {
        let send: @Sendable (String) async -> Void
        let userId: String?  // nil = server owner (sees all)
    }

    private var clients: [UUID: ClientInfo] = [:]

    func addClient(id: UUID, userId: String? = nil, send: @escaping @Sendable (String) async -> Void) {
        clients[id] = ClientInfo(send: send, userId: userId)
    }

    func removeClient(id: UUID) {
        clients.removeValue(forKey: id)
    }

    /// Broadcast to all clients (no filtering)
    func broadcast(_ message: String) async {
        for (id, client) in clients {
            do {
                try await withTimeout(seconds: 2) {
                    await client.send(message)
                }
            } catch {
                clients.removeValue(forKey: id)
            }
        }
    }

    /// Broadcast a per-user message. The builder receives the client's userId (nil for owner)
    /// and returns the message to send, or nil to skip that client.
    func broadcastFiltered(_ builder: (String?) async -> String?) async {
        for (id, client) in clients {
            guard let message = await builder(client.userId) else { continue }
            do {
                try await withTimeout(seconds: 2) {
                    await client.send(message)
                }
            } catch {
                clients.removeValue(forKey: id)
            }
        }
    }

    var clientCount: Int { clients.count }

    private func withTimeout(seconds: Double, operation: @escaping @Sendable () async -> Void) async throws {
        try await withThrowingTaskGroup(of: Void.self) { group in
            group.addTask { await operation() }
            group.addTask {
                try await Task.sleep(nanoseconds: UInt64(seconds * 1_000_000_000))
                throw CancellationError()
            }
            try await group.next()
            group.cancelAll()
        }
    }
}

/// WebSocket message types sent to clients
struct WSMessage: Codable {
    let type: String       // "sessions", "output", "status_change"
    let data: String       // JSON-encoded payload
}

func encodeWSMessage(type: String, data: some Encodable) -> String? {
    let encoder = JSONEncoder()
    encoder.dateEncodingStrategy = .iso8601
    guard let payload = try? encoder.encode(data) else { return nil }
    let msg = WSMessage(type: type, data: String(data: payload, encoding: .utf8) ?? "")
    guard let msgData = try? JSONEncoder().encode(msg) else { return nil }
    return String(data: msgData, encoding: .utf8)
}

func encodeWSMessage(type: String, rawData: String) -> String? {
    let msg = WSMessage(type: type, data: rawData)
    guard let msgData = try? JSONEncoder().encode(msg) else { return nil }
    return String(data: msgData, encoding: .utf8)
}
