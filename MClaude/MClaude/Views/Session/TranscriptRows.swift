import SwiftUI
import UIKit

/// Top of the transcript (docs/design-claude-uplift.md → History). While older history remains, a
/// spinner row that loads the next page when it scrolls into view; once everything is loaded, a
/// muted "Beginning of conversation" marker.
/// Shared between ConversationView (writes, from scroll callbacks) and HistoryTopRow (reads).
@Observable
final class HistoryLoadGate {
    /// The user has scrolled this conversation themselves. Until then the loader stays idle, so
    /// the first layout (empty list, estimated heights) can't trigger loads and re-anchoring.
    var userHasScrolled = false
    /// Bumped when a user scroll ends; the row re-checks whether it's on screen.
    var scrollEnded = 0
}

/// Progressive reveal (docs/design-claude-uplift.md → History): the transcript renders only its
/// newest rows at first and reveals older ones in chunks as the reader nears the top. A plain
/// class (not observed): it's written from scroll callbacks and never re-renders anything.
@MainActor
final class RevealState {
    /// The reader is within about two screens of the top of the rendered rows (iOS 18+).
    var nearTop = false
    /// The reveal in progress; later callers wait for it instead of starting another.
    var inFlight: Task<Bool, Never>?
    /// An older page fetched ahead of need, while rows were still hidden in memory.
    var prefetch: Task<Bool, Never>?
    /// The window has been set for this screen (the "open" record went out).
    var opened = false
    /// Telemetry for the last reveal, completed with row counts once its rows have rendered.
    var pendingLog: [String: Any]?
    /// The reader has revealed older rows (from then on, memory is kept ahead of them).
    var revealed = false
    /// As of the latest render: the timeline rows (the view's own row type) and whether history
    /// is complete. Reveals run in tasks that outlive the view value they started from.
    var latestRows: Any?
    var historyComplete = false
    /// The latest vertical scroll geometry (iOS 18+), for the reveal record.
    var geometry: ScrollSample?
    /// The transcript's scroll view, and until when content growth (the rows a reveal put in above
    /// the reader) is matched by moving the offset. The scroll view itself keeps the offset from
    /// the top, which would show the new rows instead of the reader's.
    weak var scrollView: UIScrollView?
    var keepPlaceUntil: Date?
    /// Telemetry: how far the offset was moved to keep the reader's place, since the last reveal.
    var keptSinceReveal: CGFloat = 0

    struct ScrollSample: Equatable {
        var offsetY: CGFloat
        var contentHeight: CGFloat
        var near: Bool
    }
}

extension RevealState {
    /// The scroll view's content size changed (KVO, inside the layout pass that changed it). Right
    /// after a reveal, rows went in above the reader; the scroll view keeps its offset from the
    /// top, which would show the new rows instead (a jump back by their height). So the offset
    /// moves down by as much as the content grew, in the same pass, before the frame is drawn.
    func contentSizeChanged(_ sv: UIScrollView, from old: CGSize, to new: CGSize) {
        guard let until = keepPlaceUntil else { return }
        guard Date() < until else { keepPlaceUntil = nil; return }
        let grown = new.height - old.height
        guard old.height > 0, abs(grown) > 0.5 else { return }
        keptSinceReveal += grown
        sv.contentOffset.y += grown
    }
}

/// Finds the transcript's UIScrollView (its nearest scroll view ancestor) for `RevealState`.
struct ScrollViewLocator: UIViewRepresentable {
    let state: RevealState

    func makeUIView(context: Context) -> LocatorView {
        let v = LocatorView()
        v.state = state
        v.isUserInteractionEnabled = false
        return v
    }

    func updateUIView(_ uiView: LocatorView, context: Context) {}

    final class LocatorView: UIView {
        weak var state: RevealState?
        private var observation: NSKeyValueObservation?
        override func didMoveToWindow() {
            super.didMoveToWindow()
            var v = superview
            while let view = v, !(view is UIScrollView) { v = view.superview }
            guard let sv = v as? UIScrollView, sv !== state?.scrollView else { return }
            state?.scrollView = sv
            observation = sv.observe(\.contentSize, options: [.old, .new]) { [weak self] sv, change in
                guard let old = change.oldValue, let new = change.newValue else { return }
                MainActor.assumeIsolated { self?.state?.contentSizeChanged(sv, from: old, to: new) }
            }
        }
    }
}

