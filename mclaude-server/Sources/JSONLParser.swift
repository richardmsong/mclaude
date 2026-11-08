import Foundation
import CryptoKit

/// Shared JSONL line parser used by both JSONLTailer (history) and FileWatcher (real-time).
enum JSONLParser {

    /// Parse a single JSONL line into a SessionEvent, or nil if not a recognized/relevant event.
    static func parseEvent(line: String) -> SessionEvent? {
        guard let data = line.data(using: .utf8),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let type = json["type"] as? String,
              let timestamp = json["timestamp"] as? String else { return nil }

        // queue-operation events (mid-turn user messages) don't have uuid
        if type == "queue-operation" {
            return parseQueueOperation(json: json, timestamp: timestamp)
        }

        guard let uuid = json["uuid"] as? String else { return nil }

        switch type {
        case "user":
            return parseUserEvent(json: json, uuid: uuid, timestamp: timestamp)
        case "assistant":
            return parseAssistantEvent(json: json, uuid: uuid, timestamp: timestamp)
        case "system":
            return parseSystemEvent(json: json, uuid: uuid, timestamp: timestamp)
        default:
            return nil
        }
    }

    static func parseQueueOperation(json: [String: Any], timestamp: String) -> SessionEvent? {
        guard json["operation"] as? String == "enqueue",
              let content = json["content"] as? String,
              !content.isEmpty else { return nil }
        let seed = "\(timestamp)-\(content)"
        let syntheticUuid = "q-\(seed.hashValue)"
        return SessionEvent(uuid: syntheticUuid, timestamp: timestamp, type: .user,
                            text: content, thinking: nil, toolUse: nil, toolResults: nil,
                            model: nil, durationMs: nil)
    }

    static func parseUserEvent(json: [String: Any], uuid: String, timestamp: String) -> SessionEvent? {
        guard let message = json["message"] as? [String: Any] else { return nil }
        let content = message["content"]

        var text = ""
        var toolResults: [ToolResultBlock] = []

        if let str = content as? String {
            text = str
        } else if let arr = content as? [[String: Any]] {
            for item in arr {
                let itemType = item["type"] as? String ?? ""
                if itemType == "tool_result" {
                    let resultContent = extractToolResultContent(item["content"])
                    let isError = item["is_error"] as? Bool ?? false
                    let toolUseId = item["tool_use_id"] as? String ?? ""
                    let images = extractToolResultImages(item["content"])
                    toolResults.append(ToolResultBlock(toolUseId: toolUseId, content: resultContent, isError: isError,
                                                       images: images.isEmpty ? nil : images))
                } else if itemType == "text" {
                    let t = item["text"] as? String ?? ""
                    if !t.isEmpty { text = t }
                }
            }
        }

        // Skill expansion content → show as tool result so it renders in the Skill block.
        // Checked before the isMeta skip below: the CLI may mark these entries isMeta too.
        if text.hasPrefix("Base directory for this skill:") {
            // Extract skill path from first line
            let firstLine = text.prefix(while: { $0 != "\n" })
            let skillPath = firstLine.replacingOccurrences(of: "Base directory for this skill: ", with: "")
            return SessionEvent(
                uuid: uuid, timestamp: timestamp, type: .toolResult,
                text: nil, thinking: nil, toolUse: nil,
                toolResults: [ToolResultBlock(toolUseId: "", content: text, isError: false)],
                model: nil, durationMs: nil
            )
        }

        // Skip internal metadata entries. isMeta marks CLI-generated lines the user never typed
        // (e.g. "[Image: original 1206x2622, displayed at ...]" after a Read of an image), which
        // would otherwise render as a user bubble. Tool results are never meta, so keep those.
        if json["isMeta"] as? Bool == true, toolResults.isEmpty { return nil }
        // The server's own nudge to a session that was mid-turn when the server restarted: restarts are invisible
        if toolResults.isEmpty, text == SessionManager.restartResumeText { return nil }
        if text.hasPrefix("[Image: source:") || text.hasPrefix("[Image source:") || text.hasPrefix("[Image: original") { return nil }
        // The user's own /compact, echoed in Claude Code's command form → show it as they typed it
        if text.hasPrefix("<command-name>/compact</command-name>") {
            let args = extractTagContent(text, tag: "command-args").trimmingCharacters(in: .whitespacesAndNewlines)
            return SessionEvent(uuid: uuid, timestamp: timestamp, type: .user,
                                text: args.isEmpty ? "/compact" : "/compact \(args)", thinking: nil, toolUse: nil,
                                toolResults: nil, model: nil, durationMs: nil)
        }
        if text.hasPrefix("<local-command-caveat>") || text.hasPrefix("<command-name>") { return nil }
        // A local command's output (older CLIs write it as a user line): show it as its reply
        if text.hasPrefix("<local-command-stdout>") {
            return localCommandOutput(extractTagContent(text, tag: "local-command-stdout"), uuid: uuid, timestamp: timestamp)
        }

        // User-initiated bash commands (! prefix in Claude Code)
        if text.hasPrefix("<bash-input>") {
            let command = text.replacingOccurrences(of: "<bash-input>", with: "")
                .replacingOccurrences(of: "</bash-input>", with: "")
                .trimmingCharacters(in: .whitespacesAndNewlines)
            guard !command.isEmpty else { return nil }
            return SessionEvent(
                uuid: uuid, timestamp: timestamp, type: .toolUse,
                text: nil, thinking: nil,
                toolUse: ToolUseBlock(id: uuid, name: "!", inputSummary: command, fullInput: nil),
                toolResults: nil, model: nil, durationMs: nil
            )
        }
        if text.contains("<bash-stdout>") {
            let stdout = extractTagContent(text, tag: "bash-stdout")
            let stderr = extractTagContent(text, tag: "bash-stderr")
            let content = stderr.isEmpty ? stdout : (stdout.isEmpty ? stderr : "\(stdout)\n\(stderr)")
            guard !content.isEmpty else { return nil }
            return SessionEvent(
                uuid: uuid, timestamp: timestamp, type: .toolResult,
                text: nil, thinking: nil, toolUse: nil,
                toolResults: [ToolResultBlock(toolUseId: "", content: content, isError: !stderr.isEmpty && stdout.isEmpty)],
                model: nil, durationMs: nil
            )
        }

        // Compaction summary → distinct event type (merged into its compact_boundary marker by the
        // history parse)
        if json["isCompactSummary"] as? Bool == true
            || text.hasPrefix("This session is being continued from a previous conversation") {
            return SessionEvent(
                uuid: uuid, timestamp: timestamp, type: .compaction,
                text: text, thinking: nil, toolUse: nil, toolResults: nil,
                model: nil, durationMs: nil
            )
        }

        if !text.isEmpty {
            return SessionEvent(
                uuid: uuid, timestamp: timestamp, type: .user,
                text: text, thinking: nil, toolUse: nil,
                toolResults: toolResults.isEmpty ? nil : toolResults,
                model: nil, durationMs: nil
            )
        }
        if !toolResults.isEmpty {
            return SessionEvent(
                uuid: uuid, timestamp: timestamp, type: .toolResult,
                text: nil, thinking: nil, toolUse: nil,
                toolResults: toolResults, model: nil, durationMs: nil
            )
        }
        return nil
    }

