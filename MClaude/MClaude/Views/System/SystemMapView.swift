import SwiftUI

// System map (docs/plan-observability.md → System map → Map screen): boxes for phone, Mac, Obs,
// server, Claude and Bedrock with coloured lines between them, the events of the selected moment,
// and a 24 h scrubber. Live polls the map every 3 s and the timeline every 60 s, only while the
// screen is visible and the app is active.

// MARK: - State styling

extension ObsState {
    var color: Color {
        switch self {
        case .ok: Theme.success
        case .degraded: Theme.warning
        case .failing: Theme.danger
        case .nodata: Theme.textMuted
        }
    }

    var word: String {
        switch self {
        case .ok: "ok"
        case .degraded: "degraded"
        case .failing: "failing"
        case .nodata: "no data"
        }
    }
}

extension ObsNode {
    // Bedrock "idle" (no model request) arrives as state ok with value "idle", so no special case.
    var stateWord: String { state.word }
    var stateColor: Color { state.color }

    var accessibilityText: String {
        [label, stateWord, value, reason].compactMap { $0?.isEmpty == false ? $0 : nil }.joined(separator: ", ")
    }
}

enum ObsFormat {
    static func hm(_ d: Date) -> String { d.formatted(date: .omitted, time: .shortened) }
    static func hms(_ d: Date) -> String { d.formatted(.dateTime.hour().minute().second()) }
}

// MARK: - Model

@MainActor @Observable
final class SystemMapModel {
    var host = ""
    var client: ObsAPIClient { ObsAPIClient(host: host) }

    private(set) var map: ObsMap?
    private(set) var timeline: ObsTimeline?
    /// The range the timeline was requested for (the last 24 h when it was fetched).
    private(set) var timelineRange: ClosedRange<Date>?
    private(set) var live = true
    /// The scrubbed moment; nil while Live.
    private(set) var selectedAt: Date?
    /// The latest map request failed (the last map, if any, stays on screen).
    private(set) var failed = false
    private(set) var lastUpdated: Date?

    /// Delivered Ops findings by trace ID: which finding sheets get a Show in Ops button.
    private(set) var findingByTrace: [String: String] = [:]

    private var scrubTask: Task<Void, Never>?
    private var scrubGeneration = 0
    /// Maps for scrubbed moments, by request second; cleared when Live comes back.
    private var scrubCache: [Int: ObsMap] = [:]

    static let pollInterval: Duration = .seconds(3)
    static let timelineInterval: TimeInterval = 60
    static let span: TimeInterval = 24 * 3600
    static let bins = 96

    /// Runs until cancelled (the view went away, the app left the foreground, or Live was turned off).
    func pollLive() async {
        var lastTimeline = Date.distantPast
        while !Task.isCancelled, live {
            await loadMap(at: nil)
            if timeline == nil || Date().timeIntervalSince(lastTimeline) >= Self.timelineInterval {
                if await loadTimeline() { lastTimeline = Date() }
                await loadFindings()
            }
            try? await Task.sleep(for: Self.pollInterval)
        }
    }

    private func loadMap(at: Date?) async {
        do {
            let m = try await client.map(at: at)
            guard !Task.isCancelled else { return }
            // Drop answers for a moment that's no longer selected.
            if at == nil ? !live : (live || selectedAt != at) { return }
            map = m
            failed = false
            lastUpdated = Date()
            if at == nil { SystemStatus.shared.anyFailing = m.anyFailing }
        } catch {
            guard !Task.isCancelled else { return }
            if at == nil ? live : (!live && selectedAt == at) { failed = true }
        }
    }

    @discardableResult
    private func loadTimeline() async -> Bool {
        let to = Date(), from = to.addingTimeInterval(-Self.span)
        guard let t = try? await client.timeline(from: from, to: to, bins: Self.bins), !Task.isCancelled else { return false }
        timeline = t
        timelineRange = from...to
        return true
    }

    @discardableResult
    func loadFindings() async -> Bool {
        guard let list = try? await client.findings(), !Task.isCancelled else { return false }
        var byTrace: [String: String] = [:]
        for f in list.reversed() where f.delivered {   // oldest first, so the newest finding wins a shared trace
            for t in f.traceIds { byTrace[t] = f.id }
        }
        findingByTrace = byTrace
        return true
    }

