package app.pod

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.hilt.navigation.compose.hiltViewModel
import app.pod.api.SessionState
import app.pod.api.generated.CopyKey
import app.pod.api.generated.PodTokens
import app.pod.feature.auth.AuthViewModel
import app.pod.feature.auth.SignInScreen
import app.pod.feature.auth.VerifyCodeScreen
import app.pod.api.OnboardingPhase
import app.pod.feature.onboarding.OnboardingScreen
import app.pod.feature.onboarding.OnboardingViewModel
import app.pod.feature.onboarding.WaitingScreen
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
    val copy = LocalCopywriter.current

    LaunchedEffect(Unit) { viewModel.load() }

    when (val phase = state.phase) {
        is OnboardingPhase.Loading ->
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                CircularProgressIndicator(
                    color = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.semantics { contentDescription = copy(CopyKey.A11Y_LOADING) },
                )
            }

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
            WeekScreen(pod = phase.pod, onSignOut = onSignOut)
    }
}
