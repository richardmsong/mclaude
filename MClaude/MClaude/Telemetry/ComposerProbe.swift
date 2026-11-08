import UIKit

/// Composer probe (docs/plan-observability.md → Composer probe).
///
/// Each send opens a 1 s observation window, its own trace. It records the UIKit text view's real
/// state at the send, every write to either copy of the text (the app's model and the text view),
/// and checks at 0.1, 0.5 and 1.0 s that the two agree. On a mismatch it reports a
/// `composer_desync` rule break with the whole timeline and a screenshot. It only observes: it
/// never changes the text field.
@MainActor
final class ComposerProbe {
    static let shared = ComposerProbe()

    private final class Window {
        let span: Telemetry.Span
        let message: String
        let start = CACurrentMediaTime()
        weak var textView: UITextView?
        var timeline: [[String: Any]] = []
        var modelText: String
        var lastViewText: String?
        var lastViewMarked = false
        var markedAtSend = false
        var sendSnapshot: [String: Any] = [:]
        var intended: [(source: String, value: String)] = []
        var mismatchAt: [Double] = []
        var screenshotRequested = false
        var observer: NSObjectProtocol?
        var displayLink: CADisplayLink?

        init(span: Telemetry.Span, message: String, modelText: String) {
            self.span = span
            self.message = message
            self.modelText = modelText
        }
        var elapsedMs: Int { Int((CACurrentMediaTime() - start) * 1000) }
    }

    private var window: Window?
    private var lastDesyncReport = Date.distantPast

    /// Share of in-sync sends whose records are exported. Broken sends are always exported in full.
    static let inSyncSampleRate = 0.05

    // MARK: - Hooks called by the composer

