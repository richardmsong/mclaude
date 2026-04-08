import Foundation

actor TmuxMonitor {
    private let sessionsDir: String
    private let tmuxTarget: String
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
        tmuxTarget: String = "0"
    ) {
        self.sessionsDir = sessionsDir
        self.tmuxTarget = tmuxTarget
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

    func getSessions(jsonlIdleSince: [String: Date] = [:], jsonlWorking: Set<String> = []) async -> [ClaudeSession] {
        let windows = await getTmuxWindows()
        let sessionFiles = loadSessionFiles()

        // Build list of valid windows with their session files
        struct WindowInfo {
            let window: TmuxWindow
            let sessionFile: ClaudeSessionFile
        }
        let validWindows = windows.compactMap { window -> WindowInfo? in
            guard let pid = window.pid, let sf = sessionFiles[pid] else { return nil }
            return WindowInfo(window: window, sessionFile: sf)
        }

        // Capture all panes in parallel (80 lines for broadcast, lightweight)
        let captures: [(Int, String)] = await withTaskGroup(of: (Int, String).self) { group in
            for info in validWindows {
                group.addTask { [self] in
                    let content = await self.capturePaneContent(window: info.window.index, lines: 80)
                    return (info.window.index, content)
                }
            }
            var results: [(Int, String)] = []
            for await result in group { results.append(result) }
            return results
        }
        let captureMap = Dictionary(captures, uniquingKeysWith: { _, last in last })

        var sessions: [ClaudeSession] = []

        for info in validWindows {
            let paneContent = captureMap[info.window.index] ?? ""
            var status = detectStatus(from: paneContent)
            let prompt = detectPrompt(from: paneContent)
            let projectName = extractProjectName(from: info.sessionFile.cwd)
            let id = "\(info.window.index)"

            // JSONL working state is more reliable than pane spinner detection
            if jsonlWorking.contains(id) && status == .idle {
                status = .working
            }

            let previousStatus = previousStatuses[id]
            if previousStatus != status {
                statusTimestamps[id] = Date()
                if let prev = previousStatus {
                    // Defer onStatusChange until after session is built
                }
            }
            previousStatuses[id] = status

            // For idle sessions, prefer JSONL turn_duration timestamp over in-memory statusTimestamps
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

            sessions.append(session)

            // Check output change using same capture (no second tmux call)
            let hash = String(paneContent.hashValue)
            if outputCache[id] != hash {
                outputCache[id] = hash
                outputContent[id] = paneContent
                onOutputChanged?(id, paneContent)
            }
        }

        // Only broadcast sessions if they changed (compare ids + statuses)
        let sessionsHash = sessions.map { "\($0.id):\($0.status.rawValue):\($0.prompt?.question ?? "")" }.joined().hashValue
        let oldHash = cachedSessionsHash
        cachedSessions = sessions
        cachedSessionsHash = sessionsHash
        if sessionsHash != oldHash {
            onSessionsUpdated?(sessions)
        }

        return sessions
    }

    func getCachedSessions() -> [ClaudeSession] {
        return cachedSessions
    }

    func getCachedOutput(id: String) -> String? {
        return outputContent[id]
    }

    func getMoreOutput(id: String, lines: Int) async -> String? {
        guard let session = cachedSessions.first(where: { $0.id == id }) else { return nil }
        return await capturePaneContent(window: session.tmuxWindow, lines: lines)
    }

    func getSession(id: String) async -> ClaudeSession? {
        // Try cache first
        if let cached = cachedSessions.first(where: { $0.id == id }) {
            return cached
        }
        let sessions = await getSessions()
        return sessions.first { $0.id == id }
    }

    func getPaneContent(window: Int, lines: Int = 200) async -> String {
        return await capturePaneContent(window: window, lines: lines)
    }

    func sendKeys(window: Int, keys: String) async -> Bool {
        return await runTmux(["send-keys", "-t", "\(tmuxTarget):\(window)", keys])
    }

    func sendEnter(window: Int) async -> Bool {
        return await runTmux(["send-keys", "-t", "\(tmuxTarget):\(window)", "Enter"])
    }

    func createSession(cwd: String, token: String? = nil) async -> String? {
        // Run claude directly as the window command — window auto-closes when claude exits
        let projectName = (cwd as NSString).lastPathComponent
        var args = ["new-window", "-t", "\(tmuxTarget):", "-n", projectName, "-c", cwd]

        // Set CLAUDE_CODE_OAUTH_TOKEN env var for per-user auth
        if let token {
            args += ["-e", "CLAUDE_CODE_OAUTH_TOKEN=\(token)"]
        }

        args += ["-P", "-F", "#{window_index}",
                 "claude", "--dangerously-skip-permissions"]

        let output = await runTmuxOutput(args)
        let windowIndex = output.trimmingCharacters(in: .whitespacesAndNewlines)
        guard Int(windowIndex) != nil else { return nil }
        return windowIndex
    }

    // MARK: - Private

    private struct TmuxWindow {
        let index: Int
        let pid: Int?
        let command: String
        let path: String
    }

    private func getTmuxWindows() async -> [TmuxWindow] {
        let output = await runTmuxOutput([
            "list-panes", "-s", "-t", tmuxTarget,
            "-F", "#{window_index} #{pane_pid} #{pane_current_command} #{pane_current_path}"
        ])

        // Build maps: PPID -> claude PID, and claude PID set (for direct-run detection)
        let (claudeByParent, claudePids) = await getClaudeProcessMap()

        return output.components(separatedBy: "\n").compactMap { line in
            let parts = line.split(separator: " ", maxSplits: 3).map(String.init)
            guard parts.count >= 3 else { return nil }
            let panePid = Int(parts[1]) ?? 0
            // Check if claude is a child of the pane shell, or IS the pane process directly
            let claudePid = claudeByParent[panePid] ?? (claudePids.contains(panePid) ? panePid : nil)
            return TmuxWindow(
                index: Int(parts[0]) ?? 0,
                pid: claudePid,
                command: parts[2],
                path: parts.count > 3 ? parts[3] : ""
            )
        }
    }

    /// Scans all running claude processes and returns (PPID -> claude PID map, set of all claude PIDs)
    private func getClaudeProcessMap() async -> (byParent: [Int: Int], pids: Set<Int>) {
        let output = await Shell.exec("/bin/ps", arguments: ["-eo", "pid,ppid,args"])

        var byParent: [Int: Int] = [:]
        var pids: Set<Int> = []
        for line in output.components(separatedBy: "\n") {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            let parts = trimmed.split(separator: " ", maxSplits: 2).map(String.init)
            guard parts.count >= 3 else { continue }
            // Match lines where the command is the claude binary (not claude-*, mclaude, etc.)
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

    private func capturePaneContent(window: Int, lines: Int = 200) async -> String {
        return await runTmuxOutput([
            "capture-pane", "-t", "\(tmuxTarget):\(window)", "-p", "-e",
            "-S", "-\(lines)"
        ])
    }

    /// Strip ANSI escape sequences from text
    private func stripANSI(_ text: String) -> String {
        // Matches ESC[ ... m (SGR), ESC[ ... letter (CSI), and OSC sequences
        guard let regex = try? NSRegularExpression(pattern: "\u{1b}\\[[0-9;]*[a-zA-Z]|\u{1b}\\][^\u{07}]*\u{07}", options: []) else {
            return text
        }
        return regex.stringByReplacingMatches(in: text, range: NSRange(text.startIndex..., in: text), withTemplate: "")
    }

    func detectPrompt(from content: String) -> DetectedPrompt? {
        let lines = content.components(separatedBy: "\n")
        let cleaned = lines.map { stripANSI($0) }

        // Look for a question line starting with "?" in the last 20 lines
        let tail = Array(cleaned.suffix(20))
        guard let questionIdx = tail.lastIndex(where: { $0.trimmingCharacters(in: .whitespaces).hasPrefix("?") }) else {
            return nil
        }

        let questionLine = tail[questionIdx].trimmingCharacters(in: .whitespaces)
        // Strip leading "? " prefix
        let question = String(questionLine.dropFirst(1)).trimmingCharacters(in: .whitespaces)
        guard !question.isEmpty else { return nil }

        // Collect numbered options after the question line
        var options: [String] = []
        for i in (questionIdx + 1)..<tail.count {
            let line = tail[i].trimmingCharacters(in: .whitespaces)
            // Match lines like "1. Option A" or with leading marker (arrow/highlight)
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
        // Check the very last few lines for the idle prompt — this is definitive
        let bottomClean = stripANSI(lines.suffix(5).joined(separator: "\n"))

        // If the input prompt is visible at the bottom, Claude is idle
        // (regardless of spinner colors in scrollback above)
        let idlePrompts = ["bypass permissions", "shift+tab to cycle", "tab to cycle"]
        let isAtPrompt = idlePrompts.contains { bottomClean.contains($0) }

        // Check for plan mode
        if cleanTail.contains("plan and is ready to execute") ||
           cleanTail.contains("Plan is ready") ||
           cleanTail.contains("execute this plan") {
            return .planMode
        }

        // Check for permission prompts
        if cleanTail.contains("Do you want to") ||
           cleanTail.contains("Allow this") ||
           cleanTail.contains("Approve?") ||
           cleanTail.contains("(y/n)") ||
           cleanTail.contains("(Y/n)") {
            return .needsPermission
        }

        // Detect working BEFORE idle check — spinner colors appear even when
        // the status bar (which contains "bypass permissions") is visible
        let spinnerColors = ["174", "216", "180", "210"]
        for color in spinnerColors {
            if rawTail.contains("\u{1b}[38;5;\(color)m") {
                return .working
            }
        }

        // Fallback: check for working indicator text patterns in clean output
        let workingPatterns = ["Running…", "Ideating…", "Caramelizing…", "Thinking…",
                               "Brewing…", "Generating…", "Analyzing…", "Processing…",
                               "Compacting conversation"]
        for pattern in workingPatterns {
            if cleanTail.contains(pattern) {
                return .working
            }
        }

        // If at the prompt and no working indicators, it's idle
        if isAtPrompt {
            return .idle
        }

        // No prompt, no spinner — likely working (output streaming)
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

    /// Runs a process off the actor's thread to avoid blocking
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
    /// Synchronous process execution — must be called off the main/actor thread
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

        // Read ALL data before waitUntilExit to avoid pipe buffer deadlock
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
