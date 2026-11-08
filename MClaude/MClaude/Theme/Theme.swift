import SwiftUI
import UIKit

// Claude-style design tokens (docs/design-claude-uplift.md). Light = ivory, dark = warm charcoal.
// Every grey is warm; clay is used sparingly (send, primary button, small highlights).

extension Color {
    /// A colour that follows the system light/dark appearance.
    init(light: UInt32, dark: UInt32, lightAlpha: CGFloat = 1, darkAlpha: CGFloat = 1) {
        self.init(uiColor: UIColor { traits in
            traits.userInterfaceStyle == .dark
                ? UIColor(hex: dark, alpha: darkAlpha)
                : UIColor(hex: light, alpha: lightAlpha)
        })
    }
}

extension UIColor {
    convenience init(hex: UInt32, alpha: CGFloat = 1) {
        self.init(red: CGFloat((hex >> 16) & 0xFF) / 255,
                  green: CGFloat((hex >> 8) & 0xFF) / 255,
                  blue: CGFloat(hex & 0xFF) / 255,
                  alpha: alpha)
    }
}

enum Theme {
    // MARK: Surfaces
    /// Page background (bg-100).
    static let bgPage = Color(light: 0xFAF9F5, dark: 0x262624)
    /// Raised: cards, composer, sheets, grouped rows (bg-000).
    static let bgRaised = Color(light: 0xFFFFFF, dark: 0x30302E)
    /// Sunken: user bubble, chips, secondary buttons, code (bg-300).
    static let bgSunken = Color(light: 0xF0EEE6, dark: 0x1F1E1D)
    /// Strong: pressed state, divider blocks (bg-400).
    static let bgStrong = Color(light: 0xE8E6DC, dark: 0x141413)

    // MARK: Text
    static let textPrimary = Color(light: 0x141413, dark: 0xFAF9F5)
    static let textSecondary = Color(light: 0x3D3D3A, dark: 0xC2C0B6)
    static let textMuted = Color(light: 0x73726C, dark: 0x9C9A92)

    // MARK: Lines
    static let border = Color(light: 0x141413, dark: 0xDEDCD1, lightAlpha: 0.12, darkAlpha: 0.15)

    // MARK: Accents
    static let clay = Color(light: 0xD97757, dark: 0xD97757)
    static let clayPressed = Color(light: 0xC6613F, dark: 0xC6613F)
    static let link = Color(light: 0x2C84DB, dark: 0x5BA4E6)
    static let danger = Color(light: 0xB53333, dark: 0xDD5353)
    static let success = Color(light: 0x3F8F1F, dark: 0x65BB30)
    /// Muted warm yellow for warnings (e.g. permission needed).
    static let warning = Color(light: 0xA8741A, dark: 0xD9A441)

    // MARK: Diff tints (muted, not saturated)
    static let diffAddBg = Color(light: 0x3F8F1F, dark: 0x65BB30, lightAlpha: 0.10, darkAlpha: 0.14)
    static let diffAddText = Color(light: 0x2F6B17, dark: 0x8FCB6A)
    static let diffDelBg = Color(light: 0xB53333, dark: 0xDD5353, lightAlpha: 0.08, darkAlpha: 0.14)
    static let diffDelText = Color(light: 0x8F2A2A, dark: 0xE88A8A)

    // MARK: Radii
    static let radiusCard: CGFloat = 12
    static let radiusComposer: CGFloat = 16
    static let radiusBubble: CGFloat = 18
    static let radiusSmall: CGFloat = 8
}

// MARK: - Typography (Apple lookalikes only: New York serif, SF Pro, SF Mono)

extension Font {
    /// Claude's prose: New York (serif design) ~17pt. Pair with `.claudeProseSpacing()` for ~1.45 line height.
    static let claudeProse = Font.system(size: 17, design: .serif)
    static func claudeProseHeading(level: Int) -> Font {
        let size: CGFloat = level <= 1 ? 24 : level == 2 ? 21 : 19
        return .system(size: size, weight: .semibold, design: .serif)
    }
    /// UI text (SF Pro).
    static let claudeBody = Font.system(size: 16)
    static let claudeUI = Font.system(size: 15)
    static let claudeUISemibold = Font.system(size: 15, weight: .semibold)
    static let claudeCaption = Font.system(size: 13)
    static let claudeCaptionSemibold = Font.system(size: 13, weight: .semibold)
    static let claudeFootnote = Font.system(size: 12)
    /// Code / tool output (SF Mono).
    static func claudeMono(_ size: CGFloat = 12) -> Font { .system(size: size, design: .monospaced) }
}

