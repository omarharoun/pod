import SwiftUI
import PodAPI

/// Chooses the screen from the session state, and nothing else.
struct RootView: View {
    let environment: PodEnvironment

    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        content
            .environment(\.palette, PodPalette(colorScheme))
            .environment(\.copy, environment.copy)
            .animation(.easeOut(duration: PodTokens.Duration.standard), value: stateID)
            .task { await environment.session.restore() }
    }

    @ViewBuilder
    private var content: some View {
        switch environment.session.state {
        case .restoring:
            LoadingView()
        case .signedOut:
            SignInView(session: environment.session)
        case .awaitingCode(let email, let expiresAt, let resendAfter):
            VerifyCodeView(
                session: environment.session,
                email: email,
                expiresAt: expiresAt,
                resendAfter: resendAfter
            )
        case .signedIn:
            SignedInView(environment: environment)
        }
    }

    /// A coarse identity for the state, so the transition animates on a screen change and
    /// not on every countdown tick.
    private var stateID: Int {
        switch environment.session.state {
        case .restoring: 0
        case .signedOut: 1
        case .awaitingCode: 2
        case .signedIn: 3
        }
    }
}

/// Everything after sign-in: still answering, waiting for a window, or in a pod.
private struct SignedInView: View {
    let environment: PodEnvironment

    var body: some View {
        content
            .task { await environment.onboarding.load() }
    }

    @ViewBuilder
    private var content: some View {
        switch environment.onboarding.phase {
        case .loading:
            LoadingView()
        case .recovering(let recovery):
            RecoveryView(controller: environment.onboarding, recovery: recovery)
        case .questions(let question):
            OnboardingFlowView(controller: environment.onboarding, question: question)
        case .waiting(let ticket):
            WaitingView(controller: environment.onboarding, ticket: ticket)
        case .matched(let pod):
            WeekView(
                controller: environment.weekController(podID: pod.id),
                pod: pod,
                session: environment.session
            )
        }
    }
}

private struct LoadingView: View {
    @Environment(\.palette) private var palette
    @Environment(\.copy) private var copy

    var body: some View {
        ProgressView()
            .tint(palette(.accentPrimary))
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(palette(.surfaceCanvas))
            .accessibilityLabel(copy(.a11yLoading))
    }
}

#Preview("Root — signed out") {
    RootView(environment: .preview())
}
