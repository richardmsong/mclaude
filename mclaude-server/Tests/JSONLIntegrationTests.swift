import XCTest
@testable import mclaude_server

/// Integration tests that feed real JSONL lines (from actual Claude Code sessions)
/// through the full getRecentEvents pipeline. These test the complete path:
/// JSONL file → JSONLParser.parseEvent → event list with dedup.
///
/// These tests were designed to run against BOTH the pre-refactor (duplicated parsers)
/// and post-refactor (shared JSONLParser) code to verify behavior equivalence.
final class JSONLIntegrationTests: XCTestCase {

    private var tempDir: String!
    private var tailer: JSONLTailer!

    override func setUp() async throws {
        tempDir = NSTemporaryDirectory() + "mclaude-test-\(UUID().uuidString)"
        try FileManager.default.createDirectory(atPath: tempDir, withIntermediateDirectories: true)
        tailer = JSONLTailer(projectsDir: tempDir)
    }

    override func tearDown() async throws {
        try? FileManager.default.removeItem(atPath: tempDir)
    }

    /// Write lines to a temp JSONL file and fetch events through getRecentEvents
    private func eventsFromLines(_ lines: [String]) async -> [SessionEvent] {
        let cwd = "/test/project"
        let sessionId = "test-session"
        let encodedPath = cwd.replacingOccurrences(of: "/", with: "-")
        let dir = "\(tempDir!)/\(encodedPath)"
        try! FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        let content = lines.joined(separator: "\n") + "\n"
        let path = "\(dir)/\(sessionId).jsonl"
        try! content.write(toFile: path, atomically: true, encoding: .utf8)
        return await tailer.getRecentEvents(id: "1", sessionId: sessionId, cwd: cwd)
    }

    // MARK: - Real JSONL line tests

    func testRealUserTextMessage() async {
        let line = """
        {"parentUuid":null,"isSidechain":false,"promptId":"9bd4d4e1-ea0a-40c2-9e81-a91a0b57d9cf","type":"user","message":{"role":"user","content":"im exploring ways to handle my multi-claude setup on mobile. just as a reminder:"},"uuid":"fd9849f2-f90e-4ace-bb6c-186fca546935","timestamp":"2026-04-01T13:13:31.823Z","permissionMode":"default","userType":"external","entrypoint":"cli","cwd":"~/mclaude","sessionId":"e24635d4-d3e2-43c4-bc82-377e6ce6b8c3","version":"2.1.89","gitBranch":"HEAD"}
        """
        let events = await eventsFromLines([line])
        XCTAssertEqual(events.count, 1)
        XCTAssertEqual(events[0].type, .user)
        XCTAssertEqual(events[0].uuid, "fd9849f2-f90e-4ace-bb6c-186fca546935")
        XCTAssertEqual(events[0].text, "im exploring ways to handle my multi-claude setup on mobile. just as a reminder:")
        XCTAssertEqual(events[0].timestamp, "2026-04-01T13:13:31.823Z")
    }

    func testRealUserToolResult() async {
        let line = """
        {"parentUuid":"48504cee-4521-49e9-9af8-1abe0341fbc9","isSidechain":false,"promptId":"9bd4d4e1-ea0a-40c2-9e81-a91a0b57d9cf","type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"File does not exist. Note: your current working directory is ~/mclaude.","is_error":true,"tool_use_id":"toolu_01VSrUFrHurzbCeSEyHQk9mb"}]},"uuid":"71087043-4618-4ac3-b67f-cc77df0a36a1","timestamp":"2026-04-01T13:13:37.059Z","toolUseResult":"Error: File does not exist.","sourceToolAssistantUUID":"48504cee-4521-49e9-9af8-1abe0341fbc9","userType":"external","entrypoint":"cli","cwd":"~/mclaude","sessionId":"e24635d4-d3e2-43c4-bc82-377e6ce6b8c3","version":"2.1.89","gitBranch":"HEAD","slug":"crystalline-wobbling-dream"}
        """
        let events = await eventsFromLines([line])
        XCTAssertEqual(events.count, 1)
        XCTAssertEqual(events[0].type, .toolResult)
        XCTAssertEqual(events[0].uuid, "71087043-4618-4ac3-b67f-cc77df0a36a1")
        XCTAssertEqual(events[0].toolResults?.count, 1)
        XCTAssertEqual(events[0].toolResults?[0].isError, true)
        XCTAssertTrue(events[0].toolResults?[0].content.contains("File does not exist") ?? false)
    }

