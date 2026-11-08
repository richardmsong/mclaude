import Foundation

// MCP servers: per-session status and the global (user-level) config.
// Spec: docs/plan-mcp-servers.md → Server (endpoint shapes) and iOS app.

/// Any JSON value. MCP config objects are arbitrary JSON, so unknown keys are kept as-is.
public enum JSONValue: Codable, Sendable, Hashable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case array([JSONValue])
    case object([String: JSONValue])
    case null

    public init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { self = .null }
        else if let b = try? c.decode(Bool.self) { self = .bool(b) }
        else if let n = try? c.decode(Double.self) { self = .number(n) }
        else if let s = try? c.decode(String.self) { self = .string(s) }
        else if let a = try? c.decode([JSONValue].self) { self = .array(a) }
        else { self = .object(try c.decode([String: JSONValue].self)) }
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .string(let s): try c.encode(s)
        case .number(let n): try c.encode(n)
        case .bool(let b): try c.encode(b)
        case .array(let a): try c.encode(a)
        case .object(let o): try c.encode(o)
        case .null: try c.encodeNil()
        }
    }

    public var stringValue: String? {
        if case .string(let s) = self { return s }
        return nil
    }

    /// A string → string map (env, headers). Non-string values are rendered as text.
    public var stringMap: [String: String]? {
        guard case .object(let o) = self else { return nil }
        return o.mapValues { $0.stringValue ?? $0.displayText }
    }

    /// Readable text for a scalar (numbers without a trailing ".0").
    public var displayText: String {
        switch self {
        case .string(let s): return s
        case .number(let n): return n.rounded() == n && abs(n) < 1e15 ? String(Int64(n)) : String(n)
        case .bool(let b): return b ? "true" : "false"
        case .null: return "null"
        case .array, .object:
            let data = (try? JSONEncoder().encode(self)) ?? Data()
            return String(data: data, encoding: .utf8) ?? ""
        }
    }
}

/// A server's config object (`~/.claude.json` → `mcpServers.<name>`). The raw object is kept so
/// keys the app doesn't know (timeouts, oauth settings) aren't lost.
public struct McpConfig: Codable, Sendable, Hashable {
    public let raw: [String: JSONValue]

    public init(raw: [String: JSONValue]) { self.raw = raw }

    public init(from decoder: Decoder) throws {
        raw = try decoder.singleValueContainer().decode([String: JSONValue].self)
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        try c.encode(raw)
    }

    /// `stdio`, `http`, `sse`, … (nil = absent, which Claude Code treats as stdio).
    public var type: String? { raw["type"]?.stringValue }
    public var command: String? { raw["command"]?.stringValue }
    public var args: [String] {
        guard case .array(let a)? = raw["args"] else { return [] }
        return a.map { $0.stringValue ?? $0.displayText }
    }
    public var env: [String: String] { raw["env"]?.stringMap ?? [:] }
    public var url: String? { raw["url"]?.stringValue }
    public var headers: [String: String] { raw["headers"]?.stringMap ?? [:] }
}

// MARK: - Per session (GET /sessions/:id/mcp)

public struct SessionMcpProject: Codable, Sendable, Hashable {
    public let name: String
    public let cwd: String
}

/// One MCP server in a session's Claude process.
public struct SessionMcpServer: Codable, Sendable, Hashable, Identifiable {
    public let name: String
    /// `connected`, `pending`, `failed`, `needs-auth`, `disabled`, or anything newer (shown raw).
    public let status: String
    public let error: String?
    public let version: String?
    public let transport: String?
    /// `user`, `project` or `plugin`.
    public let group: String
    public let summary: String?
    public let tools: [String]

    public var id: String { name }

    private enum CodingKeys: String, CodingKey { case name, status, error, version, transport, group, summary, tools }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decode(String.self, forKey: .name)
        status = (try? c.decode(String.self, forKey: .status)) ?? "unknown"
        error = try? c.decodeIfPresent(String.self, forKey: .error)
        version = try? c.decodeIfPresent(String.self, forKey: .version)
        transport = try? c.decodeIfPresent(String.self, forKey: .transport)
        group = (try? c.decodeIfPresent(String.self, forKey: .group)) ?? "user"
        summary = try? c.decodeIfPresent(String.self, forKey: .summary)
        tools = (try? c.decodeIfPresent([String].self, forKey: .tools)) ?? []
    }
}

public struct SessionMcpList: Codable, Sendable, Hashable {
    public let project: SessionMcpProject?
    public let servers: [SessionMcpServer]

    private enum CodingKeys: String, CodingKey { case project, servers }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        project = try? c.decodeIfPresent(SessionMcpProject.self, forKey: .project)
        servers = try c.decodeIfPresent([SessionMcpServer].self, forKey: .servers) ?? []
    }
}

/// `POST /sessions/:id/mcp/auth` → where to sign in, and the localhost port the provider redirects to.
public struct McpAuthStart: Codable, Sendable, Hashable {
    public let authUrl: String
    public let callbackPort: Int
}

// MARK: - Global (GET /mcp)

/// One running session's view of a server.
public struct McpSessionReport: Codable, Sendable, Hashable, Identifiable {
    public let id: String
    public let name: String
    public let status: String
    public let error: String?
}

public struct GlobalMcpServer: Codable, Sendable, Hashable, Identifiable {
    public let name: String
    /// `user` or `plugin`.
    public let group: String
    /// User server with a form-editable type (stdio or absent, http, sse).
    public let editable: Bool
    /// Full config, secrets included. Nil for plugin servers.
    public let config: McpConfig?
    public let summary: String?
    public let sessions: [McpSessionReport]

    public var id: String { name }

    private enum CodingKeys: String, CodingKey { case name, group, editable, config, summary, sessions }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decode(String.self, forKey: .name)
        group = (try? c.decodeIfPresent(String.self, forKey: .group)) ?? "user"
        editable = (try? c.decodeIfPresent(Bool.self, forKey: .editable)) ?? false
        config = try? c.decodeIfPresent(McpConfig.self, forKey: .config)
        summary = try? c.decodeIfPresent(String.self, forKey: .summary)
        sessions = (try? c.decodeIfPresent([McpSessionReport].self, forKey: .sessions)) ?? []
    }
}

public struct GlobalMcpList: Codable, Sendable, Hashable {
    public let servers: [GlobalMcpServer]
    /// Sessions that answered the status query.
    public let sessionCount: Int
    /// Running sessions started with a different user MCP config than the current one.
    public let staleSessions: Int

    private enum CodingKeys: String, CodingKey { case servers, sessionCount, staleSessions }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        servers = try c.decodeIfPresent([GlobalMcpServer].self, forKey: .servers) ?? []
        sessionCount = (try? c.decodeIfPresent(Int.self, forKey: .sessionCount)) ?? 0
        staleSessions = (try? c.decodeIfPresent(Int.self, forKey: .staleSessions)) ?? 0
    }
}

/// `POST /mcp/restart-all`.
public struct McpRestartAllResult: Codable, Sendable, Hashable {
    public let restarting: Int
    public let afterTurn: Int
}

/// What the app shows for a server name: plugin servers ("plugin:Notion:notion") show their
/// last part ("notion"); the Plugins section header already says where they come from.
public func mcpDisplayName(_ name: String) -> String {
    guard name.hasPrefix("plugin:"), let last = name.split(separator: ":").last, !last.isEmpty else { return name }
    return String(last)
}
