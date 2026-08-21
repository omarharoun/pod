package app.pod.api

import app.pod.api.generated.CopyKey
import app.pod.api.generated.GoalCategory
import app.pod.api.generated.SelfReportReliability
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The onboarding screens build copy keys from contract enum values — a category row looks
 * up `onboarding.category.<value>`. Adding a category to the spec without adding its copy
 * would produce a blank row, not a build failure, so it is checked here.
 */
class OnboardingCopyTest {
    private val copy = Copywriter.bundled

    @Test
    fun `every goal category has a label`() {
        for (category in GoalCategory.entries.filter { it != GoalCategory.UNKNOWN }) {
            val key = "onboarding.category.${category.wire}"
            assertTrue("no label for category ${category.wire}", copy.contains(key))
            assertTrue("empty label for category ${category.wire}", copy(key).isNotEmpty())
        }
    }

    @Test
    fun `every self report answer has a label`() {
        for (answer in SelfReportReliability.entries.filter { it != SelfReportReliability.UNKNOWN }) {
            assertTrue(
                "no label for self-report ${answer.wire}",
                copy.contains("onboarding.reliability.${answer.wire}"),
            )
        }
    }

    @Test
    fun `every intensity rung has a label`() {
        for (rung in 1..5) {
            assertTrue("no label for intensity rung $rung", copy.contains("onboarding.intensity.$rung"))
        }
    }

    // ADR-010: a person picks a phrase, never a number, and never sees where they sit on
    // a scale relative to anyone else.
    @Test
    fun `intensity labels carry no numbers or positions`() {
        for (rung in 1..5) {
            val label = copy("onboarding.intensity.$rung")
            assertFalse("rung $rung shows its number: $label", label.contains(rung.toString()))
            for (marker in listOf("of 5", "level", "/5", "tier")) {
                assertFalse("rung $rung leaks a position: $label", label.lowercase().contains(marker))
            }
        }
    }

    // The self-report screen is the most likely place in the product to make someone feel
    // judged before they have done anything.
    @Test
    fun `self report answers do not judge`() {
        for (answer in SelfReportReliability.entries.filter { it != SelfReportReliability.UNKNOWN }) {
            val label = copy("onboarding.reliability.${answer.wire}").lowercase()
            for (judgement in listOf("rarely", "never", "bad", "poor", "unreliable", "flaky")) {
                assertFalse("${answer.wire} reads as a verdict: $label", label.contains(judgement))
            }
        }
    }
}

/**
 * The recovery screen is the sharpest test of CLAUDE.md §2 rule 1: it is shown to someone
 * whose pod just ended, and every true statement about why is a statement about somebody
 * not showing up.
 */
class RecoveryCopyTest {
    private val copy = Copywriter.bundled

    private val keys = listOf(
        CopyKey.RECOVERY_POD_ENDED_TITLE,
        CopyKey.RECOVERY_POD_ENDED_BODY,
        CopyKey.RECOVERY_LOWER_BAR_TITLE,
        CopyKey.RECOVERY_LOWER_BAR_BODY,
        CopyKey.RECOVERY_LOWER_BAR_ACCEPT,
        CopyKey.RECOVERY_LOWER_BAR_KEEP,
        CopyKey.RECOVERY_FIND_ANOTHER,
        CopyKey.RECOVERY_ALTERNATE_TITLE,
        CopyKey.RECOVERY_ALTERNATE_BODY,
        CopyKey.RECOVERY_ALTERNATE_NOT_YET,
    )

    @Test
    fun `every string on the screen resolves`() {
        for (key in keys) {
            assertTrue("missing $key", copy.contains(key))
            assertTrue("empty $key", copy(key).isNotEmpty())
        }
    }

    /**
     * It may not say why the pod ended, and it may not count. "Again", "still" and
     * "unfortunately" all carry a history the copy is not allowed to have.
     */
    @Test
    fun `nothing on the screen assigns blame or counts`() {
        val forbidden = listOf(
            "again", "unfortunately", "failed", "fail", "missed", "inactive",
            "nobody", "no one", "didn't post", "gave up", "quit", "third", "second time",
        )
        for (key in keys) {
            val text = copy(key).lowercase()
            for (word in forbidden) {
                assertFalse("$key says \"$word\": ${copy(key)}", text.contains(word))
            }
        }
    }

    /**
     * Both rungs are named by phrase. Interpolating the raw integer would put someone on a
     * scale, which ADR-010 forbids.
     */
    @Test
    fun `the offer names both rungs by phrase`() {
        val body = copy(
            CopyKey.RECOVERY_LOWER_BAR_BODY,
            mapOf(
                "current" to copy("onboarding.intensity.3"),
                "suggested" to copy("onboarding.intensity.2"),
            ),
        )
        assertTrue(body, body.contains("An hour or two"))
        assertTrue(body, body.contains("Half an hour"))
        assertFalse("unresolved placeholder: $body", body.contains("{"))
    }

    @Test
    fun `the accept button names the smaller rung`() {
        val title = copy(
            CopyKey.RECOVERY_LOWER_BAR_ACCEPT,
            mapOf("suggested" to copy("onboarding.intensity.1")),
        )
        assertTrue(title, title.contains("A few minutes"))
        assertFalse("unresolved placeholder: $title", title.contains("{"))
    }

    /** The alternate path is offered as another way, never as a verdict about the person. */
    @Test
    fun `the alternate path is not a verdict`() {
        val text = (copy(CopyKey.RECOVERY_ALTERNATE_TITLE) + " " + copy(CopyKey.RECOVERY_ALTERNATE_BODY)).lowercase()
        for (phrase in listOf("not for you", "instead of", "isn't working", "better suited")) {
            assertFalse("the alternate path reads as a verdict: $text", text.contains(phrase))
        }
    }
}
