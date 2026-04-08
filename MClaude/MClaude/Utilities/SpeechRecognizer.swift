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

    func requestPermission() async -> Bool {
        let speechStatus = await withCheckedContinuation { cont in
            SFSpeechRecognizer.requestAuthorization { status in
                cont.resume(returning: status)
            }
        }
        guard speechStatus == .authorized else {
            error = "Speech recognition not authorized"
            return false
        }

        let audioSession = AVAudioSession.sharedInstance()
        do {
            try audioSession.setCategory(.record, mode: .measurement, options: .duckOthers)
            try audioSession.setActive(true, options: .notifyOthersOnDeactivation)
        } catch {
            self.error = "Audio session error"
            return false
        }
        permissionGranted = true
        return true
    }

    func startRecording() {
        guard let speechRecognizer, speechRecognizer.isAvailable else {
            error = "Speech recognizer unavailable"
            return
        }

        transcript = ""

        recognitionRequest = SFSpeechAudioBufferRecognitionRequest()
        guard let recognitionRequest else { return }
        recognitionRequest.shouldReportPartialResults = true
        recognitionRequest.addsPunctuation = true

        let inputNode = audioEngine.inputNode
        let recordingFormat = inputNode.outputFormat(forBus: 0)

        inputNode.installTap(onBus: 0, bufferSize: 1024, format: recordingFormat) { buffer, _ in
            recognitionRequest.append(buffer)
        }

        recognitionTask = speechRecognizer.recognitionTask(with: recognitionRequest) { [weak self] result, error in
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

        audioEngine.prepare()
        do {
            try audioEngine.start()
            isRecording = true
        } catch {
            self.error = "Could not start audio engine"
        }
    }

    func stopRecording() {
        guard isRecording else { return }
        audioEngine.stop()
        audioEngine.inputNode.removeTap(onBus: 0)
        recognitionRequest?.endAudio()
        recognitionRequest = nil
        isRecording = false
        isFinalizing = true
        // Let the task finish naturally — don't cancel it
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
