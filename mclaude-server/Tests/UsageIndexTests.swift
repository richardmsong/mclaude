import XCTest
@testable import mclaude_server

final class UsageIndexTests: XCTestCase {
    private var dir: String!

    override func setUp() {
        dir = NSTemporaryDirectory() + "usage-index-\(UUID().uuidString)"
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
    }

    override func tearDown() {
        try? FileManager.default.removeItem(atPath: dir)
    }

    private func line(_ obj: [String: Any]) -> String {
        String(data: try! JSONSerialization.data(withJSONObject: obj), encoding: .utf8)!
    }

    private func assistant(id: String, ts: String, model: String = "claude-opus-5-5", cwd: String? = "/Users/me/work/proj",
                           input: Int = 0, output: Int = 0, cacheRead: Int = 0, cacheWrite: Int = 0,
                           write5m: Int? = nil, write1h: Int? = nil, requestedModel: String? = nil,
                           block: String = "text") -> String {
        var usage: [String: Any] = ["input_tokens": input, "output_tokens": output,
                                    "cache_read_input_tokens": cacheRead, "cache_creation_input_tokens": cacheWrite]
        if write5m != nil || write1h != nil {
            usage["cache_creation"] = ["ephemeral_5m_input_tokens": write5m ?? 0, "ephemeral_1h_input_tokens": write1h ?? 0]
        }
        var obj: [String: Any] = [
            "type": "assistant", "timestamp": ts, "uuid": UUID().uuidString,
            "message": ["id": id, "model": model, "role": "assistant", "usage": usage,
                        "content": [["type": block, "text": "hi"]]],
        ]
        if let cwd { obj["cwd"] = cwd }
        if let requestedModel { obj["requestedModel"] = requestedModel }
        return line(obj)
    }

    @discardableResult
    private func write(_ relPath: String, _ lines: [String]) -> String {
        let path = "\(dir!)/\(relPath)"
        try? FileManager.default.createDirectory(atPath: (path as NSString).deletingLastPathComponent,
                                                 withIntermediateDirectories: true)
        FileManager.default.createFile(atPath: path, contents: Data((lines.joined(separator: "\n") + "\n").utf8))
        return path
    }

    private func record(model: String = "claude-opus-5-5", provider: String = "anthropic", input: Int = 0,
                        output: Int = 0, cacheRead: Int = 0, cacheWrite: Int = 0, write1h: Int = 0,
                        ts: Date = Date(), project: String = "p") -> UsageRecord {
        UsageRecord(id: UUID().uuidString, timestamp: ts, model: model, provider: provider, project: project,
                    inputTokens: input, outputTokens: output, cacheReadTokens: cacheRead,
                    cacheWriteTokens: cacheWrite, cacheWrite1hTokens: write1h)
    }

    private let utc = TimeZone(identifier: "UTC")!
    private let distantPast = Date(timeIntervalSince1970: 0)
    private func date(_ s: String) -> Date { ISO8601DateFormatter().date(from: s)! }

    // MARK: - Parsing and dedupe

    func testDedupesRepeatedContentBlockLines() {
        let path = write("-proj/a.jsonl", [
            line(["type": "user", "cwd": "/Users/me/work/proj", "message": ["role": "user", "content": "hi"]]),
            assistant(id: "msg_1", ts: "2026-11-04T10:00:00.000Z", input: 10, output: 5, cacheRead: 100, cacheWrite: 50, block: "thinking"),
            assistant(id: "msg_1", ts: "2026-11-04T10:00:01.000Z", input: 10, output: 5, cacheRead: 100, cacheWrite: 50, block: "text"),
            assistant(id: "msg_1", ts: "2026-11-04T10:00:02.000Z", input: 10, output: 5, cacheRead: 100, cacheWrite: 50, block: "tool_use"),
            assistant(id: "msg_2", ts: "2026-11-04T10:01:00.000Z", input: 1, output: 2),
            assistant(id: "msg_s", ts: "2026-11-04T10:02:00.000Z", model: "<synthetic>", input: 99, output: 99),
        ])
        let parsed = UsageIndex.parseFile(path: path)
        let (records, cwd) = (parsed.records, parsed.firstCwd)
        XCTAssertEqual(cwd, "/Users/me/work/proj")
        XCTAssertEqual(records.map(\.id), ["msg_1", "msg_2"])
        XCTAssertEqual(records[0].inputTokens, 10)
        XCTAssertEqual(records[0].cacheReadTokens, 100)
        XCTAssertEqual(records[0].project, "proj")
        XCTAssertEqual(records[0].timestamp, date("2026-11-04T10:00:00Z"))
    }

