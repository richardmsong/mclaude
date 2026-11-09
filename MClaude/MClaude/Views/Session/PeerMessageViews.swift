import SwiftUI
import UIKit

// Cross-session peer messages (docs/plan-peer-messages.md → iOS app): the From/To card in the
// transcript, the read-only thread, and the Info screen's Messages card. The server does all the
// parsing; these views only read `SessionEvent.peerMessages` / `peerDelivery` and the two endpoints.

// MARK: - Peer card (transcript)

/// One-line From/To card for one `PeerMessage` (docs/plan-peer-messages.md → Peer card). Resolved
/// peers open the thread on tap; unresolved ones expand the full message inline when the line cut
/// something off. Long-press copies the full body.
struct PeerCard: View {
    let message: PeerMessage
    /// Sent cards only: from the paired tool_result (`sending` until it arrives).
    var status: PeerStatus? = nil
    var error: String? = nil
    /// Opens the thread sheet at this message (resolved peers only).
    var onOpenThread: ((PeerMessage) -> Void)? = nil
    /// False while the transcript is scrolling (a tap that stops a fling does nothing else).
    var canTap: () -> Bool = { true }

    @State private var expanded = false
    @State private var lineHeight: CGFloat = 0
    @State private var fullHeight: CGFloat = 0

    private var sent: Bool { message.isSent }
    private var peer: PeerRef { message.peer ?? PeerRef() }
    private var resolved: Bool { peer.isResolved && onOpenThread != nil }
    private var bodyText: String { message.body.trimmingCharacters(in: .whitespacesAndNewlines) }
    private var preview: String { PeerPreview.text(message) }
    /// The line doesn't show the whole message: a summary hides the body, the body has more
    /// lines, or the preview didn't fit.
    private var cut: Bool {
        if message.summary != nil, !bodyText.isEmpty { return true }
        if preview != bodyText { return true }
        return fullHeight > lineHeight + 1
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            line
            if status == .failed, let error {
                Text(error)
                    .font(.claudeFootnote)
                    .foregroundStyle(Theme.danger)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if expanded { fullMessage }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .frame(maxWidth: .infinity, alignment: .leading)
        .claudeCard()
        .contentShape(Rectangle())
        .onTapGesture { tap() }
        .contextMenu {
            Button {
                UIPasteboard.general.string = message.body
            } label: {
                Label("Copy message", systemImage: "doc.on.doc")
            }
        }
        .padding(.vertical, 3)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityText)
        .accessibilityAddTraits(resolved || cut ? .isButton : [])
        .accessibilityHint(resolved ? "Opens the thread" : cut ? (expanded ? "Shows less" : "Shows the full message") : "")
        .accessibilityAction { tap() }
        .accessibilityAction(named: "Copy message") { UIPasteboard.general.string = message.body }
    }

