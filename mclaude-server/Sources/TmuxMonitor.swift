import Foundation

actor TmuxMonitor {
    private let sessionsDir: String
    private let defaultTmuxSession: String
    /// All tmux sessions we're monitoring. The default is always included.
    private var monitoredSessions: Set<String>
    private var previousStatuses: [String: SessionStatus] = [:]
    private var statusTimestamps: [String: Date] = [:]
    private(set) var cachedSessions: [ClaudeSession] = []
    private var cachedSessionsHash: Int = 0
    private var outputCache: [String: String] = [:]  // id -> last output hash
    private var outputContent: [String: String] = [:] // id -> full output

    private var onStatusChange: (@Sendable (ClaudeSession, SessionStatus, SessionStatus) -> Void)?
    private var onSessionsUpdated: (@Sendable ([ClaudeSession]) -> Void)?
    private var onOutputChanged: (@Sendable (String, String) -> Void)? // id, content

    init(
        sessionsDir: String = "\(NSHomeDirectory())/.claude/sessions",
        tmuxTarget: String = "mclaude"
    ) {
        self.sessionsDir = sessionsDir
        self.defaultTmuxSession = tmuxTarget
        self.monitoredSessions = [tmuxTarget]
    }

    func setOnStatusChange(_ handler: @escaping @Sendable (ClaudeSession, SessionStatus, SessionStatus) -> Void) {
        self.onStatusChange = handler
    }

    func setOnSessionsUpdated(_ handler: @escaping @Sendable ([ClaudeSession]) -> Void) {
        self.onSessionsUpdated = handler
    }

    func setOnOutputChanged(_ handler: @escaping @Sendable (String, String) -> Void) {
        self.onOutputChanged = handler
    }

    /// Sync monitoredSessions to only include sessions that actually exist in tmux.
    /// Removes stale entries (avoids tmux falling back to the attached session for missing names).
    private func discoverTmuxSessions() async {
        let output = await runTmuxOutput(["list-sessions", "-F", "#{session_name}"])
        let existing = Set(output.components(separatedBy: "\n").compactMap { line -> String? in
            let name = line.trimmingCharacters(in: .whitespacesAndNewlines)
            return name.isEmpty ? nil : name
        })
        // Add newly discovered sessions
        for name in existing { monitoredSessions.insert(name) }
        // Remove sessions that no longer exist (prevents tmux fallback to attached session)
        monitoredSessions = monitoredSessions.filter { existing.contains($0) }
    }

    /// Ensure a tmux session exists, creating it if needed. Adds it to the monitored set.
    func ensureTmuxSession(_ name: String) async {
        monitoredSessions.insert(name)
        let exists = await runTmux(["has-session", "-t", name])
        if !exists {
            _ = await runTmux(["new-session", "-d", "-s", name, "-n", "harness"])
        }
    }

    /// Build a composite session ID: "tmuxSession:windowIndex"
    private func compositeId(tmuxSession: String, windowIndex: Int) -> String {
        if tmuxSession == defaultTmuxSession {
            return "\(windowIndex)"  // backward compatible for default session
        }
        return "\(tmuxSession):\(windowIndex)"
    }

    /// Parse a composite ID back to (tmuxSession, windowIndex)
    private func parseId(_ id: String) -> (String, Int)? {
        if let colonIdx = id.firstIndex(of: ":") {
            let session = String(id[id.startIndex..<colonIdx])
            let window = Int(id[id.index(after: colonIdx)...])
            return window.map { (session, $0) }
        }
        // Legacy: plain window index = default session
        if let window = Int(id) {
            return (defaultTmuxSession, window)
        }
        return nil
    }

    func getSessions(jsonlIdleSince: [String: Date] = [:], jsonlWorking: Set<String> = []) async -> [ClaudeSession] {
        // Auto-discover all tmux sessions so we never miss one after a server restart
        await discoverTmuxSessions()

        let sessionFiles = loadSessionFiles()
        var allSessions: [ClaudeSession] = []

        // Scan all monitored tmux sessions
        for tmuxSessionName in monitoredSessions {
            let windows = await getTmuxWindows(in: tmuxSessionName)

            struct WindowInfo {
                let window: TmuxWindow
                let sessionFile: ClaudeSessionFile
                let tmuxSession: String
            }
            let validWindows = windows.compactMap { window -> WindowInfo? in
                guard let pid = window.pid, let sf = sessionFiles[pid] else { return nil }
                return WindowInfo(window: window, sessionFile: sf, tmuxSession: tmuxSessionName)
            }

            // Capture all panes in parallel
            let captures: [(Int, String)] = await withTaskGroup(of: (Int, String).self) { group in
                for info in validWindows {
                    group.addTask { [self] in
                        let content = await self.capturePaneContent(tmuxSession: info.tmuxSession, window: info.window.index, lines: 80)
                        return (info.window.index, content)
                    }
                }
                var results: [(Int, String)] = []
                for await result in group { results.append(result) }
                return results
            }
            let captureMap = Dictionary(captures, uniquingKeysWith: { _, last in last })

            for info in validWindows {
                let paneContent = captureMap[info.window.index] ?? ""
                var status = detectStatus(from: paneContent)
                let prompt = detectPrompt(from: paneContent)
                let projectName = extractProjectName(from: info.sessionFile.cwd)
                let id = compositeId(tmuxSession: tmuxSessionName, windowIndex: info.window.index)

                if jsonlWorking.contains(id) && status == .idle {
                    status = .working
                }

                let previousStatus = previousStatuses[id]
                if previousStatus != status {
                    statusTimestamps[id] = Date()
                }
                previousStatuses[id] = status

                let effectiveStatusSince: Date?
                if status == .idle, let jsonlDate = jsonlIdleSince[id] {
                    effectiveStatusSince = jsonlDate
                } else {
                    effectiveStatusSince = statusTimestamps[id]
                }

                let session = ClaudeSession(
                    id: id,
                    pid: info.window.pid!,
                    sessionId: info.sessionFile.sessionId,
                    cwd: info.sessionFile.cwd,
                    startedAt: Date(timeIntervalSince1970: info.sessionFile.startedAt / 1000),
                    tmuxWindow: info.window.index,
                    tmuxSession: tmuxSessionName,
                    windowName: info.window.name,
                    status: status,
                    statusSince: effectiveStatusSince,
                    projectName: projectName,
                    lastOutput: "",
                    prompt: prompt
                )

                if previousStatus != status {
                    if let prev = previousStatus {
                        onStatusChange?(session, prev, status)
                    }
                }

                allSessions.append(session)

                let hash = String(paneContent.hashValue)
                if outputCache[id] != hash {
                    outputCache[id] = hash
                    outputContent[id] = paneContent
                    onOutputChanged?(id, paneContent)
                }
            }
        }

        let sessionsHash = allSessions.map { "\($0.id):\($0.status.rawValue):\($0.prompt?.question ?? "")" }.joined().hashValue
        let oldHash = cachedSessionsHash
        cachedSessions = allSessions
        cachedSessionsHash = sessionsHash
        if sessionsHash != oldHash {
            onSessionsUpdated?(allSessions)
        }

        return allSessions
    }

    func getCachedSessions() -> [ClaudeSession] {
        return cachedSessions
    }

    func getCachedOutput(id: String) -> String? {
        return outputContent[id]
    }

    func getMoreOutput(id: String, lines: Int) async -> String? {
        guard let (tmuxSession, window) = parseId(id) else { return nil }
        return await capturePaneContent(tmuxSession: tmuxSession, window: window, lines: lines)
    }

    func getSession(id: String) async -> ClaudeSession? {
        if let cached = cachedSessions.first(where: { $0.id == id }) {
            return cached
        }
        let sessions = await getSessions()
        return sessions.first { $0.id == id }
    }

    func getPaneContent(window: Int, lines: Int = 200) async -> String {
        return await capturePaneContent(tmuxSession: defaultTmuxSession, window: window, lines: lines)
    }

    func sendKeys(window: Int, keys: String) async -> Bool {
        return await runTmux(["send-keys", "-t", "\(defaultTmuxSession):\(window)", keys])
    }

    func sendKeysToSession(id: String, keys: String) async -> Bool {
        guard let (tmuxSession, window) = parseId(id) else { return false }
        return await runTmux(["send-keys", "-t", "\(tmuxSession):\(window)", keys])
    }

    func sendEnter(window: Int) async -> Bool {
        return await runTmux(["send-keys", "-t", "\(defaultTmuxSession):\(window)", "Enter"])
    }

    func sendEnterToSession(id: String) async -> Bool {
        guard let (tmuxSession, window) = parseId(id) else { return false }
        return await runTmux(["send-keys", "-t", "\(tmuxSession):\(window)", "Enter"])
    }

    func createSession(cwd: String, token: String? = nil, tmuxSession: String? = nil, windowName: String? = nil) async -> String? {
        let targetSession = tmuxSession ?? defaultTmuxSession

        // Ensure the target tmux session exists
        await ensureTmuxSession(targetSession)

        let name = windowName ?? (cwd as NSString).lastPathComponent
        var args = ["new-window", "-t", "\(targetSession):", "-n", name, "-c", cwd]

        if let token {
            args += ["-e", "CLAUDE_CODE_OAUTH_TOKEN=\(token)"]
        }

        // Resolve claude binary from PATH
        let whichResult = await shellExec("/usr/bin/which", args: ["claude"])
        let resolved = whichResult.1.trimmingCharacters(in: .whitespacesAndNewlines)
        let claudePath: String
        if !resolved.isEmpty {
            claudePath = resolved
        } else {
            let candidates = ["/opt/homebrew/bin/claude", "/usr/local/bin/claude", "\(NSHomeDirectory())/.local/bin/claude"]
            claudePath = candidates.first { FileManager.default.fileExists(atPath: $0) } ?? "claude"
        }
        args += ["-P", "-F", "#{window_index}", claudePath, "--dangerously-skip-permissions"]

        let output = await runTmuxOutput(args)
        let windowIndex = output.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let idx = Int(windowIndex) else { return nil }
        return compositeId(tmuxSession: targetSession, windowIndex: idx)
    }

    /// Get the list of monitored tmux sessions
    func getMonitoredSessions() -> [String] {
        return Array(monitoredSessions).sorted()
    }

    // MARK: - Private

    private struct TmuxWindow {
        let index: Int
        let name: String
        let pid: Int?
        let command: String
        let path: String
    }

    private func getTmuxWindows(in tmuxSession: String) async -> [TmuxWindow] {
        let output = await runTmuxOutput([
            "list-panes", "-s", "-t", tmuxSession,
            "-F", "#{window_index} #{window_name} #{pane_pid} #{pane_current_command} #{pane_current_path}"
        ])

        let (claudeByParent, claudePids) = await getClaudeProcessMap()

        return output.components(separatedBy: "\n").compactMap { line in
            let parts = line.split(separator: " ", maxSplits: 4).map(String.init)
            guard parts.count >= 4 else { return nil }
            let panePid = Int(parts[2]) ?? 0
            let claudePid = claudeByParent[panePid] ?? (claudePids.contains(panePid) ? panePid : nil)
            return TmuxWindow(
                index: Int(parts[0]) ?? 0,
                name: parts[1],
                pid: claudePid,
                command: parts[3],
                path: parts.count > 4 ? parts[4] : ""
            )
        }
    }

    private func getClaudeProcessMap() async -> (byParent: [Int: Int], pids: Set<Int>) {
        let output = await Shell.exec("/bin/ps", arguments: ["-eo", "pid,ppid,args"])

        var byParent: [Int: Int] = [:]
        var pids: Set<Int> = []
        for line in output.components(separatedBy: "\n") {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            let parts = trimmed.split(separator: " ", maxSplits: 2).map(String.init)
            guard parts.count >= 3 else { continue }
            let args = parts[2]
            let cmd = (args as NSString).lastPathComponent
            let cmdName = cmd.split(separator: " ", maxSplits: 1).first.map(String.init) ?? cmd
            guard (cmdName == "claude") && !args.contains("mclaude") else { continue }
            if let pid = Int(parts[0]), let ppid = Int(parts[1]) {
                byParent[ppid] = pid
                pids.insert(pid)
            }
        }
        return (byParent, pids)
    }

    private func loadSessionFiles() -> [Int: ClaudeSessionFile] {
        var result: [Int: ClaudeSessionFile] = [:]
        let fm = FileManager.default
        guard let files = try? fm.contentsOfDirectory(atPath: sessionsDir) else { return result }

        let decoder = JSONDecoder()
        for file in files where file.hasSuffix(".json") {
            let path = "\(sessionsDir)/\(file)"
            guard let data = fm.contents(atPath: path),
                  let session = try? decoder.decode(ClaudeSessionFile.self, from: data) else { continue }
            result[session.pid] = session
        }
        return result
    }

    private func capturePaneContent(tmuxSession: String, window: Int, lines: Int = 200) async -> String {
        return await runTmuxOutput([
            "capture-pane", "-t", "\(tmuxSession):\(window)", "-p", "-e",
            "-S", "-\(lines)"
        ])
    }

    private func stripANSI(_ text: String) -> String {
        guard let regex = try? NSRegularExpression(pattern: "\u{1b}\\[[0-9;]*[a-zA-Z]|\u{1b}\\][^\u{07}]*\u{07}", options: []) else {
            return text
        }
        return regex.stringByReplacingMatches(in: text, range: NSRange(text.startIndex..., in: text), withTemplate: "")
    }

    func detectPrompt(from content: String) -> DetectedPrompt? {
        let lines = content.components(separatedBy: "\n")
        let cleaned = lines.map { stripANSI($0) }

        let tail = Array(cleaned.suffix(20))
        guard let questionIdx = tail.lastIndex(where: { $0.trimmingCharacters(in: .whitespaces).hasPrefix("?") }) else {
            return nil
        }

        let questionLine = tail[questionIdx].trimmingCharacters(in: .whitespaces)
        let question = String(questionLine.dropFirst(1)).trimmingCharacters(in: .whitespaces)
        guard !question.isEmpty else { return nil }

        var options: [String] = []
        for i in (questionIdx + 1)..<tail.count {
            let line = tail[i].trimmingCharacters(in: .whitespaces)
            if let match = line.range(of: #"^\d+\.\s+"#, options: .regularExpression) {
                let optionText = String(line[match.upperBound...])
                options.append(optionText)
            } else if line.isEmpty {
                continue
            } else {
                break
            }
        }

        return DetectedPrompt(
            question: question,
            options: options.isEmpty ? nil : options
        )
    }

    func detectStatus(from content: String) -> SessionStatus {
        let lines = content.components(separatedBy: "\n")
        let rawTail = lines.suffix(20).joined(separator: "\n")
        let cleanTail = stripANSI(rawTail)
        let bottomClean = stripANSI(lines.suffix(5).joined(separator: "\n"))

        let idlePrompts = ["bypass permissions", "shift+tab to cycle", "tab to cycle"]
        let isAtPrompt = idlePrompts.contains { bottomClean.contains($0) }

        if cleanTail.contains("plan and is ready to execute") ||
           cleanTail.contains("Plan is ready") ||
           cleanTail.contains("execute this plan") {
            return .planMode
        }

        if cleanTail.contains("Do you want to") ||
           cleanTail.contains("Allow this") ||
           cleanTail.contains("Approve?") ||
           cleanTail.contains("(y/n)") ||
           cleanTail.contains("(Y/n)") {
            return .needsPermission
        }

        let spinnerColors = ["174", "216", "180", "210"]
        for color in spinnerColors {
            if rawTail.contains("\u{1b}[38;5;\(color)m") {
                return .working
            }
        }

        let workingPatterns = ["Running…", "Ideating…", "Caramelizing…", "Thinking…",
                               "Brewing…", "Generating…", "Analyzing…", "Processing…",
                               "Compacting conversation"]
        for pattern in workingPatterns {
            if cleanTail.contains(pattern) {
                return .working
            }
        }

        if isAtPrompt {
            return .idle
        }

        return .working
    }

    private func extractProjectName(from cwd: String) -> String {
        return (cwd as NSString).lastPathComponent
    }

    @discardableResult
    private func runTmux(_ args: [String]) async -> Bool {
        return await shellExec("/opt/homebrew/bin/tmux", args: args).0
    }

    private func runTmuxOutput(_ args: [String]) async -> String {
        return await shellExec("/opt/homebrew/bin/tmux", args: args).1
    }

    private nonisolated func shellExec(_ path: String, args: [String]) async -> (Bool, String) {
        await withCheckedContinuation { continuation in
            DispatchQueue.global().async {
                let result = Shell.syncExec(path, arguments: args)
                continuation.resume(returning: result)
            }
        }
    }
}

// Helper to run a process and capture output
enum Shell {
    static func syncExec(_ path: String, arguments: [String]) -> (Bool, String) {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: path)
        process.arguments = arguments
        let pipe = Pipe()
        let errPipe = Pipe()
        process.standardOutput = pipe
        process.standardError = errPipe

        do {
            try process.run()
        } catch {
            return (false, "")
        }

        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        let errData = errPipe.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()

        let output = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .newlines) ?? ""
        let stderr = String(data: errData, encoding: .utf8)?.trimmingCharacters(in: .newlines) ?? ""
        if !stderr.isEmpty {
            print("[shell] stderr: \(stderr)")
        }
        return (process.terminationStatus == 0, output)
    }

    static func exec(_ path: String, arguments: [String]) async -> String {
        await withCheckedContinuation { continuation in
            DispatchQueue.global().async {
                let (_, output) = syncExec(path, arguments: arguments)
                continuation.resume(returning: output)
            }
        }
    }
}
