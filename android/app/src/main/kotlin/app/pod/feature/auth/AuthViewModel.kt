package app.pod.feature.auth

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.pod.api.ApiException
import app.pod.api.SessionRepository
import app.pod.api.SessionState
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Everything a sign-in screen needs, in one value. */
data class AuthUiState(
    val session: SessionState = SessionState.Restoring,
    val working: Boolean = false,
    val failure: ApiException? = null,
)

/**
 * Unidirectional: the screen sends events in, and observes [state] coming back
 * (CLAUDE.md §5). The state itself lives in [SessionRepository], which outlives this
 * ViewModel — a configuration change must not restart the sign-in flow.
 */
@HiltViewModel
class AuthViewModel @Inject constructor(
    private val sessions: SessionRepository,
) : ViewModel() {

    val state: StateFlow<AuthUiState> =
        combine(sessions.state, sessions.isWorking, sessions.failure) { session, working, failure ->
            AuthUiState(session, working, failure)
        }.stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000),
            initialValue = AuthUiState(),
        )

    fun restore() {
        viewModelScope.launch { sessions.restore() }
    }

    fun sendCode(email: String) {
        viewModelScope.launch { sessions.sendCode(email) }
    }

    fun resendCode() {
        viewModelScope.launch { sessions.resendCode() }
    }

    fun submitCode(code: String) {
        viewModelScope.launch { sessions.submitCode(code) }
    }

    fun changeEmail() = sessions.changeEmail()

    fun signOut() {
        viewModelScope.launch { sessions.signOut() }
    }
}