    /// Extract text between XML-style tags, e.g. extractTagContent("<foo>bar</foo>", tag: "foo") → "bar"
    static func extractTagContent(_ text: String, tag: String) -> String {
        guard let startRange = text.range(of: "<\(tag)>"),
              let endRange = text.range(of: "</\(tag)>") else { return "" }
        return String(text[startRange.upperBound..<endRange.lowerBound])
    }

    /// Where tool-result images are written (served by GET /screenshots/<file>).
    nonisolated(unsafe) static var toolImageDir = "/tmp/mclaude-screenshots"

    /// Save each base64 image block in a tool_result to disk and return the file paths.
    /// Names are content hashes, so re-parsing history (or the live stream + JSONL) is idempotent
    /// and the event payload stays small (paths, not base64).
    static func extractToolResultImages(_ content: Any?) -> [String] {
        guard let arr = content as? [[String: Any]] else { return [] }
        let exts = ["image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp"]
        var paths: [String] = []
        for block in arr where block["type"] as? String == "image" {
            guard let source = block["source"] as? [String: Any],
                  source["type"] as? String == "base64",
                  let b64 = source["data"] as? String,
                  let data = Data(base64Encoded: b64) else { continue }
            let ext = exts[(source["media_type"] as? String ?? "").lowercased()] ?? "png"
            let hash = SHA256.hash(data: data).prefix(12).map { String(format: "%02x", $0) }.joined()
            let path = "\(toolImageDir)/toolimg-\(hash).\(ext)"
            if !FileManager.default.fileExists(atPath: path) {
                try? FileManager.default.createDirectory(atPath: toolImageDir, withIntermediateDirectories: true)
                try? data.write(to: URL(fileURLWithPath: path), options: .atomic)
            }
            paths.append(path)
        }
        return paths
    }

