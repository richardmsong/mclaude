import SwiftUI
import UIKit

/// Transcript alignment probe (docs/plan-observability.md → Transcript alignment probe).
///
/// Measures the transcript column against the scroll view's visible frame whenever either changes
/// horizontally. Aligned means the column's left edge is the viewport's left edge, the column is
/// exactly as wide as the viewport, and the scroll view isn't scrolled sideways. Short ring
/// buffers of recent context stay in memory and are exported only with a rule break. It only
/// observes: it never changes the layout.
@MainActor
final class TranscriptProbe {
    static let shared = TranscriptProbe()

    enum Ring: String, CaseIterable {
        case wideRows = "wide_rows", drawer, keyboard, scrolls, scrollX = "scroll_x", viewport, loads, connection
    }

    private var rings: [Ring: [[String: Any]]] = [:]
    private let ringCap = 30

    private var sessionId = ""
    private var eventCount = 0
    private var viewport: (minX: CGFloat, width: CGFloat)?
    private var column: (minX: CGFloat, width: CGFloat)?
    private var scroll: ScrollSample?
    private var misaligned = false
    private var pendingCheck: DispatchWorkItem?
    private var episode: (span: Telemetry.Span, since: Date, reported: Bool)?
    private var lastReport = Date.distantPast
    private var baselineSessionId: String?

    struct ScrollSample: Equatable {
        var offsetX: CGFloat
        var contentWidth: CGFloat
        var containerWidth: CGFloat
        var insetLeft: CGFloat
        var insetRight: CGFloat
    }

    private init() {
        let nc = NotificationCenter.default
        for (name, label) in [(UIResponder.keyboardWillShowNotification, "will_show"),
                              (UIResponder.keyboardWillHideNotification, "will_hide")] {
            nc.addObserver(forName: name, object: nil, queue: .main) { note in
                let height = (note.userInfo?[UIResponder.keyboardFrameEndUserInfoKey] as? CGRect)?.height ?? 0
                MainActor.assumeIsolated {
                    TranscriptProbe.shared.note(.keyboard, ["event": label, "height": Double(height)])
                }
            }
        }
    }

    // MARK: - Inputs

    func context(sessionId: String, eventCount: Int) {
        let opened = sessionId != self.sessionId
        if opened { Telemetry.shared.contextSessionId = sessionId }
        let countChanged = eventCount != self.eventCount
        self.sessionId = sessionId
        self.eventCount = eventCount
        if opened {
            sessionOpenedAt = Date()
            visibleRows.removeAll()
            evaluateBlank()
        } else if countChanged {
            evaluateBlank()   // an empty chat that gets its first event is blank-eligible again
        }
        if eventCount > 0 { logBaselineIfNeeded() }   // measurements usually arrive first
    }

    func note(_ ring: Ring, _ entry: [String: Any]) {
        var e = entry
        e["t"] = ISO8601DateFormatter.withFraction.string(from: (entry["at"] as? Date) ?? Date())
        e["at"] = nil
        var list = rings[ring] ?? []
        list.append(e)
        if list.count > ringCap { list.removeFirst(list.count - ringCap) }
        rings[ring] = list
        // A successful load: from then on, zero events in that session means an empty chat. Kept
        // per session, because a cached load can finish before the view switches sessions.
        if ring == .loads, entry["event"] as? String == "load_end", entry["ok"] as? Bool == true,
           let id = entry["session_id"] as? String, loadedSessions.insert(id).inserted, id == sessionId {
            evaluateBlank()
        }
    }

    func viewportChanged(minX: CGFloat, width: CGFloat) {
        if viewport?.width != width { note(.viewport, ["min_x": Double(minX), "width": Double(width)]) }
        viewport = (minX, width)
        evaluate()
    }

    func columnChanged(minX: CGFloat, width: CGFloat) {
        column = (minX, width)
        evaluate()
    }

    func scrollChanged(_ sample: ScrollSample) {
        note(.scrollX, ["offset_x": Double(sample.offsetX), "content_width": Double(sample.contentWidth),
                        "container_width": Double(sample.containerWidth)])
        scroll = sample
        evaluate()
    }

