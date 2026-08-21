import SwiftUI
import PodAPI

/// The one typed answer in onboarding.
///
/// It keeps a Continue button rather than advancing on its own: auto-advancing out of a
/// text field the moment someone pauses would be hostile, and this is the question people
/// think about longest (docs/DESIGN.md).
struct GoalQuestionView: View {
    let controller: OnboardingController

    @Environment(\.palette) private var palette
    @Environment(\.copy) private var copy
    @FocusState private var isFieldFocused: Bool

    private var canContinue: Bool {
        controller.draft.isAnswered(.goal)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.lg) {
            Text(copy(.onboardingIntentTitle))
                .podText(PodTokens.TypeScale.title)
                .foregroundStyle(palette(.contentPrimary))

            Text(copy(.onboardingIntentBody))
                .podText(PodTokens.TypeScale.body)
                .foregroundStyle(palette(.contentSecondary))

            VStack(alignment: .leading, spacing: PodTokens.Spacing.xs) {
                Text(copy(.onboardingGoalLabel))
                    .podText(PodTokens.TypeScale.micro)
                    .foregroundStyle(palette(.contentTertiary))

                TextField(copy(.onboardingGoalPlaceholder), text: goalBinding, axis: .vertical)
                    .podText(PodTokens.TypeScale.body)
                    .foregroundStyle(palette(.contentPrimary))
                    .lineLimit(2...4)
                    .submitLabel(.done)
                    .focused($isFieldFocused)
                    .padding(PodTokens.Spacing.lg)
                    .background(palette(.surfaceSunken), in: .rect(cornerRadius: PodTokens.Radius.md))

                Text(copy(.onboardingGoalHint))
                    .podText(PodTokens.TypeScale.micro)
                    .foregroundStyle(palette(.contentTertiary))
            }

            PodPrimaryButton(
                title: copy(.onboardingContinue),
                isEnabled: canContinue,
                isWorking: controller.isWorking
            ) {
                isFieldFocused = false
                controller.advance()
            }
        }
        .onAppear { isFieldFocused = true }
    }

    private var goalBinding: Binding<String> {
        Binding(
            get: { controller.draft.goalText },
            set: { controller.draft.goalText = $0 }
        )
    }
}

#Preview("Onboarding — goal") {
    GoalQuestionView(controller: PodEnvironment.preview().onboarding)
        .padding(PodTokens.Spacing.screenGutter)
        .environment(\.palette, PodPalette(.light))
}
