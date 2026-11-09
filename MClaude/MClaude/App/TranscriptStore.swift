import Foundation

/// Saved transcripts (docs/design-claude-uplift.md → Saved transcripts): the newest events of
/// each session, kept in Caches so a session opened after a relaunch shows its last copy at once
/// instead of an empty screen while the first load is in flight (Ops F-8).
struct TranscriptStore: Sendable {
    /// Events kept per session: one `/events` page.
    static let keep = 200
    static let maxAge: TimeInterval = 14 * 24 * 3600

    struct Saved: Codable, Sendable {
        let claudeSessionId: String?
        let events: [SessionEvent]
    }

    static var root: URL {
        FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("transcripts", isDirectory: true)
    }

    /// One folder per server, so another server's `s56` never shows up.
    let directory: URL

    init(host: String, port: Int) {
        directory = Self.root.appendingPathComponent("\(Self.safe(host))-\(port)", isDirectory: true)
    }

    private static func safe(_ name: String) -> String {
        String(name.map { $0.isLetter || $0.isNumber || $0 == "." || $0 == "-" ? $0 : "_" })
    }

    private func file(_ sessionId: String) -> URL {
        directory.appendingPathComponent(Self.safe(sessionId) + ".json")
    }

    func load(_ sessionId: String) -> Saved? {
        guard let data = try? Data(contentsOf: file(sessionId)) else { return nil }
        return try? JSONDecoder().decode(Saved.self, from: data)
    }

    func save(_ sessionId: String, claudeSessionId: String?, events: [SessionEvent]) {
        let saved = Saved(claudeSessionId: claudeSessionId, events: Array(events.suffix(Self.keep)))
        guard let data = try? JSONEncoder().encode(saved) else { return }
        try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try? data.write(to: file(sessionId), options: .atomic)
    }

    func remove(_ sessionId: String) {
        try? FileManager.default.removeItem(at: file(sessionId))
    }

    /// Delete files (for every server) not written for `maxAge`.
    static func pruneOld(now: Date = Date()) {
        let fm = FileManager.default
        guard let servers = try? fm.contentsOfDirectory(at: root, includingPropertiesForKeys: nil) else { return }
        for server in servers {
            let files = (try? fm.contentsOfDirectory(at: server, includingPropertiesForKeys: [.contentModificationDateKey])) ?? []
            for f in files {
                let modified = (try? f.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate
                if let modified, now.timeIntervalSince(modified) > maxAge { try? fm.removeItem(at: f) }
            }
        }
    }

    static func removeAll() {
        try? FileManager.default.removeItem(at: root)
    }

    /// Join a fresh `/events` page onto a restored copy. The fresh page is the server's truth for
    /// its time range, so restored events inside that range are dropped. Older restored events are
    /// kept unless they duplicate fresh ones, and so are newer ones (live events that arrived
    /// while the request was in flight). A full page that shares nothing with the copy means the
    /// copy is too old to join up: the fresh page replaces it, so there's never a gap.
    static func merge(restored: [SessionEvent], fresh: [SessionEvent], pageSize: Int = keep) -> [SessionEvent] {
        let times = fresh.map(\.timestamp)
        guard let low = times.min(), let high = times.max() else { return restored }
        let freshIds = Set(fresh.map(\.uuid))
        let outside = restored.filter { !freshIds.contains($0.uuid) && ($0.timestamp < low || $0.timestamp > high) }
        let kept = AppState.dropDuplicates(outside, of: fresh)
        let overlaps = restored.contains { freshIds.contains($0.uuid) }
            || AppState.dropDuplicates(restored, of: fresh).count < restored.count
        if fresh.count >= pageSize && !overlaps {
            return fresh + kept.filter { $0.timestamp > high }
        }
        return kept + fresh   // the caller sorts by timestamp
    }
}
