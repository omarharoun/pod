package app.pod.api

import app.pod.api.generated.User

/**
 * Where the person is in the sign-in flow.
 *
 * Three states and no more, because it is three screens and no more — a person should be
 * in the app inside fifteen seconds (CLAUDE.md §2 rule 7).
 */
public sealed interface SessionState {
    /** Nothing known yet: reading storage, or confirming a restored session. */
    public data object Restoring : SessionState

    /** No session. The email screen. */
    public data object SignedOut : SessionState

    /** A code is outstanding for [email]. */
    public data class AwaitingCode(
        val email: String,
        val expiresAtMillis: Long,
        val resendAfterSeconds: Int,
    ) : SessionState

    /** In. */
    public data class SignedIn(val user: User) : SessionState
}
