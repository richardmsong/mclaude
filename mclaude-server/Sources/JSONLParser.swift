import Foundation

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
                    toolResults.append(ToolResultBlock(toolUseId: toolUseId, content: resultContent, isError: isError))
                } else if itemType == "text" {
                    let t = item["text"] as? String ?? ""
                    if !t.isEmpty { text = t }
                }
            }
        }

        // Skip internal metadata entries
        if text.hasPrefix("[Image: source:") || text.hasPrefix("[Image source:") { return nil }
        if text.hasPrefix("<local-command-caveat>") || text.hasPrefix("<command-name>") { return nil }

        // Skill expansion content → show as tool result so it renders in the Skill block
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

        // Compaction summary → distinct event type
        if text.hasPrefix("This session is being continued from a previous conversation") {
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
        guard subtype == "turn_duration" else { return nil }
        let durationMs = json["durationMs"] as? Int
        return SessionEvent(
            uuid: uuid, timestamp: timestamp, type: .system,
            text: nil, thinking: nil, toolUse: nil, toolResults: nil,
            model: nil, durationMs: durationMs
        )
    }
}
