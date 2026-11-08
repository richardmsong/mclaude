import Foundation

// Cross-session peer messages (docs/plan-peer-messages.md → Shapes). The server parses Claude Code's
// `SendMessage` / `<cross-session-message>` formats and attaches these to events; the app never
// parses the raw tags. Every decoder here is lenient (missing or mistyped fields become nil) so a
// shape change on the server can't make the surrounding event fail to decode.

/// The other side of a peer message.
public struct PeerRef: Codable, Sendable, Hashable {
    /// Claude session UUID; nil when the peer couldn't be resolved.
    public let claudeSessionId: String?
    /// Claude Code's own name for the session (e.g. `mclaude-v1-f1`).
    public let ccName: String?
    /// `uds:/tmp/cc-socks/<pid>.sock`.
    public let address: String?
    /// MClaude session id; only for an open managed session the caller can see.
    public let sessionId: String?
    public let displayName: String?
    public let open: Bool?

    public init(claudeSessionId: String? = nil, ccName: String? = nil, address: String? = nil,
                sessionId: String? = nil, displayName: String? = nil, open: Bool? = nil) {
        self.claudeSessionId = claudeSessionId; self.ccName = ccName; self.address = address
        self.sessionId = sessionId; self.displayName = displayName; self.open = open
    }

    public init(from decoder: Decoder) throws {
        let c = try? decoder.container(keyedBy: CodingKeys.self)
        claudeSessionId = Self.nonEmpty(try? c?.decodeIfPresent(String.self, forKey: .claudeSessionId))
        ccName = Self.nonEmpty(try? c?.decodeIfPresent(String.self, forKey: .ccName))
        address = Self.nonEmpty(try? c?.decodeIfPresent(String.self, forKey: .address))
        sessionId = Self.nonEmpty(try? c?.decodeIfPresent(String.self, forKey: .sessionId))
        displayName = Self.nonEmpty(try? c?.decodeIfPresent(String.self, forKey: .displayName))
        open = try? c?.decodeIfPresent(Bool.self, forKey: .open)
    }

    static func nonEmpty(_ s: String??) -> String? {
        guard let s = s ?? nil, !s.isEmpty else { return nil }
        return s
    }

    /// The server's display name, else the Claude Code name, else "Another session".
    public var name: String { displayName ?? ccName ?? "Another session" }

    /// The peer was resolved, so a thread exists.
    public var isResolved: Bool { claudeSessionId != nil }

    /// The MClaude session to switch to ("Open <peer>"), when it's open and visible.
    public var openSessionId: String? {
        guard let sessionId, open ?? true else { return nil }
        return sessionId
    }
}

public enum PeerDirection: String, Sendable {
    case sent, received
}

public enum PeerStatus: String, Sendable {
    case sending, delivered, failed, received
}

/// One message between two sessions: an entry in `SessionEvent.peerMessages`, or in a thread.
public struct PeerMessage: Codable, Sendable, Identifiable, Hashable {
    /// The dedup key (`sent:<tool_use id>` / `recv:…`).
    public let id: String
    /// "sent" or "received" (see `isSent`).
    public let direction: String?
    public let peer: PeerRef?
    public let summary: String?
    public let text: String?
    /// ISO 8601 (with or without fractional seconds); see `date`.
    public let at: String?
    /// "sending", "delivered", "failed" or "received" (see `peerStatus`).
    public let status: String?
    public let error: String?

    public init(id: String, direction: String?, peer: PeerRef?, summary: String? = nil, text: String?,
                at: String? = nil, status: String? = nil, error: String? = nil) {
        self.id = id; self.direction = direction; self.peer = peer; self.summary = summary
        self.text = text; self.at = at; self.status = status; self.error = error
    }

