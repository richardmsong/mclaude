import SwiftUI
import UIKit
import UIKit.UIGestureRecognizerSubclass

/// Range-selectable text (docs/ui-spec.md → Conversation Events → Text Selection).
///
/// SwiftUI `Text.textSelection(.enabled)` only offers whole-block copy on iOS, so transcript text
/// renders through a non-editable, non-scrolling `UITextView` that sizes to its content.
/// Long-press / double-tap selects with handles; the system edit menu copies.
///
/// Taps are handled here rather than by an ancestor `.onTapGesture` (which would also fire on
/// selection-dismissing taps): a tap with no active selection runs `onTap`; a tap while a selection
/// is active only clears it; a tap on a link opens the link.
struct SelectableText: View {
    let text: NSAttributedString
    /// Size to the text's own width (chat bubbles) instead of filling the proposed width.
    var hugsContent: Bool = false
    var onTap: (() -> Void)? = nil

    @Environment(\.staticTextRendering) private var staticRendering

    var body: some View {
        if staticRendering {
            // PDF export / Full Page screenshot: ImageRenderer can't draw UIKit views
            Text(RichText.swiftUI(text))
                .lineSpacing(RichText.lineSpacing(of: text))
                .fixedSize(horizontal: false, vertical: true)
        } else {
            SelectableTextRepresentable(text: text, hugsContent: hugsContent, onTap: onTap)
        }
    }
}

extension EnvironmentValues {
    /// Render `SelectableText` as plain SwiftUI `Text` (for `ImageRenderer` exports).
    @Entry var staticTextRendering = false
}

private struct SelectableTextRepresentable: UIViewRepresentable {
    let text: NSAttributedString
    var hugsContent: Bool
    var onTap: (() -> Void)?

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeUIView(context: Context) -> SelectableTextView {
        let tv = SelectableTextView()
        tv.isEditable = false
        tv.isSelectable = true
        tv.isScrollEnabled = false
        tv.backgroundColor = .clear
        tv.textContainerInset = .zero
        tv.textContainer.lineFragmentPadding = 0
        tv.dataDetectorTypes = []
        tv.linkTextAttributes = [.foregroundColor: UIColor(Theme.link)]
        tv.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        tv.setContentHuggingPriority(.defaultHigh, for: .vertical)

        tv.delegate = context.coordinator
        let tap = QuickTapGestureRecognizer(target: context.coordinator, action: #selector(Coordinator.tapped(_:)))
        tap.delegate = context.coordinator
        tv.addGestureRecognizer(tap)
        tv.rowTap = tap
        context.coordinator.textView = tv
        return tv
    }

    func updateUIView(_ tv: SelectableTextView, context: Context) {
        context.coordinator.onTap = onTap
        if !tv.attributedText.isEqual(to: text) {
            tv.attributedText = text
        }
    }

    func sizeThatFits(_ proposal: ProposedViewSize, uiView: SelectableTextView, context: Context) -> CGSize? {
        let maxWidth = proposal.width.flatMap { $0.isFinite ? $0 : nil } ?? .greatestFiniteMagnitude
        // Tight width of the laid-out text (≤ maxWidth). With no width proposed (e.g. inside a
        // horizontal ScrollView) this is the longest line, so code blocks don't wrap.
        let bounds = text.boundingRect(with: CGSize(width: maxWidth, height: .greatestFiniteMagnitude),
                                       options: [.usesLineFragmentOrigin, .usesFontLeading], context: nil)
        let tight = min(maxWidth, ceil(bounds.width) + 1)
        let width = (hugsContent || proposal.width == nil) ? tight : maxWidth
        let height = uiView.sizeThatFits(CGSize(width: width, height: .greatestFiniteMagnitude)).height
        return CGSize(width: width, height: ceil(height))
    }

    final class Coordinator: NSObject, UIGestureRecognizerDelegate, UITextViewDelegate {
        weak var textView: SelectableTextView?
        var onTap: (() -> Void)?
        /// The system tap clears a selection before ours fires, so it's sampled at touch-down.
        private var hadSelectionAtTouchDown = false

        @objc func tapped(_ gr: UITapGestureRecognizer) {
            guard let tv = textView, gr.state == .ended else { return }
            // Taps that dismiss a selection, long-presses (which select), and link taps don't
            // open the row's detail.
            if hadSelectionAtTouchDown || tv.selectedRange.length > 0 { return }
            if tv.hasLink(at: gr.location(in: tv)) { return }
            onTap?()
        }

        func gestureRecognizer(_ gr: UIGestureRecognizer,
                               shouldRecognizeSimultaneouslyWith other: UIGestureRecognizer) -> Bool { true }

        func gestureRecognizerShouldBegin(_ gr: UIGestureRecognizer) -> Bool { onTap != nil }

        func textViewDidChangeSelection(_ tv: UITextView) {
            TextSelectionState.shared.update(tv, selected: tv.selectedRange.length > 0)
        }

        func gestureRecognizer(_ gr: UIGestureRecognizer, shouldReceive touch: UITouch) -> Bool {
            hadSelectionAtTouchDown = (textView?.selectedRange.length ?? 0) > 0
            return true
        }
    }
}

/// A single tap that fails if the finger is held (a long-press is a selection gesture, not a tap).
/// Duration is measured touch-down → touch-up, so waiting on the double-tap to fail doesn't count.
final class QuickTapGestureRecognizer: UITapGestureRecognizer {
    static let maxDuration: TimeInterval = 0.35
    private var downAt: TimeInterval = 0

