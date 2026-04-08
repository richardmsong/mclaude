import Foundation
import CryptoKit

/// Manages user authentication via Google SSO and session ownership.
/// Identity is tied to Google account ID (stable across token rotations/device switches).
/// Session tokens are issued after Google auth and used as Bearer tokens.
actor SessionStore {
    private let usersPath: String
    private let ownersPath: String
    private let configPath: String
    private let sessionTokensPath: String

    // googleId -> MCUser
    private var users: [String: MCUser] = [:]
    // legacy: tokenHash -> MCUser (for backward compat during migration)
    private var legacyUsers: [String: MCUser] = [:]
    // sessionDisplayId -> userId
    private var sessionOwners: [String: String] = [:]
    // sessionToken -> userId (for Bearer auth)
    private var sessionTokens: [String: String] = [:]

    /// The server owner's userId — sessions without an explicit owner belong to this user.
    private(set) var ownerUserId: String = "owner"

    // Google OAuth config
    private(set) var googleClientId: String = ""
    private(set) var googleClientSecret: String = ""
    private(set) var googleRedirectUri: String = ""

    init(
        usersPath: String = "\(NSHomeDirectory())/mclaude-users.json",
        ownersPath: String = "\(NSHomeDirectory())/mclaude-session-owners.json",
        configPath: String = "\(NSHomeDirectory())/mclaude-config.json",
        sessionTokensPath: String = "\(NSHomeDirectory())/mclaude-session-tokens.json"
    ) {
        self.usersPath = usersPath
        self.ownersPath = ownersPath
        self.configPath = configPath
        self.sessionTokensPath = sessionTokensPath
        loadConfig()
        loadUsers()
        loadOwners()
        loadSessionTokens()
    }

    // MARK: - Token hashing

    nonisolated func hashToken(_ token: String) -> String {
        let data = Data(token.utf8)
        let hash = SHA256.hash(data: data)
        return hash.map { String(format: "%02x", $0) }.joined()
    }

    // MARK: - Google OAuth

    /// Build the Google OAuth authorization URL
    func googleAuthURL(state: String? = nil) -> String {
        var components = URLComponents(string: "https://accounts.google.com/o/oauth2/v2/auth")!
        var items = [
            URLQueryItem(name: "client_id", value: googleClientId),
            URLQueryItem(name: "redirect_uri", value: googleRedirectUri),
            URLQueryItem(name: "response_type", value: "code"),
            URLQueryItem(name: "scope", value: "openid email profile"),
            URLQueryItem(name: "access_type", value: "offline"),
            URLQueryItem(name: "prompt", value: "select_account"),
        ]
        if let state {
            items.append(URLQueryItem(name: "state", value: state))
        }
        components.queryItems = items
        return components.url!.absoluteString
    }

    /// Exchange Google auth code for tokens and user info.
    /// Returns (userId, sessionToken, userName, email) on success.
    nonisolated func exchangeGoogleCode(_ code: String) async -> (googleId: String, email: String, name: String)? {
        // Read config (these are set at init, safe to access via method)
        let clientId = await self.googleClientId
        let clientSecret = await self.googleClientSecret
        let redirectUri = await self.googleRedirectUri

        // Exchange code for tokens
        var request = URLRequest(url: URL(string: "https://oauth2.googleapis.com/token")!)
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        let body = [
            "code=\(code)",
            "client_id=\(clientId)",
            "client_secret=\(clientSecret)",
            "redirect_uri=\(redirectUri)",
            "grant_type=authorization_code"
        ].joined(separator: "&")
        request.httpBody = body.data(using: .utf8)

        guard let (data, _) = try? await URLSession.shared.data(for: request),
              let tokenResponse = try? JSONDecoder().decode(GoogleTokenResponse.self, from: data) else {
            print("[auth] Google token exchange failed")
            return nil
        }

        // Decode the ID token (JWT) to get user info
        let parts = tokenResponse.id_token.split(separator: ".")
        guard parts.count >= 2 else {
            print("[auth] Invalid Google ID token format")
            return nil
        }

        // Base64URL decode the payload
        var payload = String(parts[1])
        // Pad to multiple of 4
        while payload.count % 4 != 0 { payload += "=" }
        payload = payload.replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/")

        guard let payloadData = Data(base64Encoded: payload),
              let userInfo = try? JSONDecoder().decode(GoogleUserInfo.self, from: payloadData) else {
            print("[auth] Failed to decode Google ID token payload")
            return nil
        }

        return (googleId: userInfo.sub, email: userInfo.email, name: userInfo.name ?? userInfo.email)
    }

    /// Register or find a user by Google identity. Returns (userId, sessionToken).
    func authenticateGoogle(googleId: String, email: String, name: String) -> (userId: String, sessionToken: String) {
        let user: MCUser
        if let existing = users[googleId] {
            user = existing
            print("[auth] Google login: \(email) -> \(existing.id)")
        } else {
            // Check if there's a legacy user to migrate
            let userId = UUID().uuidString.prefix(8).lowercased()
            user = MCUser(
                id: String(userId),
                name: name,
                email: email,
                googleId: googleId,
                tokenHash: nil,
                createdAt: Date()
            )
            users[googleId] = user
            saveUsers()
            print("[auth] Google registered new user: \(user.id) (\(email))")
        }

        // Set owner to first authenticated user
        if ownerUserId == "owner" {
            ownerUserId = user.id
            saveConfig()
            print("[auth] Server owner set to: \(user.id)")
        }

        // Generate session token
        let sessionToken = generateSessionToken()
        sessionTokens[sessionToken] = user.id
        saveSessionTokens()

        return (userId: user.id, sessionToken: sessionToken)
    }

    /// Authenticate a session token (Bearer). Returns userId if valid.
    func authenticateSessionToken(_ token: String) -> String? {
        return sessionTokens[token]
    }

    // MARK: - Legacy token auth (backward compat)

    /// Authenticate a Claude OAuth token. Returns userId if valid.
    func authenticate(token: String) -> String {
        // First check if it's a session token from Google auth
        if let userId = sessionTokens[token] {
            return userId
        }
        // Legacy: Claude OAuth token
        let hash = hashToken(token)
        if let user = legacyUsers[hash] {
            if ownerUserId == "owner" {
                ownerUserId = user.id
                saveConfig()
                print("[auth] Server owner set to: \(user.id)")
            }
            return user.id
        }
        // Auto-register legacy user
        let userId = UUID().uuidString.prefix(8).lowercased()
        let user = MCUser(
            id: String(userId),
            name: "User \(userId)",
            email: nil,
            googleId: nil,
            tokenHash: hash,
            createdAt: Date()
        )
        legacyUsers[hash] = user
        saveUsers()
        if ownerUserId == "owner" {
            ownerUserId = user.id
            saveConfig()
            print("[auth] Server owner set to: \(user.id)")
        }
        print("[auth] Auto-registered legacy user: \(user.id)")
        return user.id
    }

    /// Get userId for a token without registering
    func getUserId(token: String) -> String? {
        if let userId = sessionTokens[token] { return userId }
        let hash = hashToken(token)
        return legacyUsers[hash]?.id
    }

    /// Get user by userId
    func getUser(userId: String) -> MCUser? {
        return users.values.first { $0.id == userId }
            ?? legacyUsers.values.first { $0.id == userId }
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
    /// Unowned sessions belong to the server owner.
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

    // MARK: - Session token generation

    private func generateSessionToken() -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        _ = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        return "mcs_" + bytes.map { String(format: "%02x", $0) }.joined()
    }

    // MARK: - Persistence

    private func loadConfig() {
        guard let data = FileManager.default.contents(atPath: configPath),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return }
        if let id = json["googleClientId"] as? String { googleClientId = id }
        if let secret = json["googleClientSecret"] as? String { googleClientSecret = secret }
        if let uri = json["googleRedirectUri"] as? String { googleRedirectUri = uri }
        if let owner = json["ownerUserId"] as? String { ownerUserId = owner }
        print("[auth] Loaded config (Google client: \(googleClientId.prefix(20))..., owner: \(ownerUserId))")
    }

    private func saveConfig() {
        let config: [String: String] = [
            "googleClientId": googleClientId,
            "googleClientSecret": googleClientSecret,
            "googleRedirectUri": googleRedirectUri,
            "ownerUserId": ownerUserId,
        ]
        guard let data = try? JSONSerialization.data(withJSONObject: config, options: .prettyPrinted) else { return }
        try? data.write(to: URL(fileURLWithPath: configPath))
    }

    private func loadUsers() {
        guard let data = FileManager.default.contents(atPath: usersPath),
              let decoded = try? JSONDecoder.withISO8601.decode([String: MCUser].self, from: data) else { return }
        // Split into Google users and legacy users
        for (key, user) in decoded {
            if user.googleId != nil {
                users[key] = user
            } else {
                legacyUsers[key] = user
            }
        }
        print("[auth] Loaded \(users.count) Google users, \(legacyUsers.count) legacy users")
    }

    private func saveUsers() {
        var all: [String: MCUser] = [:]
        for (k, v) in users { all[k] = v }
        for (k, v) in legacyUsers { all[k] = v }
        guard let data = try? JSONEncoder.withISO8601.encode(all) else { return }
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

    private func loadSessionTokens() {
        guard let data = FileManager.default.contents(atPath: sessionTokensPath),
              let decoded = try? JSONDecoder().decode([String: String].self, from: data) else { return }
        sessionTokens = decoded
        print("[auth] Loaded \(sessionTokens.count) session tokens")
    }

    private func saveSessionTokens() {
        guard let data = try? JSONEncoder().encode(sessionTokens) else { return }
        try? data.write(to: URL(fileURLWithPath: sessionTokensPath))
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
