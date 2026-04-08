import SwiftUI
import PhotosUI
import UniformTypeIdentifiers

struct InputBarView: View {
    @Binding var text: String
    let isSending: Bool
    let skills: [SkillSuggestion]
    let onSend: () -> Void
    let onPhoto: (Data) -> Void
    let onFile: (Data, String) -> Void
    let onVoiceSend: (String) -> Void
    let onKey: (String) -> Void
    var onHistoryBack: (() -> String?)? = nil
    var onHistoryForward: (() -> String?)? = nil

    @State private var selectedItem: PhotosPickerItem?
    @State private var showFilePicker = false
    @State private var speech = SpeechRecognizer()
    @State private var voiceMode = true  // Push-to-talk is the default input mode
    @State private var draggedOff = false
    @State private var pendingPhoto: Data?
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
                Divider()
            }
            if voiceMode {
                voiceModeView
            } else {
                textModeView
            }
        }
        .background(.bar)
        .onChange(of: selectedItem) {
            guard let item = selectedItem else { return }
            selectedItem = nil
            Task {
                if let data = try? await item.loadTransferable(type: Data.self) {
                    pendingPhoto = data
                }
            }
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
                                .font(.system(.body, design: .monospaced))
                                .fontWeight(.medium)
                            if skill.source != "builtin" {
                                Text(skill.source)
                                    .font(.system(size: 10))
                                    .padding(.horizontal, 5)
                                    .padding(.vertical, 2)
                                    .background(.quaternary)
                                    .clipShape(Capsule())
                            }
                            Spacer()
                            Text(skill.description)
                                .font(.caption)
                                .foregroundStyle(.secondary)
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

    // MARK: - Terminal key strip (Blink-style)

    @State private var ctrlActive = false
    @State private var shiftActive = false

    private var terminalStripView: some View {
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

                // Modifier toggles
                stripKey("ctrl", isActive: ctrlActive) {
                    ctrlActive.toggle()
                    if ctrlActive { shiftActive = false }
                }
                stripKey("shift", isActive: shiftActive) {
                    shiftActive.toggle()
                    if shiftActive { ctrlActive = false }
                }

                // Tab / Shift-Tab
                stripKey("⇥") {
                    onKey(shiftActive ? "BTab" : "Tab")
                    shiftActive = false
                }

                // Arrows (with shift support)
                stripIcon("chevron.up") {
                    onKey(shiftActive ? "S-Up" : "Up")
                    shiftActive = false
                }
                stripIcon("chevron.down") {
                    onKey(shiftActive ? "S-Down" : "Down")
                    shiftActive = false
                }
                stripIcon("chevron.left") {
                    onKey(shiftActive ? "S-Left" : "Left")
                    shiftActive = false
                }
                stripIcon("chevron.right") {
                    onKey(shiftActive ? "S-Right" : "Right")
                    shiftActive = false
                }

                Divider().frame(height: 20).opacity(0.3)

                // Common terminal chars — insert into text field (or send Ctrl combo)
                ForEach(["|", "/", "~", "`", "-", "_", "@", "#", "$", "=", "+", ";", ":", "\"", "'", "\\", "!", "%", "&", "*"], id: \.self) { char in
                    stripKey(char) {
                        if ctrlActive {
                            onKey("C-\(char)")
                            ctrlActive = false
                        } else {
                            text.append(char)
                        }
                    }
                }
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 5)
        }
        .background(Color(.systemGray6))
    }

    private func stripKey(_ label: String, isActive: Bool = false, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(label)
                .font(.system(size: 14, weight: .medium, design: .monospaced))
                .foregroundStyle(isActive ? .white : .primary)
                .frame(minWidth: 32, minHeight: 32)
                .padding(.horizontal, 4)
                .background(isActive ? Color.accentColor : Color(.systemGray4))
                .clipShape(RoundedRectangle(cornerRadius: 6))
        }
        .buttonStyle(.plain)
    }

    private func stripIcon(_ name: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: name)
                .font(.system(size: 14, weight: .medium))
                .frame(width: 32, height: 32)
                .background(Color(.systemGray4))
                .clipShape(RoundedRectangle(cornerRadius: 6))
        }
        .buttonStyle(.plain)
    }

    // MARK: - Shared action buttons

    @ViewBuilder
    private var sharedActionButtons: some View {
        PhotosPicker(selection: $selectedItem, matching: .screenshots) {
            Image(systemName: "photo")
                .font(.title3)
        }
        .disabled(isSending)

        Button { showFilePicker = true } label: {
            Image(systemName: "doc")
                .font(.title3)
        }
        .disabled(isSending)

        Button { showSkillPicker = true } label: {
            Image(systemName: "command.circle")
                .font(.title3)
        }
        .disabled(isSending || skills.isEmpty)
    }

    @ViewBuilder
    private var screenshotPreview: some View {
        if let photoData = pendingPhoto, let uiImage = UIImage(data: photoData) {
            HStack(spacing: 8) {
                Image(uiImage: uiImage)
                    .resizable()
                    .scaledToFill()
                    .frame(width: 60, height: 60)
                    .clipShape(RoundedRectangle(cornerRadius: 8))
                    .overlay(alignment: .topTrailing) {
                        Button {
                            pendingPhoto = nil
                        } label: {
                            Image(systemName: "xmark.circle.fill")
                                .font(.caption)
                                .foregroundStyle(.white)
                                .background(Circle().fill(.black.opacity(0.6)))
                        }
                        .offset(x: 4, y: -4)
                    }
                Text("Screenshot attached")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer()
            }
            .padding(.horizontal)
            .padding(.vertical, 6)
        }
    }

    private var escapeButton: some View {
        Button {
            onKey("Escape")
        } label: {
            Image(systemName: "xmark.circle.fill")
                .font(.title3)
                .foregroundStyle(.red)
        }
    }

    // MARK: - Text mode (normal keyboard input)

    private var textModeView: some View {
        VStack(spacing: 0) {
            terminalStripView
            screenshotPreview
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
                        .font(.title3)
                }
                .disabled(isSending)

                escapeButton

                TextField("Send to session...", text: $text)
                    .textFieldStyle(.roundedBorder)
                    .font(.system(.body, design: .monospaced))
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
                    .onSubmit {
                        if let photoData = pendingPhoto {
                            pendingPhoto = nil
                            onPhoto(photoData)
                        }
                        if !text.isEmpty { onSend() }
                    }

                Button {
                    if let photoData = pendingPhoto {
                        pendingPhoto = nil
                        onPhoto(photoData)
                    }
                    if !text.isEmpty {
                        onSend()
                    }
                } label: {
                    Image(systemName: "arrow.up.circle.fill")
                        .font(.title2)
                }
                .disabled(text.isEmpty && pendingPhoto == nil || isSending)
            }
            .padding(.horizontal)
            .padding(.vertical, 8)
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
                        .font(.title3)
                }

                Button {
                    stagedSkill = nil
                    onKey("Escape")
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.title3)
                        .foregroundStyle(.red)
                }

                Text(voiceTranscriptPlaceholder)
                    .font(.system(.body, design: .monospaced))
                    .foregroundStyle(speech.transcript.isEmpty ? .secondary : .primary)
                    .lineLimit(3)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(.horizontal)
            .padding(.top, 8)

            screenshotPreview

            // Staged skill chip
            if let skill = stagedSkill {
                HStack(spacing: 8) {
                    Text(skill.command)
                        .font(.system(.subheadline, design: .monospaced))
                        .fontWeight(.semibold)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 5)
                        .background(Color.accentColor.opacity(0.15))
                        .clipShape(Capsule())

                    Button {
                        onVoiceSend(skill.command)
                        stagedSkill = nil
                    } label: {
                        Image(systemName: "arrow.up.circle.fill")
                            .font(.title3)
                    }

                    Button {
                        stagedSkill = nil
                    } label: {
                        Image(systemName: "xmark.circle")
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                    }
                }
                .padding(.horizontal)
            }

            // Push-to-talk button
            ZStack {
                // Cancel zone indicator
                if speech.isRecording && draggedOff {
                    Text("Release to cancel")
                        .font(.caption)
                        .foregroundStyle(.red)
                        .offset(y: -50)
                }

                Circle()
                    .fill(speech.isRecording ? (draggedOff ? .red : .blue) : Color(.systemGray5))
                    .frame(width: 72, height: 72)
                    .overlay {
                        Image(systemName: speech.isRecording ? (draggedOff ? "xmark" : "mic.fill") : "mic.fill")
                            .font(.title)
                            .foregroundStyle(speech.isRecording ? .white : .accentColor)
                    }
                    .overlay {
                        if stagedSkill != nil && !speech.isRecording {
                            Circle()
                                .strokeBorder(Color.accentColor, lineWidth: 2)
                        }
                    }
                    .gesture(
                        DragGesture(minimumDistance: 0)
                            .onChanged { value in
                                if !speech.isRecording {
                                    draggedOff = false
                                    speech.startRecording()
                                }
                                let distance = sqrt(value.translation.width * value.translation.width +
                                                    value.translation.height * value.translation.height)
                                draggedOff = distance > 60
                            }
                            .onEnded { _ in
                                speech.stopRecording()
                                let wasCancelled = draggedOff
                                draggedOff = false
                                if wasCancelled { return }
                                Task {
                                    let final = await speech.waitForFinalTranscript()
                                    if let photoData = pendingPhoto {
                                        pendingPhoto = nil
                                        onPhoto(photoData)
                                    }
                                    if let skill = stagedSkill {
                                        let message = final.isEmpty ? skill.command : "\(skill.command) \(final)"
                                        onVoiceSend(message)
                                        stagedSkill = nil
                                    } else if !final.isEmpty {
                                        onVoiceSend(final)
                                    }
                                    speech.transcript = ""
                                }
                            }
                    )
            }
            .padding(.bottom, 16)
        }
    }

    private var voiceTranscriptPlaceholder: String {
        if !speech.transcript.isEmpty {
            return speech.transcript
        }
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
                                .font(.system(.body, design: .monospaced))
                                .fontWeight(.medium)
                            if skill.source != "builtin" {
                                Text(skill.source)
                                    .font(.system(size: 10))
                                    .padding(.horizontal, 5)
                                    .padding(.vertical, 2)
                                    .background(.quaternary)
                                    .clipShape(Capsule())
                            }
                            Spacer()
                            Text(skill.description)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }
                    }
                    .buttonStyle(.plain)
                }
            }
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