    override func touchesBegan(_ touches: Set<UITouch>, with event: UIEvent) {
        downAt = event.timestamp
        super.touchesBegan(touches, with: event)
    }

    override func touchesEnded(_ touches: Set<UITouch>, with event: UIEvent) {
        if event.timestamp - downAt > Self.maxDuration {
            state = .failed
            return
        }
        super.touchesEnded(touches, with: event)
    }
}

final class SelectableTextView: UITextView {
    weak var rowTap: UITapGestureRecognizer?

    override func didMoveToWindow() {
        super.didMoveToWindow()
        // A double-tap selects a word; don't also treat its first tap as a row tap.
        guard let rowTap else { return }
        for gr in gestureRecognizers ?? [] where gr !== rowTap {
            if let tap = gr as? UITapGestureRecognizer, tap.numberOfTapsRequired == 2 {
                rowTap.require(toFail: tap)
            }
        }
    }

    func hasLink(at point: CGPoint) -> Bool {
        guard attributedText.length > 0, let pos = closestPosition(to: point) else { return false }
        let offset = min(self.offset(from: beginningOfDocument, to: pos), attributedText.length - 1)
        return attributedText.attribute(.link, at: max(0, offset), effectiveRange: nil) != nil
    }
}

// MARK: - Attributed-string builders (match the SwiftUI `Text` styling they replace)

enum RichText {
    static func serif(_ size: CGFloat, weight: UIFont.Weight = .regular, italic: Bool = false) -> UIFont {
        var desc = UIFont.systemFont(ofSize: size, weight: weight).fontDescriptor
        desc = desc.withDesign(.serif) ?? desc
        if italic { desc = desc.withSymbolicTraits(desc.symbolicTraits.union(.traitItalic)) ?? desc }
        return UIFont(descriptor: desc, size: size)
    }

    static func mono(_ size: CGFloat, weight: UIFont.Weight = .regular) -> UIFont {
        .monospacedSystemFont(ofSize: size, weight: weight)
    }

    static func paragraph(lineSpacing: CGFloat) -> NSParagraphStyle {
        let p = NSMutableParagraphStyle()
        p.lineSpacing = lineSpacing
        return p
    }

    /// UIKit-attributed string → SwiftUI-scoped `AttributedString` (fonts, colours, links,
    /// strikethrough), for `Text`.
    static func swiftUI(_ ns: NSAttributedString) -> AttributedString {
        var out = AttributedString()
        ns.enumerateAttributes(in: NSRange(location: 0, length: ns.length)) { attrs, range, _ in
            var run = AttributedString(ns.attributedSubstring(from: range).string)
            if let f = attrs[.font] as? UIFont { run.font = Font(f as CTFont) }
            if let c = attrs[.foregroundColor] as? UIColor { run.foregroundColor = Color(uiColor: c) }
            if let c = attrs[.backgroundColor] as? UIColor { run.backgroundColor = Color(uiColor: c) }
            if let url = attrs[.link] as? URL {
                run.link = url
                run.foregroundColor = Theme.link
            }
            if attrs[.strikethroughStyle] != nil { run.strikethroughStyle = .single }
            out.append(run)
        }
        return out
    }

