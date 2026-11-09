import Foundation
import MachO
import QuartzCore
import UIKit

/// Phone feeds for the system map (docs/plan-observability.md → System map → What feeds it → Phone):
/// vitals gauges every 10 s in the foreground, a main-thread stall watchdog, and HTTP client spans
/// for every request to mclaude-server (with a `traceparent` header). Observe-only.
@MainActor
final class AppVitals {
    static let shared = AppVitals()

    private var timer: Timer?
    private var connected: () -> Bool = { false }
    private var started = false

    /// `connected`: whether the WebSocket to mclaude-server is up right now.
    func start(connected: @escaping () -> Bool) {
        self.connected = connected
        guard !started else { return }
        started = true
        installNetworkObserver()
        StallWatchdog.shared.start()
        let nc = NotificationCenter.default
        nc.addObserver(forName: UIApplication.didBecomeActiveNotification, object: nil, queue: .main) { _ in
            MainActor.assumeIsolated { AppVitals.shared.resume() }
        }
        nc.addObserver(forName: UIApplication.willResignActiveNotification, object: nil, queue: .main) { _ in
            MainActor.assumeIsolated { AppVitals.shared.pause() }
        }
        if UIApplication.shared.applicationState == .active { resume() }
    }

    private func resume() {
        StallWatchdog.shared.setActive(true)
        guard timer == nil else { return }
        sample()
        timer = Timer.scheduledTimer(withTimeInterval: 10, repeats: true) { _ in
            MainActor.assumeIsolated { AppVitals.shared.sample() }
        }
    }

    private func pause() {
        StallWatchdog.shared.setActive(false)
        timer?.invalidate()
        timer = nil
    }

    private func sample() {
        Telemetry.shared.gauge("mclaude.app.cpu", Self.cpuPercent())
        Telemetry.shared.gauge("mclaude.app.memory", Double(Self.memoryFootprint()))
        Telemetry.shared.gauge("mclaude.app.connected", connected() ? 1 : 0)
    }

    // MARK: - HTTP client spans

    private func installNetworkObserver() {
        NetworkObserver.start = { method, path in
            let template = AppVitals.pathTemplate(path)
            let span = Telemetry.shared.startSpan("HTTP \(method)", kind: 3, attributes: [
                "http.request.method": method, "url.path": template,
            ])
            let traceparent = "00-\(span.context.traceId)-\(span.context.spanId)-01"
            return (traceparent, { status, error in
                var attrs: [String: Any] = [:]
                if let status { attrs["http.response.status_code"] = status }
                if let error { attrs["error.type"] = String(describing: type(of: error)) }
                span.end(attributes: attrs)
            })
        }
    }

    /// `/sessions/s47/input` → `/sessions/:id/input`, so paths stay low-cardinality.
    nonisolated static func pathTemplate(_ path: String) -> String {
        var parts = path.split(separator: "/", omittingEmptySubsequences: false).map(String.init)
        for i in parts.indices where i > 0 {
            let prev = parts[i - 1]
            if ["sessions", "screenshots", "transcripts", "queue", "servers", "peers", "thread"].contains(prev),
               !parts[i].isEmpty { parts[i] = ":id" }
        }
        return parts.joined(separator: "/")
    }

    // MARK: - Process stats

    nonisolated static func cpuPercent() -> Double {
        var threads: thread_act_array_t?
        var count: mach_msg_type_number_t = 0
        guard task_threads(mach_task_self_, &threads, &count) == KERN_SUCCESS, let threads else { return 0 }
        defer {
            vm_deallocate(mach_task_self_, vm_address_t(UInt(bitPattern: threads)),
                          vm_size_t(Int(count) * MemoryLayout<thread_t>.stride))
        }
        var total = 0.0
        for i in 0..<Int(count) {
            var info = thread_basic_info()
            var infoCount = mach_msg_type_number_t(THREAD_INFO_MAX)
            let kr = withUnsafeMutablePointer(to: &info) {
                $0.withMemoryRebound(to: integer_t.self, capacity: Int(infoCount)) {
                    thread_info(threads[i], thread_flavor_t(THREAD_BASIC_INFO), $0, &infoCount)
                }
            }
            if kr == KERN_SUCCESS, info.flags & TH_FLAGS_IDLE == 0 {
                total += Double(info.cpu_usage) / Double(TH_USAGE_SCALE) * 100
            }
        }
        return total
    }

    nonisolated static func memoryFootprint() -> UInt64 {
        var info = task_vm_info_data_t()
        var count = mach_msg_type_number_t(MemoryLayout<task_vm_info_data_t>.size / MemoryLayout<natural_t>.size)
        let kr = withUnsafeMutablePointer(to: &info) {
            $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
                task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), $0, &count)
            }
        }
        return kr == KERN_SUCCESS ? info.phys_footprint : 0
    }
}

/// Pings the main thread every 50 ms from a background thread. When the main thread takes
/// 250 ms or more to answer while the app is active, it records a `mclaude.app.stall`.
final class StallWatchdog: @unchecked Sendable {
    static let shared = StallWatchdog()

    private let lock = NSLock()
    private var active = false
    private var activeSince = CACurrentMediaTime()
    private var running = false
    private var lastReport = 0.0

    func setActive(_ value: Bool) {
        lock.lock()
        active = value
        if value { activeSince = CACurrentMediaTime() }
        lock.unlock()
    }

    func start() {
        lock.lock()
        defer { lock.unlock() }
        guard !running else { return }
        running = true
        let thread = Thread { [self] in loop() }
        thread.name = "mclaude.stall-watchdog"
        thread.qualityOfService = .userInitiated
        thread.start()
    }

    private func loop() {
        while true {
            Thread.sleep(forTimeInterval: 0.05)
            lock.lock()
            let isActive = active
            lock.unlock()
            guard isActive else { continue }

            let sent = CACurrentMediaTime()
            let answered = DispatchSemaphore(value: 0)
            DispatchQueue.main.async { answered.signal() }
            _ = answered.wait(timeout: .now() + 10)
            let stall = CACurrentMediaTime() - sent
            guard stall >= 0.25 else { continue }

            lock.lock()
            // Ignore waits that span going to the background and back (the process was suspended).
            let valid = active && activeSince <= sent && stall < 30
            let now = CACurrentMediaTime()
            let report = valid && now - lastReport >= 2   // at most one record per 2 s
            if report { lastReport = now }
            lock.unlock()
            guard valid else { continue }
            Telemetry.shared.count("mclaude.app.stalls", attributes: ["bucket": stall >= 1 ? "1s+" : "250ms+"])
            if report {
                let started = Date().addingTimeInterval(-stall)
                // On main, now that it's free again: the transcript probe's rings show what the app
                // was doing just before and during the stall (docs/plan-observability.md, Ops F-10).
                DispatchQueue.main.async { MainActor.assumeIsolated {
                    var body = TranscriptProbe.shared.ringsBody()
                    let iso = ISO8601DateFormatter()
                    iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
                    body["stall_started_at"] = iso.string(from: started)
                    body["duration_ms"] = Int(stall * 1000)
                    Telemetry.shared.log("mclaude.app.stall", body: Telemetry.json(body), attributes: [
                        "duration_ms": Int(stall * 1000),
                        "mclaude.session_id": Telemetry.shared.contextSessionId,
                        "mclaude.sampling.rate": 1.0,
                    ], severity: stall >= 1 ? 13 : 9)
                } }
            }
        }
    }
}
