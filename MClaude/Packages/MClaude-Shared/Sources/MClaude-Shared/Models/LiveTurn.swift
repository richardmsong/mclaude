import Foundation

/// The running turn's thinking, text and tool blocks as the server streams them
/// (docs/plan-watch-app.md → Live stream, Live view).
public struct LiveTurn: Codable, Sendable, Equatable {
    public struct Block: Codable, Sendable, Equatable, Identifiable {
        public let index: Int
        /// "thinking", "text" or "tool".
        public var kind: String
        public var text: String
        public var startedAt: Date?
        public var done: Bool
        public var id: Int { index }

        public init(index: Int, kind: String, text: String, startedAt: Date?, done: Bool) {
            self.index = index
            self.kind = kind
            self.text = text
            self.startedAt = startedAt
            self.done = done
        }
    }

    /// The message that started the turn (docs/plan-watch-app.md → Live stream).
    public struct Prompt: Codable, Sendable, Equatable {
        public let text: String
        /// nil for your own message; the other session's name for a peer message.
        public let from: String?
        public init(text: String, from: String?) { self.text = text; self.from = from }
    }

    public var turn: Int
    public var active: Bool
    public var prompt: Prompt?
    public var blocks: [Block]

    public init(turn: Int, active: Bool, prompt: Prompt? = nil, blocks: [Block]) {
        self.turn = turn
        self.active = active
        self.prompt = prompt
        self.blocks = blocks
    }

    /// Apply one pushed frame. Returns false when the frame doesn't continue what we have
    /// (another turn we didn't see start, or a gap in block indexes): re-request the live turn.
    public mutating func apply(_ frame: LiveFrame) -> Bool {
        if frame.turn < turn { return true }                       // late frame from an older turn
        if frame.turn > turn {
            // A new turn: only usable if this frame starts it (its first block is block 0).
            guard let first = frame.blocks.first, first.index == 0, first.new == true else {
                if frame.blocks.isEmpty {
                    turn = frame.turn; active = !(frame.ended ?? false); blocks = []; prompt = frame.prompt
                    return true
                }
                return false
            }
            turn = frame.turn
            active = true
            blocks = []
            prompt = nil
        }
        if let p = frame.prompt { prompt = p }
        for fb in frame.blocks {
            if let i = blocks.firstIndex(where: { $0.index == fb.index }) {
                blocks[i].text += fb.append
            } else if fb.index == (blocks.last?.index ?? -1) + 1 {
                if !blocks.isEmpty { blocks[blocks.count - 1].done = true }
                blocks.append(Block(index: fb.index, kind: fb.kind, text: fb.append,
                                    startedAt: fb.startedAt, done: false))
            } else {
                return false
            }
        }
        if frame.ended == true {
            active = false
            for i in blocks.indices { blocks[i].done = true }
        }
        return true
    }

    private static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { decoder in
            let s = try decoder.singleValueContainer().decode(String.self)
            let f = ISO8601DateFormatter()
            f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let d = f.date(from: s) { return d }
            f.formatOptions = [.withInternetDateTime]
            if let d = f.date(from: s) { return d }
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "bad date \(s)"))
        }
        return d
    }()

    public static func decode(_ data: Data) -> LiveTurn? { try? decoder.decode(LiveTurn.self, from: data) }
    public static func decodeFrame(_ data: Data) -> LiveFrame? { try? decoder.decode(LiveFrame.self, from: data) }
    public static func decodeTurns(_ data: Data) -> [PastTurn]? { try? decoder.decode([PastTurn].self, from: data) }

    /// The feed as the watch shows it: thinking and text blocks, and each run of consecutive
    /// tool blocks folded into one item.
    public var feed: [FeedItem] {
        var items: [FeedItem] = []
        var i = 0
        while i < blocks.count {
            let b = blocks[i]
            if b.kind == "tool" {
                var j = i
                while j + 1 < blocks.count, blocks[j + 1].kind == "tool" { j += 1 }
                let next = j + 1 < blocks.count ? blocks[j + 1] : nil
                let running = next == nil && active
                items.append(.tools(id: b.index, start: b.startedAt, end: next?.startedAt, running: running))
                i = j + 1
            } else {
                let growing = !b.done && active && i == blocks.count - 1
                items.append(b.kind == "thinking" ? .thinking(id: b.index, text: b.text, growing: growing)
                                                  : .text(id: b.index, text: b.text, growing: growing))
                i += 1
            }
        }
        return items
    }

    public enum FeedItem: Equatable, Identifiable, Sendable {
        case thinking(id: Int, text: String, growing: Bool)
        case text(id: Int, text: String, growing: Bool)
        case tools(id: Int, start: Date?, end: Date?, running: Bool)

        public var id: Int {
            switch self {
            case .thinking(let id, _, _), .text(let id, _, _), .tools(let id, _, _, _): id
            }
        }
    }
}

