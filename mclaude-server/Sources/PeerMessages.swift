import Foundation
import CryptoKit

// Peer messages: Claude Code sessions on this laptop messaging each other with SendMessage
// (docs/plan-peer-messages.md). Claude Code's formats are undocumented; every parser here fails
// safe, so an unrecognized shape just leaves the event as it renders today.

// MARK: - Wire shapes

/// One side of a peer conversation, as the app sees it.
struct PeerRef: Codable, Sendable, Equatable {
    var claudeSessionId: String?   // omitted when unresolved
    var ccName: String?            // Claude Code name, e.g. "mclaude-v1-f1"
    var address: String?           // "uds:/tmp/cc-socks/<pid>.sock"
    var sessionId: String?         // open managed session the caller can see
    var displayName: String
    var open: Bool
}

struct PeerMessage: Codable, Sendable {
    let id: String                 // the dedup key
    var direction: String          // "sent" | "received"
    var peer: PeerRef
    var summary: String?
    let text: String
    var at: String                 // ISO 8601
    var status: String             // "sending" | "delivered" | "failed" | "received"
    var error: String?
}

struct PeerDelivery: Codable, Sendable, Equatable {
    let toolUseId: String
    let status: String             // "delivered" | "failed"
    var error: String?
}

/// One line of peer-messages.jsonl.
struct PeerRecord: Codable, Sendable, Equatable {
    let key: String
    let observedIn: String         // observing session's Claude UUID
    let direction: String          // "sent" | "received"
    var peerUuid: String?
    var peerCcName: String?
    var peerAddress: String?
    var summary: String?
    let text: String
    let at: String
    let status: String             // "delivered" | "failed" | "received"
    var error: String?
}

/// GET /sessions/:id/peers element.
struct PeerSummary: Codable, Sendable {
    let peer: PeerRef
    let messageCount: Int
    let lastAt: String
    let lastDirection: String
    let lastPreview: String
}

/// GET /sessions/:id/peers/:peerUuid/thread
struct PeerThread: Codable, Sendable {
    let this: PeerRef
    let peer: PeerRef
    let messages: [PeerMessage]
}

// MARK: - Parsing Claude Code's formats

enum PeerParser {
    struct Tag: Equatable {
        let from: String?          // address, e.g. "uds:/tmp/cc-socks/26108.sock"
        let fromName: String?
        let body: String
    }

    static let openTag = "<cross-session-message"
    static let closeTag = "</cross-session-message>"
    static let wrapperLine = "Another Claude session sent a message:"
    /// Idle deliveries also end with a fixed paragraph Claude Code adds for the model
    /// ("This came from another Claude session — not typed by your user, …"). Like the wrapper
    /// line, it isn't part of the message.
    static let trailerPrefix = "This came from another Claude session"

    /// Every `<cross-session-message>` in `text`, in order, plus the text outside them (wrapper
    /// line and trailer removed, trimmed). Nil when there's no tag or any tag can't be parsed
    /// (no closing tag, or neither `from` nor `from-name`): the event then renders as today.
    static func parse(_ text: String) -> (tags: [Tag], otherText: String)? {
        guard text.contains(openTag) else { return nil }
        var tags: [Tag] = []
        var outside = ""
        var cursor = text.startIndex
        while let open = text.range(of: openTag, range: cursor..<text.endIndex) {
            // "<cross-session-messages" or similar isn't our tag
            guard open.upperBound < text.endIndex,
                  text[open.upperBound] == ">" || text[open.upperBound].isWhitespace else {
                outside += text[cursor..<open.upperBound]
                cursor = open.upperBound
                continue
            }
            outside += text[cursor..<open.lowerBound]
            guard let gt = endOfOpenTag(text, from: open.upperBound),
                  let close = text.range(of: closeTag, range: text.index(after: gt)..<text.endIndex) else { return nil }
            let attrs = attributes(String(text[open.upperBound..<gt]))
            let from = attrs["from"].flatMap { $0.isEmpty ? nil : $0 }
            let fromName = attrs["from-name"].flatMap { $0.isEmpty ? nil : $0 }
            guard from != nil || fromName != nil else { return nil }
            let body = String(text[text.index(after: gt)..<close.lowerBound]).trimmingCharacters(in: .whitespacesAndNewlines)
            tags.append(Tag(from: from, fromName: fromName, body: body))
            cursor = close.upperBound
        }
        outside += text[cursor...]
        guard !tags.isEmpty else { return nil }
        return (tags, cleanOutside(outside))
    }

