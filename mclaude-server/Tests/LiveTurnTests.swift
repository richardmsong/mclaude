import XCTest
@testable import mclaude_server

/// docs/plan-watch-app.md → Server → Live stream.
final class LiveTurnTests: XCTestCase {
    private func line(_ s: String) -> [String: Any] {
        try! JSONSerialization.jsonObject(with: Data(s.utf8)) as! [String: Any]
    }
    private func event(_ e: String, parent: String? = nil) -> [String: Any] {
        line("{\"type\":\"stream_event\",\"event\":\(e),\"parent_tool_use_id\":\(parent.map { "\"\($0)\"" } ?? "null")}")
    }
    private func start(_ i: Int, _ type: String) -> [String: Any] {
        event("{\"type\":\"content_block_start\",\"index\":\(i),\"content_block\":{\"type\":\"\(type)\"}}")
    }
    private func thinking(_ i: Int, _ t: String) -> [String: Any] {
        event("{\"type\":\"content_block_delta\",\"index\":\(i),\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"\(t)\"}}")
    }
    private func text(_ i: Int, _ t: String) -> [String: Any] {
        event("{\"type\":\"content_block_delta\",\"index\":\(i),\"delta\":{\"type\":\"text_delta\",\"text\":\"\(t)\"}}")
    }
    private let messageStart: String = "{\"type\":\"message_start\",\"message\":{}}"

    func testBeforeAnyTurn() {
        let t = LiveTurn()
        XCTAssertEqual(t.snapshot, LiveTurn.Snapshot(turn: 0, active: false, blocks: []))
        var m = t
        XCTAssertNil(m.takeFrame(id: "s1"))
    }

    func testThinkingTextToolAndTurnEnd() {
        var t = LiveTurn()
        t.begin()
        XCTAssertFalse(t.ingest(event(messageStart)))
        XCTAssertTrue(t.ingest(start(0, "thinking")))
        XCTAssertTrue(t.ingest(thinking(0, "Let me ")))
        XCTAssertTrue(t.ingest(thinking(0, "think.")))
        XCTAssertFalse(t.ingest(event("{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"x\"}}")))
        XCTAssertFalse(t.ingest(event("{\"type\":\"content_block_stop\",\"index\":0}")))

        var f = t.takeFrame(id: "s1")!
        XCTAssertEqual(f.turn, 1)
        XCTAssertFalse(f.ended)
        XCTAssertEqual(f.blocks.count, 1)
        XCTAssertEqual(f.blocks[0].index, 0)
        XCTAssertEqual(f.blocks[0].kind, .thinking)
        XCTAssertEqual(f.blocks[0].append, "Let me think.")
        XCTAssertTrue(f.blocks[0].isNew)
        XCTAssertNotNil(f.blocks[0].startedAt)
        XCTAssertNil(t.takeFrame(id: "s1"))                   // nothing new

        // Tool block in the same message, subagent lines that are left out, then a new
        // assistant message whose Claude index restarts at 0
        XCTAssertTrue(t.ingest(start(1, "tool_use")))
        XCTAssertFalse(t.ingest(event("{\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}", parent: "toolu_1")))
        XCTAssertFalse(t.ingest(event("{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"sub\"}}", parent: "toolu_1")))
        XCTAssertFalse(t.ingest(event("{\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}")))
        XCTAssertTrue(t.ingest(event(messageStart)) == false)
        XCTAssertTrue(t.ingest(start(0, "text")))
        XCTAssertTrue(t.ingest(text(0, "Hi")))
        XCTAssertTrue(t.ingest(text(0, " there")))

        f = t.takeFrame(id: "s1")!
        XCTAssertEqual(f.blocks.map(\.index), [1, 2])
        XCTAssertEqual(f.blocks.map(\.kind), [.tool, .text])
        XCTAssertEqual(f.blocks.map(\.append), ["", "Hi there"])
        XCTAssertEqual(f.blocks.map(\.isNew), [true, true])

        var s = t.snapshot
        XCTAssertTrue(s.active)
        XCTAssertEqual(s.blocks.map(\.done), [true, true, false])
        XCTAssertEqual(s.blocks.map(\.text), ["Let me think.", "", "Hi there"])

        XCTAssertTrue(t.ingest(text(0, "!")))
        XCTAssertTrue(t.end())
        XCTAssertFalse(t.end())
        f = t.takeFrame(id: "s1")!
        XCTAssertTrue(f.ended)
        XCTAssertEqual(f.blocks.count, 1)
        XCTAssertEqual(f.blocks[0].append, "!")
        XCTAssertFalse(f.blocks[0].isNew)
        XCTAssertNil(f.blocks[0].startedAt)

        // Ended turn's blocks stay until the next turn starts
        s = t.snapshot
        XCTAssertFalse(s.active)
        XCTAssertEqual(s.blocks.map(\.done), [true, true, true])
        t.begin()
        XCTAssertEqual(t.snapshot, LiveTurn.Snapshot(turn: 2, active: true, blocks: []))
        XCTAssertTrue(t.ingest(start(0, "redacted_thinking")))
        XCTAssertEqual(t.snapshot.blocks.map(\.index), [0])
        XCTAssertEqual(t.snapshot.blocks.map(\.kind), [.thinking])
    }

    func testStreamStartsATurnWhenNoneIsRunning() {
        var t = LiveTurn()
        XCTAssertFalse(t.ingest(text(0, "orphan")))           // a delta alone never starts one
        XCTAssertTrue(t.ingest(start(0, "server_tool_use")))
        XCTAssertEqual(t.turn, 1)
        XCTAssertTrue(t.active)
        XCTAssertEqual(t.snapshot.blocks.map(\.kind), [.tool])
    }

    func testSnapshotKeepsLastBlocksAndTextButFramesCarryEverything() {
        var t = LiveTurn()
        t.begin()
        for i in 0..<35 { t.ingest(start(i, "text")) }
        _ = t.takeFrame(id: "s1")
        XCTAssertEqual(t.snapshot.blocks.count, 30)
        XCTAssertEqual(t.snapshot.blocks.first?.index, 5)
        XCTAssertEqual(t.snapshot.blocks.last?.index, 34)

        let chunk = String(repeating: "a", count: 999) + "b"
        for _ in 0..<12 { t.ingest(text(34, chunk)) }         // 12,000 characters
        let f = t.takeFrame(id: "s1")!
        XCTAssertEqual(f.blocks.count, 1)
        XCTAssertEqual(f.blocks[0].append.count, 12_000)
        let last = t.snapshot.blocks.last!.text
        XCTAssertEqual(last.count, LiveTurn.maxText)
        XCTAssertTrue(last.hasPrefix("…"))
        XCTAssertTrue(last.hasSuffix("ab"))
    }

    func testWireShapes() throws {
        var t = LiveTurn()
        t.begin()
        t.ingest(start(0, "tool_use"))
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        let frame = try JSONSerialization.jsonObject(with: encoder.encode(t.takeFrame(id: "s12")!)) as! [String: Any]
        XCTAssertEqual(Set(frame.keys), ["id", "turn", "ended", "blocks"])
        XCTAssertEqual(frame["id"] as? String, "s12")
        let block = (frame["blocks"] as! [[String: Any]])[0]
        XCTAssertEqual(Set(block.keys), ["index", "kind", "append", "new", "startedAt"])
        XCTAssertEqual(block["kind"] as? String, "tool")
        XCTAssertEqual(block["new"] as? Bool, true)
        XCTAssertNotNil(ISO8601DateFormatter().date(from: block["startedAt"] as! String))

        let snap = try JSONSerialization.jsonObject(with: encoder.encode(t.snapshot)) as! [String: Any]
        XCTAssertEqual(Set(snap.keys), ["turn", "active", "prompt", "blocks"])
        XCTAssertTrue(snap["prompt"] is NSNull)               // null until known
        let sb = (snap["blocks"] as! [[String: Any]])[0]
        XCTAssertEqual(Set(sb.keys), ["index", "kind", "text", "startedAt", "done"])

        t.ingest(start(1, "text"))
        t.ingest(text(1, "x"))
        _ = t.takeFrame(id: "s12")
        t.ingest(text(1, "y"))
        let later = try JSONSerialization.jsonObject(with: encoder.encode(t.takeFrame(id: "s12")!)) as! [String: Any]
        XCTAssertEqual(Set((later["blocks"] as! [[String: Any]])[0].keys), ["index", "kind", "append", "new"])
    }

    // MARK: - What started the turn

    private func userEvent(_ text: String, type: SessionEventType = .user, subagent: Bool = false) -> SessionEvent {
        SessionEvent(uuid: "u1", timestamp: "2026-11-11T10:00:00.000Z", type: type, text: text, thinking: nil,
                     toolUse: nil, toolResults: nil, model: nil, durationMs: nil,
                     subagentInfo: subagent ? SubagentInfo(agentId: "a", agentType: "Explore", description: "d", parentToolUseId: "t") : nil)
    }

    private func peerEvent(_ body: String, other: String? = nil) -> SessionEvent {
        var e = userEvent("<cross-session-message from=\"uds:/tmp/cc-socks/1.sock\">\(body)</cross-session-message>")
        let ref = PeerRef(claudeSessionId: "uuid-a", ccName: "mclaude-v1-f1", address: "uds:/tmp/cc-socks/1.sock",
                          sessionId: nil, displayName: "mclaude-v1-f1", open: false)
        e.peerMessages = [PeerMessage(id: "k1", direction: "received", peer: ref, summary: nil, text: body,
                                      at: e.timestamp, status: "received", error: nil)]
        e.peerOtherText = other
        return e
    }

    func testPromptAfterTurnStartIsInTheFirstFrameOnly() throws {
        var t = LiveTurn()
        t.begin()
        XCTAssertNil(t.snapshot.prompt)
        _ = t.takeFrame(id: "s1")
        XCTAssertTrue(t.notePrompt(LiveTurn.Prompt(text: "Fix the build")))
        XCTAssertFalse(t.notePrompt(LiveTurn.Prompt(text: "a mid-turn message")))   // the first one stays
        t.ingest(start(0, "text"))

        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        let first = try JSONSerialization.jsonObject(with: encoder.encode(t.takeFrame(id: "s1")!)) as! [String: Any]
        XCTAssertEqual(Set(first.keys), ["id", "turn", "ended", "blocks", "prompt"])
        let p = first["prompt"] as! [String: Any]
        XCTAssertEqual(p["text"] as? String, "Fix the build")
        XCTAssertTrue(p["from"] is NSNull)
        XCTAssertEqual(Set(p.keys), ["text", "from"])

        t.ingest(text(0, "ok"))
        let later = try JSONSerialization.jsonObject(with: encoder.encode(t.takeFrame(id: "s1")!)) as! [String: Any]
        XCTAssertEqual(Set(later.keys), ["id", "turn", "ended", "blocks"])
        XCTAssertNil(t.takeFrame(id: "s1"))
        t.end()
        XCTAssertNil(t.takeFrame(id: "s1")!.prompt)

        // The snapshot keeps it, also after the turn ended; the next turn starts without one
        XCTAssertEqual(t.snapshot.prompt, LiveTurn.Prompt(text: "Fix the build"))
        let snap = try JSONSerialization.jsonObject(with: encoder.encode(t.snapshot)) as! [String: Any]
        XCTAssertEqual((snap["prompt"] as? [String: Any])?["text"] as? String, "Fix the build")
        t.begin()
        XCTAssertNil(t.snapshot.prompt)
        XCTAssertNil(t.takeFrame(id: "s1"))
    }

    func testPromptJustBeforeTurnStartIsCarriedIntoIt() {
        var t = LiveTurn()
        XCTAssertFalse(t.notePrompt(LiveTurn.Prompt(text: "Hello")))
        XCTAssertFalse(t.hasPending)
        XCTAssertNil(t.snapshot.prompt)
        t.begin()
        XCTAssertEqual(t.snapshot.prompt?.text, "Hello")
        let f = t.takeFrame(id: "s1")!                        // a frame for the prompt alone
        XCTAssertEqual(f.prompt?.text, "Hello")
        XCTAssertEqual(f.blocks.count, 0)
        XCTAssertNil(t.takeFrame(id: "s1"))
        t.end()
        t.begin()
        XCTAssertNil(t.snapshot.prompt)                       // carried once, into one turn
    }

    func testPromptOfEvents() {
        XCTAssertEqual(LiveTurn.prompt(of: userEvent("look at /tmp/mclaude-screenshots/a.png this\nplease")),
                       LiveTurn.Prompt(text: "look at this please"))
        XCTAssertNil(LiveTurn.prompt(of: userEvent("x", type: .toolResult)))
        XCTAssertNil(LiveTurn.prompt(of: userEvent("x", subagent: true)))
        XCTAssertNil(LiveTurn.prompt(of: userEvent(SessionManager.crashResumeText)))
        XCTAssertNil(LiveTurn.prompt(of: userEvent("<task-notification>\n<task-id>b1</task-id>\n</task-notification>")))

        let long = String(repeating: "a", count: 1200)
        let cut = LiveTurn.prompt(of: userEvent(long))!.text
        XCTAssertEqual(cut.count, LiveTurn.maxPrompt)
        XCTAssertEqual(cut, String(repeating: "a", count: 999) + "…")
        XCTAssertEqual(LiveTurn.prompt(of: userEvent(String(repeating: "b", count: 1000)))!.text.count, 1000)
    }

    func testPeerPrompt() async throws {
        let p = LiveTurn.prompt(of: peerEvent(" Can you review s3? "))!
        XCTAssertEqual(p.text, "Can you review s3?")
        XCTAssertEqual(p.from, "mclaude-v1-f1")
        XCTAssertEqual(p.peer?.claudeSessionId, "uuid-a")
        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(p)) as! [String: Any]
        XCTAssertEqual(Set(json.keys), ["text", "from"])      // the PeerRef stays off the wire
        XCTAssertEqual(json["from"] as? String, "mclaude-v1-f1")

        // Named per viewer: the peer's session name when they can see it
        let directory = PeerDirectory(open: ["uuid-a": .init(id: "s3", name: "🔐 Fix Login")], ended: [:])
        let seen = await p.named(by: directory, canSee: { $0 == "s3" })
        XCTAssertEqual(seen.from, "🔐 Fix Login")
        let hidden = await p.named(by: directory, canSee: { _ in false })
        XCTAssertEqual(hidden.from, "mclaude-v1-f1")
        let mine = LiveTurn.Prompt(text: "hi")
        let unchanged = await mine.named(by: directory, canSee: { _ in true })
        XCTAssertEqual(unchanged, mine)

        // Peer tags plus the user's own text: the user's message
        XCTAssertEqual(LiveTurn.prompt(of: peerEvent("fyi", other: "and also do this")),
                       LiveTurn.Prompt(text: "and also do this"))
    }
}
