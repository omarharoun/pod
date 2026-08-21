package app.pod.feature.week

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import app.pod.api.WeekPhase
import app.pod.api.daysRemaining
import app.pod.api.displayNameFor
import app.pod.api.generated.CopyKey
import app.pod.api.generated.MemberWeek
import app.pod.api.generated.MemberWeekState
import app.pod.api.generated.Pod
import app.pod.api.generated.PodTokens
import app.pod.api.generated.Week
import app.pod.api.isOpen
import app.pod.api.podProgress
import app.pod.ui.components.PodNotice
import app.pod.ui.components.PodPrimaryButton
import app.pod.ui.theme.LocalCopywriter
import app.pod.ui.theme.LocalPodColors

/**
 * Requests POST_NOTIFICATIONS on the versions that need it, once per composition.
 *
 * A refusal is a normal outcome and produces nothing: the week loop does not depend on
 * push, and someone who declined notifications must not be nagged about it.
 */
@Composable
private fun AskForNotificationsOnce() {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return
    val context = LocalContext.current
    val ask = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { }
    LaunchedEffect(Unit) {
        val granted = ContextCompat.checkSelfPermission(
            context,
            Manifest.permission.POST_NOTIFICATIONS,
        ) == PackageManager.PERMISSION_GRANTED
        if (!granted) {
            ask.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }
}

/** The week. This is the product (CLAUDE.md §7). */
@Composable
fun WeekScreen(
    pod: Pod,
    onSignOut: () -> Unit,
    modifier: Modifier = Modifier,
    viewModel: WeekViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current
    var capturing by remember { mutableStateOf(false) }

    // Asked here, not at launch: this is the first moment "can we let you know when your
    // pod does something" has a subject, which is the same moment iOS asks.
    //
    // Declaring POST_NOTIFICATIONS in the manifest is not asking for it. On API 33+ it is
    // denied until requested, so every notification the backend sent was being dropped on
    // the floor while the manifest comment said it was asked for when a pod forms.
    AskForNotificationsOnce()

    LaunchedEffect(pod.id) { viewModel.start(pod.id) }
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { viewModel.refresh() }

    Column(
        verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.XL.dp),
        modifier = modifier
            .fillMaxSize()
            .background(colors.canvas)
            .verticalScroll(rememberScrollState())
            .padding(
                horizontal = PodTokens.Spacing.SCREEN_GUTTER.dp,
                vertical = PodTokens.Spacing.XXL.dp,
            ),
    ) {
        when (val phase = state.phase) {
            is WeekPhase.Loading ->
                Box(
                    Modifier
                        .fillMaxWidth()
                        .heightIn(min = PodTokens.Size.RING_DIAMETER.dp),
                    contentAlignment = Alignment.Center,
                ) {
                    CircularProgressIndicator(
                        color = MaterialTheme.colorScheme.primary,
                        modifier = Modifier.clearAndSetSemantics {
                            contentDescription = copy(CopyKey.A11Y_LOADING)
                        },
                    )
                }

            is WeekPhase.Ready -> WeekContent(
                pod = pod,
                week = phase.week,
                savingAction = state.savingAction,
                posting = state.posting,
                canPost = viewModel.canPost(myId(pod)),
                hasPosted = viewModel.memberState(myId(pod))?.state == MemberWeekState.POSTED,
                onSetAction = viewModel::setAction,
                onPost = { capturing = true },
            )
        }

        state.failure?.let { PodNotice(failure = it, onRetry = viewModel::refresh) }

        TextButton(onClick = onSignOut, modifier = Modifier.fillMaxWidth()) {
            Text(copy(CopyKey.AUTH_SIGN_OUT_ACTION), style = MaterialTheme.typography.bodySmall)
        }
    }

    if (capturing) {
        ProofCaptureSheet(
            onCapture = { captured ->
                capturing = false
                viewModel.post(captured.kind, captured.contentType, captured.bytes, captured.durationMs)
            },
            onDismiss = { capturing = false },
        )
    }
}

private fun myId(pod: Pod): String = pod.members.firstOrNull { it.isYou }?.id.orEmpty()

