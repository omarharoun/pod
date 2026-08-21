import SwiftUI
import PodAPI

/// The first screen. One field, one button, and out.
///
/// Variant B of two (see docs/DESIGN.md): the field is focused on appear and the keyboard
/// submits, so the whole screen is reachable without ever moving a thumb to the button.
struct SignInView: View {
    let session: SessionController

    @Environment(\.palette) private var palette
    @Environment(\.copy) private var copy
    @State private var email = ""
    @FocusState private var isFieldFocused: Bool

    private var canSubmit: Bool {
        // `isWorking` belongs here, not only on the button. The keyboard's Go key goes
        // through `.onSubmit`, which the button's disabled state does not reach, so a
        // second sign-in could be fired while the first was still in flight.
        !session.isWorking && email.contains("@") && !email.hasSuffix("@") && email.count > 3
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: PodTokens.Spacing.lg) {
                Text(copy(.authStartTitle))
                    .podText(PodTokens.TypeScale.title)
                    .foregroundStyle(palette(.contentPrimary))

                Text(copy(.authStartBody))
                    .podText(PodTokens.TypeScale.body)
                    .foregroundStyle(palette(.contentSecondary))

                field
                    .padding(.top, PodTokens.Spacing.sm)

                PodPrimaryButton(
                    title: copy(.authStartSubmit),
                    isEnabled: canSubmit,
                    isWorking: session.isWorking
                ) {
                    submit()
                }

                if let failure = session.failure {
                    PodNotice(error: failure) { submit() }
                }

                Text(copy(.authStartLegal))
                    .podText(PodTokens.TypeScale.micro)
                    .foregroundStyle(palette(.contentTertiary))
                    .padding(.top, PodTokens.Spacing.sm)
            }
            .padding(.horizontal, PodTokens.Spacing.screenGutter)
            .padding(.top, PodTokens.Spacing.xxxl)
        }
        .background(palette(.surfaceCanvas))
        .onAppear { isFieldFocused = true }
    }

    private var field: some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.xs) {
            Text(copy(.authStartEmailLabel))
                .podText(PodTokens.TypeScale.micro)
                .foregroundStyle(palette(.contentTertiary))

            TextField(copy(.authStartEmailPlaceholder), text: $email)
                .podText(PodTokens.TypeScale.body)
                .foregroundStyle(palette(.contentPrimary))
                .textContentType(.emailAddress)
                .keyboardType(.emailAddress)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .submitLabel(.go)
                .focused($isFieldFocused)
                .onSubmit(submit)
                .padding(PodTokens.Spacing.lg)
                .background(palette(.surfaceSunken), in: .rect(cornerRadius: PodTokens.Radius.md))
        }
    }

    private func submit() {
        guard canSubmit else { return }
        Task { await session.sendCode(to: email.trimmingCharacters(in: .whitespaces)) }
    }
}

#Preview("Sign in") {
    SignInView(session: PodEnvironment.preview().session)
        .environment(\.palette, PodPalette(.light))
}
