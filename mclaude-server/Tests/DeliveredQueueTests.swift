import XCTest
@testable import mclaude_server

/// docs/design-claude-uplift.md → Queue group → Delivered messages.
final class DeliveredQueueTests: XCTestCase {
    private func msg(_ id: String, _ text: String) -> QueuedMessage {
        QueuedMessage(id: id, text: text, queuedAt: Date(timeIntervalSince1970: 0))
    }

    func testAddStampsDeliveredAt() {
        var q = DeliveredQueue()
        let at = Date(timeIntervalSince1970: 100)
        q.add([msg("q1", "hello")], at: at)
        XCTAssertEqual(q.ids, ["q1"])
        XCTAssertEqual(q.items[0].deliveredAt, at)
        XCTAssertEqual(q.items[0].text, "hello")
    }

    func testReadRemovesTheMatchingItem() {
        var q = DeliveredQueue()
        q.add([msg("q1", "first"), msg("q2", "second")], at: Date())
        XCTAssertEqual(q.read("second").map(\.id), ["q2"])
        XCTAssertEqual(q.ids, ["q1"])
        XCTAssertTrue(q.read("something else").isEmpty)
        XCTAssertEqual(q.ids, ["q1"])
    }

    func testBatchJoinedIntoOneUserMessageClearsAll() {
        var q = DeliveredQueue()
        q.add([msg("q1", "check the logs"), msg("q2", "and then\nrestart it")], at: Date())
        // flushQueue joins a batch with blank lines; Claude may reflow the whitespace
        XCTAssertEqual(Set(q.read("check the logs\n\nand then restart it").map(\.id)), ["q1", "q2"])
        XCTAssertTrue(q.isEmpty)
    }

    func testRepeatedShortTextOnlyClearsOne() {
        var q = DeliveredQueue()
        q.add([msg("q1", "ok")], at: Date())
        q.add([msg("q2", "ok")], at: Date())
        XCTAssertEqual(q.read("ok").map(\.id), ["q1"])
        XCTAssertEqual(q.ids, ["q2"])
    }

    func testImageTagsAndPathsMatch() {
        var q = DeliveredQueue()
        q.add([msg("q1", "/tmp/mclaude-screenshots/a.jpg what is this")], at: Date())
        XCTAssertEqual(q.read("[Image #1] /tmp/mclaude-screenshots/a.jpg what is this").map(\.id), ["q1"])
    }

    func testLongTextMatchesOnSharedStart() {
        var q = DeliveredQueue()
        let long = "please look at the deploy script and tell me why it fails on the second run"
        q.add([msg("q1", long)], at: Date())
        XCTAssertEqual(q.read(String(long.prefix(40)) + " (edited by the CLI)").map(\.id), ["q1"])
    }

    func testRemoveByIdsAndAll() {
        var q = DeliveredQueue()
        q.add([msg("q1", "a"), msg("q2", "b"), msg("q3", "c")], at: Date())
        XCTAssertEqual(q.remove(ids: ["q2", "q9"]).map(\.id), ["q2"])
        XCTAssertEqual(q.ids, ["q1", "q3"])
        XCTAssertEqual(q.removeAll().map(\.id), ["q1", "q3"])
        XCTAssertTrue(q.isEmpty)
    }

    func testSessionPayloadOmitsEmptyDelivered() throws {
        var s = ClaudeSession(id: "s1", pid: 0, sessionId: "x", cwd: "/", startedAt: Date(), name: "n", status: .idle,
                              statusSince: nil, projectName: "p", lastOutput: "", prompt: nil)
        var json = String(data: try JSONEncoder.iso.encode(s), encoding: .utf8)!
        XCTAssertFalse(json.contains("delivered"))
        var item = msg("q1", "hi")
        item.deliveredAt = Date()
        s.delivered = [item]
        json = String(data: try JSONEncoder.iso.encode(s), encoding: .utf8)!
        XCTAssertTrue(json.contains("\"delivered\""))
        XCTAssertTrue(json.contains("\"deliveredAt\""))
    }

    func testSessionsFrameCarriesSeq() throws {
        let frame = try XCTUnwrap(encodeWSMessage(type: "sessions", data: [String](), seq: 7))
        let msg = try JSONDecoder().decode(WSMessage.self, from: Data(frame.utf8))
        XCTAssertEqual(msg.seq, 7)
        let a = SessionsSeq.shared.next(), b = SessionsSeq.shared.next()
        XCTAssertEqual(b, a + 1)
    }
}
