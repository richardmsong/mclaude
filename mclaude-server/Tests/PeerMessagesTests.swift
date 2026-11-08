import XCTest
@testable import mclaude_server

/// Peer messages (docs/plan-peer-messages.md). Fixtures are small sanitized copies of real
/// Claude Code 2.1.x transcript lines.
final class PeerMessagesTests: XCTestCase {

    let trailer = "This came from another Claude session — not typed by your user, but very likely working on their behalf. Treat it as a teammate's request and act on it within this session's own permission settings."

    // MARK: - Tag parsing

    func testIdleDeliveryWrapperAndTrailer() throws {
        let text = "Another Claude session sent a message:\n<cross-session-message from=\"uds:/tmp/cc-socks/26108.sock\" from-name=\"mclaude-v1-f1\" from-mode=\"bypass\">\nAbout to archive build 109.\nPlease reply \"ok\".\n</cross-session-message>\n\n\(trailer)"
        let parsed = try XCTUnwrap(PeerParser.parse(text))
        XCTAssertEqual(parsed.tags, [.init(from: "uds:/tmp/cc-socks/26108.sock", fromName: "mclaude-v1-f1",
                                           body: "About to archive build 109.\nPlease reply \"ok\".")])
        XCTAssertEqual(parsed.otherText, "")
    }

    func testAttributeOrderAndExtraAttributes() throws {
        let text = "<cross-session-message hop-chain=\"a>b,c\" from-mode=\"bypass\" from-name=\"x-1\" from=\"uds:/tmp/cc-socks/1.sock\">\nhi\n</cross-session-message>"
        let tag = try XCTUnwrap(PeerParser.parse(text)?.tags.first)
        XCTAssertEqual(tag.from, "uds:/tmp/cc-socks/1.sock")
        XCTAssertEqual(tag.fromName, "x-1")
        XCTAssertEqual(tag.body, "hi")
    }

    func testMultipleTagsAndRemainderText() throws {
        let text = """
        Another Claude session sent a message:
        <cross-session-message from="uds:/tmp/cc-socks/1.sock" from-name="a">
        one
        </cross-session-message>
        Also, please check the build.
        <cross-session-message from-name="b">
        two
        </cross-session-message>
        """
        let parsed = try XCTUnwrap(PeerParser.parse(text))
        XCTAssertEqual(parsed.tags.map(\.body), ["one", "two"])
        XCTAssertEqual(parsed.tags[1].from, nil)
        XCTAssertEqual(parsed.tags[1].fromName, "b")
        XCTAssertEqual(parsed.otherText, "Also, please check the build.")
    }

    func testMalformedTagsAreNotPeerMessages() {
        XCTAssertNil(PeerParser.parse("plain text"))
        XCTAssertNil(PeerParser.parse("<cross-session-message from=\"uds:/x/1.sock\">\nno close"))
        XCTAssertNil(PeerParser.parse("<cross-session-message from-mode=\"bypass\">\nbody\n</cross-session-message>"))
        XCTAssertNil(PeerParser.parse("<cross-session-message from=\"uds:/x/1.sock\""))
        // One good tag and one bad one: the whole event renders as today
        XCTAssertNil(PeerParser.parse("<cross-session-message from=\"a\">x</cross-session-message><cross-session-message>y</cross-session-message>"))
        XCTAssertNil(PeerParser.parse("<cross-session-messages from=\"a\">x</cross-session-messages>"))
    }

