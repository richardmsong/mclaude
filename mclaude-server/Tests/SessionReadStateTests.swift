import XCTest
@testable import mclaude_server

/// Session order, read state and background work (docs/design-claude-uplift.md).
final class SessionReadStateTests: XCTestCase {
    private var dir: String!

    override func setUp() {
        dir = NSTemporaryDirectory() + "read-state-\(UUID().uuidString)"
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
    }

    override func tearDown() {
        try? FileManager.default.removeItem(atPath: dir)
    }

    private func json(_ s: String) -> [String: Any] {
        try! JSONSerialization.jsonObject(with: Data(s.utf8)) as! [String: Any]
    }

    private func userLine(_ text: String) -> [String: Any] {
        ["type": "user", "message": ["role": "user", "content": text]]
    }

    private func ev(_ text: String, at ts: String, subagent: Bool = false) -> SessionEvent {
        SessionEvent(uuid: UUID().uuidString, timestamp: ts, type: .user, text: text, thinking: nil, toolUse: nil,
                     toolResults: nil, model: nil, durationMs: nil,
                     subagentInfo: subagent ? SubagentInfo(agentId: "a", agentType: "x", description: "d", parentToolUseId: "a") : nil)
    }

    // MARK: - Interrupted-tasks note

    func testNoteText() {
        let note = SessionManager.interruptedTasksNote([
            BackgroundTask(id: "a1", taskType: "local_agent", description: "Map server code"),
            BackgroundTask(id: "b1", taskType: "local_bash", description: "npm test"),
            BackgroundTask(id: "w1", taskType: "monitor", description: "Watch the build"),
        ])
        XCTAssertEqual(note, "A restart stopped your background tasks; they won't report back: Agent \"Map server code\", command \"npm test\", monitor \"Watch the build\". Restart any you still need, then continue.")
        XCTAssertNil(SessionManager.interruptedTasksNote([]))
        XCTAssertTrue(note!.hasPrefix(SessionManager.tasksNotePrefix))
    }

    func testMessageAfterLaunch() {
        let note = "A restart stopped your background tasks; they won't report back: command \"x\". Restart any you still need, then continue."
        XCTAssertNil(SessionManager.messageAfterLaunch(resume: nil, note: nil))
        XCTAssertEqual(SessionManager.messageAfterLaunch(resume: nil, note: note), note)
        XCTAssertEqual(SessionManager.messageAfterLaunch(resume: SessionManager.restartResumeText, note: nil),
                       SessionManager.restartResumeText)
        XCTAssertEqual(SessionManager.messageAfterLaunch(resume: SessionManager.crashResumeText, note: note),
                       SessionManager.crashResumeText + "\n\n" + note)
    }

    func testServerMessagesAreHidden() {
        let note = SessionManager.interruptedTasksNote([BackgroundTask(id: "a", taskType: "local_agent", description: "Scan")])!
        let restartCombined = SessionManager.messageAfterLaunch(resume: SessionManager.restartResumeText, note: note)!
        let crashCombined = SessionManager.messageAfterLaunch(resume: SessionManager.crashResumeText, note: note)!

        // Exact restart text, the note alone (prefix match), and restart + note: hidden
        for hidden in [SessionManager.restartResumeText, note, SessionManager.tasksNotePrefix + "Agent \"other\". …", restartCombined] {
            XCTAssertEqual(SessionManager.visibleText(ofUserLine: hidden), "", hidden)
            XCTAssertNil(JSONLParser.parseUserEvent(json: userLine(hidden), uuid: "u", timestamp: "t"), hidden)
        }
        // Crash resume + note: the crash resume still shows, without the note
        XCTAssertEqual(JSONLParser.parseUserEvent(json: userLine(crashCombined), uuid: "u", timestamp: "t")?.text,
                       SessionManager.crashResumeText)
        XCTAssertEqual(JSONLParser.parseUserEvent(json: userLine(SessionManager.crashResumeText), uuid: "u", timestamp: "t")?.text,
                       SessionManager.crashResumeText)
        // Ordinary text, even mentioning the note, is unchanged
        let mention = "Why did you say \"\(SessionManager.tasksNotePrefix)\"?"
        XCTAssertEqual(JSONLParser.parseUserEvent(json: userLine(mention), uuid: "u", timestamp: "t")?.text, mention)

        // History: a transcript line and a queue-operation enqueue
        let line = String(data: try! JSONSerialization.data(withJSONObject: [
            "type": "user", "uuid": "u1", "timestamp": "2026-11-09T10:00:00.000Z",
            "message": ["role": "user", "content": restartCombined]]), encoding: .utf8)!
        XCTAssertNil(JSONLParser.parseEvent(line: line))
        XCTAssertNil(JSONLParser.parseQueueOperation(json: ["operation": "enqueue", "content": note], timestamp: "t"))
        XCTAssertEqual(JSONLParser.parseQueueOperation(json: ["operation": "enqueue", "content": "hi"], timestamp: "t")?.text, "hi")
    }

