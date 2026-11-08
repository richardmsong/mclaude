import SwiftUI

/// The Ops session: the observability agent (docs/plan-observability.md → Ops session). It's
/// recognized by its name and shown apart from regular sessions (docs/design-claude-uplift.md → Drawer).
extension ClaudeSession {
    var isOps: Bool { name?.trimmingCharacters(in: .whitespaces).caseInsensitiveCompare("Ops") == .orderedSame }
}

/// The Ops mark: a stethoscope in the Ops tint.
struct OpsMark: View {
    var size: CGFloat = 13
    var body: some View {
        Image(systemName: "stethoscope")
            .font(.system(size: size, weight: .semibold))
            .foregroundStyle(Theme.ops)
            .accessibilityHidden(true)
    }
}