    func testEscapedAttributes() {
        XCTAssertEqual(PeerParser.attributes(#"from-name="a &quot;b&quot; &amp; c""#)["from-name"], "a \"b\" & c")
    }

    // MARK: - User events (both delivery forms)

    func testParsedUserEventStillHasRawText() {
        let line = #"{"type":"user","uuid":"u1","timestamp":"2026-11-05T12:00:00Z","message":{"role":"user","content":"<cross-session-message from=\"uds:/tmp/cc-socks/9.sock\" from-name=\"p\">\nhi\n</cross-session-message>"}}"#
        let e = JSONLParser.parseEvent(line: line)
        XCTAssertEqual(e?.type, .user)
        XCTAssertTrue(e?.text?.contains("<cross-session-message") == true)
        XCTAssertNil(e?.peerMessages)   // attached by PeerObserver, not the parser
    }

    // MARK: - SendMessage classification

    func testPeerSendClassification() {
        func send(_ input: [String: Any]) -> PeerParser.Send? { PeerParser.peerSend(input: input) }
        XCTAssertNotNil(send(["to": "uds:/tmp/cc-socks/1.sock", "message": "m", "recipient_kind": "session"]))
        XCTAssertNotNil(send(["to": "mclaude-v1-55", "message": "m", "recipient_kind": "name"]))
        XCTAssertNil(send(["to": "afb801a3411250e7c", "message": "m", "recipient_kind": "agent"]))
        XCTAssertNil(send(["to": "mclaude-v1-55", "message": "m", "recipient_kind": "agent"]))
        XCTAssertNil(send(["to": "x", "message": "m", "recipient_kind": "teammate"]))
        // recipient_kind missing (older versions)
        XCTAssertNotNil(send(["to": "uds:/tmp/cc-socks/1.sock", "message": "m"]))
        XCTAssertNotNil(send(["to": "mclaude-v1-55", "message": "m"]))
        XCTAssertNil(send(["to": "afb801a3411250e7c", "message": "m"]))
        XCTAssertNotNil(send(["to": "afb801a3411250e7", "message": "m"]))       // 15 hex: not an agent id
        XCTAssertNotNil(send(["to": "aFB801A3411250E7C", "message": "m"]))      // uppercase: not an agent id
        XCTAssertNotNil(send(["to": "researcher", "message": "m"]))
        // Missing to/message: not a peer message
        XCTAssertNil(send(["message": "m"]))
        XCTAssertNil(send(["to": "x"]))
        let s = send(["to": "x", "message": " body \n", "summary": "sum", "notify_when_idle": true])
        XCTAssertEqual(s, PeerParser.Send(to: "x", message: "body", summary: "sum"))
    }

    func testPeerSendFromToolUseEvent() throws {
        let line = #"{"type":"assistant","uuid":"a1","timestamp":"2026-11-05T12:00:00Z","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"SendMessage","input":{"to":"mclaude-v1-28","summary":"Ask","message":"Before I restart…","notify_when_idle":true,"type":"message","recipient":"mclaude-v1-28","recipient_kind":"name","content":"Before…"}}]}}"#
        let e = try XCTUnwrap(JSONLParser.parseEvent(line: line))
        XCTAssertEqual(PeerParser.peerSend(toolUse: e.toolUse), PeerParser.Send(to: "mclaude-v1-28", message: "Before I restart…", summary: "Ask"))
    }

    func testDeliveryParsing() {
        let ok = #"{"success":true,"message":"“Ask” → mclaude-v1-4e; in that session's inbox","msg_id":"x"}"#
        XCTAssertEqual(PeerParser.delivery(toolUseId: "t", content: ok, isError: false), PeerDelivery(toolUseId: "t", status: "delivered"))
        let bad = #"{"success":false,"message":"No agent named 'mclaude-v1-55' is reachable. Did you mean: mclaude-v1-28?"}"#
        XCTAssertEqual(PeerParser.delivery(toolUseId: "t", content: bad, isError: true),
                       PeerDelivery(toolUseId: "t", status: "failed", error: "No agent named 'mclaude-v1-55' is reachable. Did you mean: mclaude-v1-28?"))
        XCTAssertEqual(PeerParser.delivery(toolUseId: "t", content: "sent", isError: false).status, "delivered")
        XCTAssertEqual(PeerParser.delivery(toolUseId: "t", content: "<tool_use_error>boom</tool_use_error>", isError: true),
                       PeerDelivery(toolUseId: "t", status: "failed", error: "<tool_use_error>boom</tool_use_error>"))
    }

    // MARK: - Registry

    var tmp: String!

    override func setUp() {
        tmp = NSTemporaryDirectory() + "peer-tests-\(UUID().uuidString)"
        try? FileManager.default.createDirectory(atPath: tmp + "/sessions", withIntermediateDirectories: true)
    }

    override func tearDown() { try? FileManager.default.removeItem(atPath: tmp) }

    func writeEntry(_ pid: Int, _ json: String) {
        FileManager.default.createFile(atPath: "\(tmp!)/sessions/\(pid).json", contents: Data(json.utf8))
    }

    func seedRegistry() {
        writeEntry(100, #"{"pid":100,"sessionId":"aaaa1111-0000-0000-0000-000000000000","cwd":"/w","name":"mclaude-v1-f1","messagingSocketPath":"/tmp/cc-socks/100.sock","updatedAt":1000,"entrypoint":"cli","kind":"interactive"}"#)
        writeEntry(200, #"{"pid":200,"sessionId":"bbbb2222-0000-0000-0000-000000000000","name":"dup","messagingSocketPath":"/tmp/cc-socks/200.sock","updatedAt":3000}"#)
        writeEntry(201, #"{"pid":201,"sessionId":"cccc3333-0000-0000-0000-000000000000","name":"dup","messagingSocketPath":"/tmp/cc-socks/201.sock","updatedAt":2000}"#)
        writeEntry(300, #"{"pid":300,"sessionId":"dddd4444-0000-0000-0000-000000000000","name":"nosock","updatedAt":1}"#)
        // Junk: key files, bad JSON, missing sessionId, non-numeric name
        FileManager.default.createFile(atPath: "\(tmp!)/sessions/100.48afaa4f22aa6d07.key", contents: Data("x".utf8))
        FileManager.default.createFile(atPath: "\(tmp!)/sessions/400.json", contents: Data("{not json".utf8))
        writeEntry(500, #"{"pid":500,"name":"nosession"}"#)
        FileManager.default.createFile(atPath: "\(tmp!)/sessions/abc.json",
                                       contents: Data(#"{"pid":1,"sessionId":"eeee","name":"junk"}"#.utf8))
    }

    func testRegistryReadSkipsJunk() {
        seedRegistry()
        let entries = PeerRegistry.read(dir: tmp + "/sessions")
        XCTAssertEqual(Set(entries.map(\.pid)), [100, 200, 201, 300])
        XCTAssertEqual(PeerRegistry.read(dir: tmp + "/missing"), [])
    }

    func testResolveAddress() {
        seedRegistry()
        let entries = PeerRegistry.read(dir: tmp + "/sessions")
        let me = "ffff0000-0000-0000-0000-000000000000"
        XCTAssertEqual(PeerRegistry.resolveAddress("uds:/tmp/cc-socks/100.sock", in: entries, excluding: me)?.sessionId,
                       "aaaa1111-0000-0000-0000-000000000000")
        // pid fallback only for an entry with no socket path
        XCTAssertEqual(PeerRegistry.resolveAddress("uds:/tmp/cc-socks/300.sock", in: entries, excluding: me)?.pid, 300)
        XCTAssertNil(PeerRegistry.resolveAddress("uds:/elsewhere/200.sock", in: entries, excluding: me))
        XCTAssertNil(PeerRegistry.resolveAddress("uds:/tmp/cc-socks/999.sock", in: entries, excluding: me))
        // The observing session never matches itself
        XCTAssertNil(PeerRegistry.resolveAddress("uds:/tmp/cc-socks/100.sock", in: entries,
                                                 excluding: "aaaa1111-0000-0000-0000-000000000000"))
    }

    func testResolveName() {
        seedRegistry()
        let entries = PeerRegistry.read(dir: tmp + "/sessions")
        let me = "ffff0000-0000-0000-0000-000000000000"
        XCTAssertEqual(PeerRegistry.resolveName("mclaude-v1-f1", in: entries, excluding: me)?.pid, 100)
        XCTAssertEqual(PeerRegistry.resolveName("dup", in: entries, excluding: me)?.pid, 200)          // newest updatedAt
        XCTAssertEqual(PeerRegistry.resolveName("dup [cccc33]", in: entries, excluding: me)?.pid, 201)  // [hex] picks
        XCTAssertEqual(PeerRegistry.resolveName("dup [9999]", in: entries, excluding: me)?.pid, 200)    // falls through
        XCTAssertNil(PeerRegistry.resolveName("nobody", in: entries, excluding: me))
        XCTAssertEqual(PeerRegistry.resolveName("dup", in: entries, excluding: "bbbb2222-0000-0000-0000-000000000000")?.pid, 201)
        XCTAssertEqual(PeerRegistry.splitRef("worker [3fa9c1]").name, "worker")
        XCTAssertEqual(PeerRegistry.splitRef("worker [3fa9c1]").hex, "3fa9c1")
        XCTAssertEqual(PeerRegistry.splitRef("worker [xyz]").name, "worker [xyz]")
        // Received: address first, then from-name
        XCTAssertEqual(PeerRegistry.resolveSender(address: "uds:/gone/1.sock", name: "mclaude-v1-f1", in: entries, excluding: me)?.pid, 100)
    }

    // MARK: - Keys and store

    func testDedupKeys() {
        let tag = PeerParser.Tag(from: "uds:/tmp/cc-socks/1.sock", fromName: "n", body: "hello")
        XCTAssertEqual(PeerParser.receivedKey(observing: "U", tag: tag),
                       "recv:U:uds:/tmp/cc-socks/1.sock:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824")
        XCTAssertTrue(PeerParser.receivedKey(observing: "U", tag: .init(from: nil, fromName: "n", body: "hello")).hasPrefix("recv:U:n:"))
        XCTAssertEqual(PeerParser.sentKey(toolUseId: "toolu_1"), "sent:toolu_1")
    }

    func record(_ key: String, _ observedIn: String, _ dir: String, peer: String?, text: String, at: String,
                status: String, summary: String? = nil, error: String? = nil, ccName: String? = nil) -> PeerRecord {
        PeerRecord(key: key, observedIn: observedIn, direction: dir, peerUuid: peer, peerCcName: ccName, peerAddress: nil,
                   summary: summary, text: text, at: at, status: status, error: error)
    }

    func testStoreAppendDedupAndReload() throws {
        let path = tmp + "/state/peer-messages.jsonl"
        let store = PeerStore(path: path)
        let r = record("sent:t1", "A", "sent", peer: "B", text: "hi", at: "2026-11-05T12:00:00.000Z", status: "delivered", summary: "s")
        XCTAssertTrue(store.append(r))
        XCTAssertFalse(store.append(r))
        // A torn last line (crash mid-write) is skipped and never glued onto the next record
        let handle = try XCTUnwrap(FileHandle(forWritingAtPath: path))
        try handle.seekToEnd(); try handle.write(contentsOf: Data(#"{"key":"torn"#.utf8)); try handle.close()
        let reloaded = PeerStore(path: path)
        XCTAssertEqual(reloaded.all(), [r])
        let r2 = record("recv:A:x:y", "A", "received", peer: nil, text: "yo", at: "2026-11-05T12:01:00.000Z", status: "received")
        XCTAssertTrue(reloaded.append(r2))
        XCTAssertEqual(PeerStore(path: path).all(), [r, r2])
        // Optional fields are omitted on disk
        let text = try String(contentsOfFile: path, encoding: .utf8)
        XCTAssertFalse(text.contains("peerUuid\":null"))
        XCTAssertFalse(text.split(separator: "\n").last!.contains("peerUuid"))
        XCTAssertEqual(PeerStore(path: tmp + "/nothing.jsonl").all(), [])
    }

    // MARK: - Threads

    let A = "aaaa1111-0000-0000-0000-000000000000"
    let B = "bbbb2222-0000-0000-0000-000000000000"
    let C = "cccc3333-0000-0000-0000-000000000000"

    func threadRecords() -> [PeerRecord] {
        [
            // A asks B (both managed): A's sent + B's received → one message
            record("sent:t1", A, "sent", peer: B, text: "About to archive 109", at: "2026-11-05T19:18:02.114Z",
                   status: "delivered", summary: "Ask before archiving", ccName: "mclaude-v1-b"),
            record("recv:\(B):uds:/a.sock:h1", B, "received", peer: A, text: "About to archive 109",
                   at: "2026-11-05T19:18:03.000Z", status: "received", ccName: "mclaude-v1-a"),
            // B replies; A's received record, plus B's sent
            record("recv:\(A):uds:/b.sock:h2", A, "received", peer: B, text: "ok\nnothing in flight",
                   at: "2026-11-05T19:19:00.500Z", status: "received", ccName: "mclaude-v1-b"),
            record("sent:t2", B, "sent", peer: A, text: "ok\nnothing in flight", at: "2026-11-05T19:19:00.000Z",
                   status: "delivered", summary: "Reply ok"),
            // Same text again, too far apart to merge
            record("recv:\(B):uds:/a.sock:h1b", B, "received", peer: A, text: "About to archive 109",
                   at: "2026-11-05T19:30:00.000Z", status: "received"),
            // A failed send to B
            record("sent:t3", A, "sent", peer: B, text: "ping", at: "2026-11-05T19:40:00.000Z", status: "failed", error: "nope"),
            // A talks to C once; unresolved send is left out
            record("sent:t4", A, "sent", peer: C, text: "hello C", at: "2026-11-05T18:00:00.000Z", status: "delivered"),
            record("sent:t5", A, "sent", peer: nil, text: "to nobody", at: "2026-11-05T20:00:00.000Z", status: "failed", error: "No agent"),
        ]
    }

    func testThreadMergeAndDirection() {
        let recs = threadRecords()
        let fromA = PeerThreads.items(this: A, peer: B, records: recs)
        XCTAssertEqual(fromA.map(\.id), ["sent:t1", "recv:\(A):uds:/b.sock:h2", "recv:\(B):uds:/a.sock:h1b", "sent:t3"])
        XCTAssertEqual(fromA.map(\.direction), ["sent", "received", "sent", "sent"])
        XCTAssertEqual(fromA.map(\.status), ["delivered", "received", "delivered", "failed"])
        XCTAssertEqual(fromA[0].summary, "Ask before archiving")
        XCTAssertEqual(fromA[1].summary, "Reply ok")              // merged: the sender's summary
        XCTAssertEqual(fromA[1].at, "2026-11-05T19:19:00.000Z")   // the earlier at
        XCTAssertEqual(fromA[3].error, "nope")

        let fromB = PeerThreads.items(this: B, peer: A, records: recs)
        XCTAssertEqual(fromB.map(\.id), ["recv:\(B):uds:/a.sock:h1", "sent:t2", "recv:\(B):uds:/a.sock:h1b", "sent:t3"])
        XCTAssertEqual(fromB.map(\.direction), ["received", "sent", "received", "received"])
        XCTAssertEqual(fromB[0].summary, "Ask before archiving")
        XCTAssertEqual(fromB[0].at, "2026-11-05T19:18:02.114Z")
        XCTAssertEqual(fromB[3].status, "failed")
        XCTAssertEqual(PeerThreads.items(this: A, peer: "zzzz", records: recs), [])
    }

    func testPeersList() {
        let recs = threadRecords()
        let directory = PeerDirectory(open: [B: .init(id: "s2", name: "Gmail config")], ended: [C: "Old work"])
        let list = PeerAPI.peers(this: A, records: recs, directory: directory, visible: { _ in true })
        XCTAssertEqual(list.map(\.peer.claudeSessionId), [B, C])
        XCTAssertEqual(list[0].messageCount, 4)
        XCTAssertEqual(list[0].lastAt, "2026-11-05T19:40:00.000Z")
        XCTAssertEqual(list[0].lastDirection, "sent")
        XCTAssertEqual(list[0].lastPreview, "ping")
        XCTAssertEqual(list[0].peer, PeerRef(claudeSessionId: B, ccName: "mclaude-v1-b", address: nil, sessionId: "s2",
                                             displayName: "Gmail config", open: true))
        XCTAssertEqual(list[1].peer.displayName, "Old work")
        XCTAssertFalse(list[1].peer.open)
        // A caller who can't see s2 gets no link and no session name
        let hidden = PeerAPI.peers(this: A, records: recs, directory: directory, visible: { _ in false })
        XCTAssertNil(hidden[0].peer.sessionId)
        XCTAssertEqual(hidden[0].peer.displayName, "mclaude-v1-b")
        XCTAssertEqual(PeerAPI.peers(this: "nobody", records: recs, directory: directory, visible: { _ in true }).count, 0)
    }

    func testThreadResponse() throws {
        let recs = threadRecords()
        let directory = PeerDirectory(open: [A: .init(id: "s1", name: "Drawer/resume"), B: .init(id: "s2", name: "Gmail config")])
        let thread = try XCTUnwrap(PeerAPI.thread(this: B, peer: A, records: recs, directory: directory, registry: [],
                                                  visible: { _ in true }))
        XCTAssertEqual(thread.this.displayName, "Gmail config")
        XCTAssertEqual(thread.this.ccName, "mclaude-v1-b")
        XCTAssertEqual(thread.peer.sessionId, "s1")
        XCTAssertEqual(thread.peer.ccName, "mclaude-v1-a")
        XCTAssertEqual(thread.messages.count, 4)
        XCTAssertNil(PeerAPI.thread(this: B, peer: C, records: recs, directory: directory, registry: [], visible: { _ in true }))
    }

    func testPreview() {
        XCTAssertEqual(PeerThreads.preview("\n\n  First line  \nsecond"), "First line")
        XCTAssertEqual(PeerThreads.preview(String(repeating: "x", count: 130)), String(repeating: "x", count: 120) + "…")
        XCTAssertEqual(PeerThreads.preview(String(repeating: "x", count: 120)), String(repeating: "x", count: 120))
    }

    func testDisplayNameRules() {
        let d = PeerDirectory(open: [A: .init(id: "s1", name: "Open name")], ended: [B: "Ended name"])
        XCTAssertEqual(d.ref(claudeSessionId: A, ccName: "cc", address: nil, visible: { _ in true }).displayName, "Open name")
        XCTAssertEqual(d.ref(claudeSessionId: B, ccName: "cc", address: nil, visible: { _ in true }).displayName, "Ended name")
        XCTAssertEqual(d.ref(claudeSessionId: C, ccName: "cc", address: nil, visible: { _ in true }).displayName, "cc")
        XCTAssertEqual(d.ref(claudeSessionId: nil, ccName: nil, address: "uds:/x", visible: { _ in true }).displayName, "Another session")
    }

    // MARK: - Observation (live) and history enrichment

    func testLiveObservationRecordsAndEnriches() throws {
        seedRegistry()
        let store = PeerStore(path: tmp + "/peer-messages.jsonl")
        let observer = PeerObserver(store: store, registryDir: tmp + "/sessions")
        let me = "ffff0000-0000-0000-0000-000000000000"
        var pending: [String: PendingPeerSend] = [:]

        // Received (idle delivery), with a sentence of ordinary text after the tag
        let text = "Another Claude session sent a message:\n<cross-session-message from=\"uds:/tmp/cc-socks/100.sock\" from-name=\"mclaude-v1-f1\" from-mode=\"bypass\">\nhello\n</cross-session-message>\n\n\(trailer)"
        let user = SessionEvent(uuid: "u1", timestamp: "2026-11-05T12:00:00.000Z", type: .user, text: text, thinking: nil,
                                toolUse: nil, toolResults: nil, model: nil, durationMs: nil)
        let e1 = observer.observeLive(user, observing: me, now: "2026-11-05T12:00:00.000Z", pending: &pending)
        let m = try XCTUnwrap(e1.peerMessages?.first)
        XCTAssertEqual(m.direction, "received")
        XCTAssertEqual(m.status, "received")
        XCTAssertEqual(m.text, "hello")
        XCTAssertEqual(m.peer.claudeSessionId, "aaaa1111-0000-0000-0000-000000000000")
        XCTAssertEqual(m.peer.ccName, "mclaude-v1-f1")
        XCTAssertNil(e1.peerOtherText)
        XCTAssertEqual(e1.text, text)                                // unchanged
        XCTAssertEqual(store.all().count, 1)
        _ = observer.observeLive(user, observing: me, now: "2026-11-05T12:05:00.000Z", pending: &pending)
        XCTAssertEqual(store.all().count, 1)                         // same key: not written again

        // Sent: tool_use → "sending", resolved now; tool_result → record + delivery
        let input = #"{"to":"dup [cccc33]","message":"reply","summary":"Re","recipient_kind":"name"}"#
        let use = SessionEvent(uuid: "a1", timestamp: "2026-11-05T12:01:00.000Z", type: .toolUse, text: nil, thinking: nil,
                               toolUse: ToolUseBlock(id: "toolu_9", name: "SendMessage", inputSummary: "", fullInput: input),
                               toolResults: nil, model: nil, durationMs: nil)
        let e2 = observer.observeLive(use, observing: me, now: "2026-11-05T12:01:00.000Z", pending: &pending)
        XCTAssertEqual(e2.peerMessages?.first?.status, "sending")
        XCTAssertEqual(e2.peerMessages?.first?.id, "sent:toolu_9")
        XCTAssertEqual(e2.peerMessages?.first?.peer.claudeSessionId, "cccc3333-0000-0000-0000-000000000000")
        XCTAssertEqual(e2.peerMessages?.first?.peer.ccName, "dup")
        XCTAssertNil(store.record(forKey: "sent:toolu_9"))           // not written before the result
        let result = SessionEvent(uuid: "r1", timestamp: "2026-11-05T12:01:02.000Z", type: .toolResult, text: nil, thinking: nil,
                                  toolUse: nil, toolResults: [ToolResultBlock(toolUseId: "toolu_9", content: #"{"success":true,"message":"ok"}"#, isError: false)],
                                  model: nil, durationMs: nil)
        let e3 = observer.observeLive(result, observing: me, now: "2026-11-05T12:01:02.000Z", pending: &pending)
        XCTAssertEqual(e3.peerDelivery, PeerDelivery(toolUseId: "toolu_9", status: "delivered"))
        let rec = try XCTUnwrap(store.record(forKey: "sent:toolu_9"))
        XCTAssertEqual(rec.at, "2026-11-05T12:01:00.000Z")           // the tool_use's time
        XCTAssertEqual(rec.summary, "Re")
        XCTAssertTrue(pending.isEmpty)

        // Subagent sends keep the tool row
        let agentUse = SessionEvent(uuid: "a2", timestamp: "t", type: .toolUse, text: nil, thinking: nil,
                                    toolUse: ToolUseBlock(id: "toolu_10", name: "SendMessage", inputSummary: "",
                                                          fullInput: #"{"to":"afb801a3411250e7c","message":"x"}"#),
                                    toolResults: nil, model: nil, durationMs: nil)
        XCTAssertNil(observer.observeLive(agentUse, observing: me, now: "t", pending: &pending).peerMessages)

        // Failed send to a nonexistent name: stored with an unresolved peer
        let bad = SessionEvent(uuid: "a3", timestamp: "t", type: .toolUse, text: nil, thinking: nil,
                               toolUse: ToolUseBlock(id: "toolu_11", name: "SendMessage", inputSummary: "",
                                                     fullInput: #"{"to":"mclaude-v1-55","message":"x","recipient_kind":"name"}"#),
                               toolResults: nil, model: nil, durationMs: nil)
        _ = observer.observeLive(bad, observing: me, now: "2026-11-05T12:02:00.000Z", pending: &pending)
        let badResult = SessionEvent(uuid: "r2", timestamp: "t", type: .toolResult, text: nil, thinking: nil, toolUse: nil,
                                     toolResults: [ToolResultBlock(toolUseId: "toolu_11", content: #"{"success":false,"message":"No agent named 'mclaude-v1-55' is reachable."}"#, isError: true)],
                                     model: nil, durationMs: nil)
        let e4 = observer.observeLive(badResult, observing: me, now: "t", pending: &pending)
        XCTAssertEqual(e4.peerDelivery?.status, "failed")
        XCTAssertEqual(store.record(forKey: "sent:toolu_11")?.peerUuid, nil)
        XCTAssertEqual(store.record(forKey: "sent:toolu_11")?.peerCcName, "mclaude-v1-55")
        XCTAssertEqual(store.record(forKey: "sent:toolu_11")?.error, "No agent named 'mclaude-v1-55' is reachable.")

        // History: same events enriched from records, no writes
        let count = store.all().count
        let page = observer.enrichHistory([user, use, result, bad], observing: me)
        XCTAssertEqual(store.all().count, count)
        XCTAssertEqual(page[0].peerMessages?.first?.at, "2026-11-05T12:00:00.000Z")
        XCTAssertEqual(page[0].peerMessages?.first?.peer.claudeSessionId, "aaaa1111-0000-0000-0000-000000000000")
        XCTAssertEqual(page[1].peerMessages?.first?.status, "delivered")
        XCTAssertEqual(page[2].peerDelivery?.status, "delivered")
        XCTAssertEqual(page[3].peerMessages?.first?.status, "failed")
    }

    func testMidTurnDeliveryUsesEnqueueTime() throws {
        let store = PeerStore(path: tmp + "/peer-messages.jsonl")
        let observer = PeerObserver(store: store, registryDir: tmp + "/sessions")
        var pending: [String: PendingPeerSend] = [:]
        // stdout replays a mid-turn message (bare tag) when Claude takes it, stamped with the enqueue
        let bare = SessionEvent(uuid: "u", timestamp: "2026-11-05T12:58:09.400Z", type: .user,
                                text: "<cross-session-message from=\"uds:/tmp/cc-socks/1.sock\" from-name=\"a\" from-mode=\"bypass\">\nmid\n</cross-session-message>",
                                thinking: nil, toolUse: nil, toolResults: nil, model: nil, durationMs: nil)
        let e = observer.observeLive(bare, observing: "me", now: "2026-11-05T12:58:09.400Z",
                                     receivedAt: "2026-11-05T12:57:35.036Z", pending: &pending)
        XCTAssertEqual(e.peerMessages?.first?.at, "2026-11-05T12:57:35.036Z")
        XCTAssertEqual(store.all().first?.at, "2026-11-05T12:57:35.036Z")
        XCTAssertNil(e.peerOtherText)
    }

    func testHistoryWithoutRecords() throws {
        let store = PeerStore(path: tmp + "/peer-messages.jsonl")
        let observer = PeerObserver(store: store, registryDir: tmp + "/sessions")
        writeEntry(700, #"{"pid":700,"sessionId":"77770000-0000-0000-0000-000000000000","name":"live-one","messagingSocketPath":"/tmp/cc-socks/700.sock","updatedAt":5}"#)
        let old = SessionEvent(uuid: "u", timestamp: "2026-01-01T00:00:00Z", type: .user,
                               text: "<cross-session-message from=\"uds:/tmp/cc-socks/700.sock\" from-name=\"old-name\">\nold\n</cross-session-message>",
                               thinking: nil, toolUse: nil, toolResults: nil, model: nil, durationMs: nil)
        let answered = SessionEvent(uuid: "a", timestamp: "2026-01-01T00:00:01Z", type: .toolUse, text: nil, thinking: nil,
                                    toolUse: ToolUseBlock(id: "t-old", name: "SendMessage", inputSummary: "",
                                                          fullInput: #"{"to":"live-one","message":"x"}"#),
                                    toolResults: nil, model: nil, durationMs: nil)
        let answer = SessionEvent(uuid: "r", timestamp: "2026-01-01T00:00:02Z", type: .toolResult, text: nil, thinking: nil, toolUse: nil,
                                  toolResults: [ToolResultBlock(toolUseId: "t-old", content: #"{"success":false,"message":"gone"}"#, isError: true)],
                                  model: nil, durationMs: nil)
        let inFlight = SessionEvent(uuid: "b", timestamp: "2026-01-01T00:00:03Z", type: .toolUse, text: nil, thinking: nil,
                                    toolUse: ToolUseBlock(id: "t-new", name: "SendMessage", inputSummary: "",
                                                          fullInput: #"{"to":"live-one","message":"y"}"#),
                                    toolResults: nil, model: nil, durationMs: nil)
        let page = observer.enrichHistory([old, answered, answer, inFlight], observing: "me")
        // Pre-feature received: unresolved even though the registry has that socket now
        let m = try XCTUnwrap(page[0].peerMessages?.first)
        XCTAssertNil(m.peer.claudeSessionId)
        XCTAssertEqual(m.peer.displayName, "old-name")
        XCTAssertEqual(m.at, "2026-01-01T00:00:00Z")
        // Answered send with no record: unresolved, status from its result
        XCTAssertNil(page[1].peerMessages?.first?.peer.claudeSessionId)
        XCTAssertEqual(page[1].peerMessages?.first?.status, "failed")
        XCTAssertEqual(page[1].peerMessages?.first?.error, "gone")
        XCTAssertEqual(page[2].peerDelivery, PeerDelivery(toolUseId: "t-old", status: "failed", error: "gone"))
        // Result still pending: resolved against the live registry
        XCTAssertEqual(page[3].peerMessages?.first?.status, "sending")
        XCTAssertEqual(page[3].peerMessages?.first?.peer.claudeSessionId, "77770000-0000-0000-0000-000000000000")
        XCTAssertTrue(store.all().isEmpty)
    }

    /// Idle delivery in a headless transcript: queue-operation enqueue (bare tag) + dequeue + the
    /// wrapped user line. History shows it once.
    func testHistoryDedupsIdleDeliveryAgainstEnqueue() async throws {
        let projects = tmp + "/projects"
        let cwd = "/nonexistent/peer-test"
        let sid = "e0d20754-139f-46c2-95fe-bc1d7e04cec2"
        let dir = projects + "/" + ClaudePaths.encode(cwd)
        try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        let tag = #"<cross-session-message from=\"uds:/tmp/cc-socks/92463.sock\" from-name=\"peer-test-4a\" from-mode=\"bypass\">\nmid-turn hello from A\n</cross-session-message>"#
        let lines = [
            #"{"type":"queue-operation","operation":"enqueue","timestamp":"2026-11-05T12:54:59.668Z","sessionId":"\#(sid)","content":"\#(tag)"}"#,
            #"{"type":"queue-operation","operation":"dequeue","timestamp":"2026-11-05T12:54:59.674Z","sessionId":"\#(sid)"}"#,
            #"{"type":"user","uuid":"6737ac11","timestamp":"2026-11-05T12:54:59.698Z","message":{"role":"user","content":"Another Claude session sent a message:\n\#(tag)\n\nThis came from another Claude session — not typed by your user."}}"#,
            #"{"type":"assistant","uuid":"32e78f4d","timestamp":"2026-11-05T12:55:01.535Z","message":{"content":[{"type":"text","text":"Hello"}]}}"#,
        ]
        FileManager.default.createFile(atPath: "\(dir)/\(sid).jsonl", contents: Data(lines.joined(separator: "\n").utf8))
        let events = await JSONLTailer(projectsDir: projects).getRecentEvents(id: "s1", sessionId: sid, cwd: cwd)
        XCTAssertEqual(events.map(\.type), [.user, .text])
        let observer = PeerObserver(store: PeerStore(path: tmp + "/p.jsonl"), registryDir: tmp + "/sessions")
        let enriched = observer.enrichHistory(events, observing: sid)
        XCTAssertEqual(enriched[0].peerMessages?.first?.text, "mid-turn hello from A")
        XCTAssertEqual(enriched[0].peerMessages?.first?.peer.ccName, "peer-test-4a")
    }

    func testEventEncodingOmitsEmptyPeerFields() throws {
        let e = SessionEvent(uuid: "u", timestamp: "t", type: .user, text: "x", thinking: nil, toolUse: nil,
                             toolResults: nil, model: nil, durationMs: nil)
        let json = String(data: try JSONEncoder().encode(e), encoding: .utf8)!
        XCTAssertFalse(json.contains("peer"))
        var p = e
        p.peerDelivery = PeerDelivery(toolUseId: "t1", status: "delivered")
        let pj = String(data: try JSONEncoder().encode(p), encoding: .utf8)!
        XCTAssertTrue(pj.contains(#""peerDelivery":{"status":"delivered","toolUseId":"t1"}"#)
                      || pj.contains(#""peerDelivery":{"toolUseId":"t1","status":"delivered"}"#))
        XCTAssertFalse(pj.contains("error"))
    }
}