    /// The `>` that ends the opening tag, skipping any inside quoted attribute values.
    private static func endOfOpenTag(_ text: String, from start: String.Index) -> String.Index? {
        var inQuote = false
        var i = start
        while i < text.endIndex {
            let c = text[i]
            if c == "\"" { inQuote.toggle() } else if c == ">", !inQuote { return i }
            i = text.index(after: i)
        }
        return nil
    }

    private static let attributePattern = try! NSRegularExpression(pattern: #"([A-Za-z_][A-Za-z0-9_:.\-]*)\s*=\s*"([^"]*)""#)

    static func attributes(_ s: String) -> [String: String] {
        var out: [String: String] = [:]
        let ns = s as NSString
        for m in attributePattern.matches(in: s, range: NSRange(location: 0, length: ns.length)) {
            let name = ns.substring(with: m.range(at: 1))
            if out[name] == nil { out[name] = unescape(ns.substring(with: m.range(at: 2))) }
        }
        return out
    }

    static func unescape(_ s: String) -> String {
        guard s.contains("&") else { return s }
        return s.replacingOccurrences(of: "&quot;", with: "\"").replacingOccurrences(of: "&apos;", with: "'")
            .replacingOccurrences(of: "&lt;", with: "<").replacingOccurrences(of: "&gt;", with: ">")
            .replacingOccurrences(of: "&amp;", with: "&")
    }

    private static func cleanOutside(_ s: String) -> String {
        var text = s
        if let r = text.range(of: trailerPrefix) {
            // The trailer is one paragraph: drop it up to the next blank line (or the end)
            let end = text.range(of: "\n\n", range: r.upperBound..<text.endIndex)?.lowerBound ?? text.endIndex
            text.removeSubrange(r.lowerBound..<end)
        }
        let lines = text.components(separatedBy: "\n")
            .filter { $0.trimmingCharacters(in: .whitespaces) != wrapperLine }
        return lines.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// The tags of `text` re-serialized bare (for matching an idle delivery's user line to the
    /// `queue-operation` enqueue that carries the bare tag). Nil when `text` has no tags.
    static func bareTagsSignature(_ text: String) -> String? {
        guard let parsed = parse(text) else { return nil }
        return parsed.tags.map { "\($0.from ?? "")|\($0.fromName ?? "")|\($0.body)" }.joined(separator: "\u{1}")
    }

    // MARK: SendMessage

    struct Send: Equatable {
        let to: String
        let message: String
        let summary: String?
    }

    /// A SendMessage input that is a cross-session peer message (not a subagent/teammate), else nil.
    static func peerSend(input: [String: Any]) -> Send? {
        guard let to = input["to"] as? String, !to.isEmpty,
              let message = input["message"] as? String else { return nil }
        let kind = input["recipient_kind"] as? String
        let isPeer: Bool
        switch kind {
        case "session", "name": isPeer = true
        case nil: isPeer = to.hasPrefix("uds:") || !isAgentId(to)
        default: isPeer = false
        }
        guard isPeer else { return nil }
        let summary = (input["summary"] as? String).flatMap { $0.isEmpty ? nil : $0 }
        return Send(to: to, message: message.trimmingCharacters(in: .whitespacesAndNewlines), summary: summary)
    }

    /// The SendMessage input behind a tool_use event (its `fullInput` JSON), if it's a peer send.
    static func peerSend(toolUse: ToolUseBlock?) -> Send? {
        guard let t = toolUse, t.name == "SendMessage", let full = t.fullInput,
              let input = (try? JSONSerialization.jsonObject(with: Data(full.utf8))) as? [String: Any] else { return nil }
        return peerSend(input: input)
    }

    /// An in-process agent id: "a" followed by exactly 16 lowercase hex characters.
    static func isAgentId(_ s: String) -> Bool {
        let chars = Array(s.utf8)
        guard chars.count == 17, chars[0] == UInt8(ascii: "a") else { return false }
        return chars.dropFirst().allSatisfy { (48...57).contains($0) || (97...102).contains($0) }
    }

    /// The SendMessage tool_result: `{"success": Bool, "message": String}`; otherwise the
    /// result's error flag decides.
    static func delivery(toolUseId: String, content: String, isError: Bool) -> PeerDelivery {
        if let json = (try? JSONSerialization.jsonObject(with: Data(content.utf8))) as? [String: Any],
           let success = json["success"] as? Bool {
            return success ? PeerDelivery(toolUseId: toolUseId, status: "delivered")
                : PeerDelivery(toolUseId: toolUseId, status: "failed", error: json["message"] as? String ?? content)
        }
        return isError ? PeerDelivery(toolUseId: toolUseId, status: "failed", error: content)
            : PeerDelivery(toolUseId: toolUseId, status: "delivered")
    }

    // MARK: Keys

    static func sha256(_ s: String) -> String {
        SHA256.hash(data: Data(s.utf8)).map { String(format: "%02x", $0) }.joined()
    }

    static func receivedKey(observing: String, tag: Tag) -> String {
        "recv:\(observing):\(tag.from ?? tag.fromName ?? ""):\(sha256(tag.body))"
    }

    static func sentKey(toolUseId: String) -> String { "sent:\(toolUseId)" }
}

// MARK: - Session registry (~/.claude/sessions/<pid>.json)

struct RegistryEntry: Sendable, Equatable {
    let pid: Int
    let sessionId: String
    let name: String?
    let socketPath: String?
    let updatedAt: Double

    var address: String? { socketPath.map { "uds:\($0)" } }
}

enum PeerRegistry {
    static let defaultDir = "\(NSHomeDirectory())/.claude/sessions"

    /// Every readable `<n>.json` entry. Anything else (`<pid>.<hash>.key`, bad JSON, missing
    /// pid/sessionId) is skipped. Read fresh on every observation: entries exist only while a
    /// process runs.
    static func read(dir: String) -> [RegistryEntry] {
        guard let files = try? FileManager.default.contentsOfDirectory(atPath: dir) else { return [] }
        var entries: [RegistryEntry] = []
        for file in files where file.hasSuffix(".json") {
            let stem = file.dropLast(5)
            guard !stem.isEmpty, stem.allSatisfy(\.isASCII), stem.allSatisfy(\.isNumber),
                  let data = FileManager.default.contents(atPath: "\(dir)/\(file)"),
                  let json = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
                  let pid = (json["pid"] as? NSNumber)?.intValue,
                  let sessionId = json["sessionId"] as? String, !sessionId.isEmpty else { continue }
            let name = (json["name"] as? String).flatMap { $0.isEmpty ? nil : $0 }
            let socket = (json["messagingSocketPath"] as? String).flatMap { $0.isEmpty ? nil : $0 }
            let updatedAt = (json["updatedAt"] as? NSNumber)?.doubleValue ?? 0
            entries.append(RegistryEntry(pid: pid, sessionId: sessionId.lowercased(), name: name,
                                         socketPath: socket, updatedAt: updatedAt))
        }
        return entries
    }

    /// `uds:<path>` (or a bare path) → the entry with that socket, else the entry for the pid in
    /// `/<pid>.sock` if it has no socket path recorded.
    static func resolveAddress(_ address: String, in entries: [RegistryEntry], excluding selfUuid: String) -> RegistryEntry? {
        let path = address.hasPrefix("uds:") ? String(address.dropFirst(4)) : address
        guard !path.isEmpty else { return nil }
        let others = entries.filter { $0.sessionId != selfUuid.lowercased() }
        if let e = others.first(where: { $0.socketPath == path }) { return e }
        let last = (path as NSString).lastPathComponent
        guard last.hasSuffix(".sock"), let pid = Int(last.dropLast(5)) else { return nil }
        return others.first { $0.pid == pid && $0.socketPath == nil }
    }

    /// Split "name [3fa9c1]" into ("name", "3fa9c1"). No suffix → (name, nil).
    static func splitRef(_ name: String) -> (name: String, hex: String?) {
        guard name.hasSuffix("]"), let open = name.range(of: " [", options: .backwards) else { return (name, nil) }
        let hex = name[open.upperBound..<name.index(before: name.endIndex)]
        guard !hex.isEmpty, hex.allSatisfy(\.isHexDigit) else { return (name, nil) }
        return (String(name[..<open.lowerBound]), hex.lowercased())
    }

    static func resolveName(_ raw: String, in entries: [RegistryEntry], excluding selfUuid: String) -> RegistryEntry? {
        let (name, hex) = splitRef(raw)
        let matches = entries.filter { $0.sessionId != selfUuid.lowercased() && $0.name == name }
        if let hex, let e = matches.filter({ $0.sessionId.hasPrefix(hex) }).max(by: { $0.updatedAt < $1.updatedAt }) {
            return e
        }
        return matches.max { $0.updatedAt < $1.updatedAt }
    }

    /// A received message: by address, then by `from-name`.
    static func resolveSender(address: String?, name: String?, in entries: [RegistryEntry], excluding selfUuid: String) -> RegistryEntry? {
        if let address, let e = resolveAddress(address, in: entries, excluding: selfUuid) { return e }
        if let name { return resolveName(name, in: entries, excluding: selfUuid) }
        return nil
    }

    /// A send's `to`: an address when it starts with `uds:`, else a name.
    static func resolveRecipient(_ to: String, in entries: [RegistryEntry], excluding selfUuid: String) -> RegistryEntry? {
        to.hasPrefix("uds:") ? resolveAddress(to, in: entries, excluding: selfUuid)
            : resolveName(to, in: entries, excluding: selfUuid)
    }
}

// MARK: - Record store (peer-messages.jsonl)

/// Append-only store of observed peer messages, loaded into memory at start. Synchronous and
/// lock-protected (not an actor) so SessionManager can dedup and write from inside its event loop
/// without suspending mid-line.
final class PeerStore: @unchecked Sendable {
    let path: String
    private let lock = NSLock()
    private var records: [PeerRecord] = []
    private var index: [String: Int] = [:]
    private var endsWithNewline = true

    init(path: String) {
        self.path = path
        load()
    }

    private func load() {
        guard let data = FileManager.default.contents(atPath: path) else { return }
        endsWithNewline = data.isEmpty || data.last == 0x0A
        let decoder = JSONDecoder()
        var lineNo = 0
        for line in data.split(separator: 0x0A, omittingEmptySubsequences: false) {
            lineNo += 1
            guard !line.allSatisfy({ $0 == 0x20 || $0 == 0x0D }) else { continue }
            guard let r = try? decoder.decode(PeerRecord.self, from: Data(line)) else {
                print("[peers] skipping unreadable line \(lineNo) of \(path)")
                continue
            }
            guard index[r.key] == nil else { continue }
            index[r.key] = records.count
            records.append(r)
        }
        print("[peers] loaded \(records.count) peer message record(s)")
    }

    func record(forKey key: String) -> PeerRecord? {
        lock.lock(); defer { lock.unlock() }
        return index[key].map { records[$0] }
    }

    func contains(_ key: String) -> Bool { record(forKey: key) != nil }

    /// Write a new record. A key that already exists is never written again (returns false).
    /// On a write failure the record isn't kept either, so threads only show what's on disk.
    @discardableResult
    func append(_ r: PeerRecord) -> Bool {
        lock.lock(); defer { lock.unlock() }
        guard index[r.key] == nil else { return false }
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        guard var line = try? encoder.encode(r) else { return false }
        line.append(0x0A)
        if !endsWithNewline { line.insert(0x0A, at: 0) }    // after a torn last line
        let fm = FileManager.default
        if !fm.fileExists(atPath: path) {
            try? fm.createDirectory(atPath: (path as NSString).deletingLastPathComponent, withIntermediateDirectories: true)
            fm.createFile(atPath: path, contents: nil, attributes: [.posixPermissions: 0o600])
        }
        do {
            guard let handle = FileHandle(forWritingAtPath: path) else { throw CocoaError(.fileWriteUnknown) }
            defer { try? handle.close() }
            try handle.seekToEnd()
            try handle.write(contentsOf: line)
        } catch {
            print("[peers] WARNING: failed to write \(path): \(error)")
            return false
        }
        endsWithNewline = true
        index[r.key] = records.count
        records.append(r)
        return true
    }

    /// Records where `uuid` is the observing side or the resolved peer.
    func records(involving uuid: String) -> [PeerRecord] {
        lock.lock(); defer { lock.unlock() }
        return records.filter { $0.observedIn == uuid || $0.peerUuid == uuid }
    }

    func all() -> [PeerRecord] {
        lock.lock(); defer { lock.unlock() }
        return records
    }
}

// MARK: - Display names and links

/// What the server knows about its sessions, for filling in PeerRefs.
struct PeerDirectory: Sendable {
    /// Claude UUID → (MClaude session id, current name), for open managed sessions
    var open: [String: OpenSession] = [:]
    /// Claude UUID → remembered name of an ended session
    var ended: [String: String] = [:]

    struct OpenSession: Sendable { let id: String; let name: String }

    /// Rebuild a PeerRef from its identity fields. `visible` says whether the caller can see an
    /// open session (only then are its id and name shown).
    func ref(claudeSessionId: String?, ccName: String?, address: String?, visible: (String) -> Bool) -> PeerRef {
        var displayName: String?
        var sessionId: String?
        if let uuid = claudeSessionId {
            if let o = open[uuid], visible(o.id) {
                sessionId = o.id
                displayName = o.name
            } else if open[uuid] == nil, let name = ended[uuid] {
                displayName = name
            }
        }
        return PeerRef(claudeSessionId: claudeSessionId, ccName: ccName, address: address, sessionId: sessionId,
                       displayName: displayName ?? ccName ?? "Another session", open: sessionId != nil)
    }

    func refresh(_ r: PeerRef, visible: (String) -> Bool) -> PeerRef {
        ref(claudeSessionId: r.claudeSessionId, ccName: r.ccName, address: r.address, visible: visible)
    }

    /// Session ids of open sessions referenced by these events (to check the caller's access).
    func sessionIds(in events: [SessionEvent]) -> Set<String> {
        Set(events.flatMap { ($0.peerMessages ?? []).compactMap { $0.peer.claudeSessionId.flatMap { open[$0]?.id } } })
    }

    /// The events with every PeerRef filled in for this caller.
    func refresh(_ events: [SessionEvent], visible: (String) -> Bool) -> [SessionEvent] {
        events.map { e in
            guard let msgs = e.peerMessages else { return e }
            var copy = e
            copy.peerMessages = msgs.map { var m = $0; m.peer = refresh(m.peer, visible: visible); return m }
            return copy
        }
    }
}

// MARK: - Threads and the peers list

enum PeerThreads {
    static let mergeWindow: TimeInterval = 120

    /// A message in a thread, before PeerRefs are filled in.
    struct Item: Equatable {
        let id: String
        let direction: String      // relative to `this`
        let summary: String?
        let text: String
        let at: String
        let date: Date
        let status: String
        let error: String?
    }

    /// Every message between `this` and `peer` from both sides' records, merged, oldest first.
    static func items(this: String, peer: String, records: [PeerRecord]) -> [Item] {
        let pair = records.filter {
            ($0.observedIn == this && $0.peerUuid == peer) || ($0.observedIn == peer && $0.peerUuid == this)
        }
        func date(_ r: PeerRecord) -> Date { Date.fromISO(r.at) ?? .distantPast }
        var used = Set<String>()
        var out: [Item] = []
        let sent = pair.filter { $0.direction == "sent" }.sorted { date($0) < date($1) }
        let received = pair.filter { $0.direction == "received" }.sorted { date($0) < date($1) }
        for s in sent {
            var match: PeerRecord?
            if s.status == "delivered" {
                match = received.filter { r in
                    !used.contains(r.key) && r.observedIn == s.peerUuid && r.peerUuid == s.observedIn
                        && r.text == s.text && abs(date(r).timeIntervalSince(date(s))) <= mergeWindow
                }.min { abs(date($0).timeIntervalSince(date(s))) < abs(date($1).timeIntervalSince(date(s))) }
            }
            if let m = match { used.insert(m.key) }
            let earlier = match.map { date($0) < date(s) ? $0 : s } ?? s
            let byThis = s.observedIn == this
            // The id is the key of the record observed in `this`, so a card's id finds its message
            let id = byThis ? s.key : (match?.key ?? s.key)
            let status: String
            if byThis { status = s.status } else { status = s.status == "failed" ? "failed" : "received" }
            out.append(Item(id: id, direction: byThis ? "sent" : "received", summary: s.summary, text: s.text,
                            at: earlier.at, date: date(earlier), status: status,
                            error: s.status == "failed" ? s.error : nil))
        }
        for r in received where !used.contains(r.key) {
            let byThis = r.observedIn == this      // `this` received it
            out.append(Item(id: r.key, direction: byThis ? "received" : "sent", summary: nil, text: r.text,
                            at: r.at, date: date(r), status: byThis ? "received" : "delivered", error: nil))
        }
        return out.sorted { $0.date == $1.date ? $0.id < $1.id : $0.date < $1.date }
    }

    /// Every resolved peer of `this`, with its latest record's name/address (from `this`'s side).
    static func peers(of this: String, records: [PeerRecord]) -> [(uuid: String, ccName: String?, address: String?)] {
        var order: [String] = []
        var info: [String: (ccName: String?, address: String?)] = [:]
        for r in records {
            let other: String?
            if r.observedIn == this { other = r.peerUuid } else if r.peerUuid == this { other = r.observedIn } else { other = nil }
            guard let other, other != this else { continue }
            if info[other] == nil { order.append(other); info[other] = (nil, nil) }
            if r.observedIn == this {
                if let n = r.peerCcName { info[other]?.ccName = n }
                if let a = r.peerAddress { info[other]?.address = a }
            }
        }
        return order.map { ($0, info[$0]?.ccName, info[$0]?.address) }
    }

    /// The latest known ccName/address of `uuid` as seen by its peers.
    static func identity(of uuid: String, records: [PeerRecord]) -> (ccName: String?, address: String?) {
        var ccName: String?, address: String?
        for r in records where r.peerUuid == uuid {
            if let n = r.peerCcName { ccName = n }
            if let a = r.peerAddress { address = a }
        }
        return (ccName, address)
    }

    static func preview(_ text: String) -> String {
        let line = text.components(separatedBy: "\n").map { $0.trimmingCharacters(in: .whitespaces) }
            .first { !$0.isEmpty } ?? ""
        return line.count > 120 ? String(line.prefix(120)) + "…" : line
    }

    static func message(_ i: Item, peer: PeerRef) -> PeerMessage {
        PeerMessage(id: i.id, direction: i.direction, peer: peer, summary: i.summary, text: i.text, at: i.at,
                    status: i.status, error: i.error)
    }

    /// GET /sessions/:id/peers, most recent first. `ref` fills in a PeerRef for a peer.
    static func summaries(of this: String, records: [PeerRecord],
                          ref: (_ uuid: String, _ ccName: String?, _ address: String?) -> PeerRef) -> [PeerSummary] {
        var out: [(Date, PeerSummary)] = []
        for p in peers(of: this, records: records) {
            let items = items(this: this, peer: p.uuid, records: records)
            guard let last = items.last else { continue }
            out.append((last.date, PeerSummary(peer: ref(p.uuid, p.ccName, p.address), messageCount: items.count,
                                               lastAt: last.at, lastDirection: last.direction,
                                               lastPreview: preview(last.text))))
        }
        return out.sorted { $0.0 > $1.0 }.map(\.1)
    }
}

// MARK: - Observation and event enrichment

/// A peer SendMessage whose tool_result hasn't arrived yet. Resolved when the tool_use was seen.
struct PendingPeerSend: Sendable {
    let send: PeerParser.Send
    let peerUuid: String?
    let ccName: String?
    let address: String?
    let at: String
}

/// Turns a managed session's events into peer records (live) and attaches peerMessages /
/// peerOtherText / peerDelivery. PeerRefs come out with identity fields only; the API fills in
/// sessionId/displayName/open per caller (PeerDirectory.refresh).
struct PeerObserver: Sendable {
    let store: PeerStore
    let registryDir: String

    static func baseRef(uuid: String?, ccName: String?, address: String?) -> PeerRef {
        PeerRef(claudeSessionId: uuid, ccName: ccName, address: address, sessionId: nil,
                displayName: ccName ?? "Another session", open: false)
    }

    /// The sender's summary for a received record, when the sender is managed too (cross-side merge).
    private func senderSummary(_ r: PeerRecord) -> String? {
        guard r.direction == "received", let sender = r.peerUuid, let at = Date.fromISO(r.at) else { return nil }
        return store.records(involving: sender).first { s in
            s.direction == "sent" && s.status == "delivered" && s.observedIn == sender && s.peerUuid == r.observedIn
                && s.text == r.text && abs((Date.fromISO(s.at) ?? .distantPast).timeIntervalSince(at)) <= PeerThreads.mergeWindow
        }?.summary
    }

    func message(_ r: PeerRecord) -> PeerMessage {
        PeerMessage(id: r.key, direction: r.direction,
                    peer: Self.baseRef(uuid: r.peerUuid, ccName: r.peerCcName, address: r.peerAddress),
                    summary: r.direction == "sent" ? r.summary : senderSummary(r), text: r.text, at: r.at,
                    status: r.status, error: r.status == "failed" ? r.error : nil)
    }

    /// Sends resolve `to`: an address names the socket; a name is recorded without its " [ref]".
    private func sendIdentity(_ send: PeerParser.Send, _ entry: RegistryEntry?) -> (ccName: String?, address: String?) {
        let isAddress = send.to.hasPrefix("uds:")
        return (entry?.name ?? (isAddress ? nil : PeerRegistry.splitRef(send.to).name),
                isAddress ? send.to : entry?.address)
    }

    private func applyReceived(_ event: inout SessionEvent, observing: String,
                               build: (PeerParser.Tag, String) -> PeerRecord?) {
        guard event.type == .user, let text = event.text, let parsed = PeerParser.parse(text) else { return }
        event.peerMessages = parsed.tags.map { tag in
            let key = PeerParser.receivedKey(observing: observing, tag: tag)
            if let r = store.record(forKey: key) ?? build(tag, key) { return message(r) }
            return PeerMessage(id: key, direction: "received",
                               peer: Self.baseRef(uuid: nil, ccName: tag.fromName, address: tag.from),
                               summary: nil, text: tag.body, at: event.timestamp, status: "received", error: nil)
        }
        event.peerOtherText = parsed.otherText.isEmpty ? nil : parsed.otherText
    }

    /// Live stream: record what's new and enrich the event. `pending` carries sends from their
    /// tool_use to their tool_result. `receivedAt` is the stream line's own timestamp: for a
    /// mid-turn delivery that's the enqueue, which can be long before Claude takes it.
    func observeLive(_ event: SessionEvent, observing: String, now: String, receivedAt: String? = nil,
                     pending: inout [String: PendingPeerSend]) -> SessionEvent {
        let receivedAt = receivedAt.flatMap { Date.fromISO($0) }.map { ISO8601DateFormatter.fractional.string(from: $0) } ?? now
        var e = event
        var registry: [RegistryEntry]?
        func entries() -> [RegistryEntry] {
            if let registry { return registry }
            let r = PeerRegistry.read(dir: registryDir)
            registry = r
            return r
        }
        switch e.type {
        case .user:
            applyReceived(&e, observing: observing) { tag, key in
                let entry = PeerRegistry.resolveSender(address: tag.from, name: tag.fromName, in: entries(), excluding: observing)
                let r = PeerRecord(key: key, observedIn: observing, direction: "received", peerUuid: entry?.sessionId,
                                   peerCcName: tag.fromName ?? entry?.name, peerAddress: tag.from ?? entry?.address,
                                   summary: nil, text: tag.body, at: receivedAt, status: "received", error: nil)
                store.append(r)          // a failed write is logged; the live event is still enriched
                return r
            }
        case .toolResult:
            break
        case .toolUse:
            guard let send = PeerParser.peerSend(toolUse: e.toolUse), let toolId = e.toolUse?.id, !toolId.isEmpty else { break }
            let key = PeerParser.sentKey(toolUseId: toolId)
            if let r = store.record(forKey: key) {
                e.peerMessages = [message(r)]
                break
            }
            let entry = PeerRegistry.resolveRecipient(send.to, in: entries(), excluding: observing)
            let id = sendIdentity(send, entry)
            pending[toolId] = PendingPeerSend(send: send, peerUuid: entry?.sessionId, ccName: id.ccName,
                                              address: id.address, at: now)
            e.peerMessages = [PeerMessage(id: key, direction: "sent",
                                          peer: Self.baseRef(uuid: entry?.sessionId, ccName: id.ccName, address: id.address),
                                          summary: send.summary, text: send.message, at: now, status: "sending", error: nil)]
        default:
            break
        }
        // Results of pending sends (tool_result events, or a user event carrying results)
        if !pending.isEmpty {
            for result in e.toolResults ?? [] {
                guard let p = pending.removeValue(forKey: result.toolUseId) else { continue }
                let d = PeerParser.delivery(toolUseId: result.toolUseId, content: result.content, isError: result.isError)
                store.append(PeerRecord(key: PeerParser.sentKey(toolUseId: result.toolUseId), observedIn: observing,
                                        direction: "sent", peerUuid: p.peerUuid, peerCcName: p.ccName,
                                        peerAddress: p.address, summary: p.send.summary, text: p.send.message,
                                        at: p.at, status: d.status, error: d.error))
                if e.peerDelivery == nil { e.peerDelivery = d }
            }
        }
        return e
    }

    /// History pages and rebuilt history: look records up by key, never write. A send with no
    /// record and no result yet is resolved against the live registry, like a live tool_use.
    func enrichHistory(_ events: [SessionEvent], observing: String) -> [SessionEvent] {
        var sends: [String: PeerParser.Send] = [:]       // tool_use id -> send, from this page
        var resultIds = Set<String>()
        for e in events {
            if e.type == .toolUse, let id = e.toolUse?.id, let s = PeerParser.peerSend(toolUse: e.toolUse) { sends[id] = s }
            for r in e.toolResults ?? [] { resultIds.insert(r.toolUseId) }
        }
        var registry: [RegistryEntry]?
        return events.map { event in
            var e = event
            switch e.type {
            case .user:
                applyReceived(&e, observing: observing) { _, _ in nil }
            case .toolUse:
                guard let id = e.toolUse?.id, let send = sends[id] else { break }
                if let r = store.record(forKey: PeerParser.sentKey(toolUseId: id)) {
                    e.peerMessages = [message(r)]
                    break
                }
                var entry: RegistryEntry?
                var status = "sending"
                if resultIds.contains(id) {
                    // Answered but never recorded: from before this feature (unresolved)
                    status = events.lazy.compactMap { $0.toolResults?.first { $0.toolUseId == id } }.first
                        .map { PeerParser.delivery(toolUseId: id, content: $0.content, isError: $0.isError).status } ?? "delivered"
                } else {
                    if registry == nil { registry = PeerRegistry.read(dir: registryDir) }
                    entry = PeerRegistry.resolveRecipient(send.to, in: registry ?? [], excluding: observing)
                }
                let ident = sendIdentity(send, entry)
                let error = status == "failed"
                    ? events.lazy.compactMap { $0.toolResults?.first { $0.toolUseId == id } }.first
                        .map { PeerParser.delivery(toolUseId: id, content: $0.content, isError: $0.isError).error } ?? nil
                    : nil
                e.peerMessages = [PeerMessage(id: PeerParser.sentKey(toolUseId: id), direction: "sent",
                                              peer: Self.baseRef(uuid: entry?.sessionId, ccName: ident.ccName, address: ident.address),
                                              summary: send.summary, text: send.message, at: e.timestamp,
                                              status: status, error: error)]
            case .toolResult:
                for r in e.toolResults ?? [] where sends[r.toolUseId] != nil
                    || store.contains(PeerParser.sentKey(toolUseId: r.toolUseId)) {
                    e.peerDelivery = PeerParser.delivery(toolUseId: r.toolUseId, content: r.content, isError: r.isError)
                    break
                }
            default:
                break
            }
            return e
        }
    }
}

extension PeerDirectory {
    /// Which of these open session ids the caller may see.
    static func visible(_ ids: Set<String>, canSee: (String) async -> Bool) async -> Set<String> {
        var out = Set<String>()
        for id in ids where await canSee(id) { out.insert(id) }
        return out
    }

    /// The events with PeerRefs filled in for this caller.
    func refresh(_ events: [SessionEvent], canSee: (String) async -> Bool) async -> [SessionEvent] {
        guard events.contains(where: { $0.peerMessages != nil }) else { return events }
        let ok = await Self.visible(sessionIds(in: events), canSee: canSee)
        return refresh(events, visible: { ok.contains($0) })
    }
}

// MARK: - API responses

enum PeerAPI {
    /// GET /sessions/:id/peers for the session with Claude UUID `this`.
    static func peers(this: String, records: [PeerRecord], directory: PeerDirectory,
                      visible: (String) -> Bool) -> [PeerSummary] {
        PeerThreads.summaries(of: this, records: records) { uuid, _, _ in
            let ident = PeerThreads.identity(of: uuid, records: records)
            return directory.ref(claudeSessionId: uuid, ccName: ident.ccName, address: ident.address, visible: visible)
        }
    }

    /// GET /sessions/:id/peers/:peerUuid/thread. Nil when the two have no records together.
    static func thread(this: String, peer: String, records: [PeerRecord], directory: PeerDirectory,
                       registry: [RegistryEntry], visible: (String) -> Bool) -> PeerThread? {
        let items = PeerThreads.items(this: this, peer: peer, records: records)
        guard !items.isEmpty else { return nil }
        func ref(_ uuid: String) -> PeerRef {
            var ident = PeerThreads.identity(of: uuid, records: records)
            if ident.ccName == nil || ident.address == nil, let e = registry.first(where: { $0.sessionId == uuid }) {
                ident = (ident.ccName ?? e.name, ident.address ?? e.address)
            }
            return directory.ref(claudeSessionId: uuid, ccName: ident.ccName, address: ident.address, visible: visible)
        }
        let peerRef = ref(peer)
        return PeerThread(this: ref(this), peer: peerRef, messages: items.map { PeerThreads.message($0, peer: peerRef) })
    }
}
