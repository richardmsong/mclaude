import Foundation

/// One row of `GET /transcripts`: a Claude Code transcript on this laptop that isn't an open session.
struct TranscriptListing: Codable, Sendable, Equatable {
    let id: String              // Claude session UUID (the file name)
    let cwd: String
    let projectName: String
    let name: String?           // remembered ended-session name, else the last ai-title
    let firstPrompt: String?
    let modifiedAt: Date
    let inUse: Bool             // written in the last 120 s (maybe still open in a terminal)
}

/// What the scan pulls out of one transcript file.
struct TranscriptSummary: Sendable, Equatable {
    let id: String
    let path: String
    let cwd: String
    let firstPrompt: String?
    let aiTitle: String?
    let modifiedAt: Date
}

/// Session state the transcript endpoints need from SessionManager, readable without hopping
/// onto its actor: which Claude session ids are open (-> display id) and the names of ended ones.
final class ResumeState: @unchecked Sendable {
    private let lock = NSLock()
    private var openIds: [String: String] = [:]
    private var ended: [String: String] = [:]

    func update(open: [String: String], endedNames: [String: String]) {
        lock.lock(); defer { lock.unlock() }
        openIds = open
        ended = endedNames
    }

    func read() -> (open: [String: String], endedNames: [String: String]) {
        lock.lock(); defer { lock.unlock() }
        return (openIds, ended)
    }
}

