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
        case rows, context, scene
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
        // App lifecycle: F-5's real blank started right at the return to the foreground.
        for (name, label) in [(UIApplication.didBecomeActiveNotification, "active"),
                              (UIApplication.willResignActiveNotification, "resign_active"),
                              (UIApplication.didEnterBackgroundNotification, "background"),
                              (UIApplication.willEnterForegroundNotification, "foreground")] {
            nc.addObserver(forName: name, object: nil, queue: .main) { _ in
                MainActor.assumeIsolated { TranscriptProbe.shared.note(.scene, ["event": label]) }
            }
        }
    }

    // MARK: - Inputs

    func context(sessionId: String, eventCount: Int) {
        let opened = sessionId != self.sessionId
        if opened { Telemetry.shared.contextSessionId = sessionId }
        let countChanged = eventCount != self.eventCount
        note(.context, ["session_id": sessionId, "opened": opened, "event_count": eventCount,
                        "on_screen": visibleRowCount])
        self.sessionId = sessionId
        self.eventCount = eventCount
        if opened {
            sessionOpenedAt = Date()
            evaluateBlank()
        } else if countChanged {
            evaluateBlank()   // an empty chat that gets its first event is blank-eligible again
        }
        if eventCount > 0 { logBaselineIfNeeded() }   // measurements usually arrive first
    }

    /// All ring buffers, for records outside the probe (stalls).
    func ringsBody() -> [String: Any] {
        var body: [String: Any] = ["session_id": sessionId, "event_count": eventCount]
        for ring in Ring.allCases { body["recent_\(ring.rawValue)"] = rings[ring] ?? [] }
        return body
    }

    func note(_ ring: Ring, _ entry: [String: Any]) {
        var e = entry
        e["t"] = ISO8601DateFormatter.withFraction.string(from: (entry["at"] as? Date) ?? Date())
        e["at"] = nil
        var list = rings[ring] ?? []
        list.append(e)
        let cap = ring == .rows ? 80 : ringCap   // row on/off changes come in bursts
        if list.count > cap { list.removeFirst(list.count - cap) }
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

    private var sessionOpenedAt = Date()
    /// Sessions whose events have loaded successfully at least once.
    private var loadedSessions: Set<String> = []
    private var blankCheck: DispatchWorkItem?
    private var blankEpisode: (span: Telemetry.Span, since: Date, reported: Bool, kind: String, scrollAtBlank: Any)?
    /// Last report per blank kind: a slow first load must not use up the report for a rendering
    /// blank right after it (docs/plan-observability.md → Blank screen probe, Ops F-8).
    private var lastBlankReport: [String: Date] = [:]

    /// Row bookkeeping, per row view instance ("id#token") (docs/plan-observability.md → Blank
    /// screen probe; Ops F-9/F-11). A row is on screen when its view has appeared and not
    /// disappeared, and its last measured visibility is on. The visibility is kept across a
    /// disappear: the lazy stack can fire onDisappear and then onAppear again on a view that never
    /// moved, and a geometry callback only fires when the value changes, so it won't say "visible"
    /// again. Session switches don't clear anything: the old session's rows disappear on their own.
    private var geometryVisible: [String: Bool] = [:]
    private var liveInstances: Set<String> = []
    private var onScreen: Set<String> = []
    private var bottomSentinelVisible = false

    /// Distinct row ids on screen.
    private var visibleRowCount: Int { Set(onScreen.map { $0.split(separator: "#").first ?? "" }).count }

    /// A row's measured visibility in the scroll view changed.
    func rowVisible(id: String, instance: String, _ visible: Bool) {
        let key = "\(id)#\(instance)"
        geometryVisible[key] = visible
        update(key, id: id, instance: instance, source: "geometry", visible: visible)
    }

    /// A row view appeared (was built by the lazy stack) or disappeared.
    func rowLive(id: String, instance: String, _ alive: Bool) {
        let key = "\(id)#\(instance)"
        if alive { liveInstances.insert(key) } else { liveInstances.remove(key) }
        if !alive, liveInstances.count < geometryVisible.count - 2000 {
            // Bound memory: forget instances that are gone (keeping the ones still on screen).
            geometryVisible = geometryVisible.filter { liveInstances.contains($0.key) }
        }
        update(key, id: id, instance: instance, source: alive ? "appear" : "gone", visible: geometryVisible[key] ?? false)
    }

    private func update(_ key: String, id: String, instance: String, source: String, visible: Bool) {
        let wasBlank = onScreen.isEmpty
        if liveInstances.contains(key) && geometryVisible[key] == true { onScreen.insert(key) } else { onScreen.remove(key) }
        note(.rows, ["id": id, "inst": instance, "visible": visible, "live": liveInstances.contains(key),
                     "src": source, "set": visibleRowCount])
        if wasBlank != onScreen.isEmpty { evaluateBlank() }
    }

    /// The transcript's bottom sentinel came on or went off screen.
    func bottomSentinel(_ visible: Bool) { bottomSentinelVisible = visible }

    /// No row on screen in an open session. A session that loaded with no events (a new chat) has
    /// nothing to show, so it isn't blank (docs/plan-observability.md → Blank screen probe).
    private var isBlank: Bool {
        !sessionId.isEmpty && onScreen.isEmpty && !(eventCount == 0 && loadedSessions.contains(sessionId))
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
                // Every episode, reported or not, keeps its evidence (Ops F-8).
                var body: [String: Any] = [
                    "session_id": sessionId, "event_count": eventCount, "kind": ep.kind,
                    "duration_s": seconds, "reported": ep.reported,
                    "scroll_view_at_blank": ep.scrollAtBlank,
                    "scroll_view_at_unblank": Self.scrollState(viewportWidth: viewport?.width),
                ]
                for ring in Ring.allCases { body["recent_\(ring.rawValue)"] = rings[ring] ?? [] }
                Telemetry.shared.log("mclaude.transcript.unblanked", body: Telemetry.json(body),
                                     attributes: ["duration_s": seconds, "reported": ep.reported, "kind": ep.kind,
                                                  "mclaude.session_id": sessionId, "event_count": eventCount],
                                     in: ep.span.context)
                ep.span.end(attributes: ["duration_s": seconds])
            }
        }
    }

    private func confirmBlank() {
        guard isBlank, blankEpisode == nil,
              UIApplication.shared.applicationState == .active else { return }
        let kind = eventCount > 0 ? "rows_offscreen" : "not_loaded"
        let span = Telemetry.shared.startSpan("transcript.blank",
                                              attributes: ["mclaude.session_id": sessionId, "kind": kind])
        let report = Date().timeIntervalSince(lastBlankReport[kind] ?? .distantPast) > 120
        let scrollState = Self.scrollState(viewportWidth: viewport?.width)
        blankEpisode = (span, Date(), report, kind, scrollState)
        guard report else { return }
        lastBlankReport[kind] = Date()

        var body: [String: Any] = [
            "session_id": sessionId, "event_count": eventCount, "visible_rows": 0, "kind": kind,
            "since_session_open_s": Date().timeIntervalSince(sessionOpenedAt),
            "visible_instances": geometryVisible.filter(\.value).count, "live_instances": liveInstances.count,
            "visible_instance_examples": Array(geometryVisible.filter(\.value).keys.sorted().prefix(10)),
            "live_instance_examples": Array(liveInstances.sorted().prefix(10)),
            "bottom_sentinel_visible": bottomSentinelVisible,
        ]
        body["scroll_view"] = scrollState
        for ring in Ring.allCases { body["recent_\(ring.rawValue)"] = rings[ring] ?? [] }
        let ctx = span.context
        let capturedAt = ISO8601DateFormatter.withFraction.string(from: Date())   // drawn synchronously below
        Telemetry.shared.log("mclaude.rule.broken", body: Telemetry.json(body),
                             attributes: ["rule": "transcript_blank", "mclaude.session_id": sessionId,
                                          "event_count": eventCount, "kind": kind],
                             in: ctx, severity: 13)
        TelemetryScreenshot.capture { path in
            Telemetry.shared.log("mclaude.rule.screenshot", body: path,
                                 attributes: ["rule": "transcript_blank", "path": path ?? "",
                                              "uploaded": path != nil, "captured_at": capturedAt],
                                 in: ctx)
        }
    }

    /// The transcript scroll view's state, for blank reports and unblank records.
    private static func scrollState(viewportWidth: CGFloat?) -> Any {
        guard let sv = transcriptScrollView(viewportWidth: viewportWidth) else { return "not found" }
        return [
            "offset_y": Double(sv.contentOffset.y), "offset_x": Double(sv.contentOffset.x),
            "content_height": Double(sv.contentSize.height), "content_width": Double(sv.contentSize.width),
            "bounds_height": Double(sv.bounds.height),
            "inset_top": Double(sv.adjustedContentInset.top), "inset_bottom": Double(sv.adjustedContentInset.bottom),
            "dragging": sv.isDragging, "decelerating": sv.isDecelerating,
        ] as [String: Any]
    }

    /// The transcript's UIScrollView, read only when a blank is confirmed or ends: the tallest vertical
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
        modifier(TranscriptRowProbe(id: id, kind: kind))
    }
}

private struct TranscriptRowProbe: ViewModifier {
    let id: String
    let kind: String
    /// Tells two views of the same row id apart (diagnostics).
    @State private var instance = String(UUID().uuidString.prefix(6))

    func body(content: Content) -> some View {
        content
            .onGeometryChange(for: CGFloat.self) { ($0.size.width * 2).rounded() / 2 } action: { w in
                TranscriptProbe.shared.rowWidth(id: id, kind: kind, width: w)
            }
            .onGeometryChange(for: Bool.self) { proxy in
                guard let visible = proxy.bounds(of: .scrollView) else { return false }
                return CGRect(origin: .zero, size: proxy.size).intersects(visible)
            } action: { visible in
                TranscriptProbe.shared.rowVisible(id: id, instance: instance, visible)
            }
            .onAppear { TranscriptProbe.shared.rowLive(id: id, instance: instance, true) }
            .onDisappear { TranscriptProbe.shared.rowLive(id: id, instance: instance, false) }
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
