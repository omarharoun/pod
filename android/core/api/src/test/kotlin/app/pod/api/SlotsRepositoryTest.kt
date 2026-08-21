package app.pod.api

import app.pod.api.generated.CopyKey
import app.pod.api.generated.PurchaseSource
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.content.TextContent
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

class SlotsRepositoryTest {
    private val nowMillis = 1_800_000_000_000L
    private val jsonHeaders = headersOf(HttpHeaders.ContentType, "application/json")

    private val oneOpenLane = """
        {"max_slots":5,"slots":[
          {"slot":0,"entitled":true,"has_intent":true,"in_pool":false,"has_pod":true,"goal_text":"sort out the spare room"},
          {"slot":1,"entitled":false,"has_intent":false,"in_pool":false,"has_pod":false},
          {"slot":2,"entitled":false,"has_intent":false,"in_pool":false,"has_pod":false},
          {"slot":3,"entitled":false,"has_intent":false,"in_pool":false,"has_pod":false},
          {"slot":4,"entitled":false,"has_intent":false,"in_pool":false,"has_pod":false}
        ]}
    """.trimIndent()

    private class Call(val path: String, val method: String, val body: String)

    private class CancellingBroker : PurchaseBroker {
        override suspend fun purchaseAdditionalLane(): String? = null
    }

    private class BuyingBroker(private val receipt: String) : PurchaseBroker {
        var calls = 0
        override suspend fun purchaseAdditionalLane(): String {
            calls++
            return receipt
        }
    }

    private fun repository(
        broker: PurchaseBroker = CancellingBroker(),
        vararg replies: Pair<HttpStatusCode, String>,
    ): Pair<SlotsRepository, MutableList<Call>> {
        val calls = mutableListOf<Call>()
        val queue = replies.toMutableList()
        val engine = MockEngine { request ->
            calls += Call(
                request.url.encodedPath + (request.url.encodedQuery.takeIf { it.isNotEmpty() }?.let { "?$it" } ?: ""),
                request.method.value,
                (request.body as? TextContent)?.text.orEmpty(),
            )
            val reply = queue.removeFirstOrNull() ?: (HttpStatusCode.OK to oneOpenLane)
            respond(reply.second, reply.first, jsonHeaders)
        }
        val tokens = InMemoryTokenStore(
            StoredSession("access-1", "refresh-1", nowMillis + 900_000, "user-1"),
        )
        val client = PodApiClient("https://api.test.invalid", tokens, engine) { nowMillis }
        return SlotsRepository(client, broker, PurchaseSource.PLAY_STORE) to calls
    }

    @Test
    fun `the free lane is always open`() = runTest {
        val (repository, _) = repository()
        repository.load()

        val lanes = repository.openLanes()
        assertEquals(1, lanes.size)
        assertEquals(0, lanes.first().slot)
        assertTrue(lanes.first().entitled)
        assertTrue(repository.canOpenAnother())
    }

    /** Cancelling a purchase is a decision, not an error. */
    @Test
    fun `cancelling the purchase sheet produces no failure`() = runTest {
        val (repository, calls) = repository(CancellingBroker())
        repository.load()
        calls.clear()

        val opened = repository.openAnotherLane()

        assertNull(opened)
        assertNull("cancelling is a decision, not a failure", repository.failure.value)
        assertTrue("cancelling must not reach the server: $calls", calls.isEmpty())
        assertFalse(repository.isWorking.value)
    }

    @Test
    fun `a completed purchase claims the lane and reloads`() = runTest {
        val broker = BuyingBroker("dev:order-1")
        val (repository, calls) = repository(
            broker,
            HttpStatusCode.OK to oneOpenLane,
            HttpStatusCode.OK to """{"slot":1,"entitled":true,"has_intent":false,"in_pool":false,"has_pod":false}""",
            HttpStatusCode.OK to oneOpenLane.replace(
                """{"slot":1,"entitled":false""",
                """{"slot":1,"entitled":true""",
            ),
        )
        repository.load()

        val opened = repository.openAnotherLane()

        assertNotNull(opened)
        assertEquals(1, opened!!.slot)
        assertEquals(1, broker.calls)
        val post = calls.first { it.method == "POST" }
        assertEquals("/v1/slots", post.path)
        assertTrue(post.body, post.body.contains("dev:order-1"))
        assertEquals(2, repository.openLanes().size)
    }

