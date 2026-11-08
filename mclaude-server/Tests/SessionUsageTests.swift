import XCTest
@testable import mclaude_server

final class SessionUsageTests: XCTestCase {
    private func json(_ s: String) -> [String: Any] {
        try! JSONSerialization.jsonObject(with: Data(s.utf8)) as! [String: Any]
    }

    func testContextFromAssistantMessage() {
        var u = SessionUsage()
        u.noteAssistantMessage(usage: ["input_tokens": 10, "cache_creation_input_tokens": 23676,
                                       "cache_read_input_tokens": 1000, "output_tokens": 3],
                               model: "us.anthropic.claude-haiku-4-5-20251001-v1:0")
        XCTAssertEqual(u.contextTokens, 24689)
        XCTAssertEqual(u.contextWindow, 200_000)
    }

    func testOneMillionContextGuess() {
        XCTAssertEqual(SessionUsage.guessContextWindow(model: "claude-opus-4-6[1m]"), 1_000_000)
        XCTAssertEqual(SessionUsage.guessContextWindow(model: "claude-sonnet-4-5"), 200_000)
    }

    func testResultAccumulatesPerTurnDeltas() {
        var u = SessionUsage()
        u.noteAssistantMessage(usage: ["input_tokens": 5, "output_tokens": 1], model: "us.anthropic.claude-haiku-4-5-20251001-v1:0")
        let result = json("""
        {"type":"result","total_cost_usd":0.03,"model_usage_since_last_result":{"us.anthropic.claude-haiku-4-5-20251001-v1:0":
          {"inputTokens":10,"outputTokens":72,"cacheReadInputTokens":0,"cacheCreationInputTokens":23676,
           "costUSD":0.029965,"contextWindow":200000,"canonicalModel":"claude-haiku-4-5"}}}
        """)
        u.addResult(result)
        u.addResult(result)
        XCTAssertEqual(u.turns, 2)
        XCTAssertEqual(u.inputTokens, 20)
        XCTAssertEqual(u.outputTokens, 144)
        XCTAssertEqual(u.cacheWriteTokens, 47352)
        XCTAssertEqual(u.costUSD ?? 0, 0.05993, accuracy: 1e-9)
        XCTAssertEqual(u.byModel["claude-haiku-4-5"]?.outputTokens, 144)
        XCTAssertEqual(u.contextWindow, 200_000)
        XCTAssertEqual(u.totalTokens, 20 + 144 + 47352)
    }

    func testBackfillDedupesContentBlocksAndSkipsSidechainForContext() throws {
        let dir = NSTemporaryDirectory() + "usage-\(UUID().uuidString)"
        try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(atPath: dir) }
        let path = dir + "/t.jsonl"
        let lines = [
            #"{"type":"assistant","message":{"id":"m1","model":"claude-sonnet-4-5","usage":{"input_tokens":100,"cache_read_input_tokens":0,"cache_creation_input_tokens":5000,"output_tokens":50}}}"#,
            #"{"type":"assistant","message":{"id":"m1","model":"claude-sonnet-4-5","usage":{"input_tokens":100,"cache_read_input_tokens":0,"cache_creation_input_tokens":5000,"output_tokens":50}}}"#,
            #"{"type":"system","subtype":"turn_duration","durationMs":1000}"#,
            #"{"type":"assistant","message":{"id":"m2","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"cache_read_input_tokens":5100,"cache_creation_input_tokens":200,"output_tokens":30}}}"#,
            #"{"type":"assistant","isSidechain":true,"message":{"id":"s1","model":"claude-haiku-4-5","usage":{"input_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":9}}}"#,
        ]
        try lines.joined(separator: "\n").write(toFile: path, atomically: true, encoding: .utf8)
        let u = try XCTUnwrap(SessionUsage.backfill(transcriptPath: path))
        XCTAssertEqual(u.inputTokens, 111)            // m1 counted once + m2 + sidechain
        XCTAssertEqual(u.outputTokens, 89)
        XCTAssertEqual(u.cacheReadTokens, 5100)
        XCTAssertEqual(u.cacheWriteTokens, 5200)
        XCTAssertEqual(u.contextTokens, 10 + 5100 + 200 + 30)   // last main-thread message, not the sidechain
        XCTAssertEqual(u.turns, 1)
        XCTAssertNil(u.costUSD)
        XCTAssertEqual(u.partial, true)
    }

    func testWindowGuessForCurrentModelsAndOverflow() {
        XCTAssertEqual(SessionUsage.guessContextWindow(model: "claude-opus-5-5"), 1_000_000)
        XCTAssertEqual(SessionUsage.guessContextWindow(model: "claude-haiku-4-5"), 200_000)
        // A context bigger than the guess proves the window is larger
        XCTAssertEqual(SessionUsage.guessContextWindow(model: "claude-haiku-4-5", contextTokens: 702_586), 1_000_000)
    }

    func testLearnedWindowOverridesGuess() {
        var u = SessionUsage()
        u.noteAssistantMessage(usage: ["input_tokens": 10, "cache_read_input_tokens": 100_000], model: "claude-opus-5-5")
        u.refreshContextWindow(known: ["claude-opus-5-5": 1_000_000])
        XCTAssertEqual(u.contextWindow, 1_000_000)
        var h = SessionUsage()
        h.noteAssistantMessage(usage: ["input_tokens": 10], model: "us.anthropic.claude-haiku-4-5-20251001-v1:0")
        h.refreshContextWindow(known: ["claude-haiku-4-5": 200_000])
        XCTAssertEqual(h.contextWindow, 200_000)
    }
}