/// A WebSocket `live` frame's data: what's new in the turn since the last frame.
public struct LiveFrame: Codable, Sendable {
    public struct Block: Codable, Sendable {
        public let index: Int
        public let kind: String
        public let append: String
        public let new: Bool?
        public let startedAt: Date?
    }

    public let id: String
    public let turn: Int
    public let ended: Bool?
    /// Present once, in the first frame after the turn's starting message is known.
    public let prompt: LiveTurn.Prompt?
    public let blocks: [Block]
}

/// An earlier turn for the Past turns screen (docs/plan-watch-app.md → Relay → Past turns).
public struct PastTurn: Codable, Sendable, Equatable, Identifiable {
    public let prompt: String
    public let from: String?
    public let reply: String?
    public let at: Date?
    public var id: String { "\(at?.timeIntervalSince1970 ?? 0)-\(prompt.prefix(20))" }

    public init(prompt: String, from: String?, reply: String?, at: Date?) {
        self.prompt = prompt; self.from = from; self.reply = reply; self.at = at
    }

    /// Group history (oldest first) into turns: each starts at a main-thread user message (yours
    /// or another session's); its reply is Claude's last text before the next turn. Newest first,
    /// up to `limit`, leaving out the newest turn (it's on the session screen).
    public static func group(_ events: [SessionEvent], limit: Int = 15) -> [PastTurn] {
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        func date(_ s: String) -> Date? { iso.date(from: s) ?? ISO8601DateFormatter().date(from: s) }

        var turns: [(prompt: String, from: String?, reply: String?, at: Date?)] = []
        for e in events where e.subagentInfo == nil {
            switch e.type {
            case .user:
                if let peers = e.peerMessages, let first = peers.first(where: { !$0.isSent }) {
                    let own = (e.peerOtherText ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                    if own.isEmpty {
                        turns.append((first.body, first.peer?.name ?? "Another session", nil, date(e.timestamp)))
                        continue
                    }
                }
                let text = (e.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                guard !text.isEmpty, !text.hasPrefix("<"), !text.hasPrefix("The previous turn was interrupted"),
                      !text.hasPrefix("Caveat:") else { continue }
                turns.append((text, nil, nil, date(e.timestamp)))
            case .text:
                guard !turns.isEmpty, let t = e.text, !t.allSatisfy(\.isWhitespace) else { continue }
                turns[turns.count - 1].reply = t
            default:
                continue
            }
        }
        return turns.dropLast().reversed().prefix(limit).map {
            PastTurn(prompt: cap($0.prompt, 300), from: $0.from, reply: $0.reply.map { cap($0, 3000) }, at: $0.at)
        }
    }

    static func cap(_ s: String, _ limit: Int) -> String {
        s.count > limit ? String(s.prefix(limit)) + "…" : s
    }
}

public enum LiveFormat {
    /// "41s", "2m 5s", "1h 3m".
    public static func duration(_ seconds: TimeInterval) -> String {
        let s = Int(max(0, seconds))
        if s < 60 { return "\(s)s" }
        if s < 3600 { return s % 60 == 0 ? "\(s / 60)m" : "\(s / 60)m \(s % 60)s" }
        return "\(s / 3600)h \((s % 3600) / 60)m"
    }

    /// Long thinking shows only its end: the last `limit` characters, starting with "…".
    public static func tail(_ text: String, limit: Int = 1500) -> String {
        guard text.count > limit else { return text }
        let cut = text.suffix(limit)
        let start = cut.firstIndex(of: " ").map { cut.index(after: $0) } ?? cut.startIndex
        return "…" + cut[start...]
    }
}