    private var line: some View {
        HStack(spacing: 6) {
            Image(systemName: sent ? "arrow.up.right" : "arrow.down.left")
                .font(.system(size: 12, weight: .regular))
                .foregroundStyle(Theme.textSecondary)
            Text(peer.name)
                .font(.claudeCaptionSemibold)
                .foregroundStyle(Theme.textPrimary)
                .lineLimit(1)
                .layoutPriority(1)
            if !preview.isEmpty {
                Text(preview)
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.textSecondary)
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .background(alignment: .topLeading) {
                        // The same text, wrapped, to tell whether the one line cut it off
                        Text(preview)
                            .font(.claudeCaption)
                            .fixedSize(horizontal: false, vertical: true)
                            .hidden()
                            .readHeight($fullHeight)
                    }
                    .readHeight($lineHeight)
            }
            Spacer(minLength: 4)
            if sent { statusView }
            if resolved {
                Image(systemName: "chevron.right")
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(Theme.textMuted)
            }
        }
    }

    @ViewBuilder
    private var statusView: some View {
        switch status ?? .sending {
        case .delivered:
            Image(systemName: "checkmark")
                .font(.system(size: 12, weight: .regular))
                .foregroundStyle(Theme.textMuted)
        case .failed:
            Text("Not delivered")
                .font(.claudeFootnote)
                .foregroundStyle(Theme.danger)
                .lineLimit(1)
                .fixedSize()
        case .sending, .received:
            ProgressView()
                .controlSize(.mini)
                .tint(Theme.textMuted)
        }
    }

    /// Unresolved cards, expanded: the whole summary and text.
    private var fullMessage: some View {
        VStack(alignment: .leading, spacing: 4) {
            if let summary = message.summary {
                Text(summary)
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(Theme.textPrimary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if !bodyText.isEmpty {
                Text(bodyText)
                    .font(.system(size: 14))
                    .foregroundStyle(Theme.textSecondary)
                    .multilineTextAlignment(.leading)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func tap() {
        guard canTap() else { return }
        if resolved {
            onOpenThread?(message)
        } else if cut || expanded {
            withAnimation(.snappy(duration: 0.2)) { expanded.toggle() }
        }
    }

    private var accessibilityText: String {
        var parts = ["\(sent ? "Message to" : "Message from") \(peer.name)"]
        if sent {
            switch status ?? .sending {
            case .delivered: parts.append("Delivered")
            case .failed: parts.append("Not delivered" + (error.map { ": \($0)" } ?? ""))
            default: parts.append("Sending")
            }
        }
        if let summary = message.summary { parts.append(summary) }
        if !bodyText.isEmpty { parts.append(bodyText) }
        return parts.joined(separator: ". ")
    }
}

/// The one-line preview of a peer message: its summary, else the first non-empty line of its text.
enum PeerPreview {
    static func text(_ message: PeerMessage) -> String {
        if let summary = message.summary?.trimmingCharacters(in: .whitespacesAndNewlines), !summary.isEmpty {
            return summary
        }
        let body = message.body.trimmingCharacters(in: .whitespacesAndNewlines)
        return body.split(whereSeparator: \.isNewline)
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .first { !$0.isEmpty } ?? ""
    }
}

/// What an exchange row says (docs/plan-peer-messages.md → Peer exchanges).
struct PeerExchangeSummary {
    let messages: [PeerMessage]

    /// Distinct peer names in order of first appearance, two at most, then "+N".
    var peers: String {
        var names: [String] = []
        for m in messages {
            let name = (m.peer ?? PeerRef()).name
            if !names.contains(name) { names.append(name) }
        }
        if names.isEmpty { return "Another session" }
        let shown = names.prefix(2).joined(separator: ", ")
        return names.count > 2 ? "\(shown) +\(names.count - 2)" : shown
    }

    var countText: String { messages.count == 1 ? "1 message" : "\(messages.count) messages" }

    /// " · 3 messages · 2m ago" (no age when the last message has no time).
    func detail(now: Date) -> String {
        var parts = [countText]
        if let date = messages.last?.date { parts.append(relativeTimeLabel(date, now: now)) }
        return " · " + parts.joined(separator: " · ")
    }

    /// The last message's preview: "You: …" when it was sent, else "<peer>: …".
    var preview: String? {
        guard let last = messages.last else { return nil }
        let text = PeerPreview.text(last)
        guard !text.isEmpty else { return nil }
        return "\(last.isSent ? "You" : (last.peer ?? PeerRef()).name): \(text)"
    }

    func accessibilityLabel(now: Date, expanded: Bool) -> String {
        var parts = ["Exchange with \(peers)", countText]
        if let date = messages.last?.date { parts.append(relativeTimeLabel(date, now: now)) }
        if let preview { parts.append(preview) }
        parts.append(expanded ? "expanded" : "collapsed")
        return parts.joined(separator: ", ")
    }
}

/// The exchange row and, when expanded, its turns (docs/plan-peer-messages.md → Peer exchanges).
/// A plain struct (not AnyView) so expanding keeps the row's identity in the transcript's LazyVStack.
struct PeerExchangeRow<Content: View>: View {
    let summary: PeerExchangeSummary
    let expanded: Bool
    let running: Bool
    let onToggle: () -> Void
    @ViewBuilder let content: () -> Content

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Button(action: onToggle) {
                HStack(alignment: .center, spacing: 8) {
                    if running { PulsingDot() }
                    Image(systemName: "arrow.left.arrow.right")
                        .font(.system(size: 12, weight: .regular))
                        .foregroundStyle(Theme.textSecondary)
                    VStack(alignment: .leading, spacing: 2) {
                        TimelineView(.periodic(from: .now, by: 30)) { context in
                            (Text(summary.peers).font(.claudeCaptionSemibold).foregroundStyle(Theme.textPrimary)
                             + Text(summary.detail(now: context.date)).font(.claudeCaption).foregroundStyle(Theme.textSecondary))
                                .lineLimit(1)
                        }
                        if let preview = summary.preview {
                            Text(preview)
                                .font(.claudeCaption)
                                .foregroundStyle(Theme.textMuted)
                                .lineLimit(1)
                        }
                    }
                    Spacer(minLength: 0)
                    Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundStyle(Theme.textMuted)
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 9)
                .claudeCard()
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(summary.accessibilityLabel(now: Date(), expanded: expanded))
            .accessibilityAddTraits(.isButton)
            if expanded {
                VStack(alignment: .leading, spacing: 2) {
                    content()
                }
                .padding(.leading, 10)
                .overlay(alignment: .leading) {
                    Theme.border.frame(width: 1.5).padding(.vertical, 4)
                }
            }
        }
        .padding(.vertical, 3)
    }
}

private extension View {
    /// Report this view's height into `height` (iOS 17 has no onGeometryChange).
    func readHeight(_ height: Binding<CGFloat>, enabled: Bool = true) -> some View {
        background {
            if enabled {
                GeometryReader { geo in
                    Color.clear
                        .onAppear { height.wrappedValue = geo.size.height }
                        .onChange(of: geo.size.height) { _, h in height.wrappedValue = h }
                }
            }
        }
    }
}

// MARK: - Live refresh

enum PeerLive {
    /// Changes whenever a peer event for `peerUuid` arrives on the session's stream (a received
    /// message, a send, or a send's delivery result). The open thread reloads on a change.
    static func token(events: [SessionEvent], peerUuid: String) -> String {
        var count = 0
        var last = ""
        var sendIds: Set<String> = []
        for event in events {
            guard let messages = event.peerMessages, messages.contains(where: { $0.peer?.claudeSessionId == peerUuid })
            else {
                if let tid = event.peerDelivery?.toolUseId, sendIds.contains(tid) {
                    count += 1
                    last = event.uuid
                }
                continue
            }
            if event.type == .toolUse, let tid = event.toolUse?.id { sendIds.insert(tid) }
            count += 1
            last = event.uuid
        }
        return "\(count)|\(last)"
    }
}

// MARK: - Thread

/// What to open: the peer (Claude session UUID) and, from a card, the message to scroll to.
struct PeerThreadTarget: Identifiable, Hashable {
    let peerUuid: String
    var focusMessageId: String? = nil
    /// Shown in the header until the thread loads.
    var peerHint: PeerRef? = nil
    var id: String { "\(peerUuid)|\(focusMessageId ?? "")" }
}

/// The thread as its own sheet (opened from a transcript card): "Messages", Done, large detent.
struct PeerThreadSheet: View {
    let sessionId: String
    let target: PeerThreadTarget
    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            PeerThreadView(sessionId: sessionId, target: target) { id in
                // Same path as a push-notification tap; switch once the sheet is gone
                dismiss()
                Task { @MainActor in
                    try? await Task.sleep(for: .milliseconds(350))
                    appState.openSessionRequest = id
                }
            }
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Done") { dismiss() }
                        .fontWeight(.semibold)
                        .foregroundStyle(Theme.clay)
                }
            }
        }
        .tint(Theme.clay)
        .presentationDetents([.large])
        .presentationBackground(Theme.bgPage)
    }
}

