import Foundation

@Observable
final class SessionDetailViewModel {
    var inputText: String = ""
    var isSending = false
    var actionError: String?
    var cancelCooldown = false
    private(set) var lastSentText: String = ""
    var pendingMessages: [(id: UUID, text: String, sentAt: Date, delivered: Bool)] = []

    // Input history
    private(set) var inputHistory: [String] = []
    private var historyIndex: Int = -1  // -1 = not browsing history
    private var savedDraft: String = ""  // text being typed before browsing history

    func historyBack() -> String? {
        guard !inputHistory.isEmpty else { return nil }
        if historyIndex == -1 {
            savedDraft = inputText
            historyIndex = inputHistory.count - 1
        } else if historyIndex > 0 {
            historyIndex -= 1
        } else {
            return nil
        }
        return inputHistory[historyIndex]
    }

    func historyForward() -> String? {
        guard historyIndex >= 0 else { return nil }
        if historyIndex < inputHistory.count - 1 {
            historyIndex += 1
            return inputHistory[historyIndex]
        } else {
            historyIndex = -1
            return savedDraft
        }
    }

    private func addToHistory(_ text: String) {
        guard !text.isEmpty else { return }
        // Avoid duplicating the last entry
        if inputHistory.last != text {
            inputHistory.append(text)
            // Cap at 50 entries
            if inputHistory.count > 50 { inputHistory.removeFirst() }
        }
        historyIndex = -1
        savedDraft = ""
    }

    func send(client: APIClient, id: String) async {
        let text = inputText
        guard !text.isEmpty else { return }
        lastSentText = text
        addToHistory(text)
        let msgId = UUID()
        pendingMessages.append((id: msgId, text: text, sentAt: Date(), delivered: false))
        isSending = true
        inputText = ""
        actionError = nil
        do {
            try await client.sendInput(id: id, text: text, sendEnter: true)
            if let idx = pendingMessages.firstIndex(where: { $0.id == msgId }) {
                pendingMessages[idx].delivered = true
            }
        } catch {
            inputText = text
            // Keep the pending message visible but mark as failed
            actionError = "Failed to send input"
        }
        isSending = false
    }

    /// Clean up stale pending messages. Dedup against JSONL events is handled
    /// at display time in ConversationView's timelineItems.
    func clearMatchedPending(events: [SessionEvent]) {
        guard !pendingMessages.isEmpty else { return }

        // Remove stale undelivered messages (send likely failed silently)
        let staleCutoff = Date().addingTimeInterval(-120)
        pendingMessages.removeAll { !$0.delivered && $0.sentAt < staleCutoff }

        // Remove very old delivered messages (> 1 hour) as cleanup
        let oldCutoff = Date().addingTimeInterval(-3600)
        pendingMessages.removeAll { $0.sentAt < oldCutoff }
    }

    func approve(client: APIClient, id: String) async {
        actionError = nil
        do {
            try await client.approve(id: id)
        } catch {
            actionError = "Approve failed"
        }
    }

    func cancel(client: APIClient, id: String) async {
        guard !cancelCooldown else { return }
        cancelCooldown = true
        actionError = nil
        do {
            try await client.cancel(id: id)
        } catch {
            actionError = "Cancel failed"
        }
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(3))
            self.cancelCooldown = false
        }
    }

    func sendPromptResponse(client: APIClient, id: String, text: String) async {
        guard !text.isEmpty else { return }
        isSending = true
        actionError = nil
        do {
            try await client.sendInput(id: id, text: text, sendEnter: true)
        } catch {
            actionError = "Failed to send selection"
        }
        isSending = false
    }

    func sendKey(client: APIClient, id: String, key: String) async {
        do {
            try await client.sendInput(id: id, text: key, sendEnter: false)
        } catch {
            actionError = "Failed to send key"
        }
    }

    func sendVoice(client: APIClient, id: String, text: String) async {
        guard !text.isEmpty else { return }
        addToHistory(text)
        let msgId = UUID()
        pendingMessages.append((id: msgId, text: text, sentAt: Date(), delivered: false))
        isSending = true
        actionError = nil
        do {
            try await client.sendInput(id: id, text: text, sendEnter: true)
            if let idx = pendingMessages.firstIndex(where: { $0.id == msgId }) {
                pendingMessages[idx].delivered = true
            }
        } catch {
            // Keep the pending message visible but mark as failed
            actionError = "Failed to send voice input"
        }
        isSending = false
    }

    func sendScreenshot(client: APIClient, id: String, imageData: Data) async {
        isSending = true
        actionError = nil
        do {
            let path = try await client.uploadScreenshot(imageData: imageData)
            try await client.sendInput(id: id, text: path, sendEnter: true)
        } catch {
            actionError = "Screenshot upload failed"
        }
        isSending = false
    }

    func sendFile(client: APIClient, id: String, data: Data, filename: String) async {
        isSending = true
        actionError = nil
        do {
            let path = try await client.uploadFile(data: data, filename: filename)
            try await client.sendInput(id: id, text: path, sendEnter: true)
        } catch {
            actionError = "File upload failed"
        }
        isSending = false
    }
}
