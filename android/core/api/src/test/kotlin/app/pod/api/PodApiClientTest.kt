package app.pod.api

import app.pod.api.generated.ErrorCode
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

class PodApiClientTest {
    private val jsonHeaders = headersOf(HttpHeaders.ContentType, "application/json")
    private val nowMillis = 1_800_000_000_000L

    private val sessionJson = """
        {
          "access_token": "access-1",
          "refresh_token": "refresh-1",
          "expires_in_seconds": 900,
          "token_type": "Bearer",
          "user": {
            "id": "8b1f2a7c-1111-4111-8111-111111111111",
            "email": "sam@example.com",
            "created_at": "2026-08-21T09:00:00Z",
            "onboarding_complete": false
          }
        }
    """.trimIndent()

    private val userJson = """
        {"id":"8b1f2a7c-1111-4111-8111-111111111111","email":"sam@example.com",
         "created_at":"2026-08-21T09:00:00Z","onboarding_complete":false}
    """.trimIndent()

    private class Reply(val status: HttpStatusCode, val body: String)

    private fun client(
        tokens: TokenStore = InMemoryTokenStore(),
        vararg replies: Reply,
    ): Pair<PodApiClient, MutableList<Pair<String, String?>>> {
        val recorded = mutableListOf<Pair<String, String?>>()
        val queue = replies.toMutableList()
        val engine = MockEngine { request ->
            recorded += request.url.encodedPath to request.headers[HttpHeaders.Authorization]
            val reply = queue.removeFirstOrNull() ?: error("no scripted reply for ${request.url}")
            respond(reply.body, reply.status, jsonHeaders)
        }
        return PodApiClient("https://api.test.invalid", tokens, engine) { nowMillis } to recorded
    }

    @Test
    fun `health decodes`() = runTest {
        val (client, _) = client(
            replies = arrayOf(Reply(HttpStatusCode.OK, """{"status":"ok","version":"t","checks":[{"name":"postgres","status":"ok","latency_ms":3}]}""")),
        )
        val health = client.health()
        assertEquals("ok", health.status)
        assertEquals("postgres", health.checks.first().name)
        assertEquals(3, health.checks.first().latencyMs)
    }

    @Test
    fun `health sends no authorization header`() = runTest {
        val (client, recorded) = client(
            replies = arrayOf(Reply(HttpStatusCode.OK, """{"status":"ok","version":"t","checks":[]}""")),
        )
        client.health()
        assertNull(recorded.single().second)
    }

    @Test
    fun `verify stores the session`() = runTest {
        val tokens = InMemoryTokenStore()
        val (client, _) = client(tokens, Reply(HttpStatusCode.OK, sessionJson))

        val session = client.verifySignIn("1a2b3c4d-1111-4111-8111-111111111111", "482913", "test")
        assertEquals("sam@example.com", session.user.email)

        val stored = requireNotNull(tokens.load())
        assertEquals("access-1", stored.accessToken)
        assertEquals("refresh-1", stored.refreshToken)
        assertEquals(nowMillis + 900_000, stored.accessTokenExpiresAtMillis)
    }

    @Test
    fun `authenticated call sends the bearer token`() = runTest {
        val tokens = InMemoryTokenStore(
            StoredSession("access-1", "refresh-1", nowMillis + 900_000, "user-1"),
        )
        val (client, recorded) = client(tokens, Reply(HttpStatusCode.OK, userJson))
        client.currentUser()
        assertEquals("Bearer access-1", recorded.single().second)
    }

    @Test
    fun `signed-out call never reaches the network`() = runTest {
        val (client, recorded) = client()
        try {
            client.currentUser()
            fail("expected NotSignedIn")
        } catch (expected: ApiException.NotSignedIn) {
            assertTrue(recorded.isEmpty())
        }
    }

