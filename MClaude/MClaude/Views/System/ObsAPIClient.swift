import Foundation
import Observation

// Read-only client for the mclaude-obs API (docs/plan-observability.md → System map → Obs API):
// plain HTTP GETs to http://<server host>:4320/api/…, decoded into the models below.

// MARK: - Models

/// A node's or edge's state. Anything unknown decodes as `nodata`, so it's never drawn healthy.
enum ObsState: String, Decodable, CaseIterable {
    case ok, degraded, failing, nodata

    init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = ObsState(rawValue: raw) ?? .nodata
    }

    /// Worst first, for "any failing" style checks.
    var severity: Int {
        switch self {
        case .failing: 3
        case .degraded: 2
        case .nodata: 1
        case .ok: 0
        }
    }
}

/// Any JSON value (node `details`, a finding's record body).
enum ObsJSON: Decodable, Equatable {
    case string(String), number(Double), bool(Bool), object([String: ObsJSON]), array([ObsJSON]), null

    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { self = .null }
        else if let b = try? c.decode(Bool.self) { self = .bool(b) }
        else if let n = try? c.decode(Double.self) { self = .number(n) }
        else if let s = try? c.decode(String.self) { self = .string(s) }
        else if let o = try? c.decode([String: ObsJSON].self) { self = .object(o) }
        else if let a = try? c.decode([ObsJSON].self) { self = .array(a) }
        else { self = .null }
    }

    /// Short display text for a label–value row.
    var display: String {
        switch self {
        case .string(let s): return s.isEmpty ? "\"\"" : s
        case .number(let n):
            if n.rounded() == n, abs(n) < 1e15 { return String(Int64(n)) }
            return n.formatted(.number.precision(.fractionLength(0...2)))
        case .bool(let b): return b ? "yes" : "no"
        case .null: return "—"
        case .object(let o): return o.sorted { $0.key < $1.key }.map { "\($0.key): \($0.value.display)" }.joined(separator: " · ")
        case .array(let a): return a.map(\.display).joined(separator: ", ")
        }
    }

    /// Label–value rows: an object's keys (nested objects flattened one level, "Column width"),
    /// an array's items ("1 · send"), or a single row.
    func summaryRows(title: String = "Value") -> [(String, String)] {
        switch self {
        case .object(let o):
            return o.sorted { $0.key < $1.key }.flatMap { k, v -> [(String, String)] in
                if case .object(let inner) = v, !inner.isEmpty, inner.count <= 8 {
                    return inner.sorted { $0.key < $1.key }.map { (Self.humanize(k + "_" + $0.key), $0.value.cellText) }
                }
                return [(Self.humanize(k), v.cellText)]
            }
        case .array(let a):
            return a.enumerated().map { i, item in
                if case .object(var o) = item, case .string(let kind)? = o["kind"] {
                    o["kind"] = nil
                    return ("\(i + 1) · \(kind)", ObsJSON.object(o).cellText)
                }
                return ("\(i + 1)", item.cellText)
            }
        default:
            return [(title, display)]
        }
    }

    /// `display`, with long nested values cut short and empty lists shown as "none".
    var cellText: String {
        if case .array(let a) = self, a.isEmpty { return "none" }
        let text = display
        return text.count > 160 ? String(text.prefix(160)) + "…" : text
    }

    /// Pretty-printed JSON with sorted keys. Numbers use Swift's shortest form (0.1, not
    /// 0.10000000000000001 as JSONSerialization writes it).
    var prettyJSON: String { pretty(indent: "") }

    private func pretty(indent: String) -> String {
        let inner = indent + "  "
        switch self {
        case .string(let s): return Self.quote(s)
        case .number(let n):
            if n.rounded() == n, abs(n) < 1e15 { return String(Int64(n)) }
            return "\(n)"
        case .bool(let b): return b ? "true" : "false"
        case .null: return "null"
        case .array(let a):
            if a.isEmpty { return "[]" }
            return "[\n" + a.map { inner + $0.pretty(indent: inner) }.joined(separator: ",\n") + "\n\(indent)]"
        case .object(let o):
            if o.isEmpty { return "{}" }
            return "{\n" + o.sorted { $0.key < $1.key }
                .map { inner + Self.quote($0.key) + ": " + $0.value.pretty(indent: inner) }
                .joined(separator: ",\n") + "\n\(indent)}"
        }
    }

    private static func quote(_ s: String) -> String {
        var out = "\""
        for ch in s.unicodeScalars {
            switch ch {
            case "\"": out += "\\\""
            case "\\": out += "\\\\"
            case "\n": out += "\\n"
            case "\r": out += "\\r"
            case "\t": out += "\\t"
            default:
                if ch.value < 0x20 { out += String(format: "\\u%04x", ch.value) } else { out.unicodeScalars.append(ch) }
            }
        }
        return out + "\""
    }

    /// Turns `cpu_pct` into "CPU %", `memory_mb` into "Memory MB", `ws_clients` into "WS clients".
    static func humanize(_ key: String) -> String {
        let key = key.hasPrefix("mclaude.") ? String(key.dropFirst(8)) : key
        let upper: Set = ["cpu", "ws", "id", "http", "api", "pid", "url", "mb", "gb", "kb", "p50", "p95", "p99"]
        let words = key.split(whereSeparator: { $0 == "_" || $0 == "." }).map { w -> String in
            let s = String(w)
            if s == "pct" { return "%" }
            if s == "ms" || s == "s" { return "(\(s))" }
            return upper.contains(s.lowercased()) ? s.uppercased() : s
        }
        guard let first = words.first else { return key }
        return ([first.prefix(1).uppercased() + first.dropFirst()] + words.dropFirst()).joined(separator: " ")
    }
}

