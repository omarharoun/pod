package app.pod.api

import kotlinx.serialization.Serializable

/** The tokens for one signed-in device. */
@Serializable
public data class StoredSession(
    val accessToken: String,
    val refreshToken: String,
    /** Epoch milliseconds. */
    val accessTokenExpiresAtMillis: Long,
    val userId: String,
) {
    /**
     * Treats a nearly-expired token as expired, so a request does not die in flight over
     * a second's difference between this clock and the server's.
     */
    public fun accessTokenIsFresh(nowMillis: Long, marginMillis: Long = 30_000): Boolean =
        nowMillis + marginMillis < accessTokenExpiresAtMillis
}

/**
 * Where the tokens live.
 *
 * The Android implementation is `EncryptedTokenStore` in :app, because it needs a
 * `Context` and this module deliberately has no Android dependency.
 */
public interface TokenStore {
    public fun load(): StoredSession?
    public fun save(session: StoredSession)
    public fun clear()
}

/** Keeps tokens for the lifetime of the process. Tests and previews only. */
public class InMemoryTokenStore(private var session: StoredSession? = null) : TokenStore {
    @Synchronized override fun load(): StoredSession? = session
    @Synchronized override fun save(session: StoredSession) { this.session = session }
    @Synchronized override fun clear() { session = null }
}
