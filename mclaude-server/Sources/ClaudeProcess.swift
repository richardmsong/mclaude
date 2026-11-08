import Foundation

/// One headless Claude Code process speaking stream-json over stdio (the mclaude v2 session-agent
/// driver, ported). Claude owns the conversation; we only write JSON lines to stdin and read JSON
/// lines from stdout. Interactive tools (AskUserQuestion, ExitPlanMode) arrive as `can_use_tool`
/// control requests because of `--permission-prompt-tool stdio`. Ordinary tools run in auto mode
/// (docs/design-claude-uplift.md → Permissions (auto mode)): routine calls run, risky ones are
/// denied by Claude Code's classifier and come back as error tool results (see AutoModeBlock).
final class ClaudeProcess: @unchecked Sendable {
    let process = Process()
    private let stdinPipe = Pipe()
    private let stdoutPipe = Pipe()
    private let stderrPipe = Pipe()
    /// Serializes stdin writes so concurrent callers can never interleave two JSON lines.
    private let writeQueue = DispatchQueue(label: "claude-stdin")
    private let stderrLock = NSLock()
    private var stderrTail: [String] = []
    /// Control requests we sent and are waiting on, by request_id (docs/plan-mcp-servers.md)
    private let controlLock = NSLock()
    private var controlWaiters: [String: CheckedContinuation<ControlResult, Never>] = [:]

    /// Every stdout line, in order. Consumed by exactly one task (the session's event loop).
    let lines: AsyncStream<String>
    private let linesContinuation: AsyncStream<String>.Continuation

    var pid: Int32 { process.processIdentifier }

    init(claudePath: String, sessionId: String, resume: Bool, cwd: String, oauthToken: String?) {
        var args = [
            "--print", "--verbose",
            "--output-format", "stream-json",
            "--input-format", "stream-json",
            "--include-partial-messages",
            "--replay-user-messages",
            "--permission-mode", "auto",
            "--permission-prompt-tool", "stdio",
        ]
        args += resume ? ["--resume", sessionId] : ["--session-id", sessionId]
        // Optional extra flags for every session, e.g. MCLAUDE_CLAUDE_ARGS="--model haiku" in tests
        if let extra = ProcessInfo.processInfo.environment["MCLAUDE_CLAUDE_ARGS"] {
            args += extra.split(separator: " ").map(String.init)
        }
        process.executableURL = URL(fileURLWithPath: claudePath)
        process.arguments = args
        process.currentDirectoryURL = URL(fileURLWithPath: cwd)
        var env = ProcessInfo.processInfo.environment
        if let oauthToken { env["CLAUDE_CODE_OAUTH_TOKEN"] = oauthToken }
        // launchd gives us a minimal PATH; Claude's Bash tool needs the user's usual one
        let extra = ["/opt/homebrew/bin", "/usr/local/bin", "\(NSHomeDirectory())/.local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"]
        env["PATH"] = (extra + (env["PATH"]?.split(separator: ":").map(String.init) ?? [])).joined(separator: ":")
        // 90 s for an MCP server to connect (Claude's default is 30 s, too short while other
        // sessions are starting theirs; docs/plan-mcp-servers.md → Startup pacing and recovery)
        if env["MCP_TIMEOUT"] == nil { env["MCP_TIMEOUT"] = "90000" }
        // Claude Code's own OpenTelemetry, to the local collector (docs/plan-observability.md)  // telemetry
        env["CLAUDE_CODE_ENABLE_TELEMETRY"] = "1"  // telemetry
        env["CLAUDE_CODE_ENHANCED_TELEMETRY_BETA"] = "1"  // telemetry: traces
        env["OTEL_METRICS_EXPORTER"] = "otlp"  // telemetry
        env["OTEL_LOGS_EXPORTER"] = "otlp"  // telemetry
        env["OTEL_TRACES_EXPORTER"] = "otlp"  // telemetry
        env["OTEL_EXPORTER_OTLP_PROTOCOL"] = "http/json"  // telemetry
        env["OTEL_EXPORTER_OTLP_ENDPOINT"] = Telemetry.endpoint  // telemetry
        env["OTEL_LOG_USER_PROMPTS"] = "1"  // telemetry
        env["OTEL_LOG_ASSISTANT_RESPONSES"] = "1"  // telemetry
        env["OTEL_LOG_TOOL_CONTENT"] = "1"  // telemetry
        env["OTEL_LOG_TOOL_DETAILS"] = "1"  // telemetry
        env["CLAUDE_CODE_PROPAGATE_TRACEPARENT"] = "1"  // telemetry: into tool subprocesses
        env["OTEL_METRIC_EXPORT_INTERVAL"] = "10000"  // telemetry
        env["OTEL_LOGS_EXPORT_INTERVAL"] = "5000"  // telemetry
        env["OTEL_TRACES_EXPORT_INTERVAL"] = "5000"  // telemetry
        env["OTEL_RESOURCE_ATTRIBUTES"] = nil  // telemetry: set per session by setTelemetrySession
        env["TRACEPARENT"] = nil  // telemetry: never parent turns on a trace inherited from our own launcher
        env["TRACESTATE"] = nil  // telemetry
        process.environment = env
        process.standardInput = stdinPipe
        process.standardOutput = stdoutPipe
        process.standardError = stderrPipe

        var cont: AsyncStream<String>.Continuation!
        lines = AsyncStream(bufferingPolicy: .unbounded) { cont = $0 }
        linesContinuation = cont
    }

