import Foundation
import CryptoKit

/// Manages user authentication and session ownership.
/// Users are auto-registered on first connection with a valid Claude Code OAuth token.
/// Session ownership is tracked by tagging tmux windows.
actor SessionStore {
    private let usersPath: String
    private let ownersPath: String
    private var users: [String: MCUser] = [:]  // tokenHash -> user
    private var sessionOwners: [String: String] = [:]  // sessionDisplayId -> userId

    /// The server owner's userId — sessions without a token belong to this user
    private(set) var ownerUserId: String = "owner"

    init(
        usersPath: String = "\(NSHomeDirectory())/mclaude-users.json",
        ownersPath: String = "\(NSHomeDirectory())/mclaude-session-owners.json"
    ) {
        self.usersPath = usersPath
        self.ownersPath = ownersPath
        loadUsers()
        loadOwners()
    }

    // MARK: - Token hashing

    nonisolated func hashToken(_ token: String) -> String {
        let data = Data(token.utf8)
        let hash = SHA256.hash(data: data)
        return hash.map { String(format: "%02x", $0) }.joined()
    }

    // MARK: - User management

    /// Authenticate a token. Returns userId if valid, auto-registers if new.
    func authenticate(token: String) -> String {
        let hash = hashToken(token)
        if let user = users[hash] {
            return user.id
        }
        // Auto-register new user
        let userId = UUID().uuidString.prefix(8).lowercased()
        let user = MCUser(
            id: String(userId),
            name: "User \(userId)",
            tokenHash: hash,
            createdAt: Date()
        )
        users[hash] = user
        saveUsers()
        print("[auth] Auto-registered new user: \(user.id)")
        return user.id
    }

    /// Get userId for a token without registering
    func getUserId(token: String) -> String? {
        let hash = hashToken(token)
        return users[hash]?.id
    }

    // MARK: - Session ownership

    func setOwner(sessionId: String, userId: String) {
        sessionOwners[sessionId] = userId
        saveOwners()
    }

    func getOwner(sessionId: String) -> String? {
        return sessionOwners[sessionId]
    }

    func removeOwner(sessionId: String) {
        sessionOwners.removeValue(forKey: sessionId)
        saveOwners()
    }

    /// Filter sessions to only those owned by userId.
    /// Sessions without an owner are assumed to belong to the server owner.
    func filterSessions(_ sessions: [ClaudeSession], userId: String?) -> [ClaudeSession] {
        guard let userId else { return sessions }
        return sessions.filter { session in
            let owner = sessionOwners[session.id] ?? ownerUserId
            return owner == userId
        }
    }

    /// Check if a user owns a specific session
    func userOwns(sessionId: String, userId: String?) -> Bool {
        guard let userId else { return true }  // no auth = owner access
        let owner = sessionOwners[sessionId] ?? ownerUserId
        return owner == userId
    }

    // MARK: - Persistence

    private func loadUsers() {
        guard let data = FileManager.default.contents(atPath: usersPath),
              let decoded = try? JSONDecoder.withISO8601.decode([String: MCUser].self, from: data) else { return }
        users = decoded
        print("[auth] Loaded \(users.count) users")
    }

    private func saveUsers() {
        guard let data = try? JSONEncoder.withISO8601.encode(users) else { return }
        try? data.write(to: URL(fileURLWithPath: usersPath))
    }

    private func loadOwners() {
        guard let data = FileManager.default.contents(atPath: ownersPath),
              let decoded = try? JSONDecoder().decode([String: String].self, from: data) else { return }
        sessionOwners = decoded
        print("[auth] Loaded \(sessionOwners.count) session owners")
    }

    private func saveOwners() {
        guard let data = try? JSONEncoder().encode(sessionOwners) else { return }
        try? data.write(to: URL(fileURLWithPath: ownersPath))
    }
}

// MARK: - JSON coding helpers

private extension JSONEncoder {
    static let withISO8601: JSONEncoder = {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        return e
    }()
}

private extension JSONDecoder {
    static let withISO8601: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }()
}