@Composable
private fun WeekContent(
    pod: Pod,
    week: Week,
    savingAction: Boolean,
    posting: Boolean,
    canPost: Boolean,
    hasPosted: Boolean,
    onSetAction: (String) -> Unit,
    onPost: () -> Unit,
) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current
    val now = System.currentTimeMillis()
    var editing by remember(week.id) { mutableStateOf(false) }
    var draft by remember(week.id) { mutableStateOf("") }

    // The "you're matched" moment survives as a banner on the first week rather than as
    // its own screen, so nobody lands somewhere with nothing to do.
    if (week.index == 1 && week.podProgress().first == 0) {
        Column(
            verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.XS.dp),
            modifier = Modifier
                .fillMaxWidth()
                .background(MaterialTheme.colorScheme.primaryContainer, RoundedCornerShape(PodTokens.Radius.MD.dp))
                .padding(PodTokens.Spacing.LG.dp),
        ) {
            Text(copy(CopyKey.POD_NEWLY_FORMED_TITLE), style = MaterialTheme.typography.titleMedium, color = colors.contentPrimary)
            Text(copy(CopyKey.POD_NEWLY_FORMED_BODY), style = MaterialTheme.typography.bodySmall, color = colors.contentSecondary)
        }
    }

    // ---- the one thing ------------------------------------------------------
    Column(verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.SM.dp)) {
        Text(copy(CopyKey.WEEK_TITLE), style = MaterialTheme.typography.labelSmall, color = colors.contentTertiary)

        if (editing) {
            OutlinedTextField(
                value = draft,
                onValueChange = { draft = it },
                placeholder = { Text(copy(CopyKey.WEEK_ACTION_PLACEHOLDER)) },
                minLines = 1,
                maxLines = 3,
                modifier = Modifier.fillMaxWidth(),
            )
            Row(horizontalArrangement = Arrangement.spacedBy(PodTokens.Spacing.LG.dp)) {
                PodPrimaryButton(
                    title = copy(CopyKey.WEEK_ACTION_SAVE),
                    onClick = { onSetAction(draft); editing = false },
                    enabled = draft.trim().length >= 3,
                    working = savingAction,
                    modifier = Modifier.weight(1f),
                )
                TextButton(onClick = { editing = false }) { Text(copy(CopyKey.COMMON_CANCEL)) }
            }
        } else {
            Text(week.actionText, style = MaterialTheme.typography.titleLarge, color = colors.contentPrimary)
            if (week.actionIsDefault && week.actionEditable) {
                // An invitation, never a nag.
                Text(copy(CopyKey.WEEK_ACTION_NAME_PROMPT), style = MaterialTheme.typography.bodySmall, color = colors.contentSecondary)
            }
            if (week.actionEditable) {
                TextButton(onClick = {
                    draft = if (week.actionIsDefault) "" else week.actionText
                    editing = true
                }) {
                    Text(
                        copy(if (week.actionIsDefault) CopyKey.WEEK_ACTION_NAME_CTA else CopyKey.WEEK_ACTION_EDIT_CTA),
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
            } else if (week.isOpen(now)) {
                Text(copy(CopyKey.WEEK_ACTION_LOCKED_NOTE), style = MaterialTheme.typography.labelSmall, color = colors.contentTertiary)
            }
        }

        Text(
            closesLine(week, now),
            style = MaterialTheme.typography.bodySmall,
            color = colors.contentSecondary,
        )
    }

    // ---- the ring -----------------------------------------------------------
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.MD.dp),
        modifier = Modifier.fillMaxWidth(),
    ) {
        PodRing(members = week.members, streakWeeks = pod.streakWeeks)
        if (week.podCompleted) {
            Text(copy(CopyKey.WEEK_POD_COMPLETED), style = MaterialTheme.typography.labelLarge, color = colors.statePosted)
        } else if (!week.isOpen(now)) {
            // Nothing went wrong, so nothing reports that it did.
            Text(
                copy(CopyKey.STATE_POD_RESTARTING_NOTE),
                style = MaterialTheme.typography.bodySmall,
                color = colors.contentSecondary,
                textAlign = TextAlign.Center,
            )
        }
    }

    // ---- who is in ----------------------------------------------------------
    Column(verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.SM.dp)) {
        for (member in week.members) {
            MemberRow(member = member, pod = pod)
        }
    }

    // ---- posting ------------------------------------------------------------
    if (canPost) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.SM.dp),
            modifier = Modifier.fillMaxWidth(),
        ) {
            PodPrimaryButton(title = copy(CopyKey.WEEK_POST_CTA), onClick = onPost, working = posting)
            Text(
                copy(CopyKey.WEEK_POST_EPHEMERAL_NOTE),
                style = MaterialTheme.typography.labelSmall,
                color = colors.contentTertiary,
                textAlign = TextAlign.Center,
            )
        }
    } else if (hasPosted) {
        Text(
            copy(CopyKey.STATE_MEMBER_YOU_POSTED_DETAIL),
            style = MaterialTheme.typography.labelLarge,
            color = colors.statePosted,
            modifier = Modifier.fillMaxWidth(),
            textAlign = TextAlign.Center,
        )
    }
}

@Composable
private fun MemberRow(member: MemberWeek, pod: Pod) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current
    val isYou = pod.members.firstOrNull { it.isYou }?.id == member.memberId
    val name = if (isYou) copy(CopyKey.POD_YOU) else pod.displayNameFor(member.memberId)

    val (dot, short, description) = when (member.state) {
        MemberWeekState.POSTED -> Triple(
            colors.statePostedSubtle,
            copy(CopyKey.STATE_MEMBER_POSTED_SHORT),
            copy(CopyKey.A11Y_MEMBER_POSTED, mapOf("name" to name)),
        )
        MemberWeekState.RESTING -> Triple(
            colors.stateRestingSubtle,
            copy(CopyKey.STATE_MEMBER_RESTING_SHORT),
            copy(CopyKey.A11Y_MEMBER_RESTING, mapOf("name" to name)),
        )
        else -> Triple(
            colors.stateOpenSubtle,
            copy(CopyKey.STATE_MEMBER_OPEN_SHORT),
            copy(CopyKey.A11Y_MEMBER_OPEN, mapOf("name" to name)),
        )
    }

    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(PodTokens.Spacing.MD.dp),
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = PodTokens.Size.MIN_TOUCH_TARGET.dp)
            .background(MaterialTheme.colorScheme.surface, RoundedCornerShape(PodTokens.Radius.MD.dp))
            .padding(horizontal = PodTokens.Spacing.LG.dp)
            .clearAndSetSemantics { contentDescription = description },
    ) {
        Box(Modifier.size(PodTokens.Spacing.MD.dp).background(dot, CircleShape))
        Text(name, style = MaterialTheme.typography.bodyLarge, color = colors.contentPrimary)
        Spacer(Modifier.weight(1f))
        Text(short, style = MaterialTheme.typography.bodySmall, color = colors.contentSecondary)
    }
}

@Composable
private fun closesLine(week: Week, nowMillis: Long): String {
    val copy = LocalCopywriter.current
    if (!week.isOpen(nowMillis)) return copy(CopyKey.WEEK_CLOSED)
    val days = week.daysRemaining(nowMillis)
    return if (days == 0) copy(CopyKey.WEEK_CLOSES_TODAY) else copy.plural(CopyKey.WEEK_CLOSES_IN, days)
}
