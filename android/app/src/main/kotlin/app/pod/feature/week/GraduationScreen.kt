package app.pod.feature.week

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import app.pod.api.generated.CopyKey
import app.pod.api.generated.Pod
import app.pod.api.generated.PodTokens
import app.pod.ui.components.PodNotice
import app.pod.ui.components.PodPrimaryButton
import app.pod.ui.components.PodSecondaryButton
import app.pod.ui.theme.LocalCopywriter
import app.pod.ui.theme.LocalPodColors
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import kotlinx.datetime.TimeZone
import kotlinx.datetime.toJavaInstant

/**
 * The pod reached the end of its cycle.
 *
 * Its own screen rather than a dialog over the week (docs/DESIGN.md, variant B). What
 * would be behind a dialog is a week that has ended — a proof grid whose proof was deleted,
 * an action nobody can post against — so dismissing it would land someone on a dead screen
 * with the choice somewhere they have to go looking for.
 *
 * This is the only moment in the product where celebration is the right register, and it is
 * still not a ranking. Nothing here is per-member (CLAUDE.md §2 rule 3).
 */
@Composable
fun GraduationScreen(
    pod: Pod,
    modifier: Modifier = Modifier,
    viewModel: GraduationViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current
    val configuration = LocalConfiguration.current

    LaunchedEffect(pod.id) { viewModel.start(pod) }
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { viewModel.refresh() }

    // The pod the view model holds, once it has one: it carries the answer that came back
    // and the argument does not.
    val current = state.pod ?: pod
    val cycleLength = current.cycle?.lengthWeeks ?: 0
    val deadline = remember(current.continuation?.decideBy, configuration) {
        current.continuation?.decideBy?.let {
            DateTimeFormatter
                .ofLocalizedDate(FormatStyle.MEDIUM)
                .withZone(ZoneId.of(TimeZone.currentSystemDefault().id))
                .format(it.toJavaInstant())
        }.orEmpty()
    }

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
                copy(CopyKey.GRADUATION_TITLE, mapOf("count" to cycleLength.toString())),
                style = MaterialTheme.typography.displaySmall,
                color = colors.contentPrimary,
            )
            Text(
                copy(CopyKey.GRADUATION_BODY),
                style = MaterialTheme.typography.bodyLarge,
                color = colors.contentSecondary,
            )
            // Weeks the pod completed, never streakWeeks. The streak is the trailing run
            // and is reset by any miss, so binding it here handed a five-of-six pod the
            // copy written for a pod that managed none.
            //
            // The fork still matters. A pod that completed one week of six finished the
            // cycle, and the screen may not imply otherwise.
            val everyoneIn = current.cycle?.weeksEveryoneWasIn ?: 0
            Text(
                if (everyoneIn > 0) {
                    copy(CopyKey.GRADUATION_STREAK, mapOf("count" to everyoneIn.toString()))
                } else {
                    copy(CopyKey.GRADUATION_STREAK_NONE)
                },
                style = MaterialTheme.typography.titleMedium,
                color = colors.statePosted,
            )
        }

        val answer = current.continuation?.youAreContinuing
        if (answer == null) {
            Choice(
                cycleLength = cycleLength,
                deadline = deadline,
                working = state.working,
                onAnswer = viewModel::answer,
            )
        } else {
            Answered(
                continuing = answer,
                continuingCount = current.continuation?.continuing ?: 0,
                deadline = deadline,
                working = state.working,
                onChange = { viewModel.answer(!answer) },
            )
        }

        // Below the choice and never above it. The offer belongs here because you just
        // finished six weeks of something, which is when starting another thing is a real
        // thought — not on a permanent "+" shown most often to the person who has not
        // managed their first thing yet (docs/DESIGN.md, variant B).
        if (state.canOpenAnotherLane) {
            SecondThing(working = state.working, onStart = viewModel::openAnotherLane)
        }

        (state.failure ?: state.slotsFailure)?.let { PodNotice(failure = it) }
    }
}

/**
 * Nothing about the free pod is locked, so the offer never says "unlock". It does not name
 * a price either — the button opens the platform sheet, where the real price is.
 */
