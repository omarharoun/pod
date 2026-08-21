package app.pod.feature.onboarding

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.unit.dp
import app.pod.api.IntentDraft
import app.pod.api.OnboardingQuestion
import app.pod.api.generated.CopyKey
import app.pod.api.generated.PodTokens
import app.pod.ui.components.PodPrimaryButton
import app.pod.ui.theme.LocalCopywriter
import app.pod.ui.theme.LocalPodColors

/**
 * The one typed answer in onboarding.
 *
 * It keeps a Continue button rather than advancing on its own: auto-advancing out of a
 * text field the moment someone pauses would be hostile, and this is the question people
 * think about longest (docs/DESIGN.md).
 */
@Composable
fun GoalQuestion(
    draft: IntentDraft,
    working: Boolean,
    onDraftChange: ((IntentDraft) -> IntentDraft) -> Unit,
    onContinue: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current
    val focus = remember { FocusRequester() }

    LaunchedEffect(Unit) { focus.requestFocus() }

    Column(
        verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.LG.dp),
        modifier = modifier.fillMaxWidth(),
    ) {
        Text(
            copy(CopyKey.ONBOARDING_INTENT_TITLE),
            style = MaterialTheme.typography.titleLarge,
            color = colors.contentPrimary,
        )
        Text(
            copy(CopyKey.ONBOARDING_INTENT_BODY),
            style = MaterialTheme.typography.bodyLarge,
            color = colors.contentSecondary,
        )

        OutlinedTextField(
            value = draft.goalText,
            onValueChange = { text -> onDraftChange { it.copy(goalText = text) } },
            label = { Text(copy(CopyKey.ONBOARDING_GOAL_LABEL)) },
            placeholder = { Text(copy(CopyKey.ONBOARDING_GOAL_PLACEHOLDER)) },
            supportingText = { Text(copy(CopyKey.ONBOARDING_GOAL_HINT)) },
            minLines = 2,
            maxLines = 4,
            keyboardOptions = KeyboardOptions(
                capitalization = KeyboardCapitalization.Sentences,
                imeAction = ImeAction.Default,
            ),
            modifier = Modifier
                .fillMaxWidth()
                .focusRequester(focus),
        )

        PodPrimaryButton(
            title = copy(CopyKey.ONBOARDING_CONTINUE),
            onClick = onContinue,
            enabled = draft.isAnswered(OnboardingQuestion.GOAL),
            working = working,
        )
    }
}
