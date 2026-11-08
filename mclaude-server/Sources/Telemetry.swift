import Foundation
import Hummingbird

/// Minimal OpenTelemetry exporter for mclaude-server (docs/plan-observability.md → System map →
/// What feeds it). Same approach as the app's exporter (MClaude/Telemetry/Telemetry.swift):
/// standard OTLP/HTTP JSON, no SDK dependency, sent to the local collector.
///
/// Records sit in an in-memory buffer (at most `maxBuffered`; past that the oldest are dropped
/// and one "dropped N records" record is sent in their place), flushed every 2 s. Export
/// failures are retried on the next flush and never affect the server.
final class Telemetry: @unchecked Sendable {
    static let shared = Telemetry()

    /// The collector's OTLP/HTTP base URL, also handed to Claude Code (ClaudeProcess).
    static let endpoint = ProcessInfo.processInfo.environment["MCLAUDE_OTLP_ENDPOINT"] ?? "http://127.0.0.1:4318"

    /// The HTTP request being handled, set by `TelemetryMiddleware` for the handler's task, so
    /// work it starts (a Claude turn) can be its child.
    @TaskLocal static var currentRequest: SpanContext?

    /// A span's identity, used to put records inside a trace.
    struct SpanContext: Sendable, Equatable {
        let traceId: String
        let spanId: String
        var flags = "01"

        /// W3C `traceparent` header value.
        var traceparent: String { "00-\(traceId)-\(spanId)-\(flags)" }

        /// Parses `version-traceid-spanid-flags`; nil when malformed or all-zero ids.
        static func parse(traceparent header: String) -> SpanContext? {
            let parts = header.trimmingCharacters(in: .whitespaces).lowercased().split(separator: "-")
            guard parts.count >= 4, parts[0].count == 2, parts[0] != "ff",
                  parts[1].count == 32, parts[2].count == 16, parts[3].count == 2,
                  parts.prefix(4).allSatisfy({ $0.allSatisfy(\.isHexDigit) }),
                  parts[1] != String(repeating: "0", count: 32),
                  parts[2] != String(repeating: "0", count: 16) else { return nil }
            return SpanContext(traceId: String(parts[1]), spanId: String(parts[2]), flags: String(parts[3]))
        }
    }

    enum SpanKind: Int { case `internal` = 1, server = 2, client = 3 }

    /// An open span; call `end` once (later calls are ignored).
    final class Span: @unchecked Sendable {
        let context: SpanContext
        fileprivate let name: String
        fileprivate let kind: SpanKind
        fileprivate let parentSpanId: String?
        fileprivate let start: UInt64
        private let lock = NSLock()
        private var attributes: [String: Any]
        private var ended = false

        fileprivate init(name: String, kind: SpanKind, parent: SpanContext?, attributes: [String: Any]) {
            self.name = name
            self.kind = kind
            self.parentSpanId = parent?.spanId
            self.context = SpanContext(traceId: parent?.traceId ?? Telemetry.randomHex(16),
                                       spanId: Telemetry.randomHex(8), flags: parent?.flags ?? "01")
            self.start = Telemetry.nowNanos()
            self.attributes = attributes
        }

        func setAttribute(_ key: String, _ value: Any) {
            lock.lock(); attributes[key] = value; lock.unlock()
        }

        /// `error` marks the span's status as an error (OTLP status code 2).
        func end(attributes extra: [String: Any] = [:], error: Bool = false) {
            lock.lock()
            guard !ended else { lock.unlock(); return }
            ended = true
            attributes.merge(extra) { _, new in new }
            let attrs = attributes
            lock.unlock()
            var rec: [String: Any] = [
                "traceId": context.traceId,
                "spanId": context.spanId,
                "name": name,
                "kind": kind.rawValue,
                "startTimeUnixNano": String(start),
                "endTimeUnixNano": String(Telemetry.nowNanos()),
                "attributes": Telemetry.otlpAttributes(attrs),
            ]
            if let parentSpanId { rec["parentSpanId"] = parentSpanId }
            if error { rec["status"] = ["code": 2] }
            Telemetry.shared.append(.span, rec)
        }
    }

    fileprivate enum Kind { case log, span, metric }

    static let maxBuffered = 10_000
    private static let batchSize = 1_000
    private let lock = NSLock()
    private var buffer: [(kind: Kind, record: [String: Any])] = []
    private var dropped = 0
    private var flushing = false
    private var started = false
    private let session: URLSession = {
        let c = URLSessionConfiguration.ephemeral
        c.timeoutIntervalForRequest = 5
        c.waitsForConnectivity = false
        return URLSession(configuration: c)
    }()

