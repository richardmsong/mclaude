import Foundation

/// User-scope MCP config and status shaping (docs/plan-mcp-servers.md). The config is read
/// from ~/.claude.json but only ever written through the Claude CLI, because running Claude
/// processes write that file too.
enum McpConfig {
    static var configPath: String { "\(NSHomeDirectory())/.claude.json" }
    static let namePattern = #"^[A-Za-z0-9_.-]{1,64}$"#

    static func isValidName(_ name: String) -> Bool {
        name.range(of: namePattern, options: .regularExpression) != nil
    }

    /// `mcpServers` from ~/.claude.json (user scope). Empty when the file is missing or unreadable.
    static func userServers() -> [String: [String: Any]] {
        guard let data = FileManager.default.contents(atPath: configPath),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let servers = json["mcpServers"] as? [String: Any] else { return [:] }
        return servers.compactMapValues { $0 as? [String: Any] }
    }

    /// Canonical serialization of the user config: equal strings mean equal config.
    static func userFingerprint() -> String? {
        let servers = userServers()
        guard let data = try? JSONSerialization.data(withJSONObject: servers, options: [.sortedKeys]) else { return nil }
        return String(data: data, encoding: .utf8)
    }

    /// Form-editable transports. Anything else is shown read-only.
    static func isEditable(_ config: [String: Any]) -> Bool {
        let type = config["type"] as? String ?? "stdio"
        return ["stdio", "http", "sse"].contains(type)
    }

    /// "npx -y mcp-searxng@2.10.0" or the URL.
    static func summary(_ config: [String: Any]?) -> String {
        guard let config else { return "" }
        if let url = config["url"] as? String { return url }
        let command = config["command"] as? String ?? ""
        let args = (config["args"] as? [Any] ?? []).map { "\($0)" }
        return ([command] + args).filter { !$0.isEmpty }.joined(separator: " ")
    }

    /// The old config with the submitted keys replaced. Empty values (cleared env, headers,
    /// args, or a URL/command left over from the other transport) remove the key.
    static func merged(old: [String: Any], new: [String: Any]) -> [String: Any] {
        var out = old
        for (key, value) in new {
            if isEmptyValue(value) { out.removeValue(forKey: key) } else { out[key] = value }
        }
        // Switching transport drops the other transport's fields
        let type = out["type"] as? String ?? "stdio"
        if type == "stdio" {
            for k in ["url", "headers"] { out.removeValue(forKey: k) }
        } else if type == "http" || type == "sse" {
            for k in ["command", "args", "env"] { out.removeValue(forKey: k) }
        }
        return out
    }

    private static func isEmptyValue(_ v: Any) -> Bool {
        if v is NSNull { return true }
        if let s = v as? String { return s.isEmpty }
        if let a = v as? [Any] { return a.isEmpty }
        if let d = v as? [String: Any] { return d.isEmpty }
        return false
    }

    // MARK: - CLI writes

    struct CLIError: Error, Sendable { let message: String }

    static func add(claudePath: String, name: String, config: [String: Any]) async -> CLIError? {
        guard let data = try? JSONSerialization.data(withJSONObject: config),
              let json = String(data: data, encoding: .utf8) else { return CLIError(message: "invalid config") }
        return await run(claudePath, ["mcp", "add-json", "-s", "user", name, json])
    }

    static func remove(claudePath: String, name: String) async -> CLIError? {
        await run(claudePath, ["mcp", "remove", "-s", "user", name])
    }

    /// Run the CLI off the actor, returning its stderr (or stdout) on a non-zero exit.
    private static func run(_ path: String, _ args: [String]) async -> CLIError? {
        await withCheckedContinuation { cont in
            DispatchQueue.global().async {
                let p = Process()
                p.executableURL = URL(fileURLWithPath: path)
                p.arguments = args
                p.currentDirectoryURL = URL(fileURLWithPath: NSHomeDirectory())
                let out = Pipe(), err = Pipe()
                p.standardOutput = out
                p.standardError = err
                p.standardInput = FileHandle.nullDevice
                do { try p.run() } catch {
                    cont.resume(returning: CLIError(message: "couldn't run claude: \(error.localizedDescription)"))
                    return
                }
                let o = out.fileHandleForReading.readDataToEndOfFile()
                let e = err.fileHandleForReading.readDataToEndOfFile()
                p.waitUntilExit()
                guard p.terminationStatus != 0 else { cont.resume(returning: nil); return }
                let text = [e, o].compactMap { String(data: $0, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) }
                    .first { !$0.isEmpty } ?? "claude exited with status \(p.terminationStatus)"
                cont.resume(returning: CLIError(message: text))
            }
        }
    }