    func testDedupeAcrossFilesKeepsFinalCount() async {
        write("-proj/a.jsonl", [assistant(id: "msg_1", ts: "2026-11-04T10:00:00.000Z", output: 2248)])
        write("-proj/a/subagents/agent-x.jsonl", [assistant(id: "msg_1", ts: "2026-11-04T10:00:00.000Z", output: 17)])
        let index = UsageIndex(projectsDir: dir)
        let s = await index.summary(timeZone: utc, from: distantPast, now: date("2026-11-05T00:00:00Z"))
        XCTAssertEqual(s.days.count, 1)
        XCTAssertEqual(s.days[0].byModel["claude-opus-5-5"]?.outputTokens, 2248)
    }

    func testStreamsLinesAcrossChunkBoundariesAndUnterminatedLastLine() {
        let filler = line(["type": "user", "message": ["content": String(repeating: "x", count: 1_500_000)]])
        let lines = [filler, assistant(id: "msg_a", ts: "2026-11-04T10:00:00Z", output: 1), filler,
                     assistant(id: "msg_b", ts: "2026-11-04T10:00:00Z", output: 2)]
        let path = "\(dir!)/big.jsonl"
        FileManager.default.createFile(atPath: path, contents: Data(lines.joined(separator: "\n").utf8))
        XCTAssertEqual(UsageIndex.parseFile(path: path).records.map(\.id), ["msg_a", "msg_b"])
    }

    // MARK: - Model names and providers

    func testCanonicalModel() {
        XCTAssertEqual(UsageIndex.canonicalModel("us.anthropic.claude-haiku-4-5-20251001-v1:0"), "claude-haiku-4-5")
        XCTAssertEqual(UsageIndex.canonicalModel("anthropic.claude-haiku-4-5-20251001-v1:0"), "claude-haiku-4-5")
        XCTAssertEqual(UsageIndex.canonicalModel("eu.anthropic.claude-sonnet-4-5-20250929-v1:0"), "claude-sonnet-4-5")
        XCTAssertEqual(UsageIndex.canonicalModel("global.anthropic.claude-opus-4-8-v1"), "claude-opus-4-8")
        XCTAssertEqual(UsageIndex.canonicalModel("claude-sonnet-4-5-20250929"), "claude-sonnet-4-5")
        XCTAssertEqual(UsageIndex.canonicalModel("claude-opus-5-5"), "claude-opus-5-5")
    }

    func testProvider() {
        XCTAssertEqual(UsageIndex.provider("global.anthropic.claude-opus-5-5"), "bedrock-global")
        XCTAssertEqual(UsageIndex.provider("us.anthropic.claude-opus-5-5[1m]"), "bedrock-geo")
        XCTAssertEqual(UsageIndex.provider("apac.anthropic.claude-opus-5-5"), "bedrock-geo")
        XCTAssertEqual(UsageIndex.provider("anthropic.claude-haiku-4-5-20251001-v1:0"), "bedrock-regional")
        XCTAssertEqual(UsageIndex.provider("claude-sonnet-4-6"), "anthropic")
    }

    func testProviderComesFromRequestedModel() {
        // Bedrock responses report the bare model; the requested id keeps the inference-profile prefix.
        let path = write("-proj/a.jsonl", [
            assistant(id: "m1", ts: "2026-11-04T10:00:00Z", model: "claude-opus-5-5", requestedModel: "us.anthropic.claude-opus-5-5"),
            assistant(id: "m2", ts: "2026-11-04T10:00:00Z", model: "claude-opus-4-8"),
        ])
        let records = UsageIndex.parseFile(path: path).records
        XCTAssertEqual(records.map(\.provider), ["bedrock-geo", "anthropic"])
    }

    // MARK: - Pricing

