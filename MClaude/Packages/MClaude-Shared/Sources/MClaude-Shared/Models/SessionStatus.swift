import SwiftUI

public enum SessionStatus: String, Codable, Sendable, CaseIterable {
    case working
    case waitingForInput = "waiting_for_input"
    case needsPermission = "needs_permission"
    case planMode = "plan_mode"
    case idle
    case unknown
}

extension SessionStatus {
    public var color: Color {
        switch self {
        case .working: return .blue
        case .waitingForInput: return .green
        case .needsPermission: return .orange
        case .planMode: return .purple
        case .idle: return .gray
        case .unknown: return .secondary
        }
    }

    public var label: String {
        switch self {
        case .working: return "Working"
        case .waitingForInput: return "Ready"
        case .needsPermission: return "Needs Approval"
        case .planMode: return "Plan Ready"
        case .idle: return "Idle"
        case .unknown: return "Unknown"
        }
    }

    public var icon: String {
        switch self {
        case .working: return "circle.dotted"
        case .waitingForInput: return "text.cursor"
        case .needsPermission: return "hand.raised.fill"
        case .planMode: return "list.clipboard.fill"
        case .idle: return "moon.fill"
        case .unknown: return "questionmark.circle"
        }
    }

    public var needsAttention: Bool {
        switch self {
        case .needsPermission, .planMode: return true
        default: return false
        }
    }
}
