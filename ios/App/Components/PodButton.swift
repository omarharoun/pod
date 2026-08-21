import SwiftUI
import PodAPI

/// The primary action on a screen. There is only ever one.
struct PodPrimaryButton: View {
    let title: String
    var isEnabled = true
    var isWorking = false
    let action: () -> Void

    @Environment(\.palette) private var palette

    var body: some View {
        Button(action: action) {
            ZStack {
                Text(title)
                    .podText(PodTokens.TypeScale.bodyStrong)
                    .opacity(isWorking ? 0 : 1)
                if isWorking {
                    ProgressView()
                        .tint(palette(.contentOnAccent))
                }
            }
            .frame(maxWidth: .infinity, minHeight: PodTokens.Size.primaryButtonHeight)
            .foregroundStyle(palette(.contentOnAccent))
            .background(palette(.accentPrimary), in: .rect(cornerRadius: PodTokens.Radius.md))
        }
        .buttonStyle(.plain)
        .disabled(!isEnabled || isWorking)
        .opacity(isEnabled ? 1 : PodTokens.Opacity.disabled)
        .accessibilityLabel(title)
        .accessibilityAddTraits(.isButton)
    }
}

#Preview("Primary button") {
    VStack(spacing: PodTokens.Spacing.lg) {
        PodPrimaryButton(title: "Send code") {}
        PodPrimaryButton(title: "Send code", isEnabled: false) {}
        PodPrimaryButton(title: "Send code", isWorking: true) {}
    }
    .padding(PodTokens.Spacing.screenGutter)
    .environment(\.palette, PodPalette(.light))
}