    func testCostMath() {
        // Opus 5.5: 4 / 20 / 0.20, cache write 5m = 5.00
        let c = UsagePricing.cost(record(input: 1_000_000, output: 1_000_000, cacheRead: 1_000_000, cacheWrite: 1_000_000))!
        XCTAssertEqual(c, 4 + 20 + 0.2 + 5, accuracy: 1e-9)
        // Matches Claude Code's own cost-state for a Haiku 4.5 message
        let h = UsagePricing.cost(record(model: "claude-haiku-4-5", input: 10, output: 147, cacheWrite: 23532))!
        XCTAssertEqual(h, 0.03016, accuracy: 1e-9)
    }

    func testOneHourCacheWriteCostsDouble() {
        // 1M written: 400K at 1h (2x of $5) + 600K at 5m (1.25x of $5)
        let c = UsagePricing.cost(record(model: "claude-opus-4-8", cacheWrite: 1_000_000, write1h: 400_000))!
        XCTAssertEqual(c, 0.4 * 10 + 0.6 * 6.25, accuracy: 1e-9)
    }

    func testOneHourSplitParsedFromLine() {
        let path = write("-proj/a.jsonl", [
            assistant(id: "m1", ts: "2026-11-04T10:00:00Z", cacheWrite: 300, write5m: 100, write1h: 200),
            assistant(id: "m2", ts: "2026-11-04T10:00:00Z", cacheWrite: 300),
        ])
        let records = UsageIndex.parseFile(path: path).records
        XCTAssertEqual(records.map(\.cacheWrite1hTokens), [200, 0])
        XCTAssertEqual(records.map(\.cacheWriteTokens), [300, 300])
    }

    func testHaiku55LongPromptTier() {
        let short = UsagePricing.cost(record(model: "claude-haiku-5-5", input: 100_000, output: 1_000_000))!
        XCTAssertEqual(short, 0.1 * 0.1 + 0.5, accuracy: 1e-9)
        // Prompt = input + cache read + cache write = 100_001 > 100K: 0.50 / 2.50 / 0.05, write 0.625
        let long = UsagePricing.cost(record(model: "claude-haiku-5-5", input: 1, output: 1_000_000,
                                            cacheRead: 50_000, cacheWrite: 50_000))!
        XCTAssertEqual(long, 0.5e-6 + 2.5 + 0.05 * 0.05 + 0.05 * 0.625, accuracy: 1e-9)
    }

    func testFamilyPrefixMatching() {
        XCTAssertEqual(UsagePricing.price(provider: "anthropic", model: "claude-opus-4-8-something")?.base.input, 5)
        XCTAssertEqual(UsagePricing.price(provider: "anthropic", model: "claude-opus-5-5[1m]")?.base.input, 4)
        XCTAssertEqual(UsagePricing.price(provider: "anthropic", model: "claude-opus-4-1")?.base.input, 15)
        XCTAssertEqual(UsagePricing.price(provider: "anthropic", model: "claude-fable-5-1")?.base.cacheRead, 0.25)
        XCTAssertEqual(UsagePricing.price(provider: "anthropic", model: "claude-fable-5")?.base.cacheRead, 1.0)
        XCTAssertEqual(UsagePricing.price(provider: "bedrock-geo", model: "claude-sonnet-5")!.base.input, 2.2, accuracy: 1e-9)
        XCTAssertNil(UsagePricing.price(provider: "bedrock-geo", model: "claude-opus-4-9"))
        XCTAssertNil(UsagePricing.price(provider: "anthropic", model: "claude-opus-4-9"))
        XCTAssertNil(UsagePricing.price(provider: "anthropic", model: "claude-opus-5-7"))
    }

    // MARK: Bedrock rates

    private func mtok(_ provider: String, _ model: String, input: Int = 0, output: Int = 0, cacheRead: Int = 0,
                      cacheWrite: Int = 0, write1h: Int = 0) -> Double {
        UsagePricing.cost(record(model: model, provider: provider, input: input, output: output, cacheRead: cacheRead,
                                 cacheWrite: cacheWrite, write1h: write1h))!
    }

    func testBedrockGeoOpus55IsGlobalTimesOnePointOne() {
        let m = 1_000_000
        for provider in ["bedrock-geo", "bedrock-regional"] {
            XCTAssertEqual(mtok(provider, "claude-opus-5-5", input: m), 4.40, accuracy: 1e-9)
            XCTAssertEqual(mtok(provider, "claude-opus-5-5", output: m), 22.00, accuracy: 1e-9)
            XCTAssertEqual(mtok(provider, "claude-opus-5-5", cacheWrite: m), 5.50, accuracy: 1e-9)
            XCTAssertEqual(mtok(provider, "claude-opus-5-5", cacheWrite: m, write1h: m), 8.80, accuracy: 1e-9)
            XCTAssertEqual(mtok(provider, "claude-opus-5-5", cacheRead: m), 0.22, accuracy: 1e-9)
        }
        // Global is Anthropic's price
        XCTAssertEqual(mtok("bedrock-global", "claude-opus-5-5", input: m, output: m), 24, accuracy: 1e-9)
    }

