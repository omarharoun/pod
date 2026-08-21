package app.pod

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import app.pod.api.OnboardingPhase
import app.pod.api.SessionState
import app.pod.api.generated.CopyKey
import app.pod.api.generated.PodState
import app.pod.api.generated.PodTokens
import app.pod.feature.auth.AuthViewModel
import app.pod.feature.auth.SignInScreen
import app.pod.feature.auth.VerifyCodeScreen
import app.pod.feature.onboarding.LanesUiState
import app.pod.feature.onboarding.OnboardingScreen
import app.pod.feature.onboarding.OnboardingUiState
import app.pod.feature.onboarding.OnboardingViewModel
import app.pod.feature.onboarding.RecoveryScreen
import app.pod.feature.onboarding.WaitingScreen
import app.pod.feature.week.GraduationScreen
import app.pod.feature.week.WeekScreen
import app.pod.ui.theme.LocalCopywriter
import app.pod.ui.theme.LocalPodColors

/** Chooses the screen from the session state, and nothing else. */
@Composable
fun RootScreen(viewModel: AuthViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current

    LaunchedEffect(Unit) { viewModel.restore() }

    AnimatedContent(
        targetState = state.session,
        // Keyed by the case, not the value: the code screen's countdown changes the value
        // every second and must not re-run the transition.
        contentKey = { it::class },
        transitionSpec = {
            val duration = PodTokens.DurationMs.STANDARD
            fadeIn(tween(duration)) togetherWith fadeOut(tween(duration))
        },
        label = "session",
        modifier = Modifier
            .fillMaxSize()
            .background(colors.canvas),
    ) { session ->
        when (session) {
            is SessionState.Restoring ->
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator(
                        color = MaterialTheme.colorScheme.primary,
                        modifier = Modifier.semantics { contentDescription = copy(CopyKey.A11Y_LOADING) },
                    )
                }

            is SessionState.SignedOut ->
                SignInScreen(
                    working = state.working,
                    failure = state.failure,
                    onSubmit = viewModel::sendCode,
                )

            is SessionState.AwaitingCode ->
                VerifyCodeScreen(
                    email = session.email,
                    expiresAtMillis = session.expiresAtMillis,
                    resendAfterSeconds = session.resendAfterSeconds,
                    working = state.working,
                    failure = state.failure,
                    onSubmit = viewModel::submitCode,
                    onResend = viewModel::resendCode,
                    onChangeEmail = viewModel::changeEmail,
                )

            is SessionState.SignedIn ->
                SignedInScreen(onSignOut = viewModel::signOut)
        }
    }
}

/** Everything after sign-in: still answering, waiting for a window, or in a pod. */
@Composable
private fun SignedInScreen(
    onSignOut: () -> Unit,
    viewModel: OnboardingViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val lanes by viewModel.lanes.collectAsStateWithLifecycle()

    LaunchedEffect(Unit) { viewModel.load() }

    Column {
        // Only once there is more than one thing. A switcher over a single lane is a
        // control that does nothing, on the screen that should be simplest.
        if (lanes.open.size > 1) {
            LaneSwitcher(lanes = lanes, onShow = viewModel::showLane)
        }
        LanePhase(phase = state.phase, state = state, viewModel = viewModel, onSignOut = onSignOut)
    }
}

/**
 * Which thing you are looking at.
 *
 * Goals, never numbers. A lane's number is the matching filter's business exactly as an
 * intensity rung's is (ADR-010, ADR-026) — and "1 of 2" would turn two things somebody is
 * working on into a score.
 */
@Composable
private fun LaneSwitcher(lanes: LanesUiState, onShow: (Int) -> Unit, modifier: Modifier = Modifier) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current

    Row(
        horizontalArrangement = Arrangement.spacedBy(PodTokens.Spacing.SM.dp),
        modifier = modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState())
            .background(colors.canvas)
            .padding(
                horizontal = PodTokens.Spacing.SCREEN_GUTTER.dp,
                vertical = PodTokens.Spacing.SM.dp,
            ),
    ) {
        for (lane in lanes.open) {
            val goal = lane.goalText ?: copy(CopyKey.SLOTS_EMPTY)
            FilterChip(
                selected = lane.slot == lanes.current,
                onClick = { onShow(lane.slot) },
                label = { Text(goal, maxLines = 1) },
                modifier = Modifier.semantics {
                    contentDescription = copy(CopyKey.A11Y_LANE_SWITCH, mapOf("goal" to goal))
                },
            )
        }
    }
}

@Composable
private fun LanePhase(
    phase: OnboardingPhase,
    state: OnboardingUiState,
    viewModel: OnboardingViewModel,
    onSignOut: () -> Unit,
) {
    val copy = LocalCopywriter.current

    when (phase) {
        is OnboardingPhase.Loading ->
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                CircularProgressIndicator(
                    color = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.semantics { contentDescription = copy(CopyKey.A11Y_LOADING) },
                )
            }

        is OnboardingPhase.Recovering ->
            RecoveryScreen(
                recovery = phase.recovery,
                working = state.working,
                failure = state.failure,
                onAcceptLowerIntensity = viewModel::acceptLowerIntensity,
                onKeepIntensity = viewModel::keepIntensity,
            )

        is OnboardingPhase.Questions ->
            OnboardingScreen(
                question = phase.question,
                draft = state.draft,
                working = state.working,
                failure = state.failure,
                onDraftChange = viewModel::updateDraft,
                onAdvance = viewModel::advance,
                onBack = viewModel::back,
            )

        is OnboardingPhase.Waiting ->
            WaitingScreen(
                ticket = phase.ticket,
                failure = state.failure,
                onCheckForPod = viewModel::checkForPod,
                onLeaveQueue = viewModel::leaveQueue,
            )

        is OnboardingPhase.Matched ->
            // A graduated pod has no week. Showing the week screen would show a proof grid
            // whose proof was deleted and an action nobody can post against, and would
            // leave the choice somewhere the person has to go looking for it.
            if (phase.pod.state == PodState.COMPLETED) {
                GraduationScreen(pod = phase.pod)
            } else {
                WeekScreen(pod = phase.pod, onSignOut = onSignOut)
            }
    }
}