    func testRealAssistantText() async {
        let line = """
        {"parentUuid":"331f2db1-ed90-4167-b56f-efdf06469aa1","isSidechain":false,"message":{"model":"claude-opus-4-6","id":"msg_01LkFf6TEM2cX5xngiCiACE3","type":"message","role":"assistant","content":[{"type":"text","text":"Let me check my memory for context on your multi-claude setup."}],"stop_reason":null,"stop_sequence":null,"stop_details":null,"usage":{"input_tokens":3}},"requestId":"req_abc","type":"assistant","uuid":"26286178-7ecc-4abd-a054-1d66acb4c673","timestamp":"2026-04-01T13:13:36.476Z","userType":"external","entrypoint":"cli","cwd":"~/mclaude","sessionId":"e24635d4-d3e2-43c4-bc82-377e6ce6b8c3","version":"2.1.89","gitBranch":"HEAD"}
        """
        let events = await eventsFromLines([line])
        XCTAssertEqual(events.count, 1)
        XCTAssertEqual(events[0].type, .text)
        XCTAssertEqual(events[0].text, "Let me check my memory for context on your multi-claude setup.")
        XCTAssertEqual(events[0].model, "claude-opus-4-6")
        XCTAssertEqual(events[0].uuid, "26286178-7ecc-4abd-a054-1d66acb4c673")
    }

    func testRealAssistantToolUse() async {
        let line = """
        {"parentUuid":"ff009d91-16c2-480f-bced-9abffd5abe82","isSidechain":false,"message":{"model":"claude-opus-4-6","id":"msg_01HXM9CUkCLY4dCCCr41VS1b","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_01T46SonsHm4b4XcQrmX49AJ","name":"Bash","input":{"command":"ls ~/mclaude/"}}],"stop_reason":null,"stop_sequence":null,"stop_details":null,"usage":{"input_tokens":3}},"requestId":"req_xyz","type":"assistant","uuid":"dd6b3be5-0741-4682-9f09-bf132032facc","timestamp":"2026-04-01T13:15:45.110Z","userType":"external","entrypoint":"cli","cwd":"~/mclaude","sessionId":"e24635d4-d3e2-43c4-bc82-377e6ce6b8c3","version":"2.1.89","gitBranch":"HEAD"}
        """
        let events = await eventsFromLines([line])
        XCTAssertEqual(events.count, 1)
        XCTAssertEqual(events[0].type, .toolUse)
        XCTAssertEqual(events[0].toolUse?.name, "Bash")
        XCTAssertEqual(events[0].toolUse?.inputSummary, "ls ~/mclaude/")
        XCTAssertEqual(events[0].toolUse?.id, "toolu_01T46SonsHm4b4XcQrmX49AJ")
        XCTAssertNotNil(events[0].toolUse?.fullInput)
    }

    func testRealSystemTurnDuration() async {
        let line = """
        {"parentUuid":"d009df46-5c6e-4ade-aa0d-af1e4d387ec2","isSidechain":false,"type":"system","subtype":"turn_duration","durationMs":33963,"messageCount":33,"timestamp":"2026-04-01T13:16:46.129Z","uuid":"8c1743d4-a8bd-40c1-b333-e977db331368","isMeta":false,"userType":"external","entrypoint":"cli","cwd":"~/mclaude","sessionId":"e24635d4-d3e2-43c4-bc82-377e6ce6b8c3","version":"2.1.89","gitBranch":"HEAD","slug":"crystalline-wobbling-dream"}
        """
        let events = await eventsFromLines([line])
        XCTAssertEqual(events.count, 1)
        XCTAssertEqual(events[0].type, .system)
        XCTAssertEqual(events[0].durationMs, 33963)
        XCTAssertEqual(events[0].uuid, "8c1743d4-a8bd-40c1-b333-e977db331368")
    }

