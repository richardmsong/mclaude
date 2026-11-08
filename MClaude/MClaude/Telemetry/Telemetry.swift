import Foundation
import UIKit

/// Minimal OpenTelemetry exporter (docs/plan-observability.md → App telemetry).
///
/// Sends standard OTLP/HTTP JSON (logs and spans) to the collector on the Mac, at
/// `http://<server host>:4318`. Every record is appended to an on-disk outbox right away, so an
/// app kill doesn't lose it; the outbox is sent every 5 s and when the app goes to the background.
/// Export failures never affect the app: they're retried on the next flush.
final class Telemetry: @unchecked Sendable {
    static let shared = Telemetry()

    /// A span's identity, used to put records inside a trace.
    struct SpanContext: Sendable {
        let traceId: String
        let spanId: String
    }

    /// An open span; call `end` once.
    final class Span: @unchecked Sendable {
        let context: SpanContext
        fileprivate let name: String
        fileprivate let parentSpanId: String?
        fileprivate let kind: Int
        fileprivate let start: UInt64
        fileprivate var attributes: [String: Any]
        private var ended = false

        fileprivate init(name: String, parent: SpanContext?, kind: Int, attributes: [String: Any]) {
            self.name = name
            self.kind = kind
            self.parentSpanId = parent?.spanId
            self.context = SpanContext(traceId: parent?.traceId ?? Telemetry.randomHex(16),
                                       spanId: Telemetry.randomHex(8))
            self.start = Telemetry.nowNanos()
            self.attributes = attributes
        }

        func end(attributes extra: [String: Any] = [:]) {
            guard !ended else { return }
            ended = true
            attributes.merge(extra) { _, new in new }
            Telemetry.shared.record(span: self, end: Telemetry.nowNanos())
        }
    }

    private let queue = DispatchQueue(label: "mclaude.telemetry", qos: .utility)
    private var endpoint: URL?
    private var timer: DispatchSourceTimer?
    private var flushing = false
    private var currentSegment: URL?
    private var currentHandle: FileHandle?
    private var droppedSinceLastReport = 0
    private var observingBackground = false
    /// Cumulative counters since launch: key → (name, attributes, value). Exported as OTLP sums.
    private var counters: [String: (name: String, attributes: [String: Any], value: Int)] = [:]
    private var countersDirty = false
    private var ticks = 0
    private let launchNanos = Telemetry.nowNanos()
    private var contextSessionIdValue = ""
    /// The open session, readable from any thread (for the stall watchdog).
    var contextSessionId: String {
        get { queue.sync { contextSessionIdValue } }
        set {
            queue.async { self.contextSessionIdValue = newValue }
            crashContextLock.lock(); crashContextSessionId = newValue; crashContextLock.unlock()
        }
    }
    /// A copy of the open session for the crash path, which must not wait on `queue`.
    private let crashContextLock = NSLock()
    private var crashContextSessionId = ""
    /// The open session for a crash record; "" if the lock is held (never blocks).
    var crashSessionId: String {
        guard crashContextLock.try() else { return "" }
        defer { crashContextLock.unlock() }
        return crashContextSessionId
    }
    private var launchLogged = false
    private let maxOutboxBytes = 20 * 1024 * 1024
    private let session: URLSession = {
        let c = URLSessionConfiguration.ephemeral
        c.timeoutIntervalForRequest = 10
        c.waitsForConnectivity = false
        return URLSession(configuration: c)
    }()

    /// Uploads a screenshot and returns its server path (set by AppState; nil until configured).
    var uploadScreenshot: (@Sendable (Data) async -> String?)?

    let installId: String = {
        let key = "mclaude.telemetry.installId"
        if let id = UserDefaults.standard.string(forKey: key) { return id }
        let id = UUID().uuidString.lowercased()
        UserDefaults.standard.set(id, forKey: key)
        return id
    }()
    let launchId = UUID().uuidString.lowercased()

