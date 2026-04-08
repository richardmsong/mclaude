import Foundation

/// Watches JSONL session files and emits parsed events as new lines appear.
actor JSONLTailer {
    private let projectsDir: String
    private var watchers: [String: FileWatcher] = [:]  // sessionId -> watcher
    private var onEvent: (@Sendable (String, SessionEvent) -> Void)?
    /// Tracks whether each session is mid-turn (working) based on JSONL events.
    /// Set to true on user text event, false on turn_duration system event.
    private var sessionWorking: [String: Bool] = [:]
    /// Timestamp of the last turn_duration event per session (ISO 8601 string from JSONL).
    private var lastTurnEndTimestamp: [String: String] = [:]

    // Subagent tracking
    private var subagentWatchers: [String: FileWatcher] = [:]  // agentId -> watcher
    /// Maps display ID -> JSONL path for deriving subagent directory
    private var sessionJsonlPaths: [String: String] = [:]
    /// Tracks known subagent JSONL filenames to detect new ones
    private var knownSubagentFiles: [String: Set<String>] = [:]  // displayId -> filenames
    /// Maps agentId -> (displayId, toolUseId, SubagentInfo)
    private var activeSubagents: [String: (displayId: String, info: SubagentInfo)] = [:]

    init(projectsDir: String = "\(NSHomeDirectory())/.claude/projects") {
        self.projectsDir = projectsDir
    }

    func setOnEvent(_ handler: @escaping @Sendable (String, SessionEvent) -> Void) {
        self.onEvent = handler
    }

    /// Returns true if JSONL events indicate the session is mid-turn (working).
    func isWorking(id: String) -> Bool {
        return sessionWorking[id] ?? false
    }

    /// Returns the Date when the session last became idle (last turn_duration timestamp).
    func lastIdleSince(id: String) -> Date? {
        guard let ts = lastTurnEndTimestamp[id] else { return nil }
        return parseISO8601(ts)
    }

    private nonisolated func parseISO8601(_ str: String) -> Date? {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f.date(from: str) ?? {
            let f2 = ISO8601DateFormatter()
            f2.formatOptions = [.withInternetDateTime]
            return f2.date(from: str)
        }()
    }

    /// Track sessionId per display id to detect /clear (sessionId change)
    private var watchedSessionIds: [String: String] = [:]

    /// Start tailing a session's JSONL file
    func watchSession(id: String, sessionId: String, cwd: String) {
        // If sessionId changed (e.g. /clear), tear down old watcher
        if let oldSessionId = watchedSessionIds[id], oldSessionId != sessionId {
            unwatchSession(id: id)
            sessionWorking[id] = false
            lastTurnEndTimestamp.removeValue(forKey: id)
        }
        guard watchers[id] == nil else { return }
        watchedSessionIds[id] = sessionId

        // Derive the JSONL path: ~/.claude/projects/{encoded-cwd}/{sessionId}.jsonl
        let encodedPath = cwd.replacingOccurrences(of: "/", with: "-")
        let jsonlPath = "\(projectsDir)/\(encodedPath)/\(sessionId).jsonl"

        guard FileManager.default.fileExists(atPath: jsonlPath) else {
            print("[jsonl] File not found: \(jsonlPath)")
            return
        }

        // Seed last turn_duration timestamp from historical data
        if lastTurnEndTimestamp[id] == nil {
            seedLastTurnEnd(id: id, jsonlPath: jsonlPath)
        }

        sessionJsonlPaths[id] = jsonlPath

        // Seed known subagent files so we only detect new ones
        let subDir = String(jsonlPath.dropLast(6)) + "/subagents"
        if let existing = try? FileManager.default.contentsOfDirectory(atPath: subDir) {
            knownSubagentFiles[id] = Set(existing.filter { $0.hasSuffix(".jsonl") })
        }

        let watcher = FileWatcher(path: jsonlPath, sessionDisplayId: id) { [weak self] displayId, event in
            guard let self else { return }
            Task { await self.handleEvent(displayId: displayId, event: event) }
        }
        watchers[id] = watcher
        watcher.start()
        print("[jsonl] Watching: \(jsonlPath)")
    }

    func unwatchSession(id: String) {
        watchers[id]?.stop()
        watchers.removeValue(forKey: id)
        watchedSessionIds.removeValue(forKey: id)
        sessionJsonlPaths.removeValue(forKey: id)
        // Clean up any subagent watchers for this session
        let toRemove = activeSubagents.filter { $0.value.displayId == id }.map(\.key)
        for agentId in toRemove {
            subagentWatchers[agentId]?.stop()
            subagentWatchers.removeValue(forKey: agentId)
            activeSubagents.removeValue(forKey: agentId)
        }
        knownSubagentFiles.removeValue(forKey: id)
    }

    /// Send recent history for a session (last N events)
    func getRecentEvents(id: String, sessionId: String, cwd: String, count: Int = 50) -> [SessionEvent] {
        let encodedPath = cwd.replacingOccurrences(of: "/", with: "-")
        let jsonlPath = "\(projectsDir)/\(encodedPath)/\(sessionId).jsonl"

        guard let data = FileManager.default.contents(atPath: jsonlPath),
              let content = String(data: data, encoding: .utf8) else { return [] }

        let lines = content.components(separatedBy: "\n").filter { !$0.isEmpty }
        let recentLines = Array(lines.suffix(count))

        // Pre-scan for enqueue texts (for dedup) and remove timestamps (for positioning)
        var enqueuedForDedup: Set<String> = []
        var removeTimestamps: [String] = []  // remove timestamps in order
        for line in recentLines {
            guard let lineData = line.data(using: .utf8),
                  let json = try? JSONSerialization.jsonObject(with: lineData) as? [String: Any],
                  json["type"] as? String == "queue-operation" else { continue }
            let op = json["operation"] as? String ?? ""
            if op == "enqueue", let text = json["content"] as? String {
                enqueuedForDedup.insert(text.trimmingCharacters(in: .whitespacesAndNewlines))
            } else if op == "remove" || op == "dequeue" {
                if let ts = json["timestamp"] as? String {
                    removeTimestamps.append(ts)
                }
            }
        }

        var events: [SessionEvent] = []
        var removeIdx = 0
        for line in recentLines {
            guard let event = JSONLParser.parseEvent(line: line) else { continue }
            // Skip real user events that duplicate an enqueued mid-turn message
            // Only skip non-synthetic events (real user events), so the synthetic queue event survives
            if event.type == .user, !event.uuid.hasPrefix("q-"), let text = event.text {
                let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
                if enqueuedForDedup.contains(trimmed) {
                    enqueuedForDedup.remove(trimmed)
                    continue
                }
            }
            // Reposition synthetic queue events to when they were processed (remove/dequeue time)
            if event.type == .user, event.uuid.hasPrefix("q-"), removeIdx < removeTimestamps.count {
                let adjusted = SessionEvent(
                    uuid: event.uuid, timestamp: removeTimestamps[removeIdx],
                    type: event.type, text: event.text, thinking: event.thinking,
                    toolUse: event.toolUse, toolResults: event.toolResults,
                    model: event.model, durationMs: event.durationMs
                )
                removeIdx += 1
                events.append(adjusted)
                continue
            }
            events.append(event)
        }
        return events
    }

    /// Scan tail of JSONL for the last turn_duration timestamp to survive restarts.
    private func seedLastTurnEnd(id: String, jsonlPath: String) {
        guard let data = FileManager.default.contents(atPath: jsonlPath),
              let content = String(data: data, encoding: .utf8) else { return }
        // Scan last 200 lines for the most recent turn_duration
        let lines = content.components(separatedBy: "\n").filter { !$0.isEmpty }
        for line in lines.suffix(200).reversed() {
            guard let lineData = line.data(using: .utf8),
                  let json = try? JSONSerialization.jsonObject(with: lineData) as? [String: Any],
                  json["type"] as? String == "system",
                  json["subtype"] as? String == "turn_duration",
                  let ts = json["timestamp"] as? String else { continue }
            lastTurnEndTimestamp[id] = ts
            print("[jsonl] Seeded idle-since for \(id): \(ts)")
            return
        }
    }

    private func handleEvent(displayId: String, event: SessionEvent) {
        // Track working state from JSONL events
        if event.type == .user && event.text != nil && !(event.text?.isEmpty ?? true) {
            sessionWorking[displayId] = true
        } else if event.type == .system && event.durationMs != nil {
            // turn_duration = turn ended
            sessionWorking[displayId] = false
            lastTurnEndTimestamp[displayId] = event.timestamp
        }

        // Detect Agent tool_use → start watching subagent JSONL
        if event.type == .toolUse, event.toolUse?.name == "Agent" {
            let toolUseId = event.toolUse?.id ?? ""
            let description = event.toolUse?.inputSummary ?? ""
            startSubagentWatcher(displayId: displayId, toolUseId: toolUseId, description: description)
        }

        onEvent?(displayId, event)
    }

    /// Subagents directory for a session
    private func subagentsDir(for displayId: String) -> String? {
        guard let jsonlPath = sessionJsonlPaths[displayId] else { return nil }
        // {sessionId}.jsonl → {sessionId}/subagents/
        let base = String(jsonlPath.dropLast(6)) // remove .jsonl
        return "\(base)/subagents"
    }

    /// Detect new subagent JSONL file and start watching it
    private func startSubagentWatcher(displayId: String, toolUseId: String, description: String) {
        guard let dir = subagentsDir(for: displayId) else { return }

        // Get current known files
        let known = knownSubagentFiles[displayId] ?? []

        // Poll for new file (may not exist yet)
        let capturedSelf = self
        Task.detached {
            for attempt in 0..<20 {  // poll up to 2s
                if attempt > 0 {
                    try? await Task.sleep(nanoseconds: 100_000_000) // 100ms
                }
                guard let files = try? FileManager.default.contentsOfDirectory(atPath: dir) else { continue }
                let jsonlFiles = Set(files.filter { $0.hasSuffix(".jsonl") && !$0.contains("compact") })
                let newFiles = jsonlFiles.subtracting(known)

                for filename in newFiles {
                    // Read meta.json to verify match
                    let agentId = String(filename.dropFirst(6).dropLast(6)) // "agent-{id}.jsonl" → "{id}"
                    let metaPath = "\(dir)/agent-\(agentId).meta.json"
                    guard let metaData = FileManager.default.contents(atPath: metaPath),
                          let meta = try? JSONSerialization.jsonObject(with: metaData) as? [String: Any] else { continue }

                    let agentType = meta["agentType"] as? String ?? "unknown"
                    let metaDesc = meta["description"] as? String ?? ""

                    let info = SubagentInfo(
                        agentId: agentId, agentType: agentType,
                        description: metaDesc, parentToolUseId: toolUseId
                    )

                    await capturedSelf.beginWatchingSubagent(
                        displayId: displayId, agentId: agentId,
                        jsonlPath: "\(dir)/\(filename)", info: info
                    )
                    return
                }
            }
            print("[subagent] No new subagent JSONL found for tool_use \(toolUseId)")
        }
    }

    private func beginWatchingSubagent(displayId: String, agentId: String, jsonlPath: String, info: SubagentInfo) {
        guard subagentWatchers[agentId] == nil else { return }

        // Track as known
        var known = knownSubagentFiles[displayId] ?? []
        known.insert(URL(fileURLWithPath: jsonlPath).lastPathComponent)
        knownSubagentFiles[displayId] = known

        activeSubagents[agentId] = (displayId: displayId, info: info)

        let watcher = FileWatcher(path: jsonlPath, sessionDisplayId: displayId) { [weak self] sessId, event in
            guard let self else { return }
            Task { await self.handleSubagentEvent(agentId: agentId, event: event) }
        }
        subagentWatchers[agentId] = watcher
        watcher.start()
        print("[subagent] Watching \(info.agentType) agent \(agentId) for session \(displayId)")
    }

    private func handleSubagentEvent(agentId: String, event: SessionEvent) {
        guard let tracked = activeSubagents[agentId] else { return }

        // Tag event with subagent info
        let tagged = SessionEvent(
            uuid: "sa-\(agentId.prefix(8))-\(event.uuid)",
            timestamp: event.timestamp, type: event.type,
            text: event.text, thinking: event.thinking,
            toolUse: event.toolUse, toolResults: event.toolResults,
            model: event.model, durationMs: event.durationMs,
            subagentInfo: tracked.info
        )

        // Stop watching on system/turn_duration (subagent finished)
        if event.type == .system && event.durationMs != nil {
            print("[subagent] Agent \(agentId) finished (turn_duration)")
            subagentWatchers[agentId]?.stop()
            subagentWatchers.removeValue(forKey: agentId)
            activeSubagents.removeValue(forKey: agentId)
        }

        onEvent?(tracked.displayId, tagged)
    }
}

