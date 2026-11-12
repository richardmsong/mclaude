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
    static let maxPrompt = 1000

    /// What started the turn: its first main-thread user message. `from` is the peer session's
    /// name for a peer message, nil for the user's own.
    struct Prompt: Codable, Sendable, Equatable {
        let text: String
        var from: String?
        /// The sender of a peer message, so `from` can be named per viewer. Not on the wire.
        var peer: PeerRef? = nil

        enum CodingKeys: String, CodingKey { case text, from }

        /// `text` is cut to 1,000 characters (its first 999 and "…").
        init(text: String, from: String? = nil, peer: PeerRef? = nil) {
            self.text = text.count > LiveTurn.maxPrompt ? String(text.prefix(LiveTurn.maxPrompt - 1)) + "…" : text
            self.from = from
            self.peer = peer
        }

        func encode(to encoder: Encoder) throws {
            var c = encoder.container(keyedBy: CodingKeys.self)
            try c.encode(text, forKey: .text)
            try c.encode(from, forKey: .from)               // null for the user's own message
        }

        /// `from` as `viewer` may see it: the peer's session name when it can see that session.
        func named(by directory: PeerDirectory, canSee: (String) async -> Bool) async -> Prompt {
            guard let peer else { return self }
            let sid = peer.claudeSessionId.flatMap { directory.open[$0]?.id }
            var ok = false
            if let sid { ok = await canSee(sid) }
            var copy = self
            copy.from = directory.refresh(peer, visible: { _ in ok }).displayName
            return copy
        }
    }

    /// The prompt a top-level user event gives the turn, or nil when it isn't a real message:
    /// tool results, subagent lines, replayed task notifications and the server's own
    /// "The previous turn was interrupted…" nudge (meta lines and command echoes never become
    /// user events). A peer-only line is the peer's message; otherwise what the user typed.
    static func prompt(of e: SessionEvent) -> Prompt? {
        guard e.type == .user, e.subagentInfo == nil, let text = e.text,
              !text.hasPrefix("The previous turn was interrupted by"),
              !JSONLParser.isOnlyTaskNotifications(text) else { return nil }
        if let sender = SessionManager.peerOnlySender(e), let first = e.peerMessages?.first {
            let body = first.text.trimmingCharacters(in: .whitespacesAndNewlines)
            return Prompt(text: body, from: sender, peer: first.peer)
        }
        if e.peerMessages != nil, let other = e.peerOtherText {
            return Prompt(text: SessionTitler.typedText(of: other))
        }
        return Prompt(text: SessionTitler.typedText(of: text))
    }

    private(set) var turn = 0
    private(set) var active = false
    private(set) var blocks: [Block] = []
    private var nextIndex = 0
    /// Claude's content-block index (resets per assistant message) -> our index in the turn
    private var claudeIndex: [Int: Int] = [:]
    private var pending: [Pending] = []
    private var endedPending = false
    private(set) var prompt: Prompt?
    /// A prompt that arrived while no turn was running: the next turn's
    private var nextPrompt: Prompt?
    /// `prompt` is known and no frame has carried it yet
    private var promptPending = false

    /// A frame is waiting to be sent
    var hasPending: Bool { !pending.isEmpty || endedPending || promptPending }

    /// A new turn: replaces the previous turn's blocks (and takes a prompt that came just before).
    mutating func begin() {
        turn += 1
        active = true
        blocks = []
        nextIndex = 0
        claudeIndex = [:]
        pending = []
        endedPending = false
        prompt = nextPrompt
        nextPrompt = nil
        promptPending = prompt != nil
    }

    /// A main-thread user message: the running turn's prompt while it has none, or (no turn
    /// running) the next turn's. True when a frame has news.
    @discardableResult
    mutating func notePrompt(_ p: Prompt) -> Bool {
        guard active else {
            if nextPrompt == nil { nextPrompt = p }
            return false
        }
        guard prompt == nil else { return false }
        prompt = p
        promptPending = true
        return true
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
        /// Only in the first frame after the prompt is known (omitted otherwise)
        var prompt: Prompt? = nil
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
        var prompt: Prompt? = nil

        enum CodingKeys: String, CodingKey { case turn, active, prompt, blocks }

        func encode(to encoder: Encoder) throws {
            var c = encoder.container(keyedBy: CodingKeys.self)
            try c.encode(turn, forKey: .turn)
            try c.encode(active, forKey: .active)
            try c.encode(prompt, forKey: .prompt)           // null until known
            try c.encode(blocks, forKey: .blocks)
        }
    }

    /// Everything since the last frame, then cleared. Nil when there's nothing to send.
    mutating func takeFrame(id: String) -> Frame? {
        guard hasPending else { return nil }
        let frame = Frame(id: id, turn: turn, ended: endedPending, blocks: pending.map {
            Frame.Block(index: $0.index, kind: $0.kind, append: $0.append, isNew: $0.isNew,
                        startedAt: $0.isNew ? $0.startedAt : nil)
        }, prompt: promptPending ? prompt : nil)
        pending = []
        endedPending = false
        promptPending = false
        return frame
    }

    /// The turn as it stands: last 30 blocks, each with at most its last 4,000 characters
    /// (a cut block starts with "…", counted in the 4,000).
    var snapshot: Snapshot {
        Snapshot(turn: turn, active: active, blocks: blocks.map { b in
            let text = (b.cut || b.count > Self.maxText) ? "…" + String(b.text.suffix(Self.maxText - 1)) : b.text
            return Snapshot.Block(index: b.index, kind: b.kind, text: text, startedAt: b.startedAt, done: b.done)
        }, prompt: prompt)
    }
}
