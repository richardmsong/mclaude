import Foundation
import UIKit

/// A message the app sent that Claude hasn't echoed back yet (i.e. hasn't accepted).
/// `delivered` = the server handed it to Claude ("Sent" until the echo). Messages the server
/// queues instead (Claude mid-turn) are removed at once: the server's queue shows them.
struct PendingMessage: Identifiable {
    let id: UUID
    let text: String
    let sentAt: Date
    var delivered: Bool
    /// Local thumbnails of attached photos, shown while uploading/queued.
    var photos: [UIImage] = []
    /// What the uploads returned (photo paths, video "path [video: …]" fragments); used to match Claude's echo exactly.
    var paths: [String] = []
    /// A turn was already running when this was sent, so it waits for Claude's next step.
    /// (Sent while idle, the server hands it to Claude at once; the echo just takes a moment.)
    var sentWhileBusy = false
}

@Observable
final class SessionDetailViewModel {
    var inputText: String = ""
    var isSending = false
    var actionError: String?
    var cancelCooldown = false
    private(set) var lastSentText: String = ""
    var pendingMessages: [PendingMessage] = []
    /// Kept in sync by SessionDetailView from the session status (working = a turn is running).
    var sessionBusy = false

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

    // FIFO delivery: every send takes a ticket when the user hits send and hands its message
    // to the server only when it's that ticket's turn. Photo messages upload first, so without
    // this a text sent after a photo could reach the server (and Claude) before it.
    private var nextTicket = 0
    private var servingTicket = 0
    private var finishedTickets: Set<Int> = []   // done early (e.g. upload failed) but not yet served

    private func takeTicket() -> Int {
        defer { nextTicket += 1 }
        return nextTicket
    }

    private func waitForTurn(_ ticket: Int) async {
        while servingTicket < ticket { try? await Task.sleep(nanoseconds: 30_000_000) }
    }

    /// Release a ticket. Out-of-order finishes (a failed upload before its turn) are remembered so
    /// the line never stalls on them.
    private func finishTurn(_ ticket: Int) {
        finishedTickets.insert(ticket)
        while finishedTickets.remove(servingTicket) != nil { servingTicket += 1 }
    }

    /// Send typed text. The composer clears itself synchronously on tap (InputBarView.submitText)
    /// and passes the text here, so the field never lags behind a send.
    func send(client: APIClient, id: String, text: String) async {
        guard !text.isEmpty else { return }
        let ticket = takeTicket()
        defer { finishTurn(ticket) }
        lastSentText = text
        addToHistory(text)
        let msgId = UUID()
        pendingMessages.append(PendingMessage(id: msgId, text: text, sentAt: Date(), delivered: false,
                                              sentWhileBusy: sessionBusy))
        isSending = true
        if inputText == text { inputText = "" }
        actionError = nil
        await waitForTurn(ticket)
        do {
            let result = try await client.sendInput(id: id, text: text, sendEnter: true)
            markSent(msgId, leavesNoEcho: result.leavesNoEcho)
        } catch {
            // Put the text back for a retry, but never over a newer draft.
            if inputText.isEmpty { inputText = text }
            // Keep the pending message visible but mark as failed
            actionError = "Failed to send input"
        }
        isSending = false
    }

    /// The server accepted a pending message. If it queued it (the queue row shows it) or used it to
    /// answer a pending prompt (the prompt card shows it), no transcript copy will come, so drop the
    /// local bubble; otherwise it was handed to Claude ("Sent" until its copy arrives).
    private func markSent(_ msgId: UUID, leavesNoEcho: Bool) {
        if leavesNoEcho {
            pendingMessages.removeAll { $0.id == msgId }
        } else if let idx = pendingMessages.firstIndex(where: { $0.id == msgId }) {
            pendingMessages[idx].delivered = true
        }
    }