/// Watches a single file for appended lines using polling
final class FileWatcher: @unchecked Sendable {
    private let path: String
    private let sessionDisplayId: String
    private let onEvent: @Sendable (String, SessionEvent) -> Void
    private var offset: UInt64 = 0
    private var timer: DispatchSourceTimer?
    private let queue = DispatchQueue(label: "jsonl-watcher")
    /// Track enqueued message texts to dedup against the real user event that follows dequeue
    private var enqueuedTexts: Set<String> = []
    /// Last emitted synthetic queue event, so we can re-emit with corrected timestamp on remove
    private var lastSyntheticEvent: SessionEvent?

    init(path: String, sessionDisplayId: String, onEvent: @escaping @Sendable (String, SessionEvent) -> Void) {
        self.path = path
        self.sessionDisplayId = sessionDisplayId
        self.onEvent = onEvent
    }

    func start() {
        // Seek to end of file
        if let attrs = try? FileManager.default.attributesOfItem(atPath: path),
           let size = attrs[.size] as? UInt64 {
            offset = size
        }

        let timer = DispatchSource.makeTimerSource(queue: queue)
        timer.schedule(deadline: .now() + 0.5, repeating: 0.5)
        timer.setEventHandler { [weak self] in
            self?.checkForNewLines()
        }
        timer.resume()
        self.timer = timer
    }

