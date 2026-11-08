import Foundation

/// When you last saw each session, on the iPhone or the watch (docs/plan-watch-app.md → Read state).
/// Shared by the app and the watch relay, which can run while the app is in the background.
final class SeenStore: @unchecked Sendable {
    static let shared = SeenStore()

    private let lock = NSLock()
    private let defaults = UserDefaults.standard
    private static let mapKey = "watchSeenAt"
    private static let baselineKey = "watchSeenBaseline"
    private var map: [String: Double]
    /// First run of this version: sessions with no entry count as seen at this moment.
    let baseline: Date

    private init() {
        map = (defaults.dictionary(forKey: Self.mapKey) as? [String: Double]) ?? [:]
        let stored = defaults.double(forKey: Self.baselineKey)
        if stored > 0 {
            baseline = Date(timeIntervalSince1970: stored)
        } else {
            baseline = Date()
            defaults.set(baseline.timeIntervalSince1970, forKey: Self.baselineKey)
        }
    }

    /// Record that `id` was seen at `date`, keeping the later time.
    func markSeen(_ id: String, at date: Date = Date()) {
        lock.withLock {
            let t = date.timeIntervalSince1970
            guard t > (map[id] ?? 0) else { return }
            map[id] = t
            defaults.set(map, forKey: Self.mapKey)
        }
    }

    /// When `id` was last seen, or the baseline.
    func seenAt(_ id: String) -> Date {
        lock.withLock { map[id].map { Date(timeIntervalSince1970: $0) } ?? baseline }
    }

    /// Forget sessions that are no longer open.
    func prune(keeping ids: Set<String>) {
        lock.withLock {
            let kept = map.filter { ids.contains($0.key) }
            guard kept.count != map.count else { return }
            map = kept
            defaults.set(map, forKey: Self.mapKey)
        }
    }
}