    /// The delivered finding an event belongs to. Not linked yet: fetch the list once more.
    func resolveFinding(trace: String) async -> String? {
        if let id = findingByTrace[trace] { return id }
        await loadFindings()
        return findingByTrace[trace]
    }

    /// Scrubbing: Live off, and the map follows the finger. A cached moment shows at once; otherwise
    /// one fetch runs at a time, and when it lands it fetches whatever is selected by then, so the
    /// map keeps updating mid-drag and the moment under the finger when it stops is fetched last.
    func select(_ date: Date) {
        live = false
        selectedAt = date
        if let m = scrubCache[Self.scrubKey(date)] { show(m) }
        guard scrubTask == nil else { return }
        scrubGeneration += 1
        let gen = scrubGeneration
        scrubTask = Task { [weak self] in
            await self?.followScrub()
            if self?.scrubGeneration == gen { self?.scrubTask = nil }
        }
    }

    private func followScrub() async {
        while !Task.isCancelled, !live, let at = selectedAt {
            let key = Self.scrubKey(at)
            if scrubCache[key] == nil {
                do {
                    let m = try await client.map(at: at)
                    guard !Task.isCancelled, !live else { return }
                    if scrubCache.count >= 300 { scrubCache.removeAll() }
                    scrubCache[key] = m
                } catch {
                    guard !Task.isCancelled, !live else { return }
                    // Only a failure for the moment still selected counts; otherwise try the newer one.
                    if selectedAt.map(Self.scrubKey) == key { failed = true; return }
                    continue
                }
            }
            // Shown even if the finger has moved on: it's still newer than what's on screen.
            if let m = scrubCache[key] { show(m) }
            if selectedAt.map(Self.scrubKey) == key { return }
        }
    }

    private func show(_ m: ObsMap) {
        map = m
        failed = false
        lastUpdated = Date()
    }

    /// The request's `at`, in whole seconds (the client rounds up the same way).
    private static func scrubKey(_ d: Date) -> Int { Int(d.timeIntervalSince1970.rounded(.up)) }

    func goLive() {
        scrubTask?.cancel()
        scrubTask = nil
        scrubCache.removeAll()
        selectedAt = nil
        live = true
    }

    func retry() async {
        failed = false
        await loadMap(at: live ? nil : selectedAt)
        if timeline == nil { await loadTimeline() }
    }
}

// MARK: - Screen

struct SystemMapView: View {
    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase
    @State private var model = SystemMapModel()
    @State private var detailNodeId: NodeRef?
    @State private var finding: FindingRef?

    private struct NodeRef: Identifiable { let id: String }
    struct FindingRef: Identifiable { let id: String; let nodeLabel: String? }
    private struct PollKey: Equatable { let live: Bool; let active: Bool; let host: String }

    var body: some View {
        NavigationStack {
            content
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(Theme.bgPage)
                .navigationTitle("System")
                .navigationBarTitleDisplayMode(.inline)
                .toolbarBackground(Theme.bgPage, for: .navigationBar)
                .toolbar {
                    ToolbarItem(placement: .topBarLeading) {
                        Button("Done") { dismiss() }
                            .foregroundStyle(Theme.textSecondary)
                    }
                    ToolbarItem(placement: .topBarTrailing) { liveButton }
                }
        }
        .tint(Theme.clay)
        .task(id: PollKey(live: model.live, active: scenePhase == .active, host: appState.serverHost)) {
            model.host = appState.serverHost
            guard model.live, scenePhase == .active else { return }
            await model.pollLive()
        }
        .sheet(item: $detailNodeId) { ref in
            NodeDetailSheet(node: node(ref.id))
        }
        .sheet(item: $finding) { ref in
            FindingSheet(traceId: ref.id, nodeLabel: ref.nodeLabel, client: model.client,
                         opsTarget: { await opsTarget(trace: ref.id) },
                         onShowInOps: showInOps)
        }
    }

    private var liveButton: some View {
        Button { model.goLive() } label: {
            HStack(spacing: 5) {
                Circle()
                    .fill(model.live ? Theme.success : Theme.textMuted)
                    .frame(width: 7, height: 7)
                Text("Live")
                    .font(.claudeUISemibold)
                    .foregroundStyle(model.live ? Theme.textPrimary : Theme.clay)
            }
        }
        .disabled(model.live)
        .accessibilityLabel(model.live ? "Live" : "Return to live")
        .accessibilityValue(model.live ? "on" : "off")
    }