/// Read-only thread between this session and one peer, oldest first (no composer). Pushed inside
/// the Info sheet's navigation, or wrapped in `PeerThreadSheet`.
struct PeerThreadView: View {
    let sessionId: String
    let target: PeerThreadTarget
    /// "Open <peer>": dismiss whatever is presented and switch to that MClaude session.
    let onOpenSession: (String) -> Void

    @Environment(AppState.self) private var appState

    private enum LoadState: Equatable { case loading, loaded, failed(String) }

    @State private var thread: PeerThread?
    @State private var loadState: LoadState = .loading
    @State private var loads = 0
    @State private var didScroll = false
    @State private var highlightId: String?
    @State private var highlightOpacity: Double = 0

    private var messages: [PeerMessage] { thread?.messages ?? [] }

    var body: some View {
        GeometryReader { geo in
            ScrollViewReader { proxy in
                ScrollView {
                    VStack(alignment: .leading, spacing: 14) {
                        header
                        content(maxWidth: geo.size.width * 0.85)
                        Color.clear.frame(height: 1).id("thread-bottom")
                    }
                    .padding(16)
                }
                .refreshable { await load() }
                .onChange(of: loads) { _, _ in scroll(proxy) }
            }
        }
        .background(Theme.bgPage)
        .navigationTitle("Messages")
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
        // A new peer event for this peer on the session's live stream
        .onChange(of: PeerLive.token(events: appState.eventsCache[sessionId] ?? [], peerUuid: target.peerUuid)) {
            Task { await load() }
        }
    }

    // MARK: Header

    private var peer: PeerRef { thread?.peer ?? target.peerHint ?? PeerRef() }

