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

    /// The lane being looked at, and its own onboarding.
    private var slots: SlotsController { environment.slots }
    private var onboarding: OnboardingController { environment.onboarding(for: slots.current) }

    var body: some View {
        VStack(spacing: 0) {
            // Only once there is more than one thing. A switcher over a single lane is a
            // control that does nothing, on the screen that should be simplest.
            if slots.openLanes.count > 1 {
                LaneSwitcher(slots: slots)
            }
            content
        }
        .task {
            await slots.load()
            await onboarding.load()
        }
        // A lane change is a different thing entirely, so its onboarding starts fresh.
        .task(id: slots.current) { await onboarding.load() }
    }

    @ViewBuilder
    private var content: some View {
        switch onboarding.phase {
        case .loading:
            LoadingView()
        case .recovering(let recovery):
            RecoveryView(controller: onboarding, recovery: recovery)
        case .questions(let question):
            OnboardingFlowView(controller: onboarding, question: question)
        case .waiting(let ticket):
            WaitingView(controller: onboarding, ticket: ticket)
        case .matched(let pod):
            // A graduated pod has no week. Showing the week screen would show a proof
            // grid whose proof was deleted and an action nobody can post against, and
            // would leave the choice somewhere the person has to go looking for it.
            if pod.state == .completed {
                GraduationView(
                    controller: environment.graduationController(for: pod),
                    slots: environment.slots
                )
            } else {
                WeekView(
                    controller: environment.weekController(podID: pod.id),
                    pod: pod,
                    session: environment.session
                )
            }
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

/// Which thing you are looking at.
///
/// Goals, never numbers. A lane's number is the matching filter's business exactly as an
/// intensity rung's is (ADR-010, ADR-026) — and "1 of 2" would turn two things somebody is
/// working on into a score.
private struct LaneSwitcher: View {
    let slots: SlotsController

    @Environment(\.palette) private var palette
    @Environment(\.copy) private var copy

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: PodTokens.Spacing.sm) {
                ForEach(slots.openLanes, id: \.slot) { lane in
                    let selected = lane.slot == slots.current
                    Button {
                        slots.show(slot: lane.slot)
                    } label: {
                        Text(lane.goalText ?? copy(.slotsEmpty))
                            .podText(PodTokens.TypeScale.caption)
                            .lineLimit(1)
                            .padding(.horizontal, PodTokens.Spacing.md)
                            .padding(.vertical, PodTokens.Spacing.sm)
                            .foregroundStyle(palette(selected ? .contentOnAccent : .contentSecondary))
                            .background(
                                palette(selected ? .accentPrimary : .surfaceSunken),
                                in: .rect(cornerRadius: PodTokens.Radius.pill)
                            )
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(copy(.a11yLaneSwitch, ["goal": lane.goalText ?? copy(.slotsEmpty)]))
                    .accessibilityAddTraits(selected ? [.isButton, .isSelected] : .isButton)
                }
            }
            .padding(.horizontal, PodTokens.Spacing.screenGutter)
            .padding(.vertical, PodTokens.Spacing.sm)
        }
        .background(palette(.surfaceCanvas))
    }
}
