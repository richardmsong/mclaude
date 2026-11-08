import Foundation

/// One deduped API response's usage, parsed from a Claude Code transcript line.
struct UsageRecord: Sendable, Equatable {
    var id: String                  // message.id
    var timestamp: Date
    var model: String               // canonical, e.g. "claude-haiku-4-5"
    var provider: String            // "anthropic", "bedrock-global", "bedrock-geo", "bedrock-regional"
    var project: String?            // last path component of the line's cwd (nil: resolved later)
    var inputTokens: Int
    var outputTokens: Int
    var cacheReadTokens: Int
    var cacheWriteTokens: Int       // all cache_creation_input_tokens
    var cacheWrite1hTokens: Int     // the 1-hour-TTL part of cacheWriteTokens; the rest is 5-minute

    var promptTokens: Int { inputTokens + cacheReadTokens + cacheWriteTokens }
}

/// Tokens and list-price cost summed over some set of messages.
struct UsageTotals: Codable, Sendable, Equatable {
    var inputTokens = 0
    var outputTokens = 0
    var cacheReadTokens = 0
    var cacheWriteTokens = 0
    var costUSD: Double = 0

    mutating func add(_ r: UsageRecord, cost: Double) {
        inputTokens += r.inputTokens
        outputTokens += r.outputTokens
        cacheReadTokens += r.cacheReadTokens
        cacheWriteTokens += r.cacheWriteTokens
        costUSD += cost
    }

    func rounded() -> UsageTotals {
        var t = self
        t.costUSD = (costUSD * 1_000_000).rounded() / 1_000_000
        return t
    }
}

struct UsageDay: Codable, Sendable, Equatable {
    let date: String                // yyyy-MM-dd in the requested time zone
    let byModel: [String: UsageTotals]
    let byProject: [String: UsageTotals]
}

/// `GET /usage/summary` response.
struct UsageSummary: Codable, Sendable, Equatable {
    let days: [UsageDay]
    let byProvider: [String: UsageTotals]   // over the whole range
    let unpricedModels: [String]

    static let empty = UsageSummary(days: [], byProvider: [:], unpricedModels: [])
}

// MARK: - Pricing

/// List prices in USD per million tokens per provider (docs/design-claude-uplift.md, "Usage page").
enum UsagePricing {
    struct Rates: Sendable, Equatable {
        let input: Double
        let output: Double
        let cacheRead: Double

        func scaled(_ f: Double) -> Rates { Rates(input: input * f, output: output * f, cacheRead: cacheRead * f) }
    }

    struct Price: Sendable, Equatable {
        let base: Rates
        /// Applies to a message whose prompt (input + cache read + cache write) is over 100K tokens.
        let longPrompt: Rates?

        /// Cache writes are derived from input, so they scale too.
        func scaled(_ f: Double) -> Price { Price(base: base.scaled(f), longPrompt: longPrompt?.scaled(f)) }
    }

    static let longPromptThreshold = 100_000
    static let cacheWrite5mMultiplier = 1.25
    static let cacheWrite1hMultiplier = 2.0

    private static func p(_ i: Double, _ o: Double, _ r: Double, long: Rates? = nil) -> Price {
        Price(base: Rates(input: i, output: o, cacheRead: r), longPrompt: long)
    }

    /// Anthropic's first-party table, keyed by canonical model family.
    static let anthropic: [String: Price] = [
        "claude-fable-5-1": p(10, 50, 0.25), "claude-mythos-5-1": p(10, 50, 0.25),
        "claude-fable-5": p(10, 50, 1.00), "claude-mythos-5": p(10, 50, 1.00),
        "claude-opus-5-5": p(4, 20, 0.20),
        "claude-opus-5": p(5, 25, 0.50), "claude-opus-4-8": p(5, 25, 0.50), "claude-opus-4-7": p(5, 25, 0.50),
        "claude-opus-4-6": p(5, 25, 0.50), "claude-opus-4-5": p(5, 25, 0.50),
        "claude-opus-4-1": p(15, 75, 1.50), "claude-opus-4": p(15, 75, 1.50),
        "claude-sonnet-5-5": p(2, 10, 0.20), "claude-sonnet-5": p(2, 10, 0.20),
        "claude-sonnet-4-6": p(3, 15, 0.30), "claude-sonnet-4-5": p(3, 15, 0.30), "claude-sonnet-4": p(3, 15, 0.30),
        "claude-haiku-5-5": p(0.10, 0.50, 0.01, long: Rates(input: 0.50, output: 2.50, cacheRead: 0.05)),
        "claude-haiku-4-5": p(1, 5, 0.10),
    ]

