import SwiftUI

struct AskQuestion: Identifiable {
    /// Stable across re-parses (index + text). A fresh UUID per parse changed ForEach identity on
    /// every row evaluation, which reset selections and could spin LazyVStack layout forever.
    var id: String { "\(index)|\(question)" }
    let index: Int
    let header: String
    let question: String
    let options: [(label: String, description: String)]
    var multiSelect = false
}

struct ConversationView: View {
    let events: [SessionEvent]
    let sessionId: String
    var pendingMessages: [PendingMessage] = []
    var onRefresh: (() async -> Void)?
    var onSendInput: ((String) -> Void)?
    /// Send a prompt key to the session: option numbers + "Enter" to answer, "Escape" to dismiss/interrupt
    var onSendKey: ((String) -> Void)?
    /// Answer the pending AskUserQuestion (question text -> answer). Returns false on failure.
    var onAnswer: (([String: String]) async -> Bool)?
    /// Server reports a pending prompt (session.prompt != nil). Only then can an unanswered
    /// AskUserQuestion block be interactive.
    var hasPendingPrompt: Bool = false
    /// A turn is running (shows the "Working…" row).
    var sessionWorking: Bool = false
    /// The running turn is a compaction (the row reads "Compacting conversation…").
    var sessionCompacting: Bool = false
    /// The server's queue for this session: messages held for Claude's next step.
    var queue: [QueuedMessage] = []
    var onEditQueued: ((QueuedMessage, String) -> Void)?
    var onDeleteQueued: ((QueuedMessage) -> Void)?
    /// Interrupt Claude's current step and deliver the whole queue now.
    var onFlushQueue: (() -> Void)?
    /// Older history (docs/design-claude-uplift.md → History): fetch the next older page.
    var onLoadOlder: (() async -> Bool)?
    /// History is loaded back to the first event.
    var historyComplete = true
    /// Render for PDF export / Full Page screenshot: a plain VStack (no scroll view, sheets,
    /// copy buttons, pending/working/queue rows) for ImageRenderer.
    var exportMode = false

    @State private var selectedEvent: SessionEvent?
    @State private var isNearBottom = true
    @State private var pendingQuestionVisible = false
    // Auto-follow: only the user's own scrolling may turn following off. These are written from
    // callbacks and never read in `body` (reading them there caused layout feedback loops).
    @State private var sentinelVisible = true
    @State private var userScrolling = false
    /// Older-history loading waits for the user's own scroll to the top (never fires on the
    /// first layout, when the list is still empty or estimating). Reference type so poking it
    /// re-renders only HistoryTopRow, not this view.
    @State private var historyGate = HistoryLoadGate()
    /// Progressive reveal (docs/design-claude-uplift.md → History): rows render from the row that
    /// holds the event `revealFrom`, plus `revealAbove` rows above it. Set when rows first arrive
    /// (the newest `revealChunk` rows); it only grows, so new rows at the bottom never push rows
    /// off the top, and older pages prepended to the events stay hidden until revealed.
    @State private var revealFrom: String?
    @State private var revealAbove = 0
    /// Bumped after each reveal: log it, and check again whether the reader is still near the top.
    @State private var revealTick = 0
    @State private var reveal = RevealState()
    /// Work groups (docs/design-claude-uplift.md → Work groups): the user's own toggles, which
    /// win over everything, and the groups of the running turn, auto-expanded while it runs.
    @State private var groupOverride: [String: Bool] = [:]
    @State private var liveExpanded: Set<String> = []
    /// Thinking blocks are expanded by default; ids the user collapsed.
    @State private var thinkingCollapsed: Set<String> = []
    /// Whether the list is moving (or just stopped). A tap that stops a fling must not open a row.
    @State private var scrollMotion = ScrollMotion()
    /// The peer thread opened from a From/To card.
    @State private var peerThread: PeerThreadTarget?

    /// Merge events and pending messages into a single timeline.
    /// Pending messages appear at the bottom. The view model removes each one as soon as its
    /// transcript copy arrives, which then renders at its server-time position.
    private var timelineItems: [(id: String, kind: TimelineKind)] {
        var items: [(id: String, kind: TimelineKind)] = []

        // Group subagent events by their parent tool_use ID
        var subagentEvents: [String: [SessionEvent]] = [:]  // parentToolUseId -> events
        for event in events {
            if let info = event.subagentInfo {
                subagentEvents[info.parentToolUseId, default: []].append(event)
            }
        }

        // Turn timings after turns that showed nothing (stacked "Turn: …" rows) are dropped
        let hiddenTurnRows = TurnDurationFilter.hiddenDurationEvents(in: events.filter { $0.subagentInfo == nil })
        // A peer send's result shows as the To card's status, not as its own row
        let peerSends = Self.peerSendToolIds(events)

        // Events in server-time order, with subagent events grouped under Agent blocks
        for event in events {
            // Skip individual subagent events (they're rendered inside the group)
            if event.subagentInfo != nil { continue }
            if hiddenTurnRows.contains(event.uuid) { continue }
            if Self.isPeerDeliveryOnly(event, sends: peerSends) { continue }

            // If this is an Agent tool_use with subagent events, bundle them
            if event.type == .toolUse, event.toolUse?.name == "Agent",
               let subs = subagentEvents[event.toolUse?.id ?? ""], !subs.isEmpty {
                items.append((id: event.uuid, kind: .agentGroup(toolUse: event, subEvents: subs)))
            } else {
                items.append((id: event.uuid, kind: .event(event)))
            }
        }

        items = Self.foldWork(items, lastTurnRunning: sessionWorking && !exportMode)
        items = Self.foldPeerExchanges(items, lastTurnRunning: sessionWorking && !exportMode,
                                       waitingOnYou: hasPendingPrompt)
        if exportMode { return items }

        // Pending messages at the bottom (removed by the view model once Claude's copy arrives)
        for msg in pendingMessages.sorted(by: { $0.sentAt < $1.sentAt }) {
            items.append((id: msg.id.uuidString, kind: .pending(msg)))
        }

        // A turn is running. Shown even when it has no visible output yet (e.g. Claude woke up
        // for a background-task notification), so queued messages have an obvious reason.
        if sessionWorking {
            items.append((id: "working-indicator", kind: .working))
        }

        // The server's queue, always last. One row with a fixed id (items keyed by their server id).
        if !queue.isEmpty {
            items.append((id: "queue-group", kind: .queue(queue)))
        }

        return items
    }

    private enum TimelineKind {
        case event(SessionEvent)
        case pending(PendingMessage)
        case working
        case queue([QueuedMessage])
        case agentGroup(toolUse: SessionEvent, subEvents: [SessionEvent])
        /// Folded tool calls / results / narration (never nested).
        indirect case workGroup([(id: String, kind: TimelineKind)])
        /// Back-to-back peer turns folded into one row (docs/plan-peer-messages.md → Peer exchanges).
        /// Members are top-level rows, work groups included. `running`: its last turn is running.
        indirect case peerExchange([(id: String, kind: TimelineKind)], running: Bool)
    }

    private enum FoldRole { case boundary, visible, text, work }

    private static func foldRole(_ kind: TimelineKind) -> FoldRole {
        switch kind {
        case .agentGroup: return .work
        case .event(let e):
            switch e.type {
            case .user: return .boundary
            case .text: return .text
            // A peer send is a standalone To card: never folded, and it splits the run around it
            case .toolUse: return e.toolUse?.name == "AskUserQuestion" || isPeerSend(e) ? .visible : .work
            // An auto-mode block is a standalone Blocked card: never folded
            case .toolResult: return AutoModeBlock.inEvent(e) ? .visible : .work
            case .thinking, .system, .compaction: return .visible
            }
        default: return .visible
        }
    }

    // MARK: Peer messages (docs/plan-peer-messages.md → iOS app)

    /// A `SendMessage` the server recognized as cross-session (it attached `peerMessages`).
    private static func isPeerSend(_ e: SessionEvent) -> Bool {
        e.type == .toolUse && e.toolUse != nil && !(e.peerMessages ?? []).isEmpty
    }

    private static func peerSendToolIds(_ events: [SessionEvent]) -> Set<String> {
        Set(events.lazy.filter { $0.subagentInfo == nil && isPeerSend($0) }.compactMap { $0.toolUse?.id })
    }

    /// A tool_result that only carries a peer send's delivery (shown on its To card instead).
    private static func isPeerDeliveryOnly(_ e: SessionEvent, sends: Set<String>) -> Bool {
        guard e.type == .toolResult, let toolId = e.peerDelivery?.toolUseId, sends.contains(toolId) else { return false }
        return (e.toolResults ?? []).allSatisfy { $0.toolUseId == toolId }
    }

    /// A To card's status: the paired tool_result's `peerDelivery` (or, from a server that didn't
    /// enrich it, its error flag), else the message's own status, else still sending.
    private func peerSendStatus(_ event: SessionEvent, _ message: PeerMessage) -> (status: PeerStatus, error: String?) {
        if let toolId = event.toolUse?.id {
            for e in events.reversed() {
                if let d = e.peerDelivery, d.toolUseId == toolId {
                    return d.failed ? (.failed, d.error) : (.delivered, nil)
                }
                if let r = e.toolResults?.first(where: { $0.toolUseId == toolId }) {
                    return r.isError ? (.failed, r.content.isEmpty ? nil : r.content) : (.delivered, nil)
                }
            }
        }
        switch message.peerStatus {
        case .delivered: return (.delivered, nil)
        case .failed: return (.failed, message.error)
        default: return (.sending, nil)
        }
    }

    private func openPeerThread(_ message: PeerMessage) {
        guard let uuid = message.peer?.claudeSessionId else { return }
        peerThread = PeerThreadTarget(peerUuid: uuid, focusMessageId: message.id, peerHint: message.peer)
    }

