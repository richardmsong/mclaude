import Foundation

actor SignalNotifier {
    private let signalCliPath: String
    private let recipientPhone: String?
    private var enabled: Bool

    init(
        signalCliPath: String = "/opt/homebrew/bin/signal-cli",
        recipientPhone: String? = nil
    ) {
        self.signalCliPath = signalCliPath
        self.recipientPhone = recipientPhone
        self.enabled = recipientPhone != nil

        if !enabled {
            print("[signal] No recipient phone configured — notifications disabled")
            print("[signal] Set MCLAUDE_SIGNAL_RECIPIENT to enable")
        }
    }

    func notify(session: ClaudeSession, from oldStatus: SessionStatus, to newStatus: SessionStatus) async {
        guard enabled, let phone = recipientPhone else { return }

        let emoji: String
        switch newStatus {
        case .waitingForInput: emoji = "⏳"
        case .planMode: emoji = "📋"
        case .idle: emoji = "✅"
        default: return // Don't notify for working/unknown
        }

        let message = "\(emoji) [\(session.projectName)] \(newStatus.rawValue)\nWindow \(session.tmuxWindow) | \(session.cwd)"

        let process = Process()
        process.executableURL = URL(fileURLWithPath: signalCliPath)
        process.arguments = ["send", "-m", message, phone]
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice

        do {
            try process.run()
            process.waitUntilExit()
            if process.terminationStatus == 0 {
                print("[signal] Sent: \(message.prefix(60))...")
            } else {
                print("[signal] Failed to send (exit \(process.terminationStatus))")
            }
        } catch {
            print("[signal] Error: \(error)")
        }
    }
}
