package app.pod.api

import app.pod.api.generated.ApiError
import app.pod.api.generated.AuthRefreshRequest
import app.pod.api.generated.AuthSession
import app.pod.api.generated.AuthStartRequest
import app.pod.api.generated.AuthStartResponse
import app.pod.api.generated.AuthVerifyRequest
import app.pod.api.generated.ClaimSlotRequest
import app.pod.api.generated.ContinueRequest
import app.pod.api.generated.DevicePlatform
import app.pod.api.generated.DeviceRequest
import app.pod.api.generated.ErrorCode
import app.pod.api.generated.Health
import app.pod.api.generated.Intent
import app.pod.api.generated.IntentRequest
import app.pod.api.generated.OnboardingState
import app.pod.api.generated.Pod
import app.pod.api.generated.PoolTicket
import app.pod.api.generated.Proof
import app.pod.api.generated.ProofKind
import app.pod.api.generated.ProofUpload
import app.pod.api.generated.ProofUploadRequest
import app.pod.api.generated.PurchaseSource
import app.pod.api.generated.Slot
import app.pod.api.generated.SlotList
import app.pod.api.generated.User
import app.pod.api.generated.Week
import app.pod.api.generated.WeekActionRequest
import app.pod.api.generated.WeekList
import io.ktor.client.HttpClient
import io.ktor.client.HttpClientConfig
import io.ktor.client.call.body
import io.ktor.client.engine.HttpClientEngine
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.client.request.HttpRequestBuilder
import io.ktor.client.request.header
import io.ktor.client.request.request
import io.ktor.client.request.setBody
import io.ktor.client.statement.HttpResponse
import io.ktor.http.ContentType
import io.ktor.http.HttpMethod
import io.ktor.http.HttpStatusCode
import io.ktor.http.contentType
import io.ktor.serialization.kotlinx.json.json
import java.io.IOException
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.Json

/**
 * The one way this app talks to Pod.
 *
 * Ktor and the generated models (CLAUDE.md §5). The refresh path is serialised behind a
 * mutex: two calls meeting a stale access token must produce one refresh, not two, or the
 * second rotates the token the first just stored and trips the server's reuse detection
 * (ADR-002).
 */
