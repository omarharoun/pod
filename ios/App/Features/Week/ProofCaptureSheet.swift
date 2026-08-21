import AVFoundation
import SwiftUI
import PodAPI
#if canImport(UIKit)
import UIKit
#endif

/// What a completed capture produces.
struct CapturedProof: Sendable {
    let kind: ProofKind
    let contentType: String
    let data: Data
    let durationMs: Int?
}

/// Choose a photo or a voice note, capture it, hand it back.
///
/// Two equal choices rather than a camera that opens itself. A camera that opens itself is
/// a camera pointed at whatever is in front of you, and someone who wanted a voice note
/// chose it because they are not somewhere they want to photograph (docs/DESIGN.md).
struct ProofCaptureSheet: View {
    let onCapture: (CapturedProof) -> Void

    @Environment(\.dismiss) private var dismiss
    @Environment(\.palette) private var palette
    @Environment(\.copy) private var copy
    @State private var showingCamera = false
    @State private var recorder = VoiceRecorder()

    var body: some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.lg) {
            Text(copy(.weekPostCta))
                .podText(PodTokens.TypeScale.title)
                .foregroundStyle(palette(.contentPrimary))

            // The product's central promise, at the moment someone decides whether to
            // believe it.
            Text(copy(.weekPostEphemeralNote))
                .podText(PodTokens.TypeScale.body)
                .foregroundStyle(palette(.contentSecondary))

            if recorder.isRecording {
                recordingRow
            } else {
                choice(icon: "camera", label: copy(.weekPostPhoto)) { showingCamera = true }
                choice(icon: "mic", label: copy(.weekPostVoice)) { recorder.start() }
            }

            Spacer()

            Button(copy(.weekPostCancel)) {
                recorder.cancel()
                dismiss()
            }
            .podText(PodTokens.TypeScale.caption)
            .foregroundStyle(palette(.contentSecondary))
        }
        .padding(PodTokens.Spacing.screenGutter)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(palette(.surfaceCanvas))
        #if canImport(UIKit)
        .fullScreenCover(isPresented: $showingCamera) {
            CameraPicker { image in
                showingCamera = false
                guard let data = image.jpegData(compressionQuality: 0.8) else { return }
                onCapture(CapturedProof(kind: .photo, contentType: "image/jpeg", data: data, durationMs: nil))
                dismiss()
            }
            .ignoresSafeArea()
        }
        #endif
        .onChange(of: recorder.finished) { _, finished in
            guard let finished else { return }
            onCapture(CapturedProof(
                kind: .voice,
                contentType: "audio/m4a",
                data: finished.data,
                durationMs: finished.durationMs
            ))
            dismiss()
        }
    }

    private var recordingRow: some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.md) {
            HStack(spacing: PodTokens.Spacing.md) {
                Circle()
                    .fill(palette(.accentPrimary))
                    .frame(width: PodTokens.Spacing.md, height: PodTokens.Spacing.md)
                Text("\(recorder.secondsRemaining)")
                    .podText(PodTokens.TypeScale.display)
                    .foregroundStyle(palette(.contentPrimary))
                    .monospacedDigit()
            }
            PodPrimaryButton(title: copy(.commonDone)) { recorder.stop() }
        }
        .accessibilityElement(children: .combine)
    }

    private func choice(icon: String, label: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: PodTokens.Spacing.md) {
                Image(systemName: icon)
                    .foregroundStyle(palette(.accentPrimary))
                Text(label)
                    .podText(PodTokens.TypeScale.body)
                    .foregroundStyle(palette(.contentPrimary))
                Spacer()
            }
            .frame(maxWidth: .infinity, minHeight: PodTokens.Size.primaryButtonHeight, alignment: .leading)
            .padding(.horizontal, PodTokens.Spacing.lg)
            .background(palette(.surfaceSunken), in: .rect(cornerRadius: PodTokens.Radius.md))
        }
        .buttonStyle(.plain)
    }
}