    // MARK: Content

    @ViewBuilder
    private var content: some View {
        if let map = model.map {
            VStack(spacing: 0) {
                MapBoard(map: map) { detailNodeId = NodeRef(id: $0) }
                    .padding(.horizontal, 16)
                    .padding(.top, 14)
                eventsStrip(map)
                TimelineScrubber(
                    timeline: model.timeline,
                    range: model.timelineRange,
                    selected: model.selectedAt,
                    onSelect: { model.select($0) }
                )
                .padding(.horizontal, 16)
                .padding(.bottom, 8)
            }
        } else if model.failed {
            VStack(spacing: 10) {
                Text("Can't reach observability (\(model.client.hostLabel))")
                    .font(.system(size: 20, design: .serif))
                    .foregroundStyle(Theme.textPrimary)
                    .multilineTextAlignment(.center)
                Button("Retry") { Task { await model.retry() } }
                    .font(.claudeUISemibold)
                    .foregroundStyle(Theme.clay)
            }
            .padding(.horizontal, 32)
        } else {
            ProgressView("Loading system map…")
                .tint(Theme.textMuted)
                .foregroundStyle(Theme.textMuted)
        }
    }

    private func node(_ id: String) -> ObsNode {
        model.map.flatMap { m in m.nodes.first { $0.id == id } } ?? MapBoard.placeholder(id)
    }

    private func nodeLabel(_ id: String?) -> String? {
        guard let id else { return nil }
        return model.map?.nodes.first { $0.id == id }?.label ?? MapBoard.defaultLabels[id] ?? id
    }

    // MARK: Events strip

    /// Where the finding sheet's Show in Ops lands: the delivered finding holding `trace`, in the
    /// Ops session. Nil (no button) when it was held, there's no finding, or no Ops session.
    private func opsTarget(trace: String) async -> FindingFocus? {
        guard let findingId = await model.resolveFinding(trace: trace), let ops = appState.opsSessionId else { return nil }
        return FindingFocus(sessionId: ops, findingId: findingId)
    }

    /// docs/plan-observability.md → Map screen → Finding sheet → Show in Ops.
    private func showInOps(_ focus: FindingFocus) {
        finding = nil
        appState.findingFocus = focus
        dismiss()
    }

    private func eventsStrip(_ map: ObsMap) -> some View {
        let events = map.events.sorted { $0.t > $1.t }
        return VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                Rectangle().fill(Theme.border).frame(width: 14, height: 1)
                Text("At \(ObsFormat.hm(Date(timeIntervalSince1970: map.at)))")
                    .font(.claudeCaptionSemibold)
                    .foregroundStyle(Theme.textSecondary)
                if model.failed, let last = model.lastUpdated {
                    Text("Last updated \(ObsFormat.hms(last))")
                        .font(.claudeCaption)
                        .foregroundStyle(Theme.warning)
                        .lineLimit(1)
                        .fixedSize()
                }
                Rectangle().fill(Theme.border).frame(height: 1)
            }
            .padding(.horizontal, 16)
            .padding(.top, 14)
            .padding(.bottom, 4)
            .accessibilityElement(children: .combine)