    private let resourceJSON: [String: Any] = {
        var name = [CChar](repeating: 0, count: 256)
        gethostname(&name, name.count)
        let attrs: [String: Any] = [
            "service.name": "mclaude-server",
            "service.version": Telemetry.buildVersion(),
            "host.name": String(cString: name),
            "process.pid": Int(getpid()),
            "mclaude.server.port": Int(ProcessInfo.processInfo.environment["MCLAUDE_PORT"] ?? "8377") ?? 8377,
        ]
        return ["attributes": Telemetry.otlpAttributes(attrs)]
    }()

    private init() {}

    /// Start flushing every 2 s. Safe to call more than once.
    func start() {
        lock.lock()
        let first = !started
        started = true
        lock.unlock()
        guard first else { return }
        Task.detached(priority: .utility) { [self] in
            while true {
                try? await Task.sleep(nanoseconds: 2_000_000_000)
                await flush()
            }
        }
    }

    // MARK: - Recording

    func startSpan(_ name: String, kind: SpanKind = .internal, parent: SpanContext? = nil,
                   attributes: [String: Any] = [:]) -> Span {
        Span(name: name, kind: kind, parent: parent, attributes: attributes)
    }

    /// A log record. `event` becomes the OTLP event name; `body` is free text or JSON.
    func log(_ event: String, body: String? = nil, attributes: [String: Any] = [:],
             in span: SpanContext? = nil, severity: Int = 9) {
        let now = String(Telemetry.nowNanos())
        var attrs = attributes
        attrs["event.name"] = event
        var rec: [String: Any] = [
            "timeUnixNano": now,
            "observedTimeUnixNano": now,
            "severityNumber": severity,
            "severityText": severity >= 17 ? "ERROR" : severity >= 13 ? "WARN" : "INFO",
            "eventName": event,
            "attributes": Telemetry.otlpAttributes(attrs),
        ]
        rec["body"] = ["stringValue": body ?? event]
        if let span {
            rec["traceId"] = span.traceId
            rec["spanId"] = span.spanId
        }
        append(.log, rec)
    }

    /// One data point of an OTLP gauge (integer value).
    func gauge(_ name: String, _ value: Int, attributes: [String: Any] = [:]) {
        let point: [String: Any] = ["attributes": Telemetry.otlpAttributes(attributes),
                                    "timeUnixNano": String(Telemetry.nowNanos()),
                                    "asInt": String(value)]
        append(.metric, ["name": name, "point": point])
    }

    fileprivate func append(_ kind: Kind, _ record: [String: Any]) {
        lock.lock()
        buffer.append((kind, record))
        enforceCapLocked()
        lock.unlock()
    }

    /// Drops the oldest records past the cap; the next flush reports how many went.
    private func enforceCapLocked() {
        let over = buffer.count - Telemetry.maxBuffered
        guard over > 0 else { return }
        buffer.removeFirst(over)
        dropped += over
    }

    // MARK: - Export

    /// Sends the buffer, oldest first, in batches; stops at the first failure and keeps the rest.
    func flush() async {
        let begun: Bool = lock.withLock {
            guard !flushing else { return false }
            flushing = true
            return true
        }
        guard begun else { return }
        defer { lock.withLock { flushing = false } }

        let droppedNow = lock.withLock { dropped }
        if droppedNow > 0 {
            guard await send(logs: [droppedRecord(count: droppedNow)], spans: [], metrics: []) else { return }
            lock.withLock { dropped -= droppedNow }
        }
        while true {
            let batch = lock.withLock {
                let b = Array(buffer.prefix(Telemetry.batchSize))
                buffer.removeFirst(b.count)
                return b
            }
            guard !batch.isEmpty else { return }
            let logs = batch.filter { $0.kind == .log }.map(\.record)
            let spans = batch.filter { $0.kind == .span }.map(\.record)
            let metrics = batch.filter { $0.kind == .metric }.map(\.record)
            if await send(logs: logs, spans: spans, metrics: metrics) { continue }
            // Collector unreachable: put the batch back in front for the next flush
            lock.withLock {
                buffer.insert(contentsOf: batch, at: 0)
                enforceCapLocked()
            }
            return
        }
    }

    private func droppedRecord(count: Int) -> [String: Any] {
        let now = String(Telemetry.nowNanos())
        return [
            "timeUnixNano": now, "observedTimeUnixNano": now, "severityNumber": 13, "severityText": "WARN",
            "eventName": "mclaude.telemetry.dropped",
            "body": ["stringValue": "dropped \(count) records: buffer over its \(Telemetry.maxBuffered) record cap"],
            "attributes": Telemetry.otlpAttributes(["event.name": "mclaude.telemetry.dropped", "count": count]),
        ]
    }

