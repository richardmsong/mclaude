import SwiftUI
import UIKit
import AVKit

/// Thumbnails for images in a tool result (e.g. Claude Read a screenshot). The server saves them
/// under /tmp/mclaude-screenshots and returns paths; we fetch over the authenticated API.
/// Tap a thumbnail to view it full screen.
struct ToolResultThumbnails: View {
    let paths: [String]
    var size: CGFloat = 64
    @State private var viewing: ViewedImage?

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                ForEach(paths, id: \.self) { path in      // paths are stable content hashes
                    Button {
                        viewing = ViewedImage(path: path)
                    } label: {
                        RemoteImageView(path: path, contentMode: .fill)
                            .frame(width: size, height: size)
                            .clipShape(RoundedRectangle(cornerRadius: Theme.radiusSmall, style: .continuous))
                            .claudeHairline(radius: Theme.radiusSmall)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Tool result image")
                }
            }
        }
        .fullScreenCover(item: $viewing) { item in
            ImageViewer(path: item.path)
        }
    }
}

/// One attachment in a user message, exactly as it appears in the text: a photo path, or a video
/// path plus the server's " [video: 12.4s, N frames in …]" note (docs/design-claude-uplift.md →
/// Video attachments).
struct UserAttachment: Hashable {
    let fragment: String
    let path: String
    let duration: Double?

    init(fragment: String) {
        self.fragment = fragment
        path = String(fragment.prefix { $0 != " " })
        let ns = fragment as NSString
        duration = Self.durationPattern.firstMatch(in: fragment, range: NSRange(location: 0, length: ns.length))
            .flatMap { Double(ns.substring(with: $0.range(at: 1))) }
    }

    var isVideo: Bool { ["mov", "mp4"].contains((path as NSString).pathExtension.lowercased()) }
    /// What to show as the tile: the photo itself, or the poster the server saved next to the video.
    var thumbnailPath: String { isVideo ? (path as NSString).deletingPathExtension + "-poster.jpg" : path }

    /// Photo paths and video paths (with their optional note), as the app sends them.
    static let pattern = try! NSRegularExpression(
        pattern: #"/tmp/mclaude-screenshots/(?:screenshot-\d+\.(?:png|jpe?g)|video-\d+\.(?:mov|mp4)(?: \[video:[^\]]*\])?)"#
    )
    private static let durationPattern = try! NSRegularExpression(pattern: #"\[video: ([0-9]+(?:\.[0-9]+)?)s"#)

    /// All attachment fragments (in order) and the remaining message text.
    static func parse(_ text: String) -> (fragments: [String], message: String) {
        let range = NSRange(text.startIndex..., in: text)
        let fragments = pattern.matches(in: text, range: range)
            .compactMap { Range($0.range, in: text).map { String(text[$0]) } }
        guard !fragments.isEmpty else { return ([], text) }
        let remaining = pattern.stringByReplacingMatches(in: text, range: range, withTemplate: "")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return (fragments, remaining)
    }

    /// "1 photo", "2 videos", "1 photo, 1 video".
    static func countLabel(photos: Int, videos: Int) -> String {
        var parts: [String] = []
        if photos > 0 || videos == 0 { parts.append(photos == 1 ? "1 photo" : "\(photos) photos") }
        if videos > 0 { parts.append(videos == 1 ? "1 video" : "\(videos) videos") }
        return parts.joined(separator: ", ")
    }

    static func countLabel(_ attachments: [UserAttachment]) -> String {
        let videos = attachments.filter(\.isVideo).count
        return countLabel(photos: attachments.count - videos, videos: videos)
    }

    /// "0:07", "1:05", "1:05:03".
    static func durationLabel(_ seconds: Double) -> String {
        let total = max(0, Int(seconds.rounded()))
        let h = total / 3600, m = (total % 3600) / 60, s = total % 60
        return h > 0 ? String(format: "%d:%02d:%02d", h, m, s) : String(format: "%d:%02d", m, s)
    }
}

/// Play glyph (centred) + duration badge (bottom-left) drawn over a video tile.
/// Two fixed-size aligned overlays, no flexible frames: a ZStack with
/// `.frame(maxWidth: .infinity, maxHeight: .infinity)` in this overlay sent the transcript's
/// LazyVStack into a layout loop (100% CPU) whenever a video message arrived live.
struct VideoTileOverlay: ViewModifier {
    let duration: Double?
    var compact = false

