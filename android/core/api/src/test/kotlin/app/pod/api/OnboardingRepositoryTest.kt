package app.pod.api

import app.pod.api.generated.CopyKey
import app.pod.api.generated.GoalCategory
import app.pod.api.generated.SelfReportReliability
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class OnboardingRepositoryTest {
    private val nowMillis = 1_800_000_000_000L
    private val jsonHeaders = headersOf(HttpHeaders.ContentType, "application/json")

    private val podJson = """
        {"id":"5c1f2a7c-1111-4111-8111-111111111111","state":"active","goal_category":"home",
         "timezone":"Europe/Lisbon","streak_weeks":0,
         "members":[
           {"id":"8b1f2a7c-1111-4111-8111-111111111111","display_name":"Sam","is_you":true,"joined_at":"2026-08-21T09:00:00Z"},
           {"id":"9b1f2a7c-1111-4111-8111-111111111111","display_name":"Alex","is_you":false,"joined_at":"2026-08-21T09:00:00Z"}
         ],
         "created_at":"2026-08-21T09:00:00Z"}
    """.trimIndent()

    private class Reply(val status: HttpStatusCode, val body: String)

    private fun repository(vararg replies: Reply): Pair<OnboardingRepository, MutableList<String>> {
        val recorded = mutableListOf<String>()
        val queue = replies.toMutableList()
        val engine = MockEngine { request ->
            recorded += request.url.encodedPath
            val reply = queue.removeFirstOrNull() ?: error("no scripted reply for ${request.url}")
            respond(reply.body, reply.status, jsonHeaders)
        }
        val tokens = InMemoryTokenStore(
            StoredSession("access-1", "refresh-1", nowMillis + 900_000, "user-1"),
        )
        val client = PodApiClient("https://api.test.invalid", tokens, engine) { nowMillis }
        return OnboardingRepository(client) to recorded
    }

    private fun completeDraft() = IntentDraft(
        goalText = "sort out the spare room",
        category = GoalCategory.HOME,
        intensity = 3,
        selfReport = SelfReportReliability.SOMETIMES,
    )

    @Test
    fun `draft completeness is per question`() {
        var draft = IntentDraft()
        assertFalse(draft.isAnswered(OnboardingQuestion.GOAL))
        draft = draft.copy(goalText = "  ab  ")
        assertFalse("whitespace should not count towards the minimum", draft.isAnswered(OnboardingQuestion.GOAL))
        draft = draft.copy(goalText = "sort out the spare room")
        assertTrue(draft.isAnswered(OnboardingQuestion.GOAL))
        assertFalse(draft.isAnswered(OnboardingQuestion.CATEGORY))
    }

    @Test
    fun `advance requires an answer`() = runTest {
        val (repository, recorded) = repository(
            Reply(HttpStatusCode.OK, """{"step":"intent","intent_complete":false,"in_pool":false}"""),
        )
        repository.load()
        recorded.clear()

        repository.advance()
        assertEquals(OnboardingPhase.Questions(OnboardingQuestion.GOAL), repository.phase.value)

        repository.updateDraft { it.copy(goalText = "sort out the spare room") }
        repository.advance()
        assertEquals(OnboardingPhase.Questions(OnboardingQuestion.CATEGORY), repository.phase.value)
    }

    @Test
    fun `back walks the questions and stops at the first`() = runTest {
        val (repository, _) = repository(
            Reply(HttpStatusCode.OK, """{"step":"intent","intent_complete":false,"in_pool":false}"""),
        )
        repository.load()
        repository.updateDraft { completeDraft() }
        repository.advance()
        repository.advance()
        assertEquals(OnboardingPhase.Questions(OnboardingQuestion.INTENSITY), repository.phase.value)

        repository.back()
        assertEquals(OnboardingPhase.Questions(OnboardingQuestion.CATEGORY), repository.phase.value)
        repository.back()
        assertEquals(OnboardingPhase.Questions(OnboardingQuestion.GOAL), repository.phase.value)
        repository.back()
        assertEquals(
            "back from the first question is a no-op",
            OnboardingPhase.Questions(OnboardingQuestion.GOAL),
            repository.phase.value,
        )
    }

    @Test
    fun `submit saves the intent then joins the queue`() = runTest {
        val (repository, recorded) = repository(
            Reply(HttpStatusCode.OK, """{"step":"intent","intent_complete":false,"in_pool":false}"""),
            Reply(
                HttpStatusCode.OK,
                """{"goal_text":"sort out the spare room","goal_category":"home","intensity":3,
                    "timezone":"Europe/Lisbon","updated_at":"2026-08-21T09:00:00Z"}""",
            ),
            Reply(HttpStatusCode.OK, """{"queued_at":"2026-08-21T09:00:00Z","next_window_at":"2026-08-21T10:00:00Z"}"""),
        )
        repository.load()
        repository.updateDraft { completeDraft() }

        repository.submit()

        assertEquals(listOf("/v1/onboarding/state", "/v1/onboarding/intent", "/v1/pool"), recorded)
        val phase = repository.phase.value
        assertTrue("expected the waiting phase, got $phase", phase is OnboardingPhase.Waiting)
        assertNull(repository.failure.value)
    }

    @Test
    fun `submit with an incomplete draft never reaches the network`() = runTest {
        val (repository, recorded) = repository()
        repository.updateDraft { it.copy(goalText = "sort out the spare room") }

        repository.submit()

        assertTrue(recorded.isEmpty())
    }

    @Test
    fun `load puts a matched person on their pod`() = runTest {
        val (repository, _) = repository(
            Reply(HttpStatusCode.OK, """{"step":"ready","intent_complete":true,"in_pool":false}"""),
            Reply(HttpStatusCode.OK, podJson),
        )
        repository.load()

        val phase = repository.phase.value
        assertTrue("expected the matched phase, got $phase", phase is OnboardingPhase.Matched)
        val pod = (phase as OnboardingPhase.Matched).pod
        assertEquals(2, pod.members.size)
        assertEquals(1, pod.members.count { it.isYou })
    }

    @Test
    fun `load puts a queued person on the waiting screen`() = runTest {
        val (repository, _) = repository(
            Reply(
                HttpStatusCode.OK,
                """{"step":"waiting_for_pod","intent_complete":true,"in_pool":true,
                    "pool_ticket":{"queued_at":"2026-08-21T09:00:00Z","next_window_at":"2026-08-21T10:00:00Z"}}""",
            ),
        )
        repository.load()
        assertTrue(repository.phase.value is OnboardingPhase.Waiting)
    }

    // No pod yet arrives as a 204, and must not look like a failure.
    @Test
    fun `checking for a pod that is not there yet stays on the waiting screen`() = runTest {
        val (repository, _) = repository(
            Reply(
                HttpStatusCode.OK,
                """{"step":"waiting_for_pod","intent_complete":true,"in_pool":true,
                    "pool_ticket":{"queued_at":"2026-08-21T09:00:00Z","next_window_at":"2026-08-21T10:00:00Z"}}""",
            ),
            Reply(HttpStatusCode.NoContent, ""),
        )
        repository.load()

        repository.checkForPod()

        assertTrue(repository.phase.value is OnboardingPhase.Waiting)
        assertNull("not being matched yet is a state, not an error", repository.failure.value)
    }

    @Test
    fun `checking for a pod moves on once the window has run`() = runTest {
        val (repository, _) = repository(
            Reply(
                HttpStatusCode.OK,
                """{"step":"waiting_for_pod","intent_complete":true,"in_pool":true,
                    "pool_ticket":{"queued_at":"2026-08-21T09:00:00Z","next_window_at":"2026-08-21T10:00:00Z"}}""",
            ),
            Reply(HttpStatusCode.OK, podJson),
        )
        repository.load()

        repository.checkForPod()

        assertTrue(repository.phase.value is OnboardingPhase.Matched)
    }

    @Test
    fun `a failure is surfaced rather than swallowed`() = runTest {
        val (repository, _) = repository(
            Reply(HttpStatusCode.OK, """{"step":"intent","intent_complete":false,"in_pool":false}"""),
            Reply(
                HttpStatusCode.BadRequest,
                """{"code":"bad_request","copy_key":"error.goalTooShort.body","request_id":"r-1"}""",
            ),
        )
        repository.load()
        repository.updateDraft { completeDraft() }

        repository.submit()

        val failure = repository.failure.value
        assertNotNull(failure)
        assertEquals(CopyKey.ERROR_GOAL_TOO_SHORT_BODY, failure!!.bodyKey)
    }
}