    func testBedrockSonnet55CacheRead() {
        let m = 1_000_000
        XCTAssertEqual(mtok("bedrock-global", "claude-sonnet-5-5", cacheRead: m), 0.10, accuracy: 1e-9)
        XCTAssertEqual(mtok("bedrock-geo", "claude-sonnet-5-5", cacheRead: m), 0.11, accuracy: 1e-9)
        XCTAssertEqual(mtok("anthropic", "claude-sonnet-5-5", cacheRead: m), 0.20, accuracy: 1e-9)
    }

    func testBedrockRegionalOlderModelsHaveNoPremium() {
        let m = 1_000_000
        XCTAssertEqual(mtok("bedrock-regional", "claude-sonnet-4", input: m, output: m, cacheRead: m), 3 + 15 + 0.30, accuracy: 1e-9)
        XCTAssertEqual(mtok("bedrock-geo", "claude-opus-4-1", input: m), 15, accuracy: 1e-9)
        XCTAssertEqual(mtok("bedrock-geo", "claude-opus-4", output: m), 75, accuracy: 1e-9)
        // ...but Sonnet 4.5 does
        XCTAssertEqual(mtok("bedrock-regional", "claude-sonnet-4-5", input: m), 3.3, accuracy: 1e-9)
    }

    func testBedrockRegionalHaiku55LongPromptTier() {
        // Over 100K: global long-context rates 0.50 / 2.50 / 0.05, x 1.1 in-region
        let c = mtok("bedrock-geo", "claude-haiku-5-5", input: 200_000, output: 1_000_000)
        XCTAssertEqual(c, (0.2 * 0.5 + 2.5) * 1.1, accuracy: 1e-9)
    }

    func testByProviderKeepsGeoAndRegionalSeparate() {
        let ts = date("2026-11-04T10:00:00Z")
        let s = UsageIndex.summarize(records: [record(provider: "bedrock-geo", output: 1, ts: ts),
                                               record(provider: "bedrock-regional", output: 2, ts: ts),
                                               record(provider: "bedrock-global", output: 4, ts: ts)],
                                     timeZone: utc, from: distantPast, now: date("2026-11-04T12:00:00Z"))
        XCTAssertEqual(s.byProvider.mapValues(\.outputTokens), ["bedrock-geo": 1, "bedrock-regional": 2, "bedrock-global": 4])
        XCTAssertEqual(s.days[0].byModel["claude-opus-5-5"]?.outputTokens, 7)
    }

    func testUnpricedModelCountsTokensAtZeroCost() {
        let s = UsageIndex.summarize(records: [record(model: "claude-future-9", input: 5, output: 7, ts: date("2026-11-04T10:00:00Z"))],
                                     timeZone: utc, from: distantPast, now: date("2026-11-04T12:00:00Z"))
        XCTAssertEqual(s.unpricedModels, ["claude-future-9"])
        XCTAssertEqual(s.days[0].byModel["claude-future-9"], UsageTotals(inputTokens: 5, outputTokens: 7, costUSD: 0))
    }

    // MARK: - Aggregation

    func testTimeZoneDayBucketingAcrossMidnight() {
        // 03:30Z on Nov 4 is 23:30 on Nov 3 in New York (EDT ends Nov 1 2026 -> EST, UTC-5: 22:30)
        let records = [
            record(output: 1, ts: date("2026-11-04T03:30:00Z"), project: "a"),
            record(output: 2, ts: date("2026-11-04T05:30:00Z"), project: "b"),
        ]
        let now = date("2026-11-04T20:00:00Z")
        let ny = UsageIndex.summarize(records: records, timeZone: TimeZone(identifier: "America/New_York")!,
                                      from: distantPast, now: now)
        XCTAssertEqual(ny.days.map(\.date), ["2026-11-03", "2026-11-04"])
        XCTAssertEqual(ny.days[0].byProject["a"]?.outputTokens, 1)
        XCTAssertEqual(ny.days[1].byProject["b"]?.outputTokens, 2)
        let u = UsageIndex.summarize(records: records, timeZone: utc, from: distantPast, now: now)
        XCTAssertEqual(u.days.map(\.date), ["2026-11-04"])
        XCTAssertEqual(u.days[0].byModel["claude-opus-5-5"]?.outputTokens, 3)
        XCTAssertEqual(u.byProvider["anthropic"]?.outputTokens, 3)
    }

