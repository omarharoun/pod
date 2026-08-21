package app.pod.api

import app.pod.api.generated.CopyKey
import app.pod.api.generated.ErrorCode
import app.pod.api.generated.Pod
import app.pod.api.generated.PodState
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.content.TextContent
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlinx.coroutines.test.runTest
import kotlinx.datetime.Instant
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class GraduationRepositoryTest {
    private val nowMillis = 1_800_000_000_000L
    private val jsonHeaders = headersOf(HttpHeaders.ContentType, "application/json")

    private val graduatedPodJson = """
        {"id":"5c1f2a7c-1111-4111-8111-111111111111","state":"completed","goal_category":"home",
         "timezone":"Europe/Lisbon","streak_weeks":6,
         "members":[
           {"id":"8b1f2a7c-1111-4111-8111-111111111111","display_name":"Sam","is_you":true,"joined_at":"2026-07-06T00:00:00Z"},
           {"id":"9b1f2a7c-1111-4111-8111-111111111111","display_name":"Alex","is_you":false,"joined_at":"2026-07-06T00:00:00Z"}
         ],
         "cycle":{"length_weeks":6,"weeks_completed":6,"weeks_everyone_was_in":6,"ends_at":"2026-08-17T00:00:00Z"},
         "continuation":{"continuing":1,"undecided":1,"you_answered":true,
                         "you_are_continuing":true,"decide_by":"2026-08-24T00:00:00Z"},
         "created_at":"2026-07-06T00:00:00Z","completed_at":"2026-08-17T00:00:00Z"}
    """.trimIndent()

    private class Call(val path: String, val method: String, val body: String)

    private fun repository(status: HttpStatusCode = HttpStatusCode.OK, reply: String = ""): Pair<GraduationRepository, MutableList<Call>> {
        val calls = mutableListOf<Call>()
        val engine = MockEngine { request ->
            calls += Call(
                request.url.encodedPath,
                request.method.value,
                (request.body as? TextContent)?.text.orEmpty(),
            )
            respond(reply.ifEmpty { graduatedPodJson }, status, jsonHeaders)
        }
        val tokens = InMemoryTokenStore(
            StoredSession("access-1", "refresh-1", nowMillis + 900_000, "user-1"),
        )
        val client = PodApiClient("https://api.test.invalid", tokens, engine) { nowMillis }
        val pod = Pod(
            id = "5c1f2a7c-1111-4111-8111-111111111111",
            state = PodState.COMPLETED,
            goalCategory = app.pod.api.generated.GoalCategory.HOME,
            timezone = "Europe/Lisbon",
            streakWeeks = 6,
            members = emptyList(),
            createdAt = Instant.fromEpochMilliseconds(nowMillis),
        )
        return GraduationRepository(client, pod) to calls
    }

    @Test
    fun `answering sends the choice and takes the server's pod back`() = runTest {
        val (repository, calls) = repository()

        repository.answer(continuing = true)

        assertNull(repository.failure.value)
        val post = calls.first { it.method == "POST" }
        assertEquals("/v1/pods/5c1f2a7c-1111-4111-8111-111111111111/continue", post.path)
        assertTrue(post.body, post.body.contains("\"continuing\":true"))
        assertEquals(true, repository.pod.value.continuation?.youAreContinuing)
    }

    /** Declining is an ordinary answer, not a failure. */
    @Test
    fun `declining is an ordinary answer`() = runTest {
        val (repository, calls) = repository()

        repository.answer(continuing = false)

        assertTrue(calls.first { it.method == "POST" }.body.contains("\"continuing\":false"))
        assertNull(repository.failure.value)
    }

    /** The choice moving on must surface as copy the screen can show, not a silent no-op. */
    @Test
    fun `answering after the choice closes surfaces the failure`() = runTest {
        val (repository, _) = repository(
            HttpStatusCode.Conflict,
            """{"code":"conflict","copy_key":"error.cycleChoiceClosed.body","request_id":"r1"}""",
        )

        repository.answer(continuing = true)

        val failure = repository.failure.value
        assertNotNull(failure)
        assertTrue(failure is ApiException.Problem)
        assertEquals(ErrorCode.CONFLICT, (failure as ApiException.Problem).error.code)
        assertTrue(Copywriter.bundled.contains(failure.error.copyKey))
    }

    /**
     * The client half of the assertion the backend makes against its own response bytes:
     * a decoded pod cannot express who declined.
     */
    @Test
    fun `the decoded pod cannot express who declined`() = runTest {
        val (repository, _) = repository()
        repository.answer(continuing = true)

        val pod = repository.pod.value
        val continuation = pod.continuation
        assertNotNull(continuation)
        assertEquals(1, continuation!!.continuing)
        assertEquals(1, continuation.undecided)
        assertTrue(continuation.youAnswered)

        // Every field on it, rendered: counts, and the caller's own answer. Nothing else.
        val rendered = continuation.toString()
        for (member in pod.members) {
            assertFalse(
                "the continuation carries a member id: $rendered",
                rendered.contains(member.id),
            )
        }
    }
}

