import SwiftUI

// Shared pieces of the MCP screens (docs/plan-mcp-servers.md → iOS app).

enum McpText {
    static let unavailable = "MCP management isn't available for this session."
    static let unreachable = "Couldn't reach the server"

    /// A 404 from an MCP endpoint: an older server or a k8s session (unless the server named a
    /// more specific reason, e.g. an unknown server name).
    static func isUnavailable(_ error: Error) -> Bool {
        guard let e = error as? APIServerError, e.status == 404 else { return false }
        return e.message == nil || e.message == "not found"
    }

    static func status(of error: Error) -> Int? { (error as? APIServerError)?.status }

    static func isCancellation(_ error: Error) -> Bool {
        error is CancellationError || (error as? URLError)?.code == .cancelled
    }

    /// The server's `error` text, or "Couldn't reach the server" on a network failure.
    static func message(for error: Error) -> String {
        if isUnavailable(error) { return unavailable }
        if let e = error as? APIServerError { return e.message ?? "The server answered \(e.status)." }
        if error is URLError { return unreachable }
        if error is DecodingError { return "The server sent an answer the app doesn't understand." }
        return error.localizedDescription
    }

    /// Short status word: per-session row detail (non-connected) and global session rows.
    static func statusWord(_ status: String) -> String {
        switch status {
        case "connected": "connected"
        case "pending": "connecting"
        case "failed": "failed"
        case "needs-auth": "needs sign-in"
        case "disabled": "disabled"
        default: status
        }
    }

    /// Spoken form of a status for VoiceOver.
    static func statusLabel(_ status: String) -> String {
        "Status: \(statusWord(status))"
    }

    static func dotColor(for status: String) -> Color? {
        switch status {
        case "connected": Theme.success
        case "pending": Theme.clay
        case "failed": Theme.danger
        case "needs-auth": Theme.link
        default: nil
        }
    }

    static func tools(_ n: Int) -> String { n == 1 ? "1 tool" : "\(n) tools" }

    static func sessions(_ n: Int) -> String { n == 1 ? "1 session" : "\(n) sessions" }

    static let namePattern = #"^[A-Za-z0-9_.-]{1,64}$"#

    static func isValidName(_ name: String) -> Bool {
        name.range(of: namePattern, options: .regularExpression) != nil
    }

    static func isValidURL(_ text: String) -> Bool {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let c = URLComponents(string: t), let scheme = c.scheme?.lowercased(),
              scheme == "http" || scheme == "https", let host = c.host, !host.isEmpty else { return false }
        return true
    }
}

/// Status dot: a coloured circle, a small spinner for `pending`, or nothing (an empty slot) when there's no colour.
struct McpStatusDot: View {
    let color: Color?
    var pending = false
    var label: String

    var body: some View {
        Group {
            if pending {
                ProgressView().controlSize(.mini).tint(Theme.clay)
            } else if let color {
                Circle().fill(color).frame(width: 8, height: 8)
            } else {
                Color.clear   // no dot (disabled / no reports); keeps names aligned
            }
        }
        .frame(width: 14, height: 14)
        .accessibilityElement()
        .accessibilityLabel(label)
    }
}

/// Small link-blue capsule (Sign in).
struct LinkCapsuleButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.claudeCaptionSemibold)
            .foregroundStyle(Color.white)
            .padding(.horizontal, 12)
            .padding(.vertical, 6)
            .background(Theme.link.opacity(configuration.isPressed ? 0.8 : 1), in: Capsule())
    }
}

/// Full-width secondary capsule label with an optional spinner.
struct McpWideButtonLabel: View {
    let title: String
    var systemImage: String? = nil
    var busy = false

    var body: some View {
        HStack(spacing: 8) {
            if busy {
                ProgressView().controlSize(.small).tint(Theme.textSecondary)
            } else if let systemImage {
                Image(systemName: systemImage)
            }
            Text(title)
        }
        .frame(maxWidth: .infinity)
    }
}

/// Bottom toast (raised card), hidden after 4 s.
struct McpToast: ViewModifier {
    @Binding var message: String?

    func body(content: Content) -> some View {
        content.overlay(alignment: .bottom) {
            if let message {
                Text(message)
                    .font(.claudeUI)
                    .foregroundStyle(Theme.textPrimary)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 11)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(
                        RoundedRectangle(cornerRadius: Theme.radiusCard, style: .continuous)
                            .fill(Theme.bgRaised)
                            .shadow(color: .black.opacity(0.08), radius: 8, y: 2)
                    )
                    .claudeHairline(radius: Theme.radiusCard)
                    .padding(.horizontal, 12)
                    .padding(.bottom, 10)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
                    .task(id: message) {
                        try? await Task.sleep(for: .seconds(4))
                        guard !Task.isCancelled, self.message == message else { return }
                        withAnimation(.snappy) { self.message = nil }
                    }
            }
        }
    }
}

extension View {
    func mcpToast(_ message: Binding<String?>) -> some View { modifier(McpToast(message: message)) }
}

/// Muted message with an optional Retry, for the sheet / list error states.
struct McpStateMessage: View {
    let text: String
    var retry: (() -> Void)? = nil

    var body: some View {
        VStack(spacing: 12) {
            Text(text)
                .font(.claudeUI)
                .foregroundStyle(Theme.textMuted)
                .multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)
            if let retry {
                Button("Retry", action: retry)
                    .buttonStyle(SunkenCapsuleButtonStyle())
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 32)
        .padding(.horizontal, 16)
    }
}