    func testFromAndTodayBounds() {
        let tz = TimeZone(identifier: "America/New_York")!
        let now = date("2026-11-04T20:00:00Z")
        guard case .success(let (zone, start)) = UsageIndex.parseQuery(tz: "America/New_York", from: "2026-11-04", now: now) else {
            return XCTFail("parse")
        }
        XCTAssertEqual(zone, tz)
        XCTAssertEqual(start, date("2026-11-04T05:00:00Z"))
        let records = [record(output: 1, ts: date("2026-11-04T04:59:00Z")), record(output: 2, ts: date("2026-11-04T05:00:00Z")),
                       record(output: 4, ts: date("2026-11-05T06:00:00Z"))]  // tomorrow in NY: excluded
        let s = UsageIndex.summarize(records: records, timeZone: zone, from: start, now: now)
        XCTAssertEqual(s.days.map(\.date), ["2026-11-04"])
        XCTAssertEqual(s.days[0].byModel["claude-opus-5-5"]?.outputTokens, 2)
    }

    func testParseQuery() {
        let now = date("2026-11-04T12:00:00Z")
        XCTAssertEqual(UsageIndex.parseQuery(tz: "Not/AZone", from: nil, now: now).failure, .badTimeZone)
        XCTAssertEqual(UsageIndex.parseQuery(tz: "UTC", from: "2026-13-01", now: now).failure, .badFrom)
        XCTAssertEqual(UsageIndex.parseQuery(tz: "UTC", from: "2026-02-30", now: now).failure, .badFrom)
        XCTAssertEqual(UsageIndex.parseQuery(tz: "UTC", from: "yesterday", now: now).failure, .badFrom)
        guard case .success(let (_, def)) = UsageIndex.parseQuery(tz: "UTC", from: nil, now: now),
              case .success(let (_, clamped)) = UsageIndex.parseQuery(tz: "UTC", from: "2020-01-01", now: now),
              case .success(let (zone, _)) = UsageIndex.parseQuery(tz: nil, from: nil, now: now) else { return XCTFail() }
        XCTAssertEqual(def, date("2026-10-04T00:00:00Z"))
        XCTAssertEqual(clamped, date("2025-09-30T00:00:00Z"))   // 400 days back
        XCTAssertEqual(zone, TimeZone.current)
    }

    // MARK: - Index

    func testIncludesSubagentsAndResolvesProjects() async {
        write("-proj/s1.jsonl", [
            line(["type": "user", "cwd": "/Users/me/work/mclaude-v1", "message": ["content": "hi"]]),
            assistant(id: "main", ts: "2026-11-04T10:00:00Z", cwd: nil, output: 1),
            assistant(id: "cd-sub", ts: "2026-11-04T10:00:01Z", cwd: "/Users/me/work/mclaude-v1/MClaude", output: 8),
        ])
        write("-proj/s1/subagents/agent-a.jsonl", [assistant(id: "sub", ts: "2026-11-04T10:00:00Z", cwd: nil, output: 2)])
        write("-proj/s1/subagents/agent-b.jsonl", [assistant(id: "sub2", ts: "2026-11-04T10:00:00Z", cwd: "/x/other", output: 4)])
        write("-proj/s1/tool-results/ignored.jsonl", [assistant(id: "nope", ts: "2026-11-04T10:00:00Z", output: 100)])
        let index = UsageIndex(projectsDir: dir)
        let s = await index.summary(timeZone: utc, from: distantPast, now: date("2026-11-05T00:00:00Z"))
        XCTAssertEqual(s.days.count, 1)
        // Everything counts toward the session's starting project, even lines whose cwd is a
        // subfolder (cd) or another folder (a subagent working elsewhere).
        XCTAssertEqual(s.days[0].byProject["mclaude-v1"]?.outputTokens, 15)
        XCTAssertNil(s.days[0].byProject["MClaude"])
        XCTAssertNil(s.days[0].byProject["other"])
        XCTAssertEqual(s.days[0].byModel["claude-opus-5-5"]?.outputTokens, 15)
    }