    func stop() {
        timer?.cancel()
        timer = nil
    }

    private func checkForNewLines() {
        guard let handle = FileHandle(forReadingAtPath: path) else { return }
        defer { handle.closeFile() }

        handle.seek(toFileOffset: offset)
        let data = handle.readDataToEndOfFile()
        guard !data.isEmpty else { return }

        offset += UInt64(data.count)

        guard let text = String(data: data, encoding: .utf8) else { return }
        let lines = text.components(separatedBy: "\n").filter { !$0.isEmpty }

        for line in lines {
            // Pre-parse to track queue-operation state for dedup
            if let lineData = line.data(using: .utf8),
               let json = try? JSONSerialization.jsonObject(with: lineData) as? [String: Any],
               json["type"] as? String == "queue-operation" {
                let op = json["operation"] as? String ?? ""
                if op == "enqueue", let content = json["content"] as? String {
                    enqueuedTexts.insert(content.trimmingCharacters(in: .whitespacesAndNewlines))
                } else if op == "remove" || op == "dequeue" {
                    // Re-emit synthetic event with remove timestamp for correct positioning
                    if let synthetic = lastSyntheticEvent,
                       let ts = json["timestamp"] as? String {
                        let repositioned = SessionEvent(
                            uuid: synthetic.uuid, timestamp: ts,
                            type: synthetic.type, text: synthetic.text, thinking: synthetic.thinking,
                            toolUse: synthetic.toolUse, toolResults: synthetic.toolResults,
                            model: synthetic.model, durationMs: synthetic.durationMs
                        )
                        print("[watcher] Repositioning queue event to remove time: \(ts)")
                        onEvent(sessionDisplayId, repositioned)
                        lastSyntheticEvent = nil
                    }
                    enqueuedTexts.removeAll()
                }
            }

            if let event = JSONLParser.parseEvent(line: line) {
                // Defer synthetic queue events — they'll be emitted with correct timestamp on remove
                if event.type == .user, event.uuid.hasPrefix("q-") {
                    lastSyntheticEvent = event
                    print("[watcher] Deferring queue event until remove: \(event.text?.prefix(40) ?? "nil")")
                    continue
                }
                // Skip real user events that duplicate an enqueued mid-turn message
                if event.type == .user, let eventText = event.text {
                    let trimmed = eventText.trimmingCharacters(in: .whitespacesAndNewlines)
                    if enqueuedTexts.contains(trimmed) {
                        enqueuedTexts.remove(trimmed)
                        print("[watcher] Skipping duplicate user event (already emitted via queue-operation): \(trimmed.prefix(40))")
                        continue
                    }
                }
                print("[watcher] Parsed event: type=\(event.type.rawValue) text=\(event.text?.prefix(40) ?? "nil")")
                onEvent(sessionDisplayId, event)
            }
        }
    }
}
