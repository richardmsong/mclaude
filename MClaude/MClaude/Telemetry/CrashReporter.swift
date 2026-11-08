import Foundation
import MetricKit
import UIKit

/// Crash records (docs/plan-observability.md → Crashes):
/// - `mclaude.app.exception`: an uncaught Objective-C exception, written as the app dies.
/// - `mclaude.app.unclean_exit`: at launch, when the previous launch ended in the foreground.
/// - `mclaude.app.crash` / `mclaude.app.diagnostic`: MetricKit diagnostics, delivered at a later launch.
final class CrashReporter: NSObject, MXMetricManagerSubscriber, @unchecked Sendable {
    static let shared = CrashReporter()

    private let lock = NSLock()
    private var started = false
    private let dir: URL = {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        let dir = base.appendingPathComponent("telemetry-state", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }()
    private var markerURL: URL { dir.appendingPathComponent("launch-marker.json") }
    private var launchesURL: URL { dir.appendingPathComponent("recent-launches.json") }
    private static let maxLaunches = 20

    /// Call once, as early in launch as possible, on the main thread.
    @MainActor
    func start() {
        lock.lock()
        guard !started else { lock.unlock(); return }
        started = true
        lock.unlock()

        NSSetUncaughtExceptionHandler { exception in
            CrashReporter.recordException(exception)
        }

        reportPreviousLaunch()
        rememberThisLaunch()
        // App.init can run before UIApplication exists; check the state on the next main-loop turn.
        writeMarker(foreground: false)
        DispatchQueue.main.async {
            if UIApplication.shared.applicationState != .background {
                CrashReporter.shared.writeMarker(foreground: true)
            }
        }

        let nc = NotificationCenter.default
        nc.addObserver(forName: UIApplication.didBecomeActiveNotification, object: nil, queue: .main) { _ in
            CrashReporter.shared.writeMarker(foreground: true)
        }
        nc.addObserver(forName: UIApplication.didEnterBackgroundNotification, object: nil, queue: .main) { _ in
            CrashReporter.shared.writeMarker(foreground: false)
        }
        nc.addObserver(forName: UIApplication.willTerminateNotification, object: nil, queue: .main) { _ in
            CrashReporter.shared.writeMarker(foreground: false)
        }

        MXMetricManager.shared.add(self)
    }

    // MARK: - Uncaught Objective-C exception

    private static func recordException(_ exception: NSException) {
        Telemetry.shared.logImmediately("mclaude.app.exception", body: exception.reason, attributes: [
            "exception.type": exception.name.rawValue,
            "exception.message": exception.reason ?? "",
            "exception.stacktrace": exception.callStackSymbols.joined(separator: "\n"),
            "mclaude.session_id": Telemetry.shared.crashSessionId,
        ], severity: 17)
    }

    // MARK: - Launch marker

    private struct Marker: Codable {
        var launchId: String
        var pid: Int32
        var build: String
        var version: String
        var foreground: Bool
        var since: Double   // unix seconds
    }

    private struct LaunchEntry: Codable {
        var launchId: String
        var pid: Int32
        var build: String
        var started: Double
    }

    private static var build: String { Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? "?" }
    private static var version: String { Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "?" }

    private func reportPreviousLaunch() {
        guard let data = try? Data(contentsOf: markerURL),
              let prev = try? JSONDecoder().decode(Marker.self, from: data),
              prev.launchId != Telemetry.shared.launchId,
              prev.foreground else { return }
        Telemetry.shared.log("mclaude.app.unclean_exit",
                             body: "previous launch ended in the foreground",
                             attributes: [
                                 "previous.launch_id": prev.launchId,
                                 "previous.pid": Int(prev.pid),
                                 "previous.build": prev.build,
                                 "previous.version": prev.version,
                                 "previous.foreground_since": Self.iso(prev.since),
                             ], severity: 13)
    }

    private func rememberThisLaunch() {
        var list = (try? Data(contentsOf: launchesURL)).flatMap { try? JSONDecoder().decode([LaunchEntry].self, from: $0) } ?? []
        list.append(LaunchEntry(launchId: Telemetry.shared.launchId, pid: getpid(), build: Self.build,
                                started: Date().timeIntervalSince1970))
        if list.count > Self.maxLaunches { list.removeFirst(list.count - Self.maxLaunches) }
        if let data = try? JSONEncoder().encode(list) { try? data.write(to: launchesURL, options: .atomic) }
    }

    private func launchId(forPid pid: Int32, build: String) -> String? {
        guard let data = try? Data(contentsOf: launchesURL),
              let all = try? JSONDecoder().decode([LaunchEntry].self, from: data) else { return nil }
        let list = all.filter { $0.launchId != Telemetry.shared.launchId }
        // Newest first: process IDs get reused, so prefer the latest launch with that pid and build.
        return list.last { $0.pid == pid && $0.build == build }?.launchId
            ?? list.last { $0.pid == pid }?.launchId
    }

    fileprivate func writeMarker(foreground: Bool) {
        let marker = Marker(launchId: Telemetry.shared.launchId, pid: getpid(), build: Self.build,
                            version: Self.version, foreground: foreground, since: Date().timeIntervalSince1970)
        if let data = try? JSONEncoder().encode(marker) { try? data.write(to: markerURL, options: .atomic) }
    }

    // MARK: - MetricKit

    func didReceive(_ payloads: [MXDiagnosticPayload]) {
        for payload in payloads {
            let window: [String: Any] = [
                "diagnostic.window_begin": Self.iso(payload.timeStampBegin.timeIntervalSince1970),
                "diagnostic.window_end": Self.iso(payload.timeStampEnd.timeIntervalSince1970),
            ]
            for crash in payload.crashDiagnostics ?? [] {
                var attrs = window.merging(common(crash)) { a, _ in a }
                if let v = crash.exceptionType { attrs["exception.type"] = v.intValue }
                if let v = crash.exceptionCode { attrs["exception.code"] = v.intValue }
                if let v = crash.signal { attrs["signal"] = v.intValue }
                if let v = crash.terminationReason { attrs["termination_reason"] = v }
                if let reason = crash.exceptionReason {
                    attrs["objc_exception.name"] = reason.exceptionName
                    attrs["objc_exception.message"] = reason.composedMessage
                    attrs["objc_exception.class"] = reason.className
                }
                Telemetry.shared.log("mclaude.app.crash", body: Self.json(crash), attributes: attrs, severity: 17)
            }
            for hang in payload.hangDiagnostics ?? [] {
                var attrs = window.merging(common(hang)) { a, _ in a }
                attrs["kind"] = "hang"
                attrs["hang_duration_s"] = hang.hangDuration.converted(to: .seconds).value
                Telemetry.shared.log("mclaude.app.diagnostic", body: Self.json(hang), attributes: attrs, severity: 13)
            }
            for cpu in payload.cpuExceptionDiagnostics ?? [] {
                var attrs = window.merging(common(cpu)) { a, _ in a }
                attrs["kind"] = "cpu_exception"
                attrs["total_cpu_time_s"] = cpu.totalCPUTime.converted(to: .seconds).value
                attrs["total_sampled_time_s"] = cpu.totalSampledTime.converted(to: .seconds).value
                Telemetry.shared.log("mclaude.app.diagnostic", body: Self.json(cpu), attributes: attrs, severity: 13)
            }
            for disk in payload.diskWriteExceptionDiagnostics ?? [] {
                var attrs = window.merging(common(disk)) { a, _ in a }
                attrs["kind"] = "disk_write_exception"
                attrs["total_writes_mb"] = disk.totalWritesCaused.converted(to: .megabytes).value
                Telemetry.shared.log("mclaude.app.diagnostic", body: Self.json(disk), attributes: attrs, severity: 13)
            }
        }
    }

    /// Attributes every MetricKit record carries: the crashed build/version and process.
    private func common(_ d: MXDiagnostic) -> [String: Any] {
        let meta = d.metaData
        var attrs: [String: Any] = [
            "crashed.build": meta.applicationBuildVersion,
            "crashed.version": d.applicationVersion,
            "crashed.os_version": meta.osVersion,
            "crashed.pid": Int(meta.pid),
        ]
        if let launch = launchId(forPid: meta.pid, build: meta.applicationBuildVersion) {
            attrs["crashed.launch_id"] = launch
        }
        return attrs
    }

    private static func json(_ d: MXDiagnostic) -> String {
        String(decoding: d.jsonRepresentation(), as: UTF8.self)
    }

    private static func iso(_ unix: Double) -> String {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f.string(from: Date(timeIntervalSince1970: unix))
    }
}