/// Scans `~/.claude/projects/*/<uuid>.jsonl` for the Resume sheet. Runs on its own actor so a scan
/// never blocks SessionManager. Only each file's first and last 256 KB are read (transcripts can be
/// tens of MB), and results are cached per path until the file's size or mtime changes.
actor TranscriptIndex {
    static let window = 256 * 1024
    static let inUseWindow: TimeInterval = 120
    static let maxPromptLength = 200

    private let projectsDir: String
    /// path -> (size, mtime, parsed). `parsed` is nil for files without a cwd.
    private var cache: [String: (size: Int, mtime: Date, summary: TranscriptSummary?)] = [:]

    init(projectsDir: String = ClaudePaths.projectsDir) {
        self.projectsDir = projectsDir
    }

    /// Every resumable transcript, newest first. `open` are the Claude session ids of open sessions.
    func list(open: Set<String>, endedNames: [String: String], now: Date = Date()) -> [TranscriptListing] {
        var seen = Set<String>()
        var result: [TranscriptListing] = []
        for path in transcriptPaths() {
            seen.insert(path)
            guard let s = summary(at: path), !open.contains(s.id), let prompt = s.firstPrompt else { continue }
            result.append(TranscriptListing(
                id: s.id, cwd: s.cwd, projectName: (s.cwd as NSString).lastPathComponent,
                name: endedNames[s.id] ?? s.aiTitle, firstPrompt: prompt, modifiedAt: s.modifiedAt,
                inUse: now.timeIntervalSince(s.modifiedAt) < Self.inUseWindow))
        }
        for path in cache.keys where !seen.contains(path) { cache.removeValue(forKey: path) }
        return result.sorted { $0.modifiedAt > $1.modifiedAt }
    }

    /// The transcript with this Claude session id, or nil if there's none (or it has no cwd).
    func find(id: String) -> TranscriptSummary? {
        guard Self.isSessionId(id) else { return nil }
        let dirs = (try? FileManager.default.contentsOfDirectory(atPath: projectsDir)) ?? []
        for dir in dirs {
            let path = "\(projectsDir)/\(dir)/\(id).jsonl"
            if FileManager.default.fileExists(atPath: path), let s = summary(at: path) { return s }
        }
        return nil
    }

    /// Top-level `<uuid>.jsonl` files of each project folder (subagent transcripts live deeper).
    private func transcriptPaths() -> [String] {
        let fm = FileManager.default
        var paths: [String] = []
        for dir in (try? fm.contentsOfDirectory(atPath: projectsDir)) ?? [] {
            let dirPath = "\(projectsDir)/\(dir)"
            var isDir: ObjCBool = false
            guard fm.fileExists(atPath: dirPath, isDirectory: &isDir), isDir.boolValue else { continue }
            for file in (try? fm.contentsOfDirectory(atPath: dirPath)) ?? []
                where file.hasSuffix(".jsonl") && Self.isSessionId(String(file.dropLast(6))) {
                paths.append("\(dirPath)/\(file)")
            }
        }
        return paths
    }

    private func summary(at path: String) -> TranscriptSummary? {
        guard let attrs = try? FileManager.default.attributesOfItem(atPath: path),
              (attrs[.type] as? FileAttributeType) == .typeRegular,
              let size = (attrs[.size] as? NSNumber)?.intValue,
              let mtime = attrs[.modificationDate] as? Date else { return nil }
        if let hit = cache[path], hit.size == size, hit.mtime == mtime { return hit.summary }
        let id = String((path as NSString).lastPathComponent.dropLast(6))
        let parsed = Self.readWindows(path: path, size: size).flatMap { head, tail in
            Self.parse(head: head, tail: tail).map { info in
                TranscriptSummary(id: id, path: path, cwd: info.cwd, firstPrompt: info.firstPrompt,
                                  aiTitle: info.aiTitle, modifiedAt: mtime)
            }
        }
        cache[path] = (size, mtime, parsed)
        return parsed
    }

    // MARK: - Parsing (static for tests)

    static func isSessionId(_ s: String) -> Bool {
        UUID(uuidString: s) != nil
    }

    /// The first `window` bytes, plus the last `window` bytes when the file is bigger than both
    /// windows together (nil tail = the head is the whole file).
    static func readWindows(path: String, size: Int, window: Int = window) -> (head: Data, tail: Data?)? {
        guard let handle = FileHandle(forReadingAtPath: path) else { return nil }
        defer { try? handle.close() }
        if size <= 2 * window {
            guard let all = try? handle.readToEnd() else { return nil }
            return (all, nil)
        }
        guard let head = try? handle.read(upToCount: window) else { return nil }
        try? handle.seek(toOffset: UInt64(size - window))
        let tail = (try? handle.readToEnd()) ?? Data()
        return (head, tail)
    }

    /// cwd (first line with one), first real prompt, and last ai-title. Nil if there's no cwd.
    /// With a tail, the head's last line and the tail's first line may be cut off; they're dropped.
    static func parse(head: Data, tail: Data?) -> (cwd: String, firstPrompt: String?, aiTitle: String?)? {
        var cwd: String?
        var prompt: String?
        var title: String?
        var headLines = lines(head)
        if tail != nil, !headLines.isEmpty, head.last != UInt8(ascii: "\n") { headLines.removeLast() }
        for line in headLines {
            let isTitle = line.contains("\"ai-title\"")
            // Once cwd and the prompt are known, only ai-title lines matter
            if cwd != nil, prompt != nil, !isTitle { continue }
            guard let json = try? JSONSerialization.jsonObject(with: Data(line.utf8)) as? [String: Any] else { continue }
            if cwd == nil, let c = json["cwd"] as? String, !c.isEmpty { cwd = c }
            if prompt == nil, let p = realPrompt(json) { prompt = p }
            if isTitle, let t = aiTitle(json) { title = t }
        }
        if let tail {
            var tailLines = lines(tail)
            if !tailLines.isEmpty { tailLines.removeFirst() }
            for line in tailLines.reversed() where line.contains("\"ai-title\"") {
                if let json = try? JSONSerialization.jsonObject(with: Data(line.utf8)) as? [String: Any],
                   let t = aiTitle(json) {
                    title = t
                    break
                }
            }
        }
        guard let cwd else { return nil }
        return (cwd, prompt, title)
    }

    private static func lines(_ data: Data) -> [String] {
        data.split(separator: UInt8(ascii: "\n")).map { String(decoding: $0, as: UTF8.self) }
    }

    private static func aiTitle(_ json: [String: Any]) -> String? {
        guard json["type"] as? String == "ai-title",
              let t = (json["aiTitle"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines),
              !t.isEmpty else { return nil }
        return t
    }

    static let commandPrefixes = ["<command-name>", "<local-command", "<command-message>", "Caveat:"]

    /// The text of a prompt the user actually typed: a non-meta, non-sidechain `user` line whose
    /// content is a string or text blocks (not tool results, slash-command echoes, or compaction
    /// summaries). Whitespace-collapsed and capped at `limit` (default 200) characters.
    static func realPrompt(_ json: [String: Any], limit: Int = maxPromptLength) -> String? {
        guard json["type"] as? String == "user",
              json["isMeta"] as? Bool != true,
              json["isSidechain"] as? Bool != true,
              json["isCompactSummary"] as? Bool != true,
              let message = json["message"] as? [String: Any] else { return nil }
        let text: String
        if let s = message["content"] as? String {
            text = s
        } else if let blocks = message["content"] as? [[String: Any]] {
            guard !blocks.contains(where: { $0["type"] as? String == "tool_result" }) else { return nil }
            text = blocks.filter { $0["type"] as? String == "text" }.compactMap { $0["text"] as? String }
                .joined(separator: "\n")
        } else {
            return nil
        }
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, !commandPrefixes.contains(where: { trimmed.hasPrefix($0) }) else { return nil }
        let collapsed = trimmed.split(whereSeparator: { $0.isWhitespace }).joined(separator: " ")
        return String(collapsed.prefix(limit))
    }
}