    func testRealQueueOperationEnqueue() async {
        let line = """
        {"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-01T18:47:22.980Z","sessionId":"e24635d4-d3e2-43c4-bc82-377e6ce6b8c3","content":"thorn"}
        """
        let events = await eventsFromLines([line])
        XCTAssertEqual(events.count, 1)
        XCTAssertEqual(events[0].type, .user)
        XCTAssertEqual(events[0].text, "thorn")
        XCTAssertTrue(events[0].uuid.hasPrefix("q-"))
    }

    func testRealCompactionEvent() async {
        let line = """
        {"parentUuid":"ed85b2cf-c1dd-49a2-8241-fb3ca7078b97","isSidechain":false,"promptId":"5d7fbbc3-1e2e-462e-b5e6-5e66997fe72f","type":"user","message":{"role":"user","content":"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion."},"isVisibleInTranscriptOnly":true,"isCompactSummary":true,"uuid":"d6e50229-8bd9-493e-b1f5-ba198768d84d","timestamp":"2026-04-02T00:14:27.316Z","userType":"external","entrypoint":"cli","cwd":"~/mclaude/MClaude","sessionId":"e24635d4-d3e2-43c4-bc82-377e6ce6b8c3","version":"2.1.89","gitBranch":"HEAD","slug":"crystalline-wobbling-dream"}
        """
        let events = await eventsFromLines([line])
        XCTAssertEqual(events.count, 1)
        XCTAssertEqual(events[0].type, .compaction)
        XCTAssertTrue(events[0].text?.hasPrefix("This session is being continued") ?? false)
        XCTAssertEqual(events[0].uuid, "d6e50229-8bd9-493e-b1f5-ba198768d84d")
    }

    func testRealImageMetadataFiltered() async {
        let line = """
        {"parentUuid":"da468ef4-1ba1-4db5-83f1-11b8ae644c8e","isSidechain":false,"promptId":"5a7fa5c0-7dd8-42a3-adc3-eb9d964d1cba","type":"user","message":{"role":"user","content":[{"type":"text","text":"[Image #1] whys the app so short?"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"abc"}}]},"uuid":"imgtest-1","timestamp":"2026-04-01T14:00:00.000Z"}
        """
        let events = await eventsFromLines([line])
        // Image metadata with [Image #1] prefix should NOT be filtered — only [Image: source: and [Image source: prefixes are filtered
        // This text has "[Image #1]" which is a user message with an image attachment, not metadata
        XCTAssertEqual(events.count, 1)
        XCTAssertEqual(events[0].type, .user)
        XCTAssertEqual(events[0].text, "[Image #1] whys the app so short?")
    }

    func testImageSourceMetadataFiltered() async {
        let line = """
        {"type":"user","uuid":"imgmeta-1","timestamp":"2026-04-01T14:00:00.000Z","message":{"role":"user","content":"[Image: source: /tmp/foo.png]"}}
        """
        let events = await eventsFromLines([line])
        XCTAssertEqual(events.count, 0, "Image source metadata should be filtered out")
    }

    // MARK: - Queue dedup integration

    func testQueueEnqueueThenDequeueDedup() async {
        // Simulates: user sends mid-turn message → enqueue, then message gets processed → dequeue + real user event
        // The real user event should be deduped, leaving only the synthetic queue event
        let lines = [
            """
            {"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-01T18:47:22.980Z","sessionId":"sess-1","content":"hello from queue"}
            """,
            """
            {"type":"queue-operation","operation":"dequeue","timestamp":"2026-04-01T18:47:25.000Z","sessionId":"sess-1"}
            """,
            """
            {"type":"user","uuid":"real-user-1","timestamp":"2026-04-01T18:47:25.100Z","message":{"role":"user","content":"hello from queue"}}
            """
        ]
        let events = await eventsFromLines(lines)
        // Should have 1 event (the synthetic from enqueue), not 2
        let userEvents = events.filter { $0.type == .user }
        XCTAssertEqual(userEvents.count, 1, "Duplicate user event after dequeue should be removed")
        XCTAssertTrue(userEvents[0].uuid.hasPrefix("q-"), "Surviving event should be the synthetic queue event")
        XCTAssertEqual(userEvents[0].text, "hello from queue")
    }

