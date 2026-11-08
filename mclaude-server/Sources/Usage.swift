import Foundation

/// Token usage of one model within a session.
struct ModelUsage: Codable, Sendable, Equatable {
    var inputTokens = 0
    var outputTokens = 0
    var cacheReadTokens = 0
    var cacheWriteTokens = 0
    var costUSD: Double?
    var contextWindow: Int?
}

/// Usage for one conversation: what the app's context gauge and Usage screen show.
/// Totals come from the CLI's per-turn `result` (model_usage_since_last_result: exact tokens,
/// list-price cost and context window per model). "Context" is the size of the prompt Claude
/// last saw on the main thread, from the latest top-level assistant message's usage.
struct SessionUsage: Codable, Sendable, Equatable {
    var inputTokens = 0
    var outputTokens = 0
    var cacheReadTokens = 0
    var cacheWriteTokens = 0
    /// Nil when unknown (history rebuilt from a transcript has tokens but no cost).
    var costUSD: Double?
    var turns = 0
    var contextTokens: Int?
    var contextWindow: Int?
    var model: String?
    var byModel: [String: ModelUsage] = [:]
    var updatedAt: Date?
    /// True when part of the history came from the transcript (so costUSD covers only part of it).
    var partial: Bool?

    var totalTokens: Int { inputTokens + outputTokens + cacheReadTokens + cacheWriteTokens }

    static let defaultContextWindow = 200_000

    /// Main-thread assistant message: refresh the context size and model.
    mutating func noteAssistantMessage(usage: [String: Any], model: String?) {
        let input = usage["input_tokens"] as? Int ?? 0
        let cacheRead = usage["cache_read_input_tokens"] as? Int ?? 0
        let cacheWrite = usage["cache_creation_input_tokens"] as? Int ?? 0
        let output = usage["output_tokens"] as? Int ?? 0
        let context = input + cacheRead + cacheWrite + output
        guard context > 0 else { return }
        contextTokens = context
        if let model { self.model = model }
        if contextWindow == nil || context > (contextWindow ?? 0) {
            contextWindow = Self.guessContextWindow(model: model, contextTokens: context)
        }
        updatedAt = Date()
    }

    /// End of a turn: add the per-turn deltas reported by the CLI.
    mutating func addResult(_ json: [String: Any]) {
        turns += 1
        updatedAt = Date()
        guard let perModel = (json["model_usage_since_last_result"] ?? json["modelUsage"]) as? [String: [String: Any]] else {
            if let cost = json["total_cost_usd"] as? Double { costUSD = (costUSD ?? 0) + cost }
            return
        }
        for (name, m) in perModel {
            let delta = ModelUsage(
                inputTokens: m["inputTokens"] as? Int ?? 0,
                outputTokens: m["outputTokens"] as? Int ?? 0,
                cacheReadTokens: m["cacheReadInputTokens"] as? Int ?? 0,
                cacheWriteTokens: m["cacheCreationInputTokens"] as? Int ?? 0,
                costUSD: m["costUSD"] as? Double,
                contextWindow: m["contextWindow"] as? Int)
            let key = (m["canonicalModel"] as? String) ?? name
            var entry = byModel[key] ?? ModelUsage()
            entry.inputTokens += delta.inputTokens
            entry.outputTokens += delta.outputTokens
            entry.cacheReadTokens += delta.cacheReadTokens
            entry.cacheWriteTokens += delta.cacheWriteTokens
            if let c = delta.costUSD { entry.costUSD = (entry.costUSD ?? 0) + c }
            if let w = delta.contextWindow { entry.contextWindow = w }
            byModel[key] = entry

            inputTokens += delta.inputTokens
            outputTokens += delta.outputTokens
            cacheReadTokens += delta.cacheReadTokens
            cacheWriteTokens += delta.cacheWriteTokens
            if let c = delta.costUSD { costUSD = (costUSD ?? 0) + c }
            // The main model's window sets the gauge's limit
            if let w = delta.contextWindow, let model, Self.model(model, matches: key) { contextWindow = w }
        }
        if contextWindow == nil {
            contextWindow = byModel.values.compactMap(\.contextWindow).max()
                ?? Self.guessContextWindow(model: model, contextTokens: contextTokens)
        }
    }

    /// "us.anthropic.claude-haiku-4-5-20251001-v1:0" vs canonical "claude-haiku-4-5".
    static func model(_ model: String, matches key: String) -> Bool {
        model == key || model.contains(key) || key.contains(model)
    }

    /// Fallback until the CLI reports the real window (it does in every turn's result).
    /// Current Opus/Sonnet 5 models default to a 1M window (CLI reports 1_000_000 for
    /// claude-opus-5-5); a context already above the guess proves the window is larger.
    static func guessContextWindow(model: String?, contextTokens: Int? = nil) -> Int {
        let m = model?.lowercased() ?? ""
        var guess = defaultContextWindow
        if m.contains("[1m]") || m.contains("-1m") || m.contains("opus-5") || m.contains("sonnet-5") {
            guess = 1_000_000
        }
        if let used = contextTokens, used > guess { guess = 1_000_000 }
        return guess
    }

    /// Apply a window learned from the CLI (for this model) or re-derive the fallback.
    mutating func refreshContextWindow(known: [String: Int]) {
        if let model, let w = known.first(where: { Self.model(model, matches: $0.key) })?.value {
            contextWindow = w
        } else if byModel.isEmpty || contextWindow == nil
                    || (contextTokens ?? 0) > (contextWindow ?? 0) {
            contextWindow = Self.guessContextWindow(model: model, contextTokens: contextTokens)
        }
    }

    /// Rebuild usage from a Claude transcript (for sessions that predate tracking).
    /// Assistant lines repeat a message's usage once per content block, so dedupe by message id.
    static func backfill(transcriptPath: String) -> SessionUsage? {
        guard let data = FileManager.default.contents(atPath: transcriptPath),
              let text = String(data: data, encoding: .utf8) else { return nil }
        var perMessage: [String: (usage: [String: Any], model: String?, sidechain: Bool)] = [:]
        var order: [String] = []
        var turns = 0
        for line in text.split(separator: "\n") {
            guard let json = try? JSONSerialization.jsonObject(with: Data(line.utf8)) as? [String: Any] else { continue }
            if json["type"] as? String == "system", json["subtype"] as? String == "turn_duration" { turns += 1 }
            guard json["type"] as? String == "assistant",
                  let message = json["message"] as? [String: Any],
                  let usage = message["usage"] as? [String: Any] else { continue }
            let id = message["id"] as? String ?? (json["uuid"] as? String ?? UUID().uuidString)
            if perMessage[id] == nil { order.append(id) }
            perMessage[id] = (usage, message["model"] as? String, json["isSidechain"] as? Bool ?? false)
        }
        guard !order.isEmpty else { return nil }
        var u = SessionUsage()
        u.partial = true
        u.turns = turns
        for id in order {
            guard let m = perMessage[id] else { continue }
            u.inputTokens += m.usage["input_tokens"] as? Int ?? 0
            u.outputTokens += m.usage["output_tokens"] as? Int ?? 0
            u.cacheReadTokens += m.usage["cache_read_input_tokens"] as? Int ?? 0
            u.cacheWriteTokens += m.usage["cache_creation_input_tokens"] as? Int ?? 0
            if !m.sidechain { u.noteAssistantMessage(usage: m.usage, model: m.model) }
        }
        return u
    }
}
