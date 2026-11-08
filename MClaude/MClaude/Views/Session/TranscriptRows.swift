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
