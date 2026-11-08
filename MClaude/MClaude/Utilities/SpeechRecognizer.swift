import Foundation
import Speech
import AVFoundation

@Observable
final class SpeechRecognizer {
    var transcript: String = ""
    var isRecording = false
    var error: String?
    var permissionGranted = false
    private(set) var isFinalizing = false

    private var audioEngine = AVAudioEngine()
    private var recognitionTask: SFSpeechRecognitionTask?
    private var recognitionRequest: SFSpeechAudioBufferRecognitionRequest?
    private let speechRecognizer = SFSpeechRecognizer(locale: Locale(identifier: "en-US"))
    private var finalizationContinuation: CheckedContinuation<String, Never>?
    private var tapInstalled = false

    func requestPermission() async -> Bool {
        let speechStatus = await withCheckedContinuation { cont in
            SFSpeechRecognizer.requestAuthorization { status in
                cont.resume(returning: status)
            }
        }
        guard speechStatus == .authorized else {
            error = "Speech recognition not allowed"
            return false
        }
        guard await AVAudioApplication.requestRecordPermission() else {
            error = "Microphone access not allowed"
            return false
        }
        guard activateSession() else { return false }
        permissionGranted = true
        return true
    }

    /// Sets the shared audio session up for recording and activates it. Run before every start:
    /// iOS can deactivate the session while the app is in the background, or another app can take it.
    @discardableResult
    private func activateSession() -> Bool {
        let audioSession = AVAudioSession.sharedInstance()
        do {
            try audioSession.setCategory(.record, mode: .measurement, options: .duckOthers)
            try audioSession.setActive(true, options: .notifyOthersOnDeactivation)
            return true
        } catch {
            self.error = "Microphone unavailable"
            return false
        }
    }

    /// Starts recording if it can; otherwise sets `error` and leaves nothing installed, so the
    /// next call starts cleanly. Never call twice for one press (see InputBarView's hold-to-talk).
    func startRecording() {
        error = nil
        guard !isRecording else { return }

        // Permission not decided yet: ask now; this press doesn't record, the next one does.
        if SFSpeechRecognizer.authorizationStatus() == .notDetermined
            || AVAudioApplication.shared.recordPermission == .undetermined {
            Task { @MainActor in _ = await requestPermission() }
            return
        }
        guard SFSpeechRecognizer.authorizationStatus() == .authorized else {
            error = "Speech recognition not allowed"
            return
        }
        guard AVAudioApplication.shared.recordPermission == .granted else {
            error = "Microphone access not allowed"
            return
        }
        guard let speechRecognizer, speechRecognizer.isAvailable else {
            error = "Speech recognizer unavailable"
            return
        }
        guard activateSession() else { return }
        permissionGranted = true

        // A tap left on the input bus makes installTap raise an Objective-C exception (crash).
        removeTapIfInstalled()

        let inputNode = audioEngine.inputNode
        let recordingFormat = inputNode.outputFormat(forBus: 0)
        // No usable input (e.g. during a call, or the session isn't really active): installTap
        // would raise on a 0 Hz / 0 channel format.
        guard recordingFormat.sampleRate > 0, recordingFormat.channelCount > 0 else {
            error = "Microphone unavailable"
            return
        }

        transcript = ""
        let request = SFSpeechAudioBufferRecognitionRequest()
        request.shouldReportPartialResults = true
        request.addsPunctuation = true
        recognitionRequest = request

        inputNode.installTap(onBus: 0, bufferSize: 1024, format: recordingFormat) { buffer, _ in
            request.append(buffer)
        }
        tapInstalled = true

        audioEngine.prepare()
        do {
            try audioEngine.start()
        } catch {
            removeTapIfInstalled()
            recognitionRequest = nil
            self.error = "Microphone unavailable"
            return
        }

        recognitionTask = speechRecognizer.recognitionTask(with: request) { [weak self] result, error in
            guard let self else { return }
            if let result {
                self.transcript = result.bestTranscription.formattedString
                if result.isFinal {
                    self.finalizationContinuation?.resume(returning: self.transcript)
                    self.finalizationContinuation = nil
                }
            }
            if error != nil {
                self.finalizationContinuation?.resume(returning: self.transcript)
                self.finalizationContinuation = nil
            }
        }
        isRecording = true
    }

    func stopRecording() {
        guard isRecording else { return }
        audioEngine.stop()
        removeTapIfInstalled()
        recognitionRequest?.endAudio()
        recognitionRequest = nil
        isRecording = false
        isFinalizing = true
        // Let the task finish naturally — don't cancel it
    }

    private func removeTapIfInstalled() {
        guard tapInstalled else { return }
        audioEngine.inputNode.removeTap(onBus: 0)
        tapInstalled = false
    }

    /// Waits for the recognizer to deliver a final transcript after stopRecording().
    /// Times out after 1.5s and returns whatever transcript exists.
    func waitForFinalTranscript() async -> String {
        let result = await withCheckedContinuation { (cont: CheckedContinuation<String, Never>) in
            if recognitionTask == nil || recognitionTask?.state == .completed {
                cont.resume(returning: transcript)
                return
            }
            finalizationContinuation = cont
            // Timeout: don't hang forever
            Task {
                try? await Task.sleep(for: .milliseconds(1500))
                if let pending = self.finalizationContinuation {
                    self.finalizationContinuation = nil
                    pending.resume(returning: self.transcript)
                }
            }
        }
        recognitionTask?.finish()
        recognitionTask = nil
        isFinalizing = false
        return result
    }
}