extension View {
    /// ~1.45 line height for 17pt serif prose.
    func claudeProseSpacing() -> some View { lineSpacing(4.5) }

    /// Raised card: bg-000, 12pt radius, hairline border, soft shadow in light mode only.
    func claudeCard(radius: CGFloat = Theme.radiusCard, padding: CGFloat? = nil) -> some View {
        modifier(ClaudeCardModifier(radius: radius, padding: padding))
    }

    /// Hairline outline in the warm border colour.
    func claudeHairline(radius: CGFloat) -> some View {
        overlay(RoundedRectangle(cornerRadius: radius, style: .continuous)
            .strokeBorder(Theme.border, lineWidth: 0.5))
    }
}

private struct ClaudeCardModifier: ViewModifier {
    let radius: CGFloat
    let padding: CGFloat?
    @Environment(\.colorScheme) private var scheme

    func body(content: Content) -> some View {
        content
            .padding(padding ?? 0)
            .background(Theme.bgRaised, in: RoundedRectangle(cornerRadius: radius, style: .continuous))
            .claudeHairline(radius: radius)
            .shadow(color: .black.opacity(scheme == .dark ? 0 : 0.06), radius: 4, x: 0, y: 2)
    }
}

// MARK: - Buttons

/// Primary action: clay capsule, ivory text.
struct ClayCapsuleButtonStyle: ButtonStyle {
    var enabled = true
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.claudeUISemibold)
            .foregroundStyle(enabled ? Color.white : Theme.textMuted)
            .padding(.horizontal, 18)
            .padding(.vertical, 10)
            .background(enabled ? (configuration.isPressed ? Theme.clayPressed : Theme.clay) : Theme.bgSunken,
                        in: Capsule())
    }
}

/// Secondary action: sunken capsule, text-secondary.
struct SunkenCapsuleButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.claudeUI)
            .foregroundStyle(Theme.textSecondary)
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
            .background(configuration.isPressed ? Theme.bgStrong : Theme.bgSunken, in: Capsule())
    }
}

// MARK: - Session status (drawer dot / header)

extension SessionStatus {
    /// Claude palette: clay = working, blue = waiting on you, none = idle/unknown.
    var themeDot: Color? {
        switch self {
        case .working: Theme.clay
        case .waitingForInput, .needsPermission, .planMode: Theme.link
        case .idle, .unknown: nil
        }
    }
}

// MARK: - App mark (original; not Anthropic's logo)

/// The MClaude mark: a softened `>_` prompt in clay (two round-capped strokes).
/// Same geometry as the app icon (/tmp/mclaude-uitest-scripts/mkicon.swift).
struct MClaudeMark: View {
    var size: CGFloat = 28
    var color: Color = Theme.clay

    var body: some View {
        MClaudeMarkShape()
            .stroke(color, style: StrokeStyle(lineWidth: size * 0.115, lineCap: .round, lineJoin: .round))
            .frame(width: size, height: size)
            .accessibilityLabel("MClaude")
    }
}

/// Unit-square path: chevron, then the underscore.
struct MClaudeMarkShape: Shape {
    func path(in rect: CGRect) -> Path {
        func p(_ x: CGFloat, _ y: CGFloat) -> CGPoint {
            CGPoint(x: rect.minX + x * rect.width, y: rect.minY + y * rect.height)
        }
        var path = Path()
        path.move(to: p(0.14, 0.22))
        path.addLine(to: p(0.48, 0.48))
        path.addLine(to: p(0.14, 0.74))
        path.move(to: p(0.60, 0.78))
        path.addLine(to: p(0.90, 0.78))
        return path
    }
}
