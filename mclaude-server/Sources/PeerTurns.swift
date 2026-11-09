import Foundation

/// Which turns are quiet peer turns (docs/design-claude-uplift.md → Session order, read state and
/// background work → Peer turns). A turn that a peer-only message starts is quiet unless this
/// session's last message to that peer was sent from a turn that wasn't quiet: a reply to
/// something the user asked for belongs to the user.
struct PeerTurnState: Sendable, Equatable {
    /// Peer identity key -> whether this session's last message to it came from a non-quiet turn
    private(set) var lastSendWasYours: [String: Bool] = [:]
    /// History pass only: the previous top-level event was a tool result (a user line right after
    /// one was taken mid-turn; any other user line starts a turn)
    private var afterToolResult = false
    /// History pass only: the open turn is quiet
    private var quietTurn = false

    /// Claude session id, else socket address, else Claude Code name.
    static func keys(_ p: PeerRef) -> [String] {
        [p.claudeSessionId.map { "u:\($0)" }, p.address.map { "a:\($0)" }, p.ccName.map { "n:\($0)" }].compactMap { $0 }
    }

    /// A peer-only user line that starts a turn: quiet unless the last send to that peer was the user's.
    func isQuietStart(_ e: SessionEvent) -> Bool {
        guard SessionManager.peerOnlySender(e) != nil, let peer = e.peerMessages?.first?.peer else { return false }
        for k in Self.keys(peer) {
            if let yours = lastSendWasYours[k] { return !yours }
        }
        return true
    }

    /// A peer send (tool_use with a sent peer message), from a quiet turn or not.
    mutating func noteSend(_ e: SessionEvent, fromQuietTurn: Bool) {
        guard e.type == .toolUse else { return }
        for m in e.peerMessages ?? [] where m.direction == "sent" {
            for k in Self.keys(m.peer) { lastSendWasYours[k] = !fromQuietTurn }
        }
    }

    /// History: walk events in order and mark the user lines that start quiet turns. Transcripts
    /// have no turn ends, so a top-level user line right after a tool result was delivered mid-turn
    /// (Claude reads input only between a tool result and its next step); any other one starts a turn.
    mutating func mark(_ events: [SessionEvent]) -> [SessionEvent] {
        events.map { event in
            var e = event
            let topLevel = e.subagentInfo == nil
            defer { if topLevel, e.type != .system, e.type != .compaction { afterToolResult = e.type == .toolResult } }
            switch e.type {
            case .user where topLevel:
                if !afterToolResult {
                    quietTurn = isQuietStart(e)
                    e.quiet = quietTurn ? true : nil
                } else if quietTurn, SessionManager.peerOnlySender(e) == nil,
                          !JSONLParser.isOnlyTaskNotifications(e.text ?? "") {
                    quietTurn = false                  // something else reached Claude mid-turn
                }
            case .toolUse:
                noteSend(e, fromQuietTurn: quietTurn)
            case .system where e.durationMs != nil:
                quietTurn = false
            default:
                break
            }
            return e
        }
    }
}
