package app.pod.api

import app.pod.api.generated.CopyKey
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class CopywriterTest {
    private val copy = Copywriter.bundled

    /** Every placeholder any key might use, so this test is about resolution. */
    private val everyPlaceholder = mapOf(
        "email" to "sam@example.com",
        "minutes" to "10",
        "seconds" to "30",
        "count" to "3",
        "name" to "Ada",
        "code" to "482913",
    )

    // The generated constants and the packaged catalogue come from the same file. If they
    // ever disagree, every screen using the missing key renders blank.
    @Test
    fun `every generated key resolves`() {
        for (key in CopyKey.all) {
            assertTrue("missing copy for $key", copy.contains(key))
            assertTrue("$key resolved to nothing", copy(key, everyPlaceholder).isNotEmpty())
        }
    }

    @Test
    fun `interpolation fills every occurrence`() {
        assertEquals(
            "one and two and one",
            Copywriter.interpolate("{a} and {b} and {a}", mapOf("a" to "one", "b" to "two")),
        )
        assertEquals("nothing to fill", Copywriter.interpolate("nothing to fill", emptyMap()))
        assertEquals("unclosed {brace", Copywriter.interpolate("unclosed {brace", emptyMap()))
    }

    @Test
    fun `plural forms differ`() {
        assertEquals("1 week together", copy.plural(CopyKey.STATE_POD_WEEKS_TOGETHER, 1))
        assertEquals("7 weeks together", copy.plural(CopyKey.STATE_POD_WEEKS_TOGETHER, 7))
    }

    // CLAUDE.md §2 rule 1, checked on the client: tools/copylint guards the catalogue in
    // CI, and this guards the copy that actually ships inside the APK.
    @Test
    fun `no shaming vocabulary ships in the apk`() {
        val forbidden = listOf(
            "failed", "failure", "you missed", "streak broken", "don't let",
            "let your pod down", "lazy", "0%", "penalty", "leaderboard",
        )
        for (key in CopyKey.all) {
            val text = copy(key, everyPlaceholder).lowercase()
            for (word in forbidden) {
                assertFalse("$key contains \"$word\": $text", text.contains(word))
            }
            assertFalse("$key is shouting: $text", text.contains("!"))
        }
    }
}
