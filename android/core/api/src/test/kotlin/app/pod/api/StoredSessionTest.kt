package app.pod.api

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class StoredSessionTest {
    private val now = 1_800_000_000_000L

    private fun session(expiresAt: Long) =
        StoredSession("a", "r", expiresAt, "user-1")

    @Test
    fun `freshness leaves a margin`() {
        assertTrue(session(now + 120_000).accessTokenIsFresh(now))
        // Inside the margin the token counts as stale, so a request does not expire in
        // flight over a second of clock skew.
        assertFalse(session(now + 10_000).accessTokenIsFresh(now))
        assertFalse(session(now - 1).accessTokenIsFresh(now))
    }

    @Test
    fun `round trips through json`() {
        val original = session(now + 900_000)
        val encoded: String = Json.encodeToString(StoredSession.serializer(), original)
        assertEquals(original, Json.decodeFromString(StoredSession.serializer(), encoded))
    }
}
