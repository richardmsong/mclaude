import XCTest
@testable import mclaude_server

/// docs/design-claude-uplift.md → Peer turns: replies to the user's requests aren't quiet.
final class PeerTurnsTests: XCTestCase {
    private let peerUuid = "6f3b0f6e-0000-4000-8000-000000000001"

    private func ref(uuid: String? = nil, name: String = "me-ac") -> PeerRef {
        PeerRef(claudeSessionId: uuid, ccName: name, address: "uds:/tmp/cc-socks/\(name).sock",
                sessionId: nil, displayName: name, open: false)
    }

    private func received(_ id: String, from peer: PeerRef, other: String? = nil) -> SessionEvent {
        var e = SessionEvent(uuid: id, timestamp: "2026-11-09T16:00:00Z", type: .user, text: "<cross-session-message>…",
                             thinking: nil, toolUse: nil, toolResults: nil, model: nil, durationMs: nil)
        e.peerMessages = [PeerMessage(id: "recv:\(id)", direction: "received", peer: peer, summary: nil, text: "pong",
                                      at: "2026-11-09T16:00:00Z", status: "received", error: nil)]
        e.peerOtherText = other
        return e
    }

    private func sent(_ id: String, to peer: PeerRef) -> SessionEvent {
        var e = SessionEvent(uuid: id, timestamp: "2026-11-09T15:59:00Z", type: .toolUse, text: nil, thinking: nil,
                             toolUse: nil, toolResults: nil, model: nil, durationMs: nil)
        e.peerMessages = [PeerMessage(id: "sent:\(id)", direction: "sent", peer: peer, summary: nil, text: "ping",
                                      at: "2026-11-09T15:59:00Z", status: "delivered", error: nil)]
        return e
    }

    private func user(_ id: String, _ text: String) -> SessionEvent {
        SessionEvent(uuid: id, timestamp: "2026-11-09T15:58:00Z", type: .user, text: text, thinking: nil, toolUse: nil,
                     toolResults: nil, model: nil, durationMs: nil)
    }

    private func turnEnd(_ id: String) -> SessionEvent {
        SessionEvent(uuid: id, timestamp: "2026-11-09T16:01:00Z", type: .system, text: nil, thinking: nil, toolUse: nil,
                     toolResults: nil, model: nil, durationMs: 1200)
    }

    func testUnsolicitedPeerMessageIsQuiet() {
        let s = PeerTurnState()
        XCTAssertTrue(s.isQuietStart(received("r1", from: ref(uuid: peerUuid))))
        XCTAssertFalse(s.isQuietStart(received("r2", from: ref(uuid: peerUuid), other: "and also this")))
        XCTAssertFalse(s.isQuietStart(user("u1", "Fix the login bug")))
    }

    func testReplyToYourSendIsNotQuiet() {
        var s = PeerTurnState()
        s.noteSend(sent("t1", to: ref(uuid: peerUuid)), fromQuietTurn: false)
        XCTAssertFalse(s.isQuietStart(received("r1", from: ref(uuid: peerUuid))))
        // Matched by name when the reply isn't resolved to a Claude session id
        XCTAssertFalse(s.isQuietStart(received("r2", from: ref(uuid: nil))))
        // Another peer is still quiet
        XCTAssertTrue(s.isQuietStart(received("r3", from: ref(uuid: "other", name: "me-19"))))
    }

    func testSendFromQuietTurnKeepsTheConversationQuiet() {
        var s = PeerTurnState()
        s.noteSend(sent("t1", to: ref(uuid: peerUuid)), fromQuietTurn: false)
        s.noteSend(sent("t2", to: ref(uuid: peerUuid)), fromQuietTurn: true)   // replied from a quiet turn
        XCTAssertTrue(s.isQuietStart(received("r1", from: ref(uuid: peerUuid))))
    }

    /// The reported case, in history: you ask for a test message; the pong starts the next turn.
    func testHistoryMarksOnlyUnsolicitedStarts() {
        var s = PeerTurnState()
        let marked = s.mark([
            user("u1", "Send a test peer message"),
            sent("t1", to: ref(uuid: peerUuid)),
            turnEnd("e1"),
            received("r1", from: ref(uuid: peerUuid)),          // reply to your request: not quiet
            turnEnd("e2"),
            received("r2", from: ref(uuid: "x", name: "me-19")), // unsolicited: quiet
            sent("t2", to: ref(uuid: "x", name: "me-19")),       // answered from the quiet turn
            turnEnd("e3"),
            received("r3", from: ref(uuid: "x", name: "me-19")), // still quiet
            turnEnd("e4"),
        ])
        XCTAssertEqual(marked.filter { $0.quiet == true }.map(\.uuid), ["r2", "r3"])
    }

    private func toolResult(_ id: String) -> SessionEvent {
        SessionEvent(uuid: id, timestamp: "2026-11-09T15:59:30Z", type: .toolResult, text: nil, thinking: nil,
                     toolUse: nil, toolResults: [], model: nil, durationMs: nil)
    }

    private func text(_ id: String) -> SessionEvent {
        SessionEvent(uuid: id, timestamp: "2026-11-09T15:59:40Z", type: .text, text: "done", thinking: nil,
                     toolUse: nil, toolResults: nil, model: nil, durationMs: nil)
    }

    func testMidTurnPeerMessageIsNeverQuiet() {
        var s = PeerTurnState()
        let marked = s.mark([
            user("u1", "Do the thing"),
            toolResult("tr1"),
            received("r1", from: ref(uuid: "x", name: "me-19")),   // taken at the step boundary
            text("x1"),
        ])
        XCTAssertTrue(marked.allSatisfy { $0.quiet == nil })
    }

    /// Real transcripts have no turn-time events (seen 2026-11-09 on a test server): turn starts
    /// come from tool-result adjacency alone.
    func testHistoryWithoutTurnEnds() {
        var s = PeerTurnState()
        let peer = ref(uuid: "x", name: "PeerA")
        let marked = s.mark([
            user("u1", "Run a background sleep"), toolResult("tr1"), text("x1"),
            received("r1", from: peer), sent("t1", to: peer), toolResult("tr2"), text("x2"),   // unsolicited: quiet
            user("u2", "Run another"), text("x3"),
            received("r2", from: peer), text("x4"),                                            // still quiet
        ])
        XCTAssertEqual(marked.filter { $0.quiet == true }.map(\.uuid), ["r1", "r2"])
        XCTAssertTrue(s.isQuietStart(received("r3", from: peer)))   // its replies came from quiet turns
    }

    func testQuietFieldOmittedWhenUnset() throws {
        let data = try JSONEncoder().encode(user("u1", "hi"))
        XCTAssertFalse(String(decoding: data, as: UTF8.self).contains("quiet"))
    }
}