    // An access token past its expiry is refreshed before the call, not after a 401.
    @Test
    fun `stale access token is refreshed first`() = runTest {
        val tokens = InMemoryTokenStore(
            StoredSession("expired", "refresh-1", nowMillis - 1, "user-1"),
        )
        val (client, recorded) = client(
            tokens,
            Reply(HttpStatusCode.OK, sessionJson),
            Reply(HttpStatusCode.OK, userJson),
        )
        client.currentUser()
        assertEquals(listOf("/v1/auth/refresh", "/v1/me"), recorded.map { it.first })
        assertEquals("access-1", tokens.load()?.accessToken)
    }

    // A refresh that is itself rejected means the chain is gone: clear and stop.
    @Test
    fun `rejected refresh clears the session`() = runTest {
        val tokens = InMemoryTokenStore(
            StoredSession("expired", "refresh-1", nowMillis - 1, "user-1"),
        )
        val (client, _) = client(
            tokens,
            Reply(HttpStatusCode.Unauthorized, """{"code":"unauthorized","copy_key":"error.sessionExpired.body","request_id":"r-1"}"""),
        )
        try {
            client.currentUser()
            fail("expected NotSignedIn")
        } catch (expected: ApiException.NotSignedIn) {
            assertNull("a dead chain must not be left in storage", tokens.load())
        }
    }

    @Test
    fun `problem body becomes a typed failure`() = runTest {
        val (client, _) = client(
            replies = arrayOf(
                Reply(HttpStatusCode.Unauthorized, """{"code":"code_incorrect","copy_key":"auth.verify.error.codeIncorrect","request_id":"r-2"}"""),
            ),
        )
        try {
            client.verifySignIn("1a2b3c4d-1111-4111-8111-111111111111", "000000", null)
            fail("expected a problem")
        } catch (failure: ApiException.Problem) {
            assertEquals(ErrorCode.CODE_INCORRECT, failure.code)
            assertEquals("auth.verify.error.codeIncorrect", failure.bodyKey)
            assertFalse("a wrong code is corrected, not retried", failure.isRetryable)
        }
    }

    @Test
    fun `rate limit carries its parameters`() = runTest {
        val (client, _) = client(
            replies = arrayOf(
                Reply(
                    HttpStatusCode.TooManyRequests,
                    """{"code":"rate_limited","copy_key":"error.rateLimited.body","request_id":"r-3","params":{"seconds":"3600"},"retry_after_seconds":3600}""",
                ),
            ),
        )
        try {
            client.startSignIn("sam@example.com")
            fail("expected a problem")
        } catch (failure: ApiException.Problem) {
            assertEquals("3600", failure.params["seconds"])
            val rendered = Copywriter.bundled(failure.bodyKey, failure.params)
            assertTrue("the wait should be interpolated: $rendered", rendered.contains("3600"))
        }
    }

    // A future server may send a code this build has never heard of. It must not crash,
    // and it must still say something.
    @Test
    fun `unknown error code falls back to generic copy`() = runTest {
        val (client, _) = client(
            replies = arrayOf(
                Reply(HttpStatusCode(418, "Teapot"), """{"code":"a_code_from_the_future","copy_key":"some.key.we.do.not.have","request_id":"r-4"}"""),
            ),
        )
        try {
            client.health()
            fail("expected a problem")
        } catch (failure: ApiException.Problem) {
            assertEquals(ErrorCode.UNKNOWN, failure.code)
            assertEquals("error.server.body", failure.bodyKey)
            assertTrue(Copywriter.bundled(failure.bodyKey).isNotEmpty())
        }
    }

    @Test
    fun `sign out clears locally even when the call fails`() = runTest {
        val tokens = InMemoryTokenStore(
            StoredSession("access-1", "refresh-1", nowMillis + 900_000, "user-1"),
        )
        val (client, _) = client(
            tokens,
            Reply(HttpStatusCode.InternalServerError, """{"code":"server_error","copy_key":"error.server.body","request_id":"r-5"}"""),
        )
        client.signOut()
        assertNull("someone who asked to sign out is signed out", tokens.load())
    }
}