    // MARK: - Status shaping

    /// "user", "project" or "plugin" for a raw mcp_status entry.
    static func group(_ entry: [String: Any]) -> String {
        if entry["source"] as? String == "plugin" { return "plugin" }
        let scope = entry["scope"] as? String
        if scope == "project" || scope == "local" { return "project" }
        return "user"
    }

    private static let groupOrder = ["user": 0, "project": 1, "plugin": 2]

    /// One per-session McpServer object (docs/plan-mcp-servers.md → GET /sessions/:id/mcp).
    static func sessionServer(_ entry: [String: Any]) -> [String: Any] {
        var out: [String: Any] = [
            "name": entry["name"] as? String ?? "",
            "status": entry["status"] as? String ?? "unknown",
            "transport": entry["transport"] as? String ?? ((entry["config"] as? [String: Any])?["type"] as? String ?? "stdio"),
            "group": group(entry),
            "summary": summary(entry["config"] as? [String: Any]),
            "tools": (entry["tools"] as? [Any] ?? []).compactMap { t -> String? in
                if let s = t as? String { return s }
                return (t as? [String: Any])?["name"] as? String
            },
        ]
        if let error = entry["error"] as? String, !error.isEmpty { out["error"] = error }
        if let version = (entry["serverInfo"] as? [String: Any])?["version"] as? String { out["version"] = version }
        return out
    }

    static func sortedSessionServers(_ entries: [[String: Any]]) -> [[String: Any]] {
        entries.map(sessionServer).sorted {
            let g0 = groupOrder[$0["group"] as? String ?? ""] ?? 9, g1 = groupOrder[$1["group"] as? String ?? ""] ?? 9
            if g0 != g1 { return g0 < g1 }
            return ($0["name"] as? String ?? "").localizedCaseInsensitiveCompare($1["name"] as? String ?? "") == .orderedAscending
        }
    }

    /// GET /mcp's server list: user servers from the config, plugin servers from what running
    /// sessions report, each with its per-session statuses.
    static func globalServers(config: [String: [String: Any]],
                              reports: [(id: String, name: String, servers: [[String: Any]])]) -> [[String: Any]] {
        var sessionsByServer: [String: [[String: Any]]] = [:]
        var pluginNames: Set<String> = []
        for report in reports {
            for entry in report.servers {
                guard let name = entry["name"] as? String else { continue }
                let g = group(entry)
                if g == "project" { continue }
                if g == "plugin" { pluginNames.insert(name) }
                else if config[name] == nil { continue }   // removed since that process started
                var s: [String: Any] = ["id": report.id, "name": report.name,
                                        "status": entry["status"] as? String ?? "unknown"]
                if let e = entry["error"] as? String, !e.isEmpty { s["error"] = e }
                sessionsByServer[name, default: []].append(s)
            }
        }
        let byName: ([String: Any], [String: Any]) -> Bool = {
            ($0["name"] as? String ?? "").localizedCaseInsensitiveCompare($1["name"] as? String ?? "") == .orderedAscending
        }
        let users: [[String: Any]] = config.map { name, cfg in
            ["name": name, "group": "user", "editable": isEditable(cfg), "config": cfg,
             "summary": summary(cfg), "sessions": sessionsByServer[name] ?? []]
        }.sorted(by: byName)
        let plugins: [[String: Any]] = pluginNames.map { name in
            ["name": name, "group": "plugin", "editable": false, "summary": "",
             "sessions": sessionsByServer[name] ?? []]
        }.sorted(by: byName)
        return users + plugins
    }
}
