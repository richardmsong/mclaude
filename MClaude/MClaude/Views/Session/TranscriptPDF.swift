import SwiftUI
import UIKit

/// PDF renders of a transcript (docs/design-claude-uplift.md → Export): the conversation as it
/// looks on screen, drawn with ImageRenderer from ConversationView's export mode.
@MainActor
enum TranscriptPDF {
    /// Full Page PDF units per point. iOS flattens a shared / saved Full Page at about one pixel
    /// per PDF unit, so this sets the shared image's resolution. 1× (Richard, 2026-11-04: "1x is
    /// enough"; 3× was too heavy). Vector content stays sharp in the editor either way.
    static let fullPageScale: CGFloat = 1
    /// Tallest page, in PDF units (≈ pixels once iOS flattens it), kept under the 65,535px JPEG
    /// limit (about 70 screens at 1×). Only beyond that are the oldest events dropped.
    static let fullPageMaxUnits: CGFloat = 60_000
    /// Scale for anything ImageRenderer has to rasterize (shadows, images), matching the screen.
    static let rasterScale: CGFloat = 3

    /// Multi-page PDF of the whole conversation, pages about 1.5 screens tall.
    static func paged(events: [SessionEvent], session: ClaudeSession?, sessionId: String, width: CGFloat,
                      screenHeight: CGFloat, scheme: ColorScheme, appState: AppState) -> Data? {
        guard let renderer = renderer(events: events, session: session, sessionId: sessionId, width: width,
                                      scheme: scheme, omittedNote: false, appState: appState) else { return nil }
        let pageHeight = (screenHeight * 1.5).rounded()
        let data = NSMutableData()
        renderer.render { size, draw in
            guard let consumer = CGDataConsumer(data: data as CFMutableData) else { return }
            var defaultBox = CGRect(x: 0, y: 0, width: size.width, height: pageHeight)
            guard let pdf = CGContext(consumer: consumer, mediaBox: &defaultBox, nil) else { return }
            let pageCount = max(1, Int((size.height / pageHeight).rounded(.up)))
            for i in 0..<pageCount {
                let top = CGFloat(i) * pageHeight
                let h = min(pageHeight, size.height - top)
                beginPage(pdf, width: size.width, height: h)
                pdf.saveGState()
                // The renderer draws the whole view with its top at y = size.height (PDF origin is
                // bottom-left); shift so this page's slice lands in the page box.
                pdf.translateBy(x: 0, y: h - size.height + top)
                draw(pdf)
                pdf.restoreGState()
                pdf.endPDFPage()
            }
            pdf.closePDF()
        }
        return data.length > 0 ? data as Data : nil
    }

    /// Single-page PDF for the Full Page screenshot, and the scale it was drawn at: the whole
    /// conversation, unless it's taller than `fullPageMaxUnits` (≈70 screens), in which case the
    /// oldest events are dropped and the page starts with a note pointing to Export.
    static func fullPage(events: [SessionEvent], session: ClaudeSession?, sessionId: String, width: CGFloat,
                         scheme: ColorScheme, appState: AppState) async -> (data: Data, scale: CGFloat)? {
        // Drawn in pieces of whole turns, newest first, with the main thread free between pieces:
        // laying out a long conversation in one go (and re-measuring it to trim) froze the app for
        // a second or more (Ops F-23). Pieces split at user messages, so groups render the same.
        func piece<V: View>(_ v: V) -> ImageRenderer<AnyView> {
            let r = ImageRenderer(content: AnyView(v.frame(width: width).background(Theme.bgPage)
                .environment(\.colorScheme, scheme).environment(appState)))
            r.proposedSize = ProposedViewSize(width: width, height: nil)
            r.scale = rasterScale
            return r
        }
        func height(_ r: ImageRenderer<AnyView>) -> CGFloat {
            var h: CGFloat = 0
            r.render { size, _ in h = size.height }   // measure only; nothing is drawn
            return h
        }
        func breather() async { try? await Task.sleep(for: .milliseconds(2)) }

        let limit = fullPageMaxUnits / fullPageScale
        let composer = piece(ExportComposer())
        var used = height(composer) + 140   // + the header, measured below
        var pieces: [(renderer: ImageRenderer<AnyView>, height: CGFloat)] = []
        var omitted = false
        for chunk in turnChunks(events).reversed() {
            await breather()
            let r = piece(ConversationView(events: chunk, sessionId: sessionId, exportMode: true))
            let h = height(r)
            if used + h > limit, !pieces.isEmpty { omitted = true; break }
            pieces.insert((r, h), at: 0)
            used += h
        }
        let header = piece(VStack(alignment: .leading, spacing: 0) {
            ExportHeader(session: session, sessionId: sessionId)
            if omitted {
                Text("Earlier messages omitted. Use ••• → Export → PDF for the full conversation.")
                    .font(.claudeFootnote)
                    .foregroundStyle(Theme.textMuted)
                    .frame(maxWidth: .infinity)
                    .padding(.top, 16)
            }
        })
        let all = [(header, height(header))] + pieces + [(composer, height(composer))]
        let total = all.reduce(0) { $0 + $1.1 }

        let data = NSMutableData()
        let k = fullPageScale
        var box = CGRect(x: 0, y: 0, width: width * k, height: total * k)
        guard let consumer = CGDataConsumer(data: data as CFMutableData),
              let pdf = CGContext(consumer: consumer, mediaBox: &box, nil) else { return nil }
        pdf.beginPDFPage(nil)
        pdf.scaleBy(x: k, y: k)
        var top: CGFloat = 0
        for (r, _) in all {
            await breather()
            r.render { size, draw in
                pdf.saveGState()
                // The renderer draws its view with its top at y = size.height (PDF origin is
                // bottom-left); shift it down to this piece's place on the page.
                pdf.translateBy(x: 0, y: total - top - size.height)
                draw(pdf)
                pdf.restoreGState()
                top += size.height
            }
        }
        pdf.endPDFPage()
        pdf.closePDF()
        return data.length > 0 ? (data as Data, k) : nil
    }