    static func lineSpacing(of ns: NSAttributedString) -> CGFloat {
        guard ns.length > 0 else { return 0 }
        return (ns.attribute(.paragraphStyle, at: 0, effectiveRange: nil) as? NSParagraphStyle)?.lineSpacing ?? 0
    }

    /// Plain text in one style.
    static func plain(_ text: String, font: UIFont, color: Color, lineSpacing: CGFloat = 0) -> NSAttributedString {
        NSAttributedString(string: text, attributes: [
            .font: font,
            .foregroundColor: UIColor(color),
            .paragraphStyle: paragraph(lineSpacing: lineSpacing),
        ])
    }

    /// Claude prose: serif 17, ~1.45 line height, inline markdown (bold/italic/code/links).
    static func prose(_ text: String) -> NSAttributedString {
        markdown(text, font: serif(17), codeFont: mono(15), color: Theme.textPrimary, lineSpacing: 4.5)
    }

    /// Inline markdown (via `ConversationView.inlineMarkdown`) converted to UIKit attributes.
    /// Falls back to plain text if the markdown doesn't parse.
    static func markdown(_ text: String, font: UIFont, codeFont: UIFont, color: Color,
                         lineSpacing: CGFloat = 0) -> NSAttributedString {
        guard let md = ConversationView.inlineMarkdown(text, codeSize: codeFont.pointSize) else {
            return plain(text, font: font, color: color, lineSpacing: lineSpacing)
        }
        return convert(md, font: font, codeFont: codeFont, color: color, lineSpacing: lineSpacing)
    }

    /// Convert a SwiftUI-styled `AttributedString` (foreground/background colours, inline
    /// presentation intents, links) into an `NSAttributedString` a `UITextView` can render.
    static func convert(_ attr: AttributedString, font: UIFont, codeFont: UIFont, color: Color,
                        lineSpacing: CGFloat = 0) -> NSAttributedString {
        let out = NSMutableAttributedString()
        let para = paragraph(lineSpacing: lineSpacing)
        for run in attr.runs {
            let intent = run.inlinePresentationIntent ?? []
            var runFont = intent.contains(.code) ? codeFont : font
            var traits = runFont.fontDescriptor.symbolicTraits
            if intent.contains(.stronglyEmphasized) { traits.insert(.traitBold) }
            if intent.contains(.emphasized) { traits.insert(.traitItalic) }
            if traits != runFont.fontDescriptor.symbolicTraits,
               let desc = runFont.fontDescriptor.withSymbolicTraits(traits) {
                runFont = UIFont(descriptor: desc, size: runFont.pointSize)
            }
            var attrs: [NSAttributedString.Key: Any] = [.font: runFont, .paragraphStyle: para]
            let fg = run[AttributeScopes.SwiftUIAttributes.ForegroundColorAttribute.self] ?? color
            attrs[.foregroundColor] = UIColor(fg)
            if let bg = run[AttributeScopes.SwiftUIAttributes.BackgroundColorAttribute.self] {
                attrs[.backgroundColor] = UIColor(bg)
            }
            if intent.contains(.strikethrough) { attrs[.strikethroughStyle] = NSUnderlineStyle.single.rawValue }
            if let link = run.link { attrs[.link] = link }
            out.append(NSAttributedString(string: String(attr[run.range].characters), attributes: attrs))
        }
        return out
    }
}

/// Transcript text views that have a selection right now. A rightward swipe that starts on one
/// is a selection-handle drag, not "open the drawer".
@MainActor
final class TextSelectionState {
    static let shared = TextSelectionState()
    private var views = NSHashTable<UITextView>.weakObjects()

    func update(_ view: UITextView, selected isSelected: Bool) {
        if isSelected { views.add(view) } else { views.remove(view) }
    }

    /// Whether `point` (window coordinates) is on a text view with a selection (with some slack
    /// for the handles, which hang outside the text).
    func selection(contains point: CGPoint) -> Bool {
        views.allObjects.contains { v in
            v.window != nil && v.convert(v.bounds, to: nil).insetBy(dx: -24, dy: -24).contains(point)
        }
    }

    /// Drop every selection (the drawer is opening).
    func clearAll() {
        for v in views.allObjects { v.selectedRange = NSRange(location: 0, length: 0); v.resignFirstResponder() }
        views.removeAllObjects()
    }
}
