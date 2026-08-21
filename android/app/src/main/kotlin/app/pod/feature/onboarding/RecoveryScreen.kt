package app.pod.feature.onboarding

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import app.pod.api.ApiException
import app.pod.api.generated.CopyKey
import app.pod.api.generated.PodTokens
import app.pod.api.generated.Recovery
import app.pod.ui.components.PodNotice
import app.pod.ui.components.PodPrimaryButton
import app.pod.ui.components.PodSecondaryButton
import app.pod.ui.theme.LocalCopywriter
import app.pod.ui.theme.LocalPodColors

/**
 * What someone sees when a pod they were in has wound down.
 *
 * Its own screen rather than a banner over the questions (docs/DESIGN.md, variant B). The
 * person most likely to quietly stop opening the app is exactly the person here, and a
 * pre-lowered answer under a banner says *we have adjusted you* where this asks.
 *
 * It never says why the pod ended. Everything true about that is a statement about
 * somebody not showing up, so it reads identically for the person who posted every week
 * and the person who never did (CLAUDE.md §2 rule 1).
 */
@Composable
fun RecoveryScreen(
    recovery: Recovery,
    working: Boolean,
    failure: ApiException?,
    onAcceptLowerIntensity: (Int) -> Unit,
    onKeepIntensity: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current

    /** The rung's phrase. The integer is the matching filter's business (ADR-010). */
    fun rung(value: Int) = copy("onboarding.intensity.$value")

    Column(
        verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.SECTION_GAP.dp),
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(
                horizontal = PodTokens.Spacing.SCREEN_GUTTER.dp,
                vertical = PodTokens.Spacing.XXXL.dp,
            ),
    ) {
        Column(verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.MD.dp)) {
            Text(
                copy(CopyKey.RECOVERY_POD_ENDED_TITLE),
                style = MaterialTheme.typography.titleLarge,
                color = colors.contentPrimary,
            )
            Text(
                copy(CopyKey.RECOVERY_POD_ENDED_BODY),
                style = MaterialTheme.typography.bodyLarge,
                color = colors.contentSecondary,
            )
        }

        val current = recovery.currentIntensity
        val suggested = recovery.suggestedIntensity
        if (recovery.offerLowerIntensity && current != null && suggested != null) {
            Column(verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.MD.dp)) {
                Text(
                    copy(CopyKey.RECOVERY_LOWER_BAR_TITLE),
                    style = MaterialTheme.typography.titleMedium,
                    color = colors.contentPrimary,
                )
                Text(
                    copy(
                        CopyKey.RECOVERY_LOWER_BAR_BODY,
                        mapOf("current" to rung(current), "suggested" to rung(suggested)),
                    ),
                    style = MaterialTheme.typography.bodyLarge,
                    color = colors.contentSecondary,
                )
                // Both buttons are equal weight. "Keep it as it is" is not a secondary
                // action — for plenty of people the pod simply did not work out.
                Column(verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.SM.dp)) {
                    PodPrimaryButton(
                        title = copy(
                            CopyKey.RECOVERY_LOWER_BAR_ACCEPT,
                            mapOf("suggested" to rung(suggested)),
                        ),
                        onClick = { onAcceptLowerIntensity(suggested) },
                        working = working,
                    )
                    PodSecondaryButton(
                        title = copy(CopyKey.RECOVERY_LOWER_BAR_KEEP),
                        onClick = onKeepIntensity,
                        enabled = !working,
                    )
                }
            }
        } else {
            // Already on the smallest rung, so there is nothing smaller to offer. Asking
            // anyway would be a question with one answer.
            PodPrimaryButton(
                title = copy(CopyKey.RECOVERY_FIND_ANOTHER),
                onClick = onKeepIntensity,
                working = working,
            )
        }

        if (recovery.offerAlternatePath) {
            AlternatePathCard()
        }

        if (failure != null) {
            PodNotice(failure = failure)
        }
    }
}

/**
 * Below the offer and never alone on screen: the message is "here is another way", never
 * "the group thing is not for you" (docs/DESIGN.md, CLAUDE.md §6).
 */
@Composable
private fun AlternatePathCard(modifier: Modifier = Modifier) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current

    OutlinedCard(modifier = modifier.fillMaxWidth()) {
        Column(
            verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.SM.dp),
            modifier = Modifier.padding(PodTokens.Spacing.LG.dp),
        ) {
            Text(
                copy(CopyKey.RECOVERY_ALTERNATE_TITLE),
                style = MaterialTheme.typography.labelLarge,
                color = colors.contentPrimary,
            )
            Text(
                copy(CopyKey.RECOVERY_ALTERNATE_BODY),
                style = MaterialTheme.typography.bodyLarge,
                color = colors.contentSecondary,
            )
            // Not a button: it does not open anything yet, and a control that does
            // nothing is worse than a sentence that says so.
            Text(
                copy(CopyKey.RECOVERY_ALTERNATE_NOT_YET),
                style = MaterialTheme.typography.bodySmall,
                color = colors.contentTertiary,
            )
        }
    }
}
