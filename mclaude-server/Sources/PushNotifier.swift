import Foundation
import CryptoKit

/// Apple Push Notification service client (token-based auth). Sends "your long turn finished"
/// notifications to the iPhones the app registered.
///
/// Config: ~/.mclaude/apns/config.json
///   {"keyId": "L9T69BWT57", "teamId": "Y63FV5M5D9", "topic": "com.rsong.mclaude.app",
///    "keyPath": "~/.mclaude/apns/AuthKey_L9T69BWT57.p8", "minTurnSeconds": 300}
/// Devices: ~/.mclaude/apns/devices.json (written by POST /devices).
actor PushNotifier {
    struct Config: Codable {
        var keyId: String
        var teamId: String
        var topic: String
        var keyPath: String
        var minTurnSeconds: Int?
    }

    struct Device: Codable, Equatable {
        var token: String
        var environment: String      // "production" (TestFlight/App Store) or "sandbox" (Xcode builds)
        var userId: String?
        var registeredAt: Date
    }

    private let dir: String
    private var config: Config?
    private var key: P256.Signing.PrivateKey?
    private var devices: [Device] = []
    private var cachedJWT: (token: String, issuedAt: Date)?

    init(dir: String = NSHomeDirectory() + "/.mclaude/apns") {
        self.dir = dir
    }

    var minTurnSeconds: Int { config?.minTurnSeconds ?? 300 }
    var isConfigured: Bool { config != nil && key != nil }

    func load() {
        let fm = FileManager.default
        if let data = fm.contents(atPath: dir + "/config.json"),
           let cfg = try? JSONDecoder().decode(Config.self, from: data) {
            let path = (cfg.keyPath as NSString).expandingTildeInPath
            if let pem = try? String(contentsOfFile: path, encoding: .utf8),
               let k = try? P256.Signing.PrivateKey(pemRepresentation: pem) {
                config = cfg
                key = k
            } else {
                print("[push] can't read APNs key at \(path); push disabled")
            }
        } else {
            print("[push] no \(dir)/config.json; push disabled")
        }
        if let data = fm.contents(atPath: dir + "/devices.json"),
           let list = try? JSONDecoder.iso.decode([Device].self, from: data) {
            devices = list
        }
        if isConfigured { print("[push] APNs ready (key \(config!.keyId), \(devices.count) device(s), min turn \(minTurnSeconds)s)") }
    }

    func register(token: String, environment: String, userId: String?) {
        let token = token.lowercased().filter { $0.isHexDigit }
        guard !token.isEmpty else { return }
        let env = environment == "sandbox" ? "sandbox" : "production"
        devices.removeAll { $0.token == token }
        devices.append(Device(token: token, environment: env, userId: userId, registeredAt: Date()))
        save()
        print("[push] registered device …\(token.suffix(8)) (\(env))")
    }

    private func save() {
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        guard let data = try? JSONEncoder.iso.encode(devices) else { return }
        let path = dir + "/devices.json"
        FileManager.default.createFile(atPath: path, contents: data, attributes: [.posixPermissions: 0o600])
    }

    /// Notify every registered device (optionally only the session owner's).
    func notify(title: String, body: String, sessionId: String, userId: String?) async {
        guard isConfigured else { return }
        let targets = devices.filter { userId == nil || $0.userId == nil || $0.userId == userId || userId == "owner" }
        for device in targets {
            await send(to: device, title: title, body: body, sessionId: sessionId)   // prunes dead tokens
        }
    }

    /// Returns the HTTP status (0 on transport error).
    @discardableResult
    func send(to device: Device, title: String, body: String, sessionId: String) async -> Int {
        guard let config, let jwt = authToken() else { return 0 }
        let host = device.environment == "sandbox" ? "api.sandbox.push.apple.com" : "api.push.apple.com"
        guard let url = URL(string: "https://\(host)/3/device/\(device.token)") else { return 0 }
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("bearer \(jwt)", forHTTPHeaderField: "authorization")
        request.setValue(config.topic, forHTTPHeaderField: "apns-topic")
        request.setValue("alert", forHTTPHeaderField: "apns-push-type")
        request.setValue("10", forHTTPHeaderField: "apns-priority")
        request.setValue("mclaude-\(sessionId)", forHTTPHeaderField: "apns-collapse-id")
        let payload: [String: Any] = [
            "aps": ["alert": ["title": title, "body": body], "sound": "default", "thread-id": sessionId],
            "sessionId": sessionId,
        ]
        request.httpBody = try? JSONSerialization.data(withJSONObject: payload)
        do {
            let (data, response) = try await URLSession.shared.data(for: request)
            let status = (response as? HTTPURLResponse)?.statusCode ?? 0
            if status != 200 {
                let reason = String(data: data, encoding: .utf8) ?? ""
                print("[push] APNs \(status) for …\(device.token.suffix(8)): \(reason)")
                if status == 410 || reason.contains("BadDeviceToken") || reason.contains("Unregistered") {
                    devices.removeAll { $0.token == device.token }
                    save()
                }
            } else {
                print("[push] sent to …\(device.token.suffix(8)): \(title)")
            }
            return status
        } catch {
            print("[push] APNs error: \(error.localizedDescription)")
            return 0
        }
    }

    /// ES256 provider token; Apple wants it refreshed between 20 and 60 minutes.
    private func authToken() -> String? {
        if let cached = cachedJWT, Date().timeIntervalSince(cached.issuedAt) < 40 * 60 { return cached.token }
        guard let config, let key else { return nil }
        func b64(_ d: Data) -> String {
            d.base64EncodedString().replacingOccurrences(of: "+", with: "-")
                .replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
        }
        let now = Date()
        let header = b64(try! JSONSerialization.data(withJSONObject: ["alg": "ES256", "kid": config.keyId]))
        let claims = b64(try! JSONSerialization.data(withJSONObject: ["iss": config.teamId, "iat": Int(now.timeIntervalSince1970)]))
        let signingInput = "\(header).\(claims)"
        guard let signature = try? key.signature(for: Data(signingInput.utf8)) else { return nil }
        let token = "\(signingInput).\(b64(signature.rawRepresentation))"
        cachedJWT = (token, now)
        return token
    }

    /// Plain-ish text for a notification body: drop markdown markers, collapse whitespace.
    static func summarize(_ text: String, limit: Int = 220) -> String {
        var s = text
        for marker in ["**", "__", "`", "#", "> "] { s = s.replacingOccurrences(of: marker, with: "") }
        s = s.split(whereSeparator: { $0.isWhitespace }).joined(separator: " ")
        if s.count > limit { s = String(s.prefix(limit - 1)).trimmingCharacters(in: .whitespaces) + "…" }
        return s
    }
}