    /// Bedrock global endpoints: Anthropic's table except Sonnet 5.5 cache reads (cut 50% on 2026-10-07).
    static let bedrockGlobal: [String: Price] = {
        var t = anthropic
        t["claude-sonnet-5-5"] = p(2, 10, 0.10)
        return t
    }()

    static let regionalPremium = 1.1
    /// Older models Bedrock prices the same in-region as globally.
    static let noRegionalPremium: Set<String> = ["claude-opus-4", "claude-opus-4-1", "claude-sonnet-4"]

    /// Bedrock in-region (bare `anthropic.` ids) and cross-region `us.`/`eu.`/`apac.`/`jp.` profiles:
    /// global x 1.1 for every token type.
    static let bedrockRegional: [String: Price] = Dictionary(uniqueKeysWithValues: bedrockGlobal.map { key, price in
        (key, noRegionalPremium.contains(key) ? price : price.scaled(regionalPremium))
    })

    /// Provider -> table; unknown providers use Anthropic's.
    static let byProvider: [String: [String: Price]] = [
        "anthropic": anthropic,
        "bedrock-global": bedrockGlobal,
        "bedrock-geo": bedrockRegional,
        "bedrock-regional": bedrockRegional,
    ]

    /// Family keys, longest first (all tables share the same models).
    private static let familyKeys = anthropic.keys.sorted { $0.count > $1.count }

    /// The family `model` belongs to: the key itself or the key plus a non-version suffix
    /// ("claude-opus-4-8-something", "claude-opus-5-5[1m]"), but not a newer version
    /// ("claude-opus-4-9" isn't "claude-opus-4").
    static func family(of model: String, in keys: [String]) -> String? {
        keys.first { key in
            guard model.hasPrefix(key) else { return false }
            let rest = model.dropFirst(key.count)
            guard let c = rest.first else { return true }
            if c.isNumber { return false }
            if c == "-", let d = rest.dropFirst().first, d.isNumber { return false }
            return true
        }
    }

    static func price(provider: String, model: String) -> Price? {
        guard let key = family(of: model, in: familyKeys) else { return nil }
        return (byProvider[provider] ?? anthropic)[key]
    }

    /// List-price cost of one message, or nil when the model isn't in the table.
    static func cost(_ r: UsageRecord) -> Double? {
        guard let price = price(provider: r.provider, model: r.model) else { return nil }
        let rates = (r.promptTokens > longPromptThreshold ? price.longPrompt : nil) ?? price.base
        let write1h = min(max(r.cacheWrite1hTokens, 0), r.cacheWriteTokens)
        let write5m = r.cacheWriteTokens - write1h
        let micro = Double(r.inputTokens) * rates.input
            + Double(r.outputTokens) * rates.output
            + Double(r.cacheReadTokens) * rates.cacheRead
            + Double(write5m) * rates.input * cacheWrite5mMultiplier
            + Double(write1h) * rates.input * cacheWrite1hMultiplier
        return micro / 1_000_000
    }
}

// MARK: - Index

