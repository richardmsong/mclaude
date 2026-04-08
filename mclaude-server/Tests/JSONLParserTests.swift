import XCTest
@testable import mclaude_server

final class JSONLParserTests: XCTestCase {

    // MARK: - User events

    func testUserTextMessage() {
        let line = """
        {"type":"user","uuid":"abc-123","timestamp":"2026-04-01T00:00:00Z","message":{"content":"Hello world"}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertNotNil(event)
        XCTAssertEqual(event?.type, .user)
        XCTAssertEqual(event?.uuid, "abc-123")
        XCTAssertEqual(event?.text, "Hello world")
        XCTAssertEqual(event?.timestamp, "2026-04-01T00:00:00Z")
    }

    func testUserTextFromArray() {
        let line = """
        {"type":"user","uuid":"abc-456","timestamp":"2026-04-01T00:00:00Z","message":{"content":[{"type":"text","text":"Array text"}]}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.type, .user)
        XCTAssertEqual(event?.text, "Array text")
    }

    func testUserToolResult() {
        let line = """
        {"type":"user","uuid":"tr-1","timestamp":"2026-04-01T00:00:00Z","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"result text","is_error":false}]}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.type, .toolResult)
        XCTAssertEqual(event?.toolResults?.count, 1)
        XCTAssertEqual(event?.toolResults?.first?.content, "result text")
        XCTAssertEqual(event?.toolResults?.first?.isError, false)
    }

    func testUserToolResultError() {
        let line = """
        {"type":"user","uuid":"tr-2","timestamp":"2026-04-01T00:00:00Z","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"error msg","is_error":true}]}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.type, .toolResult)
        XCTAssertEqual(event?.toolResults?.first?.isError, true)
    }

    func testUserMixedContent() {
        let line = """
        {"type":"user","uuid":"mix-1","timestamp":"2026-04-01T00:00:00Z","message":{"content":[{"type":"text","text":"user msg"},{"type":"tool_result","tool_use_id":"t1","content":"result","is_error":false}]}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.type, .user)
        XCTAssertEqual(event?.text, "user msg")
        XCTAssertEqual(event?.toolResults?.count, 1)
    }

    func testSkipsImageMetadata() {
        let line1 = """
        {"type":"user","uuid":"img-1","timestamp":"2026-04-01T00:00:00Z","message":{"content":"[Image: source: /tmp/foo.png]"}}
        """
        let line2 = """
        {"type":"user","uuid":"img-2","timestamp":"2026-04-01T00:00:00Z","message":{"content":"[Image source: /tmp/bar.png]"}}
        """
        XCTAssertNil(JSONLParser.parseEvent(line: line1))
        XCTAssertNil(JSONLParser.parseEvent(line: line2))
    }

    func testSkipsLocalCommand() {
        let line = """
        {"type":"user","uuid":"cmd-1","timestamp":"2026-04-01T00:00:00Z","message":{"content":"<local-command-result>foo</local-command-result>"}}
        """
        XCTAssertNil(JSONLParser.parseEvent(line: line))
    }

    func testSkipsCommandName() {
        let line = """
        {"type":"user","uuid":"cn-1","timestamp":"2026-04-01T00:00:00Z","message":{"content":"<command-name>bash</command-name>"}}
        """
        XCTAssertNil(JSONLParser.parseEvent(line: line))
    }

    func testCompactionEvent() {
        let line = """
        {"type":"user","uuid":"comp-1","timestamp":"2026-04-01T00:00:00Z","message":{"content":"This session is being continued from a previous conversation that ran out of context. Summary here."}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.type, .compaction)
        XCTAssertTrue(event?.text?.hasPrefix("This session is being continued") ?? false)
    }

    func testEmptyUserText() {
        let line = """
        {"type":"user","uuid":"e-1","timestamp":"2026-04-01T00:00:00Z","message":{"content":""}}
        """
        XCTAssertNil(JSONLParser.parseEvent(line: line))
    }

    // MARK: - Assistant events

    func testAssistantText() {
        let line = """
        {"type":"assistant","uuid":"ast-1","timestamp":"2026-04-01T00:00:00Z","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"Hello!"}]}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.type, .text)
        XCTAssertEqual(event?.text, "Hello!")
        XCTAssertEqual(event?.model, "claude-opus-4-6")
    }

    func testAssistantThinking() {
        let line = """
        {"type":"assistant","uuid":"think-1","timestamp":"2026-04-01T00:00:00Z","message":{"content":[{"type":"thinking","thinking":"Let me consider..."}]}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.type, .thinking)
        XCTAssertEqual(event?.thinking, "Let me consider...")
    }

    func testSkipsEmptyThinking() {
        let line = """
        {"type":"assistant","uuid":"think-2","timestamp":"2026-04-01T00:00:00Z","message":{"content":[{"type":"thinking","thinking":""}]}}
        """
        XCTAssertNil(JSONLParser.parseEvent(line: line))
    }

    func testAssistantToolUse() {
        let line = """
        {"type":"assistant","uuid":"tu-1","timestamp":"2026-04-01T00:00:00Z","message":{"model":"claude-opus-4-6","content":[{"type":"tool_use","id":"tool-abc","name":"Bash","input":{"command":"ls -la"}}]}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.type, .toolUse)
        XCTAssertEqual(event?.toolUse?.name, "Bash")
        XCTAssertEqual(event?.toolUse?.inputSummary, "ls -la")
        XCTAssertEqual(event?.toolUse?.id, "tool-abc")
        XCTAssertNotNil(event?.toolUse?.fullInput)
    }

    func testToolUseFilePath() {
        let line = """
        {"type":"assistant","uuid":"tu-2","timestamp":"2026-04-01T00:00:00Z","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/tmp/foo.swift"}}]}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.toolUse?.inputSummary, "/tmp/foo.swift")
    }

    func testToolUsePattern() {
        let line = """
        {"type":"assistant","uuid":"tu-3","timestamp":"2026-04-01T00:00:00Z","message":{"content":[{"type":"tool_use","id":"t1","name":"Grep","input":{"pattern":"func.*test"}}]}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.toolUse?.inputSummary, "func.*test")
    }

    func testSkipsEmptyAssistantText() {
        let line = """
        {"type":"assistant","uuid":"ast-2","timestamp":"2026-04-01T00:00:00Z","message":{"content":[{"type":"text","text":""}]}}
        """
        XCTAssertNil(JSONLParser.parseEvent(line: line))
    }

    // MARK: - System events

    func testSystemTurnDuration() {
        let line = """
        {"type":"system","subtype":"turn_duration","uuid":"sys-1","timestamp":"2026-04-01T00:00:00Z","durationMs":5000}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.type, .system)
        XCTAssertEqual(event?.durationMs, 5000)
    }

    func testSkipsOtherSystemEvents() {
        let line = """
        {"type":"system","subtype":"other","uuid":"sys-2","timestamp":"2026-04-01T00:00:00Z"}
        """
        XCTAssertNil(JSONLParser.parseEvent(line: line))
    }

    // MARK: - Queue operations

    func testQueueEnqueue() {
        let line = """
        {"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-01T00:00:00Z","sessionId":"sess-1","content":"Hello from queue"}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.type, .user)
        XCTAssertEqual(event?.text, "Hello from queue")
        XCTAssertTrue(event?.uuid.hasPrefix("q-") ?? false)
    }

    func testQueueDequeue() {
        let line = """
        {"type":"queue-operation","operation":"dequeue","timestamp":"2026-04-01T00:00:00Z"}
        """
        XCTAssertNil(JSONLParser.parseEvent(line: line))
    }

    func testQueueRemove() {
        let line = """
        {"type":"queue-operation","operation":"remove","timestamp":"2026-04-01T00:00:00Z"}
        """
        XCTAssertNil(JSONLParser.parseEvent(line: line))
    }

    func testQueueEmptyContent() {
        let line = """
        {"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-01T00:00:00Z","content":""}
        """
        XCTAssertNil(JSONLParser.parseEvent(line: line))
    }

    // MARK: - Edge cases

    func testInvalidJSON() {
        XCTAssertNil(JSONLParser.parseEvent(line: "not json"))
    }

    func testMissingType() {
        XCTAssertNil(JSONLParser.parseEvent(line: "{\"uuid\":\"a\",\"timestamp\":\"t\"}"))
    }

    func testMissingTimestamp() {
        XCTAssertNil(JSONLParser.parseEvent(line: "{\"type\":\"user\",\"uuid\":\"a\"}"))
    }

    func testUnknownType() {
        let line = """
        {"type":"unknown","uuid":"u-1","timestamp":"2026-04-01T00:00:00Z"}
        """
        XCTAssertNil(JSONLParser.parseEvent(line: line))
    }

    // MARK: - Bash commands (! prefix)

    func testBashInput() {
        let line = """
        {"type":"user","uuid":"bi-1","timestamp":"2026-04-01T00:00:00Z","message":{"content":"<bash-input> gh auth token</bash-input>"}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.type, .toolUse)
        XCTAssertEqual(event?.toolUse?.name, "!")
        XCTAssertEqual(event?.toolUse?.inputSummary, "gh auth token")
    }

    func testBashOutput() {
        let line = """
        {"type":"user","uuid":"bo-1","timestamp":"2026-04-01T00:00:00Z","message":{"content":"<bash-stdout>some output</bash-stdout><bash-stderr></bash-stderr>"}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.type, .toolResult)
        XCTAssertEqual(event?.toolResults?.first?.content, "some output")
        XCTAssertEqual(event?.toolResults?.first?.isError, false)
    }

    func testBashStderrOnly() {
        let line = """
        {"type":"user","uuid":"bo-2","timestamp":"2026-04-01T00:00:00Z","message":{"content":"<bash-stdout></bash-stdout><bash-stderr>error msg</bash-stderr>"}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.type, .toolResult)
        XCTAssertEqual(event?.toolResults?.first?.content, "error msg")
        XCTAssertEqual(event?.toolResults?.first?.isError, true)
    }

    func testBashEmptyOutput() {
        let line = """
        {"type":"user","uuid":"bo-3","timestamp":"2026-04-01T00:00:00Z","message":{"content":"<bash-stdout></bash-stdout><bash-stderr></bash-stderr>"}}
        """
        XCTAssertNil(JSONLParser.parseEvent(line: line))
    }

    // MARK: - extractTagContent

    func testExtractTagContent() {
        XCTAssertEqual(JSONLParser.extractTagContent("<foo>bar</foo>", tag: "foo"), "bar")
        XCTAssertEqual(JSONLParser.extractTagContent("<a>x</a><b>y</b>", tag: "b"), "y")
        XCTAssertEqual(JSONLParser.extractTagContent("no tags", tag: "foo"), "")
    }

    // MARK: - extractToolResultContent

    func testToolResultString() {
        XCTAssertEqual(JSONLParser.extractToolResultContent("hello"), "hello")
    }

    func testToolResultArray() {
        let arr: [[String: Any]] = [
            ["type": "text", "text": "line 1"],
            ["type": "text", "text": "line 2"],
            ["type": "image", "data": "..."]
        ]
        XCTAssertEqual(JSONLParser.extractToolResultContent(arr), "line 1\nline 2")
    }

    func testToolResultNil() {
        XCTAssertEqual(JSONLParser.extractToolResultContent(nil), "")
    }

    // MARK: - Deterministic UUID

    func testQueueUuidDeterministic() {
        let line = """
        {"type":"queue-operation","operation":"enqueue","timestamp":"2026-04-01T00:00:00Z","content":"test"}
        """
        let event1 = JSONLParser.parseEvent(line: line)
        let event2 = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event1?.uuid, event2?.uuid)
    }
}
