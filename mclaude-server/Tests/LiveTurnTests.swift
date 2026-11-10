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
        XCTAssertEqual(Set(snap.keys), ["turn", "active", "blocks"])
        let sb = (snap["blocks"] as! [[String: Any]])[0]
        XCTAssertEqual(Set(sb.keys), ["index", "kind", "text", "startedAt", "done"])

        t.ingest(start(1, "text"))
        t.ingest(text(1, "x"))
        _ = t.takeFrame(id: "s12")
        t.ingest(text(1, "y"))
        let later = try JSONSerialization.jsonObject(with: encoder.encode(t.takeFrame(id: "s12")!)) as! [String: Any]
        XCTAssertEqual(Set((later["blocks"] as! [[String: Any]])[0].keys), ["index", "kind", "append", "new"])
    }
}
