import SwiftUI
import PhotosUI
import UniformTypeIdentifiers
import AVFoundation

struct InputBarView: View {
    @Binding var text: String
    let isSending: Bool
    let skills: [SkillSuggestion]
    /// Called with the text being sent; the field is already cleared.
    let onSend: (String) -> Void
    /// Called with the attached photos and videos (in pick order) and the caption; the caller
    /// uploads them all, then sends ONE input "<p1> <video [video: …]> ... <caption>".
    let onPhotos: ([PendingPhoto], String) -> Void
    let onFile: (Data, String) -> Void
    let onVoiceSend: (String) -> Void
    let onKey: (String) -> Void
    var onHistoryBack: (() -> String?)? = nil
    var onHistoryForward: (() -> String?)? = nil

    @State private var selectedItems: [PhotosPickerItem] = []
    @State private var showFilePicker = false
    @State private var showCamera = false
    @State private var showLibrary = false
    /// Set by the camera's Library button; the picker opens once the camera has dismissed.
    @State private var libraryAfterCamera = false
    @State private var speech = SpeechRecognizer()
    @State private var voiceMode = true  // Push-to-talk is the default input mode
    @State private var draggedOff = false
    /// True from a hold-to-talk press's first touch until it lifts: each press starts recording once.
    @State private var pressStarted = false
    /// Also true during a press, but reset by SwiftUI on cancel too (onEnded doesn't run then).
    @GestureState private var pressing = false
    @State private var pendingPhotos: [PendingPhoto] = []
    /// Picked items still loading (videos can take a moment to copy out of Photos).
    @State private var loadingCount = 0
    @State private var attachNotice: String?
    @State private var showSkillPicker = false
    @State private var stagedSkill: SkillSuggestion?
    @State private var skillSearchText = ""

    private var filteredSkills: [SkillSuggestion] {
        guard text.hasPrefix("/") else { return [] }
        let query = String(text.dropFirst()).lowercased()
        if query.isEmpty { return skills }
        return skills.filter { $0.name.lowercased().contains(query) }
    }

    private var showSuggestions: Bool {
        text.hasPrefix("/") && !filteredSkills.isEmpty && !text.contains(" ")
    }

    var body: some View {
        VStack(spacing: 0) {
            if showSuggestions && !voiceMode {
                suggestionsView
                Rectangle().fill(Theme.border).frame(height: 0.5)
            }
            if voiceMode {
                voiceModeView
            } else {
                textModeView
            }
        }
        .claudeCard(radius: Theme.radiusComposer)
        .padding(.horizontal, 10)
        .padding(.top, 6)
        .padding(.bottom, 6)
        .background(Theme.bgPage)
        .tint(Theme.clay)
        .onChange(of: selectedItems) {
            let items = selectedItems
            guard !items.isEmpty else { return }
            selectedItems = []
            attachNotice = nil
            loadingCount += items.count
            Task {
                let (loaded, skippedLarge) = await Self.loadPhotos(items)
                loadingCount -= items.count
                let all = pendingPhotos + loaded
                for dropped in all.dropFirst(Self.maxPhotos) { dropped.discardLocalFile() }
                pendingPhotos = Array(all.prefix(Self.maxPhotos))
                if skippedLarge > 0 {
                    attachNotice = skippedLarge == 1 ? "Video over 500 MB skipped" : "\(skippedLarge) videos over 500 MB skipped"
                }
            }
        }
        .photosPicker(isPresented: $showLibrary,
                      selection: $selectedItems,
                      maxSelectionCount: max(1, Self.maxPhotos - pendingPhotos.count - loadingCount),
                      matching: .any(of: [.images, .videos]),
                      preferredItemEncoding: .current)
        .fullScreenCover(isPresented: $showCamera, onDismiss: {
            if libraryAfterCamera {
                libraryAfterCamera = false
                showLibrary = true
            }
        }) {
            CameraCaptureView(
                onPhoto: { data in
                    showCamera = false
                    addCapturedPhoto(data)
                },
                onLibrary: {
                    libraryAfterCamera = true
                    showCamera = false
                },
                onClose: { showCamera = false }
            )
        }
        .sheet(isPresented: $showSkillPicker) {
            skillPickerSheet
        }
        .fileImporter(isPresented: $showFilePicker, allowedContentTypes: [.item], allowsMultipleSelection: false) { result in
            guard case .success(let urls) = result, let url = urls.first else { return }
            guard url.startAccessingSecurityScopedResource() else { return }
            defer { url.stopAccessingSecurityScopedResource() }
            if let data = try? Data(contentsOf: url) {
                onFile(data, url.lastPathComponent)
            }
        }
    }

