package app.pod.api

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * Owns the sign-in flow and the signed-in user.
 *
 * Unidirectional: screens call in, and observe [state] and [failure] coming back
 * (CLAUDE.md §5). It lives in `:core:api` rather than the app module because the state
 * machine is not a UI concern and is tested without one.
 */
public class SessionRepository(
    private val client: PodApiClient,
    private val deviceName: String? = null,
) {
    private val _state = MutableStateFlow<SessionState>(SessionState.Restoring)
    public val state: StateFlow<SessionState> = _state.asStateFlow()

    private val _failure = MutableStateFlow<ApiException?>(null)
    public val failure: StateFlow<ApiException?> = _failure.asStateFlow()

    private val _isWorking = MutableStateFlow(false)
    public val isWorking: StateFlow<Boolean> = _isWorking.asStateFlow()

    private var challengeId: String? = null

    /** Restores a session from storage if there is one. Safe to call on every launch. */
    public suspend fun restore() {
        _state.value = SessionState.Restoring
        if (client.restoredSession() == null) {
            _state.value = SessionState.SignedOut
            return
        }
        try {
            _state.value = SessionState.SignedIn(client.currentUser())
        } catch (failure: ApiException) {
            // A restored session that cannot be confirmed right now is not a reason to
            // sign someone out — they may just be on a train.
            _failure.value = failure
            _state.value = SessionState.SignedOut
        }
    }

    public suspend fun sendCode(email: String) {
        perform {
            val challenge = client.startSignIn(email.trim())
            challengeId = challenge.challengeId
            _state.value = SessionState.AwaitingCode(
                email = email.trim(),
                expiresAtMillis = challenge.expiresAt.toEpochMilliseconds(),
                resendAfterSeconds = challenge.resendAfterSeconds,
            )
        }
    }

    /** Sends a fresh code for the address already on screen. */
    public suspend fun resendCode() {
        val current = _state.value as? SessionState.AwaitingCode ?: return
        sendCode(current.email)
    }

    public suspend fun submitCode(code: String) {
        val challenge = challengeId ?: return
        perform {
            val session = client.verifySignIn(challenge, code, deviceName)
            challengeId = null
            _state.value = SessionState.SignedIn(session.user)
        }
    }

    /** Returns to the email screen, abandoning the outstanding code. */
    public fun changeEmail() {
        challengeId = null
        _failure.value = null
        _state.value = SessionState.SignedOut
    }

    public suspend fun signOut() {
        client.signOut()
        challengeId = null
        _failure.value = null
        _state.value = SessionState.SignedOut
    }

    public fun dismissFailure() {
        _failure.value = null
    }

    private suspend fun perform(work: suspend () -> Unit) {
        _isWorking.value = true
        _failure.value = null
        try {
            work()
        } catch (failure: ApiException) {
            _failure.value = failure
        } finally {
            _isWorking.value = false
        }
    }
}