@Composable
private fun SecondThing(working: Boolean, onStart: () -> Unit, modifier: Modifier = Modifier) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current

    OutlinedCard(modifier = modifier.fillMaxWidth()) {
        Column(
            verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.SM.dp),
            modifier = Modifier.padding(PodTokens.Spacing.LG.dp),
        ) {
            Text(
                copy(CopyKey.SLOTS_OFFER_TITLE),
                style = MaterialTheme.typography.titleSmall,
                color = colors.contentPrimary,
            )
            Text(
                copy(CopyKey.SLOTS_OFFER_BODY),
                style = MaterialTheme.typography.bodyLarge,
                color = colors.contentSecondary,
            )
            PodSecondaryButton(
                title = copy(CopyKey.SLOTS_OFFER_ACTION),
                onClick = onStart,
                enabled = !working,
            )
        }
    }
}

/**
 * Both buttons carry equal weight — a filled one and a tonal one, which is Material 3's
 * way of saying "equal weight, different emphasis". "Everyone decides for themselves"
 * pre-empts the thought the product most wants to avoid: *if I say no, am I letting them
 * down* (docs/DESIGN.md).
 */
@Composable
private fun Choice(
    cycleLength: Int,
    deadline: String,
    working: Boolean,
    onAnswer: (Boolean) -> Unit,
) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current

    Column(verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.MD.dp)) {
        Text(
            copy(CopyKey.GRADUATION_CHOICE_TITLE, mapOf("count" to cycleLength.toString())),
            style = MaterialTheme.typography.titleMedium,
            color = colors.contentPrimary,
        )
        Text(
            copy(CopyKey.GRADUATION_CHOICE_BODY),
            style = MaterialTheme.typography.bodyLarge,
            color = colors.contentSecondary,
        )
        Column(verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.SM.dp)) {
            PodPrimaryButton(
                title = copy(CopyKey.GRADUATION_CHOICE_YES),
                onClick = { onAnswer(true) },
                working = working,
            )
            PodSecondaryButton(
                title = copy(CopyKey.GRADUATION_CHOICE_NO),
                onClick = { onAnswer(false) },
                enabled = !working,
            )
        }
        DeadlineNote(deadline)
    }
}

/**
 * After answering. A count, never a list — "Alex is not continuing" is the sentence this
 * whole design exists to make impossible, which is why the API carries counts and there is
 * no field here that could hold a name (ADR-023).
 */
@Composable
private fun Answered(
    continuing: Boolean,
    continuingCount: Int,
    deadline: String,
    working: Boolean,
    onChange: () -> Unit,
) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current

    Column(verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.MD.dp)) {
        Text(
            copy(if (continuing) CopyKey.GRADUATION_WAITING_TITLE else CopyKey.GRADUATION_DECLINED_TITLE),
            style = MaterialTheme.typography.titleMedium,
            color = colors.contentPrimary,
        )
        Text(
            copy(if (continuing) CopyKey.GRADUATION_WAITING_BODY else CopyKey.GRADUATION_DECLINED_BODY),
            style = MaterialTheme.typography.bodyLarge,
            color = colors.contentSecondary,
        )
        if (continuing && continuingCount > 0) {
            Text(
                copy(CopyKey.GRADUATION_WAITING_COUNT, mapOf("count" to continuingCount.toString())),
                style = MaterialTheme.typography.bodySmall,
                color = colors.contentTertiary,
            )
        }
        PodSecondaryButton(
            title = copy(CopyKey.GRADUATION_CHANGE),
            onClick = onChange,
            enabled = !working,
        )
        DeadlineNote(deadline)
    }
}

/**
 * Stated, never counted down. A countdown here would be pressure, and pressure is what
 * this product is built not to apply.
 */
@Composable
private fun DeadlineNote(deadline: String) {
    if (deadline.isEmpty()) return
    val copy = LocalCopywriter.current
    Text(
        copy(CopyKey.GRADUATION_CHOICE_DEADLINE, mapOf("date" to deadline)),
        style = MaterialTheme.typography.bodySmall,
        color = LocalPodColors.current.contentTertiary,
    )
}