    /// Remove placeholders whose transcript copy (Claude's echo of the user message) has arrived,
    /// plus failed sends (never delivered after 2 min) and anything over an hour old.
    /// Assigns `pendingMessages` only when something is removed: any write to an @Observable
    /// property redraws the session screen, and with it the whole transcript.
    func clearMatchedPending(events: [SessionEvent]) {
        guard !pendingMessages.isEmpty else { return }
        let now = Date()
        let matched = Self.matchedPendingIds(pendingMessages, events: events)
        let kept = pendingMessages.filter { msg in
            if matched.contains(msg.id) { return false }
            if !msg.delivered && msg.sentAt < now.addingTimeInterval(-120) { return false }  // send failed silently
            return msg.sentAt >= now.addingTimeInterval(-3600)
        }
        if kept.count != pendingMessages.count { pendingMessages = kept }
    }

    /// Allowance for the phone's clock running ahead of the Mac's (transcript timestamps come from the Mac).
    private static let clockSkew: TimeInterval = 60
    private static let imageTagPattern = try! NSRegularExpression(pattern: #"^\[Image #\d+\]\s*"#)

    /// Strip the server-added "[Image #N] " prefix and attachment paths so the user's own text compares.
    private static func normalizeForMatch(_ text: String) -> String {
        var result = text
        result = imageTagPattern.stringByReplacingMatches(in: result, range: NSRange(result.startIndex..., in: result),
                                                          withTemplate: "")
        result = UserAttachment.pattern.stringByReplacingMatches(in: result, range: NSRange(result.startIndex..., in: result),
                                                                 withTemplate: "")
        return result.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// Pending messages whose copy is in the transcript. Only user events timestamped no earlier
    /// than `clockSkew` before the send count, each clears at most one message, oldest message first.
    private static func matchedPendingIds(_ pending: [PendingMessage], events: [SessionEvent]) -> Set<UUID> {
        guard let earliest = pending.map(\.sentAt).min() else { return [] }
        let floor = earliest.addingTimeInterval(-clockSkew)
        // Candidate echoes: top-level user events new enough to belong to any pending message
        var candidates: [(date: Date, raw: String, normalized: String)] = []
        for event in events where event.type == .user && event.subagentInfo == nil {
            guard let text = event.text, !text.isEmpty,
                  let date = PeerDates.parse(event.timestamp), date >= floor else { continue }
            candidates.append((date, text, normalizeForMatch(text)))
        }
        guard !candidates.isEmpty else { return [] }
        candidates.sort { $0.date < $1.date }

        var used = Set<Int>()
        var matched = Set<UUID>()
        for msg in pending.sorted(by: { $0.sentAt < $1.sentAt }) {
            let notBefore = msg.sentAt.addingTimeInterval(-clockSkew)
            let normalPending = normalizeForMatch(msg.text)
            let hit = candidates.indices.first { i in
                guard !used.contains(i), candidates[i].date >= notBefore else { return false }
                if let firstPath = msg.paths.first {
                    return candidates[i].raw.contains(firstPath)   // photo/video: the exact uploaded path
                }
                guard msg.photos.isEmpty, !normalPending.isEmpty else { return false }  // still uploading
                let echo = candidates[i].normalized
                // Exact, or a shared 30-character start (voice transcripts can differ slightly)
                return echo == normalPending
                    || (normalPending.count >= 30 && echo.hasPrefix(String(normalPending.prefix(30))))
                    || (echo.count >= 30 && normalPending.hasPrefix(String(echo.prefix(30))))
            }
            if let hit {
                used.insert(hit)
                matched.insert(msg.id)
            }
        }
        return matched
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

    /// Returns false (and shows the error banner) when the answer couldn't be delivered.
    func answerQuestion(client: APIClient, id: String, answers: [String: String]) async -> Bool {
        do {
            try await client.answerQuestion(id: id, answers: answers)
            return true
        } catch {
            actionError = "Couldn't send your answer"
            return false
        }
    }

    func sendVoice(client: APIClient, id: String, text: String) async {
        guard !text.isEmpty else { return }
        let ticket = takeTicket()
        defer { finishTurn(ticket) }
        addToHistory(text)
        let msgId = UUID()
        pendingMessages.append(PendingMessage(id: msgId, text: text, sentAt: Date(), delivered: false,
                                              sentWhileBusy: sessionBusy))
        isSending = true
        actionError = nil
        await waitForTurn(ticket)
        do {
            let result = try await client.sendInput(id: id, text: text, sendEnter: true)
            markSent(msgId, leavesNoEcho: result.leavesNoEcho)
        } catch {
            // Keep the pending message visible but mark as failed
            actionError = "Failed to send voice input"
        }
        isSending = false
    }

    /// Upload all photos and videos concurrently, then send ONE input "<p1> <v1 [video: …]> ... <caption>"
    /// so they and the caption arrive as a single turn (the server makes an image block per photo path;
    /// a video contributes its path plus the frame note the server returned).
    func sendPhotos(client: APIClient, id: String, laptop: String? = nil, attachments: [InputBarView.PendingPhoto],
                    caption: String = "") async {
        guard !attachments.isEmpty else { return }
        let hasVideo = attachments.contains(where: \.isVideo)
        let failure = hasVideo
            ? (attachments.count == 1 ? "Video upload failed" : "Uploads failed")
            : (attachments.count == 1 ? "Photo upload failed" : "Photo uploads failed")
        await sendAttachment(client: client, id: id, caption: caption, thumbnails: attachments.map(\.thumbnail),
                             failureMessage: failure) {
            defer { attachments.forEach { $0.discardLocalFile() } }
            return try await withThrowingTaskGroup(of: (Int, String).self) { group in
                for (index, item) in attachments.enumerated() {
                    switch item.content {
                    case .image(let data):
                        group.addTask { (index, try await client.uploadScreenshot(imageData: data, laptop: laptop)) }
                    case .video(let url, _):
                        group.addTask { (index, try await client.uploadVideo(fileURL: url, laptop: laptop).input) }
                    }
                }
                var fragments = [String](repeating: "", count: attachments.count)
                for try await (index, fragment) in group { fragments[index] = fragment }
                return fragments  // pick order preserved
            }
        }
    }

    func sendFile(client: APIClient, id: String, laptop: String? = nil, data: Data, filename: String, caption: String = "") async {
        await sendAttachment(client: client, id: id, caption: caption, failureMessage: "File upload failed") {
            [try await client.uploadFile(data: data, filename: filename, laptop: laptop)]
        }
    }

    private struct EmptyUploadPath: Error {}

    private func sendAttachment(client: APIClient, id: String, caption: String, thumbnails: [UIImage] = [],
                                failureMessage: String, upload: () async throws -> [String]) async {
        let caption = caption.trimmingCharacters(in: .whitespacesAndNewlines)
        let ticket = takeTicket()           // position fixed now; uploads may finish in any order
        defer { finishTurn(ticket) }
        // Show the message optimistically (caption + local photo thumbnails) until Claude echoes it.
        var msgId: UUID?
        if !caption.isEmpty {
            lastSentText = caption
            addToHistory(caption)
        }
        if !caption.isEmpty || !thumbnails.isEmpty {
            let pendingId = UUID()
            pendingMessages.append(PendingMessage(id: pendingId, text: caption, sentAt: Date(), delivered: false,
                                                  photos: thumbnails, sentWhileBusy: sessionBusy))
            msgId = pendingId
        }
        isSending = true
        actionError = nil
        do {
            let paths = try await upload()
            guard !paths.isEmpty, !paths.contains(where: \.isEmpty) else { throw EmptyUploadPath() }
            let text = (paths + (caption.isEmpty ? [] : [caption])).joined(separator: " ")
            if let msgId, let idx = pendingMessages.firstIndex(where: { $0.id == msgId }) {
                pendingMessages[idx].paths = paths
            }
            await waitForTurn(ticket)
            let result = try await client.sendInput(id: id, text: text, sendEnter: true)
            if let msgId { markSent(msgId, leavesNoEcho: result.leavesNoEcho) }
        } catch {
            if !caption.isEmpty, inputText.isEmpty { inputText = caption }
            if let msgId { pendingMessages.removeAll { $0.id == msgId } }
            actionError = failureMessage
        }
        isSending = false
    }
}
