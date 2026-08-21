package app.pod.feature.onboarding

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.weight
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.background
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.unit.dp
import app.pod.api.ApiException
import app.pod.api.IntentDraft
import app.pod.api.OnboardingQuestion
import app.pod.api.generated.CopyKey
import app.pod.api.generated.GoalCategory
import app.pod.api.generated.PodTokens
import app.pod.api.generated.SelfReportReliability
import app.pod.ui.components.PodChoice
import app.pod.ui.components.PodChoiceList
import app.pod.ui.components.PodNotice
import app.pod.ui.theme.LocalCopywriter
import app.pod.ui.theme.LocalPodColors

/** The four onboarding questions, one per screen (docs/DESIGN.md). */
@Composable
fun OnboardingScreen(
    question: OnboardingQuestion,
    draft: IntentDraft,
    working: Boolean,
    failure: ApiException?,
    onDraftChange: ((IntentDraft) -> IntentDraft) -> Unit,
    onAdvance: () -> Unit,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    // Predictive back walks the questions rather than leaving the flow.
    BackHandler(enabled = question != OnboardingQuestion.GOAL, onBack = onBack)

    Column(
        modifier = modifier
            .fillMaxSize()
            .imePadding()
            .padding(top = PodTokens.Spacing.XL.dp),
    ) {
        Header(question = question, onBack = onBack)

        Column(
            verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.LG.dp),
            modifier = Modifier
                .verticalScroll(rememberScrollState())
                .padding(
                    horizontal = PodTokens.Spacing.SCREEN_GUTTER.dp,
                    vertical = PodTokens.Spacing.XL.dp,
                ),
        ) {
            when (question) {
                OnboardingQuestion.GOAL -> GoalQuestion(
                    draft = draft,
                    working = working,
                    onDraftChange = onDraftChange,
                    onContinue = onAdvance,
                )

                OnboardingQuestion.CATEGORY -> ChoiceQuestion(
                    titleKey = CopyKey.ONBOARDING_CATEGORY_TITLE,
                    explanationKey = null,
                    choices = goalCategoryChoices(),
                    selected = draft.category?.wire,
                    onSelect = { wire ->
                        onDraftChange { it.copy(category = GoalCategory.fromWire(wire)) }
                        onAdvance()
                    },
                )

                OnboardingQuestion.INTENSITY -> ChoiceQuestion(
                    titleKey = CopyKey.ONBOARDING_INTENSITY_TITLE,
                    explanationKey = CopyKey.ONBOARDING_INTENSITY_BODY,
                    // Phrases only. Never the number, never a position — ADR-010.
                    choices = intensityChoices(),
                    selected = draft.intensity?.toString(),
                    onSelect = { rung ->
                        onDraftChange { it.copy(intensity = rung.toIntOrNull()) }
                        onAdvance()
                    },
                )

                OnboardingQuestion.RELIABILITY -> ChoiceQuestion(
                    titleKey = CopyKey.ONBOARDING_RELIABILITY_TITLE,
                    explanationKey = CopyKey.ONBOARDING_RELIABILITY_BODY,
                    choices = selfReportChoices(),
                    selected = draft.selfReport?.wire,
                    onSelect = { wire ->
                        onDraftChange { it.copy(selfReport = SelfReportReliability.fromWire(wire)) }
                        onAdvance()
                    },
                )
            }

            if (failure != null) {
                PodNotice(failure = failure)
            }
        }
    }
}

@Composable
private fun Header(question: OnboardingQuestion, onBack: () -> Unit) {
    val copy = LocalCopywriter.current
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = PodTokens.Spacing.MD.dp),
    ) {
        Box(Modifier.size(PodTokens.Size.MIN_TOUCH_TARGET.dp)) {
            if (question != OnboardingQuestion.GOAL) {
                IconButton(onClick = onBack) {
                    Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = copy(CopyKey.COMMON_BACK))
                }
            }
        }
        Spacer(Modifier.weight(1f))
        Progress(question)
        Spacer(Modifier.weight(1f))
        // Balances the back button so the dots stay centred.
        Spacer(Modifier.size(PodTokens.Size.MIN_TOUCH_TARGET.dp))
    }
}

@Composable
private fun Progress(question: OnboardingQuestion) {
    val colors = LocalPodColors.current
    Row(
        horizontalArrangement = Arrangement.spacedBy(PodTokens.Spacing.XS.dp),
        // The dots repeat what the questions already say; screen readers skip them.
        modifier = Modifier.clearAndSetSemantics { },
    ) {
        for (step in OnboardingQuestion.entries) {
            Box(
                Modifier
                    .size(PodTokens.Spacing.SM.dp)
                    .background(
                        if (step.ordinal <= question.ordinal) {
                            MaterialTheme.colorScheme.primary
                        } else {
                            colors.stateOpen
                        },
                        CircleShape,
                    ),
            )
        }
    }
}

@Composable
private fun ChoiceQuestion(
    titleKey: String,
    explanationKey: String?,
    choices: List<PodChoice>,
    selected: String?,
    onSelect: (String) -> Unit,
) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current

    Column(verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.LG.dp)) {
        Text(copy(titleKey), style = MaterialTheme.typography.titleLarge, color = colors.contentPrimary)
        if (explanationKey != null) {
            Text(copy(explanationKey), style = MaterialTheme.typography.bodyLarge, color = colors.contentSecondary)
        }
        PodChoiceList(choices = choices, selected = selected, onSelect = onSelect)
    }
}

@Composable
private fun goalCategoryChoices(): List<PodChoice> {
    val copy = LocalCopywriter.current
    return GoalCategory.entries
        .filter { it != GoalCategory.UNKNOWN }
        .map { PodChoice(it.wire, copy("onboarding.category.${it.wire}")) }
}

@Composable
private fun intensityChoices(): List<PodChoice> {
    val copy = LocalCopywriter.current
    return (1..5).map { PodChoice(it.toString(), copy("onboarding.intensity.$it")) }
}

@Composable
private fun selfReportChoices(): List<PodChoice> {
    val copy = LocalCopywriter.current
    return SelfReportReliability.entries
        .filter { it != SelfReportReliability.UNKNOWN }
        .map { PodChoice(it.wire, copy("onboarding.reliability.${it.wire}")) }
}