    func testCacheInvalidatesOnFileChange() async throws {
        let path = write("-proj/s1.jsonl", [assistant(id: "m1", ts: "2026-11-04T10:00:00Z", output: 1)])
        let index = UsageIndex(projectsDir: dir)
        let now = date("2026-11-05T00:00:00Z")
        var s = await index.summary(timeZone: utc, from: distantPast, now: now)
        var parsed = 0
        XCTAssertEqual(s.days[0].byModel["claude-opus-5-5"]?.outputTokens, 1)
        parsed = await index.lastParsedCount; XCTAssertEqual(parsed, 1)

        s = await index.summary(timeZone: utc, from: distantPast, now: now)
        parsed = await index.lastParsedCount; XCTAssertEqual(parsed, 0)   // unchanged: served from cache

        let fh = try XCTUnwrap(FileHandle(forWritingAtPath: path))
        fh.seekToEndOfFile()
        fh.write(Data((assistant(id: "m2", ts: "2026-11-04T11:00:00Z", output: 10) + "\n").utf8))
        try fh.close()
        s = await index.summary(timeZone: utc, from: distantPast, now: now)
        parsed = await index.lastParsedCount; XCTAssertEqual(parsed, 1)
        XCTAssertEqual(s.days[0].byModel["claude-opus-5-5"]?.outputTokens, 11)

        try FileManager.default.removeItem(atPath: path)
        s = await index.summary(timeZone: utc, from: distantPast, now: now)
        XCTAssertEqual(s.days, [])
    }

    func testIncrementalParseOfAppendAndFullParseOfRewrite() throws {
        let path = write("-proj/s1.jsonl", [assistant(id: "m1", ts: "2026-11-04T10:00:00Z", output: 1)])
        let first = UsageIndex.parseFile(path: path)
        XCTAssertEqual(first.parsedBytes, try FileManager.default.attributesOfItem(atPath: path)[.size] as? Int)

        // Append: the repeated content-block line of m1 and a new message, plus a partial line
        let fh = try XCTUnwrap(FileHandle(forWritingAtPath: path))
        fh.seekToEndOfFile()
        fh.write(Data((assistant(id: "m1", ts: "2026-11-04T10:00:05Z", output: 1) + "\n"
                       + assistant(id: "m2", ts: "2026-11-04T10:01:00Z", output: 2) + "\n{\"type\":\"assis").utf8))
        try fh.close()
        let appended = UsageIndex.parseFile(path: path, resume: first)
        XCTAssertEqual(appended.records.map(\.id), ["m1", "m2"])
        XCTAssertEqual(appended.records[0].timestamp, date("2026-11-04T10:00:00Z"))

        // Rewritten (no newline where the last parse stopped): parsed from the start
        write("-proj/s1.jsonl", [assistant(id: "m9", ts: "2026-11-04T10:00:00Z", cwd: "/a/much/longer/path/than/before", output: 9)])
        XCTAssertEqual(UsageIndex.parseFile(path: path, resume: first).records.map(\.id), ["m9"])
    }

    func testJSONShape() throws {
        let s = UsageIndex.summarize(records: [record(input: 1, output: 2, cacheRead: 3, cacheWrite: 4, ts: date("2026-11-04T10:00:00Z"))],
                                     timeZone: utc, from: distantPast, now: date("2026-11-04T12:00:00Z"))
        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(s)) as! [String: Any]
        let day = (json["days"] as! [[String: Any]])[0]
        XCTAssertEqual(day["date"] as? String, "2026-11-04")
        let m = (day["byModel"] as! [String: [String: Any]])["claude-opus-5-5"]!
        XCTAssertEqual(Set(m.keys), ["inputTokens", "outputTokens", "cacheReadTokens", "cacheWriteTokens", "costUSD"])
        XCTAssertNotNil((day["byProject"] as! [String: Any])["p"])
        XCTAssertNotNil((json["byProvider"] as! [String: Any])["anthropic"])
        XCTAssertEqual(json["unpricedModels"] as? [String], [])
    }
}

private extension Result {
    var failure: Failure? { if case .failure(let e) = self { return e } else { return nil } }
}
