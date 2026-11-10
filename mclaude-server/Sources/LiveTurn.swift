import Foundation

/// The current turn's main-thread thinking, text and tool calls, built from Claude Code's
/// partial-message stream (docs/plan-watch-app.md → Server → Live stream). Pure state: the
/// SessionManager feeds it stdout lines and decides when to take a frame.
struct LiveTurn: Sendable {
    enum Kind: String, Codable, Sendable { case thinking, text, tool }

    struct Block: Sendable {
        let index: Int
        let kind: Kind
        var text = ""
        var count = 0                     // characters in `text` (String.count is O(n))
        var cut = false                   // older text was dropped
        let startedAt: Date
        var done = false
    }

    /// Everything appended since the last frame
    private struct Pending: Sendable {
        let index: Int
        let kind: Kind
        var append = ""
        let isNew: Bool
        let startedAt: Date
    }

    static let maxBlocks = 30
    static let maxText = 4000

    private(set) var turn = 0
    private(set) var active = false
    private(set) var blocks: [Block] = []
    private var nextIndex = 0
    /// Claude's content-block index (resets per assistant message) -> our index in the turn
    private var claudeIndex: [Int: Int] = [:]
    private var pending: [Pending] = []
    private var endedPending = false

    /// A frame is waiting to be sent
    var hasPending: Bool { !pending.isEmpty || endedPending }

    /// A new turn: replaces the previous turn's blocks.
    mutating func begin() {
        turn += 1
        active = true
        blocks = []
        nextIndex = 0
        claudeIndex = [:]
        pending = []
        endedPending = false
    }

    /// The turn ended: its last block is done. False when no turn was running.
    @discardableResult
    mutating func end() -> Bool {
        guard active else { return false }
        active = false
        if !blocks.isEmpty { blocks[blocks.count - 1].done = true }
        endedPending = true
        return true
    }

    /// One `stream_event` line. Subagent lines (non-null parent_tool_use_id) are left out.
    /// True when a frame has news.
    @discardableResult
    mutating func ingest(_ json: [String: Any], at date: Date = Date()) -> Bool {
        guard json["parent_tool_use_id"] as? String == nil,
              let event = json["event"] as? [String: Any], let type = event["type"] as? String else { return false }
        switch type {
        case "message_start":
            claudeIndex = [:]
            return false
        case "content_block_start":
            guard let block = event["content_block"] as? [String: Any] else { return false }
            if !active { begin() }
            let kind: Kind
            var initial = ""
            switch block["type"] as? String {
            case "thinking": kind = .thinking; initial = block["thinking"] as? String ?? ""
            case "redacted_thinking": kind = .thinking
            case "text": kind = .text; initial = block["text"] as? String ?? ""
            default: kind = .tool
            }
            if !blocks.isEmpty { blocks[blocks.count - 1].done = true }
            let index = nextIndex
            nextIndex += 1
            if let ci = event["index"] as? Int { claudeIndex[ci] = index }
            blocks.append(Block(index: index, kind: kind, startedAt: date))
            if blocks.count > Self.maxBlocks { blocks.removeFirst(blocks.count - Self.maxBlocks) }
            pending.append(Pending(index: index, kind: kind, isNew: true, startedAt: date))
            if !initial.isEmpty { appendText(initial, to: index) }
            return true
        case "content_block_delta":
            guard active, let delta = event["delta"] as? [String: Any] else { return false }
            let text: String
            switch delta["type"] as? String {
            case "thinking_delta": text = delta["thinking"] as? String ?? ""
            case "text_delta": text = delta["text"] as? String ?? ""
            default: return false                            // signature_delta, input_json_delta
            }
            guard !text.isEmpty else { return false }
            let index = (event["index"] as? Int).flatMap { claudeIndex[$0] } ?? (nextIndex - 1)
            return appendText(text, to: index)
        default:
            return false                                     // content_block_stop, message_delta, message_stop
        }
    }

    @discardableResult
    private mutating func appendText(_ text: String, to index: Int) -> Bool {
        guard let b = blocks.lastIndex(where: { $0.index == index }), blocks[b].kind != .tool else { return false }
        blocks[b].text += text
        blocks[b].count += text.count
        if blocks[b].count > 2 * Self.maxText {              // amortized trim; the snapshot cuts exactly
            blocks[b].text = String(blocks[b].text.suffix(Self.maxText))
            blocks[b].count = Self.maxText
            blocks[b].cut = true
        }
        if let p = pending.lastIndex(where: { $0.index == index }) {
            pending[p].append += text
        } else {
            pending.append(Pending(index: index, kind: blocks[b].kind, isNew: false, startedAt: blocks[b].startedAt))
            pending[pending.count - 1].append = text
        }
        return true
    }

    // MARK: - Wire shapes

    struct Frame: Codable, Sendable, Equatable {
        struct Block: Codable, Sendable, Equatable {
            let index: Int
            let kind: Kind
            let append: String
            let isNew: Bool
            let startedAt: Date?          // only when new
            enum CodingKeys: String, CodingKey { case index, kind, append, isNew = "new", startedAt }
        }
        let id: String
        let turn: Int
        let ended: Bool
        let blocks: [Block]
    }

    struct Snapshot: Codable, Sendable, Equatable {
        struct Block: Codable, Sendable, Equatable {
            let index: Int
            let kind: Kind
            let text: String
            let startedAt: Date
            let done: Bool
        }
        let turn: Int
        let active: Bool
        let blocks: [Block]
    }

    /// Everything since the last frame, then cleared. Nil when there's nothing to send.
    mutating func takeFrame(id: String) -> Frame? {
        guard hasPending else { return nil }
        let frame = Frame(id: id, turn: turn, ended: endedPending, blocks: pending.map {
            Frame.Block(index: $0.index, kind: $0.kind, append: $0.append, isNew: $0.isNew,
                        startedAt: $0.isNew ? $0.startedAt : nil)
        })
        pending = []
        endedPending = false
        return frame
    }

    /// The turn as it stands: last 30 blocks, each with at most its last 4,000 characters
    /// (a cut block starts with "…", counted in the 4,000).
    var snapshot: Snapshot {
        Snapshot(turn: turn, active: active, blocks: blocks.map { b in
            let text = (b.cut || b.count > Self.maxText) ? "…" + String(b.text.suffix(Self.maxText - 1)) : b.text
            return Snapshot.Block(index: b.index, kind: b.kind, text: text, startedAt: b.startedAt, done: b.done)
        })
    }
}
