import Foundation

/// A turn-duration row ("Turn: 2.3s") is only meaningful after a turn that produced something
/// visible. Turns with no output (e.g. the CLI consuming a background-task notification in a
/// few ms) would otherwise stack timings back to back. Returns the uuids of duration events to hide.
enum TurnDurationFilter {
    static func hiddenDurationEvents(in events: [SessionEvent]) -> Set<String> {
        var hidden: Set<String> = []
        var sawContentSinceLastTurn = false
        for event in events {
            switch event.type {
            case .system:
                guard event.durationMs != nil else { continue }
                if !sawContentSinceLastTurn { hidden.insert(event.uuid) }
                sawContentSinceLastTurn = false
            case .user, .text, .thinking, .toolUse, .toolResult, .compaction:
                sawContentSinceLastTurn = true
            }
        }
        return hidden
    }
}
