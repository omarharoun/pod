package app.pod.api

import app.pod.api.generated.MemberWeekState
import app.pod.api.generated.ProofKind
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

class WeekRepositoryTest {
    private val nowMillis = 1_800_000_000_000L
    private val jsonHeaders = headersOf(HttpHeaders.ContentType, "application/json")
    private val podId = "b2fa324b-3d7c-4d3e-a22d-4a3b58121280"
    private val weekId = "55555555-5555-4555-8555-555555555555"
    private val mine = "8b1f2a7c-1111-4111-8111-111111111111"
    private val theirs = "9b1f2a7c-1111-4111-8111-111111111111"

    /** A week that closes three days from `nowMillis`. */
    private fun weekJson(
        mineState: String = "open",
        theirsState: String = "open",
        actionText: String = "One small thing towards it this week.",
        actionIsDefault: Boolean = true,
        actionEditable: Boolean = true,
        podCompleted: Boolean = false,
    ) = """
        {"id":"$weekId","pod_id":"$podId","index":1,
         "action_text":"$actionText","action_is_default":$actionIsDefault,
         "action_editable":$actionEditable,
         "opens_at":"2027-01-11T00:00:00Z","closes_at":"2027-01-18T00:00:00Z",
         "members":[
           {"member_id":"$mine","state":"$mineState"},
           {"member_id":"$theirs","state":"$theirsState"}
         ],
         "pod_completed":$podCompleted}
    """.trimIndent()

    private class Reply(val status: HttpStatusCode, val body: String)

    private fun repository(vararg replies: Reply): Pair<WeekRepository, MutableList<Pair<String, String>>> {
        val recorded = mutableListOf<Pair<String, String>>()
        val queue = replies.toMutableList()
        val engine = MockEngine { request ->
            recorded += request.method.value to request.url.encodedPath
            val reply = queue.removeFirstOrNull() ?: error("no scripted reply for ${request.url}")
            respond(reply.body, reply.status, jsonHeaders)
        }
        val tokens = InMemoryTokenStore(StoredSession("access-1", "refresh-1", nowMillis + 900_000, mine))
        val client = PodApiClient("https://api.test.invalid", tokens, engine) { nowMillis }
        return WeekRepository(client, podId) { nowMillis } to recorded
    }

    @Test
    fun `load reads the current week`() = runTest {
        val (repository, recorded) = repository(Reply(HttpStatusCode.OK, weekJson()))
        repository.load()

        assertEquals(listOf("GET" to "/v1/pods/$podId/weeks/current"), recorded)
        val week = requireNotNull(repository.week)
        assertEquals(1, week.index)
        assertTrue(week.actionIsDefault)
        assertEquals(2, week.members.size)
    }

    @Test
    fun `pod progress counts the pod, not the person`() = runTest {
        val (repository, _) = repository(Reply(HttpStatusCode.OK, weekJson(mineState = "posted")))
        repository.load()
        assertEquals(1 to 2, requireNotNull(repository.week).podProgress())
    }

    @Test
    fun `you can post while the week is open and you have not`() = runTest {
        val (repository, _) = repository(Reply(HttpStatusCode.OK, weekJson()))
        repository.load()
        assertTrue(repository.canPost(mine))
    }

    @Test
    fun `you cannot post twice`() = runTest {
        val (repository, _) = repository(Reply(HttpStatusCode.OK, weekJson(mineState = "posted")))
        repository.load()
        assertFalse(repository.canPost(mine))
        assertEquals(MemberWeekState.POSTED, repository.memberWeek(mine)?.state)
    }

    @Test
    fun `you cannot post once the week has closed`() = runTest {
        val (repository, _) = repository(Reply(HttpStatusCode.OK, weekJson()))
        repository.load()
        val week = requireNotNull(repository.week)
        // A week that closed an hour ago.
        assertFalse(week.isOpen(week.closesAt.toEpochMilliseconds() + 3_600_000))
    }

    @Test
    fun `days remaining is never negative`() = runTest {
        val (repository, _) = repository(Reply(HttpStatusCode.OK, weekJson()))
        repository.load()
        val week = requireNotNull(repository.week)
        assertEquals(0, week.daysRemaining(week.closesAt.toEpochMilliseconds() + 86_400_000))
        assertTrue(week.daysRemaining(week.opensAt.toEpochMilliseconds()) > 0)
    }