    private var thisName: String {
        thread?.this.displayName
            ?? appState.sessions.first { $0.id == sessionId }?.displayName
            ?? "This session"
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("\(thisName)  ⇄  \(peer.name)")
                .font(.claudeUISemibold)
                .foregroundStyle(Theme.textPrimary)
                .fixedSize(horizontal: false, vertical: true)
            if thread != nil || target.peerHint != nil {
                if let id = peer.openSessionId {
                    HStack {
                        Spacer()
                        Button("Open \(peer.name)") { onOpenSession(id) }
                            .buttonStyle(SunkenCapsuleButtonStyle())
                            .lineLimit(1)
                            .accessibilityHint("Switches to that session")
                    }
                } else {
                    Text(["Not open in MClaude", peer.ccName].compactMap { $0 }.joined(separator: " · "))
                        .font(.claudeCaption)
                        .foregroundStyle(Theme.textMuted)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    // MARK: Content

    @ViewBuilder
    private func content(maxWidth: CGFloat) -> some View {
        switch loadState {
        case .loading where thread == nil:
            ProgressView()
                .tint(Theme.textMuted)
                .frame(maxWidth: .infinity)
                .padding(.top, 40)
        case .failed(let message) where thread == nil:
            VStack(spacing: 10) {
                Text("Couldn't load messages")
                    .font(.system(size: 20, design: .serif))
                    .foregroundStyle(Theme.textPrimary)
                Text(message)
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.textMuted)
                    .multilineTextAlignment(.center)
                Button("Retry") { Task { await load() } }
                    .font(.claudeUISemibold)
                    .foregroundStyle(Theme.clay)
            }
            .frame(maxWidth: .infinity)
            .padding(.top, 40)
        default:
            if messages.isEmpty {
                Text("No messages yet")
                    .font(.system(size: 20, design: .serif))
                    .foregroundStyle(Theme.textPrimary)
                    .frame(maxWidth: .infinity)
                    .padding(.top, 40)
            } else {
                ForEach(messages) { message in
                    messageRow(message, maxWidth: maxWidth)
                        .id(message.id)
                }
            }
        }
    }

    private func messageRow(_ m: PeerMessage, maxWidth: CGFloat) -> some View {
        let sent = m.isSent
        let failed = m.peerStatus == .failed
        let radius = sent ? Theme.radiusBubble : Theme.radiusCard
        return VStack(alignment: sent ? .trailing : .leading, spacing: 4) {
            bubble(m, sent: sent, radius: radius)
                .overlay(
                    RoundedRectangle(cornerRadius: radius, style: .continuous)
                        .strokeBorder(Theme.clay.opacity(highlightId == m.id ? highlightOpacity : 0), lineWidth: 2)
                )
                .opacity(failed ? 0.6 : 1)
                .frame(maxWidth: maxWidth, alignment: sent ? .trailing : .leading)
            caption(m, sent: sent, failed: failed)
        }
        .frame(maxWidth: .infinity, alignment: sent ? .trailing : .leading)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityText(m, sent: sent, failed: failed))
    }

    private func accessibilityText(_ m: PeerMessage, sent: Bool, failed: Bool) -> String {
        var parts = [sent ? "You" : peer.name]
        if let summary = m.summary { parts.append(summary) }
        parts.append(m.body)
        if let date = m.date { parts.append(PeerTime.label(date)) }
        if failed { parts.append("Not delivered" + (m.error.map { ": \($0)" } ?? "")) }
        else if sent, m.peerStatus == .delivered { parts.append("Delivered") }
        return parts.joined(separator: ". ")
    }

    @ViewBuilder
    private func bubble(_ m: PeerMessage, sent: Bool, radius: CGFloat) -> some View {
        let inner = VStack(alignment: .leading, spacing: 4) {
            if let summary = m.summary {
                Text(summary)
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(Theme.textPrimary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            SelectableText(text: RichText.linked(m.body, font: .systemFont(ofSize: 15), color: Theme.textPrimary),
                           hugsContent: true)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        if sent {
            inner.background(Theme.bgSunken, in: RoundedRectangle(cornerRadius: radius, style: .continuous))
        } else {
            inner.claudeCard(radius: radius)
        }
    }

    private func caption(_ m: PeerMessage, sent: Bool, failed: Bool) -> some View {
        HStack(spacing: 4) {
            if let date = m.date {
                Text(PeerTime.label(date))
                    .foregroundStyle(Theme.textMuted)
            }
            if sent, m.peerStatus == .delivered {
                Image(systemName: "checkmark")
                    .foregroundStyle(Theme.textMuted)
                    .accessibilityLabel("Delivered")
            }
            if failed {
                Text("Not delivered" + (m.error.map { ": \($0)" } ?? ""))
                    .foregroundStyle(Theme.danger)
                    .multilineTextAlignment(sent ? .trailing : .leading)
            }
        }
        .font(.system(size: 11))
        .padding(.horizontal, 4)
    }

    // MARK: Loading and scrolling

    private func load() async {
        if thread == nil { loadState = .loading }
        do {
            thread = try await appState.client.fetchPeerThread(sessionId: sessionId, peerUuid: target.peerUuid)
            loadState = .loaded
        } catch {
            if case APIError.http(404) = error {
                // No records between the two sessions (or an older server)
                loadState = .loaded
            } else if thread == nil {
                loadState = .failed(error.localizedDescription)
            }
        }
        loads += 1
    }

    /// First load: to the tapped message (briefly ringed in clay), else the bottom. Later loads
    /// (pull to refresh, live updates) leave the reader where they are.
    private func scroll(_ proxy: ScrollViewProxy) {
        guard !didScroll, !messages.isEmpty else { return }
        didScroll = true
        let focus = target.focusMessageId.flatMap { id in messages.contains { $0.id == id } ? id : nil }
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(60))   // after the rows are laid out
            if let focus {
                proxy.scrollTo(focus, anchor: .center)
                highlightId = focus
                highlightOpacity = 1
                withAnimation(.easeOut(duration: 1).delay(0.2)) { highlightOpacity = 0 }
            } else {
                proxy.scrollTo("thread-bottom", anchor: .bottomLeading)
            }
        }
    }
}

enum PeerTime {
    /// "Today 7:18 PM", "Yesterday 9:02 AM", else "Nov 3, 9:02 AM".
    static func label(_ date: Date, calendar: Calendar = .current) -> String {
        let time = DateFormatter()
        time.timeStyle = .short
        time.dateStyle = .none
        if calendar.isDateInToday(date) { return "Today \(time.string(from: date))" }
        if calendar.isDateInYesterday(date) { return "Yesterday \(time.string(from: date))" }
        let full = DateFormatter()
        full.setLocalizedDateFormatFromTemplate("MMMd jmm")
        return full.string(from: date)
    }
}

extension RichText {
    /// Plain text (no markdown) with tappable links.
    static func linked(_ text: String, font: UIFont, color: Color) -> NSAttributedString {
        let out = NSMutableAttributedString(attributedString: plain(text, font: font, color: color))
        if let detector = try? NSDataDetector(types: NSTextCheckingResult.CheckingType.link.rawValue) {
            let range = NSRange(location: 0, length: (text as NSString).length)
            for match in detector.matches(in: text, range: range) {
                if let url = match.url { out.addAttribute(.link, value: url, range: match.range) }
            }
        }
        return out
    }
}

// MARK: - Info screen: Messages card

/// The sessions this one has exchanged messages with (`GET /sessions/:id/peers`). Rows push the
/// thread inside the Info sheet's navigation (`PeerThreadTarget` destination).
struct PeerListCard: View {
    let peers: [PeerSummary]

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Messages")
                .font(.claudeCaptionSemibold)
                .foregroundStyle(Theme.textMuted)
                .padding(.bottom, 6)
            ForEach(Array(peers.enumerated()), id: \.element.id) { index, item in
                if index > 0 { Theme.border.frame(height: 0.5) }
                if let uuid = item.peer.claudeSessionId {
                    NavigationLink(value: PeerThreadTarget(peerUuid: uuid, peerHint: item.peer)) {
                        row(item)
                    }
                    .buttonStyle(.plain)
                }
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .claudeCard()
    }

    private func row(_ item: PeerSummary) -> some View {
        HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                Text(item.peer.name)
                    .font(.claudeUI)
                    .foregroundStyle(Theme.textPrimary)
                    .lineLimit(1)
                Text(meta(item))
                    .font(.claudeFootnote)
                    .foregroundStyle(Theme.textMuted)
                    .lineLimit(1)
                if let preview = item.lastPreview, !preview.isEmpty {
                    Text((item.lastWasSent ? "You: " : "") + preview)
                        .font(.claudeCaption)
                        .foregroundStyle(Theme.textMuted)
                        .lineLimit(1)
                }
            }
            Spacer(minLength: 0)
            Image(systemName: "chevron.right")
                .font(.system(size: 12, weight: .regular))
                .foregroundStyle(Theme.textMuted)
        }
        .padding(.vertical, 8)
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
        .accessibilityHint("Opens the thread")
    }

    /// "6 messages · 5m ago"
    private func meta(_ item: PeerSummary) -> String {
        var parts: [String] = []
        if let n = item.messageCount { parts.append(n == 1 ? "1 message" : "\(n) messages") }
        if let date = item.lastDate { parts.append(relativeTimeLabel(date, now: Date())) }
        return parts.joined(separator: " · ")
    }
}