    func body(content: Content) -> some View {
        content
            .overlay {
                Image(systemName: "play.fill")
                    .font(.system(size: compact ? 12 : 16, weight: .semibold))
                    .foregroundStyle(.white)
                    .frame(width: compact ? 26 : 36, height: compact ? 26 : 36)
                    .background(.black.opacity(0.45), in: Circle())
                    .allowsHitTesting(false)
            }
            .overlay(alignment: .bottomLeading) {
                if let duration {
                    Text(UserAttachment.durationLabel(duration))
                        .font(.system(size: compact ? 10 : 11, weight: .semibold).monospacedDigit())
                        .foregroundStyle(.white)
                        .padding(.horizontal, 5)
                        .padding(.vertical, 2)
                        .background(.black.opacity(0.55), in: Capsule())
                        .padding(compact ? 4 : 6)
                        .allowsHitTesting(false)
                }
            }
    }
}

extension View {
    /// Marks a thumbnail as a video (play glyph + duration badge) when `isVideo`.
    @ViewBuilder
    func videoTile(_ isVideo: Bool, duration: Double?, compact: Bool) -> some View {
        if isVideo { modifier(VideoTileOverlay(duration: duration, compact: compact)) } else { self }
    }
}

/// The photos and videos a user sent, shown above their message bubble (right-aligned, like the
/// Claude app). One shows large; several shrink to fit on one row (max 5). Tap a photo to view it
/// full screen, a video to play it.
struct SentPhotoStrip: View {
    let attachments: [UserAttachment]
    /// One full-screen cover for both kinds (two .fullScreenCover modifiers on one view can shadow each other).
    @State private var viewing: UserAttachment?

    /// `fragments` as returned by `UserAttachment.parse`.
    init(paths fragments: [String]) {
        attachments = fragments.map(UserAttachment.init(fragment:))
    }

    private var side: CGFloat {
        switch attachments.count {
        case 1: 160
        case 2: 120
        case 3: 96
        default: 64
        }
    }

    var body: some View {
        HStack(spacing: 6) {
            ForEach(attachments, id: \.self) { item in       // stable: server file paths
                Button {
                    viewing = item
                } label: {
                    RemoteImageView(path: item.thumbnailPath, contentMode: .fill)
                        .frame(width: side, height: side)
                        .videoTile(item.isVideo, duration: item.duration, compact: side < 100)
                        .clipShape(RoundedRectangle(cornerRadius: Theme.radiusCard, style: .continuous))
                        .claudeHairline(radius: Theme.radiusCard)
                }
                .buttonStyle(.plain)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(UserAttachment.countLabel(attachments))
        .fullScreenCover(item: $viewing) { item in
            if item.isVideo { RemoteVideoPlayer(path: item.path) } else { ImageViewer(path: item.path) }
        }
    }
}

extension UserAttachment: Identifiable {
    var id: String { fragment }
}

/// Full-screen player for an uploaded video, streamed from GET /screenshots/<name> (the server
/// honours Range requests, which AVPlayer needs) with the bearer token.
struct RemoteVideoPlayer: View {
    let path: String
    @Environment(AppState.self) private var appState
    @Environment(\.dismiss) private var dismiss
    @State private var player: AVPlayer?

    var body: some View {
        NavigationStack {
            ZStack {
                Color.black.ignoresSafeArea()
                if let player {
                    VideoPlayer(player: player)
                        .ignoresSafeArea(edges: .bottom)
                } else {
                    ProgressView().tint(.white)
                }
            }
            .gesture(DragGesture(minimumDistance: 30).onEnded { value in
                if value.translation.height > 120 { dismiss() }
            })
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                        .fontWeight(.semibold)
                        .foregroundStyle(Theme.clay)
                }
            }
            .toolbarBackground(.black, for: .navigationBar)
            .toolbarColorScheme(.dark, for: .navigationBar)
            .navigationBarTitleDisplayMode(.inline)
            .accessibilityHint("Swipe down to close")
        }
        .task {
            let media = appState.client.mediaRequest(path: path)
            let asset = AVURLAsset(url: media.url, options: ["AVURLAssetHTTPHeaderFieldsKey": media.headers])
            let player = AVPlayer(playerItem: AVPlayerItem(asset: asset))
            self.player = player
            player.play()
        }
        .onDisappear { player?.pause() }
    }
}

private struct ViewedImage: Identifiable {
    let path: String
    var id: String { path }
}

/// Loads an image from the server once and caches it in memory.
struct RemoteImageView: View {
    let path: String
    var contentMode: ContentMode = .fit
    /// Called with the image once it's available (from cache or the network).
    var onLoad: ((UIImage) -> Void)? = nil
    @Environment(AppState.self) private var appState
    @State private var image: UIImage?
    @State private var failed = false