    /// A row's width (rows include the 16pt side padding, so a normal row is as wide as the
    /// viewport). Only rows wider than the viewport are kept.
    func rowWidth(id: String, kind: String, width: CGFloat) {
        guard let viewport, width > viewport.width + 0.5 else { return }
        note(.wideRows, ["id": id, "kind": kind, "width": Double(width),
                         "excess": Double(width - viewport.width)])
    }

    // MARK: - Blank screen

    private var visibleRows: Set<String> = []
    private var sessionOpenedAt = Date()
    /// Sessions whose events have loaded successfully at least once.
    private var loadedSessions: Set<String> = []
    private var blankCheck: DispatchWorkItem?
    private var blankEpisode: (span: Telemetry.Span, since: Date, reported: Bool)?
    private var lastBlankReport = Date.distantPast

    /// A row became visible in the scroll view, or stopped being visible (or was destroyed).
    func rowVisible(id: String, _ visible: Bool) {
        let before = visibleRows.isEmpty
        if visible { visibleRows.insert(id) } else { visibleRows.remove(id) }
        if before != visibleRows.isEmpty { evaluateBlank() }
    }

    /// No row on screen in an open session. A session that loaded with no events (a new chat) has
    /// nothing to show, so it isn't blank (docs/plan-observability.md → Blank screen probe).
    private var isBlank: Bool {
        !sessionId.isEmpty && visibleRows.isEmpty && !(eventCount == 0 && loadedSessions.contains(sessionId))
    }

    private func evaluateBlank() {
        if isBlank {
            guard blankCheck == nil, blankEpisode == nil else { return }
            // Only a blank that lasts 1 s counts (row swaps during layout leave one-frame gaps).
            let work = DispatchWorkItem { [weak self] in
                self?.blankCheck = nil
                self?.confirmBlank()
            }
            blankCheck = work
            DispatchQueue.main.asyncAfter(deadline: .now() + 1.0, execute: work)
        } else {
            blankCheck?.cancel()
            blankCheck = nil
            if let ep = blankEpisode {
                blankEpisode = nil
                let seconds = Date().timeIntervalSince(ep.since) + 1.0
                Telemetry.shared.log("mclaude.transcript.unblanked",
                                     attributes: ["duration_s": seconds, "reported": ep.reported,
                                                  "mclaude.session_id": sessionId, "event_count": eventCount],
                                     in: ep.span.context)
                ep.span.end(attributes: ["duration_s": seconds])
            }
        }
    }

    private func confirmBlank() {
        guard isBlank, blankEpisode == nil,
              UIApplication.shared.applicationState == .active else { return }
        let span = Telemetry.shared.startSpan("transcript.blank", attributes: ["mclaude.session_id": sessionId])
        let report = Date().timeIntervalSince(lastBlankReport) > 120
        blankEpisode = (span, Date(), report)
        guard report else { return }
        lastBlankReport = Date()

        var body: [String: Any] = [
            "session_id": sessionId, "event_count": eventCount, "visible_rows": 0,
            "since_session_open_s": Date().timeIntervalSince(sessionOpenedAt),
        ]
        if let sv = Self.transcriptScrollView(viewportWidth: viewport?.width) {
            body["scroll_view"] = [
                "offset_y": Double(sv.contentOffset.y), "offset_x": Double(sv.contentOffset.x),
                "content_height": Double(sv.contentSize.height), "content_width": Double(sv.contentSize.width),
                "bounds_height": Double(sv.bounds.height),
                "inset_top": Double(sv.adjustedContentInset.top), "inset_bottom": Double(sv.adjustedContentInset.bottom),
                "dragging": sv.isDragging, "decelerating": sv.isDecelerating,
            ]
        } else {
            body["scroll_view"] = "not found"
        }
        for ring in Ring.allCases { body["recent_\(ring.rawValue)"] = rings[ring] ?? [] }
        let ctx = span.context
        Telemetry.shared.log("mclaude.rule.broken", body: Telemetry.json(body),
                             attributes: ["rule": "transcript_blank", "mclaude.session_id": sessionId,
                                          "event_count": eventCount],
                             in: ctx, severity: 13)
        TelemetryScreenshot.capture { path in
            Telemetry.shared.log("mclaude.rule.screenshot", body: path,
                                 attributes: ["rule": "transcript_blank", "path": path ?? "",
                                              "uploaded": path != nil],
                                 in: ctx)
        }
    }