/**
 * The graduation screen is the only place the product celebrates, and celebration is the
 * easiest place to accidentally rank people.
 */
class GraduationCopyTest {
    private val copy = Copywriter.bundled

    private val keys = listOf(
        CopyKey.GRADUATION_TITLE, CopyKey.GRADUATION_BODY,
        CopyKey.GRADUATION_STREAK, CopyKey.GRADUATION_STREAK_NONE,
        CopyKey.GRADUATION_CHOICE_TITLE, CopyKey.GRADUATION_CHOICE_BODY,
        CopyKey.GRADUATION_CHOICE_YES, CopyKey.GRADUATION_CHOICE_NO,
        CopyKey.GRADUATION_CHOICE_DEADLINE,
        CopyKey.GRADUATION_WAITING_TITLE, CopyKey.GRADUATION_WAITING_BODY,
        CopyKey.GRADUATION_WAITING_COUNT,
        CopyKey.GRADUATION_DECLINED_TITLE, CopyKey.GRADUATION_DECLINED_BODY,
        CopyKey.GRADUATION_CHANGE,
    )

    @Test
    fun `every string on the screen resolves`() {
        for (key in keys) {
            assertTrue("missing $key", copy.contains(key))
            assertTrue("empty $key", copy(key).isNotEmpty())
        }
    }

    /** Nothing here may be about an individual. The cycle belongs to the pod. */
    @Test
    fun `nothing celebrates or blames an individual`() {
        // Whole words, not substrings: "I'll stop here" contains "top", and the point
        // is the word, not the letters.
        val forbidden = listOf(
            "you posted", "your streak", "best", "most", "top", "winner", "leader",
            "everyone except", "only you", "everyone else", "let them down", "failed",
            "waiting on you", "up to you", "the last one",
        )
        for (key in keys) {
            val text = copy(key).lowercase()
            for (phrase in forbidden) {
                val word = Regex("\\b" + Regex.escape(phrase) + "\\b")
                assertFalse("$key says \"$phrase\": ${copy(key)}", word.containsMatchIn(text))
            }
        }
    }

    /**
     * The zero case is the one that matters. A pod that completed one week of six still
     * finished the cycle, and the copy may not imply otherwise.
     */
    @Test
    fun `the zero streak line does not report a failure`() {
        val text = copy(CopyKey.GRADUATION_STREAK_NONE).lowercase()
        for (phrase in listOf("0", "none", "no weeks", "never", "didn't get", "unfortunately")) {
            val word = Regex("\\b" + Regex.escape(phrase) + "\\b")
            assertFalse(
                "the zero-streak line reports a shortfall: ${copy(CopyKey.GRADUATION_STREAK_NONE)}",
                word.containsMatchIn(text),
            )
        }
    }

    /** The cycle length is interpolated, never spelled out — ADR-023's number can change. */
    @Test
    fun `the cycle length is interpolated not spelled out`() {
        for (key in listOf(CopyKey.GRADUATION_TITLE, CopyKey.GRADUATION_CHOICE_TITLE)) {
            val raw = copy.raw(key).orEmpty()
            assertTrue("$key does not interpolate the cycle length: $raw", raw.contains("{count}"))
            for (spelled in listOf("six", "eight", "four")) {
                assertFalse("$key spells the cycle length out: $raw", raw.lowercase().contains(spelled))
            }
        }
        val resolved = copy(CopyKey.GRADUATION_TITLE, mapOf("count" to "6"))
        assertTrue(resolved, resolved.contains("6"))
        assertFalse("unresolved placeholder: $resolved", resolved.contains("{"))
    }

    /** The deadline is stated, never counted down. A countdown here is pressure. */
    @Test
    fun `the deadline is a date not a countdown`() {
        val raw = copy.raw(CopyKey.GRADUATION_CHOICE_DEADLINE).orEmpty()
        assertTrue(raw, raw.contains("{date}"))
        for (unit in listOf("seconds", "minutes", "hours left", "remaining")) {
            assertFalse("the deadline is a countdown: $raw", raw.lowercase().contains(unit))
        }
    }
}