    @ViewBuilder
    private func peerCards(_ event: SessionEvent) -> some View {
        let messages = event.peerMessages ?? []
        VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(messages.enumerated()), id: \.offset) { _, message in
                if event.type == .toolUse {
                    let s = peerSendStatus(event, message)
                    PeerCard(message: message, status: s.status, error: s.error,
                             onOpenThread: exportMode ? nil : { openPeerThread($0) },
                             canTap: { !scrollMotion.isSettling })
                } else {
                    PeerCard(message: message,
                             onOpenThread: exportMode ? nil : { openPeerThread($0) },
                             canTap: { !scrollMotion.isSettling })
                }
            }
            if event.type == .user, let other = event.peerOtherText, !other.isEmpty {
                userBubble(SessionEvent(uuid: event.uuid, timestamp: event.timestamp, type: .user,
                                        text: other, thinking: nil, toolUse: nil,
                                        toolResults: nil, model: nil, durationMs: nil))
            }
        }
    }

    /// Fold each run of *consecutive* tool calls and tool results into one work group. Any other
    /// row (assistant text, even between tool calls; thinking; user events; questions;
    /// compaction; turn times) ends the run and stays in the transcript. Text never moves into or
    /// out of a group, so nothing changes identity when a turn ends (that left the LazyVStack
    /// blank until scrolled).
    private static func foldWork(_ items: [(id: String, kind: TimelineKind)],
                                 lastTurnRunning: Bool) -> [(id: String, kind: TimelineKind)] {
        var out: [(id: String, kind: TimelineKind)] = []
        var run: [(id: String, kind: TimelineKind)] = []
        func flush() {
            if !run.isEmpty { out.append((id: "wg-\(run[0].id)", kind: .workGroup(run))) }
            run = []
        }
        for item in items {
            if foldRole(item.kind) == .work {
                run.append(item)
            } else {
                flush()
                out.append(item)
            }
        }
        flush()
        return out
    }

    // MARK: Peer exchanges (docs/plan-peer-messages.md → Peer exchanges)

    /// A user event the server marked as starting a quiet peer turn (not a mid-turn delivery, not
    /// a reply to a message sent on your behalf).
    private static func startsQuietPeerTurn(_ e: SessionEvent) -> Bool {
        e.type == .user && e.subagentInfo == nil && e.quiet == true
    }

    /// A row that needs you (a question, a plan, a Blocked card): its turn never folds.
    private static func needsYou(_ kind: TimelineKind) -> Bool {
        switch kind {
        case .event(let e):
            if e.type == .toolUse, e.toolUse?.name == "AskUserQuestion" || e.toolUse?.name == "ExitPlanMode" { return true }
            return e.type == .toolResult && AutoModeBlock.inEvent(e)
        case .workGroup(let members): return members.contains { needsYou($0.kind) }
        default: return false
        }
    }

    /// Fold each run of back-to-back peer turns into one exchange row. A turn is a user event and
    /// the rows up to the next one; it folds when the server marked its user event `quiet` and
    /// nothing in it needs you. The last turn doesn't fold while the session waits on you.
    private static func foldPeerExchanges(_ items: [(id: String, kind: TimelineKind)], lastTurnRunning: Bool,
                                          waitingOnYou: Bool) -> [(id: String, kind: TimelineKind)] {
        // Split into turns: rows before the first user event, then one turn per user event
        var turns: [[(id: String, kind: TimelineKind)]] = [[]]
        for item in items {
            if case .event(let e) = item.kind, e.type == .user { turns.append([item]) } else { turns[turns.count - 1].append(item) }
        }
        var out: [(id: String, kind: TimelineKind)] = []
        var run: [(id: String, kind: TimelineKind)] = []
        func flush(running: Bool) {
            if !run.isEmpty { out.append((id: "px-\(run[0].id)", kind: .peerExchange(run, running: running))) }
            run = []
        }
        for (i, turn) in turns.enumerated() {
            let isLast = i == turns.count - 1
            let folds: Bool = {
                guard i > 0, case .event(let first) = turn[0].kind, startsQuietPeerTurn(first) else { return false }
                if isLast, waitingOnYou { return false }
                return !turn.contains { needsYou($0.kind) }
            }()
            if folds {
                run.append(contentsOf: turn)
            } else {
                flush(running: false)
                out.append(contentsOf: turn)
            }
        }
        flush(running: lastTurnRunning)
        return out
    }

    /// The peer messages in an exchange's rows, in order.
    private static func exchangeMessages(_ members: [(id: String, kind: TimelineKind)]) -> [PeerMessage] {
        members.flatMap { member -> [PeerMessage] in
            if case .event(let e) = member.kind { return e.peerMessages ?? [] }
            return []
        }
    }

    /// Work groups in the turn that's running (after the last user event), oldest first.
    private static func liveGroupIds(_ items: [(id: String, kind: TimelineKind)], running: Bool) -> [String] {
        guard running else { return [] }
        var ids: [String] = []
        for item in items {
            if case .event(let e) = item.kind, e.type == .user { ids = [] }
            if case .workGroup = item.kind { ids.append(item.id) }
        }
        return ids
    }

    /// tool_use id -> its tool_result, for deciding whether an AskUserQuestion was answered.
    private var toolResultsById: [String: ToolResultBlock] {
        var map: [String: ToolResultBlock] = [:]
        for event in events {
            for result in event.toolResults ?? [] { map[result.toolUseId] = result }
        }
        return map
    }

    /// The AskUserQuestion that is actually waiting: latest one with no tool_result yet,
    /// and only while the server reports a pending prompt.
    private var pendingQuestionEventId: String? {
        guard hasPendingPrompt else { return nil }
        let results = toolResultsById
        guard let event = events.last(where: { $0.subagentInfo == nil && $0.type == .toolUse && $0.toolUse?.name == "AskUserQuestion" }),
              let toolId = event.toolUse?.id, results[toolId] == nil else { return nil }
        return event.uuid
    }

    /// Open a row's detail sheet, unless the tap was the one that stopped a scroll (iOS reads a
    /// tap on a moving list as "stop", and so should we).
    private func openDetail(_ event: SessionEvent) {
        guard !scrollMotion.isSettling else { return }
        selectedEvent = event
    }

    /// Scroll to the bottom and keep it there while rows settle. A LazyVStack only knows
    /// estimated heights for rows it hasn't built, so the first jump can land short of the end
    /// (the conversation opening mid-way) or past it (a blank screen). Re-pin a few times as the
    /// newly built rows report their real heights, until the bottom sentinel is on screen.
    /// `reason` is telemetry only (docs/plan-observability.md → Blank screen probe, scroll reasons).
    private func pinToBottom(_ proxy: ScrollViewProxy, animated: Bool, reason: String) {
        TranscriptProbe.shared.note(.scrolls, ["source": "pin", "animated": animated, "reason": reason])   // telemetry
        if animated {
            withAnimation { proxy.scrollTo("bottom", anchor: .bottomLeading) }
        } else {
            proxy.scrollTo("bottom", anchor: .bottomLeading)
        }
        Task { @MainActor in
            for delay in [120, 250, 500] {
                try? await Task.sleep(for: .milliseconds(delay))
                guard isNearBottom, !userScrolling else { return }
                if sentinelVisible { continue }   // looks settled; check again in case rows grow
                TranscriptProbe.shared.note(.scrolls, ["source": "repin", "delay_ms": delay])   // telemetry
                proxy.scrollTo("bottom", anchor: .bottomLeading)
            }
        }
    }

    /// Short row kind for the alignment probe (telemetry).
    private static func probeKind(_ kind: TimelineKind) -> String {
        switch kind {
        case .event(let e): "event:\(e.type)"
        case .pending: "pending"
        case .working: "working"
        case .queue: "queue"
        case .agentGroup: "agent_group"
        case .workGroup: "work_group"
        case .peerExchange: "peer_exchange"
        }
    }

    @ViewBuilder
    /// `liveGroupId`: the newest work group of the running turn (gets the pulsing dot).
    private func timelineRow(_ item: (id: String, kind: TimelineKind), liveGroupId: String? = nil) -> some View {
        if case .workGroup(let members) = item.kind {
            workGroupBlock(id: item.id, members: members, live: item.id == liveGroupId)
        } else if case .peerExchange(let members, let running) = item.kind {
            peerExchangeBlock(id: item.id, members: members, running: running, liveGroupId: liveGroupId)
        } else {
            memberRow(item)
        }
    }

    @ViewBuilder
    private func memberRow(_ item: (id: String, kind: TimelineKind)) -> some View {
        switch item.kind {
        case .workGroup, .peerExchange:
            EmptyView()   // rendered by timelineRow
        case .event(let event):
            eventRow(event)
        case .pending(let msg):
            pendingBubble(msg)
        case .working:
            WorkingIndicatorRow(compacting: sessionCompacting)
        case .queue(let queued):
            QueueGroupView(items: queued,
                           onEdit: { item, text in onEditQueued?(item, text) },
                           onDelete: { item in onDeleteQueued?(item) },
                           onFlush: { onFlushQueue?() })
        case .agentGroup(let toolUse, let subEvents):
            agentGroupBlock(toolUse: toolUse, subEvents: subEvents)
        }
    }

    var body: some View {
        if exportMode {
            exportBody
        } else {
            liveBody
        }
    }

    /// Non-lazy, non-scrolling transcript for ImageRenderer.
    private var exportBody: some View {
        VStack(alignment: .leading, spacing: 2) {
            ForEach(timelineItems, id: \.id) { item in
                timelineRow(item)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .environment(\.staticTextRendering, true)
    }

    private var liveBody: some View {
        let allItems = timelineItems
        reveal.latestRows = allItems
        reveal.historyComplete = historyComplete
        let liveIds = Self.liveGroupIds(allItems, running: sessionWorking)
        // Only the revealed rows render (docs/design-claude-uplift.md → History)
        let firstShown = revealStart(allItems)
        let items = firstShown > 0 ? Array(allItems[firstShown...]) : allItems
        return ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 2) {
                    if onLoadOlder != nil {
                        HistoryTopRow(complete: historyComplete && firstShown == 0, hasEvents: !events.isEmpty,
                                      gate: historyGate) {
                            // The reader reached the top. Keep their place: re-anchor on the row
                            // that was first. If the bottom is on screen too (a short
                            // conversation), stay at the bottom.
                            let anchor = items.first?.id
                            let added = await revealOlder(source: "top_row")
                            if added {
                                // After the prepended rows are laid out, not in the same update
                                try? await Task.sleep(for: .milliseconds(30))
                                if sentinelVisible {
                                    TranscriptProbe.shared.note(.scrolls, ["source": "history_bottom"])   // telemetry
                                    proxy.scrollTo("bottom", anchor: .bottomLeading)
                                } else if let anchor {
                                    TranscriptProbe.shared.note(.scrolls, ["source": "history_anchor"])   // telemetry
                                    proxy.scrollTo(anchor, anchor: .topLeading)
                                }
                            }
                            return added
                        }
                        .padding(.horizontal, 16)
                    }
                    ForEach(items, id: \.id) { item in
                        timelineRow(item, liveGroupId: liveIds.last)
                            .padding(.horizontal, 16)
                            .transcriptRowProbe(id: item.id, kind: Self.probeKind(item.kind))   // telemetry
                    }
                    Color.clear.frame(height: 1).id("bottom")
                        .onAppear {
                            sentinelVisible = true
                            isNearBottom = true
                            TranscriptProbe.shared.bottomSentinel(true)   // telemetry
                        }
                        .onDisappear {
                            sentinelVisible = false
                            TranscriptProbe.shared.bottomSentinel(false)   // telemetry
                            // New content pushing the bottom off screen must not stop following;
                            // only a user scroll does (tracked via scroll phase on iOS 18+).
                            if !Self.tracksScrollPhase || userScrolling { isNearBottom = false }
                        }
                }
                // The 16pt side padding is inside each row (above), not around the stack, so every
                // scroll target (each row and the bottom sentinel) starts at x = 0. With the padding
                // around the stack, a leading-anchored scrollTo lined a target's left edge (x = 16)
                // up with the screen's and left the list scrolled 16pt sideways: the "chat stuck
                // against the left edge" glitch (seen in telemetry, 2026-11-08: offset_x = 16 with
                // content as wide as the screen).
                .padding(.vertical, 8)
                .background(ScrollViewLocator(state: reveal))   // progressive reveal keeps the reader's place
                // Pin the column to the screen width. If any row came out wider, the stack grew
                // and the (horizontally centred) bottom anchor shifted every row left until a
                // re-layout.
                .containerRelativeFrame(.horizontal, alignment: .leading)
                .transcriptColumnProbe()   // telemetry: left-edge glitch evidence
            }
            // Leading anchors, here and in every scrollTo: a centred anchor (.bottom/.top) splits any
            // excess content width, shifting the whole column left by its padding (the
            // "left-adjusted" glitch).
            .defaultScrollAnchor(.bottomLeading)
            .transcriptViewportProbe(sessionId: sessionId, eventCount: events.count)   // telemetry
            .background(Theme.bgPage)
            .refreshable {
                await onRefresh?()
            }
            .onChange(of: liveIds, initial: true) { old, new in
                if !new.isEmpty {
                    // The running turn's groups stay open so the work can be watched
                    liveExpanded.formUnion(new)
                }
                // A finished turn's groups stay open for the life of the screen; they start
                // collapsed the next time the session opens. Folding them in place while the
                // reader followed the bottom stranded the lazy stack: it kept the old, taller
                // content height and left the offset where it builds no rows, a blank screen that
                // no scroll call recovered (Ops F-13; reproduced in the simulator, and a fold with
                // or without animation, delayed, re-pinned or not all blanked; no fold never did).
            }
            .onChange(of: events.count) { old, _ in
                // First load (0 → N): jump, don't animate across rows that were never laid out
                if isNearBottom { pinToBottom(proxy, animated: old > 0, reason: old > 0 ? "events" : "first_load") }
            }
            // Progressive reveal: fix the window when rows arrive, reveal as the reader nears the top
            .modifier(RevealTriggers(eventCount: events.count, firstEventId: events.first?.uuid, tick: revealTick,
                                     settle: eventsChanged, state: reveal, nearTopChanged: nearTopChanged,
                                     revealed: afterReveal))
            .onChange(of: events.last?.text?.count ?? 0) {
                // Streaming growth of the last message
                if isNearBottom {
                    TranscriptProbe.shared.note(.scrolls, ["source": "stream_follow"])   // telemetry
                    proxy.scrollTo("bottom", anchor: .bottomLeading)
                }
            }
            .onChange(of: pendingMessages.count) { old, new in
                // The user just sent something: always jump to it and resume following
                guard new > old else { return }
                isNearBottom = true
                pinToBottom(proxy, animated: true, reason: "send")
            }
            .onChange(of: queue.count) { old, new in
                // A sent message landed in the server queue (its local bubble was just removed)
                guard new > old, isNearBottom else { return }
                pinToBottom(proxy, animated: true, reason: "queue")
            }
            .modifier(UserScrollPhaseTracking(userScrolling: $userScrolling, scrollMotion: scrollMotion, onUserScrollStarted: {
                historyGate.userHasScrolled = true
                // Already near the top when the reader first scrolls (a short window)
                if reveal.nearTop { Task { await revealOlder(source: "scroll") } }
            }) {
                // A user scroll ended: follow again only if they ended at the bottom
                isNearBottom = sentinelVisible
                // ...and if they ended at the top, load older history
                historyGate.scrollEnded += 1
            })
            // Blank screen: lazy rows realized at their real height can leave the offset past
            // the end of the content. Snap back to the bottom when that happens on its own.
            .modifier(OverscrollRecovery(isUserScrolling: { userScrolling }) {
                TranscriptProbe.shared.note(.scrolls, ["source": "overscroll_recovery"])   // telemetry
                proxy.scrollTo("bottom", anchor: .bottomLeading)
            })
            .onChange(of: pendingQuestionEventId) { _, newId in
                // A question just became pending: bring it into view if the user is at the bottom;
                // otherwise the "Question waiting" pill offers a jump.
                guard let newId, isNearBottom else { return }
                TranscriptProbe.shared.note(.scrolls, ["source": "question"])   // telemetry
                withAnimation { proxy.scrollTo(newId, anchor: .topLeading) }
            }
            .overlay(alignment: .bottom) {
                // Separate view so only the pill (not the whole timeline) re-renders when the
                // bottom sentinel / question visibility flips; reading that state here caused a
                // LazyVStack layout feedback loop.
                QuestionWaitingPill(questionId: pendingQuestionEventId,
                                    isNearBottom: $isNearBottom,
                                    questionVisible: $pendingQuestionVisible) { id in
                    TranscriptProbe.shared.note(.scrolls, ["source": "question_pill"])   // telemetry
                    withAnimation { proxy.scrollTo(id, anchor: .topLeading) }
                }
            }
        }
        .sheet(item: $selectedEvent) { event in
            EventDetailSheet(event: event)
        }
        .sheet(item: $peerThread) { target in
            PeerThreadSheet(sessionId: sessionId, target: target)
        }
    }

    @ViewBuilder
    private func eventRow(_ event: SessionEvent) -> some View {
        switch event.type {
        case .user:
            // Only show blue bubble for user messages with actual text
            // Skip tool-result-only user events (no visible content)
            if !(event.peerMessages ?? []).isEmpty {
                // Cross-session messages: From cards, then any other text as a normal bubble
                peerCards(event)
            } else if let text = event.text, text.hasPrefix("[Request interrupted") {
                interruptedNotice
            } else if let text = event.text, let parsed = FindingParser.cards(in: text) {
                // Ops findings (docs/plan-observability.md → Delivery: the Finding card)
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(Array(parsed.findings.enumerated()), id: \.offset) { FindingCard(finding: $0.element) }
                    if !parsed.remainder.isEmpty {
                        userBubble(SessionEvent(uuid: event.uuid, timestamp: event.timestamp, type: .user,
                                                text: parsed.remainder, thinking: nil, toolUse: nil,
                                                toolResults: nil, model: nil, durationMs: nil))
                    }
                }
            } else if let text = event.text, TaskNotificationParser.containsNotice(text) {
                // Background-task notifications Claude Code injects: compact cards, not a user bubble
                let parsed = TaskNotificationParser.parse(text)
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(parsed.notices) { notice in
                        TaskNotificationCard(notice: notice)
                    }
                    if !parsed.remainder.isEmpty {
                        userBubble(SessionEvent(uuid: event.uuid, timestamp: event.timestamp, type: .user,
                                                text: parsed.remainder, thinking: nil, toolUse: nil,
                                                toolResults: nil, model: nil, durationMs: nil))
                    }
                }
            } else if event.text != nil && !(event.text?.isEmpty ?? true) {
                userBubble(event)
            }
        case .text:
            assistantTextBlock(event)
        case .thinking:
            thinkingBlock(event)
        case .toolUse:
            if event.toolUse?.name == "AskUserQuestion" {
                askUserQuestionBlock(event)
            } else if Self.isPeerSend(event) {
                peerCards(event)
            } else {
                toolUseBlock(event)
            }
        case .toolResult:
            if AutoModeBlock.inEvent(event) {
                blockedBlocks(event)
            } else {
                toolResultBlock(event)
            }
        case .system:
            systemBlock(event)
        case .compaction:
            compactionBlock(event)
        }
    }

    // MARK: - Pending message (sent, waiting for event stream)

    private func pendingBubble(_ msg: PendingMessage) -> some View {
        HStack {
            Spacer(minLength: 40)
            VStack(alignment: .trailing, spacing: 6) {
                if !msg.photos.isEmpty {
                    PendingPhotoStrip(images: msg.photos)
                }
                if !msg.text.isEmpty {
                    Text(FindingParser.compactText(msg.text))
                        .font(.claudeBody)
                        .foregroundStyle(msg.delivered ? Theme.textPrimary : Theme.textMuted)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 10)
                        .background(Theme.bgSunken,
                                    in: RoundedRectangle(cornerRadius: Theme.radiusBubble, style: .continuous))
                        .overlay(
                            // Dashed outline = not yet accepted by Claude
                            RoundedRectangle(cornerRadius: Theme.radiusBubble, style: .continuous)
                                .strokeBorder(Theme.textMuted.opacity(0.45), style: StrokeStyle(lineWidth: 1, dash: [4, 3]))
                        )
                }
                pendingStatus(msg)
            }
        }
        .padding(.top, 12)
        .padding(.bottom, 6)
    }

    /// Uploading… / Sending… → Sent ✓ (handed to Claude, waiting for its echo) → accepted (bubble
    /// becomes a normal message). Messages the server queues leave this list and show in the
    /// queue group instead.
    @ViewBuilder
    private func pendingStatus(_ msg: PendingMessage) -> some View {
        HStack(spacing: 6) {
            if !msg.delivered {
                ProgressView()
                    .scaleEffect(0.6)
                    .tint(Theme.textMuted)
                Text(msg.photos.isEmpty || !msg.paths.isEmpty ? "Sending…" : "Uploading…")
            } else {
                Text("Sent")
                Image(systemName: "checkmark")
                    .font(.system(size: 10, weight: .semibold))
            }
        }
        .font(.claudeFootnote)
        .foregroundStyle(Theme.textMuted)
    }

    /// CLI marker after an interrupt ("[Request interrupted by user…]") — not something the user typed.
    private var interruptedNotice: some View {
        HStack(spacing: 6) {
            Spacer()
            Image(systemName: "stop.circle")
                .font(.system(size: 11, weight: .regular))
            Text("Interrupted")
            Spacer()
        }
        .font(.claudeFootnote)
        .foregroundStyle(Theme.textMuted)
        .padding(.vertical, 6)
    }

    // MARK: - User message

    /// Split user text into all attachment fragments (in order) and the remaining message text.
    /// A fragment is a photo path, or a video path plus its "[video: …]" note (see UserAttachment).
    fileprivate static func parseUserContent(_ text: String) -> (photoPaths: [String], message: String) {
        let parsed = UserAttachment.parse(text)
        return (parsed.fragments, parsed.message)
    }

    private func userBubble(_ event: SessionEvent) -> some View {
        let parsed = Self.parseUserContent(event.text ?? "")
        return HStack {
            Spacer(minLength: 40)
            VStack(alignment: .trailing, spacing: 6) {
                // Attached photos sit above the bubble as real thumbnails (fetched from the
                // server's /screenshots/<file>); tap one to view it full screen.
                if !parsed.photoPaths.isEmpty {
                    SentPhotoStrip(paths: parsed.photoPaths)
                }
                if !parsed.message.isEmpty {
                    // Range-selectable; a plain tap opens the detail sheet (see SelectableText)
                    SelectableText(text: RichText.plain(parsed.message, font: .systemFont(ofSize: 16),
                                                        color: Theme.textPrimary),
                                   hugsContent: true,
                                   onTap: { openDetail(event) })
                        .padding(.horizontal, 14)
                        .padding(.vertical, 10)
                        .background(Theme.bgSunken,
                                    in: RoundedRectangle(cornerRadius: Theme.radiusBubble, style: .continuous))
                }
            }
        }
        .padding(.top, 12)
        .padding(.bottom, 6)
    }

    // MARK: - Assistant text

    private func assistantTextBlock(_ event: SessionEvent) -> some View {
        let raw = event.text ?? ""
        let segments = splitMarkdownSegments(raw)
        let open = { openDetail(event) }
        return VStack(alignment: .leading, spacing: 10) {
            ForEach(Array(segments.enumerated()), id: \.offset) { _, segment in
                switch segment {
                case .text(let str):
                    SelectableText(text: RichText.prose(Self.bulletize(str)), onTap: open)
                case .header(let level, let text):
                    SelectableText(text: RichText.plain(text, font: RichText.serif(level <= 1 ? 24 : level == 2 ? 21 : 19,
                                                                                  weight: .semibold),
                                                        color: Theme.textPrimary),
                                   onTap: open)
                        .padding(.top, level <= 2 ? 6 : 2)
                case .table(let headers, let rows):
                    markdownTableView(headers: headers, rows: rows)
                        .onTapGesture(perform: open)
                case .codeBlock(let language, let code):
                    codeBlockView(language: language, code: code, onTap: open)
                }
            }
        }
        .tint(Theme.link)
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.vertical, 6)
        // Taps are handled per text block (SelectableText), so a tap that only dismisses a
        // selection doesn't also open the detail sheet.
    }

    /// Inline-only markdown with `code` spans rendered as mono on the sunken surface.
    static func inlineMarkdown(_ str: String, codeSize: CGFloat) -> AttributedString? {
        guard var md = try? AttributedString(markdown: str, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)) else {
            return nil
        }
        let codeRanges = md.runs
            .filter { $0.inlinePresentationIntent?.contains(.code) == true }
            .map(\.range)
        for range in codeRanges {
            md[range].font = .claudeMono(codeSize)
            md[range].backgroundColor = Theme.bgSunken
        }
        return md
    }

    /// Render "- item" / "* item" list markers as bullets (visual only; text is otherwise unchanged).
    private static func bulletize(_ text: String) -> String {
        guard text.contains("- ") || text.contains("* ") else { return text }
        return text.components(separatedBy: "\n").map { (line: String) -> String in
            let indent = line.prefix { $0 == " " || $0 == "\t" }
            let rest = line.dropFirst(indent.count)
            if rest.hasPrefix("- ") || rest.hasPrefix("* ") {
                return String(indent) + "•  " + String(rest.dropFirst(2))
            }
            return line
        }.joined(separator: "\n")
    }

    // MARK: - Markdown table parsing & rendering

    private enum MarkdownSegment {
        case text(String)
        case header(level: Int, text: String)
        case table(headers: [String], rows: [[String]])
        case codeBlock(language: String, code: String)
    }

    private func splitMarkdownSegments(_ text: String) -> [MarkdownSegment] {
        let lines = text.components(separatedBy: "\n")
        var segments: [MarkdownSegment] = []
        var textLines: [String] = []
        var i = 0

        while i < lines.count {
            let trimmed = lines[i].trimmingCharacters(in: .whitespaces)

            // Detect fenced code block: ```lang
            if trimmed.hasPrefix("```") {
                // Flush accumulated text
                if !textLines.isEmpty {
                    let joined = textLines.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
                    if !joined.isEmpty { segments.append(.text(joined)) }
                    textLines = []
                }

                let language = String(trimmed.dropFirst(3)).trimmingCharacters(in: .whitespaces)
                var codeLines: [String] = []
                i += 1
                while i < lines.count {
                    if lines[i].trimmingCharacters(in: .whitespaces).hasPrefix("```") {
                        i += 1
                        break
                    }
                    codeLines.append(lines[i])
                    i += 1
                }
                let code = codeLines.joined(separator: "\n")
                if !code.isEmpty {
                    segments.append(.codeBlock(language: language, code: code))
                }
            }
            // Detect table: line with pipes, followed by separator line (|---|---|)
            else if i + 1 < lines.count,
               lines[i].contains("|"),
               lines[i + 1].contains("|"),
               lines[i + 1].contains("---") {
                // Flush accumulated text
                if !textLines.isEmpty {
                    let joined = textLines.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
                    if !joined.isEmpty { segments.append(.text(joined)) }
                    textLines = []
                }

                let headers = parseTableRow(lines[i])
                var rows: [[String]] = []
                i += 2  // skip header + separator
                while i < lines.count, lines[i].contains("|") {
                    let row = parseTableRow(lines[i])
                    if !row.isEmpty { rows.append(row) }
                    i += 1
                }
                segments.append(.table(headers: headers, rows: rows))
            }
            // Detect headers: # H1, ## H2, ### H3, etc.
            else if trimmed.hasPrefix("#") {
                // Flush accumulated text
                if !textLines.isEmpty {
                    let joined = textLines.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
                    if !joined.isEmpty { segments.append(.text(joined)) }
                    textLines = []
                }
                let level = trimmed.prefix(while: { $0 == "#" }).count
                let headerText = String(trimmed.dropFirst(level)).trimmingCharacters(in: .whitespaces)
                if !headerText.isEmpty {
                    segments.append(.header(level: min(level, 6), text: headerText))
                }
                i += 1
            } else {
                textLines.append(lines[i])
                i += 1
            }
        }

        if !textLines.isEmpty {
            let joined = textLines.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
            if !joined.isEmpty { segments.append(.text(joined)) }
        }

        return segments
    }

    private func parseTableRow(_ line: String) -> [String] {
        line.split(separator: "|", omittingEmptySubsequences: false)
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }  // drop empty leading/trailing from outer pipes
    }

    /// Horizontal scroller on screen; in export mode the content is laid out in place, because
    /// ImageRenderer can't draw scroll views (they come out empty).
    @ViewBuilder
    private func horizontalScroll<Content: View>(@ViewBuilder _ content: () -> Content) -> some View {
        if exportMode {
            content()
        } else {
            ScrollView(.horizontal, showsIndicators: true) { content() }
        }
    }

    private func markdownTableView(headers: [String], rows: [[String]]) -> some View {
        horizontalScroll {
            Grid(alignment: .leading, horizontalSpacing: 0, verticalSpacing: 0) {
                // Header row
                GridRow {
                    ForEach(Array(headers.enumerated()), id: \.offset) { _, header in
                        Text(header)
                            .font(.claudeCaptionSemibold)
                            .foregroundStyle(Theme.textPrimary)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 8)
                    }
                }
                .background(Theme.bgSunken)

                Theme.border.frame(height: 0.5)

                // Data rows
                ForEach(Array(rows.enumerated()), id: \.offset) { rowIdx, row in
                    GridRow {
                        ForEach(Array(row.enumerated()), id: \.offset) { _, cell in
                            Group {
                                if let md = Self.inlineMarkdown(cell, codeSize: 12) {
                                    Text(md)
                                } else {
                                    Text(cell)
                                }
                            }
                            .font(.claudeCaption)
                            .foregroundStyle(Theme.textPrimary)
                            .textSelection(.enabled)  // per-cell copy (long-press → Copy)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 6)
                        }
                    }
                    .background(rowIdx % 2 == 1 ? Theme.bgSunken.opacity(0.5) : Theme.bgRaised)

                    if rowIdx < rows.count - 1 {
                        Theme.border.frame(height: 0.5)
                    }
                }
            }
            .clipShape(RoundedRectangle(cornerRadius: Theme.radiusSmall, style: .continuous))
            .claudeHairline(radius: Theme.radiusSmall)
        }
        .padding(.vertical, 4)
    }

    private func codeBlockView(language: String, code: String, onTap: @escaping () -> Void) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            if !language.isEmpty {
                Text(language)
                    .font(.claudeFootnote)
                    .foregroundStyle(Theme.textMuted)
                    .padding(.horizontal, 12)
                    .padding(.top, 8)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            horizontalScroll {
                // On screen no width is proposed here, so SelectableText sizes to the longest line
                // (no wrapping); in exports long lines wrap at the page width instead.
                SelectableText(text: RichText.plain(code, font: RichText.mono(13), color: Theme.textPrimary),
                               onTap: onTap)
                    .padding(12)
                    .frame(maxWidth: exportMode ? .infinity : nil, alignment: .leading)
            }
        }
        .background(Theme.bgSunken)
        .clipShape(RoundedRectangle(cornerRadius: Theme.radiusSmall, style: .continuous))
        .claudeHairline(radius: Theme.radiusSmall)
        .padding(.vertical, 2)
    }

    // MARK: - Thinking (collapsible)

    /// Expanded by default (docs/design-claude-uplift.md → Work groups and thinking): muted
    /// italic serif with a left rule. Tapping the header collapses it.
    private func thinkingBlock(_ event: SessionEvent) -> some View {
        let expanded = exportMode || !thinkingCollapsed.contains(event.uuid)
        return VStack(alignment: .leading, spacing: 6) {
            Button {
                guard !scrollMotion.isSettling else { return }
                if thinkingCollapsed.contains(event.uuid) {
                    thinkingCollapsed.remove(event.uuid)
                } else {
                    thinkingCollapsed.insert(event.uuid)
                }
            } label: {
                HStack(spacing: 5) {
                    Image(systemName: "brain")
                        .font(.system(size: 12, weight: .regular))
                    Text("Thinking")
                        .font(.claudeCaption)
                    Spacer()
                    if !exportMode {
                        Image(systemName: expanded ? "chevron.down" : "chevron.right")
                            .font(.system(size: 11, weight: .semibold))
                    }
                }
                .foregroundStyle(Theme.textMuted)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(expanded ? "Thinking, expanded" : "Thinking, collapsed")
            if expanded {
                SelectableText(text: RichText.plain(event.thinking ?? "", font: RichText.serif(14, italic: true),
                                                    color: Theme.textMuted, lineSpacing: 3))
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.leading, 10)
                    .padding(.vertical, 2)
                    .overlay(alignment: .leading) {
                        Theme.border.frame(width: 2)
                    }
            }
        }
        .padding(.vertical, 4)
    }

    // MARK: - Work groups

    /// "5 steps · 3 commands, 2 reads · 42s", and whether any tool result failed.
    private static func workSummary(_ members: [TimelineKind]) -> (label: String, hasError: Bool) {
        var tools: [String] = []
        var hasError = false
        var stamps: [String] = []
        for m in members {
            switch m {
            case .event(let e):
                stamps.append(e.timestamp)
                if e.type == .toolUse, let name = e.toolUse?.name { tools.append(name) }
                if e.toolResults?.contains(where: \.isError) == true { hasError = true }
            case .agentGroup(let toolUse, let subs):
                stamps.append(toolUse.timestamp)
                if let last = subs.last { stamps.append(last.timestamp) }
                tools.append("Agent")
            default: break
            }
        }
        func kind(_ name: String) -> (one: String, many: String) {
            switch name {
            case "Bash", "!": return ("command", "commands")
            case "Read": return ("read", "reads")
            case "Edit", "Write", "MultiEdit", "NotebookEdit": return ("edit", "edits")
            case "Grep", "Glob": return ("search", "searches")
            case "Agent", "Task": return ("agent", "agents")
            case "WebFetch", "WebSearch": return ("web", "web")
            default: return (name, name)
            }
        }
        var counts: [String: (n: Int, one: String, many: String)] = [:]
        var order: [String] = []
        for t in tools {
            let k = kind(t)
            if counts[k.many] == nil { order.append(k.many); counts[k.many] = (0, k.one, k.many) }
            counts[k.many]!.n += 1
        }
        let kinds = order.sorted { counts[$0]!.n > counts[$1]!.n }.prefix(3).map { key -> String in
            let c = counts[key]!
            return "\(c.n) \(c.n == 1 ? c.one : c.many)"
        }
        var parts = ["\(tools.count) \(tools.count == 1 ? "step" : "steps")"]
        if !kinds.isEmpty { parts.append(kinds.joined(separator: ", ")) }
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let dates = stamps.compactMap { iso.date(from: $0) ?? ISO8601DateFormatter().date(from: $0) }
        if let first = dates.min(), let last = dates.max() {
            let secs = Int(last.timeIntervalSince(first).rounded())
            if secs >= 1 { parts.append(secs >= 60 ? "\(secs / 60)m \(secs % 60)s" : "\(secs)s") }
        }
        return (parts.joined(separator: " · "), hasError)
    }

    private func isGroupExpanded(_ id: String) -> Bool {
        if exportMode { return false }
        return groupOverride[id] ?? liveExpanded.contains(id)
    }

    /// Summary row ("▸ 5 steps · 3 commands, 2 reads · 42s"), and the members when expanded.
    private func workGroupBlock(id: String, members: [(id: String, kind: TimelineKind)], live: Bool) -> some View {
        let expanded = isGroupExpanded(id)
        let summary = Self.workSummary(members.map(\.kind))
        return VStack(alignment: .leading, spacing: 2) {
            Button {
                guard !scrollMotion.isSettling else { return }
                groupOverride[id] = !expanded
            } label: {
                HStack(spacing: 8) {
                    if live {
                        PulsingDot()
                    }
                    Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundStyle(Theme.textMuted)
                        .frame(width: 12)
                    Text(summary.label)
                        .font(.claudeCaption)
                        .foregroundStyle(Theme.textSecondary)
                        .lineLimit(1)
                    Spacer(minLength: 0)
                    if summary.hasError {
                        Image(systemName: "xmark.circle")
                            .font(.system(size: 12, weight: .regular))
                            .foregroundStyle(Theme.danger)
                    }
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 9)
                .claudeCard()
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("\(summary.label)\(summary.hasError ? ", with errors" : ""), \(expanded ? "expanded" : "collapsed")")
            if expanded {
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(members, id: \.id) { member in
                        memberRow(member)
                    }
                }
                .padding(.leading, 10)
                .overlay(alignment: .leading) {
                    Theme.border.frame(width: 1.5).padding(.vertical, 4)
                }
            }
        }
        .padding(.vertical, 3)
    }

    /// One row for a run of peer turns ("⇄ Ops · 3 messages · 2m ago" and the last message's
    /// preview); a tap shows the turns in place (docs/plan-peer-messages.md → Peer exchanges).
    private func peerExchangeBlock(id: String, members: [(id: String, kind: TimelineKind)], running: Bool,
                                   liveGroupId: String?) -> PeerExchangeRow<ForEach<[Int], Int, AnyView>> {
        let expanded = isGroupExpanded(id)
        return PeerExchangeRow(summary: PeerExchangeSummary(messages: Self.exchangeMessages(members)),
                               expanded: expanded, running: running,
                               onToggle: {
                                   guard !scrollMotion.isSettling else { return }
                                   groupOverride[id] = !expanded
                               }) {
            // AnyView per member breaks the opaque-type cycle (timelineRow renders exchanges)
            ForEach(Array(members.indices), id: \.self) { i in
                AnyView(timelineRow(members[i], liveGroupId: liveGroupId).id(members[i].id))
            }
        }
    }

    // MARK: - Tool use

    private func toolUseBlock(_ event: SessionEvent) -> some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: toolIcon(event.toolUse?.name ?? ""))
                .font(.system(size: 13, weight: .regular))
                .foregroundStyle(Theme.textSecondary)
                .frame(width: 18, height: 18)
            VStack(alignment: .leading, spacing: 2) {
                Text(event.toolUse?.name ?? "unknown")
                    .font(.claudeCaptionSemibold)
                    .foregroundStyle(Theme.textPrimary)
                if let summary = event.toolUse?.inputSummary, !summary.isEmpty {
                    Text(summary)
                        .font(.claudeMono(12))
                        .foregroundStyle(Theme.textMuted)
                        .lineLimit(2)
                }
            }
            Spacer(minLength: 0)
            Image(systemName: "chevron.right")
                .font(.system(size: 11, weight: .regular))
                .foregroundStyle(Theme.textMuted)
                .frame(height: 18)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .claudeCard()
        .contentShape(Rectangle())
        .padding(.vertical, 3)
        .onTapGesture { openDetail(event) }
    }

    // MARK: - AskUserQuestion

    func parseQuestions(from event: SessionEvent) -> [AskQuestion] {
        guard let input = event.toolUse?.fullInput,
              let data = input.data(using: .utf8),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let questions = json["questions"] as? [[String: Any]] else { return [] }

        return questions.enumerated().map { index, q in
            let question = q["question"] as? String ?? ""
            let header = q["header"] as? String ?? ""
            let opts = (q["options"] as? [[String: Any]])?.compactMap { o -> (String, String)? in
                guard let label = o["label"] as? String else { return nil }
                return (label, o["description"] as? String ?? "")
            } ?? []
            return AskQuestion(index: index, header: header, question: question, options: opts,
                               multiSelect: q["multiSelect"] as? Bool ?? false)
        }
    }

    private func askUserQuestionBlock(_ event: SessionEvent) -> some View {
        let questions = parseQuestions(from: event)
        let state: AskUserQuestionView.Phase
        if let toolId = event.toolUse?.id, let result = toolResultsById[toolId] {
            state = Self.answerPhase(from: result, questions: questions)
        } else if event.uuid == pendingQuestionEventId {
            state = .pending
        } else {
            state = .closed   // session moved on (interrupted, server restarted, ...)
        }
        let isPending = state == .pending
        return AskUserQuestionView(questions: questions, phase: state, onSendKey: onSendKey, onAnswer: onAnswer)
            .id(event.uuid + (isPending ? "-pending" : "-done"))   // reset local selection state on transition
            .onAppear { if isPending { pendingQuestionVisible = true } }
            .onDisappear { if isPending { pendingQuestionVisible = false } }
    }

    /// Parse the AskUserQuestion tool_result, e.g.
    /// `Your questions have been answered: "Which color?"="Blue", "Which size?"="Large". ...`
    /// Multi-select answers are joined with ", ".
    static func answerPhase(from result: ToolResultBlock, questions: [AskQuestion]) -> AskUserQuestionView.Phase {
        var answers: [Int: Set<String>] = [:]
        for (index, q) in questions.enumerated() {
            let marker = "\"\(q.question)\"=\""
            guard let start = result.content.range(of: marker) else { continue }
            let rest = result.content[start.upperBound...]
            let value = String(rest.prefix { $0 != "\"" })
            let labels = Set(q.options.map(\.label))
            if labels.contains(value) { answers[index] = [value]; continue }
            // Multi-select answers are labels joined with ", "; whatever isn't a label is the
            // free-text ("Other") part, kept as one value.
            let parts = value.components(separatedBy: ", ")
            var chosen = Set(parts.filter { labels.contains($0) })
            let other = parts.filter { !labels.contains($0) }.joined(separator: ", ")
            if !other.isEmpty { chosen.insert(other) }
            answers[index] = chosen
        }
        if answers.isEmpty { return result.isError ? .dismissed : .closed }
        return .answered(answers)
    }

    // MARK: - Tool result

    private func toolResultBlock(_ event: SessionEvent) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            if let results = event.toolResults {
                ForEach(results, id: \.toolUseId) { result in
                    HStack(alignment: .top, spacing: 6) {
                        Image(systemName: result.isError ? "xmark.circle" : "checkmark.circle")
                            .font(.system(size: 11, weight: .regular))
                            .foregroundStyle(result.isError ? Theme.danger : Theme.success)
                            .padding(.top, 1)
                        VStack(alignment: .leading, spacing: 6) {
                            if let images = result.images, !images.isEmpty {
                                ToolResultThumbnails(paths: images)
                            }
                            if !result.content.isEmpty || (result.images ?? []).isEmpty {
                                Text(result.content.isEmpty ? "(empty)" : String(result.content.prefix(300)))
                                    .font(.claudeMono(11))
                                    .foregroundStyle(Theme.textMuted)
                                    .lineLimit(4)
                            }
                        }
                    }
                }
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.bgSunken, in: RoundedRectangle(cornerRadius: Theme.radiusSmall, style: .continuous))
        .contentShape(Rectangle())
        .padding(.vertical, 1)
        .onTapGesture { openDetail(event) }
    }

    // MARK: - Blocked (auto mode, docs/design-claude-uplift.md → Permissions (auto mode))

    /// Blocked cards for the event's denied calls, then its ordinary results (if any) as usual.
    @ViewBuilder
    private func blockedBlocks(_ event: SessionEvent) -> some View {
        let results = event.toolResults ?? []
        let blocked = results.filter { AutoModeBlock.isBlock($0) }
        let others = results.filter { !AutoModeBlock.isBlock($0) }
        VStack(alignment: .leading, spacing: 4) {
            ForEach(blocked, id: \.toolUseId) { result in
                blockedCard(event, result: result)
            }
            if !others.isEmpty {
                toolResultBlock(SessionEvent(uuid: event.uuid, timestamp: event.timestamp, type: .toolResult,
                                             text: nil, thinking: nil, toolUse: nil,
                                             toolResults: others, model: nil, durationMs: nil))
            }
        }
    }

    private func blockedCard(_ event: SessionEvent, result: ToolResultBlock) -> some View {
        let call = events.lazy.compactMap(\.toolUse).first { $0.id == result.toolUseId }
        return VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Image(systemName: "hand.raised")
                    .font(.system(size: 13, weight: .regular))
                    .foregroundStyle(Theme.danger)
                Text("Blocked")
                    .font(.claudeCaptionSemibold)
                    .foregroundStyle(Theme.danger)
                Text(AutoModeBlock.rule(result.content) ?? "Auto mode blocked this action")
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.textMuted)
                    .lineLimit(1)
                Spacer(minLength: 0)
            }
            if let call {
                Text(call.inputSummary.isEmpty ? call.name : "\(call.name) · \(call.inputSummary)")
                    .font(.claudeMono(12))
                    .foregroundStyle(Theme.textSecondary)
                    .lineLimit(3)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .claudeCard()
        .contentShape(Rectangle())
        .padding(.vertical, 3)
        .onTapGesture { openDetail(event) }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isButton)
    }

    // MARK: - System (turn duration)

    private func systemBlock(_ event: SessionEvent) -> some View {
        Group {
            if let ms = event.durationMs {
                HStack {
                    Spacer()
                    Text("Turn: \(formattedDuration(ms))")
                        .font(.claudeFootnote)
                        .foregroundStyle(Theme.textMuted)
                    Spacer()
                }
                .padding(.vertical, 4)
            }
        }
    }

    /// "Context compacted · 23.5k → 3.5k tokens" divider; tap to read the summary.
    private func compactionBlock(_ event: SessionEvent) -> some View {
        HStack(spacing: 8) {
            Theme.border.frame(height: 0.5)
            HStack(spacing: 5) {
                Image(systemName: "arrow.triangle.2.circlepath")
                    .font(.system(size: 11, weight: .regular))
                Text(Self.compactionLabel(event))
                    .font(.claudeFootnote)
                if !exportMode {
                    Image(systemName: "chevron.right")
                        .font(.system(size: 9, weight: .semibold))
                }
            }
            .foregroundStyle(Theme.textMuted)
            .fixedSize()
            Theme.border.frame(height: 0.5)
        }
        .padding(.vertical, 8)
        .contentShape(Rectangle())
        .onTapGesture { openDetail(event) }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isButton)
        .accessibilityHint("Shows the summary Claude kept")
    }

    static func compactionLabel(_ event: SessionEvent) -> String {
        guard let pre = event.compactPreTokens, let post = event.compactPostTokens else { return "Context compacted" }
        return "Context compacted · \(tokenCount(pre)) → \(tokenCount(post)) tokens"
    }

    /// 981891 → "982k", 23511 → "23.5k", 812 → "812".
    static func tokenCount(_ n: Int) -> String {
        if n >= 100_000 { return "\(Int((Double(n) / 1000).rounded()))k" }
        if n >= 1000 {
            let k = (Double(n) / 100).rounded() / 10
            return k == k.rounded() ? "\(Int(k))k" : String(format: "%.1fk", k)
        }
        return "\(n)"
    }

    // MARK: - Agent group (subagent events nested under Agent tool_use)

    @State private var expandedAgents: Set<String> = []

    private func agentGroupBlock(toolUse: SessionEvent, subEvents: [SessionEvent]) -> some View {
        let agentId = toolUse.uuid
        let isExpanded = expandedAgents.contains(agentId)
        let agentType = subEvents.first?.subagentInfo?.agentType ?? "Agent"

        return VStack(alignment: .leading, spacing: 0) {
            // Agent header (tappable to expand/collapse)
            Button {
                withAnimation(.easeInOut(duration: 0.2)) {
                    if isExpanded { expandedAgents.remove(agentId) }
                    else { expandedAgents.insert(agentId) }
                }
            } label: {
                HStack(alignment: .top, spacing: 8) {
                    Image(systemName: "person.2")
                        .font(.system(size: 13, weight: .regular))
                        .foregroundStyle(Theme.textSecondary)
                        .frame(width: 18, height: 18)
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 6) {
                            Text("Agent")
                                .font(.claudeCaptionSemibold)
                                .foregroundStyle(Theme.textPrimary)
                            Text(agentType)
                                .font(.claudeFootnote)
                                .foregroundStyle(Theme.textSecondary)
                                .padding(.horizontal, 7)
                                .padding(.vertical, 1)
                                .background(Theme.bgSunken, in: Capsule())
                        }
                        if let summary = toolUse.toolUse?.inputSummary, !summary.isEmpty {
                            Text(summary)
                                .font(.claudeMono(12))
                                .foregroundStyle(Theme.textMuted)
                                .lineLimit(2)
                                .multilineTextAlignment(.leading)
                        }
                    }
                    Spacer(minLength: 0)
                    HStack(spacing: 4) {
                        Text("\(subEvents.count)")
                            .font(.claudeFootnote)
                        Image(systemName: isExpanded ? "chevron.up" : "chevron.down")
                            .font(.system(size: 11, weight: .regular))
                    }
                    .foregroundStyle(Theme.textMuted)
                    .frame(height: 18)
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 10)
                .claudeCard()
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            // Nested subagent events
            if isExpanded {
                VStack(alignment: .leading, spacing: 1) {
                    ForEach(subEvents, id: \.uuid) { sub in
                        eventRow(sub)
                            .padding(.leading, 8)
                    }
                }
                .padding(.leading, 4)
                .padding(.top, 4)
                .overlay(
                    Rectangle()
                        .fill(Theme.border)
                        .frame(width: 2),
                    alignment: .leading
                )
            }
        }
        .padding(.vertical, 2)
        .onTapGesture { openDetail(toolUse) }
    }

    // MARK: - Helpers

    private func toolIcon(_ name: String) -> String {
        switch name {
        case "Read": return "doc.text"
        case "Write": return "square.and.pencil"
        case "Edit": return "pencil.line"
        case "Bash": return "terminal"
        case "Glob": return "magnifyingglass"
        case "Grep": return "text.magnifyingglass"
        case "Agent": return "person.2"
        case "Skill": return "star"
        case "!": return "apple.terminal"
        default: return "wrench"
        }
    }

    private func formattedDuration(_ ms: Int) -> String {
        if ms < 1000 { return "\(ms)ms" }
        let seconds = Double(ms) / 1000.0
        if seconds < 60 { return String(format: "%.1fs", seconds) }
        let minutes = Int(seconds) / 60
        let secs = Int(seconds) % 60
        return "\(minutes)m \(secs)s"
    }
}