            if events.isEmpty {
                Text("No events at this moment")
                    .font(.claudeCaption)
                    .foregroundStyle(Theme.textMuted)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 8)
                Spacer(minLength: 0)
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 0) {
                        ForEach(events) { eventRow($0) }
                    }
                }
            }
        }
        .frame(maxHeight: .infinity, alignment: .top)
    }

    @ViewBuilder
    private func eventRow(_ e: ObsEvent) -> some View {
        let label = nodeLabel(e.node)
        let title = e.title
        let row = HStack(spacing: 8) {
            Text(e.isRule ? "▲" : "•")
                .font(.claudeCaption)
                .foregroundStyle(e.isRule ? Theme.danger : Theme.textMuted)
                .frame(width: 12)
            Text([title, label].compactMap { $0 }.joined(separator: " · "))
                .font(e.isRule ? .claudeCaptionSemibold : .claudeCaption)
                .foregroundStyle(e.isRule ? Theme.textPrimary : Theme.textSecondary)
                .lineLimit(1)
            Spacer(minLength: 4)
            Text(ObsFormat.hms(Date(timeIntervalSince1970: e.t)))
                .font(.claudeMono(11))
                .foregroundStyle(Theme.textMuted)
            if e.isRule, e.traceId != nil {
                Image(systemName: "chevron.right")
                    .font(.system(size: 11, weight: .regular))
                    .foregroundStyle(Theme.textMuted)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 7)
        .contentShape(Rectangle())

        if e.isRule, let trace = e.traceId {
            Button { finding = FindingRef(id: trace, nodeLabel: label) } label: { row }
                .buttonStyle(DrawerRowStyle(selected: false))
                .accessibilityLabel("Rule break \(title)\(label.map { " on \($0)" } ?? ""), \(ObsFormat.hms(Date(timeIntervalSince1970: e.t)))")
                .accessibilityHint("Opens the finding")
        } else {
            row.accessibilityElement(children: .combine)
        }
    }
}

// MARK: - Board (boxes and lines)

/// Fixed two-column layout from the wireframe: Phone, Mac, Obs on the left; Server, Claude,
/// Bedrock on the right. Lines are drawn in a Canvas behind the boxes; Mac and Obs have none.
private struct MapBoard: View {
    let map: ObsMap
    let onTap: (String) -> Void

    static let slots: [String: (col: Int, row: Int)] = [
        "phone": (0, 0), "mac": (0, 1), "obs": (0, 2),
        "server": (1, 0), "claude": (1, 1), "bedrock": (1, 2),
    ]
    static let defaultLabels = ["phone": "Phone", "server": "Server", "claude": "Claude",
                                "bedrock": "Bedrock", "mac": "Mac", "obs": "Obs"]
    static let order = ["phone", "server", "mac", "claude", "obs", "bedrock"]

    /// A node the API didn't return: drawn as "no data", never as healthy.
    static func placeholder(_ id: String) -> ObsNode {
        ObsNode(id: id, label: defaultLabels[id] ?? id, state: .nodata, value: nil, reason: "Not in the API response", details: nil)
    }

    private let boxW: CGFloat = 122
    private let boxH: CGFloat = 78
    private let gapY: CGFloat = 34

    private var height: CGFloat { boxH * 3 + gapY * 2 }

    private func frame(_ id: String, width: CGFloat) -> CGRect? {
        guard let slot = Self.slots[id] else { return nil }
        let x = slot.col == 0 ? 4 : width - boxW - 4
        return CGRect(x: x, y: CGFloat(slot.row) * (boxH + gapY), width: boxW, height: boxH)
    }

    private func nodes() -> [ObsNode] {
        Self.order.map { id in map.nodes.first { $0.id == id } ?? Self.placeholder(id) }
    }

    /// Start and end points of an edge: horizontal between columns, vertical within one.
    private func segment(_ e: ObsEdge, width: CGFloat) -> (CGPoint, CGPoint, vertical: Bool)? {
        guard let a = frame(e.from, width: width), let b = frame(e.to, width: width) else { return nil }
        if abs(a.midY - b.midY) < 1 {
            let (l, r) = a.minX < b.minX ? (a, b) : (b, a)
            return (CGPoint(x: l.maxX, y: l.midY), CGPoint(x: r.minX, y: r.midY), false)
        }
        let (t, btm) = a.minY < b.minY ? (a, b) : (b, a)
        return (CGPoint(x: t.midX, y: t.maxY), CGPoint(x: btm.midX, y: btm.minY), true)
    }