/// Usage across every Claude Code transcript on this laptop (`~/.claude/projects/*/*.jsonl` and
/// `*/<session>/subagents/*.jsonl`) for `GET /usage/summary`. Each file is parsed once into compact
/// per-message records, cached by (path, size, mtime); a request re-reads only changed files (just
/// the appended part when a file grew) and aggregates in the caller's time zone. Its own actor, so scans never block SessionManager.
actor UsageIndex {
    struct FileEntry: Sendable {
        var size: Int
        var mtime: Date
        var records: [UsageRecord]
        var firstCwd: String?
        /// Bytes up to and including the last newline read; an append is parsed from here.
        var parsedBytes: Int
    }

    private let projectsDir: String
    private var cache: [String: FileEntry] = [:]
    private var refreshing: Task<Void, Never>?

    /// Files parsed by the last refresh (for tests and logging).
    private(set) var lastParsedCount = 0

    init(projectsDir: String = ClaudePaths.projectsDir) {
        self.projectsDir = projectsDir
    }

    func summary(timeZone: TimeZone, from: Date, now: Date = Date()) async -> UsageSummary {
        await refresh()
        return Self.summarize(records: dedupedRecords(), timeZone: timeZone, from: from, now: now)
    }

    /// Re-parse new or changed files (concurrent callers share one refresh).
    func refresh() async {
        if let running = refreshing { await running.value; return }
        let task = Task { await self.rescan() }
        refreshing = task
        await task.value
        refreshing = nil
    }

    private func rescan() async {
        let paths = Self.transcriptPaths(projectsDir: projectsDir)
        var stale: [(path: String, size: Int, mtime: Date, prior: FileEntry?)] = []
        var present = Set<String>()
        for path in paths {
            guard let attrs = try? FileManager.default.attributesOfItem(atPath: path),
                  (attrs[.type] as? FileAttributeType) == .typeRegular,
                  let size = (attrs[.size] as? NSNumber)?.intValue,
                  let mtime = attrs[.modificationDate] as? Date else { continue }
            present.insert(path)
            let hit = cache[path]
            if let hit, hit.size == size, hit.mtime == mtime { continue }
            // Transcripts are append-only: a file that grew is read from where the last parse stopped.
            stale.append((path, size, mtime, hit.flatMap { $0.size <= size ? $0 : nil }))
        }
        for path in cache.keys where !present.contains(path) { cache.removeValue(forKey: path) }
        lastParsedCount = stale.count
        guard !stale.isEmpty else { return }
        let parsed = await withTaskGroup(of: (String, FileEntry).self) { group in
            for file in stale {
                group.addTask {
                    var entry = Self.parseFile(path: file.path, resume: file.prior)
                    entry.size = file.size
                    entry.mtime = file.mtime
                    return (file.path, entry)
                }
            }
            var out: [(String, FileEntry)] = []
            for await item in group { out.append(item) }
            return out
        }
        for (path, entry) in parsed { cache[path] = entry }
    }

    /// Every cached record, deduped by message id across files, with its project resolved.
    private func dedupedRecords() -> [UsageRecord] {
        var byId: [String: UsageRecord] = [:]
        for (path, entry) in cache {
            // The session's project (where it started), so a session that cd's into a subfolder
            // still counts toward its project; a line's own cwd only when the session has none.
            let sessionProject = self.sessionProject(path: path, entry: entry)
            for var r in entry.records {
                r.project = sessionProject ?? r.project ?? "unknown"
                if let existing = byId[r.id] {
                    byId[r.id] = Self.merge(existing, r)
                } else {
                    byId[r.id] = r
                }
            }
        }
        return Array(byId.values)
    }

    /// The project a session started in: for a subagent file, its parent transcript's first cwd;
    /// otherwise the transcript's own first cwd. Nil when neither is known.
    private func sessionProject(path: String, entry: FileEntry) -> String? {
        if let parent = Self.parentTranscript(ofSubagent: path), let cwd = cache[parent]?.firstCwd {
            return Self.projectName(cwd)
        }
        return entry.firstCwd.map(Self.projectName)
    }

    /// The same message seen twice (content-block lines, or a forked transcript): keep the copy
    /// with the most output (the final count) and the earliest timestamp.
    static func merge(_ a: UsageRecord, _ b: UsageRecord) -> UsageRecord {
        var keep = b.outputTokens > a.outputTokens ? b : a
        keep.timestamp = min(a.timestamp, b.timestamp)
        if keep.project == nil { keep.project = a.project ?? b.project }
        return keep
    }

    static func projectName(_ cwd: String) -> String {
        let name = (cwd as NSString).lastPathComponent
        return name.isEmpty ? cwd : name
    }

    /// `<proj>/<session>/subagents/<x>.jsonl` -> `<proj>/<session>.jsonl`.
    static func parentTranscript(ofSubagent path: String) -> String? {
        let subagents = (path as NSString).deletingLastPathComponent
        guard (subagents as NSString).lastPathComponent == "subagents" else { return nil }
        return (subagents as NSString).deletingLastPathComponent + ".jsonl"
    }

    static func transcriptPaths(projectsDir: String) -> [String] {
        let fm = FileManager.default
        func isDir(_ p: String) -> Bool {
            var d: ObjCBool = false
            return fm.fileExists(atPath: p, isDirectory: &d) && d.boolValue
        }
        var paths: [String] = []
        for dir in (try? fm.contentsOfDirectory(atPath: projectsDir)) ?? [] {
            let dirPath = "\(projectsDir)/\(dir)"
            guard isDir(dirPath) else { continue }
            for entry in (try? fm.contentsOfDirectory(atPath: dirPath)) ?? [] {
                let entryPath = "\(dirPath)/\(entry)"
                if entry.hasSuffix(".jsonl") {
                    paths.append(entryPath)
                } else {
                    let subagents = "\(entryPath)/subagents"
                    guard isDir(subagents) else { continue }
                    for file in (try? fm.contentsOfDirectory(atPath: subagents)) ?? [] where file.hasSuffix(".jsonl") {
                        paths.append("\(subagents)/\(file)")
                    }
                }
            }
        }
        return paths
    }

    // MARK: - Model names

    /// "us.anthropic.claude-haiku-4-5-20251001-v1:0" -> "claude-haiku-4-5" (as the app's shortModel,
    /// plus `global.` and a bare `-v1`).
    static func canonicalModel(_ raw: String) -> String {
        var s = raw
        for region in ["us.", "eu.", "apac.", "jp.", "global."] where s.hasPrefix(region + "anthropic.") {
            s.removeFirst(region.count)
        }
        if s.hasPrefix("anthropic.") { s.removeFirst("anthropic.".count) }
        if let r = s.range(of: #"-\d{8}"#, options: .regularExpression) { s = String(s[..<r.lowerBound]) }
        if let r = s.range(of: "-v1:0") { s = String(s[..<r.lowerBound]) }
        if s.hasSuffix("-v1") { s.removeLast(3) }
        return s
    }

    /// Where the request went, from the raw model id the client used.
    static func provider(_ raw: String) -> String {
        if raw.hasPrefix("global.anthropic.") { return "bedrock-global" }
        for region in ["us.", "eu.", "apac.", "jp."] where raw.hasPrefix(region + "anthropic.") { return "bedrock-geo" }
        if raw.hasPrefix("anthropic.") { return "bedrock-regional" }
        return "anthropic"
    }

    // MARK: - Parsing (static for tests)

    private static let assistantNeedle = Array(#""type":"assistant""#.utf8)
    private static let cwdNeedle = Array(#""cwd":""#.utf8)

    /// Usage records of one transcript (deduped within the file) and its first cwd. Streams the file
    /// in 1 MB chunks and only JSON-parses lines that look like assistant messages. With `resume`
    /// (an earlier parse of the same file), reads only what was appended since.
    static func parseFile(path: String, resume: FileEntry? = nil) -> FileEntry {
        var entry = FileEntry(size: 0, mtime: .distantPast, records: [], firstCwd: nil, parsedBytes: 0)
        guard let fh = FileHandle(forReadingAtPath: path) else { return entry }
        defer { try? fh.close() }
        if let resume, resume.parsedBytes > 0 {
            // Only if the byte before the resume point is still the newline we stopped after
            if (try? fh.seek(toOffset: UInt64(resume.parsedBytes - 1))) != nil,
               (try? fh.read(upToCount: 1)) == Data([0x0A]) {
                entry = resume
            } else {
                try? fh.seek(toOffset: 0)
            }
        }
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let isoBasic = ISO8601DateFormatter()
        isoBasic.formatOptions = [.withInternetDateTime]

        var records = entry.records
        var index = Dictionary(records.enumerated().map { ($1.id, $0) }, uniquingKeysWith: { a, _ in a })
        var firstCwd = entry.firstCwd
        var consumed = entry.parsedBytes

        func handle(_ line: UnsafeRawBufferPointer) {
            guard line.count > 2 else { return }
            let isAssistant = contains(line, assistantNeedle)
            guard isAssistant || (firstCwd == nil && contains(line, cwdNeedle)) else { return }
            guard let json = try? JSONSerialization.jsonObject(with: Data(line)) as? [String: Any] else { return }
            let cwd = (json["cwd"] as? String).flatMap { $0.isEmpty ? nil : $0 }
            if firstCwd == nil, let cwd { firstCwd = cwd }
            guard isAssistant, let r = record(json, iso: iso, isoBasic: isoBasic, cwd: cwd) else { return }
            if let i = index[r.id] {
                records[i] = merge(records[i], r)
            } else {
                index[r.id] = records.count
                records.append(r)
            }
        }

        var pending: [UInt8] = []
        while let chunk = try? fh.read(upToCount: 1 << 20), !chunk.isEmpty {
            consumed += chunk.count
            chunk.withUnsafeBytes { (buf: UnsafeRawBufferPointer) in
                guard let base = buf.baseAddress else { return }
                var off = 0
                while off < buf.count {
                    guard let nl = memchr(base + off, 0x0A, buf.count - off) else {
                        pending.append(contentsOf: UnsafeRawBufferPointer(rebasing: buf[off...]))
                        return
                    }
                    let end = base.distance(to: UnsafeRawPointer(nl))
                    let piece = UnsafeRawBufferPointer(rebasing: buf[off..<end])
                    if pending.isEmpty {
                        handle(piece)
                    } else {
                        pending.append(contentsOf: piece)
                        pending.withUnsafeBytes { handle($0) }
                        pending.removeAll(keepingCapacity: true)
                    }
                    off = end + 1
                }
            }
        }
        consumed -= pending.count
        // An unterminated last line is counted if it's complete JSON, and re-read on the next parse
        if !pending.isEmpty { pending.withUnsafeBytes { handle($0) } }
        entry.records = records
        entry.firstCwd = firstCwd
        entry.parsedBytes = consumed
        return entry
    }

    private static func contains(_ hay: UnsafeRawBufferPointer, _ needle: [UInt8]) -> Bool {
        guard let base = hay.baseAddress, hay.count >= needle.count else { return false }
        return needle.withUnsafeBytes { n in memmem(base, hay.count, n.baseAddress!, n.count) != nil }
    }

    /// The usage record of one parsed `assistant` line, or nil (no usage, synthetic, no timestamp).
    static func record(_ json: [String: Any], iso: ISO8601DateFormatter, isoBasic: ISO8601DateFormatter,
                       cwd: String?) -> UsageRecord? {
        guard json["type"] as? String == "assistant",
              let message = json["message"] as? [String: Any],
              let usage = message["usage"] as? [String: Any],
              let rawModel = message["model"] as? String, rawModel != "<synthetic>",
              let ts = json["timestamp"] as? String,
              let date = iso.date(from: ts) ?? isoBasic.date(from: ts) else { return nil }
        let id = message["id"] as? String ?? (json["uuid"] as? String ?? UUID().uuidString)
        let write = usage["cache_creation_input_tokens"] as? Int ?? 0
        let split = usage["cache_creation"] as? [String: Any]
        let write1h = split?["ephemeral_1h_input_tokens"] as? Int ?? 0
        // The Bedrock response's model drops the region prefix; requestedModel keeps it.
        let requested = (json["requestedModel"] as? String).flatMap { $0.isEmpty ? nil : $0 }
        return UsageRecord(
            id: id, timestamp: date, model: canonicalModel(rawModel),
            provider: provider(requested ?? rawModel),
            project: cwd.map(projectName),
            inputTokens: usage["input_tokens"] as? Int ?? 0,
            outputTokens: usage["output_tokens"] as? Int ?? 0,
            cacheReadTokens: usage["cache_read_input_tokens"] as? Int ?? 0,
            cacheWriteTokens: write,
            cacheWrite1hTokens: write1h)
    }

    // MARK: - Aggregation

    static func calendar(_ tz: TimeZone) -> Calendar {
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = tz
        return cal
    }

    static func dayKey(_ date: Date, calendar cal: Calendar) -> String {
        let c = cal.dateComponents([.year, .month, .day], from: date)
        return String(format: "%04d-%02d-%02d", c.year ?? 0, c.month ?? 0, c.day ?? 0)
    }

    /// Per-day, per-model and per-project totals of records from `from` through today in `timeZone`.
    static func summarize(records: [UsageRecord], timeZone: TimeZone, from: Date, now: Date) -> UsageSummary {
        let cal = calendar(timeZone)
        let today = dayKey(now, calendar: cal)
        var days: [String: (models: [String: UsageTotals], projects: [String: UsageTotals])] = [:]
        var providers: [String: UsageTotals] = [:]
        var unpriced = Set<String>()
        for r in records where r.timestamp >= from {
            let key = dayKey(r.timestamp, calendar: cal)
            guard key <= today else { continue }
            let cost = UsagePricing.cost(r)
            if cost == nil { unpriced.insert(r.model) }
            var day = days[key] ?? ([:], [:])
            day.models[r.model, default: UsageTotals()].add(r, cost: cost ?? 0)
            day.projects[r.project ?? "unknown", default: UsageTotals()].add(r, cost: cost ?? 0)
            days[key] = day
            providers[r.provider, default: UsageTotals()].add(r, cost: cost ?? 0)
        }
        return UsageSummary(
            days: days.keys.sorted().map { key in
                UsageDay(date: key,
                         byModel: days[key]!.models.mapValues { $0.rounded() },
                         byProject: days[key]!.projects.mapValues { $0.rounded() })
            },
            byProvider: providers.mapValues { $0.rounded() },
            unpricedModels: unpriced.sorted())
    }

    // MARK: - Query

    enum QueryError: Error, Equatable { case badTimeZone, badFrom }

    static let defaultLookbackDays = 31
    static let maxLookbackDays = 400

    /// `tz` (IANA id, default the server's zone) and `from` (yyyy-MM-dd in tz, default 31 days ago,
    /// at most 400 days back) -> the zone and the instant the range starts.
    static func parseQuery(tz: String?, from: String?, now: Date = Date()) -> Result<(TimeZone, Date), QueryError> {
        let zone: TimeZone
        if let tz, !tz.isEmpty {
            guard let z = TimeZone(identifier: tz) else { return .failure(.badTimeZone) }
            zone = z
        } else {
            zone = .current
        }
        let cal = calendar(zone)
        let startOfToday = cal.startOfDay(for: now)
        let earliest = cal.date(byAdding: .day, value: -maxLookbackDays, to: startOfToday)!
        var start: Date
        if let from, !from.isEmpty {
            let parts = from.split(separator: "-", omittingEmptySubsequences: false)
            guard parts.count == 3, parts[0].count == 4, parts[1].count == 2, parts[2].count == 2,
                  let y = Int(parts[0]), let m = Int(parts[1]), let d = Int(parts[2]),
                  let date = cal.date(from: DateComponents(year: y, month: m, day: d)),
                  case let c = cal.dateComponents([.year, .month, .day], from: date),
                  c.year == y, c.month == m, c.day == d
            else { return .failure(.badFrom) }
            start = cal.startOfDay(for: date)
        } else {
            start = cal.date(byAdding: .day, value: -defaultLookbackDays, to: startOfToday)!
        }
        start = max(start, earliest)
        return .success((zone, start))
    }
}