    private static let cache = NSCache<NSString, UIImage>()

    var body: some View {
        ZStack {
            if let image {
                Image(uiImage: image)
                    .resizable()
                    .aspectRatio(contentMode: contentMode)
            } else {
                Theme.bgStrong
                Image(systemName: failed ? "photo.badge.exclamationmark" : "photo")
                    .font(.system(size: 16, weight: .light))
                    .foregroundStyle(Theme.textMuted)
            }
        }
        .task(id: path) {
            if let cached = Self.cache.object(forKey: path as NSString) { image = cached; onLoad?(cached); return }
            do {
                let data = try await appState.client.fetchImage(path: path)
                if let ui = UIImage(data: data) {
                    Self.cache.setObject(ui, forKey: path as NSString)
                    image = ui
                    onLoad?(ui)
                } else {
                    failed = true
                }
            } catch {
                failed = true
            }
        }
    }
}

/// Full-screen photo viewer (docs/design-claude-uplift.md → Photo viewer): pinch/double-tap zoom,
/// pan while zoomed, and swipe-to-close at 1× only.
private struct ImageViewer: View {
    let path: String
    @Environment(\.dismiss) private var dismiss
    @State private var imageSize: CGSize?
    /// Committed zoom and pan (screen point = content point × scale + offset, relative to centre).
    @State private var scale: CGFloat = 1
    @State private var offset: CGSize = .zero
    /// How far the image has been pulled past its edge, 1:1 with the finger; drives swipe-to-close.
    @State private var dragOffset: CGSize = .zero
    /// Scale and offset when the current pinch began.
    @State private var pinchStart: (scale: CGFloat, offset: CGSize)?
    /// Offset when the current one-finger drag began (re-captured after a pinch ends mid-drag).
    @State private var panStart: CGSize?
    @State private var panStartedZoomed = false

    private static let maxScale: CGFloat = 4

    /// 0 = at rest, 1 = dragged far enough to dismiss; fades the page behind the photo.
    private var dragProgress: CGFloat {
        min(1, hypot(dragOffset.width, dragOffset.height) / 300)
    }