// MARK: - Event Detail Sheet

struct EventDetailSheet: View {
    let event: SessionEvent
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    // Header
                    HStack {
                        Label {
                            Text(headerTitle)
                                .font(.claudeUISemibold)
                                .foregroundStyle(Theme.textPrimary)
                        } icon: {
                            Image(systemName: headerIcon)
                                .font(.system(size: 15, weight: .regular))
                                .foregroundStyle(headerColor)
                        }
                        Spacer()
                        Text(event.timestamp)
                            .font(.claudeFootnote)
                            .foregroundStyle(Theme.textMuted)
                    }

                    if let model = event.model {
                        Text(model)
                            .font(.claudeCaption)
                            .foregroundStyle(Theme.textSecondary)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 3)
                            .background(Theme.bgSunken, in: Capsule())
                    }

                    Theme.border.frame(height: 0.5)

                    // Content
                    switch event.type {
                    case .user:
                        contentText(event.text ?? "")
                    case .text:
                        markdownContent(event.text ?? "")
                    case .thinking:
                        contentText(event.thinking ?? "")
                    case .toolUse:
                        toolUseDetail
                    case .toolResult:
                        toolResultDetail
                    case .system:
                        if let ms = event.durationMs {
                            Label("Duration: \(ms)ms", systemImage: "clock")
                                .font(.claudeUI)
                                .foregroundStyle(Theme.textSecondary)
                        }
                    case .compaction:
                        VStack(alignment: .leading, spacing: 10) {
                            if event.compactPreTokens != nil {
                                Label(ConversationView.compactionLabel(event)
                                        .replacingOccurrences(of: "Context compacted · ", with: "Context: "),
                                      systemImage: "gauge.with.dots.needle.33percent")
                                    .font(.claudeUI)
                                    .foregroundStyle(Theme.textSecondary)
                            }
                            if let summary = event.text, !summary.isEmpty {
                                Text("Summary Claude kept")
                                    .font(.claudeCaptionSemibold)
                                    .foregroundStyle(Theme.textSecondary)
                                // Hide CLI scaffolding (<system-reminder> blocks) from the summary
                                markdownContent(summary.replacingOccurrences(
                                    of: #"<system-reminder>[\s\S]*?</system-reminder>\s*"#, with: "",
                                    options: .regularExpression))
                            } else {
                                Text("No summary was saved for this compaction.")
                                    .font(.claudeUI)
                                    .foregroundStyle(Theme.textMuted)
                            }
                        }
                    }
                }
                .padding()
            }
            .background(Theme.bgPage)
            .tint(Theme.link)
            .navigationTitle("Event Detail")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        Button("Copy as Markdown") {
                            UIPasteboard.general.string = TranscriptExport.rawText(of: event)
                        }
                        Button("Copy as plain text") {
                            UIPasteboard.general.string = TranscriptExport.plain(TranscriptExport.rawText(of: event))
                        }
                    } label: {
                        Image(systemName: "doc.on.doc")
                            .font(.system(size: 15, weight: .regular))
                            .foregroundStyle(Theme.textPrimary)
                    }
                    .accessibilityLabel("Copy")
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { dismiss() }
                        .font(.claudeUISemibold)
                        .foregroundStyle(Theme.textPrimary)
                }
            }
            .toolbarBackground(Theme.bgPage, for: .navigationBar)
        }
        .presentationBackground(Theme.bgPage)
        .presentationDetents([.medium, .large])
        .presentationDragIndicator(.visible)
    }

    private var headerTitle: String {
        switch event.type {
        case .user: "User Message"
        case .text: "Assistant"
        case .thinking: "Thinking"
        case .toolUse: event.toolUse?.name ?? "Tool Use"
        case .toolResult: "Tool Result"
        case .system: "System"
        case .compaction: "Context Compacted"
        }
    }

    private var headerIcon: String {
        switch event.type {
        case .user: "person.fill"
        case .text: "bubble.left.fill"
        case .thinking: "brain"
        case .toolUse: "wrench"
        case .toolResult: "checkmark.circle"
        case .system: "gearshape"
        case .compaction: "arrow.triangle.2.circlepath"
        }
    }

    private var headerColor: Color {
        switch event.type {
        case .user, .text, .toolUse: Theme.textSecondary
        case .thinking, .system, .compaction: Theme.textMuted
        case .toolResult:
            (event.toolResults?.contains { $0.isError } ?? false) ? Theme.danger : Theme.success
        }
    }

    private func contentText(_ text: String) -> some View {
        SelectableText(text: RichText.plain(text, font: RichText.mono(14), color: Theme.textPrimary))
            .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func markdownContent(_ text: String) -> some View {
        SelectableText(text: RichText.prose(text))
            .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var toolUseDetail: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let tool = event.toolUse {
                Label {
                    Text(tool.name)
                        .font(.claudeUISemibold)
                        .foregroundStyle(Theme.textPrimary)
                } icon: {
                    Image(systemName: "wrench")
                        .font(.system(size: 14, weight: .regular))
                        .foregroundStyle(Theme.textSecondary)
                }

                if !tool.id.isEmpty {
                    HStack {
                        Text("ID:")
                            .font(.claudeCaption)
                            .foregroundStyle(Theme.textMuted)
                        Text(tool.id)
                            .font(.claudeMono(12))
                            .foregroundStyle(Theme.textSecondary)
                            .textSelection(.enabled)
                    }
                }

                if let fullInput = tool.fullInput, let parsed = parseToolInput(fullInput) {
                    formattedToolInput(name: tool.name, input: parsed)
                } else if !tool.inputSummary.isEmpty {
                    Text("Input")
                        .font(.claudeCaptionSemibold)
                        .foregroundStyle(Theme.textSecondary)
                    SelectableText(text: RichText.plain(tool.fullInput ?? tool.inputSummary, font: RichText.mono(12),
                                                        color: Theme.textPrimary))
                        .padding(10)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Theme.bgSunken, in: RoundedRectangle(cornerRadius: Theme.radiusSmall, style: .continuous))
                }
            }
        }
    }

    private func parseToolInput(_ json: String) -> [String: String]? {
        guard let data = json.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return nil }
        var result: [String: String] = [:]
        for (key, value) in obj {
            if let str = value as? String { result[key] = str }
            else if let bool = value as? Bool { result[key] = bool ? "true" : "false" }
            else { result[key] = "\(value)" }
        }
        return result
    }

    @ViewBuilder
    private func formattedToolInput(name: String, input: [String: String]) -> some View {
        switch name {
        case "Edit":
            if let filePath = input["file_path"] {
                Label(filePath, systemImage: "doc")
                    .font(.claudeMono(12))
                    .foregroundStyle(Theme.textMuted)
            }
            if let replaceAll = input["replace_all"], replaceAll == "true" {
                Text("Replace all occurrences")
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.textMuted)
            }
            unifiedDiffView(old: input["old_string"] ?? "", new: input["new_string"] ?? "")

        case "Write":
            if let filePath = input["file_path"] {
                Label(filePath, systemImage: "doc.badge.plus")
                    .font(.claudeMono(12))
                    .foregroundStyle(Theme.textMuted)
            }
            if let content = input["content"] {
                codeBlock(text: content)
            }

        case "Read":
            if let filePath = input["file_path"] {
                Label(filePath, systemImage: "doc.text")
                    .font(.claudeMono(12))
                    .foregroundStyle(Theme.textMuted)
            }
            if let offset = input["offset"] { paramRow("Offset", offset) }
            if let limit = input["limit"] { paramRow("Limit", limit) }

        case "Bash":
            if let command = input["command"] {
                codeBlock(text: command)
            }

        case "Grep":
            if let pattern = input["pattern"] { paramRow("Pattern", pattern) }
            if let path = input["path"] { paramRow("Path", path) }
            if let glob = input["glob"] { paramRow("Glob", glob) }

        case "Glob":
            if let pattern = input["pattern"] { paramRow("Pattern", pattern) }
            if let path = input["path"] { paramRow("Path", path) }

        default:
            // Generic: show all fields
            ForEach(input.keys.sorted(), id: \.self) { key in
                paramRow(key, input[key] ?? "")
            }
        }
    }

    // MARK: - Unified Diff View with character-level highlighting

    private enum DiffLineType { case context, removed, added }

    private struct DiffEntry {
        let type: DiffLineType
        let text: String
        // Character-level: ranges that are specifically changed (highlighted stronger)
        let highlightRanges: [Range<String.Index>]

        init(_ type: DiffLineType, _ text: String, highlights: [Range<String.Index>] = []) {
            self.type = type
            self.text = text
            self.highlightRanges = highlights
        }
    }

    private func unifiedDiffView(old: String, new: String) -> some View {
        let entries = computeDiff(old: old, new: new)
        return VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(entries.enumerated()), id: \.offset) { _, entry in
                diffEntryView(entry)
            }
        }
        .background(Theme.bgSunken)
        .clipShape(RoundedRectangle(cornerRadius: Theme.radiusSmall, style: .continuous))
        .claudeHairline(radius: Theme.radiusSmall)
    }

    private func diffEntryView(_ entry: DiffEntry) -> some View {
        let prefix: String
        let lineColor: Color
        let bgColor: Color
        let strongBg: Color

        switch entry.type {
        case .context:
            prefix = " "; lineColor = Theme.textMuted; bgColor = .clear; strongBg = .clear
        case .removed:
            prefix = "−"; lineColor = Theme.diffDelText; bgColor = Theme.diffDelBg; strongBg = Theme.diffDelText.opacity(0.18)
        case .added:
            prefix = "+"; lineColor = Theme.diffAddText; bgColor = Theme.diffAddBg; strongBg = Theme.diffAddText.opacity(0.18)
        }

        return HStack(spacing: 0) {
            // Gutter
            Text(prefix)
                .font(.claudeMono(12))
                .foregroundStyle(lineColor)
                .frame(width: 18, alignment: .center)
                .padding(.vertical, 3)
                .background(entry.type == .context ? Color.clear : bgColor)

            // Line content with inline highlights
            buildHighlightedText(entry.text, highlights: entry.highlightRanges, baseColor: entry.type == .context ? Theme.textSecondary : lineColor, strongBg: strongBg)
                .font(.claudeMono(12))
                .padding(.horizontal, 6)
                .padding(.vertical, 3)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(bgColor)
        }
        .textSelection(.enabled)
    }

    private func buildHighlightedText(_ text: String, highlights: [Range<String.Index>], baseColor: Color, strongBg: Color) -> Text {
        guard !highlights.isEmpty, !text.isEmpty else {
            return Text(text.isEmpty ? " " : text).foregroundColor(baseColor)
        }

        var attr = AttributedString(text)
        attr.foregroundColor = baseColor

        for range in highlights {
            guard let attrLower = AttributedString.Index(range.lowerBound, within: attr),
                  let attrUpper = AttributedString.Index(range.upperBound, within: attr) else { continue }
            attr[attrLower..<attrUpper].backgroundColor = strongBg
        }

        return Text(attr)
    }

    /// LCS-based line diff, then character-level diff for paired changed lines
    private func computeDiff(old: String, new: String) -> [DiffEntry] {
        let oldLines = old.components(separatedBy: "\n")
        let newLines = new.components(separatedBy: "\n")

        // Build LCS table
        let m = oldLines.count, n = newLines.count
        var dp = Array(repeating: Array(repeating: 0, count: n + 1), count: m + 1)
        for i in 1...max(m, 1) where i <= m {
            for j in 1...max(n, 1) where j <= n {
                if oldLines[i - 1] == newLines[j - 1] {
                    dp[i][j] = dp[i - 1][j - 1] + 1
                } else {
                    dp[i][j] = max(dp[i - 1][j], dp[i][j - 1])
                }
            }
        }

        // Backtrack to produce raw diff
        enum RawDiff { case ctx(String), rem(String), add(String) }
        var raw: [RawDiff] = []
        var i = m, j = n
        while i > 0 || j > 0 {
            if i > 0 && j > 0 && oldLines[i - 1] == newLines[j - 1] {
                raw.append(.ctx(oldLines[i - 1])); i -= 1; j -= 1
            } else if j > 0 && (i == 0 || dp[i][j - 1] >= dp[i - 1][j]) {
                raw.append(.add(newLines[j - 1])); j -= 1
            } else {
                raw.append(.rem(oldLines[i - 1])); i -= 1
            }
        }
        raw.reverse()

        // Post-process: pair up adjacent removed/added runs for character-level diff
        var result: [DiffEntry] = []
        var idx = 0
        while idx < raw.count {
            switch raw[idx] {
            case .ctx(let s):
                result.append(DiffEntry(.context, s))
                idx += 1
            case .rem:
                // Collect consecutive removed lines
                var removed: [String] = []
                while idx < raw.count, case .rem(let s) = raw[idx] {
                    removed.append(s); idx += 1
                }
                // Collect consecutive added lines that follow
                var added: [String] = []
                while idx < raw.count, case .add(let s) = raw[idx] {
                    added.append(s); idx += 1
                }
                // Pair them up for character-level diff
                let pairs = max(removed.count, added.count)
                for p in 0..<pairs {
                    if p < removed.count && p < added.count {
                        let (remHL, addHL) = charDiff(old: removed[p], new: added[p])
                        result.append(DiffEntry(.removed, removed[p], highlights: remHL))
                        result.append(DiffEntry(.added, added[p], highlights: addHL))
                    } else if p < removed.count {
                        result.append(DiffEntry(.removed, removed[p]))
                    } else {
                        result.append(DiffEntry(.added, added[p]))
                    }
                }
            case .add(let s):
                result.append(DiffEntry(.added, s))
                idx += 1
            }
        }
        return result
    }

    /// Character-level LCS diff between two strings. Returns highlight ranges for each.
    private func charDiff(old: String, new: String) -> ([Range<String.Index>], [Range<String.Index>]) {
        let oldChars = Array(old)
        let newChars = Array(new)
        let m = oldChars.count, n = newChars.count

        // Skip if too large (O(m*n) space)
        guard m * n < 500_000 else { return ([], []) }

        // LCS
        var dp = Array(repeating: Array(repeating: 0, count: n + 1), count: m + 1)
        for i in 1...max(m, 1) where i <= m {
            for j in 1...max(n, 1) where j <= n {
                if oldChars[i - 1] == newChars[j - 1] {
                    dp[i][j] = dp[i - 1][j - 1] + 1
                } else {
                    dp[i][j] = max(dp[i - 1][j], dp[i][j - 1])
                }
            }
        }

        // Backtrack to find which chars are NOT in LCS (those are the changes)
        var oldChanged = Array(repeating: true, count: m)
        var newChanged = Array(repeating: true, count: n)
        var ci = m, cj = n
        while ci > 0 && cj > 0 {
            if oldChars[ci - 1] == newChars[cj - 1] {
                oldChanged[ci - 1] = false
                newChanged[cj - 1] = false
                ci -= 1; cj -= 1
            } else if dp[ci - 1][cj] >= dp[ci][cj - 1] {
                ci -= 1
            } else {
                cj -= 1
            }
        }

        // Convert bool arrays to string ranges
        func toRanges(_ changed: [Bool], _ str: String) -> [Range<String.Index>] {
            var ranges: [Range<String.Index>] = []
            var idx = str.startIndex
            var runStart: String.Index?
            for (i, ch) in changed.enumerated() {
                if ch {
                    if runStart == nil { runStart = idx }
                } else {
                    if let start = runStart {
                        ranges.append(start..<idx)
                        runStart = nil
                    }
                }
                if i < changed.count - 1 {
                    idx = str.index(after: idx)
                } else if ch, let start = runStart {
                    ranges.append(start..<str.index(after: idx))
                }
            }
            return ranges
        }

        return (toRanges(oldChanged, old), toRanges(newChanged, new))
    }

    private func codeBlock(text: String) -> some View {
        SelectableText(text: RichText.convert(highlightBash(text), font: RichText.mono(12), codeFont: RichText.mono(12),
                                              color: Theme.textPrimary))
            .padding(10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Theme.bgSunken, in: RoundedRectangle(cornerRadius: Theme.radiusSmall, style: .continuous))
            .claudeHairline(radius: Theme.radiusSmall)
    }

    /// Simple bash syntax highlighting using AttributedString
    private func highlightBash(_ command: String) -> AttributedString {
        var result = AttributedString(command)
        result.foregroundColor = Theme.textPrimary

        // Warm syntax palette (adaptive light/dark), shared with the ANSI remap's hues.
        let opColor = Theme.clayPressed
        let keywordColor = Color(light: 0x94487E, dark: 0xCB8DBD)   // plum
        let flagColor = Color(light: 0x2A7F78, dark: 0x72BFB4)      // teal
        let stringColor = Theme.diffAddText
        let variableColor = Theme.diffDelText
        let numberColor = Theme.warning
        let commentColor = Theme.textMuted

        // Operators: &&, ||, |, ;, >, >>, <, 2>&1
        let operators = [#"&&"#, #"\|\|"#, #"\|"#, #";"#, #">>"#, #"2>&1"#, #">"#, #"<"#].map { ($0, opColor) }

        // Keywords
        let keywords = [
            (#"\b(if|then|else|fi|for|do|done|while|case|esac|in)\b"#, keywordColor),
            (#"\b(cd|echo|export|source|return|exit)\b"#, keywordColor),
        ]

        // Flags: -something or --something
        let flags = [(#"\s(--?[\w][\w-]*)"#, flagColor)]

        // Strings (double and single quoted)
        let strings = [
            (#"\"[^\"]*\""#, stringColor),
            (#"'[^']*'"#, stringColor),
        ]

        // Variables: $VAR, ${VAR}, $(...)
        let variables = [
            (#"\$\{[^}]+\}"#, variableColor),
            (#"\$\([^)]+\)"#, variableColor),
            (#"\$[A-Za-z_]\w*"#, variableColor),
        ]

        // Numbers
        let numbers = [(#"\b\d+\b"#, numberColor)]

        // Comments: # to end of line (but not inside strings or ${ })
        let comments = [(#"(?:^|(?<=\s))#[^\n]*"#, commentColor)]

        // Apply all patterns (order matters — later patterns override)
        // Comments last so they override everything within the comment
        let allPatterns: [(String, Color)] = operators + keywords + numbers + flags + strings + variables + comments

        for (pattern, color) in allPatterns {
            guard let regex = try? NSRegularExpression(pattern: pattern) else { continue }
            let nsString = command as NSString
            let matches = regex.matches(in: command, range: NSRange(location: 0, length: nsString.length))
            for match in matches {
                // Use capture group 1 if it exists (for flags pattern), else group 0
                let rangeIdx = match.numberOfRanges > 1 && match.range(at: 1).location != NSNotFound ? 1 : 0
                let nsRange = match.range(at: rangeIdx)
                guard let swiftRange = Range(nsRange, in: command),
                      let attrLower = AttributedString.Index(swiftRange.lowerBound, within: result),
                      let attrUpper = AttributedString.Index(swiftRange.upperBound, within: result) else { continue }
                result[attrLower..<attrUpper].foregroundColor = color
            }
        }

        return result
    }

    private func paramRow(_ label: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(label)
                .font(.claudeFootnote)
                .foregroundStyle(Theme.textMuted)
            SelectableText(text: RichText.plain(value, font: RichText.mono(12), color: Theme.textPrimary))
        }
    }

    private var toolResultDetail: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let results = event.toolResults {
                ForEach(results, id: \.toolUseId) { result in
                    VStack(alignment: .leading, spacing: 6) {
                        HStack {
                            Image(systemName: result.isError ? "xmark.circle" : "checkmark.circle")
                                .font(.system(size: 14, weight: .regular))
                                .foregroundStyle(result.isError ? Theme.danger : Theme.success)
                            Text(result.isError ? "Error" : "Success")
                                .font(.claudeUISemibold)
                                .foregroundStyle(Theme.textPrimary)
                            Spacer()
                            Text(result.toolUseId)
                                .font(.claudeMono(11))
                                .foregroundStyle(Theme.textMuted)
                        }

                        if let images = result.images, !images.isEmpty {
                            ToolResultThumbnails(paths: images, size: 120)
                        }

                        if !(result.content.isEmpty && !(result.images ?? []).isEmpty) {
                            SelectableText(text: RichText.markdown(result.content.isEmpty ? "(empty)" : result.content,
                                                                   font: RichText.mono(12), codeFont: RichText.mono(12),
                                                                   color: Theme.textSecondary))
                                .padding(10)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .background(Theme.bgSunken, in: RoundedRectangle(cornerRadius: Theme.radiusSmall, style: .continuous))
                        }
                    }
                }
            }
        }
    }
}

// MARK: - "Question waiting" pill

private struct QuestionWaitingPill: View {
    let questionId: String?
    @Binding var isNearBottom: Bool
    @Binding var questionVisible: Bool
    let jump: (String) -> Void

    var body: some View {
        if let questionId, !questionVisible, !isNearBottom {
            Button {
                jump(questionId)
            } label: {
                Label("Question waiting", systemImage: "arrow.down")
                    .font(.claudeCaptionSemibold)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 8)
                    .foregroundStyle(Color.white)
                    .background(Theme.clay, in: Capsule())
            }
            .buttonStyle(.plain)
            .padding(.bottom, 8)
        }
    }
}

// MARK: - AskUserQuestion (stateful selection)

struct AskUserQuestionView: View {
    enum Phase: Equatable {
        case pending                       // waiting on the user: selectable, Submit / Cancel
        case answered([Int: Set<String>])  // question index -> chosen labels (from tool_result)
        case dismissed                     // user cancelled
        case closed                        // never answered; session moved on
    }

    let questions: [AskQuestion]
    var phase: Phase = .pending
    var onSendKey: ((String) -> Void)?
    /// Sends question text -> answer; returns false if it didn't go through.
    var onAnswer: (([String: String]) async -> Bool)?

    // Keyed by question id
    @State private var picked: [String: Set<Int>] = [:]   // ticked option indexes
    @State private var otherOn: Set<String> = []          // "Other" selected
    @State private var otherText: [String: String] = [:]  // kept while Other is toggled off
    @State private var submitted = false
    @FocusState private var focusedOther: String?

    private var isInteractive: Bool { phase == .pending && !submitted }

    /// A question with no options is answered by text alone.
    private func otherActive(_ q: AskQuestion) -> Bool { q.options.isEmpty || otherOn.contains(q.id) }

    private func trimmedOther(_ q: AskQuestion) -> String {
        (otherText[q.id] ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// The answer to send: option labels in option order, then the Other text, joined with ", ".
    /// nil while unanswered, or while Other is selected with blank text.
    private func answer(for q: AskQuestion) -> String? {
        if otherActive(q) && trimmedOther(q).isEmpty { return nil }
        var parts = (picked[q.id] ?? []).sorted().map { q.options[$0].label }
        if otherActive(q) { parts.append(trimmedOther(q)) }
        return parts.isEmpty ? nil : parts.joined(separator: ", ")
    }

    private var allAnswered: Bool { questions.allSatisfy { answer(for: $0) != nil } }

    private func isChosen(questionIndex: Int, optionIndex: Int) -> Bool {
        if case .answered(let answers) = phase {
            return answers[questionIndex]?.contains(questions[questionIndex].options[optionIndex].label) ?? false
        }
        return picked[questions[questionIndex].id]?.contains(optionIndex) ?? false
    }

    private func toggle(_ q: AskQuestion, option optIdx: Int) {
        var set = picked[q.id] ?? []
        if set.contains(optIdx) {
            set.remove(optIdx)
        } else {
            if !q.multiSelect {                     // single-select: options and Other are exclusive
                set = []
                otherOn.remove(q.id)
                if focusedOther == q.id { focusedOther = nil }
            }
            set.insert(optIdx)
        }
        picked[q.id] = set
    }

    private func toggleOther(_ q: AskQuestion) {
        if otherOn.contains(q.id) {
            otherOn.remove(q.id)
            if focusedOther == q.id { focusedOther = nil }
        } else {
            if !q.multiSelect { picked[q.id] = [] }
            otherOn.insert(q.id)
            focusedOther = q.id
        }
    }

    private func submit() {
        guard allAnswered, let onAnswer else { return }
        var answers: [String: String] = [:]
        for q in questions { answers[q.question] = answer(for: q) }
        submitted = true
        focusedOther = nil
        Task {
            if !(await onAnswer(answers)) { submitted = false }   // keep selections; let them retry
        }
    }

    /// Free-text answer that matched no option (e.g. "Other").
    private func freeTextAnswer(questionIndex: Int) -> String? {
        guard case .answered(let answers) = phase, let chosen = answers[questionIndex] else { return nil }
        let labels = Set(questions[questionIndex].options.map(\.label))
        return chosen.first { !labels.contains($0) && !$0.isEmpty }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 6) {
                Image(systemName: "questionmark.circle")
                    .font(.system(size: 13, weight: .regular))
                    .foregroundStyle(isInteractive ? Theme.clay : Theme.textMuted)
                Text("Question")
                    .font(.claudeCaptionSemibold)
                    .foregroundStyle(isInteractive ? Theme.textSecondary : Theme.textMuted)
            }

            ForEach(Array(questions.enumerated()), id: \.element.id) { qIdx, q in
                VStack(alignment: .leading, spacing: 6) {
                    if !q.header.isEmpty {
                        Text(q.header)
                            .font(.claudeCaptionSemibold)
                            .foregroundStyle(Theme.textMuted)
                    }
                    Text(q.question)
                        .font(.claudeProse)
                        .claudeProseSpacing()
                        .foregroundStyle(isInteractive ? Theme.textPrimary : Theme.textSecondary)
                        .padding(.bottom, 2)

                    VStack(spacing: 6) {
                        ForEach(Array(q.options.enumerated()), id: \.element.label) { optIdx, opt in
                            let chosen = isChosen(questionIndex: qIdx, optionIndex: optIdx)
                            if isInteractive {
                                Button {
                                    toggle(q, option: optIdx)
                                } label: {
                                    optionRow(opt, chosen: chosen, interactive: true, multi: q.multiSelect)
                                }
                                .buttonStyle(.plain)
                            } else {
                                optionRow(opt, chosen: chosen, interactive: false, multi: q.multiSelect)
                            }
                        }
                        if isInteractive {
                            if !q.options.isEmpty {
                                Button {
                                    toggleOther(q)
                                } label: {
                                    optionRow((label: "Other", description: "Type your own answer"),
                                              chosen: otherOn.contains(q.id), interactive: true, multi: q.multiSelect)
                                }
                                .buttonStyle(.plain)
                            }
                            if otherActive(q) {
                                otherField(q)
                            }
                        }
                    }
                    if let other = freeTextAnswer(questionIndex: qIdx) {
                        Text("Answer: \(other)")
                            .font(.claudeCaption)
                            .foregroundStyle(Theme.textSecondary)
                    }
                }

                if q.id != questions.last?.id {
                    Theme.border.frame(height: 0.5)
                }
            }

            if isInteractive {
                Button {
                    submit()
                } label: {
                    Label(
                        allAnswered ? "Submit answers" : "Answer every question",
                        systemImage: "arrow.up"
                    )
                    .frame(maxWidth: .infinity)
                }
                .buttonStyle(ClayCapsuleButtonStyle(enabled: allAnswered))
                .disabled(!allAnswered)
                .padding(.top, 2)

                Button {
                    submitted = true
                    focusedOther = nil
                    onSendKey?("Escape")
                } label: {
                    Text("Cancel")
                        .font(.claudeUI)
                        .foregroundStyle(Theme.textSecondary)
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 6)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            } else {
                statusFooter
            }
        }
        .padding(14)
        .claudeCard()
        .padding(.vertical, 6)
    }

    private func otherField(_ q: AskQuestion) -> some View {
        let shape = RoundedRectangle(cornerRadius: Theme.radiusCard - 2, style: .continuous)
        return TextField("Type your answer", text: Binding(
            get: { otherText[q.id] ?? "" },
            set: { otherText[q.id] = $0 }
        ), axis: .vertical)
            .lineLimit(1...5)
            .font(.claudeUI)
            .foregroundStyle(Theme.textPrimary)
            .focused($focusedOther, equals: q.id)
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .background(Theme.bgSunken, in: shape)
            .overlay(shape.strokeBorder(focusedOther == q.id ? Theme.clay : Theme.border,
                                        lineWidth: focusedOther == q.id ? 1 : 0.5))
            .accessibilityLabel("Other answer for \(q.header.isEmpty ? q.question : q.header)")
    }

    private func optionRow(_ opt: (label: String, description: String), chosen: Bool, interactive: Bool,
                           multi: Bool = false) -> some View {
        let shape = RoundedRectangle(cornerRadius: Theme.radiusCard - 2, style: .continuous)
        let icon = multi ? (chosen ? "checkmark.square.fill" : "square")
                         : (chosen ? "checkmark.circle.fill" : "circle")
        return HStack(alignment: .top, spacing: 10) {
            Image(systemName: icon)
                .font(.system(size: 17, weight: .regular))
                .foregroundStyle(chosen ? Theme.clay : Theme.textMuted.opacity(interactive ? 1 : 0.5))
            VStack(alignment: .leading, spacing: 2) {
                Text(opt.label)
                    .font(chosen ? .claudeUISemibold : .claudeUI)
                    .foregroundStyle(interactive || chosen ? Theme.textPrimary : Theme.textMuted)
                if !opt.description.isEmpty {
                    Text(opt.description)
                        .font(.claudeFootnote)
                        .foregroundStyle(Theme.textMuted)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .background(chosen ? Theme.clay.opacity(0.10) : (interactive ? Theme.bgSunken : Color.clear), in: shape)
        .overlay(shape.strokeBorder(chosen ? Theme.clay : Theme.border, lineWidth: chosen ? 1 : 0.5))
        .contentShape(shape)
        .accessibilityAddTraits(chosen ? .isSelected : [])
    }

    @ViewBuilder
    private var statusFooter: some View {
        let (icon, color, text): (String, Color, String) = switch phase {
        case .answered: ("checkmark.circle", Theme.success, "Answered")
        case .dismissed: ("xmark.circle", Theme.danger, "Dismissed")
        case .pending: ("clock", Theme.textMuted, "Submitted")          // submitted locally, result not in yet
        case .closed: ("minus.circle", Theme.textMuted, "Not answered")
        }
        HStack(spacing: 5) {
            Image(systemName: icon)
                .font(.system(size: 12, weight: .regular))
                .foregroundStyle(color)
            Text(text)
                .font(.claudeFootnote)
                .foregroundStyle(Theme.textMuted)
        }
    }
}

// MARK: - Progressive reveal (docs/design-claude-uplift.md → History)

extension ConversationView {
    /// Rows rendered on open and added per reveal: about two screenfuls.
    static let revealChunk = 40

    /// Index of the first rendered row. Before the window is set (or if its events were
    /// replaced), the newest chunk.
    private func revealStart(_ rows: [(id: String, kind: TimelineKind)]) -> Int {
        guard let i = revealAnchorIndex(rows) else { return max(0, rows.count - Self.revealChunk) }
        return max(0, i - revealAbove)
    }

    /// Index of the row that holds the window's first event (searched from the end: it's usually
    /// near it).
    private func revealAnchorIndex(_ rows: [(id: String, kind: TimelineKind)]) -> Int? {
        guard let from = revealFrom else { return nil }
        return rows.lastIndex { Self.row($0.kind, holds: from) }
    }

    /// Whether a row holds the event: itself, or as a member of a group.
    private static func row(_ kind: TimelineKind, holds uuid: String) -> Bool {
        switch kind {
        case .event(let e): e.uuid == uuid
        case .agentGroup(let toolUse, _): toolUse.uuid == uuid
        case .workGroup(let members), .peerExchange(let members, _): members.contains { row($0.kind, holds: uuid) }
        case .pending, .working, .queue: false
        }
    }

    /// The oldest event in a row.
    private static func firstEventId(_ kind: TimelineKind) -> String? {
        switch kind {
        case .event(let e): e.uuid
        case .agentGroup(let toolUse, _): toolUse.uuid
        case .workGroup(let members), .peerExchange(let members, _): members.first.flatMap { firstEventId($0.kind) }
        case .pending, .working, .queue: nil
        }
    }

    /// The rows as of the latest render. Reveals run in tasks that outlive the view value they
    /// were started from, so they read the rows from here, not from that value's `events`.
    private var latestRows: [(id: String, kind: TimelineKind)] {
        reveal.latestRows as? [(id: String, kind: TimelineKind)] ?? timelineItems
    }

    /// Events changed. Set the window when rows first arrive, or when the events it started at are
    /// gone (a replaced saved copy, a cleared conversation): the newest chunk of rows. Then keep
    /// memory topped up if the reader is already scrolling back.
    private func eventsChanged() {
        settleRevealWindow()
        prefetchIfLow()
    }

    private func settleRevealWindow() {
        let rows = latestRows
        if revealAnchorIndex(rows) != nil { return }
        let start = max(0, rows.count - Self.revealChunk)
        guard let id = rows[start...].lazy.compactMap({ Self.firstEventId($0.kind) }).first else { return }
        revealFrom = id
        revealAbove = 0
        guard !reveal.opened else { return }
        reveal.opened = true
        logRevealRecord(["source": "open", "rows_shown": rows.count - start, "rows_hidden": start,
                         "events": events.count])
    }

    /// Reveal the next chunk of older rows. Concurrent callers (the scroll position, the top row)
    /// share one reveal. Returns whether rows were added.
    @discardableResult
    private func revealOlder(source: String) async -> Bool {
        if let running = reveal.inFlight { return await running.value }
        let task = Task { await revealStep(source: source) }
        reveal.inFlight = task
        let added = await task.value
        reveal.inFlight = nil
        return added
    }

    /// From memory when rows are hidden; otherwise wait for the page being prefetched, or fetch the
    /// next older page and reveal its newest chunk.
    private func revealStep(source: String) async -> Bool {
        let started = Date()
        if revealFrom == nil { settleRevealWindow() }
        let rows = latestRows
        let start = revealStart(rows)
        let before = reveal.geometry
        let from: String
        reveal.keptSinceReveal = 0
        if start > 0 {
            from = "memory"
            reveal.keepPlaceUntil = Date().addingTimeInterval(0.4)
            revealAbove += min(Self.revealChunk, start)
        } else {
            let added: Bool
            if let pending = reveal.prefetch {
                from = "prefetch"
                added = await pending.value
            } else if !reveal.historyComplete, let onLoadOlder {
                from = "fetch"
                added = await onLoadOlder()
            } else {
                return false
            }
            guard added else { return false }
            reveal.keepPlaceUntil = Date().addingTimeInterval(0.4)
            // The page landed above every row shown before: reveal up to a chunk of it
            revealAbove = (revealAnchorIndex(rows) ?? 0) + Self.revealChunk
        }
        reveal.revealed = true
        var log: [String: Any] = ["source": source, "from": from, "rows_before": rows.count - start,
                                  "ms": Int(Date().timeIntervalSince(started) * 1000)]
        if let before {
            log["offset_before"] = Double(before.offsetY)
            log["content_before"] = Double(before.contentHeight)
        }
        reveal.pendingLog = log
        revealTick += 1
        return true
    }

    /// Once the reader has revealed older rows: when fewer than two chunks stay hidden in memory
    /// and history remains, fetch the next older page ahead (again each time a page lands, until
    /// memory is ahead of the reader).
    private func prefetchIfLow() {
        guard reveal.revealed, reveal.prefetch == nil, !reveal.historyComplete, let onLoadOlder,
              revealStart(latestRows) < Self.revealChunk * 2 else { return }
        let state = reveal
        state.prefetch = Task {
            let added = await onLoadOlder()
            state.prefetch = nil
            return added
        }
    }

    /// The reader came within two screens of the top, or moved away (iOS 18+).
    private func nearTopChanged(_ near: Bool) {
        reveal.nearTop = near
        if near, historyGate.userHasScrolled { Task { await revealOlder(source: "scroll") } }
    }

    /// After a reveal rendered: log it, fetch ahead if memory is low, and if the reader is still
    /// near the top (a short chunk, a fast fling), reveal the next chunk.
    private func afterReveal() async {
        guard revealTick > 0 else { return }
        prefetchIfLow()
        try? await Task.sleep(for: .milliseconds(150))
        logReveal()
        guard !Task.isCancelled, reveal.nearTop, historyGate.userHasScrolled else { return }
        await revealOlder(source: "scroll")
    }

    /// The last reveal's record, with the row counts it rendered and how the scroll position
    /// moved. With the place kept, the offset moves by as much as the content grew (`drift` ≈ 0,
    /// unless the reader was scrolling meanwhile).
    private func logReveal() {
        guard var attrs = reveal.pendingLog else { return }
        reveal.pendingLog = nil
        let rows = latestRows
        let start = revealStart(rows)
        attrs["rows_shown"] = rows.count - start
        attrs["rows_hidden"] = start
        attrs["events"] = events.count
        if let after = reveal.geometry, let offset = attrs["offset_before"] as? Double,
           let content = attrs["content_before"] as? Double {
            attrs["offset_after"] = Double(after.offsetY)
            attrs["content_after"] = Double(after.contentHeight)
            attrs["drift"] = (Double(after.offsetY) - offset) - (Double(after.contentHeight) - content)
            attrs["user_scrolling"] = userScrolling
            attrs["kept"] = Double(reveal.keptSinceReveal)
        }
        logRevealRecord(attrs)
    }

    private func logRevealRecord(_ attrs: [String: Any]) {
        TranscriptProbe.shared.note(.loads, attrs.merging(["event": "reveal", "session_id": sessionId]) { a, _ in a })   // telemetry
        Telemetry.shared.log("mclaude.transcript.reveal", attributes: attrs.merging(["mclaude.session_id": sessionId]) { a, _ in a })
    }
}

// MARK: - Auto-follow support

extension ConversationView {
    /// iOS 18+ reports scroll phases, so we can tell user scrolls from content growth.
    static var tracksScrollPhase: Bool {
        if #available(iOS 18.0, *) { return true }
        return false
    }
}

/// Is the transcript moving? Written from scroll-phase callbacks, read by tap handlers. A plain
/// class (not observed), so updating it never re-renders anything.
final class ScrollMotion {
    var moving = false
    var stoppedAt = Date.distantPast
    /// Moving, or stopped within the last 0.35s: the tap that stops a fling lands in this window.
    var isSettling: Bool { moving || Date().timeIntervalSince(stoppedAt) < 0.35 }
}

private struct UserScrollPhaseTracking: ViewModifier {
    @Binding var userScrolling: Bool
    var scrollMotion: ScrollMotion? = nil
    var onUserScrollStarted: () -> Void = {}
    let onUserScrollEnded: () -> Void

    func body(content: Content) -> some View {
        if #available(iOS 18.0, *) {
            content.onScrollPhaseChange { old, new in
                let wasUser = old == .interacting || old == .decelerating
                let isUser = new == .interacting || new == .decelerating
                let moving = new == .interacting || new == .decelerating || new == .animating
                if let m = scrollMotion {
                    if m.moving && !moving { m.stoppedAt = Date() }
                    m.moving = moving
                }
                userScrolling = isUser
                if new == .interacting && old != .interacting { onUserScrollStarted() }
                if wasUser && !isUser { onUserScrollEnded() }
            }
        } else {
            content
        }
    }
}

/// iOS 18+: if the scroll offset ends up more than a little past the end of the content while
/// the user isn't touching it (row heights settling in the LazyVStack, a streaming row
/// shrinking), call `recover` to snap back to the bottom. iOS 17: no-op.
///
/// Only a state that lasts: the LazyVStack reports one-frame content sizes while it swaps
/// estimated row heights for real ones (and content shorter than the screen at first load).
/// Scrolling to the bottom on one of those left the scroll view un-anchored, so the keyboard
/// opening no longer kept the bottom in view, and the list could then flip between two content
/// sizes every frame (the main thread pinned at 100% once the composer was used).
private struct OverscrollRecovery: ViewModifier {
    let isUserScrolling: () -> Bool
    let recover: () -> Void

    @State private var pastEnd = false

    func body(content: Content) -> some View {
        if #available(iOS 18.0, *) {
            content.onScrollGeometryChange(for: Bool.self) { g in
                g.contentOffset.y > 0
                    && g.contentOffset.y + g.containerSize.height > g.contentSize.height + g.contentInsets.bottom + 48
            } action: { _, isPastEnd in
                pastEnd = isPastEnd
                // telemetry (Ops F-14: past the end on resume, and no recovery ran)
                TranscriptProbe.shared.note(.scrolls, ["source": "past_end", "on": isPastEnd,
                                                       "app_active": UIApplication.shared.applicationState == .active])
            }
            .task(id: pastEnd) {
                guard pastEnd else { return }
                try? await Task.sleep(for: .milliseconds(250))
                guard !Task.isCancelled, pastEnd, !isUserScrolling() else {
                    TranscriptProbe.shared.note(.scrolls, ["source": "overscroll_skipped", "cancelled": Task.isCancelled,
                                                           "past_end": pastEnd, "user_scrolling": isUserScrolling()])   // telemetry
                    return
                }
                recover()
            }
        } else {
            content
        }
    }
}

/// Local thumbnails of photos still uploading / queued (before Claude echoes the message).
private struct PendingPhotoStrip: View {
    let images: [UIImage]

    private var side: CGFloat {
        switch images.count {
        case 1: 160
        case 2: 120
        case 3: 96
        default: 64
        }
    }

    var body: some View {
        HStack(spacing: 6) {
            ForEach(images.indices, id: \.self) { i in      // fixed list for this pending message
                Image(uiImage: images[i])
                    .resizable()
                    .scaledToFill()
                    .frame(width: side, height: side)
                    .clipShape(RoundedRectangle(cornerRadius: Theme.radiusCard, style: .continuous))
                    .claudeHairline(radius: Theme.radiusCard)
                    .opacity(0.85)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(images.count == 1 ? "1 photo, sending" : "\(images.count) photos, sending")
    }
}

/// "Claude is working" row at the end of the conversation while a turn runs.
private struct WorkingIndicatorRow: View {
    var compacting = false

    var body: some View {
        HStack(spacing: 8) {
            PulsingDot(size: 8)
            Text(compacting ? "Compacting conversation…" : "Working…")
                .font(.claudeFootnote)
                .foregroundStyle(Theme.textMuted)
            Spacer()
        }
        .padding(.vertical, 8)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(compacting ? "Claude is compacting the conversation" : "Claude is working")
    }
}

// MARK: - Server message queue

/// The server's queue for this session, at the very bottom of the timeline. Owns its own
/// expand/collapse and edit-sheet state so toggling it never re-evaluates ConversationView.body.
private struct QueueGroupView: View {
    let items: [QueuedMessage]
    let onEdit: (QueuedMessage, String) -> Void
    let onDelete: (QueuedMessage) -> Void
    let onFlush: () -> Void

    @State private var expanded = true
    @State private var editing: QueuedMessage?

    var body: some View {
        VStack(alignment: .trailing, spacing: 8) {
            header
            if expanded {
                ForEach(items) { item in          // stable: server queue ids
                    QueuedItemBubble(item: item,
                                     onEdit: { editing = item },
                                     onDelete: { onDelete(item) })
                }
            }
            footer
        }
        .frame(maxWidth: .infinity, alignment: .trailing)
        .padding(.top, 12)
        .padding(.bottom, 6)
        .sheet(item: $editing) { item in
            QueueEditSheet(item: item) { newText in onEdit(item, newText) }
        }
    }

    private var header: some View {
        Button {
            withAnimation(.easeInOut(duration: 0.2)) { expanded.toggle() }
        } label: {
            HStack(spacing: 5) {
                Spacer(minLength: 0)
                Image(systemName: "clock")
                    .font(.system(size: 11, weight: .regular))
                Text("Queued · \(items.count)")
                    .font(.claudeCaptionSemibold)
                Image(systemName: expanded ? "chevron.up" : "chevron.down")
                    .font(.system(size: 11, weight: .regular))
            }
            .foregroundStyle(Theme.textMuted)
            .padding(.vertical, 2)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(items.count == 1 ? "1 queued message" : "\(items.count) queued messages")
        .accessibilityHint(expanded ? "Collapses the queue" : "Expands the queue")
    }

    private var footer: some View {
        HStack(spacing: 8) {
            Spacer(minLength: 0)
            Text("Claude reads these at its next step")
                .font(.claudeFootnote)
                .foregroundStyle(Theme.textMuted)
                .lineLimit(1)
                .minimumScaleFactor(0.8)
            Button(action: onFlush) {
                Text("Send now")
                    .font(.claudeCaptionSemibold)
                    .foregroundStyle(Theme.clay)
            }
            .buttonStyle(.plain)
            .accessibilityHint("Interrupts Claude's current step so it reads the queued messages now")
        }
    }
}

/// One queued message: right-aligned like a pending bubble, dashed outline, photos as thumbnails.
private struct QueuedItemBubble: View {
    let item: QueuedMessage
    let onEdit: () -> Void
    let onDelete: () -> Void

    var body: some View {
        let parsed = ConversationView.parseUserContent(FindingParser.compactText(item.text))
        HStack {
            Spacer(minLength: 40)
            VStack(alignment: .trailing, spacing: 6) {
                VStack(alignment: .trailing, spacing: 6) {
                    if !parsed.photoPaths.isEmpty {
                        QueuedPhotoStrip(paths: parsed.photoPaths)
                    }
                    if !parsed.message.isEmpty {
                        Text(parsed.message)
                            .font(.claudeBody)
                            .foregroundStyle(Theme.textPrimary)
                            .padding(.horizontal, 14)
                            .padding(.vertical, 10)
                            .background(Theme.bgSunken,
                                        in: RoundedRectangle(cornerRadius: Theme.radiusBubble, style: .continuous))
                            .overlay(
                                // Dashed outline = not yet delivered to Claude
                                RoundedRectangle(cornerRadius: Theme.radiusBubble, style: .continuous)
                                    .strokeBorder(Theme.textMuted.opacity(0.45), style: StrokeStyle(lineWidth: 1, dash: [4, 3]))
                            )
                    }
                }
                .contentShape(Rectangle())
                .contextMenu {
                    Button(action: onEdit) { Label("Edit", systemImage: "pencil") }
                    Button(role: .destructive, action: onDelete) { Label("Delete", systemImage: "trash") }
                }
                HStack(spacing: 14) {
                    Button(action: onEdit) { Text("Edit") }
                    Button(action: onDelete) { Text("Delete") }
                }
                .buttonStyle(.plain)
                .font(.claudeFootnote)
                .foregroundStyle(Theme.textMuted)
            }
        }
    }
}

/// Server thumbnails for a queued photo message (fixed list per item).
private struct QueuedPhotoStrip: View {
    let paths: [String]

    private var side: CGFloat {
        switch paths.count {
        case 1: 120
        case 2: 96
        default: 64
        }
    }

    var body: some View {
        HStack(spacing: 6) {
            ForEach(paths.indices, id: \.self) { i in
                let item = UserAttachment(fragment: paths[i])
                RemoteImageView(path: item.thumbnailPath, contentMode: .fill)
                    .frame(width: side, height: side)
                    .videoTile(item.isVideo, duration: item.duration, compact: side < 100)
                    .clipShape(RoundedRectangle(cornerRadius: Theme.radiusCard, style: .continuous))
                    .claudeHairline(radius: Theme.radiusCard)
                    .opacity(0.85)
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(UserAttachment.countLabel(paths.map(UserAttachment.init(fragment:))) + ", queued")
    }
}

/// Edit a queued message's caption; attached photos are kept (paths re-prefixed on save).
private struct QueueEditSheet: View {
    let item: QueuedMessage
    let onSave: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var caption: String
    private let photoPaths: [String]

    init(item: QueuedMessage, onSave: @escaping (String) -> Void) {
        self.item = item
        self.onSave = onSave
        let parsed = ConversationView.parseUserContent(item.text)
        self.photoPaths = parsed.photoPaths
        _caption = State(initialValue: parsed.message)
    }

    private var trimmedCaption: String { caption.trimmingCharacters(in: .whitespacesAndNewlines) }
    private var canSave: Bool { !trimmedCaption.isEmpty || !photoPaths.isEmpty }

    var body: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: 10) {
                if !photoPaths.isEmpty {
                    Text(UserAttachment.countLabel(photoPaths.map(UserAttachment.init(fragment:)))
                         + (photoPaths.count == 1 ? " stays attached" : " stay attached"))
                        .font(.claudeFootnote)
                        .foregroundStyle(Theme.textMuted)
                }
                TextEditor(text: $caption)
                    .font(.claudeBody)
                    .foregroundStyle(Theme.textPrimary)
                    .scrollContentBackground(.hidden)
                    .padding(8)
                    .background(Theme.bgRaised, in: RoundedRectangle(cornerRadius: Theme.radiusCard, style: .continuous))
                    .claudeHairline(radius: Theme.radiusCard)
            }
            .padding()
            .background(Theme.bgPage)
            .tint(Theme.clay)
            .navigationTitle("Edit queued message")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                        .font(.claudeUI)
                        .foregroundStyle(Theme.textSecondary)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") {
                        onSave((photoPaths + (trimmedCaption.isEmpty ? [] : [trimmedCaption])).joined(separator: " "))
                        dismiss()
                    }
                    .font(.claudeUISemibold)
                    .foregroundStyle(canSave ? Theme.clay : Theme.textMuted)
                    .disabled(!canSave)
                }
            }
            .toolbarBackground(Theme.bgPage, for: .navigationBar)
        }
        .presentationBackground(Theme.bgPage)
        .presentationDetents([.medium, .large])
        .presentationDragIndicator(.visible)
    }
}