    // MARK: - Background tasks

    func testBackgroundTaskTransitions() {
        var t = BackgroundTaskTracker()
        // Started in the background
        XCTAssertTrue(t.apply(json(#"{"type":"system","subtype":"task_started","task_id":"bqceph9tg","tool_use_id":"toolu_1","description":"Sleep 2 seconds then print hi","is_backgrounded":true,"task_type":"local_bash"}"#)))
        XCTAssertEqual(t.running, [BackgroundTask(id: "bqceph9tg", taskType: "local_bash", description: "Sleep 2 seconds then print hi")])
        // A foreground agent: not counted until a patch moves it to the background
        XCTAssertFalse(t.apply(json(#"{"type":"system","subtype":"task_started","task_id":"a7","description":"Map server code","is_backgrounded":false,"task_type":"local_agent","subagent_type":"Explore","prompt":"…"}"#)))
        XCTAssertEqual(t.running.count, 1)
        XCTAssertTrue(t.apply(json(#"{"type":"system","subtype":"task_updated","task_id":"a7","patch":{"is_backgrounded":true}}"#)))
        XCTAssertEqual(t.running.last, BackgroundTask(id: "a7", taskType: "local_agent", description: "Map server code"))
        // Moving it again changes nothing; a running status changes nothing
        XCTAssertFalse(t.apply(json(#"{"type":"system","subtype":"task_updated","task_id":"a7","patch":{"is_backgrounded":true}}"#)))
        XCTAssertFalse(t.apply(json(#"{"type":"system","subtype":"task_updated","task_id":"a7","patch":{"status":"running"}}"#)))
        // Ends with a patch status other than running/pending
        XCTAssertTrue(t.apply(json(#"{"type":"system","subtype":"task_updated","task_id":"bqceph9tg","patch":{"status":"completed","end_time":1762700000000}}"#)))
        XCTAssertEqual(t.running.map(\.id), ["a7"])
        // …or its notification, whichever comes first (the later one is a no-op)
        XCTAssertFalse(t.apply(json(#"{"type":"system","subtype":"task_notification","task_id":"bqceph9tg","tool_use_id":"toolu_1","status":"completed"}"#)))
        XCTAssertTrue(t.apply(json(#"{"type":"system","subtype":"task_notification","task_id":"a7","status":"completed"}"#)))
        XCTAssertTrue(t.running.isEmpty)
        // A foreground task finishing never touches the set
        XCTAssertFalse(t.apply(json(#"{"type":"system","subtype":"task_started","task_id":"f1","description":"ls","is_backgrounded":false,"task_type":"local_bash"}"#)))
        XCTAssertFalse(t.apply(json(#"{"type":"system","subtype":"task_notification","task_id":"f1","status":"completed"}"#)))
        // Killed / failed end a task too
        _ = t.apply(json(#"{"type":"system","subtype":"task_started","task_id":"k","description":"w","is_backgrounded":true,"task_type":"local_bash"}"#))
        XCTAssertTrue(t.apply(json(#"{"type":"system","subtype":"task_updated","task_id":"k","patch":{"status":"killed"}}"#)))
        XCTAssertFalse(t.apply(json(#"{"type":"system","subtype":"init"}"#)))
    }

    /// Observed 2026-11-09: when the server stops Claude (restart, drain), Claude Code stops its
    /// background shells itself and reports `status: stopped`. Those stay listed for the note.
    func testServerStopKeepsStoppedTasks() {
        var t = BackgroundTaskTracker()
        _ = t.apply(json(#"{"subtype":"task_started","task_id":"bfwghsjq7","description":"Long sleep","is_backgrounded":true,"task_type":"local_bash"}"#))
        XCTAssertFalse(t.apply(json(#"{"subtype":"task_updated","task_id":"bfwghsjq7","patch":{"status":"stopped"}}"#), keepStopped: true))
        XCTAssertFalse(t.apply(json(#"{"subtype":"task_notification","task_id":"bfwghsjq7","status":"stopped"}"#), keepStopped: true))
        XCTAssertFalse(t.apply(json(#"{"subtype":"task_updated","task_id":"bfwghsjq7","patch":{"status":"killed"}}"#), keepStopped: true))
        XCTAssertEqual(t.running.map(\.id), ["bfwghsjq7"])
        XCTAssertNotNil(SessionManager.interruptedTasksNote(t.running))
        // A task that really finishes during the stop still ends
        XCTAssertTrue(t.apply(json(#"{"subtype":"task_notification","task_id":"bfwghsjq7","status":"completed"}"#), keepStopped: true))
        XCTAssertTrue(t.running.isEmpty)
        // Outside a server stop, "stopped" ends it (Claude or the user stopped it)
        _ = t.apply(json(#"{"subtype":"task_started","task_id":"s2","description":"w","is_backgrounded":true,"task_type":"local_bash"}"#))
        XCTAssertTrue(t.apply(json(#"{"subtype":"task_notification","task_id":"s2","status":"stopped"}"#)))
    }

    func testTrackerResumesPersistedSetAndResets() {
        var t = BackgroundTaskTracker(running: [BackgroundTask(id: "x", taskType: "local_bash", description: "npm test")])
        XCTAssertEqual(t.running.count, 1)
        XCTAssertTrue(t.apply(json(#"{"subtype":"task_notification","task_id":"x","status":"completed"}"#)))
        _ = t.apply(json(#"{"subtype":"task_started","task_id":"y","description":"d","is_backgrounded":true,"task_type":"local_agent"}"#))
        t.reset()
        XCTAssertTrue(t.running.isEmpty)
        XCTAssertTrue(t.known.isEmpty)
    }

    // MARK: - Peer turns

    func testPeerOnlyClassification() throws {
        let observer = PeerObserver(store: PeerStore(path: "\(dir!)/peers.jsonl"), registryDir: "\(dir!)/registry")
        let trailer = "This came from another Claude session — not typed by your user, but very likely working on their behalf."
        let idle = "Another Claude session sent a message:\n<cross-session-message from=\"uds:/tmp/cc-socks/1.sock\" from-name=\"mclaude-v1-f1\">\nAbout to archive build 109.\n</cross-session-message>\n\n\(trailer)"
        let midTurn = "<cross-session-message from=\"uds:/tmp/cc-socks/1.sock\">\nhi\n</cross-session-message>"
        let mixed = "<cross-session-message from=\"uds:/tmp/cc-socks/1.sock\" from-name=\"x-1\">\nhi\n</cross-session-message>\nAlso, please fix the tests."
        func enriched(_ text: String) throws -> SessionEvent {
            let e = try XCTUnwrap(JSONLParser.parseUserEvent(json: userLine(text), uuid: UUID().uuidString, timestamp: "2026-11-09T10:00:00.000Z"))
            return observer.enrichHistory([e], observing: "self-uuid")[0]
        }
        XCTAssertEqual(SessionManager.peerOnlySender(try enriched(idle)), "mclaude-v1-f1")
        XCTAssertEqual(SessionManager.peerOnlySender(try enriched(midTurn)), "Another session")   // no from-name
        XCTAssertNil(SessionManager.peerOnlySender(try enriched(mixed)))                            // has peerOtherText
        XCTAssertNil(SessionManager.peerOnlySender(try enriched("Fix the login bug")))
    }

    // MARK: - Read state

    func testUnreadExposureRule() {
        func shown(pending: Bool = true, running: Bool = true, status: SessionStatus = .idle, prompt: Bool = false,
                   turn: Bool = false, tasks: Int = 0) -> Bool {
            SessionManager.unreadShown(pending: pending, processRunning: running, status: status, promptPending: prompt,
                                       turnActive: turn, backgroundTasks: tasks)
        }
        XCTAssertTrue(shown())
        XCTAssertFalse(shown(pending: false))
        XCTAssertFalse(shown(running: false))
        XCTAssertFalse(shown(status: .working))
        XCTAssertFalse(shown(status: .waitingForInput, prompt: true))
        XCTAssertFalse(shown(prompt: true))
        XCTAssertFalse(shown(turn: true))
        XCTAssertFalse(shown(tasks: 2))
    }

    func testLastPromptDateSkipsPeerTaskAndServerLines() throws {
        let observer = PeerObserver(store: PeerStore(path: "\(dir!)/peers.jsonl"), registryDir: "\(dir!)/registry")
        let peer = "<cross-session-message from=\"uds:/tmp/cc-socks/1.sock\" from-name=\"p\">\nhi\n</cross-session-message>"
        let events = observer.enrichHistory([
            ev("first prompt", at: "2026-11-09T09:00:00.000Z"),
            ev("real prompt", at: "2026-11-09T10:00:00.000Z"),
            ev(peer, at: "2026-11-09T11:00:00.000Z"),
            ev("<task-notification>\n<task-id>b1</task-id>\n<status>completed</status>\n</task-notification>", at: "2026-11-09T11:10:00.000Z"),
            ev(SessionManager.crashResumeText, at: "2026-11-09T11:20:00.000Z"),
            ev("subagent prompt", at: "2026-11-09T11:30:00.000Z", subagent: true),
        ], observing: "self-uuid")
        XCTAssertEqual(SessionManager.lastPromptDate(in: events), Date.fromISO("2026-11-09T10:00:00.000Z"))
        XCTAssertNil(SessionManager.lastPromptDate(in: Array(events.suffix(4))))
        XCTAssertTrue(JSONLParser.isOnlyTaskNotifications("<task-notification><task-id>x</task-id></task-notification>"))
        XCTAssertFalse(JSONLParser.isOnlyTaskNotifications("<task-notification><task-id>x</task-id></task-notification> and more"))
        XCTAssertFalse(JSONLParser.isOnlyTaskNotifications("hello"))
    }

    // MARK: - Persistence and wire format

    func testOldRecordDecodesWithoutNewFields() throws {
        let old = #"""
        {"id":"s3","index":3,"claudeSessionId":"abc","cwd":"/tmp","windowName":"x","createdAt":"2026-11-01T10:00:00Z","hasConversation":true,"turnInProgress":false}
        """#
        let r = try JSONDecoder.iso.decode(ManagedSessionRecord.self, from: Data(old.utf8))
        XCTAssertNil(r.lastInputAt)
        XCTAssertNil(r.unreadPending)
        XCTAssertNil(r.backgroundTasks)

        var updated = r
        updated.lastInputAt = Date(timeIntervalSince1970: 1_762_700_000)
        updated.unreadPending = true
        updated.backgroundTasks = [BackgroundTask(id: "b", taskType: "local_bash", description: "npm test")]
        let back = try JSONDecoder.iso.decode(ManagedSessionRecord.self, from: JSONEncoder.iso.encode(updated))
        XCTAssertEqual(back.lastInputAt, updated.lastInputAt)
        XCTAssertEqual(back.unreadPending, true)
        XCTAssertEqual(back.backgroundTasks, updated.backgroundTasks)
    }

    func testSessionFieldsOmittedWhenUnset() throws {
        var s = ClaudeSession(id: "s1", pid: 0, sessionId: "x", cwd: "/tmp", startedAt: Date(), name: "n", status: .idle,
                              statusSince: nil, projectName: "tmp", lastOutput: "", prompt: nil)
        let enc = JSONEncoder()
        enc.dateEncodingStrategy = .iso8601
        var obj = try JSONSerialization.jsonObject(with: enc.encode(s)) as! [String: Any]
        for key in ["lastInputAt", "unread", "peerTurn", "backgroundTasks"] { XCTAssertNil(obj[key], key) }
        s.lastInputAt = Date(timeIntervalSince1970: 1_762_700_000)
        s.unread = true
        s.peerTurn = "mclaude-v1-f1"
        s.backgroundTasks = 2
        obj = try JSONSerialization.jsonObject(with: enc.encode(s)) as! [String: Any]
        XCTAssertEqual(obj["lastInputAt"] as? String, "2025-11-09T14:53:20Z")
        XCTAssertEqual(obj["unread"] as? Bool, true)
        XCTAssertEqual(obj["peerTurn"] as? String, "mclaude-v1-f1")
        XCTAssertEqual(obj["backgroundTasks"] as? Int, 2)
    }

    // MARK: - SessionManager (no Claude process is started)

    func testMigrationAndMarkRead() async throws {
        let stateDir = "\(dir!)/state", cwd = "\(dir!)/work"
        try FileManager.default.createDirectory(atPath: stateDir, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(atPath: cwd, withIntermediateDirectories: true)
        // An old state file: no lastInputAt/unreadPending/backgroundTasks. startOnFirstMessage keeps
        // it from launching Claude at boot.
        let state = """
        {"nextIndex":2,"sessions":[{"id":"s1","index":1,"claudeSessionId":"\(UUID().uuidString.lowercased())","cwd":"\(cwd)","windowName":"w","createdAt":"2026-11-01T10:00:00Z","hasConversation":false,"startOnFirstMessage":true}]}
        """
        FileManager.default.createFile(atPath: "\(stateDir)/managed-sessions.json", contents: Data(state.utf8))
        let manager = SessionManager(stateDir: stateDir, registryDir: "\(dir!)/registry")
        await manager.start()
        let s = await manager.getSession(id: "s1")
        XCTAssertEqual(s?.lastInputAt, Date.fromISO("2026-11-01T10:00:00Z"))   // no transcript: creation time
        XCTAssertNil(s?.unread)
        XCTAssertNil(s?.backgroundTasks)
        XCTAssertNil(s?.peerTurn)
        // Migrated value is persisted
        let saved = try String(contentsOfFile: "\(stateDir)/managed-sessions.json", encoding: .utf8)
        XCTAssertTrue(saved.contains("lastInputAt"))

        let unknown = await manager.markRead(id: "s99")
        XCTAssertFalse(unknown)
        let known = await manager.markRead(id: "s1")
        XCTAssertTrue(known)
    }
}