    var body: some View {
        NavigationStack {
            GeometryReader { geo in
                let size = geo.size
                // Gestures sit on this untransformed container so drag translations, the pinch anchor
                // and tap locations are all in screen points, whatever the zoom.
                ZStack {
                    RemoteImageView(path: path, contentMode: .fit, onLoad: { imageSize = $0.size })
                        .frame(width: size.width, height: size.height)
                        .scaleEffect(scale * (1 - dragProgress * 0.15))
                        .offset(x: offset.width + dragOffset.width, y: offset.height + dragOffset.height)
                }
                    .frame(width: size.width, height: size.height)
                    .contentShape(Rectangle())
                    .gesture(SimultaneousGesture(pinch(in: size), drag(in: size)))
                    .onTapGesture(count: 2, coordinateSpace: .local) { location in
                        withAnimation(.snappy) {
                            if scale > 1.01 {
                                scale = 1
                                offset = .zero
                            } else {
                                let p = CGPoint(x: location.x - size.width / 2, y: location.y - size.height / 2)
                                scale = 2.5
                                offset = clamped(CGSize(width: -1.5 * p.x, height: -1.5 * p.y), scale: 2.5, in: size)
                            }
                        }
                    }
                    .accessibilityZoomAction { action in
                        withAnimation(.snappy) {
                            scale = action.direction == .zoomIn ? min(Self.maxScale, scale + 1) : max(1, scale - 1)
                            offset = clamped(offset, scale: scale, in: size)
                        }
                    }
            }
            // Lay out against the whole screen, so hiding the nav bar during a swipe can't move the image.
            .ignoresSafeArea()
            .background(Theme.bgPage.opacity(1 - dragProgress * 0.7))
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                        .fontWeight(.semibold)
                        .foregroundStyle(Theme.clay)
                }
            }
            .toolbar(dragProgress > 0.05 ? .hidden : .visible, for: .navigationBar)
            .navigationBarTitleDisplayMode(.inline)
            .accessibilityHint(scale > 1.01 ? "Double-tap to zoom out" : "Swipe to close")
        }
        .presentationBackground(.clear)
    }

    // MARK: Gestures

    private func pinch(in size: CGSize) -> some Gesture {
        MagnifyGesture()
            .onChanged { value in
                if pinchStart == nil {
                    pinchStart = (scale, offset)
                    panStart = nil                       // the drag re-captures once the pinch ends
                    if dragOffset != .zero { withAnimation(.snappy) { dragOffset = .zero } }
                }
                guard let start = pinchStart else { return }
                let newScale = min(Self.maxScale * 1.5, max(0.5, start.scale * value.magnification))
                // Keep the content under the pinch's starting anchor fixed on screen.
                let p = CGPoint(x: (value.startAnchor.x - 0.5) * size.width,
                                y: (value.startAnchor.y - 0.5) * size.height)
                let k = newScale / start.scale
                scale = newScale
                offset = CGSize(width: p.x - (p.x - start.offset.width) * k,
                                height: p.y - (p.y - start.offset.height) * k)
            }
            .onEnded { _ in
                pinchStart = nil
                withAnimation(.snappy) {
                    scale = min(Self.maxScale, max(1, scale))
                    offset = scale <= 1 ? .zero : clamped(offset, scale: scale, in: size)
                }
            }
    }

    /// One drag gesture, two jobs, chosen from the zoom when the drag starts (or when a pinch ends
    /// mid-drag): zoomed, it pans and stops at the image's edges; at 1×, it's swipe-to-close.
    private func drag(in size: CGSize) -> some Gesture {
        DragGesture(minimumDistance: 10)
            .onChanged { value in
                guard pinchStart == nil else { return }
                if panStart == nil {
                    panStart = CGSize(width: offset.width - value.translation.width,
                                      height: offset.height - value.translation.height)
                    panStartedZoomed = scale > 1.01
                }
                guard let start = panStart else { return }
                if panStartedZoomed {
                    offset = clamped(CGSize(width: start.width + value.translation.width,
                                            height: start.height + value.translation.height),
                                     scale: scale, in: size)
                } else {
                    dragOffset = value.translation
                }
            }
            .onEnded { value in
                defer { panStart = nil }
                guard pinchStart == nil, let start = panStart else { return }
                if panStartedZoomed {
                    // Carry the flick's momentum, stopping at the edge.
                    withAnimation(.smooth(duration: 0.4)) {
                        offset = clamped(CGSize(width: start.width + value.predictedEndTranslation.width,
                                                height: start.height + value.predictedEndTranslation.height),
                                         scale: scale, in: size)
                    }
                    return
                }
                let distance = hypot(value.translation.width, value.translation.height)
                let flung = hypot(value.predictedEndTranslation.width, value.predictedEndTranslation.height)
                if distance > 110 || flung > 320 {
                    withAnimation(.easeOut(duration: 0.18)) {
                        dragOffset = CGSize(width: value.predictedEndTranslation.width * 1.5,
                                            height: value.predictedEndTranslation.height * 1.5)
                    }
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { dismiss() }
                } else {
                    withAnimation(.snappy) { dragOffset = .zero }
                }
            }
    }

    // MARK: Bounds

    /// The image's on-screen size at 1× (aspect-fit); the whole frame until it has loaded.
    private func fittedSize(in size: CGSize) -> CGSize {
        guard let image = imageSize, image.width > 0, image.height > 0 else { return size }
        let k = min(size.width / image.width, size.height / image.height)
        return CGSize(width: image.width * k, height: image.height * k)
    }

    /// `offset` limited so the zoomed image's edges don't come inside the screen's edges; an axis
    /// where the zoomed image fits stays centred.
    private func clamped(_ offset: CGSize, scale: CGFloat, in size: CGSize) -> CGSize {
        let fit = fittedSize(in: size)
        let maxX = max(0, (fit.width * scale - size.width) / 2)
        let maxY = max(0, (fit.height * scale - size.height) / 2)
        return CGSize(width: min(maxX, max(-maxX, offset.width)),
                      height: min(maxY, max(-maxY, offset.height)))
    }
}