    static func extractToolResultContent(_ content: Any?) -> String {
        if let str = content as? String { return str }
        if let arr = content as? [[String: Any]] {
            return arr.compactMap { block in
                if block["type"] as? String == "text" {
                    return block["text"] as? String
                }
                return nil
            }.joined(separator: "\n")
        }
        return ""
    }

    static func parseAssistantEvent(json: [String: Any], uuid: String, timestamp: String) -> SessionEvent? {
        guard let message = json["message"] as? [String: Any],
              let contentArray = message["content"] as? [[String: Any]] else { return nil }

        let model = message["model"] as? String

        // Prioritize tool_use blocks (e.g. AskUserQuestion may follow a text block)
        let toolBlock = contentArray.first(where: { $0["type"] as? String == "tool_use" })
        let firstBlock = toolBlock ?? contentArray.first
        guard let firstBlock = firstBlock,
              let blockType = firstBlock["type"] as? String else { return nil }

        switch blockType {
        case "thinking":
            let thinking = firstBlock["thinking"] as? String ?? ""
            guard !thinking.isEmpty else { return nil }
            return SessionEvent(
                uuid: uuid, timestamp: timestamp, type: .thinking,
                text: nil, thinking: thinking, toolUse: nil, toolResults: nil,
                model: model, durationMs: nil
            )

        case "text":
            let text = firstBlock["text"] as? String ?? ""
            guard !text.isEmpty else { return nil }
            return SessionEvent(
                uuid: uuid, timestamp: timestamp, type: .text,
                text: text, thinking: nil, toolUse: nil, toolResults: nil,
                model: model, durationMs: nil
            )

        case "tool_use":
            let name = firstBlock["name"] as? String ?? "unknown"
            let toolId = firstBlock["id"] as? String ?? ""
            var inputSummary = ""
            var fullInput: String?
            if let input = firstBlock["input"] as? [String: Any] {
                if let filePath = input["file_path"] as? String {
                    inputSummary = filePath
                } else if let command = input["command"] as? String {
                    inputSummary = command
                } else if let pattern = input["pattern"] as? String {
                    inputSummary = pattern
                } else if let prompt = input["prompt"] as? String {
                    inputSummary = prompt
                } else {
                    if let firstKey = input.keys.sorted().first {
                        let val = "\(input[firstKey] ?? "")"
                        inputSummary = "\(firstKey): \(val)"
                    }
                }
                if let data = try? JSONSerialization.data(withJSONObject: input, options: [.prettyPrinted, .sortedKeys]),
                   let str = String(data: data, encoding: .utf8) {
                    fullInput = str
                }
            }
            return SessionEvent(
                uuid: uuid, timestamp: timestamp, type: .toolUse,
                text: nil, thinking: nil,
                toolUse: ToolUseBlock(id: toolId, name: name, inputSummary: inputSummary, fullInput: fullInput),
                toolResults: nil, model: model, durationMs: nil
            )

        default:
            return nil
        }
    }

    static func parseSystemEvent(json: [String: Any], uuid: String, timestamp: String) -> SessionEvent? {
        let subtype = json["subtype"] as? String ?? ""
        if subtype == "compact_boundary" { return compactionMarker(json: json, uuid: uuid, timestamp: timestamp) }
        if subtype == "local_command" {
            let content = json["content"] as? String ?? ""
            return localCommandOutput(content.hasPrefix("<local-command-stdout>")
                                          ? extractTagContent(content, tag: "local-command-stdout") : content,
                                      uuid: uuid, timestamp: timestamp)
        }
        guard subtype == "turn_duration" else { return nil }
        let durationMs = json["durationMs"] as? Int
        return SessionEvent(
            uuid: uuid, timestamp: timestamp, type: .system,
            text: nil, thinking: nil, toolUse: nil, toolResults: nil,
            model: nil, durationMs: durationMs
        )
    }

    // MARK: - Local slash commands

    /// Output of a command the CLI ran itself (`/reload-plugins`, `/cost` …), shown as a reply
    /// (`.text`, model "<synthetic>"), the same as the live stream's synthetic assistant message.
    /// Empty output and /compact's "Compacted" (the compaction marker says so) are dropped.
    static func localCommandOutput(_ raw: String, uuid: String, timestamp: String) -> SessionEvent? {
        let text = stripANSI(raw).trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !text.hasPrefix("Compacted") else { return nil }
        return SessionEvent(uuid: uuid, timestamp: timestamp, type: .text, text: text, thinking: nil,
                            toolUse: nil, toolResults: nil, model: "<synthetic>", durationMs: nil)
    }

