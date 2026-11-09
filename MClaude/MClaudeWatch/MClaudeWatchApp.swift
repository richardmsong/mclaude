import SwiftUI

@main
struct MClaudeWatchApp: App {
    @State private var store = WatchStore()

    var body: some Scene {
        WindowGroup {
            SessionListView()
                .environment(store)
                .tint(WatchTheme.clay)
        }
    }
}

/// Watch palette: black background with the Claude dark-theme accents (docs/plan-watch-app.md → Look).
enum WatchTheme {
    static let textPrimary = Color(hex: 0xFAF9F5)
    static let textSecondary = Color(hex: 0xC2C0B6)
    static let textMuted = Color(hex: 0x9C9A92)
    static let raised = Color(hex: 0x30302E)
    static let clay = Color(hex: 0xD97757)
    static let link = Color(hex: 0x5BA4E6)

    static let danger = Color(hex: 0xDD5353)

    static let success = Color(hex: 0x65BB30)
}

extension Color {
    init(hex: UInt32) {
        self.init(red: Double((hex >> 16) & 0xFF) / 255, green: Double((hex >> 8) & 0xFF) / 255,
                  blue: Double(hex & 0xFF) / 255)
    }
}