public class PodApiClient(
    private val baseUrl: String,
    private val tokens: TokenStore,
    engine: HttpClientEngine? = null,
    private val now: () -> Long = System::currentTimeMillis,
) {
    private val json = Json {
        ignoreUnknownKeys = true
        encodeDefaults = true
        explicitNulls = false
    }

    private val http: HttpClient =
        if (engine != null) {
            HttpClient(engine) { configure() }
        } else {
            HttpClient { configure() }
        }

    private fun HttpClientConfig<*>.configure() {
        install(ContentNegotiation) { json(this@PodApiClient.json) }
        // Errors are the contract's problem bodies, not exceptions to be caught blind.
        expectSuccess = false
    }

    /** Serialises refresh so that concurrent callers produce one rotation, not several. */
    private val refreshLock = Mutex()

    // ---- System ----------------------------------------------------------------

    public suspend fun health(): Health =
        send("/v1/health", HttpMethod.Get, authenticated = false)

    // ---- Auth ------------------------------------------------------------------

    public suspend fun startSignIn(email: String): AuthStartResponse =
        send("/v1/auth/start", HttpMethod.Post, AuthStartRequest(email = email), authenticated = false)

    /** Verifies the code and stores the resulting session. */
    public suspend fun verifySignIn(challengeId: String, code: String, deviceName: String?): AuthSession {
        val session: AuthSession = send(
            "/v1/auth/verify",
            HttpMethod.Post,
            AuthVerifyRequest(challengeId = challengeId, code = code, deviceName = deviceName),
            authenticated = false,
        )
        store(session)
        return session
    }

    public suspend fun currentUser(): User = send("/v1/me", HttpMethod.Get)

    // ---- Devices ---------------------------------------------------------------

    public suspend fun registerDevice(token: String, platform: DevicePlatform, locale: String) {
        sendUnit("/v1/devices", HttpMethod.Put, DeviceRequest(token = token, platform = platform, locale = locale))
    }

    public suspend fun unregisterDevice(token: String, platform: DevicePlatform) {
        sendUnit("/v1/devices", HttpMethod.Delete, DeviceRequest(token = token, platform = platform))
    }

    // ---- Lanes -------------------------------------------------------------------

    /** Every lane the caller has, and whether each may be used. */
    public suspend fun slots(): SlotList = send("/v1/slots", HttpMethod.Get)

    /**
     * Opens the next lane against a verified purchase.
     *
     * Idempotent on the store's transaction id, so the retries every store SDK performs
     * return the lane already opened rather than spending another purchase.
     */
    public suspend fun claimSlot(source: PurchaseSource, receipt: String): Slot =
        send("/v1/slots", HttpMethod.Post, ClaimSlotRequest(source = source, receipt = receipt))

    // ---- Onboarding ------------------------------------------------------------

    public suspend fun onboardingState(slot: Int = 0): OnboardingState =
        send(withSlot("/v1/onboarding/state", slot), HttpMethod.Get)

    /**
     * The intent on file, or null when nothing has been answered yet.
     *
     * Recovery re-offers a smaller intensity without re-asking the goal, the timezone or
     * the availability windows, so it has to read them back (ADR-021).
     */
    public suspend fun intent(slot: Int = 0): Intent? = try {
        send(withSlot("/v1/onboarding/intent", slot), HttpMethod.Get)
    } catch (failure: ApiException.Problem) {
        if (failure.error.code == ErrorCode.NOT_FOUND) null else throw failure
    }

    public suspend fun saveIntent(intent: IntentRequest, slot: Int = 0): Intent =
        send(withSlot("/v1/onboarding/intent", slot), HttpMethod.Put, intent)

    public suspend fun joinPool(slot: Int = 0): PoolTicket =
        send(withSlot("/v1/pool", slot), HttpMethod.Post)

    public suspend fun leavePool(slot: Int = 0) {
        sendUnit(withSlot("/v1/pool", slot), HttpMethod.Delete, body = null)
    }

    // ---- Pod -------------------------------------------------------------------

    /**
     * The caller's active pod, or null when they have not been matched yet.
     *
     * The server answers 204 rather than 404 for "no pod": not being matched yet is a
     * state, not a failure, and the two must not look the same to the client.
     */
    public suspend fun currentPod(slot: Int = 0): Pod? =
        sendOrNull(withSlot("/v1/pods/current", slot), HttpMethod.Get)

    /**
     * Records whether the caller is carrying on after graduation.
     *
     * Answering does not restart or close the pod: the server settles that once the answer
     * cannot change or the deadline arrives, so the last person to answer is never the one
     * who appears to have ended it (ADR-023).
     */
    public suspend fun continueCycle(podId: String, continuing: Boolean): Pod =
        send("/v1/pods/$podId/continue", HttpMethod.Post, ContinueRequest(continuing = continuing))

    // ---- The week --------------------------------------------------------------

    public suspend fun currentWeek(podId: String): Week =
        send("/v1/pods/$podId/weeks/current", HttpMethod.Get)

    public suspend fun weeks(podId: String): WeekList =
        send("/v1/pods/$podId/weeks", HttpMethod.Get)

    public suspend fun setWeekAction(podId: String, weekId: String, actionText: String): Week =
        send("/v1/pods/$podId/weeks/$weekId/action", HttpMethod.Put, WeekActionRequest(actionText))

    // ---- Proof -----------------------------------------------------------------

    /**
     * Posts a proof: reserve a slot, PUT the bytes straight to storage, then commit.
     *
     * The three steps live together because they are one action from the person's point
     * of view, and because a slot with no bytes in it must never be committed (ADR-008).
     */
    public suspend fun postProof(
        podId: String,
        weekId: String,
        kind: ProofKind,
        contentType: String,
        bytes: ByteArray,
        durationMs: Int? = null,
    ): Proof {
        val base = "/v1/pods/$podId/weeks/$weekId/proof"
        val slot: ProofUpload = send(
            base,
            HttpMethod.Post,
            ProofUploadRequest(
                kind = kind,
                contentType = contentType,
                byteSize = bytes.size,
                durationMs = durationMs,
            ),
        )
        putBytes(slot, contentType, bytes)
        return send("$base/${slot.proofId}/commit", HttpMethod.Post)
    }

    public suspend fun proof(podId: String, weekId: String, proofId: String): Proof =
        send("/v1/pods/$podId/weeks/$weekId/proof/$proofId", HttpMethod.Get)

    /** Fetches the bytes behind a short-lived view link. */
    public suspend fun proofBytes(url: String): ByteArray {
        val response = try {
            http.request(url) { method = HttpMethod.Get }
        } catch (io: IOException) {
            throw ApiException.Offline(io)
        }
        if (response.status.value !in 200..299) throw ApiException.Malformed()
        return response.body()
    }

    /**
     * PUTs proof bytes to the signed URL. This never touches the API: it is the whole
     * point of the two-step upload that the bytes go straight to storage (ADR-008).
     */
    private suspend fun putBytes(slot: ProofUpload, contentType: String, bytes: ByteArray) {
        val response = try {
            http.request(slot.uploadUrl) {
                method = HttpMethod.parse(slot.uploadMethod)
                slot.uploadHeaders.orEmpty().forEach { (name, value) ->
                    // Content-Length is set from the body; setting it again makes the
                    // request malformed.
                    if (!name.equals("Content-Length", ignoreCase = true)) header(name, value)
                }
                contentType(ContentType.parse(contentType))
                setBody(bytes)
            }
        } catch (io: IOException) {
            throw ApiException.Offline(io)
        }
        if (response.status.value !in 200..299) throw ApiException.Malformed()
    }

    /**
     * Ends this device's session. The local tokens are cleared even if the call fails —
     * someone who asked to sign out is signed out.
     */
    public suspend fun signOut() {
        val stored = tokens.load()
        try {
            if (stored != null) {
                sendUnit(
                    "/v1/auth/logout",
                    HttpMethod.Post,
                    AuthRefreshRequest(refreshToken = stored.refreshToken),
                    retryAfterRefresh = false,
                )
            }
        } catch (_: ApiException) {
            // Nothing useful to do: the local state is what matters here.
        } finally {
            tokens.clear()
        }
    }

    public fun restoredSession(): StoredSession? = tokens.load()

    // ---- Sending ---------------------------------------------------------------

    private suspend inline fun <reified Response> send(
        path: String,
        method: HttpMethod,
        body: Any? = null,
        authenticated: Boolean = true,
    ): Response {
        val accessToken = if (authenticated) validAccessToken() else null
        return try {
            decode(perform(path, method, body, accessToken))
        } catch (failure: ApiException.Problem) {
            if (!authenticated || failure.status != 401 || failure.error.code != ErrorCode.SESSION_EXPIRED) throw failure
            // The access token was rejected even though it looked fresh — clock skew, or
            // a revocation. One refresh, one retry, then give up.
            decode(perform(path, method, body, refreshSession().accessToken))
        }
    }

    // The same one-refresh-one-retry as [send]. Without it, leavePool, registerDevice and
    // unregisterDevice were the three calls that simply failed on a token rejected for
    // clock skew — quietly, since none of them surfaces its failure.
    //
    // `retryAfterRefresh` is false for exactly one caller. Refreshing rotates the refresh
    // token, so retrying sign-out would mint a fresh session moments before the local
    // tokens are thrown away — leaving a live one on the server that nothing will ever
    // revoke, and sending a body carrying a token the refresh just consumed.
    private suspend fun sendUnit(
        path: String,
        method: HttpMethod,
        body: Any?,
        retryAfterRefresh: Boolean = true,
    ) {
        val accessToken = validAccessToken()
        try {
            perform(path, method, body, accessToken)
        } catch (failure: ApiException.Problem) {
            if (!retryAfterRefresh || failure.status != 401 || failure.error.code != ErrorCode.SESSION_EXPIRED) {
                throw failure
            }
            perform(path, method, body, refreshSession().accessToken)
        }
    }

    /** Like [send], but maps a 204 onto null instead of trying to decode an empty body. */
    private suspend inline fun <reified Response> sendOrNull(
        path: String,
        method: HttpMethod,
    ): Response? {
        val accessToken = validAccessToken()
        val response = try {
            perform(path, method, null, accessToken)
        } catch (failure: ApiException.Problem) {
            if (failure.status != 401 || failure.error.code != ErrorCode.SESSION_EXPIRED) throw failure
            perform(path, method, null, refreshSession().accessToken)
        }
        if (response.status == HttpStatusCode.NoContent) return null
        return decode(response)
    }

    /**
     * Appends the lane, and only when it is not the free one.
     *
     * Omitting it for lane 0 keeps every existing request byte-identical to what it was
     * before M7, so the free loop cannot be broken by a parameter (ADR-026).
     */
    private fun withSlot(path: String, slot: Int): String =
        if (slot == 0) path else "$path?slot=$slot"

    private suspend fun perform(
        path: String,
        method: HttpMethod,
        body: Any?,
        accessToken: String?,
    ): HttpResponse {
        val response = try {
            http.request(baseUrl.trimEnd('/') + path) {
                this.method = method
                accessToken?.let { header("Authorization", "Bearer $it") }
                if (body != null) {
                    contentType(ContentType.Application.Json)
                    setBody(body)
                }
                applyDefaults()
            }
        } catch (io: IOException) {
            throw ApiException.Offline(io)
        }

        if (response.status.value !in 200..299) {
            val problem = runCatching { response.body<ApiError>() }.getOrNull()
                ?: throw ApiException.Malformed()
            throw ApiException.Problem(problem, response.status.value)
        }
        return response
    }

    private fun HttpRequestBuilder.applyDefaults() {
        header("Accept", ContentType.Application.Json.toString())
    }

    private suspend inline fun <reified Response> decode(response: HttpResponse): Response =
        try {
            response.body<Response>()
        } catch (failure: Throwable) {
            throw ApiException.Malformed(failure)
        }

    // ---- Session ---------------------------------------------------------------

    private suspend fun validAccessToken(): String {
        val stored = tokens.load() ?: throw ApiException.NotSignedIn
        if (stored.accessTokenIsFresh(now())) return stored.accessToken
        return refreshSession().accessToken
    }

    private suspend fun refreshSession(): StoredSession = refreshLock.withLock {
        val stored = tokens.load() ?: throw ApiException.NotSignedIn
        // Another caller may have refreshed while this one waited for the lock. If so,
        // its result is already in the store and rotating again would revoke it.
        if (stored.accessTokenIsFresh(now())) return@withLock stored

        val renewed: AuthSession = try {
            decode(
                perform(
                    "/v1/auth/refresh",
                    HttpMethod.Post,
                    AuthRefreshRequest(refreshToken = stored.refreshToken),
                    accessToken = null,
                ),
            )
        } catch (failure: ApiException.Problem) {
            if (failure.status == 401) {
                // The chain is gone: expired, or reuse was detected. Nothing left to
                // retry, so the person signs in again.
                tokens.clear()
                throw ApiException.NotSignedIn
            }
            throw failure
        }
        store(renewed)
    }

    private fun store(session: AuthSession): StoredSession {
        val stored = StoredSession(
            accessToken = session.accessToken,
            refreshToken = session.refreshToken,
            accessTokenExpiresAtMillis = now() + session.expiresInSeconds * 1_000L,
            userId = session.user.id,
        )
        tokens.save(stored)
        return stored
    }
}