    func testQueueEnqueueWithRemoveKeepsSynthetic() async {
        // remove means message was consumed without a separate user event — synthetic should stand
        let lines = [
            """
            {"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-01T18:47:22.980Z","sessionId":"sess-1","content":"removed msg"}
            """,
            """
            {"type":"queue-operation","operation":"remove","timestamp":"2026-04-01T18:47:25.000Z","sessionId":"sess-1"}
            """
        ]
        let events = await eventsFromLines(lines)
        let userEvents = events.filter { $0.type == .user }
        XCTAssertEqual(userEvents.count, 1, "Enqueue with remove should keep the synthetic event")
        XCTAssertEqual(userEvents[0].text, "removed msg")
    }

    // MARK: - Multi-event sequence

    func testFullConversationSequence() async {
        // A realistic sequence: user message → assistant text → tool_use → tool_result → turn_duration
        let lines = [
            """
            {"type":"user","uuid":"u1","timestamp":"2026-04-01T00:00:01Z","message":{"role":"user","content":"Hello"}}
            """,
            """
            {"type":"assistant","uuid":"a1","timestamp":"2026-04-01T00:00:02Z","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"Let me help you."}]}}
            """,
            """
            {"type":"assistant","uuid":"a2","timestamp":"2026-04-01T00:00:03Z","message":{"model":"claude-opus-4-6","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/tmp/test.txt"}}]}}
            """,
            """
            {"type":"user","uuid":"u2","timestamp":"2026-04-01T00:00:04Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"file contents here","is_error":false}]}}
            """,
            """
            {"type":"system","subtype":"turn_duration","uuid":"s1","timestamp":"2026-04-01T00:00:05Z","durationMs":4000}
            """
        ]
        let events = await eventsFromLines(lines)
        XCTAssertEqual(events.count, 5)
        XCTAssertEqual(events[0].type, .user)
        XCTAssertEqual(events[0].text, "Hello")
        XCTAssertEqual(events[1].type, .text)
        XCTAssertEqual(events[1].text, "Let me help you.")
        XCTAssertEqual(events[2].type, .toolUse)
        XCTAssertEqual(events[2].toolUse?.name, "Read")
        XCTAssertEqual(events[2].toolUse?.inputSummary, "/tmp/test.txt")
        XCTAssertEqual(events[3].type, .toolResult)
        XCTAssertEqual(events[3].toolResults?[0].content, "file contents here")
        XCTAssertEqual(events[4].type, .system)
        XCTAssertEqual(events[4].durationMs, 4000)
    }

    // MARK: - Irrelevant lines skipped

    func testSkipsIrrelevantJSONLTypes() async {
        let lines = [
            // Valid user event
            """
            {"type":"user","uuid":"u1","timestamp":"2026-04-01T00:00:01Z","message":{"role":"user","content":"Hello"}}
            """,
            // Unknown type — should be skipped
            """
            {"type":"config","uuid":"c1","timestamp":"2026-04-01T00:00:02Z","data":{}}
            """,
            // queue-operation dequeue — no event
            """
            {"type":"queue-operation","operation":"dequeue","timestamp":"2026-04-01T00:00:03Z"}
            """,
            // system but not turn_duration — skipped
            """
            {"type":"system","subtype":"init","uuid":"s1","timestamp":"2026-04-01T00:00:04Z"}
            """,
            // Valid assistant text
            """
            {"type":"assistant","uuid":"a1","timestamp":"2026-04-01T00:00:05Z","message":{"content":[{"type":"text","text":"Hi"}]}}
            """
        ]
        let events = await eventsFromLines(lines)
        XCTAssertEqual(events.count, 2)
        XCTAssertEqual(events[0].type, .user)
        XCTAssertEqual(events[1].type, .text)
    }
}