    /// The events split into runs of whole turns (a turn starts at a user message), each about
    /// `target` events, so a piece never cuts a turn's groups apart.
    static func turnChunks(_ events: [SessionEvent], target: Int = 60) -> [[SessionEvent]] {
        var chunks: [[SessionEvent]] = []
        var current: [SessionEvent] = []
        for e in events {
            if e.type == .user, current.count >= target {
                chunks.append(current)
                current = []
            }
            current.append(e)
        }
        if !current.isEmpty { chunks.append(current) }
        return chunks
    }

    /// The transcript between static copies of the session header and the composer, so it reads
    /// like a screenshot. `appState`: rows like tool-result thumbnails read it from the environment.
    private static func renderer(events: [SessionEvent], session: ClaudeSession?, sessionId: String, width: CGFloat,
                                 scheme: ColorScheme, omittedNote: Bool, appState: AppState) -> ImageRenderer<some View>? {
        let content = VStack(alignment: .leading, spacing: 0) {
            ExportHeader(session: session, sessionId: sessionId)
            if omittedNote {
                Text("Earlier messages omitted. Use ••• → Export → PDF for the full conversation.")
                    .font(.claudeFootnote)
                    .foregroundStyle(Theme.textMuted)
                    .frame(maxWidth: .infinity)
                    .padding(.top, 16)
            }
            ConversationView(events: events, sessionId: sessionId, exportMode: true)
            ExportComposer()
        }
        .frame(width: width)
        .background(Theme.bgPage)
        .environment(\.colorScheme, scheme)
        .environment(appState)
        let renderer = ImageRenderer(content: content)
        renderer.proposedSize = ProposedViewSize(width: width, height: nil)
        renderer.scale = rasterScale
        return renderer
    }

    private static func beginPage(_ pdf: CGContext, width: CGFloat, height: CGFloat) {
        var box = CGRect(x: 0, y: 0, width: width, height: height)
        let boxData = NSData(bytes: &box, length: MemoryLayout<CGRect>.size)
        pdf.beginPDFPage([kCGPDFContextMediaBox as String: boxData] as CFDictionary)
    }
}

/// System "Full Page" screenshots (UIScreenshotService). While a session screen is visible it
/// supplies a single-page PDF of the transcript; otherwise none, so iOS offers only "Screen".
@MainActor
final class FullPageScreenshot: NSObject, UIScreenshotServiceDelegate {
    static let shared = FullPageScreenshot()

    /// Set by the visible session screen; returns the PDF to offer and the scale it was drawn at.
    var provider: (() async -> (data: Data, scale: CGFloat)?)?
    private(set) var ownerId: String?

