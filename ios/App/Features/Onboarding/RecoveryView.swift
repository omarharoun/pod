import SwiftUI
import PodAPI

/// What someone sees when a pod they were in has wound down.
///
/// Its own beat rather than a banner over the questions (docs/DESIGN.md, Variant B). The
/// person most likely to quietly stop opening the app is exactly the person on this
/// screen, and a pre-lowered answer under a banner says *we have adjusted you* where this
/// asks.
///
/// It never says why the pod ended. Everything true about that is a statement about
/// somebody not showing up, so the screen reads identically for the person who posted
/// every week and the person who never did (CLAUDE.md §2 rule 1).
struct RecoveryView: View {
    let controller: OnboardingController
    let recovery: Recovery

    @Environment(\.palette) private var palette
    @Environment(\.copy) private var copy

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: PodTokens.Spacing.sectionGap) {
                ended

                if recovery.offerLowerIntensity,
                   let current = recovery.currentIntensity,
                   let suggested = recovery.suggestedIntensity {
                    lowerTheBar(current: current, suggested: suggested)
                } else {
                    findAnother
                }

                if recovery.offerAlternatePath {
                    alternatePath
                }

                if let failure = controller.failure {
                    PodNotice(error: failure)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, PodTokens.Spacing.screenGutter)
            .padding(.vertical, PodTokens.Spacing.xxxl)
        }
        .background(palette(.surfaceCanvas))
    }

    private var ended: some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.md) {
            Text(copy(.recoveryPodEndedTitle))
                .podText(PodTokens.TypeScale.title)
                .foregroundStyle(palette(.contentPrimary))

            Text(copy(.recoveryPodEndedBody))
                .podText(PodTokens.TypeScale.body)
                .foregroundStyle(palette(.contentSecondary))
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// Both buttons are equal weight. "Keep it as it is" is not a secondary action —
    /// for plenty of people the pod simply did not work out (docs/DESIGN.md).
    private func lowerTheBar(current: Int, suggested: Int) -> some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.md) {
            Text(copy(.recoveryLowerBarTitle))
                .podText(PodTokens.TypeScale.heading)
                .foregroundStyle(palette(.contentPrimary))

            Text(copy(.recoveryLowerBarBody, [
                "current": intensityPhrase(current),
                "suggested": intensityPhrase(suggested),
            ]))
            .podText(PodTokens.TypeScale.body)
            .foregroundStyle(palette(.contentSecondary))

            VStack(spacing: PodTokens.Spacing.sm) {
                PodPrimaryButton(
                    title: copy(.recoveryLowerBarAccept, ["suggested": intensityPhrase(suggested)]),
                    isWorking: controller.isWorking
                ) {
                    Task { await controller.acceptLowerIntensity(suggested) }
                }
                PodSecondaryButton(
                    title: copy(.recoveryLowerBarKeep),
                    isEnabled: !controller.isWorking
                ) {
                    Task { await controller.keepIntensity() }
                }
            }
            .padding(.top, PodTokens.Spacing.sm)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// Already on the smallest rung, so there is nothing smaller to offer. Asking anyway
    /// would be a question with one answer.
    private var findAnother: some View {
        PodPrimaryButton(title: copy(.recoveryFindAnother), isWorking: controller.isWorking) {
            Task { await controller.keepIntensity() }
        }
    }

    /// Below the offer and never alone on screen: the message is "here is another way",
    /// never "the group thing is not for you" (docs/DESIGN.md, CLAUDE.md §6).
    private var alternatePath: some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.sm) {
            Text(copy(.recoveryAlternateTitle))
                .podText(PodTokens.TypeScale.bodyStrong)
                .foregroundStyle(palette(.contentPrimary))

            Text(copy(.recoveryAlternateBody))
                .podText(PodTokens.TypeScale.body)
                .foregroundStyle(palette(.contentSecondary))

            // Not a button: it does not open anything yet, and a control that does
            // nothing is worse than a sentence that says so.
            Text(copy(.recoveryAlternateNotYet))
                .podText(PodTokens.TypeScale.caption)
                .foregroundStyle(palette(.contentTertiary))
                .padding(.top, PodTokens.Spacing.xs)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(PodTokens.Spacing.lg)
        .background(palette(.surfaceRaised), in: .rect(cornerRadius: PodTokens.Radius.md))
        .overlay(
            RoundedRectangle(cornerRadius: PodTokens.Radius.md)
                .stroke(palette(.borderHairline), lineWidth: 1)
        )
    }

    /// The rung's phrase. The integer is the matching filter's business and is never
    /// shown (ADR-010).
    private func intensityPhrase(_ rung: Int) -> String {
        copy(CopyKey(rawValue: "onboarding.intensity.\(rung)"))
    }
}

#Preview("Pod ended, offer a smaller week") {
    RecoveryView(
        controller: PodEnvironment.preview().onboarding,
        recovery: Recovery(
            offerLowerIntensity: true,
            currentIntensity: 3,
            suggestedIntensity: 2,
            offerAlternatePath: false
        )
    )
    .environment(\.palette, PodPalette(.light))
    .environment(\.copy, Copywriter.bundled)
}

#Preview("Third pod — the other way is offered too") {
    RecoveryView(
        controller: PodEnvironment.preview().onboarding,
        recovery: Recovery(
            offerLowerIntensity: true,
            currentIntensity: 2,
            suggestedIntensity: 1,
            offerAlternatePath: true
        )
    )
    .environment(\.palette, PodPalette(.dark))
    .environment(\.copy, Copywriter.bundled)
    .preferredColorScheme(.dark)
}

#Preview("Already on the smallest rung") {
    RecoveryView(
        controller: PodEnvironment.preview().onboarding,
        recovery: Recovery(offerLowerIntensity: false, offerAlternatePath: false)
    )
    .environment(\.palette, PodPalette(.light))
    .environment(\.copy, Copywriter.bundled)
}