    /// Right before a send clears the field. `modelText` is the model's text at that moment.
    func willSend(message: String, modelText: String, hasPhotos: Bool) {
        if window != nil { finish() }
        let span = Telemetry.shared.startSpan("composer.send", attributes: [
            "mclaude.composer.message_length": message.count,
            "mclaude.composer.has_photos": hasPhotos,
        ])
        let w = Window(span: span, message: message, modelText: modelText)
        window = w

        let tv = Self.findComposerTextView(expecting: modelText)
        w.textView = tv
        var snapshot: [String: Any] = [
            "t": 0, "kind": "send",
            "model_text": modelText, "model_length": modelText.count,
            "view_found": tv != nil,
        ]
        if let tv {
            let text = tv.text ?? ""
            w.lastViewText = text
            w.lastViewMarked = tv.markedTextRange != nil
            w.markedAtSend = w.lastViewMarked
            snapshot["view_text"] = text
            snapshot["view_length"] = text.count
            snapshot["view_equals_model"] = text == modelText
            snapshot["marked"] = tv.markedTextRange != nil
            if let marked = tv.markedTextRange { snapshot["marked_text"] = tv.text(in: marked) ?? "" }
            let sel = tv.selectedRange
            snapshot["selection"] = "\(sel.location),\(sel.length)"
            snapshot["keyboard"] = tv.textInputMode?.primaryLanguage ?? "?"
            snapshot["first_responder"] = tv.isFirstResponder
            w.observer = NotificationCenter.default.addObserver(
                forName: UITextView.textDidChangeNotification, object: tv, queue: .main
            ) { [weak self] _ in
                MainActor.assumeIsolated { self?.keyboardWrite() }
            }
            let link = CADisplayLink(target: DisplayLinkTarget { [weak self] in self?.sampleView() },
                                     selector: #selector(DisplayLinkTarget.tick))
            link.preferredFrameRateRange = CAFrameRateRange(minimum: 60, maximum: 120, preferred: 120)
            link.add(to: .main, forMode: .common)
            w.displayLink = link
        }
        w.timeline.append(snapshot)
        w.sendSnapshot = snapshot

        for delay in [0.1, 0.5, 1.0] {
            DispatchQueue.main.asyncAfter(deadline: .now() + delay) { [weak self, weak w] in
                guard let self, let w, self.window === w else { return }
                self.check(at: delay)
                if delay == 1.0 { self.finish() }
            }
        }
    }

    /// The composer is about to set the model's text to `value` for `source`
    /// (`send_clear`, `reclear`, `restore_after_failure`, …).
    func willWrite(source: String, value: String) {
        window?.intended.append((source, value))
    }

    /// The composer's re-clear ran; `executed` is whether it actually cleared.
    func reclear(executed: Bool, modelText: String) {
        guard let w = window else { return }
        sampleView()
        let entry: [String: Any] = ["t": w.elapsedMs, "kind": "reclear", "executed": executed,
                                    "model_text": modelText, "model_equals_sent": modelText == w.message]
        w.timeline.append(entry)
    }

    /// The model's text changed (from the composer's onChange).
    func modelChanged(from old: String, to new: String) {
        guard let w = window else { return }
        sampleView()
        // Not one of the composer's own writes: typing propagated by SwiftUI, or the view model
        // clearing the draft. The view-side entries around it tell which.
        var source = "other"
        if let i = w.intended.firstIndex(where: { $0.value == new }) {
            source = w.intended[i].source
            w.intended.remove(at: i)
        }
        w.modelText = new
        w.timeline.append(["t": w.elapsedMs, "kind": "model_write", "source": source,
                           "from": old, "to": new, "to_length": new.count, "equals_sent": new == w.message])
    }

    // MARK: - Observation

    private func keyboardWrite() {
        guard let w = window, let tv = w.textView else { return }
        let text = tv.text ?? ""
        w.lastViewText = text
        w.lastViewMarked = tv.markedTextRange != nil
        w.timeline.append(["t": w.elapsedMs, "kind": "view_write", "source": "keyboard",
                           "to": text, "to_length": text.count, "equals_sent": text == w.message,
                           "marked": tv.markedTextRange != nil])
    }

    /// Every frame during the window, and before every other recorded entry: a view text change
    /// without a keyboard notification was set programmatically (SwiftUI pushing the model into
    /// UIKit). Its time is when it was first seen, at most one frame late.
    private func sampleView() {
        guard let w = window, let tv = w.textView else { return }
        let text = tv.text ?? ""
        let marked = tv.markedTextRange != nil
        if text != w.lastViewText {
            w.timeline.append(["t": w.elapsedMs, "kind": "view_write", "source": "programmatic",
                               "to": text, "to_length": text.count, "equals_sent": text == w.message,
                               "marked": marked])
            w.lastViewText = text
        }
        if marked != w.lastViewMarked {
            w.timeline.append(["t": w.elapsedMs, "kind": "marked_change", "marked": marked])
            w.lastViewMarked = marked
        }
    }

    private func check(at delay: Double) {
        guard let w = window else { return }
        sampleView()
        let viewText = w.textView?.text
        let inSync = viewText == nil || viewText == w.modelText
        w.timeline.append(["t": w.elapsedMs, "kind": "check", "at": delay, "in_sync": inSync,
                           "model_text": w.modelText, "view_text": viewText ?? "<no view>",
                           "marked": w.textView?.markedTextRange != nil])
        guard !inSync else { return }
        w.mismatchAt.append(delay)
        if !w.screenshotRequested, Date().timeIntervalSince(lastDesyncReport) > 60 {
            w.screenshotRequested = true
            let ctx = w.span.context
            TelemetryScreenshot.capture { path in
                Telemetry.shared.log("mclaude.rule.screenshot", body: path,
                                     attributes: ["rule": "composer_desync", "path": path ?? "",
                                                  "uploaded": path != nil],
                                     in: ctx)
            }
        }
    }

    /// Tail sampling at the source: the outcome is known now, so decide what to export.
    /// Broken: everything. In sync: a 5% sample, which is also the comparison set. Every send
    /// counts toward the `mclaude.composer.sends` counter either way.
    private func finish() {
        guard let w = window else { return }
        window = nil
        w.displayLink?.invalidate()
        if let o = w.observer { NotificationCenter.default.removeObserver(o) }
        let desync = !w.mismatchAt.isEmpty
        let outcome = desync ? "desync" : "in_sync"
        Telemetry.shared.count("mclaude.composer.sends", attributes: ["outcome": outcome])

        if !desync, Double.random(in: 0..<1) >= Self.inSyncSampleRate { return }   // not exported

        let rate = desync ? 1.0 : Self.inSyncSampleRate
        let ctx = w.span.context
        Telemetry.shared.log("mclaude.composer.send", body: Telemetry.json(w.sendSnapshot),
                             attributes: ["mclaude.composer.view_found": w.sendSnapshot["view_found"] as? Bool ?? false,
                                          "mclaude.composer.marked": w.markedAtSend,
                                          "mclaude.sampling.rate": rate],
                             in: ctx)
        if desync, Date().timeIntervalSince(lastDesyncReport) > 60 {
            lastDesyncReport = Date()
            Telemetry.shared.log("mclaude.rule.broken", body: Telemetry.json(w.timeline),
                                 attributes: ["rule": "composer_desync",
                                              "mclaude.composer.mismatch_at": w.mismatchAt.map { String($0) }.joined(separator: ","),
                                              "mclaude.composer.marked_at_send": w.markedAtSend,
                                              "mclaude.sampling.rate": 1.0],
                                 in: ctx, severity: 13)
        }
        Telemetry.shared.log("mclaude.composer.send.settled", body: Telemetry.json(w.timeline),
                             attributes: ["outcome": outcome, "mclaude.composer.entries": w.timeline.count,
                                          "mclaude.sampling.rate": rate],
                             in: ctx)
        w.span.end(attributes: ["mclaude.composer.outcome": outcome, "mclaude.sampling.rate": rate])
    }

    // MARK: - Finding the text view

    private static func findComposerTextView(expecting text: String) -> UITextView? {
        if let tv = FirstResponder.current() as? UITextView { return tv }
        guard let window = UIApplication.shared.connectedScenes
            .compactMap({ ($0 as? UIWindowScene)?.keyWindow }).first else { return nil }
        var stack: [UIView] = [window]
        while let v = stack.popLast() {
            if let tv = v as? UITextView, tv.isEditable, tv.text == text { return tv }
            stack.append(contentsOf: v.subviews)
        }
        return nil
    }
}

/// CADisplayLink needs an NSObject target.
private final class DisplayLinkTarget: NSObject {
    let action: () -> Void
    init(_ action: @escaping () -> Void) { self.action = action }
    @objc func tick() { action() }
}

/// The current first responder, via the nil-targeted action trick.
private enum FirstResponder {
    nonisolated(unsafe) static weak var found: UIResponder?
    @MainActor static func current() -> UIResponder? {
        found = nil
        UIApplication.shared.sendAction(#selector(UIResponder.mclaudeCaptureFirstResponder(_:)),
                                        to: nil, from: nil, for: nil)
        return found
    }
}

extension UIResponder {
    @objc fileprivate func mclaudeCaptureFirstResponder(_ sender: Any?) {
        FirstResponder.found = self
    }
}
