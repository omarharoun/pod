package app.pod.api

import app.pod.api.generated.ApiError
import app.pod.api.generated.CopyKey
import app.pod.api.generated.ErrorCode

/**
 * Everything that can go wrong on the way to a response.
 *
 * The server never sends display text (ADR-003), so this carries a copy key instead; the
 * UI resolves it through [Copywriter].
 */
public sealed class ApiException(message: String, cause: Throwable? = null) : Exception(message, cause) {

    /** The request never got an answer: offline, timed out, DNS, TLS. */
    public class Offline(cause: Throwable) : ApiException("offline", cause)

    /** The server answered with a problem body. */
    public class Problem(public val error: ApiError, public val status: Int) :
        ApiException("${error.code} (${status})")

    /** The server answered with something this build cannot read. */
    public class Malformed(cause: Throwable? = null) : ApiException("malformed response", cause)

    /** A signed-in call was made with no session, or the session could not be renewed. */
    public data object NotSignedIn : ApiException("not signed in") {
        private fun readResolve(): Any = NotSignedIn
    }

    /** The key for the body text to show. */
    public val bodyKey: String
        get() = when (this) {
            is Offline -> CopyKey.ERROR_NETWORK_BODY
            is Malformed, is NotSignedIn -> CopyKey.ERROR_SERVER_BODY
            is Problem -> error.copyKey.takeIf { Copywriter.bundled.contains(it) }
                ?: CopyKey.ERROR_SERVER_BODY
        }

    /**
     * The matching title, chosen locally. The server does not decide how an error is
     * framed on a screen it cannot see.
     */
    public val titleKey: String
        get() = when (this) {
            is Offline -> CopyKey.ERROR_NETWORK_TITLE
            is Malformed, is NotSignedIn -> CopyKey.ERROR_SERVER_TITLE
            is Problem -> when (error.code) {
                ErrorCode.RATE_LIMITED -> CopyKey.ERROR_RATE_LIMITED_TITLE
                ErrorCode.UNAUTHORIZED, ErrorCode.SESSION_EXPIRED -> CopyKey.ERROR_SESSION_EXPIRED_TITLE
                else -> CopyKey.ERROR_SERVER_TITLE
            }
        }

    /** Interpolation values for [bodyKey]. */
    public val params: Map<String, String>
        get() = (this as? Problem)?.error.let { it?.params }.orEmpty()

    public val code: ErrorCode?
        get() = (this as? Problem)?.error?.code

    /**
     * Whether offering "try again" makes sense. A wrong code is not retryable in this
     * sense — the person changes the input, they do not repeat the request.
     */
    public val isRetryable: Boolean
        get() = when (this) {
            is Offline -> true
            is Problem -> status >= 500
            is Malformed, is NotSignedIn -> false
        }
}