    /// The command as typed, from a live synthetic assistant message's
    /// `local_command_run {command, args}`: "/command" or "/command args". Nil for other messages.
    static func localCommandEcho(_ json: [String: Any]) -> String? {
        guard let run = json["local_command_run"] as? [String: Any],
              let command = run["command"] as? String, !command.isEmpty else { return nil }
        let args = (run["args"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        let name = command.hasPrefix("/") ? command : "/" + command
        return args.isEmpty ? name : "\(name) \(args)"
    }

    // MARK: - Task notifications

    /// stream-json stdout reports a finished background task as
    /// `{"type":"system","subtype":"task_notification",task_id,tool_use_id,status,output_file,summary}`,
    /// while the transcript stores a `<task-notification>` user line. Rebuild that user line so the
    /// app shows the same card live as after a reload. `agentDescription` is set when the task is
    /// a known Agent/Task call: stdout's summary is then the agent's output, which the transcript
    /// keeps in `<result>` under an `Agent "…" finished` summary.
    static func taskNotificationEvent(json: [String: Any], uuid: String, timestamp: String,
                                      agentDescription: String?) -> SessionEvent? {
        guard json["subtype"] as? String == "task_notification" else { return nil }
        func field(_ key: String) -> String? {
            guard let s = json[key] as? String, !s.isEmpty else { return nil }
            return s
        }
        let status = field("status") ?? ""
        var summary = field("summary")
        var result: String?
        if let description = agentDescription {
            result = summary
            summary = "Agent \"\(description)\" " + (status == "completed" || status.isEmpty ? "finished" : status)
        }
        var lines = ["<task-notification>"]
        for (tag, value) in [("task-id", field("task_id")), ("tool-use-id", field("tool_use_id")),
                             ("output-file", field("output_file")), ("status", status.isEmpty ? nil : status),
                             ("summary", summary), ("result", result)] {
            guard let value else { continue }
            lines.append("<\(tag)>\(value.replacingOccurrences(of: "<", with: "&lt;"))</\(tag)>")
        }
        lines.append("</task-notification>")
        return SessionEvent(uuid: uuid, timestamp: timestamp, type: .user, text: lines.joined(separator: "\n"),
                            thinking: nil, toolUse: nil, toolResults: nil, model: nil, durationMs: nil)
    }

    private static let taskNotificationBlock = try! NSRegularExpression(
        pattern: #"<task-notification>.*?</task-notification>"#, options: [.dotMatchesLineSeparators])

    /// `text` without the `<task-notification>` blocks whose task id `shouldRemove` accepts, trimmed.
    /// Nil when no block was removed.
    static func removingTaskNotifications(from text: String, where shouldRemove: (String) -> Bool) -> String? {
        let ns = text as NSString
        var result = ns
        var removed = false
        for match in taskNotificationBlock.matches(in: text, range: NSRange(location: 0, length: ns.length)).reversed() {
            let id = extractTagContent(ns.substring(with: match.range), tag: "task-id")
                .trimmingCharacters(in: .whitespacesAndNewlines)
            guard !id.isEmpty, shouldRemove(id) else { continue }
            result = result.replacingCharacters(in: match.range, with: "") as NSString
            removed = true
        }
        return removed ? (result as String).trimmingCharacters(in: .whitespacesAndNewlines) : nil
    }

    // MARK: - Compaction

    /// A compact_boundary (transcript: camelCase `compactMetadata`; stream-json stdout:
    /// snake_case `compact_metadata`) → a `.compaction` marker with the token counts. The summary
    /// text is attached separately (it's a following user line in the transcript).
    static func compactionMarker(json: [String: Any], uuid: String, timestamp: String, summary: String? = nil) -> SessionEvent {
        let meta = (json["compactMetadata"] ?? json["compact_metadata"]) as? [String: Any] ?? [:]
        func int(_ a: String, _ b: String) -> Int? { (meta[a] ?? meta[b]) as? Int }
        return SessionEvent(uuid: uuid, timestamp: timestamp, type: .compaction, text: summary, thinking: nil,
                            toolUse: nil, toolResults: nil, model: nil,
                            durationMs: int("durationMs", "duration_ms"),
                            compactPreTokens: int("preTokens", "pre_tokens"),
                            compactPostTokens: int("postTokens", "post_tokens"))
    }

    static func stripANSI(_ s: String) -> String {
        s.replacingOccurrences(of: "\u{1B}\\[[0-9;]*[A-Za-z]", with: "", options: .regularExpression)
    }
}