    public init(from decoder: Decoder) throws {
        let c = try? decoder.container(keyedBy: CodingKeys.self)
        direction = try? c?.decodeIfPresent(String.self, forKey: .direction)
        peer = try? c?.decodeIfPresent(PeerRef.self, forKey: .peer)
        summary = PeerRef.nonEmpty(try? c?.decodeIfPresent(String.self, forKey: .summary))
        text = try? c?.decodeIfPresent(String.self, forKey: .text)
        at = try? c?.decodeIfPresent(String.self, forKey: .at)
        status = try? c?.decodeIfPresent(String.self, forKey: .status)
        error = PeerRef.nonEmpty(try? c?.decodeIfPresent(String.self, forKey: .error))
        // id is the dedup key; never missing from a current server, but don't fail without it
        id = PeerRef.nonEmpty(try? c?.decodeIfPresent(String.self, forKey: .id))
            ?? "\(direction ?? "")|\(at ?? "")|\(text?.hashValue ?? 0)"
    }

    public var peerDirection: PeerDirection? { direction.flatMap(PeerDirection.init(rawValue:)) }
    public var isSent: Bool { peerDirection == .sent }
    public var peerStatus: PeerStatus? { status.flatMap(PeerStatus.init(rawValue:)) }
    public var body: String { text ?? "" }
    public var date: Date? { PeerDates.parse(at) }
}

/// On the tool_result event of a peer `SendMessage`.
public struct PeerDelivery: Codable, Sendable, Hashable {
    public let toolUseId: String?
    /// "delivered" or "failed".
    public let status: String?
    public let error: String?

    public init(toolUseId: String?, status: String?, error: String? = nil) {
        self.toolUseId = toolUseId; self.status = status; self.error = error
    }

    public init(from decoder: Decoder) throws {
        let c = try? decoder.container(keyedBy: CodingKeys.self)
        toolUseId = try? c?.decodeIfPresent(String.self, forKey: .toolUseId)
        status = try? c?.decodeIfPresent(String.self, forKey: .status)
        error = PeerRef.nonEmpty(try? c?.decodeIfPresent(String.self, forKey: .error))
    }

    public var failed: Bool { status == PeerStatus.failed.rawValue }
}

/// A row of `GET /sessions/:id/peers`.
public struct PeerSummary: Codable, Sendable, Identifiable, Hashable {
    public let peer: PeerRef
    public let messageCount: Int?
    public let lastAt: String?
    /// "sent" or "received".
    public let lastDirection: String?
    public let lastPreview: String?

    public init(from decoder: Decoder) throws {
        let c = try? decoder.container(keyedBy: CodingKeys.self)
        peer = (try? c?.decodeIfPresent(PeerRef.self, forKey: .peer)) ?? PeerRef()
        messageCount = try? c?.decodeIfPresent(Int.self, forKey: .messageCount)
        lastAt = try? c?.decodeIfPresent(String.self, forKey: .lastAt)
        lastDirection = try? c?.decodeIfPresent(String.self, forKey: .lastDirection)
        lastPreview = try? c?.decodeIfPresent(String.self, forKey: .lastPreview)
    }

    public var id: String { peer.claudeSessionId ?? peer.name }
    public var lastDate: Date? { PeerDates.parse(lastAt) }
    public var lastWasSent: Bool { lastDirection == PeerDirection.sent.rawValue }
}

/// `GET /sessions/:id/peers/:peerUuid/thread`. `direction` in `messages` is relative to `this`.
public struct PeerThread: Codable, Sendable {
    public let this: PeerRef
    public let peer: PeerRef
    public let messages: [PeerMessage]

    public init(from decoder: Decoder) throws {
        let c = try? decoder.container(keyedBy: CodingKeys.self)
        this = (try? c?.decodeIfPresent(PeerRef.self, forKey: .this)) ?? PeerRef()
        peer = (try? c?.decodeIfPresent(PeerRef.self, forKey: .peer)) ?? PeerRef()
        messages = (try? c?.decodeIfPresent([PeerMessage].self, forKey: .messages)) ?? []
    }
}

/// ISO 8601 with or without fractional seconds (the shared decoder's `.iso8601` rejects fractions).
public enum PeerDates {
    public static func parse(_ string: String?) -> Date? {
        guard let string, !string.isEmpty else { return nil }
        let frac = ISO8601DateFormatter()
        frac.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return frac.date(from: string) ?? ISO8601DateFormatter().date(from: string)
    }
}