    /** A rejected receipt must not take away the free lane. */
    @Test
    fun `a rejected receipt leaves the free lane alone`() = runTest {
        val (repository, _) = repository(
            BuyingBroker("dev:forged"),
            HttpStatusCode.OK to oneOpenLane,
            HttpStatusCode.PaymentRequired to
                """{"code":"bad_request","copy_key":"error.receiptRejected.body","request_id":"r1"}""",
        )
        repository.load()

        val opened = repository.openAnotherLane()

        assertNull(opened)
        assertNotNull(repository.failure.value)
        val lanes = repository.openLanes()
        assertEquals("the free lane survives a rejected purchase", 1, lanes.size)
        assertTrue(lanes.first().entitled)
    }

    /** The free lane's requests carry no slot parameter at all (ADR-026). */
    @Test
    fun `the free lane sends no slot parameter`() = runTest {
        val (repository, calls) = repository()
        val client = PodApiClient(
            "https://api.test.invalid",
            InMemoryTokenStore(StoredSession("access-1", "refresh-1", nowMillis + 900_000, "user-1")),
            MockEngine { request ->
                calls += Call(
                    request.url.encodedPath + (request.url.encodedQuery.takeIf { it.isNotEmpty() }?.let { "?$it" } ?: ""),
                    request.method.value,
                    "",
                )
                respond("""{"queued_at":"2026-08-21T09:00:00Z","next_window_at":"2026-08-21T10:00:00Z"}""", HttpStatusCode.OK, jsonHeaders)
            },
        ) { nowMillis }
        calls.clear()

        client.joinPool()
        client.joinPool(slot = 2)

        assertEquals("/v1/pool", calls[0].path)
        assertEquals("/v1/pool?slot=2", calls[1].path)
        // Keeps `repository` referenced so the harness is exercised as written.
        assertNotNull(repository)
    }
}

/** The paywall's copy carries the product's whole position on paying for this. */
class SlotsCopyTest {
    private val copy = Copywriter.bundled

    private val keys = listOf(
        CopyKey.SLOTS_OFFER_TITLE, CopyKey.SLOTS_OFFER_BODY, CopyKey.SLOTS_OFFER_ACTION,
        CopyKey.SLOTS_EMPTY, CopyKey.SLOTS_NEW_TITLE, CopyKey.SLOTS_NEW_BODY,
        CopyKey.ERROR_RECEIPT_REJECTED_TITLE, CopyKey.ERROR_RECEIPT_REJECTED_BODY,
        CopyKey.ERROR_SLOT_CEILING_TITLE, CopyKey.ERROR_SLOT_CEILING_BODY,
    )

    @Test
    fun `every string resolves`() {
        for (key in keys) {
            assertTrue("missing $key", copy.contains(key))
            assertTrue("empty $key", copy(key).isNotEmpty())
        }
    }

    /**
     * Nothing about the free pod is locked, and calling it that would be a lie about the
     * product's own shape (docs/DESIGN.md, CLAUDE.md §2 rule 6).
     */
    @Test
    fun `the offer does not call the free pod locked`() {
        for (key in keys) {
            val text = copy(key).lowercase()
            for (word in listOf("unlock", "upgrade", "premium", "pro ", "locked", "limited", "free trial")) {
                assertFalse("$key says \"$word\": ${copy(key)}", text.contains(word))
            }
        }
    }

    /** The price belongs to StoreKit and Play, not to this catalogue. */
    @Test
    fun `no copy names a price`() {
        for (key in keys) {
            val text = copy(key)
            for (marker in listOf("$", "£", "€", "/month", "per month", "a month", "/year")) {
                assertFalse("$key names a price: $text", text.contains(marker))
            }
        }
    }

    /**
     * A failed purchase must say nothing was charged. Someone who sees an error after
     * tapping a buy button assumes the worst, and the worst is easy to rule out.
     */
    @Test
    fun `a rejected receipt says nothing was charged`() {
        val body = copy(CopyKey.ERROR_RECEIPT_REJECTED_BODY).lowercase()
        assertTrue(
            "the rejection does not address the money: ${copy(CopyKey.ERROR_RECEIPT_REJECTED_BODY)}",
            body.contains("charged"),
        )
    }

    /** The ceiling may not count what somebody has not bought. */
    @Test
    fun `the ceiling does not count unbought lanes`() {
        val text = copy(CopyKey.ERROR_SLOT_CEILING_BODY).lowercase()
        for (marker in listOf("of 5", "5 slots", "slots used", "0 of", "remaining")) {
            assertFalse("the ceiling counts: ${copy(CopyKey.ERROR_SLOT_CEILING_BODY)}", text.contains(marker))
        }
    }
}