    @Test
    fun `posting walks the slot, the upload and the commit in order`() = runTest {
        val (repository, recorded) = repository(
            Reply(HttpStatusCode.OK, weekJson()),
            Reply(
                HttpStatusCode.Created,
                """{"proof_id":"66666666-6666-4666-8666-666666666666",
                    "upload_url":"https://storage.test.invalid/blobs/proof/w/p?sig=x",
                    "upload_method":"PUT","upload_expires_at":"2027-01-11T00:10:00Z"}""",
            ),
            Reply(HttpStatusCode.Created, ""),
            Reply(
                HttpStatusCode.OK,
                """{"id":"66666666-6666-4666-8666-666666666666","week_id":"$weekId","member_id":"$mine",
                    "kind":"photo","created_at":"2027-01-11T09:00:00Z","expires_at":"2027-01-18T00:00:00Z"}""",
            ),
            Reply(HttpStatusCode.OK, weekJson(mineState = "posted")),
        )
        repository.load()

        repository.post(ProofKind.PHOTO, "image/jpeg", ByteArray(64))

        assertEquals(
            listOf(
                "GET" to "/v1/pods/$podId/weeks/current",
                "POST" to "/v1/pods/$podId/weeks/$weekId/proof",
                // Straight to storage, never through the API (ADR-008).
                "PUT" to "/blobs/proof/w/p",
                "POST" to "/v1/pods/$podId/weeks/$weekId/proof/66666666-6666-4666-8666-666666666666/commit",
                "GET" to "/v1/pods/$podId/weeks/current",
            ),
            recorded,
        )
        assertEquals(MemberWeekState.POSTED, repository.memberWeek(mine)?.state)
        assertNull(repository.failure.value)
    }

    @Test
    fun `naming the action replaces the default`() = runTest {
        val (repository, recorded) = repository(
            Reply(HttpStatusCode.OK, weekJson()),
            Reply(HttpStatusCode.OK, weekJson(actionText = "Clear one shelf", actionIsDefault = false)),
        )
        repository.load()

        repository.setAction("Clear one shelf")

        assertEquals("PUT" to "/v1/pods/$podId/weeks/$weekId/action", recorded[1])
        val week = requireNotNull(repository.week)
        assertEquals("Clear one shelf", week.actionText)
        assertFalse(week.actionIsDefault)
    }

    @Test
    fun `a locked action surfaces its own copy`() = runTest {
        val (repository, _) = repository(
            Reply(HttpStatusCode.OK, weekJson()),
            Reply(
                HttpStatusCode.Conflict,
                """{"code":"conflict","copy_key":"error.actionLocked.body","request_id":"r-1"}""",
            ),
        )
        repository.load()

        repository.setAction("Something else")

        val failure = repository.failure.value
        assertNotNull(failure)
        assertEquals("error.actionLocked.body", failure!!.bodyKey)
        // And the week on screen is untouched.
        assertEquals("One small thing towards it this week.", repository.week?.actionText)
    }

    // A proof that has already been swept is a normal outcome, not an error.
    @Test
    fun `a swept proof comes back as nothing`() = runTest {
        val (repository, _) = repository(
            Reply(HttpStatusCode.OK, weekJson(mineState = "posted")),
            Reply(HttpStatusCode.NotFound, """{"code":"not_found","copy_key":"error.server.body","request_id":"r-2"}"""),
        )
        repository.load()

        assertNull(repository.proofBytes("66666666-6666-4666-8666-666666666666"))
    }

    // A failed refresh leaves what is on screen alone.
    @Test
    fun `a failed refresh does not blank the week`() = runTest {
        val (repository, _) = repository(
            Reply(HttpStatusCode.OK, weekJson()),
            Reply(HttpStatusCode.InternalServerError, """{"code":"server_error","copy_key":"error.server.body","request_id":"r-3"}"""),
        )
        repository.load()

        repository.refresh()

        assertNotNull("the week vanished on a failed refresh", repository.week)
        assertNotNull(repository.failure.value)
    }
}