    func activate(for sessionId: String, provider: @escaping () async -> (data: Data, scale: CGFloat)?) {
        ownerId = sessionId
        self.provider = provider
        for scene in UIApplication.shared.connectedScenes.compactMap({ $0 as? UIWindowScene }) {
            scene.screenshotService?.delegate = self
        }
    }

    func deactivate(for sessionId: String) {
        guard ownerId == sessionId else { return }
        ownerId = nil
        provider = nil
    }

    nonisolated func screenshotService(_ screenshotService: UIScreenshotService,
                                       generatePDFRepresentationWithCompletion completionHandler: @escaping (Data?, Int, CGRect) -> Void) {
        Task { @MainActor in
            guard let page = await self.provider?() else {
                completionHandler(nil, 0, .zero)
                return
            }
            // Open the editor at the bottom of the page (the latest messages). PDF coordinates
            // have a bottom-left origin.
            let screen = screenshotService.windowScene?.screen.bounds ?? .zero
            let k = page.scale
            completionHandler(page.data, 0, CGRect(x: 0, y: 0, width: screen.width * k, height: screen.height * k))
        }
    }
}

/// System share sheet for an exported file.
struct ShareSheet: UIViewControllerRepresentable {
    let url: URL

    func makeUIViewController(context: Context) -> UIActivityViewController {
        UIActivityViewController(activityItems: [url], applicationActivities: nil)
    }

    func updateUIViewController(_ vc: UIActivityViewController, context: Context) {}
}

/// A file ready to share (sheet item).
struct ExportedFile: Identifiable {
    let url: URL
    var id: String { url.path }
}

// MARK: - Static screen chrome for exports

/// The session header as drawn on screen (☰, status dot + name + context gauge, new-session and
/// ••• in a capsule). Static: exports can't render the real toolbar.
private struct ExportHeader: View {
    let session: ClaudeSession?
    let sessionId: String

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: "line.3.horizontal")
                .font(.system(size: 17, weight: .regular))
                .foregroundStyle(Theme.textSecondary)
                .frame(width: 44, height: 44)
                .background(Theme.bgRaised, in: Circle())
                .shadow(color: .black.opacity(0.06), radius: 8, y: 2)
            Spacer(minLength: 0)
            HStack(spacing: 6) {
                if let dot = session?.dot, dot != .unread {
                    SessionDotView(dot: dot, size: 7)
                }
                Text(session?.displayName ?? "Session \(sessionId)")
                    .font(.claudeUISemibold)
                    .foregroundStyle(Theme.textPrimary)
                    .lineLimit(1)
                if let fraction = session?.contextFraction {
                    ContextGauge(fraction: fraction, size: 13, showLabel: true)
                }
            }
            Spacer(minLength: 0)
            HStack(spacing: 24) {
                Image(systemName: "plus.bubble")
                Image(systemName: "ellipsis")
            }
            .font(.system(size: 17, weight: .regular))
            .foregroundStyle(Theme.textSecondary)
            .padding(.horizontal, 18)
            .frame(height: 44)
            .background(Theme.bgRaised, in: Capsule())
            .shadow(color: .black.opacity(0.06), radius: 8, y: 2)
        }
        .padding(.horizontal, 16)
        .padding(.top, 10)
        .padding(.bottom, 6)
    }
}

/// The composer as drawn on screen in its default (hold-to-talk) mode. Static copy of
/// InputBarView's voice mode.
private struct ExportComposer: View {
    var body: some View {
        VStack(spacing: 12) {
            HStack {
                ForEach(["camera", "doc", "command", "keyboard"], id: \.self) { name in
                    Image(systemName: name)
                        .font(.system(size: 19, weight: .regular))
                        .foregroundStyle(Theme.textSecondary)
                        .frame(width: 30, height: 30)
                }
                Image(systemName: "stop.circle")
                    .font(.system(size: 20, weight: .regular))
                    .foregroundStyle(Theme.danger)
                Text("Hold to talk")
                    .font(.claudeBody)
                    .foregroundStyle(Theme.textMuted)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(.horizontal, 12)
            .padding(.top, 10)
            Circle()
                .fill(Theme.bgSunken)
                .frame(width: 72, height: 72)
                .overlay {
                    Image(systemName: "mic")
                        .font(.system(size: 26, weight: .regular))
                        .foregroundStyle(Theme.textSecondary)
                }
                .padding(.bottom, 14)
        }
        .claudeCard(radius: Theme.radiusComposer)
        .padding(.horizontal, 10)
        .padding(.top, 6)
        .padding(.bottom, 10)
    }
}
