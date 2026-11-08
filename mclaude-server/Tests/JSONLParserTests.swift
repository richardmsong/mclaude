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

    // MARK: - Local slash commands (docs/design-claude-uplift.md → Local slash commands)

    func testLocalCommandOutputFromTranscriptSystemLine() {
        let event = JSONLParser.parseEvent(line: """
        {"type":"system","subtype":"local_command","uuid":"lc-1","timestamp":"2026-11-08T03:50:58.870Z","content":"<local-command-stdout>\\u001b[1mReloaded: 7 plugins · 22 skills\\u001b[22m</local-command-stdout>","isMeta":false}
        """)
        XCTAssertEqual(event?.type, .text)
        XCTAssertEqual(event?.text, "Reloaded: 7 plugins · 22 skills")
        XCTAssertEqual(event?.model, "<synthetic>")
    }

    func testLocalCommandOutputFromUserLine() {
        let event = JSONLParser.parseEvent(line: """
        {"type":"user","uuid":"lc-2","timestamp":"t","message":{"role":"user","content":"<local-command-stdout>Total cost: $0.12</local-command-stdout>"}}
        """)
        XCTAssertEqual(event?.type, .text)
        XCTAssertEqual(event?.text, "Total cost: $0.12")
    }

    func testEmptyAndCompactedLocalCommandOutputDropped() {
        for content in ["<local-command-stdout>  </local-command-stdout>", "<local-command-stdout>Compacted </local-command-stdout>"] {
            let json: [String: Any] = ["type": "system", "subtype": "local_command", "uuid": "x", "timestamp": "t", "content": content]
            let line = String(data: try! JSONSerialization.data(withJSONObject: json), encoding: .utf8)!
            XCTAssertNil(JSONLParser.parseEvent(line: line), content)
        }
    }

    func testLocalCommandEchoFromLiveSyntheticMessage() {
        XCTAssertEqual(JSONLParser.localCommandEcho(["local_command_run": ["command": "reload-plugins", "args": ""]]),
                       "/reload-plugins")
        XCTAssertEqual(JSONLParser.localCommandEcho(["local_command_run": ["command": "model", "args": " haiku "]]),
                       "/model haiku")
        XCTAssertNil(JSONLParser.localCommandEcho(["type": "assistant"]))
    }

    func testEchoedThisTurnOnlyMatchesNewestUserEvents() {
        func ev(_ type: SessionEventType, _ text: String? = nil, ms: Int? = nil) -> SessionEvent {
            SessionEvent(uuid: UUID().uuidString, timestamp: "t", type: type, text: text, thinking: nil,
                         toolUse: nil, toolResults: nil, model: nil, durationMs: ms)
        }
        XCTAssertTrue(SessionManager.echoedThisTurn("/compact", in: [ev(.system, ms: 5), ev(.user, "/compact")]))
        XCTAssertFalse(SessionManager.echoedThisTurn("/cost", in: [ev(.user, "/cost"), ev(.system, ms: 5)]))
        XCTAssertFalse(SessionManager.echoedThisTurn("/cost", in: []))
        // History has no turn end after a local command: its output still ends the match
        XCTAssertFalse(SessionManager.echoedThisTurn("/cost", in: [ev(.user, "/cost"), ev(.text, "Total cost")]))
    }

    // MARK: - /compact (docs/design-claude-uplift.md → Compaction)

    func testCompactBoundaryFromTranscript() {
        let line = """
        {"type":"system","subtype":"compact_boundary","uuid":"cb-1","timestamp":"2026-10-26T02:02:05.359Z","compactMetadata":{"trigger":"manual","preTokens":981891,"postTokens":13527,"durationMs":65057}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.type, .compaction)
        XCTAssertNil(event?.text)
        XCTAssertEqual(event?.compactPreTokens, 981891)
        XCTAssertEqual(event?.compactPostTokens, 13527)
        XCTAssertEqual(event?.durationMs, 65057)
    }

    func testCompactBoundaryFromStdout() {
        let json: [String: Any] = ["type": "system", "subtype": "compact_boundary",
                                   "compact_metadata": ["pre_tokens": 23511, "post_tokens": 3520, "duration_ms": 8191]]
        let event = JSONLParser.compactionMarker(json: json, uuid: "cb-2", timestamp: "t", summary: "S")
        XCTAssertEqual(event.type, .compaction)
        XCTAssertEqual(event.text, "S")
        XCTAssertEqual(event.compactPreTokens, 23511)
        XCTAssertEqual(event.compactPostTokens, 3520)
    }

    func testCompactSummaryMergesIntoBoundary() {
        let boundary = JSONLParser.parseEvent(line: """
        {"type":"system","subtype":"compact_boundary","uuid":"cb-1","timestamp":"2026-10-26T02:02:05.359Z","compactMetadata":{"preTokens":100,"postTokens":10}}
        """)!
        let summary = JSONLParser.parseEvent(line: """
        {"type":"user","uuid":"sum-1","parentUuid":"cb-1","isCompactSummary":true,"isVisibleInTranscriptOnly":true,"timestamp":"2026-10-26T02:02:04.902Z","message":{"role":"user","content":"This session is being continued from a previous conversation. Summary."}}
        """)!
        let merged = JSONLTailer.mergeCompactionSummaries([boundary, summary])
        XCTAssertEqual(merged.count, 1)
        XCTAssertEqual(merged[0].uuid, "cb-1")
        XCTAssertEqual(merged[0].text, "This session is being continued from a previous conversation. Summary.")
        XCTAssertEqual(merged[0].compactPostTokens, 10)
    }

    func testCompactEchoOrderedBeforeMarker() {
        let marker = JSONLParser.parseEvent(line: """
        {"type":"system","subtype":"compact_boundary","uuid":"cb-1","timestamp":"2026-11-04T15:19:12.299Z","compactMetadata":{"preTokens":100,"postTokens":10}}
        """)!
        let echo = JSONLParser.parseEvent(line: """
        {"type":"user","uuid":"c-1","timestamp":"2026-11-04T15:19:03.606Z","message":{"role":"user","content":"<command-name>/compact</command-name>"}}
        """)!
        let merged = JSONLTailer.mergeCompactionSummaries([marker, echo])
        XCTAssertEqual(merged.map(\.uuid), ["c-1", "cb-1"])
    }

    func testCompactCommandEchoShownAsTyped() {
        let line = """
        {"type":"user","uuid":"c-1","timestamp":"2026-11-04T14:55:37.608Z","message":{"role":"user","content":"<command-name>/compact</command-name>\\n            <command-message>compact</command-message>\\n            <command-args>keep the API notes</command-args>"}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.type, .user)
        XCTAssertEqual(event?.text, "/compact keep the API notes")
    }

    func testCompactedLocalOutputHidden() {
        let line = """
        {"type":"user","uuid":"o-1","timestamp":"2026-11-04T14:55:45.909Z","message":{"role":"user","content":"<local-command-stdout>\\u001b[2mCompacted (ctrl+o to see full summary)\\u001b[22m</local-command-stdout>"}}
        """
        XCTAssertNil(JSONLParser.parseEvent(line: line))
    }

    func testSlashCommandDetection() {
        XCTAssertTrue(SessionManager.isSlashCommand("/compact"))
        XCTAssertTrue(SessionManager.isSlashCommand("/compact keep the API notes"))
        XCTAssertTrue(SessionManager.isSlashCommand("/plugin:skill args"))
        XCTAssertFalse(SessionManager.isSlashCommand("/tmp/mclaude-screenshots/screenshot-1.jpg look at this"))
        XCTAssertFalse(SessionManager.isSlashCommand("fix the /compact handling"))
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

    // MARK: - Meta entries and tool-result images (Read of an image file)

    func testSkipsIsMetaImageCaption() {
        let line = """
        {"type":"user","isMeta":true,"uuid":"m-1","timestamp":"2026-11-02T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"[Image: original 1206x2622, displayed at 920x2000. Multiply coordinates by 1.31 to map to original image.]"}]}}
        """
        XCTAssertNil(JSONLParser.parseEvent(line: line), "isMeta caption must not render as a user bubble")
    }

    func testSkipsAnyIsMetaUserText() {
        let line = """
        {"type":"user","isMeta":true,"uuid":"m-2","timestamp":"2026-11-02T00:00:00Z","message":{"role":"user","content":"Some CLI-injected context"}}
        """
        XCTAssertNil(JSONLParser.parseEvent(line: line))
    }

    func testSkipsImageOriginalPrefixWithoutIsMeta() {
        let line = """
        {"type":"user","uuid":"m-3","timestamp":"2026-11-02T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"[Image: original 800x600, displayed at 800x600.]"}]}}
        """
        XCTAssertNil(JSONLParser.parseEvent(line: line), "prefix fallback for older CLIs that don't set isMeta")
    }

    func testSkillExpansionStillShownWhenIsMeta() {
        let line = """
        {"type":"user","isMeta":true,"uuid":"sk-1","timestamp":"2026-11-02T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /Users/me/.claude/skills/foo\\n\\nDo the thing."}]}}
        """
        let event = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(event?.type, .toolResult)
        XCTAssertTrue(event?.toolResults?.first?.content.hasPrefix("Base directory for this skill:") ?? false)
    }

    func testRegularUserMessageNotMeta() {
        let line = """
        {"type":"user","isMeta":false,"uuid":"u-1","timestamp":"2026-11-02T00:00:00Z","message":{"role":"user","content":"Look at this"}}
        """
        XCTAssertEqual(JSONLParser.parseEvent(line: line)?.text, "Look at this")
    }

    func testToolResultImageSavedAndReferenced() throws {
        let dir = NSTemporaryDirectory() + "mclaude-toolimg-test-\(UUID().uuidString)"
        let saved = JSONLParser.toolImageDir
        JSONLParser.toolImageDir = dir
        defer { JSONLParser.toolImageDir = saved; try? FileManager.default.removeItem(atPath: dir) }

        let line = """
        {"type":"user","uuid":"tr-img","timestamp":"2026-11-02T00:00:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="}}]}]}}
        """
        let event = try XCTUnwrap(JSONLParser.parseEvent(line: line))
        let result = try XCTUnwrap(event.toolResults?.first)
        XCTAssertEqual(result.toolUseId, "toolu_1")
        XCTAssertEqual(result.content, "")
        let images = try XCTUnwrap(result.images)
        XCTAssertEqual(images.count, 1)
        XCTAssertTrue(images[0].hasPrefix(dir + "/toolimg-"))
        XCTAssertTrue(images[0].hasSuffix(".png"))
        let bytes = try XCTUnwrap(FileManager.default.contents(atPath: images[0]))
        XCTAssertEqual(bytes, Data(base64Encoded: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="))

        // Idempotent: re-parsing (history reload, live + JSONL) reuses the same file.
        let again = JSONLParser.parseEvent(line: line)?.toolResults?.first?.images
        XCTAssertEqual(again, images)
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: dir).count, 1)
    }

    func testToolResultTextOnlyHasNoImages() {
        let line = """
        {"type":"user","uuid":"tr-txt","timestamp":"2026-11-02T00:00:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":[{"type":"text","text":"ok"}]}]}}
        """
        let result = JSONLParser.parseEvent(line: line)?.toolResults?.first
        XCTAssertEqual(result?.content, "ok")
        XCTAssertNil(result?.images)
    }

    // MARK: - Task notifications (stream-json stdout)

    func testTaskNotificationForBackgroundCommand() {
        let json: [String: Any] = ["type": "system", "subtype": "task_notification", "task_id": "bmshpknfa",
                                   "tool_use_id": "toolu_1", "status": "completed", "output_file": "/tmp/t.output",
                                   "summary": "Background command \"sleep 3; echo hi\" completed (exit code 0)"]
        let event = JSONLParser.taskNotificationEvent(json: json, uuid: "u1", timestamp: "t", agentDescription: nil)
        XCTAssertEqual(event?.type, .user)
        XCTAssertEqual(event?.uuid, "u1")
        XCTAssertEqual(event?.text, """
        <task-notification>
        <task-id>bmshpknfa</task-id>
        <tool-use-id>toolu_1</tool-use-id>
        <output-file>/tmp/t.output</output-file>
        <status>completed</status>
        <summary>Background command "sleep 3; echo hi" completed (exit code 0)</summary>
        </task-notification>
        """)
    }

    func testTaskNotificationForAgentMatchesTranscriptShape() {
        let json: [String: Any] = ["type": "system", "subtype": "task_notification", "task_id": "a1",
                                   "tool_use_id": "toolu_2", "status": "completed", "summary": "PONG </result> <x>"]
        let text = JSONLParser.taskNotificationEvent(json: json, uuid: "u2", timestamp: "t",
                                                     agentDescription: "Research server")?.text ?? ""
        XCTAssertTrue(text.contains("<summary>Agent \"Research server\" finished</summary>"))
        XCTAssertTrue(text.contains("<result>PONG &lt;/result> &lt;x></result>"))
        XCTAssertFalse(text.contains("<output-file>"))
    }

    func testTaskNotificationAgentFailureKeepsStatus() {
        let json: [String: Any] = ["subtype": "task_notification", "task_id": "a1", "status": "failed", "summary": "boom"]
        let text = JSONLParser.taskNotificationEvent(json: json, uuid: "u", timestamp: "t", agentDescription: "X")?.text
        XCTAssertTrue(text?.contains("<summary>Agent \"X\" failed</summary>") ?? false)
    }

    func testOtherSystemSubtypesAreNotTaskNotifications() {
        XCTAssertNil(JSONLParser.taskNotificationEvent(json: ["subtype": "task_started", "task_id": "a"],
                                                       uuid: "u", timestamp: "t", agentDescription: nil))
    }

    func testRemovingTaskNotificationsDropsOnlyMatchedBlocks() {
        let text = """
        <task-notification>
        <task-id>a1</task-id>
        <summary>one</summary>
        </task-notification>
        <task-notification>
        <task-id>b2</task-id>
        <summary>two</summary>
        </task-notification>
        """
        var seen: [String] = []
        let rest = JSONLParser.removingTaskNotifications(from: text) { seen.append($0); return $0 == "a1" }
        XCTAssertEqual(Set(seen), ["a1", "b2"])
        XCTAssertEqual(rest, "<task-notification>\n<task-id>b2</task-id>\n<summary>two</summary>\n</task-notification>")
        XCTAssertEqual(JSONLParser.removingTaskNotifications(from: text) { _ in true }, "")
        XCTAssertNil(JSONLParser.removingTaskNotifications(from: text) { _ in false })
        XCTAssertNil(JSONLParser.removingTaskNotifications(from: "hello") { _ in true })
    }
}
