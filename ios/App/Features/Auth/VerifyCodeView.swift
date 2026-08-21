import SwiftUI
import PodAPI

/// The code screen. It submits itself the moment six digits are in — nobody should have
/// to reach for a button after typing a code they just read (CLAUDE.md §2 rule 7).
struct VerifyCodeView: View {
    let session: SessionController
    let email: String
    let expiresAt: Date
    let resendAfter: TimeInterval

    @Environment(\.palette) private var palette
    @Environment(\.copy) private var copy
    @State private var code = ""
    @State private var secondsUntilResend: Int
    @FocusState private var isFieldFocused: Bool

    init(session: SessionController, email: String, expiresAt: Date, resendAfter: TimeInterval) {
        self.session = session
        self.email = email
        self.expiresAt = expiresAt
        self.resendAfter = resendAfter
        _secondsUntilResend = State(initialValue: Int(resendAfter))
    }

    /// Rounded up, and never below one: "0 minutes" is not a thing to tell someone.
    private var minutesRemaining: Int {
        max(1, Int(ceil(expiresAt.timeIntervalSinceNow / 60)))
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: PodTokens.Spacing.lg) {
                Text(copy(.authVerifyTitle))
                    .podText(PodTokens.TypeScale.title)
                    .foregroundStyle(palette(.contentPrimary))

                Text(copy(.authVerifyBody, ["email": email, "minutes": String(minutesRemaining)]))
                    .podText(PodTokens.TypeScale.body)
                    .foregroundStyle(palette(.contentSecondary))

                codeField
                    .padding(.top, PodTokens.Spacing.sm)

                if let failure = session.failure {
                    PodNotice(error: failure)
                }

                HStack(spacing: PodTokens.Spacing.lg) {
                    resendButton
                    Spacer()
                    Button(copy(.authVerifyChangeEmail)) { session.changeEmail() }
                        .podText(PodTokens.TypeScale.caption)
                        .foregroundStyle(palette(.contentSecondary))
                }
                .padding(.top, PodTokens.Spacing.sm)
            }
            .padding(.horizontal, PodTokens.Spacing.screenGutter)
            .padding(.top, PodTokens.Spacing.xxxl)
        }
        .background(palette(.surfaceCanvas))
        .onAppear { isFieldFocused = true }
        .task(id: secondsUntilResend) {
            guard secondsUntilResend > 0 else { return }
            try? await Task.sleep(for: .seconds(1))
            guard !Task.isCancelled else { return }
            secondsUntilResend -= 1
        }
    }

    private var codeField: some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.xs) {
            Text(copy(.authVerifyCodeLabel))
                .podText(PodTokens.TypeScale.micro)
                .foregroundStyle(palette(.contentTertiary))

            TextField("", text: $code)
                .podText(PodTokens.TypeScale.display)
                .foregroundStyle(palette(.contentPrimary))
                .keyboardType(.numberPad)
                .textContentType(.oneTimeCode)
                .focused($isFieldFocused)
                .accessibilityLabel(copy(.a11yCodeField))
                .padding(PodTokens.Spacing.lg)
                .background(palette(.surfaceSunken), in: .rect(cornerRadius: PodTokens.Radius.md))
                .onChange(of: code) { _, newValue in
                    let digits = String(newValue.filter(\.isNumber).prefix(6))
                    if digits != newValue { code = digits }
                    if digits.count == 6, !session.isWorking {
                        Task { await session.submit(code: digits) }
                    }
                }
        }
    }

    @ViewBuilder
    private var resendButton: some View {
        if secondsUntilResend > 0 {
            Text(copy(.authVerifyResendCountdown, ["seconds": String(secondsUntilResend)]))
                .podText(PodTokens.TypeScale.caption)
                .foregroundStyle(palette(.contentTertiary))
        } else {
            Button(copy(.authVerifyResend)) {
                code = ""
                secondsUntilResend = Int(resendAfter)
                Task { await session.resendCode() }
            }
            .podText(PodTokens.TypeScale.caption)
            .foregroundStyle(palette(.accentPrimary))
        }
    }
}

#Preview("Verify code") {
    VerifyCodeView(
        session: PodEnvironment.preview().session,
        email: "sam@example.com",
        expiresAt: .now.addingTimeInterval(600),
        resendAfter: 30
    )
    .environment(\.palette, PodPalette(.light))
}