    /// The transcript's UIScrollView, read only when a blank is confirmed: the tallest vertical
    /// scroll view as wide as the viewport.
    private static func transcriptScrollView(viewportWidth: CGFloat?) -> UIScrollView? {
        guard let window = UIApplication.shared.connectedScenes
            .compactMap({ ($0 as? UIWindowScene)?.keyWindow }).first else { return nil }
        var best: UIScrollView?
        var stack: [UIView] = [window]
        while let v = stack.popLast() {
            if let sv = v as? UIScrollView, !(sv is UITextView),
               viewportWidth == nil || abs(sv.bounds.width - (viewportWidth ?? 0)) < 1,
               sv.bounds.height > (best?.bounds.height ?? 0) {
                best = sv
            }
            stack.append(contentsOf: v.subviews)
        }
        return best
    }

    // MARK: - Evaluation

    private var isAligned: Bool {
        guard let viewport, let column else { return true }
        if abs(column.minX - viewport.minX) > 0.5 || column.width > viewport.width + 0.5 { return false }
        if let s = scroll {
            if abs(s.offsetX + s.insetLeft) > 0.5 || s.contentWidth > s.containerWidth + 0.5 { return false }
        }
        return true
    }

    private func evaluate() {
        logBaselineIfNeeded()
        let aligned = isAligned
        if !aligned && !misaligned {
            misaligned = true
            // Layout passes report one-frame sizes; only a state that lasts 300 ms counts.
            let work = DispatchWorkItem { [weak self] in self?.confirmMisaligned() }
            pendingCheck = work
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.3, execute: work)
        } else if aligned && misaligned {
            misaligned = false
            pendingCheck?.cancel()
            pendingCheck = nil
            if let ep = episode {
                episode = nil
                let seconds = Date().timeIntervalSince(ep.since)
                Telemetry.shared.log("mclaude.transcript.realigned",
                                     attributes: ["duration_s": seconds, "reported": ep.reported,
                                                  "mclaude.session_id": sessionId],
                                     in: ep.span.context)
                ep.span.end(attributes: ["duration_s": seconds])
            }
        }
    }

    /// Once per opened session: the first full measurement, so normal values are on record.
    private func logBaselineIfNeeded() {
        guard baselineSessionId != sessionId, !sessionId.isEmpty, eventCount > 0,
              let v = viewport, let c = column else { return }
        baselineSessionId = sessionId
        var attrs: [String: Any] = [
            "mclaude.session_id": sessionId, "aligned": isAligned,
            "viewport_min_x": Double(v.minX), "viewport_width": Double(v.width),
            "column_min_x": Double(c.minX), "column_width": Double(c.width),
        ]
        if let s = scroll {
            attrs["scroll_offset_x"] = Double(s.offsetX)
            attrs["scroll_content_width"] = Double(s.contentWidth)
            attrs["scroll_container_width"] = Double(s.containerWidth)
        }
        Telemetry.shared.log("mclaude.transcript.baseline", attributes: attrs)
    }

    private func confirmMisaligned() {
        guard misaligned, episode == nil, !isAligned else { return }
        let span = Telemetry.shared.startSpan("transcript.misaligned",
                                              attributes: ["mclaude.session_id": sessionId])
        let report = Date().timeIntervalSince(lastReport) > 120
        episode = (span, Date(), report)
        guard report else { return }
        lastReport = Date()

        var body: [String: Any] = ["session_id": sessionId, "event_count": eventCount]
        if let v = viewport { body["viewport"] = ["min_x": Double(v.minX), "width": Double(v.width)] }
        if let c = column {
            body["column"] = ["min_x": Double(c.minX), "width": Double(c.width)]
            if let v = viewport {
                body["left_shift"] = Double(v.minX - c.minX)
                body["extra_width"] = Double(c.width - v.width)
            }
        }
        if let s = scroll {
            body["scroll"] = ["offset_x": Double(s.offsetX), "content_width": Double(s.contentWidth),
                              "container_width": Double(s.containerWidth),
                              "inset_left": Double(s.insetLeft), "inset_right": Double(s.insetRight)]
        } else {
            body["scroll"] = "unavailable (iOS 17)"
        }
        let wide = (rings[.wideRows] ?? []).sorted {
            ($0["width"] as? Double ?? 0) > ($1["width"] as? Double ?? 0)
        }
        body["widest_rows"] = Array(wide.prefix(5))
        for ring in Ring.allCases { body["recent_\(ring.rawValue)"] = rings[ring] ?? [] }

        let ctx = span.context
        let leftShift = body["left_shift"] as? Double ?? 0
        Telemetry.shared.log("mclaude.rule.broken", body: Telemetry.json(body),
                             attributes: ["rule": "transcript_misaligned", "mclaude.session_id": sessionId,
                                          "left_shift": leftShift],
                             in: ctx, severity: 13)
        TelemetryScreenshot.capture { path in
            Telemetry.shared.log("mclaude.rule.screenshot", body: path,
                                 attributes: ["rule": "transcript_misaligned", "path": path ?? "",
                                              "uploaded": path != nil],
                                 in: ctx)
        }
    }
}