    private let outboxDir: URL = {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        let dir = base.appendingPathComponent("telemetry-outbox", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }()

    private lazy var resourceJSON: [String: Any] = {
        let info = Bundle.main.infoDictionary ?? [:]
        var sys = utsname()
        uname(&sys)
        let machine = withUnsafeBytes(of: &sys.machine) { raw in
            String(decoding: raw.prefix { $0 != 0 }, as: UTF8.self)
        }
        #if targetEnvironment(simulator)
        let simulator = true
        #else
        let simulator = false
        #endif
        let attrs: [String: Any] = [
            "service.name": "mclaude-ios",
            "service.version": info["CFBundleShortVersionString"] as? String ?? "?",
            "mclaude.build": info["CFBundleVersion"] as? String ?? "?",
            "mclaude.install_id": installId,
            "mclaude.launch_id": launchId,
            "device.model.identifier": machine,
            "os.name": "iOS",
            "os.version": UIDevice.current.systemVersion,
            "mclaude.simulator": simulator,
        ]
        return ["attributes": Self.otlpAttributes(attrs)]
    }()

    private init() {}

    /// Point the exporter at the Mac (the same host the app uses for mclaude-server) and start
    /// sending. Safe to call again when the host changes.
    func configure(serverHost: String) {
        if !launchLogged {
            launchLogged = true
            log("mclaude.app.launch", attributes: ["server_host": serverHost, "pid": Int(getpid())])
        }
        queue.async { [self] in
            endpoint = URL(string: "http://\(serverHost):4318")
            guard timer == nil else { return }
            let t = DispatchSource.makeTimerSource(queue: queue)
            t.schedule(deadline: .now() + 5, repeating: 5)
            t.setEventHandler { [weak self] in
                guard let self else { return }
                ticks += 1
                if ticks % 12 == 0 { snapshotCountersLocked() }   // once a minute
                flushLocked()
            }
            t.resume()
            timer = t
        }
        Task { @MainActor in
            guard !self.observingBackground else { return }
            self.observingBackground = true
            NotificationCenter.default.addObserver(forName: UIApplication.didEnterBackgroundNotification,
                                                   object: nil, queue: .main) { _ in
                Telemetry.shared.flushInBackground()
            }
        }
    }

    // MARK: - Recording

    /// `kind`: 1 internal, 3 client (OTLP SpanKind).
    func startSpan(_ name: String, parent: SpanContext? = nil, kind: Int = 1,
                   attributes: [String: Any] = [:]) -> Span {
        Span(name: name, parent: parent, kind: kind, attributes: attributes)
    }

    /// A log record. `event` becomes the OTLP event name; `body` is free text or JSON.
    func log(_ event: String, body: String? = nil, attributes: [String: Any] = [:],
             in span: SpanContext? = nil, severity: Int = 9) {
        append(kind: "log", record: Self.logRecord(event, body: body, attributes: attributes, in: span, severity: severity))
    }

    /// For a dying process: writes one log record to its own outbox file on the calling thread,
    /// without touching `queue` (the crash may be on it). The next launch sends it.
    func logImmediately(_ event: String, body: String? = nil, attributes: [String: Any] = [:], severity: Int = 17) {
        let rec = Self.logRecord(event, body: body, attributes: attributes, in: nil, severity: severity)
        guard var data = try? JSONSerialization.data(withJSONObject: ["k": "log", "r": rec]) else { return }
        data.append(0x0A)
        let name = String(format: "seg-%013.0f-x.jsonl", Date().timeIntervalSince1970 * 1000)
        try? data.write(to: outboxDir.appendingPathComponent(name))
    }

    private static func logRecord(_ event: String, body: String?, attributes: [String: Any],
                                  in span: SpanContext?, severity: Int) -> [String: Any] {
        let now = Telemetry.nowNanos()
        var attrs = attributes
        attrs["event.name"] = event
        var rec: [String: Any] = [
            "timeUnixNano": String(now),
            "observedTimeUnixNano": String(now),
            "severityNumber": severity,
            "severityText": severity >= 17 ? "ERROR" : severity >= 13 ? "WARN" : "INFO",
            "eventName": event,
            "attributes": Self.otlpAttributes(attrs),
        ]
        if let body { rec["body"] = ["stringValue": body] }
        if let span {
            rec["traceId"] = span.traceId
            rec["spanId"] = span.spanId
        }
        return rec
    }

    /// Adds 1 to a cumulative counter (no content, low cardinality attributes only).
    func count(_ name: String, attributes: [String: Any] = [:]) {
        queue.async { [self] in
            let key = name + "|" + attributes.sorted { $0.key < $1.key }.map { "\($0.key)=\($0.value)" }.joined(separator: ",")
            var c = counters[key] ?? (name, attributes, 0)
            c.value += 1
            counters[key] = c
            countersDirty = true
        }
    }

    /// One gauge reading (e.g. memory), written to the outbox now. Low-cardinality attributes only.
    func gauge(_ name: String, _ value: Double, attributes: [String: Any] = [:]) {
        let point: [String: Any] = ["attributes": Self.otlpAttributes(attributes),
                                    "timeUnixNano": String(Telemetry.nowNanos()), "asDouble": value]
        append(kind: "metric", record: ["name": name, "type": "gauge", "point": point])
    }

    /// Runs on `queue`: writes the current value of every counter to the outbox, if any changed.
    private func snapshotCountersLocked() {
        guard countersDirty else { return }
        countersDirty = false
        let now = String(Telemetry.nowNanos())
        for c in counters.values {
            let point: [String: Any] = ["attributes": Self.otlpAttributes(c.attributes),
                                        "startTimeUnixNano": String(launchNanos), "timeUnixNano": now,
                                        "asInt": String(c.value)]
            appendLocked(kind: "metric", record: ["name": c.name, "point": point])
        }
    }

    fileprivate func record(span: Span, end: UInt64) {
        var rec: [String: Any] = [
            "traceId": span.context.traceId,
            "spanId": span.context.spanId,
            "name": span.name,
            "kind": span.kind,
            "startTimeUnixNano": String(span.start),
            "endTimeUnixNano": String(end),
            "attributes": Self.otlpAttributes(span.attributes),
        ]
        if let parent = span.parentSpanId { rec["parentSpanId"] = parent }
        append(kind: "span", record: rec)
    }

    private func append(kind: String, record: [String: Any]) {
        queue.async { [self] in appendLocked(kind: kind, record: record) }
    }

    private func appendLocked(kind: String, record: [String: Any]) {
        guard let data = try? JSONSerialization.data(withJSONObject: ["k": kind, "r": record]) else { return }
        if currentHandle == nil { openSegment() }
        currentHandle?.write(data)
        currentHandle?.write(Data([0x0A]))
    }

    // MARK: - Outbox

    private func openSegment() {
        let url = outboxDir.appendingPathComponent(String(format: "seg-%013.0f.jsonl", Date().timeIntervalSince1970 * 1000))
        FileManager.default.createFile(atPath: url.path, contents: nil)
        currentSegment = url
        currentHandle = try? FileHandle(forWritingTo: url)
    }

    private func closeSegment() {
        try? currentHandle?.close()
        currentHandle = nil
        currentSegment = nil
    }

    private func flushInBackground() {
        let app = UIApplication.shared
        var task: UIBackgroundTaskIdentifier = .invalid
        task = app.beginBackgroundTask(withName: "telemetry-flush") { app.endBackgroundTask(task) }
        queue.async { [self] in
            snapshotCountersLocked()
            flushLocked {
                DispatchQueue.main.async { app.endBackgroundTask(task) }
            }
        }
    }

    /// Runs on `queue`. Sends closed segments oldest first; stops at the first failure.
    private func flushLocked(completion: (@Sendable () -> Void)? = nil) {
        guard !flushing, let endpoint else { completion?(); return }
        closeSegment()
        enforceCap()
        let segments = ((try? FileManager.default.contentsOfDirectory(at: outboxDir, includingPropertiesForKeys: nil)) ?? [])
            .filter { $0.pathExtension == "jsonl" }
            .sorted { $0.lastPathComponent < $1.lastPathComponent }
        guard !segments.isEmpty || droppedSinceLastReport > 0 else { completion?(); return }
        flushing = true
        let dropped = droppedSinceLastReport
        Task.detached { [self] in
            if dropped > 0 {
                let ok = await send(logs: [droppedRecord(count: dropped)], spans: [], metrics: [], to: endpoint)
                if ok { queue.sync { droppedSinceLastReport -= dropped } }
            }
            for seg in segments {
                guard let text = try? String(contentsOf: seg, encoding: .utf8) else {
                    try? FileManager.default.removeItem(at: seg); continue
                }
                var logs: [[String: Any]] = [], spans: [[String: Any]] = [], metrics: [[String: Any]] = []
                for line in text.split(separator: "\n") {
                    guard let obj = try? JSONSerialization.jsonObject(with: Data(line.utf8)) as? [String: Any],
                          let rec = obj["r"] as? [String: Any] else { continue }
                    switch obj["k"] as? String {
                    case "span": spans.append(rec)
                    case "metric": metrics.append(rec)
                    default: logs.append(rec)
                    }
                }
                if logs.isEmpty && spans.isEmpty && metrics.isEmpty { try? FileManager.default.removeItem(at: seg); continue }
                if await send(logs: logs, spans: spans, metrics: metrics, to: endpoint) {
                    try? FileManager.default.removeItem(at: seg)
                } else {
                    break   // collector unreachable: keep the rest for the next flush
                }
            }
            queue.async { self.flushing = false }
            completion?()
        }
    }

    /// Drops the oldest segments past the size cap; the next flush reports how many records went.
    private func enforceCap() {
        let fm = FileManager.default
        let segs = ((try? fm.contentsOfDirectory(at: outboxDir, includingPropertiesForKeys: [.fileSizeKey])) ?? [])
            .filter { $0.pathExtension == "jsonl" }
            .sorted { $0.lastPathComponent < $1.lastPathComponent }
        var sizes = segs.map { (try? $0.resourceValues(forKeys: [.fileSizeKey]).fileSize) ?? 0 }
        var total = sizes.reduce(0, +)
        var i = 0
        while total > maxOutboxBytes, i < segs.count {
            if let text = try? String(contentsOf: segs[i], encoding: .utf8) {
                droppedSinceLastReport += text.split(separator: "\n").count
            }
            try? fm.removeItem(at: segs[i])
            total -= sizes[i]
            sizes[i] = 0
            i += 1
        }
    }

    private func droppedRecord(count: Int) -> [String: Any] {
        let now = String(Telemetry.nowNanos())
        return [
            "timeUnixNano": now, "observedTimeUnixNano": now, "severityNumber": 13, "severityText": "WARN",
            "eventName": "mclaude.telemetry.dropped",
            "body": ["stringValue": "dropped \(count) records: outbox over its size cap"],
            "attributes": Self.otlpAttributes(["event.name": "mclaude.telemetry.dropped", "count": count]),
        ]
    }

    private func send(logs: [[String: Any]], spans: [[String: Any]], metrics: [[String: Any]],
                      to endpoint: URL) async -> Bool {
        let scope: [String: Any] = ["name": "mclaude-ios"]
        if !logs.isEmpty {
            let payload: [String: Any] = ["resourceLogs": [["resource": resourceJSON,
                                                            "scopeLogs": [["scope": scope, "logRecords": logs]]]]]
            guard await post(payload, to: endpoint.appendingPathComponent("v1/logs")) else { return false }
        }
        if !spans.isEmpty {
            let payload: [String: Any] = ["resourceSpans": [["resource": resourceJSON,
                                                             "scopeSpans": [["scope": scope, "spans": spans]]]]]
            guard await post(payload, to: endpoint.appendingPathComponent("v1/traces")) else { return false }
        }
        if !metrics.isEmpty {
            // Each record is one data point: a cumulative monotonic sum (counters) or a gauge.
            var byName: [String: (type: String, points: [[String: Any]])] = [:]
            for m in metrics {
                guard let name = m["name"] as? String, let point = m["point"] as? [String: Any] else { continue }
                let type = m["type"] as? String ?? "sum"
                byName[name, default: (type, [])].points.append(point)
            }
            let list: [[String: Any]] = byName.sorted { $0.key < $1.key }.map { name, entry in
                entry.type == "gauge"
                    ? ["name": name, "gauge": ["dataPoints": entry.points]]
                    : ["name": name, "sum": ["dataPoints": entry.points, "aggregationTemporality": 2, "isMonotonic": true]]
            }
            let payload: [String: Any] = ["resourceMetrics": [["resource": resourceJSON,
                                                               "scopeMetrics": [["scope": scope, "metrics": list]]]]]
            guard await post(payload, to: endpoint.appendingPathComponent("v1/metrics")) else { return false }
        }
        return true
    }

    private func post(_ payload: [String: Any], to url: URL) async -> Bool {
        guard let body = try? JSONSerialization.data(withJSONObject: payload) else { return true }
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = body
        let result: (Data, URLResponse)
        do { result = try await session.data(for: req) } catch {
            #if DEBUG
            print("[telemetry] POST \(url.absoluteString) failed: \(error.localizedDescription)")
            #endif
            return false
        }
        let (data, resp) = result
        guard let http = resp as? HTTPURLResponse else { return false }
        #if DEBUG
        print("[telemetry] POST \(url.path) → \(http.statusCode) \(String(decoding: data.prefix(300), as: UTF8.self))")
        #endif
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
            case let d as Double: v = ["doubleValue": d]
            case let f as CGFloat: v = ["doubleValue": Double(f)]
            case let s as String: v = ["stringValue": s]
            default: v = ["stringValue": String(describing: value)]
            }
            return ["key": key, "value": v]
        }
    }

    /// JSON text for a record body.
    static func json(_ object: Any) -> String {
        guard JSONSerialization.isValidJSONObject(object),
              let data = try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]) else {
            return String(describing: object)
        }
        return String(decoding: data, as: UTF8.self)
    }
}

/// Screenshot of the key window, uploaded through the server's screenshot endpoint.
enum TelemetryScreenshot {
    /// Captures now (main thread) and uploads in the background; `completion` gets the server path or nil.
    @MainActor
    static func capture(completion: @escaping @Sendable (String?) -> Void) {
        guard let window = UIApplication.shared.connectedScenes
            .compactMap({ ($0 as? UIWindowScene)?.keyWindow }).first else { completion(nil); return }
        let renderer = UIGraphicsImageRenderer(bounds: window.bounds)
        let image = renderer.image { _ in window.drawHierarchy(in: window.bounds, afterScreenUpdates: false) }
        guard let data = image.jpegData(compressionQuality: 0.6),
              let upload = Telemetry.shared.uploadScreenshot else { completion(nil); return }
        Task.detached { completion(await upload(data)) }
    }
}