    var body: some View {
        GeometryReader { geo in
            let w = geo.size.width
            ZStack(alignment: .topLeading) {
                Canvas { ctx, _ in
                    for e in map.edges {
                        guard let seg = segment(e, width: w) else { continue }
                        let (p, q) = (seg.0, seg.1)
                        var path = Path()
                        path.move(to: p)
                        path.addLine(to: q)
                        let dash: [CGFloat] = e.state == .nodata ? [5, 4] : []
                        ctx.stroke(path, with: .color(e.state.color),
                                   style: StrokeStyle(lineWidth: 3, lineCap: .round, dash: dash))
                    }
                }
                .accessibilityHidden(true)

                ForEach(map.edges.indices, id: \.self) { i in
                    let e = map.edges[i]
                    if let seg = segment(e, width: w), let label = e.label, !label.isEmpty {
                        let (p, q, vertical) = (seg.0, seg.1, seg.vertical)
                        edgeLabel(label, edge: e)
                            .frame(width: vertical ? 120 : max(0, q.x - p.x - 8), alignment: vertical ? .trailing : .center)
                            .position(vertical ? CGPoint(x: p.x - 10 - 60, y: (p.y + q.y) / 2)
                                               : CGPoint(x: (p.x + q.x) / 2, y: p.y - 11))
                    }
                }

                ForEach(nodes()) { node in
                    if let f = frame(node.id, width: w) {
                        NodeBox(node: node) { onTap(node.id) }
                            .frame(width: f.width, height: f.height)
                            .position(x: f.midX, y: f.midY)
                    }
                }
            }
        }
        .frame(height: height)
    }

    private func edgeLabel(_ text: String, edge e: ObsEdge) -> some View {
        let from = map.nodes.first { $0.id == e.from }?.label ?? Self.defaultLabels[e.from] ?? e.from
        let to = map.nodes.first { $0.id == e.to }?.label ?? Self.defaultLabels[e.to] ?? e.to
        return Text(text)
            .font(.claudeFootnote)
            .foregroundStyle(Theme.textMuted)
            .lineLimit(1)
            .minimumScaleFactor(0.75)
            .accessibilityLabel("\(from) to \(to) link, \(e.state.word), \(text)")
    }
}

private struct NodeBox: View {
    let node: ObsNode
    let onTap: () -> Void

    var body: some View {
        Button(action: onTap) {
            VStack(alignment: .leading, spacing: 3) {
                Text(node.label)
                    .font(.claudeUISemibold)
                    .foregroundStyle(Theme.textPrimary)
                    .lineLimit(1)
                HStack(spacing: 5) {
                    Circle().fill(node.stateColor).frame(width: 7, height: 7)
                    Text(node.stateWord)
                        .font(.claudeCaptionSemibold)
                        .foregroundStyle(node.stateColor)
                }
                Text(node.value ?? " ")
                    .font(.claudeMono(11))
                    .foregroundStyle(Theme.textSecondary)
                    .lineLimit(1)
                    .minimumScaleFactor(0.7)
            }
            .padding(.horizontal, 10)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)
            .background(Theme.bgRaised, in: RoundedRectangle(cornerRadius: Theme.radiusCard, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: Theme.radiusCard, style: .continuous)
                    .strokeBorder(node.stateColor.opacity(node.state == .nodata ? 0.6 : 1),
                                  style: StrokeStyle(lineWidth: 1.5,
                                                     dash: node.state == .nodata ? [4, 3] : []))
            )
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(node.accessibilityText)
        .accessibilityAddTraits(.isButton)
        .accessibilityHint("Shows details")
    }
}

// MARK: - Scrubber

/// The last 24 h in bins coloured by their worst state, with ▲ pins for rule breaks. Dragging
/// selects a moment (snapping to a pin within 8 pt).
private struct TimelineScrubber: View {
    let timeline: ObsTimeline?
    let range: ClosedRange<Date>?
    let selected: Date?
    let onSelect: (Date) -> Void

    private let pinH: CGFloat = 10
    private let barH: CGFloat = 30

    private func height(_ s: ObsState) -> CGFloat {
        switch s {
        case .ok: 0.3
        case .degraded: 0.6
        case .failing: 1
        case .nodata: 0.12
        }
    }

    private func fraction(_ d: Date, _ r: ClosedRange<Date>) -> CGFloat {
        let span = r.upperBound.timeIntervalSince(r.lowerBound)
        guard span > 0 else { return 1 }
        return CGFloat(min(1, max(0, d.timeIntervalSince(r.lowerBound) / span)))
    }

    /// Round hours (every 6 h) inside the range, for the axis.
    private func ticks(_ r: ClosedRange<Date>) -> [Date] {
        let cal = Calendar.current
        guard var d = cal.nextDate(after: r.lowerBound, matching: DateComponents(minute: 0, second: 0),
                                   matchingPolicy: .nextTime) else { return [] }
        var out: [Date] = []
        while d <= r.upperBound {
            if cal.component(.hour, from: d) % 6 == 0 { out.append(d) }
            d = d.addingTimeInterval(3600)
        }
        return out
    }

