import XCTest
@testable import mclaude_server

final class SessionRecapperTests: XCTestCase {
    func testParsePlainJSON() {
        let r = SessionRecapper.parse(#"{"goal":"Add a watch app","progress":"List screen done","next":"Build the detail screen","nextBy":"claude"}"#)
        XCTAssertEqual(r?.goal, "Add a watch app")
        XCTAssertEqual(r?.progress, "List screen done")
        XCTAssertEqual(r?.next, "Build the detail screen")
        XCTAssertEqual(r?.nextBy, "claude")
    }

    func testParseStripsFencesAndChatter() {
        let raw = """
        Here is the recap:
        ```json
        {"goal": "Fix {braces} in \\"names\\"", "progress": "Found it", "next": "", "nextBy": "None"}
        ```
        """
        let r = SessionRecapper.parse(raw)
        XCTAssertEqual(r?.goal, #"Fix {braces} in "names""#)
        XCTAssertEqual(r?.next, "")
        XCTAssertEqual(r?.nextBy, "none")
    }

    func testParseDropsMarkdownMarkers() {
        let r = SessionRecapper.parse(#"{"goal":"Fix `forecast(city)` **now**","progress":"P","next":"N","nextBy":"you"}"#)
        XCTAssertEqual(r?.goal, "Fix forecast(city) now")
    }

    func testParseSkipsNonJSONBraces() {
        let r = SessionRecapper.parse(#"Use {x} then: {"goal":"G","progress":"P","next":"N","nextBy":"you"}"#)
        XCTAssertEqual(r?.goal, "G")
        XCTAssertEqual(r?.nextBy, "you")
    }

    func testParseFailsOnMissingOrInvalidField() {
        XCTAssertNil(SessionRecapper.parse(#"{"goal":"G","progress":"P","nextBy":"you"}"#))
        XCTAssertNil(SessionRecapper.parse(#"{"goal":"G","progress":"P","next":"N","nextBy":"maybe"}"#))
        XCTAssertNil(SessionRecapper.parse(#"{"goal":"","progress":"P","next":"N","nextBy":"you"}"#))
        XCTAssertNil(SessionRecapper.parse("I can't summarize this."))
        XCTAssertNil(SessionRecapper.parse(#"{"goal":"G", "progress": "#))
    }

    func testParseCollapsesWhitespaceAndClipsFields() {
        let long = String(repeating: "word ", count: 40)
        let raw = #"{"goal":"a\n  b","progress":"\#(long)","next":"\#(long)","nextBy":"claude"}"#
        let r = SessionRecapper.parse(raw)!
        XCTAssertEqual(r.goal, "a b")
        XCTAssertLessThanOrEqual(r.progress.count, SessionRecapper.progressLimit)
        XCTAssertTrue(r.progress.hasSuffix("word…"))
        XCTAssertLessThanOrEqual(r.next.count, SessionRecapper.nextLimit)
    }

    func testClipAtWordBoundary() {
        XCTAssertEqual(SessionRecapper.clip("short", to: 10), "short")
        XCTAssertEqual(SessionRecapper.clip("exactly10!", to: 10), "exactly10!")
        XCTAssertEqual(SessionRecapper.clip("hello brave new world", to: 12), "hello brave…")
        XCTAssertEqual(SessionRecapper.clip("hello, world again", to: 10), "hello…")
        // Boundary exactly at the cut: keep the whole word before it
        XCTAssertEqual(SessionRecapper.clip("abcd efgh ijkl", to: 10), "abcd efgh…")
        // One long word: cut mid-word
        let r = SessionRecapper.clip("Supercalifragilisticexpialidocious", to: 10)
        XCTAssertEqual(r, "Supercali…")
        XCTAssertEqual(r.count, 10)
    }

    func testRecentMessagesKeepsNewestWithinBudget() {
        let msgs: [(fromUser: Bool, text: String)] = [
            (true, String(repeating: "a", count: 500)),
            (false, "old reply"),
            (true, "  "),
            (true, "latest question"),
            (false, "latest answer"),
        ]
        let all = SessionRecapper.recentMessages(msgs)
        XCTAssertTrue(all.hasSuffix("User: latest question\n\nClaude: latest answer"))
        XCTAssertTrue(all.hasPrefix("User: aaa"))

        let small = SessionRecapper.recentMessages(msgs, budget: 63)
        XCTAssertEqual(small, "Claude: old reply\n\nUser: latest question\n\nClaude: latest answer")

        // A single message over budget keeps its end
        let one = SessionRecapper.recentMessages([(false, String(repeating: "x", count: 100) + "END")], budget: 50)
        XCTAssertLessThanOrEqual(one.count, 50)
        XCTAssertTrue(one.hasPrefix("Claude: …"))
        XCTAssertTrue(one.hasSuffix("END"))
    }

    func testFirstPromptFromTranscript() throws {
        let path = NSTemporaryDirectory() + "recap-test-\(UUID().uuidString).jsonl"
        defer { try? FileManager.default.removeItem(atPath: path) }
        let lines = [
            #"{"type":"user","isMeta":true,"message":{"content":"meta"}}"#,
            #"{"type":"user","message":{"content":"<command-name>/clear</command-name>"}}"#,
            #"{"type":"user","message":{"content":[{"type":"text","text":"/tmp/mclaude-screenshots/a.jpg fix   the\nlayout"}]}}"#,
            #"{"type":"user","message":{"content":"second"}}"#,
        ]
        try (lines.joined(separator: "\n") + "\n").write(toFile: path, atomically: true, encoding: .utf8)
        XCTAssertEqual(SessionRecapper.firstPrompt(transcriptPath: path), "fix the layout")
        XCTAssertNil(SessionRecapper.firstPrompt(transcriptPath: path + ".missing"))
    }

    func testPromptIncludesInputs() {
        let p = SessionRecapper.prompt(firstPrompt: "build it", recent: "User: hi", pending: "Claude asked the user:\nWhich?")
        XCTAssertTrue(p.contains("build it"))
        XCTAssertTrue(p.contains("User: hi"))
        XCTAssertTrue(p.contains("Which?"))
        XCTAssertTrue(p.contains("\"nextBy\""))
    }

    // MARK: - Pending prompt detail

    func testQuestionPromptCarriesAllQuestions() throws {
        let qs = [PromptQuestion(question: "Which DB?", options: ["Postgres", "SQLite"], multiSelect: false),
                  PromptQuestion(question: "Which features?", options: ["Auth", "Search"], multiSelect: true)]
        let p = try XCTUnwrap(DetectedPrompt.question(qs))
        XCTAssertEqual(p.question, "Which DB?")
        XCTAssertEqual(p.options, ["Postgres", "SQLite"])
        XCTAssertEqual(p.kind, "question")
        XCTAssertEqual(p.questions, qs)
        XCTAssertNil(DetectedPrompt.question([]))

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(p)) as? [String: Any]
        let encoded = json?["questions"] as? [[String: Any]]
        XCTAssertEqual(encoded?.count, 2)
        XCTAssertEqual(encoded?[1]["question"] as? String, "Which features?")
        XCTAssertEqual(encoded?[1]["options"] as? [String], ["Auth", "Search"])
        XCTAssertEqual(encoded?[1]["multiSelect"] as? Bool, true)
    }

    func testPlanAndToolPrompts() throws {
        XCTAssertEqual(DetectedPrompt.plan.question, "Approve this plan?")
        XCTAssertNil(DetectedPrompt.plan.options)
        XCTAssertEqual(DetectedPrompt.plan.kind, "plan")
        XCTAssertNil(DetectedPrompt.plan.questions)
        let tool = DetectedPrompt.tool("Bash")
        XCTAssertEqual(tool.question, "Allow Bash?")
        XCTAssertEqual(tool.options, ["Yes", "No"])
        XCTAssertEqual(tool.kind, "tool")
        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(tool)) as? [String: Any]
        XCTAssertNil(json?["questions"])   // omitted, not null
    }

    func testSessionRecapEncodesISODateAndIsOmittedWhenNil() throws {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        let recap = SessionRecap(goal: "G", progress: "P", next: "N", nextBy: "you", at: Date(timeIntervalSince1970: 0))
        var s = ClaudeSession(id: "s1", pid: 0, sessionId: "x", cwd: "/", startedAt: Date(), name: "n", status: .idle,
                              statusSince: nil, projectName: "p", lastOutput: "", prompt: nil)
        var json = try JSONSerialization.jsonObject(with: encoder.encode(s)) as? [String: Any]
        XCTAssertNil(json?["recap"])
        s.recap = recap
        json = try JSONSerialization.jsonObject(with: encoder.encode(s)) as? [String: Any]
        let r = json?["recap"] as? [String: Any]
        XCTAssertEqual(r?["nextBy"] as? String, "you")
        XCTAssertEqual(r?["at"] as? String, "1970-01-01T00:00:00Z")
    }
}
