import XCTest
@testable import mclaude_server

final class SessionTitlerTests: XCTestCase {
    func testSanitizePlainTitle() {
        XCTAssertEqual(SessionTitler.sanitize("Fix Weather App Caching\n"), "Fix Weather App Caching")
    }

    func testSanitizeStripsQuotesPrefixAndPunctuation() {
        XCTAssertEqual(SessionTitler.sanitize("Title: \"Add Error Handling.\""), "Add Error Handling")
        XCTAssertEqual(SessionTitler.sanitize("**Refactor  Login   Flow**"), "Refactor Login Flow")
        XCTAssertEqual(SessionTitler.sanitize("\n\n  “Debug TLS Errors”!\nextra line"), "Debug TLS Errors")
    }

    func testSanitizeRejectsEmpty() {
        XCTAssertNil(SessionTitler.sanitize(""))
        XCTAssertNil(SessionTitler.sanitize("  \n \"\" \n"))
    }

    func testSanitizeTruncatesLongOutput() {
        let long = "Supercalifragilisticexpialidocious Antidisestablishmentarianism Pneumonoultramicroscopic Floccinaucinihilipilification"
        let title = SessionTitler.sanitize(long)
        XCTAssertNotNil(title)
        XCTAssertLessThanOrEqual(title!.count, SessionTitler.maxLength + 1)
        XCTAssertTrue(title!.hasSuffix("…"))
    }

    func testPromptDropsUploadedPhotoPaths() {
        let prompt = SessionTitler.titlePrompt(for: "/tmp/mclaude-screenshots/screenshot-1793672207222000.jpg why is this button misaligned?")
        XCTAssertFalse(prompt.contains("/tmp/mclaude-screenshots"))
        XCTAssertTrue(prompt.contains("why is this button misaligned?"))
    }

    func testPromptForPhotoOnlyMessage() {
        let prompt = SessionTitler.titlePrompt(for: "/tmp/mclaude-screenshots/screenshot-1.jpg")
        XCTAssertTrue(prompt.contains("attached images without any text"))
    }

    func testShouldInferTitle() {
        XCTAssertTrue(SessionTitler.shouldInferTitle(fromFirstMessage: "fix the login bug"))
        // Photo messages start with upload paths; they are real prompts, not slash commands
        XCTAssertTrue(SessionTitler.shouldInferTitle(fromFirstMessage:
            "/tmp/mclaude-screenshots/screenshot-1793700189592000.jpg what's wrong with this layout?"))
        XCTAssertTrue(SessionTitler.shouldInferTitle(fromFirstMessage: "/tmp/mclaude-screenshots/screenshot-1.jpg"))
        // Slash commands don't name a session
        XCTAssertFalse(SessionTitler.shouldInferTitle(fromFirstMessage: "/clear"))
        XCTAssertFalse(SessionTitler.shouldInferTitle(fromFirstMessage: "/model haiku"))
        XCTAssertFalse(SessionTitler.shouldInferTitle(fromFirstMessage: "   "))
    }

    func testSanitizeRejectsRefusalsAndSentences() {
        XCTAssertNil(SessionTitler.sanitize("This request isn't coding-related, so I can't create an appropriate title."))
        XCTAssertNil(SessionTitler.sanitize("I’m sorry, but I need more details"))
        XCTAssertNil(SessionTitler.sanitize("Could You Share More Context"))
        XCTAssertNil(SessionTitler.sanitize("Here is a short title that describes what you asked me about today"))
        XCTAssertEqual(SessionTitler.sanitize("Rinse Cycle Laundry Tips"), "Rinse Cycle Laundry Tips")
    }

    func testPromptIsNotCodingFramed() {
        let prompt = SessionTitler.titlePrompt(for: "what's a good rinse for curly hair?")
        XCTAssertFalse(prompt.contains("coding"))
        XCTAssertTrue(prompt.contains("can be about anything"))
    }

    func testNormalizedUserName() {
        XCTAssertEqual(SessionManager.normalizedName("  Taxes \n 2026   Plan "), "Taxes 2026 Plan")
        XCTAssertNil(SessionManager.normalizedName(" \n "))
        let long = String(repeating: "a", count: 80)
        XCTAssertEqual(SessionManager.normalizedName(long), long)   // user names aren't cut to 60
        XCTAssertEqual(SessionManager.normalizedName(String(repeating: "b", count: 600))?.count, 500)
    }
}