    var body: some View {
        if let timeline, let range, !timeline.bins.isEmpty {
            GeometryReader { geo in
                let w = geo.size.width
                ZStack(alignment: .topLeading) {
                    Canvas { ctx, size in
                        let n = timeline.bins.count
                        let bw = size.width / CGFloat(n)
                        for (i, bin) in timeline.bins.enumerated() {
                            let h = barH * height(bin.state)
                            let rect = CGRect(x: CGFloat(i) * bw + 0.5, y: pinH + barH - h,
                                              width: max(1, bw - 1), height: h)
                            ctx.fill(Path(rect), with: .color(bin.state.color.opacity(bin.state == .ok ? 0.7 : 1)))
                        }
                        for pin in timeline.pins {
                            let x = fraction(Date(timeIntervalSince1970: pin.t), range) * size.width
                            var tri = Path()
                            tri.move(to: CGPoint(x: x, y: 0))
                            tri.addLine(to: CGPoint(x: x + 4.5, y: pinH - 1))
                            tri.addLine(to: CGPoint(x: x - 4.5, y: pinH - 1))
                            tri.closeSubpath()
                            ctx.fill(tri, with: .color(Theme.danger))
                        }
                        // Selected moment (or now, while Live).
                        let sx = selected.map { fraction($0, range) * size.width } ?? size.width - 1
                        ctx.fill(Path(CGRect(x: sx - 1, y: pinH - 2, width: 2, height: barH + 4)),
                                 with: .color(selected == nil ? Theme.textMuted : Theme.clay))
                    }
                    .frame(height: pinH + barH)

                    ForEach(ticks(range), id: \.self) { t in
                        Text(ObsFormat.hm(t))
                            .font(.claudeFootnote)
                            .foregroundStyle(Theme.textMuted)
                            .fixedSize()
                            .position(x: min(w - 18, max(18, fraction(t, range) * w)), y: pinH + barH + 10)
                    }
                }
                .contentShape(Rectangle())
                .gesture(
                    DragGesture(minimumDistance: 0)
                        .onChanged { v in onSelect(date(at: v.location.x, width: w, range: range)) }
                )
            }
            .frame(height: pinH + barH + 20)
            .accessibilityElement()
            .accessibilityLabel("Timeline, last 24 hours")
            .accessibilityValue(accessibilityValue(timeline, range))
            .accessibilityAdjustableAction { dir in
                let step = range.upperBound.timeIntervalSince(range.lowerBound) / Double(max(1, timeline.bins.count))
                let current = selected ?? range.upperBound
                let next = current.addingTimeInterval(dir == .increment ? step : -step)
                onSelect(min(range.upperBound, max(range.lowerBound, next)))
            }
        } else {
            Text(timeline == nil ? "Loading timeline…" : "No timeline")
                .font(.claudeCaption)
                .foregroundStyle(Theme.textMuted)
                .frame(maxWidth: .infinity, minHeight: pinH + barH + 20)
        }
    }

    private func date(at x: CGFloat, width: CGFloat, range: ClosedRange<Date>) -> Date {
        let span = range.upperBound.timeIntervalSince(range.lowerBound)
        let clampedX = min(width, max(0, x))
        // Snap to a nearby rule pin so its event shows.
        if let pins = timeline?.pins, width > 0 {
            let near = pins.map { (pin: $0, dx: abs(fraction(Date(timeIntervalSince1970: $0.t), range) * width - clampedX)) }
                .filter { $0.dx <= 8 }
                .min { $0.dx < $1.dx }
            if let near { return Date(timeIntervalSince1970: near.pin.t) }
        }
        return range.lowerBound.addingTimeInterval(span * Double(clampedX / max(1, width)))
    }

    private func accessibilityValue(_ timeline: ObsTimeline, _ range: ClosedRange<Date>) -> String {
        let at = selected ?? range.upperBound
        let i = min(timeline.bins.count - 1, Int(fraction(at, range) * CGFloat(timeline.bins.count)))
        let state = timeline.bins[max(0, i)].state.word
        return "\(selected == nil ? "Live" : "At \(ObsFormat.hm(at))"), worst state \(state), \(timeline.pins.count) rule breaks"
    }
}