    // MARK: - Suggestions overlay

    private var suggestionsView: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 0) {
                ForEach(filteredSkills) { skill in
                    Button {
                        text = skill.command + " "
                    } label: {
                        HStack(spacing: 6) {
                            Text(skill.command)
                                .font(.claudeMono(15))
                                .fontWeight(.medium)
                                .foregroundStyle(Theme.textPrimary)
                            if skill.source != "builtin" {
                                Text(skill.source)
                                    .font(.system(size: 10))
                                    .foregroundStyle(Theme.textSecondary)
                                    .padding(.horizontal, 6)
                                    .padding(.vertical, 2)
                                    .background(Theme.bgSunken, in: Capsule())
                            }
                            Spacer()
                            Text(skill.description)
                                .font(.claudeCaption)
                                .foregroundStyle(Theme.textMuted)
                                .lineLimit(1)
                        }
                        .padding(.horizontal)
                        .padding(.vertical, 8)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                }
            }
        }
        .frame(maxHeight: 200)
    }

    // MARK: - Input helper strip (history + common symbols)

    private var keyStripView: some View {
        ScrollView(.horizontal, showsIndicators: true) {
            HStack(spacing: 8) {
                // History navigation
                stripIcon("arrow.up.doc") {
                    if let prev = onHistoryBack?() {
                        text = prev
                    }
                }
                stripIcon("arrow.down.doc") {
                    if let next = onHistoryForward?() {
                        text = next
                    }
                }

                Rectangle().fill(Theme.border).frame(width: 0.5, height: 20)

                // Common symbols — insert into the text field
                ForEach(["|", "/", "~", "`", "-", "_", "@", "#", "$", "=", "+", ";", ":", "\"", "'", "\\", "!", "%", "&", "*"], id: \.self) { char in
                    stripKey(char) { text.append(char) }
                }
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 6)
        }
    }

    private func stripKey(_ label: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(label)
                .font(.claudeMono(14))
                .foregroundStyle(Theme.textSecondary)
                .frame(minWidth: 30, minHeight: 30)
                .padding(.horizontal, 3)
                .background(Theme.bgSunken, in: RoundedRectangle(cornerRadius: Theme.radiusSmall, style: .continuous))
        }
        .buttonStyle(.plain)
    }

    private func stripIcon(_ name: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: name)
                .font(.system(size: 14, weight: .regular))
                .foregroundStyle(Theme.textSecondary)
                .frame(width: 30, height: 30)
                .background(Theme.bgSunken, in: RoundedRectangle(cornerRadius: Theme.radiusSmall, style: .continuous))
        }
        .buttonStyle(.plain)
    }

    // MARK: - Shared action buttons

    @ViewBuilder
    private var sharedActionButtons: some View {
        Button {
            Task {
                if await CameraCaptureView.cameraUsable() { showCamera = true } else { showLibrary = true }
            }
        } label: {
            Image(systemName: "camera")
                .composerIcon()
        }
        .disabled(isSending || pendingPhotos.count + loadingCount >= Self.maxPhotos)
        .accessibilityLabel("Take photo or attach from library")

        Button { showFilePicker = true } label: {
            Image(systemName: "doc")
                .composerIcon()
        }
        .disabled(isSending)

        Button { showSkillPicker = true } label: {
            Image(systemName: "command")
                .composerIcon()
        }
        .accessibilityLabel("command.circle")
        .disabled(isSending || skills.isEmpty)
    }

    /// A photo from the in-app camera: normalized off the main actor like a picked photo.
    private func addCapturedPhoto(_ data: Data) {
        guard pendingPhotos.count + loadingCount < Self.maxPhotos else { return }
        attachNotice = nil
        loadingCount += 1
        Task {
            let photo = await Task.detached { () -> PendingPhoto? in
                guard let jpeg = ImageNormalizer.uploadJPEG(from: data),
                      let thumb = UIImage(data: jpeg)?.preparingThumbnail(of: CGSize(width: 180, height: 180))
                else { return nil }
                return PendingPhoto(content: .image(jpeg), thumbnail: thumb)
            }.value
            loadingCount -= 1
            if let photo, pendingPhotos.count < Self.maxPhotos { pendingPhotos.append(photo) }
        }
    }

    static let maxPhotos = 5
    /// Height of the one-line composer field (16pt line + 8pt vertical padding each side).
    static let composerLineHeight: CGFloat = 37

    /// The server rejects bigger uploads (POST /videos caps at 500 MB).
    static let maxVideoBytes = 500_000_000

    /// A picked photo (normalized JPEG, ready to upload) or video (a copy in the app's temp dir).
    struct PendingPhoto: Identifiable {
        enum Content {
            case image(Data)
            case video(URL, duration: Double)
        }
        let id = UUID()
        let content: Content
        let thumbnail: UIImage

        var duration: Double? {
            if case .video(_, let duration) = content { return duration }
            return nil
        }
        var isVideo: Bool { duration != nil }

        /// Delete the temp copy of a video that won't be sent.
        func discardLocalFile() {
            if case .video(let url, _) = content { try? FileManager.default.removeItem(at: url) }
        }
    }

    /// A video from Photos, received as a file and copied into our temp dir (never read into memory).
    private struct PickedMovie: Transferable {
        let url: URL
        static var transferRepresentation: some TransferRepresentation {
            FileRepresentation(importedContentType: .movie) { received in
                let dir = FileManager.default.temporaryDirectory.appendingPathComponent("mclaude-videos", isDirectory: true)
                try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
                let ext = received.file.pathExtension.isEmpty ? "mov" : received.file.pathExtension.lowercased()
                let dest = dir.appendingPathComponent("\(UUID().uuidString).\(ext)")
                try FileManager.default.copyItem(at: received.file, to: dest)
                return PickedMovie(url: dest)
            }
        }
    }

    private enum Loaded {
        case item(PendingPhoto)
        case tooLarge
        case failed
    }

    /// Load picked photos and videos concurrently off the main actor, preserving pick order.
    /// Photos: HEIC/large -> JPEG, long edge <= 2000px (API rejects HEIC; server caps ~3.7MB).
    /// Videos: file copy + first-frame thumbnail + duration. Returns the items and how many videos
    /// were skipped for being over the size cap.
    private static func loadPhotos(_ items: [PhotosPickerItem]) async -> ([PendingPhoto], skippedLarge: Int) {
        await withTaskGroup(of: (Int, Loaded).self) { group in
            for (index, item) in items.enumerated() {
                group.addTask {
                    let types = item.supportedContentTypes
                    let isVideo = !types.contains { $0.conforms(to: .image) } && types.contains { $0.conforms(to: .movie) }
                    return (index, isVideo ? await loadVideo(item) : await loadImage(item))
                }
            }
            var results: [(Int, Loaded)] = []
            for await result in group { results.append(result) }
            results.sort { $0.0 < $1.0 }
            let photos = results.compactMap { if case .item(let p) = $0.1 { p } else { nil } }
            let skipped = results.filter { if case .tooLarge = $0.1 { true } else { false } }.count
            return (photos, skipped)
        }
    }

    private static func loadImage(_ item: PhotosPickerItem) async -> Loaded {
        guard let raw = try? await item.loadTransferable(type: Data.self),
              let jpeg = ImageNormalizer.uploadJPEG(from: raw),
              let thumb = UIImage(data: jpeg)?.preparingThumbnail(of: CGSize(width: 180, height: 180))
        else { return .failed }
        return .item(PendingPhoto(content: .image(jpeg), thumbnail: thumb))
    }

    private static func loadVideo(_ item: PhotosPickerItem) async -> Loaded {
        guard let movie = try? await item.loadTransferable(type: PickedMovie.self) else { return .failed }
        let size = (try? movie.url.resourceValues(forKeys: [.fileSizeKey]).fileSize) ?? 0
        guard size <= maxVideoBytes else {
            try? FileManager.default.removeItem(at: movie.url)
            return .tooLarge
        }
        let asset = AVURLAsset(url: movie.url)
        let generator = AVAssetImageGenerator(asset: asset)
        generator.appliesPreferredTrackTransform = true
        generator.maximumSize = CGSize(width: 360, height: 360)
        guard let duration = try? await asset.load(.duration).seconds, duration.isFinite,
              let frame = try? await generator.image(at: .zero).image
        else {
            try? FileManager.default.removeItem(at: movie.url)
            return .failed
        }
        return .item(PendingPhoto(content: .video(movie.url, duration: duration), thumbnail: UIImage(cgImage: frame)))
    }

    @ViewBuilder
    private var photosPreview: some View {
        if !pendingPhotos.isEmpty || loadingCount > 0 || attachNotice != nil {
            VStack(alignment: .leading, spacing: 4) {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 10) {
                        ForEach(pendingPhotos) { photo in
                            Image(uiImage: photo.thumbnail)
                                .resizable()
                                .scaledToFill()
                                .frame(width: 60, height: 60)
                                .videoTile(photo.isVideo, duration: photo.duration, compact: true)
                                .clipShape(RoundedRectangle(cornerRadius: Theme.radiusSmall + 2, style: .continuous))
                                .claudeHairline(radius: Theme.radiusSmall + 2)
                                .accessibilityElement(children: .ignore)
                                .accessibilityLabel(photo.duration.map { "Video, \(UserAttachment.durationLabel($0))" } ?? "Photo")
                                .overlay(alignment: .topTrailing) {
                                    Button {
                                        photo.discardLocalFile()
                                        pendingPhotos.removeAll { $0.id == photo.id }
                                    } label: {
                                        Image(systemName: "xmark")
                                            .font(.system(size: 9, weight: .bold))
                                            .foregroundStyle(Theme.textPrimary)
                                            .frame(width: 20, height: 20)
                                            .background(Theme.bgRaised, in: Circle())
                                            .claudeHairline(radius: 10)
                                    }
                                    .accessibilityLabel(photo.isVideo ? "Remove video" : "Remove photo")
                                    .offset(x: 4, y: -4)
                                }
                        }
                        ForEach(0..<loadingCount, id: \.self) { _ in
                            ProgressView()
                                .frame(width: 60, height: 60)
                                .background(Theme.bgSunken, in: RoundedRectangle(cornerRadius: Theme.radiusSmall + 2, style: .continuous))
                                .claudeHairline(radius: Theme.radiusSmall + 2)
                                .accessibilityLabel("Loading attachment")
                        }
                    }
                    .padding(.top, 4)
                    .padding(.trailing, 4)
                }
                if !pendingPhotos.isEmpty {
                    let videos = pendingPhotos.filter(\.isVideo).count
                    Text(UserAttachment.countLabel(photos: pendingPhotos.count - videos, videos: videos) + " attached")
                        .font(.claudeFootnote)
                        .foregroundStyle(Theme.textMuted)
                }
                if let attachNotice {
                    Text(attachNotice)
                        .font(.claudeFootnote)
                        .foregroundStyle(Theme.textMuted)
                }
            }
            .padding(.horizontal, 12)
            .padding(.top, 8)
            .padding(.bottom, 2)
        }
    }

    /// Interrupts the running turn (or dismisses a pending prompt). Sent as "Escape",
    /// which the headless server maps to an interrupt.
    private var stopButton: some View {
        Button {
            onKey("Escape")
        } label: {
            stopIcon
        }
        .accessibilityLabel("Stop")
    }

    // MARK: - Text mode (normal keyboard input)

    private var textModeView: some View {
        VStack(spacing: 0) {
            keyStripView
            photosPreview
            // Bottom-aligned so the icons and ↑ stay on the last line as the field grows. The
            // icons and ↑ sit in one-line-high slots so they stay centred on that line.
            HStack(alignment: .bottom, spacing: 8) {
                HStack(spacing: 8) {
                    sharedActionButtons

                    Button {
                        Task {
                            if !speech.permissionGranted {
                                guard await speech.requestPermission() else { return }
                            }
                            voiceMode = true
                        }
                    } label: {
                        Image(systemName: "mic")
                            .composerIcon()
                    }
                    .accessibilityLabel("Microphone")
                    .disabled(isSending)

                    stopButton
                }
                .frame(height: Self.composerLineHeight)

                // Multi-line, grows to ~5 lines then scrolls. Autocorrect / spell-check / sentence
                // capitalization stay at system defaults (on). Software-keyboard Return inserts a
                // newline (send with ↑); a hardware Return sends, Shift+Return inserts a newline.
                TextField("Reply to Claude…", text: $text, axis: .vertical)
                    .lineLimit(1...5)
                    .font(.claudeBody)
                    .foregroundStyle(Theme.textPrimary)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 8)
                    .background(Theme.bgSunken, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
                    // Telemetry: every write to the model's copy of the text (observe only)
                    .onChange(of: text) { old, new in ComposerProbe.shared.modelChanged(from: old, to: new) }
                    .onKeyPress(.return, phases: .down) { press in
                        guard !press.modifiers.contains(.shift) else { return .ignored }
                        submitText()
                        return .handled
                    }

                let canSend = !(text.isEmpty && pendingPhotos.isEmpty || isSending || loadingCount > 0)
                Button {
                    submitText()
                } label: {
                    Image(systemName: "arrow.up")
                        .font(.system(size: 15, weight: .semibold))
                        .foregroundStyle(canSend ? Color.white : Theme.textMuted)
                        .frame(width: 34, height: 34)
                        .background(canSend ? Theme.clay : Theme.bgSunken, in: Circle())
                }
                .accessibilityLabel("Arrow Up Circle")
                .disabled(!canSend)
                .frame(height: Self.composerLineHeight)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
        }
    }

    /// Send typed text. With a photo attached, text becomes its caption and both
    /// go out as a single input (upload first, then "<path> <caption>").
    private func submitText() {
        if !pendingPhotos.isEmpty {
            let photos = pendingPhotos
            pendingPhotos = []
            let field = ComposerField.atSend(modelText: text)
            let caption = field.message
            ComposerProbe.shared.willSend(message: caption, modelText: text, hasPhotos: true)   // telemetry
            field.empty()
            ComposerProbe.shared.willWrite(source: "send_clear", value: "")
            text = ""
            onPhotos(photos, caption)
        } else if !text.isEmpty {
            let field = ComposerField.atSend(modelText: text)
            let message = field.message
            guard !message.isEmpty else { return }   // only an unaccepted prediction: nothing to send
            ComposerProbe.shared.willSend(message: message, modelText: text, hasPhotos: false)   // telemetry
            field.empty()
            ComposerProbe.shared.willWrite(source: "send_clear", value: "")
            text = ""
            onSend(message)
            // An autocorrect, dictation or inline-prediction commit can land just after the clear
            // and put the sent text back; drop it if the field still holds exactly that.
            DispatchQueue.main.async {
                let reclear = text == message
                ComposerProbe.shared.reclear(executed: reclear, modelText: text)   // telemetry
                if reclear {
                    ComposerProbe.shared.willWrite(source: "reclear", value: "")
                    text = ""
                }
            }
        }
    }

    // MARK: - Voice mode (push-to-talk replaces keyboard)

    private var voiceModeView: some View {
        VStack(spacing: 12) {
            // Action buttons + transcript
            HStack {
                sharedActionButtons

                Button {
                    speech.stopRecording()
                    stagedSkill = nil
                    voiceMode = false
                } label: {
                    Image(systemName: "keyboard")
                        .composerIcon()
                }
                .accessibilityLabel("Keyboard")

                Button {
                    stagedSkill = nil
                    onKey("Escape")
                } label: {
                    stopIcon
                }
                .accessibilityLabel("Stop")

                Text(voiceTranscriptPlaceholder)
                    .font(.claudeBody)
                    .foregroundStyle(speech.error != nil && speech.transcript.isEmpty ? Theme.danger
                                     : speech.transcript.isEmpty ? Theme.textMuted : Theme.textPrimary)
                    .lineLimit(3)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(.horizontal, 12)
            .padding(.top, 10)

            photosPreview

            // Staged skill chip
            if let skill = stagedSkill {
                HStack(spacing: 8) {
                    Text(skill.command)
                        .font(.claudeMono(14))
                        .fontWeight(.semibold)
                        .foregroundStyle(Theme.textPrimary)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 5)
                        .background(Theme.bgSunken, in: Capsule())
                        .overlay(Capsule().strokeBorder(Theme.clay.opacity(0.5), lineWidth: 1))

                    Button {
                        onVoiceSend(skill.command)
                        stagedSkill = nil
                    } label: {
                        Image(systemName: "arrow.up")
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundStyle(.white)
                            .frame(width: 28, height: 28)
                            .background(Theme.clay, in: Circle())
                    }
                    .accessibilityLabel("Send")

                    Button {
                        stagedSkill = nil
                    } label: {
                        Image(systemName: "xmark")
                            .font(.system(size: 12, weight: .medium))
                            .foregroundStyle(Theme.textSecondary)
                    }
                    .accessibilityLabel("Remove skill")
                }
                .padding(.horizontal)
            }

            // Push-to-talk button
            ZStack {
                // Cancel zone indicator
                if speech.isRecording && draggedOff {
                    Text("Release to cancel")
                        .font(.claudeCaption)
                        .foregroundStyle(Theme.danger)
                        .offset(y: -50)
                }

                Circle()
                    .fill(speech.isRecording ? (draggedOff ? Theme.danger : Theme.clay) : Theme.bgSunken)
                    .frame(width: 72, height: 72)
                    .overlay {
                        Image(systemName: speech.isRecording ? (draggedOff ? "xmark" : "mic.fill") : "mic")
                            .font(.system(size: 26, weight: .regular))
                            .foregroundStyle(speech.isRecording ? Color.white : Theme.textSecondary)
                    }
                    .overlay {
                        if stagedSkill != nil && !speech.isRecording {
                            Circle()
                                .strokeBorder(Theme.clay, lineWidth: 2)
                        }
                    }
                    .accessibilityLabel("Hold to talk")
                    .gesture(
                        DragGesture(minimumDistance: 0)
                            .updating($pressing) { _, state, _ in state = true }
                            .onChanged { value in
                                if !pressStarted {
                                    pressStarted = true
                                    draggedOff = false
                                    speech.startRecording()
                                }
                                let distance = sqrt(value.translation.width * value.translation.width +
                                                    value.translation.height * value.translation.height)
                                draggedOff = distance > 60
                            }
                            .onEnded { _ in
                                pressStarted = false
                                let wasRecording = speech.isRecording
                                speech.stopRecording()
                                let wasCancelled = draggedOff
                                draggedOff = false
                                if wasCancelled || !wasRecording { return }
                                Task {
                                    let final = await speech.waitForFinalTranscript()
                                    var message = final
                                    if let skill = stagedSkill {
                                        message = final.isEmpty ? skill.command : "\(skill.command) \(final)"
                                        stagedSkill = nil
                                    }
                                    if !pendingPhotos.isEmpty {
                                        // One input: photo paths + transcript as caption
                                        let photos = pendingPhotos
                                        pendingPhotos = []
                                        onPhotos(photos, message)
                                    } else if !message.isEmpty {
                                        onVoiceSend(message)
                                    }
                                    speech.transcript = ""
                                }
                            }
                    )
                    .onChange(of: pressing) { _, isPressing in
                        // A cancelled press: onEnded never ran, so reset here and drop the recording.
                        // Deferred one turn so a normal release's onEnded always goes first.
                        guard !isPressing else { return }
                        DispatchQueue.main.async {
                            guard pressStarted else { return }
                            pressStarted = false
                            draggedOff = false
                            speech.stopRecording()
                            speech.transcript = ""
                        }
                    }
            }
            .padding(.bottom, 14)
        }
    }

    private var stopIcon: some View {
        Image(systemName: "stop.circle")
            .font(.system(size: 20, weight: .regular))
            .foregroundStyle(Theme.danger)
    }

    private var voiceTranscriptPlaceholder: String {
        if !speech.transcript.isEmpty {
            return speech.transcript
        }
        if let error = speech.error { return error }
        return stagedSkill != nil ? "Hold to add arguments, or tap Send" : "Hold to talk"
    }

    private var skillPickerSheet: some View {
        NavigationStack {
            List {
                if !skillSearchText.isEmpty || skills.count > 8 {
                    // Search field shown inline for larger skill lists
                }
                ForEach(filteredPickerSkills) { skill in
                    Button {
                        if voiceMode {
                            stagedSkill = skill
                        } else {
                            text = skill.command + " "
                        }
                        showSkillPicker = false
                        skillSearchText = ""
                    } label: {
                        HStack(spacing: 6) {
                            Text(skill.command)
                                .font(.claudeMono(15))
                                .fontWeight(.medium)
                                .foregroundStyle(Theme.textPrimary)
                            if skill.source != "builtin" {
                                Text(skill.source)
                                    .font(.system(size: 10))
                                    .foregroundStyle(Theme.textSecondary)
                                    .padding(.horizontal, 6)
                                    .padding(.vertical, 2)
                                    .background(Theme.bgSunken, in: Capsule())
                            }
                            Spacer()
                            Text(skill.description)
                                .font(.claudeCaption)
                                .foregroundStyle(Theme.textMuted)
                                .lineLimit(1)
                        }
                    }
                    .buttonStyle(.plain)
                    .listRowBackground(Theme.bgRaised)
                }
            }
            .scrollContentBackground(.hidden)
            .background(Theme.bgPage)
            .searchable(text: $skillSearchText, prompt: "Filter skills")
            .navigationTitle("Skills")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        showSkillPicker = false
                        skillSearchText = ""
                    }
                }
            }
        }
        .presentationDetents([.medium, .large])
    }

    private var filteredPickerSkills: [SkillSuggestion] {
        guard !skillSearchText.isEmpty else { return skills }
        let query = skillSearchText.lowercased()
        return skills.filter { $0.name.lowercased().contains(query) }
    }
}