/// Records up to ten seconds and stops itself.
///
/// The cap is enforced here rather than checked afterwards: telling someone their recording
/// was too long once they have made it is the kind of small waste this product is supposed
/// to be free of.
@MainActor
@Observable
final class VoiceRecorder {
    struct Finished: Sendable {
        let data: Data
        let durationMs: Int
    }

    private(set) var isRecording = false
    private(set) var secondsRemaining = 10
    private(set) var finished: Finished?

    private var recorder: AVAudioRecorder?
    private var url: URL?
    private var countdown: Task<Void, Never>?

    static let limit = 10

    func start() {
        guard !isRecording else { return }
        let file = FileManager.default.temporaryDirectory.appendingPathComponent("proof-\(UUID().uuidString).m4a")
        let settings: [String: Any] = [
            AVFormatIDKey: Int(kAudioFormatMPEG4AAC),
            AVSampleRateKey: 44_100,
            AVNumberOfChannelsKey: 1,
            AVEncoderAudioQualityKey: AVAudioQuality.medium.rawValue,
        ]

        do {
            #if canImport(UIKit)
            try AVAudioSession.sharedInstance().setCategory(.playAndRecord, mode: .default)
            try AVAudioSession.sharedInstance().setActive(true)
            #endif
            let recorder = try AVAudioRecorder(url: file, settings: settings)
            recorder.record(forDuration: TimeInterval(Self.limit))
            self.recorder = recorder
            self.url = file
            isRecording = true
            secondsRemaining = Self.limit
            countdown = Task { [weak self] in
                for _ in 0..<Self.limit {
                    try? await Task.sleep(for: .seconds(1))
                    guard let self, self.isRecording else { return }
                    self.secondsRemaining -= 1
                    if self.secondsRemaining <= 0 {
                        self.stop()
                        return
                    }
                }
            }
        } catch {
            // Nothing usable was recorded. The sheet stays where it is and the person can
            // choose the other option.
            isRecording = false
        }
    }

    func stop() {
        guard isRecording, let recorder, let url else { return }
        let elapsed = Self.limit - secondsRemaining
        recorder.stop()
        countdown?.cancel()
        isRecording = false
        self.recorder = nil

        defer { try? FileManager.default.removeItem(at: url) }
        guard let data = try? Data(contentsOf: url), !data.isEmpty else { return }
        finished = Finished(data: data, durationMs: max(elapsed, 1) * 1000)
    }

    func cancel() {
        countdown?.cancel()
        recorder?.stop()
        recorder = nil
        isRecording = false
        if let url { try? FileManager.default.removeItem(at: url) }
        url = nil
    }
}

#if canImport(UIKit)
/// The system camera. `UIImagePickerController` rather than a custom `AVCaptureSession`:
/// a bespoke camera for a ten-second photo is a lot of surface for no benefit, and the
/// system one is what people already know how to use.
struct CameraPicker: UIViewControllerRepresentable {
    let onImage: (UIImage) -> Void

    func makeUIViewController(context: Context) -> UIImagePickerController {
        let picker = UIImagePickerController()
        picker.sourceType = UIImagePickerController.isSourceTypeAvailable(.camera) ? .camera : .photoLibrary
        picker.delegate = context.coordinator
        return picker
    }

    func updateUIViewController(_ controller: UIImagePickerController, context: Context) {}

    func makeCoordinator() -> Coordinator { Coordinator(onImage: onImage) }

    final class Coordinator: NSObject, UIImagePickerControllerDelegate, UINavigationControllerDelegate {
        private let onImage: (UIImage) -> Void

        init(onImage: @escaping (UIImage) -> Void) {
            self.onImage = onImage
        }

        func imagePickerController(
            _ picker: UIImagePickerController,
            didFinishPickingMediaWithInfo info: [UIImagePickerController.InfoKey: Any]
        ) {
            if let image = info[.originalImage] as? UIImage {
                onImage(image)
            } else {
                picker.dismiss(animated: true)
            }
        }

        func imagePickerControllerDidCancel(_ picker: UIImagePickerController) {
            picker.dismiss(animated: true)
        }
    }
}
#endif
