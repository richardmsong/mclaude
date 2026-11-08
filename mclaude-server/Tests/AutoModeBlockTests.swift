import XCTest
@testable import mclaude_server

final class AutoModeBlockTests: XCTestCase {
    // Verbatim from a headless `--permission-mode auto` session (Claude Code 2.1.317)
    let denial = "Permission for this action was denied by the Claude Code auto mode classifier. Reason: [Auto-Mode Bypass]. If you have other tasks that don't depend on this action, continue working on those."

    func testRecognizesClassifierDenial() {
        XCTAssertTrue(AutoModeBlock.isBlock(ToolResultBlock(toolUseId: "t1", content: denial, isError: true)))
        XCTAssertTrue(AutoModeBlock.isBlock(ToolResultBlock(
            toolUseId: "t1", content: "The server-side auto mode classifier judged this action dangerous (it gave no explanation)", isError: true)))
    }

    func testIgnoresOrdinaryResults() {
        XCTAssertFalse(AutoModeBlock.isBlock(ToolResultBlock(toolUseId: "t1", content: "exit 1", isError: true)))
        // Same words in a successful result (e.g. Claude grepping these docs) aren't a block
        XCTAssertFalse(AutoModeBlock.isBlock(ToolResultBlock(toolUseId: "t1", content: denial, isError: false)))
    }

    func testRuleName() {
        XCTAssertEqual(AutoModeBlock.rule(denial), "Auto-Mode Bypass")
        XCTAssertNil(AutoModeBlock.rule("The server-side auto mode classifier judged this action dangerous"))
        XCTAssertNil(AutoModeBlock.rule("Reason: []"))
    }

    func testPushBody() {
        XCTAssertEqual(AutoModeBlock.pushBody(toolName: "Bash", content: denial), "Blocked: Bash · Auto-Mode Bypass")
        XCTAssertEqual(AutoModeBlock.pushBody(toolName: "Bash", content: "auto mode classifier judged"), "Blocked: Bash")
    }
}
