import SwiftUI
import PodAPI

/// The four onboarding questions, one per screen (docs/DESIGN.md).
struct OnboardingFlowView: View {
    let controller: OnboardingController
    let question: OnboardingQuestion

    @Environment(\.palette) private var palette
    @Environment(\.copy) private var copy

    var body: some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.xl) {
            header

            ScrollView {
                VStack(alignment: .leading, spacing: PodTokens.Spacing.lg) {
                    questionView(for: question)

                    if let failure = controller.failure {
                        PodNotice(error: failure)
                    }
                }
                .padding(.horizontal, PodTokens.Spacing.screenGutter)
                .padding(.bottom, PodTokens.Spacing.xxl)
            }
        }
        .padding(.top, PodTokens.Spacing.xl)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(palette(.surfaceCanvas))
    }

    private var header: some View {
        HStack(spacing: PodTokens.Spacing.md) {
            if question != .goal {
                Button {
                    controller.back()
                } label: {
                    Image(systemName: "chevron.left")
                        .foregroundStyle(palette(.contentSecondary))
                        .frame(width: PodTokens.Size.minTouchTarget, height: PodTokens.Size.minTouchTarget)
                }
                .accessibilityLabel(copy(.commonBack))
            }
            Spacer()
            progress
            Spacer()
            // Balances the back button so the dots stay centred.
            Color.clear.frame(width: PodTokens.Size.minTouchTarget, height: 1)
        }
        .padding(.horizontal, PodTokens.Spacing.md)
    }

    private var progress: some View {
        HStack(spacing: PodTokens.Spacing.xs) {
            ForEach(OnboardingQuestion.allCases, id: \.rawValue) { step in
                Circle()
                    .fill(step.rawValue <= question.rawValue ? palette(.accentPrimary) : palette(.stateOpen))
                    .frame(width: PodTokens.Spacing.sm, height: PodTokens.Spacing.sm)
            }
        }
        .accessibilityHidden(true)
    }

    @ViewBuilder
    private func questionView(for question: OnboardingQuestion) -> some View {
        switch question {
        case .goal:
            GoalQuestionView(controller: controller)
        case .category:
            ChoiceQuestionView(
                title: copy(.onboardingCategoryTitle),
                explanation: nil,
                choices: GoalCategory.knownCases.map {
                    PodChoice(id: $0.rawValue, label: copy(CopyKey(rawValue: "onboarding.category.\($0.rawValue)")))
                },
                selection: Binding(
                    get: { controller.draft.category?.rawValue },
                    set: { controller.draft.category = $0.map(GoalCategory.init(rawValue:)) }
                ),
                onSelect: controller.advance
            )
        case .intensity:
            ChoiceQuestionView(
                title: copy(.onboardingIntensityTitle),
                explanation: copy(.onboardingIntensityBody),
                // Phrases only. Never the number, never a position — see ADR-010.
                choices: (1...5).map {
                    PodChoice(id: String($0), label: copy(CopyKey(rawValue: "onboarding.intensity.\($0)")))
                },
                selection: Binding(
                    get: { controller.draft.intensity.map(String.init) },
                    set: { controller.draft.intensity = $0.flatMap(Int.init) }
                ),
                onSelect: controller.advance
            )
        case .reliability:
            ChoiceQuestionView(
                title: copy(.onboardingReliabilityTitle),
                explanation: copy(.onboardingReliabilityBody),
                choices: SelfReportReliability.knownCases.map {
                    PodChoice(id: $0.rawValue, label: copy(CopyKey(rawValue: "onboarding.reliability.\($0.rawValue)")))
                },
                selection: Binding(
                    get: { controller.draft.selfReport?.rawValue },
                    set: { controller.draft.selfReport = $0.map(SelfReportReliability.init(rawValue:)) }
                ),
                onSelect: controller.advance
            )
        }
    }
}

/// Title, optional explanation, and a pick-one list that advances on selection.
private struct ChoiceQuestionView: View {
    let title: String
    let explanation: String?
    let choices: [PodChoice]
    @Binding var selection: String?
    let onSelect: () -> Void

    @Environment(\.palette) private var palette

    var body: some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.lg) {
            Text(title)
                .podText(PodTokens.TypeScale.title)
                .foregroundStyle(palette(.contentPrimary))

            if let explanation {
                Text(explanation)
                    .podText(PodTokens.TypeScale.body)
                    .foregroundStyle(palette(.contentSecondary))
            }

            PodChoiceList(choices: choices, selection: $selection, onSelect: onSelect)
        }
    }
}

#Preview("Onboarding — intensity") {
    OnboardingFlowView(
        controller: PodEnvironment.preview(onboarding: .questions(.intensity)).onboarding,
        question: .intensity
    )
    .environment(\.palette, PodPalette(.light))
}