private extension Image {
    /// Composer row icon: SF Symbol, regular weight, text-secondary (dimmed when disabled).
    func composerIcon() -> some View { ComposerIcon(image: self) }
}

private struct ComposerIcon: View {
    let image: Image
    @Environment(\.isEnabled) private var isEnabled

    var body: some View {
        image
            .font(.system(size: 19, weight: .regular))
            .foregroundStyle(Theme.textSecondary)
            .opacity(isEnabled ? 1 : 0.35)
            .frame(width: 30, height: 30)
    }
}

/// The composer's UIKit text view at send time (docs/design-claude-uplift.md → Still-composing text
/// at send). iOS can hold uncommitted ("marked") text in it: an inline prediction (the grey ghost
/// word) or a composition (dictation, Chinese/Japanese input). SwiftUI won't push the cleared text
/// into the view while marked text is present, so the send resolves it on the view directly.
@MainActor
struct ComposerField {
    /// What to send: the field's text with an unaccepted inline prediction removed.
    let message: String
    private let textView: UITextView?
    private let prediction: NSRange?

    /// Finds the composer's text view (the editable one holding `modelText`) and works out the
    /// message without changing anything yet.
    static func atSend(modelText: String) -> ComposerField {
        guard let tv = findTextView(holding: modelText) else {
            return ComposerField(message: modelText, textView: nil, prediction: nil)
        }
        let text = tv.text ?? modelText
        guard let marked = tv.markedTextRange else {
            return ComposerField(message: text, textView: tv, prediction: nil)
        }
        let start = tv.offset(from: tv.beginningOfDocument, to: marked.start)
        let end = tv.offset(from: tv.beginningOfDocument, to: marked.end)
        let selection = tv.selectedRange
        // An inline prediction sits ahead of the cursor: marked text starting exactly at a collapsed
        // cursor. A composition has the cursor at or inside its end.
        if end > start, selection.length == 0, selection.location == start {
            let range = NSRange(location: start, length: end - start)
            let message = (text as NSString).replacingCharacters(in: range, with: "")
            return ComposerField(message: message, textView: tv, prediction: range)
        }
        return ComposerField(message: text, textView: tv, prediction: nil)
    }

    /// Ends any marked text (discarding a prediction, committing a composition) and empties the
    /// text view, so it agrees with the app's cleared copy.
    func empty() {
        guard let tv = textView else { return }
        if tv.markedTextRange != nil {
            if prediction != nil { tv.setMarkedText("", selectedRange: NSRange(location: 0, length: 0)) }
            tv.unmarkText()
        }
        if !(tv.text ?? "").isEmpty { tv.text = "" }
    }

    private static func findTextView(holding text: String) -> UITextView? {
        let windows = UIApplication.shared.connectedScenes
            .compactMap { $0 as? UIWindowScene }
            .flatMap(\.windows)
        var matches: [UITextView] = []
        var stack: [UIView] = windows
        while let v = stack.popLast() {
            if let tv = v as? UITextView, tv.isEditable, tv.text == text { matches.append(tv) }
            stack.append(contentsOf: v.subviews)
        }
        return matches.first(where: \.isFirstResponder) ?? matches.first
    }
}