struct ObsNode: Decodable, Identifiable, Equatable {
    let id: String
    let label: String
    let state: ObsState
    let value: String?
    let reason: String?
    let details: [String: ObsJSON]?
}

struct ObsEdge: Decodable, Equatable {
    let from: String
    let to: String
    let state: ObsState
    let label: String?
}

struct ObsEvent: Decodable, Equatable, Identifiable {
    let t: Double
    let kind: String
    let rule: String?
    let traceId: String?
    let node: String?
    /// Non-rule events (stall, restart, exit, api_error) describe themselves here.
    let label: String?

    var id: String { "\(t)-\(kind)-\(rule ?? label ?? "")-\(traceId ?? "")-\(node ?? "")" }
    var isRule: Bool { kind == "rule" }
    var title: String { isRule ? (rule ?? "rule break") : (label ?? kind.replacingOccurrences(of: "_", with: " ")) }

    enum CodingKeys: String, CodingKey { case t, kind, rule, traceId = "trace_id", node, label }
}

struct ObsMap: Decodable, Equatable {
    let at: Double
    let live: Bool?
    let nodes: [ObsNode]
    let edges: [ObsEdge]
    let events: [ObsEvent]

    var anyFailing: Bool { nodes.contains { $0.state == .failing } }
}

struct ObsTimeline: Decodable, Equatable {
    struct Bin: Decodable, Equatable { let t: Double; let state: ObsState }
    let bins: [Bin]
    let pins: [ObsEvent]
}

/// A rule break (`GET /api/finding`). The field names are read leniently: `t`/`time`/`timestamp`,
/// `body`/`record`, `screenshot`/`screenshot_path`. A body sent as a JSON string is parsed.
struct ObsFinding: Decodable {
    let rule: String
    let time: Date?
    let node: String?
    let body: ObsJSON?
    /// The record's log attributes.
    let attributes: [String: ObsJSON]
    let screenshot: String?

    private struct Key: CodingKey {
        var stringValue: String
        init(_ s: String) { stringValue = s }
        init?(stringValue: String) { self.stringValue = stringValue }
        var intValue: Int? { nil }
        init?(intValue: Int) { nil }
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: Key.self)
        func first(_ keys: [String]) -> ObsJSON? {
            for k in keys { if let v = try? c.decode(ObsJSON.self, forKey: Key(k)), v != .null { return v } }
            return nil
        }
        if case .string(let r)? = first(["rule"]) { rule = r } else { rule = "rule break" }
        if case .string(let n)? = first(["node"]) { node = n } else { node = nil }
        if case .string(let s)? = first(["screenshot", "screenshot_path", "screenshotPath"]), !s.isEmpty {
            screenshot = s
        } else { screenshot = nil }
        switch first(["t", "time", "timestamp"]) {
        case .number(let n)?: time = Date(timeIntervalSince1970: n > 1e12 ? n / 1000 : n)
        case .string(let s)?:
            time = Double(s).map { Date(timeIntervalSince1970: $0) }
                ?? ISO8601DateFormatter().date(from: s)
                ?? { let f = ISO8601DateFormatter(); f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]; return f.date(from: s) }()
        default: time = nil
        }
        var raw = first(["body", "record", "Body"])
        if case .string(let s)? = raw, let data = s.data(using: .utf8),
           let parsed = try? JSONDecoder().decode(ObsJSON.self, from: data), case .object = parsed {
            raw = parsed
        }
        body = raw
        if case .object(let a)? = first(["attributes", "LogAttributes"]) { attributes = a } else { attributes = [:] }
    }
}

// MARK: - Client

struct ObsAPIClient {
    static let port = 4320
    let host: String

    /// "home-server:4320", for error messages.
    var hostLabel: String { "\(baseURL.host ?? host):\(baseURL.port ?? Self.port)" }

    var baseURL: URL {
        URL(string: "http://\(host):\(Self.port)") ?? URL(string: "http://127.0.0.1:\(Self.port)")!
    }

    private static let session: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 6
        config.requestCachePolicy = .reloadIgnoringLocalCacheData
        config.waitsForConnectivity = false
        return URLSession(configuration: config)
    }()

    private func get<T: Decodable>(_ path: String, _ query: [URLQueryItem] = []) async throws -> T {
        var comps = URLComponents(url: baseURL.appendingPathComponent(path), resolvingAgainstBaseURL: false)!
        if !query.isEmpty { comps.queryItems = query }
        let (data, response) = try await Self.session.data(from: comps.url!)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw URLError(.badServerResponse) }
        return try JSONDecoder().decode(T.self, from: data)
    }

    /// The map now (`at` nil) or at a past moment.
    func map(at: Date? = nil) async throws -> ObsMap {
        try await get("api/map", at.map { [URLQueryItem(name: "at", value: String(Int($0.timeIntervalSince1970.rounded(.up))))] } ?? [])
    }

    func timeline(from: Date, to: Date, bins: Int = 96) async throws -> ObsTimeline {
        try await get("api/timeline", [
            URLQueryItem(name: "from", value: String(Int(from.timeIntervalSince1970))),
            URLQueryItem(name: "to", value: String(Int(to.timeIntervalSince1970))),
            URLQueryItem(name: "bins", value: String(bins)),
        ])
    }

    func finding(traceId: String) async throws -> ObsFinding {
        try await get("api/finding", [URLQueryItem(name: "trace_id", value: traceId)])
    }
}

// MARK: - Drawer dot

/// Whether any node was `failing` in the latest successful Live poll. Set only while the map is
/// open (it's the only poller); the drawer's System row shows the last known value.
@MainActor @Observable
final class SystemStatus {
    static let shared = SystemStatus()
    var anyFailing = false
}
