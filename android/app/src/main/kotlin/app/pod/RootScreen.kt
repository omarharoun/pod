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
import app.pod.feature.home.HomeScreen
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
                HomeScreen(user = session.user, onSignOut = viewModel::signOut)
        }
    }
}
