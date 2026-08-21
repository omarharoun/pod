package app.pod.api

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