/// iOS 18+: keeps the vertical scroll geometry in `state`, and reports whether the reader is
/// within two screen heights of the top of the content when that changes. iOS 17: no-op (older
/// rows are revealed when the top row comes on screen).
struct NearTopTracking: ViewModifier {
    let state: RevealState
    let onChange: (Bool) -> Void

    func body(content: Content) -> some View {
        if #available(iOS 18.0, *) {
            content.onScrollGeometryChange(for: RevealState.ScrollSample.self) { g in
                RevealState.ScrollSample(offsetY: g.contentOffset.y, contentHeight: g.contentSize.height,
                                         near: g.contentOffset.y + g.contentInsets.top < g.containerSize.height * 2)
            } action: { old, new in
                state.geometry = new
                state.nearTop = new.near
                if old.near != new.near { onChange(new.near) }
            }
        } else {
            content
        }
    }
}

/// The progressive reveal's triggers, in one modifier to keep the transcript's chain short.
struct RevealTriggers: ViewModifier {
    let eventCount: Int
    let firstEventId: String?
    let tick: Int
    /// Set the window when rows arrive (or its first events are gone).
    let settle: () -> Void
    let state: RevealState
    let nearTopChanged: (Bool) -> Void
    /// Runs after each reveal (`tick` changed).
    let revealed: () async -> Void

    func body(content: Content) -> some View {
        content
            .onChange(of: eventCount, initial: true) { settle() }
            .onChange(of: firstEventId) { settle() }
            .modifier(NearTopTracking(state: state, onChange: nearTopChanged))
            .task(id: tick) { await revealed() }
    }
}

struct HistoryTopRow: View {
    let complete: Bool
    let hasEvents: Bool
    let gate: HistoryLoadGate
    /// Loads the next older page; returns whether anything was added.
    let load: () async -> Bool

    @State private var loading = false
    @State private var failed = false
    @State private var visible = false

    var body: some View {
        Group {
            if complete {
                if hasEvents {
                    Text("Beginning of conversation")
                        .font(.claudeFootnote)
                        .foregroundStyle(Theme.textMuted)
                }
            } else if failed {
                Button("Couldn't load earlier messages. Tap to retry") { Task { await run() } }
                    .font(.claudeFootnote)
                    .foregroundStyle(Theme.textSecondary)
            } else {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("Loading earlier messages…")
                        .font(.claudeFootnote)
                        .foregroundStyle(Theme.textMuted)
                }
            }
        }
        .onAppear {
            visible = true
            Task { await run() }
        }
        .onDisappear { visible = false }
        .onChange(of: gate.scrollEnded) {
            // A user scroll ended with this row on screen (onAppear may have fired long ago)
            if visible { Task { await run() } }
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 10)
    }

    private func run() async {
        guard !loading, hasEvents, gate.userHasScrolled || !ConversationView.tracksScrollPhase else { return }
        loading = true
        failed = false
        let added = await load()
        loading = false
        // Nothing added: the request failed or found only duplicates. Show a retry instead of
        // spinning (if history turned out complete, the "Beginning" branch wins anyway).
        failed = !added
        // Still on screen (the reader is at the very top, or the page was short): keep going.
        // onAppear won't fire again while it stays visible.
        if added, visible {
            try? await Task.sleep(for: .milliseconds(300))
            if visible { await run() }
        }
    }
}

/// Small pulsing clay dot (the Working row, the live work group's row). PhaseAnimator animates
/// only the opacity; a repeatForever animation started in onAppear also animates the dot's
/// *position*, so it drifted back toward where the row first appeared as content grew.
struct PulsingDot: View {
    var size: CGFloat = 7
    var body: some View {
        PhaseAnimator([1.0, 0.35]) { opacity in
            Circle()
                .fill(Theme.clay)
                .frame(width: size, height: size)
                .opacity(opacity)
        } animation: { _ in
            .easeInOut(duration: 0.8)
        }
    }
}