    /// Start the process. `onExit` fires once, after the last stdout line has been delivered.
    func start(onExit: @escaping @Sendable (Int32) -> Void) throws {
        var buffer = Data()
        let cont = linesContinuation
        stdoutPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let chunk = handle.availableData
            if chunk.isEmpty {                       // EOF
                handle.readabilityHandler = nil
                self?.failControlRequests()
                if !buffer.isEmpty, let s = String(data: buffer, encoding: .utf8) { cont.yield(s) }
                cont.finish()
                return
            }
            buffer.append(chunk)
            while let nl = buffer.firstIndex(of: 0x0A) {
                let lineData = buffer[buffer.startIndex..<nl]
                buffer.removeSubrange(buffer.startIndex...nl)
                guard let s = String(data: lineData, encoding: .utf8), !s.isEmpty else { continue }
                // Answers to our own control requests are matched here, before the event loop
                if s.hasPrefix("{\"type\":\"control_response\""),
                   let json = try? JSONSerialization.jsonObject(with: Data(lineData)) as? [String: Any] {
                    self?.resolveControl(json)
                }
                cont.yield(s)
            }
        }
        stderrPipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let chunk = handle.availableData
            guard !chunk.isEmpty else { handle.readabilityHandler = nil; return }
            guard let self, let s = String(data: chunk, encoding: .utf8) else { return }
            self.stderrLock.lock()
            self.stderrTail.append(contentsOf: s.split(separator: "\n").map(String.init))
            if self.stderrTail.count > 40 { self.stderrTail.removeFirst(self.stderrTail.count - 40) }
            self.stderrLock.unlock()
        }
        process.terminationHandler = { p in onExit(p.terminationStatus) }
        try process.run()
        // The SDK handshake; Claude answers with a control_response and then system/init
        send(["type": "control_request", "request_id": "init-\(UUID().uuidString)", "request": ["subtype": "initialize"]])
    }

    var recentStderr: String {
        stderrLock.lock(); defer { stderrLock.unlock() }
        return stderrTail.joined(separator: "\n")
    }

    // MARK: - stdin protocol

    func send(_ object: [String: Any]) {
        guard let data = try? JSONSerialization.data(withJSONObject: object) else { return }
        let handle = stdinPipe.fileHandleForWriting
        writeQueue.async { [weak self] in
            guard let self, self.process.isRunning else { return }
            do { try handle.write(contentsOf: data + Data([0x0A])) }
            catch { print("[claude \(self.pid)] stdin write failed: \(error)") }
        }
    }

    /// A user turn. `uuid` is echoed back by `--replay-user-messages` when Claude consumes it.
    func sendUserMessage(uuid: String, content: Any) {
        send(["type": "user", "uuid": uuid, "message": ["role": "user", "content": content]])
    }

    /// A user turn carrying the turn span's context: Claude Code parents its interaction span on
    /// `parent.traceparent` (docs/plan-observability.md → Turn spans).  // telemetry
    func sendUserMessage(uuid: String, content: Any, traceparent: String?) {  // telemetry
        var message: [String: Any] = ["type": "user", "uuid": uuid, "message": ["role": "user", "content": content]]  // telemetry
        if let traceparent { message["parent"] = ["traceparent": traceparent] }  // telemetry
        send(message)  // telemetry
    }  // telemetry

    /// Tags Claude Code's telemetry with the mclaude session id. Call before `start`.  // telemetry
    func setTelemetrySession(_ id: String) {  // telemetry
        let port = ProcessInfo.processInfo.environment["MCLAUDE_PORT"] ?? "8377"  // telemetry
        process.environment?["OTEL_RESOURCE_ATTRIBUTES"] = "mclaude.session_id=\(id),mclaude.server.port=\(port)"  // telemetry
    }  // telemetry

    func interrupt() {
        send(["type": "control_request", "request_id": "int-\(UUID().uuidString)", "request": ["subtype": "interrupt"]])
    }

    /// Answer a `can_use_tool` control request.
    func respond(requestId: String, allow: Bool, updatedInput: [String: Any]?, denyMessage: String? = nil) {
        var response: [String: Any] = ["behavior": allow ? "allow" : "deny"]
        if allow { response["updatedInput"] = updatedInput ?? [:] }
        else { response["message"] = denyMessage ?? "The user declined." }
        send(["type": "control_response", "response": ["subtype": "success", "request_id": requestId, "response": response]])
    }

    // MARK: - Control requests that expect an answer

    /// Send `{"type":"control_request","request":{"subtype":…, …fields}}` and wait for the
    /// matching control_response. The session's event loop hands responses to `resolveControl`.
    func controlRequest(_ subtype: String, _ fields: [String: Any] = [:], timeout: TimeInterval) async -> ControlResult {
        guard process.isRunning else { return .notRunning }
        let requestId = "mc-\(UUID().uuidString)"
        var request = fields
        request["subtype"] = subtype
        let message: [String: Any] = ["type": "control_request", "request_id": requestId, "request": request]
        return await withCheckedContinuation { (cont: CheckedContinuation<ControlResult, Never>) in
            controlLock.lock()
            controlWaiters[requestId] = cont
            controlLock.unlock()
            send(message)
            DispatchQueue.global().asyncAfter(deadline: .now() + timeout) { [weak self] in
                self?.finishControl(requestId, .timeout)
            }
        }
    }

    /// A control_response line from stdout. Ignored unless we're waiting on its request_id.
    func resolveControl(_ json: [String: Any]) {
        guard let response = json["response"] as? [String: Any],
              let requestId = response["request_id"] as? String else { return }
        if response["subtype"] as? String == "error" {
            finishControl(requestId, .error(response["error"] as? String ?? "Claude returned an error"))
        } else {
            finishControl(requestId, .success(JSONBox(response["response"] as? [String: Any] ?? [:])))
        }
    }

    private func finishControl(_ requestId: String, _ result: ControlResult) {
        controlLock.lock()
        let cont = controlWaiters.removeValue(forKey: requestId)
        controlLock.unlock()
        cont?.resume(returning: result)
    }

    private func failControlRequests() {
        controlLock.lock()
        let waiters = controlWaiters
        controlWaiters = [:]
        controlLock.unlock()
        for cont in waiters.values { cont.resume(returning: .notRunning) }
    }

    /// Ask nicely, then insist: interrupt, close stdin (Claude exits at EOF), SIGKILL after `grace`.
    func stop(grace: TimeInterval = 10) {
        guard process.isRunning else { return }
        interrupt()
        writeQueue.async { [stdinPipe] in try? stdinPipe.fileHandleForWriting.close() }
        let p = process
        DispatchQueue.global().asyncAfter(deadline: .now() + grace) {
            if p.isRunning { kill(p.processIdentifier, SIGKILL) }
        }
    }
}


/// A JSON object crossing actor boundaries (JSONSerialization output is immutable in practice).
struct JSONBox: @unchecked Sendable {
    let value: [String: Any]
    init(_ value: [String: Any]) { self.value = value }
}

/// The answer to a control request we sent Claude.
enum ControlResult: Sendable {
    case success(JSONBox)
    case error(String)        // Claude's error text
    case timeout
    case notRunning           // the process wasn't running, or exited before answering
}
