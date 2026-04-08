import Foundation

/// Manages Claude Code sessions running as Kubernetes pods.
/// Each pod runs claude inside tmux, interacted with via `kubectl exec`.
actor K8sSessionManager {
    private let namespace: String
    private let image: String
    private var cachedSessions: [ClaudeSession] = []
    private var cachedSessionsHash: Int = 0
    private var outputCache: [String: String] = [:]
    private var outputContent: [String: String] = [:]
    private var previousStatuses: [String: SessionStatus] = [:]
    private var statusTimestamps: [String: Date] = [:]

    private var onSessionsUpdated: (@Sendable ([ClaudeSession]) -> Void)?
    private var onOutputChanged: (@Sendable (String, String) -> Void)?
    private var onStatusChange: (@Sendable (ClaudeSession, SessionStatus, SessionStatus) -> Void)?

    init(namespace: String = "mclaude", image: String = "mclaude-session:latest") {
        self.namespace = namespace
        self.image = image
    }

    func setOnSessionsUpdated(_ handler: @escaping @Sendable ([ClaudeSession]) -> Void) {
        self.onSessionsUpdated = handler
    }

    func setOnOutputChanged(_ handler: @escaping @Sendable (String, String) -> Void) {
        self.onOutputChanged = handler
    }

    func setOnStatusChange(_ handler: @escaping @Sendable (ClaudeSession, SessionStatus, SessionStatus) -> Void) {
        self.onStatusChange = handler
    }

    // MARK: - Session lifecycle

    func createSession(cwd: String, token: String?, projectName: String? = nil) async -> String? {
        let name = projectName ?? (cwd as NSString).lastPathComponent
        let safeName = name.lowercased().replacingOccurrences(of: "[^a-z0-9-]", with: "-", options: .regularExpression)
        let podName = "claude-\(safeName)-\(Int(Date().timeIntervalSince1970) % 100000)"
        let pvcName = "repo-\(safeName)"
        let worktreeId = "\(Int(Date().timeIntervalSince1970))"

        // Detect git remote from host directory
        let gitURL = await detectGitRemote(cwd: cwd)

        // Ensure PVC exists for this project
        if gitURL != nil {
            await ensurePVC(name: pvcName)
        }

        // Build env vars
        var envYaml = """
            - name: CLAUDE_CODE_OAUTH_TOKEN
              value: "\(token ?? "")"
            - name: WORKTREE_ID
              value: "\(worktreeId)"
        """
        if let gitURL {
            envYaml += """

            - name: GIT_URL
              value: "\(gitURL)"
        """
        }

        // Build volume mounts and volumes
        let volumeYaml: String
        let mountYaml: String
        if gitURL != nil {
            volumeYaml = """
              volumes:
              - name: repo
                persistentVolumeClaim:
                  claimName: \(pvcName)
        """
            mountYaml = """
                volumeMounts:
                - name: repo
                  mountPath: /repo
        """
        } else {
            volumeYaml = ""
            mountYaml = ""
        }

        let manifest = """
        apiVersion: v1
        kind: Pod
        metadata:
          name: \(podName)
          namespace: \(namespace)
          labels:
            app: mclaude-session
            project: \(safeName)
        spec:
          restartPolicy: Never
        \(volumeYaml)
          containers:
          - name: claude
            image: \(image)
            imagePullPolicy: Never
            env:
        \(envYaml)
        \(mountYaml)
            resources:
              requests:
                memory: "256Mi"
                cpu: "250m"
              limits:
                memory: "1Gi"
                cpu: "1000m"
        """

        let tmpFile = "/tmp/mclaude-pod-\(podName).yaml"
        do {
            try manifest.write(toFile: tmpFile, atomically: true, encoding: .utf8)
        } catch {
            print("[k8s] Failed to write manifest: \(error)")
            return nil
        }

        let (success, output) = await kubectl(["apply", "-f", tmpFile])
        try? FileManager.default.removeItem(atPath: tmpFile)

        guard success else {
            print("[k8s] Failed to create pod: \(output)")
            return nil
        }

        for _ in 0..<60 {
            try? await Task.sleep(nanoseconds: 1_000_000_000)
            let (_, status) = await kubectl(["get", "pod", podName, "-n", namespace, "-o", "jsonpath={.status.phase}"])
            if status.trimmingCharacters(in: .whitespacesAndNewlines) == "Running" {
                print("[k8s] Pod \(podName) is running (git: \(gitURL ?? "none"))")
                return podName
            }
        }

        print("[k8s] Pod \(podName) failed to start within 60s")
        return nil
    }

    /// Detect git remote URL from a host directory
    private nonisolated func detectGitRemote(cwd: String) async -> String? {
        let (success, output) = await withCheckedContinuation { continuation in
            DispatchQueue.global().async {
                let result = Shell.syncExec("/usr/bin/git", arguments: ["-C", cwd, "remote", "get-url", "origin"])
                continuation.resume(returning: result)
            }
        }
        guard success else { return nil }
        let url = output.trimmingCharacters(in: .whitespacesAndNewlines)
        return url.isEmpty ? nil : url
    }

    /// Ensure a PVC exists for the project repo
    private func ensurePVC(name: String) async {
        // Check if it exists
        let (exists, _) = await kubectl(["get", "pvc", name, "-n", namespace])
        if exists { return }

        let manifest = """
        apiVersion: v1
        kind: PersistentVolumeClaim
        metadata:
          name: \(name)
          namespace: \(namespace)
        spec:
          accessModes: [ReadWriteOnce]
          resources:
            requests:
              storage: 1Gi
        """

        let tmpFile = "/tmp/mclaude-pvc-\(name).yaml"
        try? manifest.write(toFile: tmpFile, atomically: true, encoding: .utf8)
        let (ok, out) = await kubectl(["apply", "-f", tmpFile])
        try? FileManager.default.removeItem(atPath: tmpFile)
        if ok {
            print("[k8s] PVC \(name) created")
        } else {
            print("[k8s] PVC \(name) failed: \(out)")
        }
    }

    // MARK: - Session interaction

    func sendKeys(podName: String, keys: String) async -> Bool {
        let (success, _) = await kubectl([
            "exec", podName, "-n", namespace, "--",
            "tmux", "send-keys", "-t", "claude", keys
        ])
        return success
    }

    func sendEnter(podName: String) async -> Bool {
        let (success, _) = await kubectl([
            "exec", podName, "-n", namespace, "--",
            "tmux", "send-keys", "-t", "claude", "Enter"
        ])
        return success
    }

    func capturePaneContent(podName: String, lines: Int = 200) async -> String {
        let (_, output) = await kubectl([
            "exec", podName, "-n", namespace, "--",
            "tmux", "capture-pane", "-t", "claude", "-p", "-e", "-S", "-\(lines)"
        ])
        return output
    }

    // MARK: - Session listing & polling

    func getSessions() async -> [ClaudeSession] {
        // List all mclaude-session pods
        let (success, output) = await kubectl([
            "get", "pods", "-n", namespace,
            "-l", "app=mclaude-session",
            "-o", "jsonpath={range .items[*]}{.metadata.name}|{.metadata.labels.project}|{.status.phase}|{.metadata.creationTimestamp}\\n{end}"
        ])

        guard success else { return cachedSessions }

        var sessions: [ClaudeSession] = []
        let lines = output.components(separatedBy: "\n").filter { !$0.isEmpty }

        // Capture output for all running pods in parallel
        let runningPods = lines.compactMap { line -> (String, String, String)? in
            let parts = line.split(separator: "|", maxSplits: 3).map(String.init)
            guard parts.count >= 4, parts[2] == "Running" else { return nil }
            return (parts[0], parts[1], parts[3])
        }

        let captures: [(String, String)] = await withTaskGroup(of: (String, String).self) { group in
            for (podName, _, _) in runningPods {
                group.addTask { [self] in
                    let content = await self.capturePaneContent(podName: podName, lines: 80)
                    return (podName, content)
                }
            }
            var results: [(String, String)] = []
            for await result in group { results.append(result) }
            return results
        }
        let captureMap = Dictionary(captures, uniquingKeysWith: { _, last in last })

        for (podName, project, createdAt) in runningPods {
            let paneContent = captureMap[podName] ?? ""
            let status = detectStatus(from: paneContent)
            let id = "k8s-\(podName)"

            let previousStatus = previousStatuses[id]
            if previousStatus != status {
                statusTimestamps[id] = Date()
                if let prev = previousStatus {
                    let session = ClaudeSession(
                        id: id, pid: 0, sessionId: podName, cwd: "/workspace",
                        startedAt: parseISO8601(createdAt), tmuxWindow: 0,
                        status: status, statusSince: statusTimestamps[id],
                        projectName: project, lastOutput: "", prompt: nil
                    )
                    onStatusChange?(session, prev, status)
                }
            }
            previousStatuses[id] = status

            let session = ClaudeSession(
                id: id, pid: 0, sessionId: podName, cwd: "/workspace",
                startedAt: parseISO8601(createdAt), tmuxWindow: 0,
                status: status, statusSince: statusTimestamps[id],
                projectName: project, lastOutput: "", prompt: nil
            )
            sessions.append(session)

            // Track output changes
            let hash = String(paneContent.hashValue)
            if outputCache[id] != hash {
                outputCache[id] = hash
                outputContent[id] = paneContent
                onOutputChanged?(id, paneContent)
            }
        }

        let sessionsHash = sessions.map { "\($0.id):\($0.status.rawValue)" }.joined().hashValue
        let oldHash = cachedSessionsHash
        cachedSessions = sessions
        cachedSessionsHash = sessionsHash
        if sessionsHash != oldHash {
            onSessionsUpdated?(sessions)
        }

        return sessions
    }

    func getCachedSessions() -> [ClaudeSession] { cachedSessions }
    func getCachedOutput(id: String) -> String? { outputContent[id] }

    func getSession(id: String) async -> ClaudeSession? {
        if let cached = cachedSessions.first(where: { $0.id == id }) { return cached }
        let sessions = await getSessions()
        return sessions.first { $0.id == id }
    }

    /// Extract the pod name from a k8s session ID (strip "k8s-" prefix)
    func podName(from sessionId: String) -> String {
        if sessionId.hasPrefix("k8s-") {
            return String(sessionId.dropFirst(4))
        }
        return sessionId
    }

    // MARK: - Status detection (same logic as TmuxMonitor)

    private func stripANSI(_ text: String) -> String {
        guard let regex = try? NSRegularExpression(pattern: "\u{1b}\\[[0-9;]*[a-zA-Z]|\u{1b}\\][^\u{07}]*\u{07}", options: []) else { return text }
        return regex.stringByReplacingMatches(in: text, range: NSRange(text.startIndex..., in: text), withTemplate: "")
    }

    private func detectStatus(from content: String) -> SessionStatus {
        let lines = content.components(separatedBy: "\n")
        let rawTail = lines.suffix(20).joined(separator: "\n")
        let cleanTail = stripANSI(rawTail)
        let bottomClean = stripANSI(lines.suffix(5).joined(separator: "\n"))

        let idlePrompts = ["bypass permissions", "shift+tab to cycle", "tab to cycle"]
        let isAtPrompt = idlePrompts.contains { bottomClean.contains($0) }

        if cleanTail.contains("plan and is ready to execute") || cleanTail.contains("Plan is ready") {
            return .planMode
        }
        if cleanTail.contains("Do you want to") || cleanTail.contains("Allow this") ||
           cleanTail.contains("Approve?") || cleanTail.contains("(y/n)") {
            return .needsPermission
        }

        let spinnerColors = ["174", "216", "180", "210"]
        for color in spinnerColors {
            if rawTail.contains("\u{1b}[38;5;\(color)m") { return .working }
        }

        let workingPatterns = ["Running…", "Ideating…", "Thinking…", "Brewing…", "Generating…",
                               "Analyzing…", "Processing…", "Compacting conversation"]
        for pattern in workingPatterns {
            if cleanTail.contains(pattern) { return .working }
        }

        if isAtPrompt { return .idle }
        return .working
    }

    private func parseISO8601(_ str: String) -> Date {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter.date(from: str) ?? Date()
    }

    // MARK: - kubectl helper

    private nonisolated func kubectl(_ args: [String]) async -> (Bool, String) {
        await withCheckedContinuation { continuation in
            DispatchQueue.global().async {
                let result = Shell.syncExec("/usr/local/bin/kubectl", arguments: args)
                continuation.resume(returning: result)
            }
        }
    }
}
