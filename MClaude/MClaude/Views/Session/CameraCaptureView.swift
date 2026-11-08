import SwiftUI
import AVFoundation

/// Full-screen in-app camera for the composer's camera button (design-claude-uplift.md →
/// Composer → Camera button). Photo only. ✕ closes, Library hands off to the photo picker,
/// the shutter shows a Retake / Use Photo review.
struct CameraCaptureView: View {
    /// Called with the captured photo's encoded data (HEIC/JPEG) when the user taps Use Photo.
    let onPhoto: (Data) -> Void
    let onLibrary: () -> Void
    let onClose: () -> Void

    @State private var camera = CameraModel()

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            if let captured = camera.captured, let image = UIImage(data: captured) {
                Image(uiImage: image)
                    .resizable()
                    .scaledToFit()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .accessibilityLabel("Captured photo")
            } else {
                CameraPreview(session: camera.session)
                    .ignoresSafeArea()
                    .accessibilityHidden(true)
            }
            VStack {
                HStack {
                    if camera.captured == nil {
                        Button(action: onClose) {
                            Image(systemName: "xmark")
                                .font(.system(size: 18, weight: .semibold))
                                .frame(width: 44, height: 44)
                                .background(.black.opacity(0.35), in: Circle())
                        }
                        .accessibilityLabel("Close camera")
                    }
                    Spacer()
                }
                .padding(.horizontal, 16)
                .padding(.top, 8)
                Spacer()
                controls
                    .padding(.horizontal, 24)
                    .padding(.bottom, 20)
            }
            .foregroundStyle(.white)
        }
        .statusBarHidden()
        .onAppear { camera.start() }
        .onDisappear { camera.stop() }
    }

    @ViewBuilder
    private var controls: some View {
        if let captured = camera.captured {
            HStack {
                Button("Retake") { camera.captured = nil }
                Spacer()
                Button("Use Photo") { onPhoto(captured) }
                    .fontWeight(.semibold)
            }
            .font(.system(size: 17))
            .frame(height: 72)
        } else {
            HStack {
                Button(action: onLibrary) {
                    Image(systemName: "photo.on.rectangle")
                        .font(.system(size: 22))
                        .frame(width: 52, height: 52)
                        .background(.white.opacity(0.15), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
                }
                .accessibilityLabel("Choose from library")
                Spacer()
                Button { camera.capture() } label: {
                    ZStack {
                        Circle().stroke(.white, lineWidth: 4).frame(width: 72, height: 72)
                        Circle().fill(.white).frame(width: 60, height: 60)
                    }
                    .opacity(camera.isCapturing ? 0.5 : 1)
                }
                .disabled(camera.isCapturing || !camera.isRunning)
                .accessibilityLabel("Take photo")
                Spacer()
                Button { camera.flip() } label: {
                    Image(systemName: "arrow.triangle.2.circlepath.camera")
                        .font(.system(size: 22))
                        .frame(width: 52, height: 52)
                        .background(.white.opacity(0.15), in: Circle())
                }
                .accessibilityLabel("Switch camera")
                .opacity(camera.canFlip ? 1 : 0)
                .disabled(!camera.canFlip)
            }
        }
    }

    /// Whether tapping the camera button should open the camera, asking for access on first use.
    /// False on devices without a camera (the simulator) and when access is denied or restricted.
    @MainActor
    static func cameraUsable() async -> Bool {
        guard AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: .back) != nil
                || AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: .front) != nil
        else { return false }
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .authorized: return true
        case .notDetermined: return await AVCaptureDevice.requestAccess(for: .video)
        default: return false
        }
    }
}

// MARK: - Capture session

@Observable
@MainActor
final class CameraModel: NSObject {
    let session = AVCaptureSession()
    var captured: Data?
    var isCapturing = false
    var isRunning = false
    var canFlip = false

    private let output = AVCapturePhotoOutput()
    private let queue = DispatchQueue(label: "mclaude.camera")
    private var input: AVCaptureDeviceInput?
    private var rotation: AVCaptureDevice.RotationCoordinator?
    private var configured = false

    func start() {
        canFlip = AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: .back) != nil
            && AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: .front) != nil
        let session = session, output = output
        if !configured {
            configured = true
            queue.async {
                session.beginConfiguration()
                session.sessionPreset = .photo
                if session.canAddOutput(output) { session.addOutput(output) }
                session.commitConfiguration()
            }
            use(position: .back)  // queued after the output, on the same serial queue
        }
        queue.async { [weak self] in
            session.startRunning()
            let running = session.isRunning
            Task { @MainActor in self?.isRunning = running }
        }
    }

    func stop() {
        let session = session
        queue.async { session.stopRunning() }
        isRunning = false
    }

    func flip() {
        let next: AVCaptureDevice.Position = input?.device.position == .front ? .back : .front
        use(position: next)
    }

    private func use(position: AVCaptureDevice.Position) {
        guard let device = AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: position),
              let newInput = try? AVCaptureDeviceInput(device: device) else { return }
        let session = session, old = input
        input = newInput
        queue.async {
            session.beginConfiguration()
            if let old { session.removeInput(old) }
            if session.canAddInput(newInput) {
                session.addInput(newInput)
            } else if let old {
                session.addInput(old)
            }
            session.commitConfiguration()
        }
        // The UI is portrait-locked; this tracks how the phone is actually held so the photo is upright.
        rotation = AVCaptureDevice.RotationCoordinator(device: device, previewLayer: nil)
    }

    func capture() {
        guard !isCapturing else { return }
        isCapturing = true
        let settings = AVCapturePhotoSettings()
        settings.flashMode = .off
        let angle = rotation?.videoRotationAngleForHorizonLevelCapture ?? 90
        let output = output
        queue.async {
            if let connection = output.connection(with: .video), connection.isVideoRotationAngleSupported(angle) {
                connection.videoRotationAngle = angle
            }
            output.capturePhoto(with: settings, delegate: self)
        }
    }
}

extension CameraModel: AVCapturePhotoCaptureDelegate {
    nonisolated func photoOutput(_ output: AVCapturePhotoOutput, didFinishProcessingPhoto photo: AVCapturePhoto,
                                 error: Error?) {
        let data = error == nil ? photo.fileDataRepresentation() : nil
        Task { @MainActor in
            self.isCapturing = false
            if let data { self.captured = data }
        }
    }
}

// MARK: - Preview layer

private struct CameraPreview: UIViewRepresentable {
    let session: AVCaptureSession

    final class PreviewView: UIView {
        override class var layerClass: AnyClass { AVCaptureVideoPreviewLayer.self }
        var previewLayer: AVCaptureVideoPreviewLayer { layer as! AVCaptureVideoPreviewLayer }
    }

    func makeUIView(context: Context) -> PreviewView {
        let view = PreviewView()
        view.previewLayer.session = session
        view.previewLayer.videoGravity = .resizeAspectFill
        return view
    }

    func updateUIView(_ view: PreviewView, context: Context) {
        // Portrait UI: keep the preview at 90° (the connection appears once an input is attached).
        if let connection = view.previewLayer.connection, connection.isVideoRotationAngleSupported(90) {
            connection.videoRotationAngle = 90
        }
    }
}