    /// Posts each non-empty signal; false if any failed (the whole batch is retried, so a
    /// partial failure can resend some records, which is acceptable for observation data).
    private func send(logs: [[String: Any]], spans: [[String: Any]], metrics: [[String: Any]]) async -> Bool {
        let scope: [String: Any] = ["name": "mclaude-server"]
        if !logs.isEmpty {
            let payload: [String: Any] = ["resourceLogs": [["resource": resourceJSON,
                                                            "scopeLogs": [["scope": scope, "logRecords": logs]]]]]
            guard await post(payload, path: "/v1/logs") else { return false }
        }
        if !spans.isEmpty {
            let payload: [String: Any] = ["resourceSpans": [["resource": resourceJSON,
                                                             "scopeSpans": [["scope": scope, "spans": spans]]]]]
            guard await post(payload, path: "/v1/traces") else { return false }
        }
        if !metrics.isEmpty {
            // Each record is one gauge data point; group them by metric name.
            var byName: [String: [[String: Any]]] = [:]
            for m in metrics {
                guard let name = m["name"] as? String, let point = m["point"] as? [String: Any] else { continue }
                byName[name, default: []].append(point)
            }
            let list: [[String: Any]] = byName.sorted { $0.key < $1.key }.map { name, points in
                ["name": name, "gauge": ["dataPoints": points]]
            }
            let payload: [String: Any] = ["resourceMetrics": [["resource": resourceJSON,
                                                               "scopeMetrics": [["scope": scope, "metrics": list]]]]]
            guard await post(payload, path: "/v1/metrics") else { return false }
        }
        return true
    }

    private func post(_ payload: [String: Any], path: String) async -> Bool {
        guard let url = URL(string: Telemetry.endpoint + path),
              let body = try? JSONSerialization.data(withJSONObject: payload) else { return true }
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = body
        guard let (_, resp) = try? await session.data(for: req), let http = resp as? HTTPURLResponse else { return false }
        // 2xx: delivered. 400: the collector can't parse it, so retrying would never succeed.
        return (200..<300).contains(http.statusCode) || http.statusCode == 400
    }

    // MARK: - Helpers

    static func nowNanos() -> UInt64 { UInt64(Date().timeIntervalSince1970 * 1_000_000_000) }

    static func randomHex(_ bytes: Int) -> String {
        (0..<bytes).map { _ in String(format: "%02x", UInt8.random(in: 0...255)) }.joined()
    }

    static func otlpAttributes(_ attrs: [String: Any]) -> [[String: Any]] {
        attrs.sorted { $0.key < $1.key }.compactMap { key, value in
            let v: [String: Any]
            switch value {
            case let b as Bool: v = ["boolValue": b]
            case let i as Int: v = ["intValue": String(i)]
            case let i as Int32: v = ["intValue": String(i)]
            case let d as Double: v = ["doubleValue": d]
            case let s as String: v = ["stringValue": s]
            default: v = ["stringValue": String(describing: value)]
            }
            return ["key": key, "value": v]
        }
    }

    /// The server has no version number; the build is identified by the executable's
    /// modification time, e.g. "build-20261108T1530".
    private static func buildVersion() -> String {
        let path = CommandLine.arguments.first.map { URL(fileURLWithPath: $0).resolvingSymlinksInPath().path } ?? ""
        guard let date = (try? FileManager.default.attributesOfItem(atPath: path))?[.modificationDate] as? Date else {
            return "unknown"
        }
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = TimeZone(identifier: "UTC")
        f.dateFormat = "yyyyMMdd'T'HHmm"
        return "build-" + f.string(from: date)
    }
}

/// One server-kind span per HTTP request (docs/plan-observability.md → System map → mclaude-server).
/// Continues the caller's trace from a W3C `traceparent` header, else starts a new one, and makes
/// the span the handler's `Telemetry.currentRequest`. Observe-only: the response is untouched.
struct TelemetryMiddleware<Context: RequestContext>: RouterMiddleware {
    func handle(_ request: Request, context: Context,
                next: (Request, Context) async throws -> Response) async throws -> Response {
        let remote = request.headers.first { $0.name.canonicalName == "traceparent" }
            .flatMap { Telemetry.SpanContext.parse(traceparent: $0.value) }
        // The router has already matched the route, so its template and parameters are known here
        let route = context.endpointPath ?? request.uri.path
        var attrs: [String: Any] = ["http.request.method": request.method.rawValue, "http.route": route,
                                    "url.path": request.uri.path]
        if route.hasPrefix("/sessions/{id}"), let id = context.parameters.get("id") { attrs["mclaude.session_id"] = id }
        let span = Telemetry.shared.startSpan("\(request.method.rawValue) \(route)", kind: .server,
                                              parent: remote, attributes: attrs)
        do {
            let response = try await Telemetry.$currentRequest.withValue(span.context) {
                try await next(request, context)
            }
            let code = Int(response.status.code)
            span.end(attributes: ["http.response.status_code": code], error: code >= 500)
            return response
        } catch {
            let code = (error as? HTTPResponseError).map { Int($0.status.code) } ?? 500
            span.end(attributes: ["http.response.status_code": code, "error.type": String(describing: type(of: error))],
                     error: code >= 500)
            throw error
        }
    }
}