private extension ISO8601DateFormatter {
    static let withFraction: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()
}

// MARK: - View hooks

private struct HFrame: Equatable {
    var minX: CGFloat
    var width: CGFloat
    init(_ rect: CGRect) {
        minX = (rect.minX * 2).rounded() / 2
        width = (rect.width * 2).rounded() / 2
    }
}

extension View {
    /// On the transcript's scroll view: its visible frame, horizontal scroll geometry and context.
    func transcriptViewportProbe(sessionId: String, eventCount: Int) -> some View {
        modifier(TranscriptViewportProbe(sessionId: sessionId, eventCount: eventCount))
    }

    /// On the transcript column (the row stack with its padding).
    func transcriptColumnProbe() -> some View {
        onGeometryChange(for: HFrame.self) { HFrame($0.frame(in: .global)) } action: { f in
            TranscriptProbe.shared.columnChanged(minX: f.minX, width: f.width)
        }
    }

    /// On each transcript row: its width, and whether it's on screen.
    func transcriptRowProbe(id: String, kind: String) -> some View {
        onGeometryChange(for: CGFloat.self) { ($0.size.width * 2).rounded() / 2 } action: { w in
            TranscriptProbe.shared.rowWidth(id: id, kind: kind, width: w)
        }
        .onGeometryChange(for: Bool.self) { proxy in
            guard let visible = proxy.bounds(of: .scrollView) else { return false }
            return CGRect(origin: .zero, size: proxy.size).intersects(visible)
        } action: { visible in
            TranscriptProbe.shared.rowVisible(id: id, visible)
        }
        .onDisappear { TranscriptProbe.shared.rowVisible(id: id, false) }
    }
}

private struct TranscriptViewportProbe: ViewModifier {
    let sessionId: String
    let eventCount: Int

    func body(content: Content) -> some View {
        let measured = content
            .onGeometryChange(for: HFrame.self) { HFrame($0.frame(in: .global)) } action: { f in
                TranscriptProbe.shared.viewportChanged(minX: f.minX, width: f.width)
            }
            .onChange(of: eventCount, initial: true) { _, count in
                TranscriptProbe.shared.context(sessionId: sessionId, eventCount: count)
            }
        if #available(iOS 18.0, *) {
            measured.onScrollGeometryChange(for: TranscriptProbe.ScrollSample.self) { g in
                func r(_ v: CGFloat) -> CGFloat { (v * 2).rounded() / 2 }
                return .init(offsetX: r(g.contentOffset.x), contentWidth: r(g.contentSize.width),
                             containerWidth: r(g.containerSize.width),
                             insetLeft: r(g.contentInsets.leading), insetRight: r(g.contentInsets.trailing))
            } action: { _, sample in
                TranscriptProbe.shared.scrollChanged(sample)
            }
        } else {
            measured
        }
    }
}
