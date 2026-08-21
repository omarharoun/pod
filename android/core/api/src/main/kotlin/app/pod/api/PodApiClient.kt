package app.pod.api

import app.pod.api.generated.ApiError
import app.pod.api.generated.AuthRefreshRequest
import app.pod.api.generated.AuthSession
import app.pod.api.generated.AuthStartRequest
import app.pod.api.generated.AuthStartResponse
import app.pod.api.generated.AuthVerifyRequest
import app.pod.api.generated.ErrorCode
import app.pod.api.generated.Health
import app.pod.api.generated.User
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
import io.ktor.http.contentType
import io.ktor.serialization.kotlinx.json.json
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.Json
import java.io.IOException

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

    /**
     * Ends this device's session. The local tokens are cleared even if the call fails —
     * someone who asked to sign out is signed out.
     */
    public suspend fun signOut() {
        val stored = tokens.load()
        try {
            if (stored != null) {
                sendUnit("/v1/auth/logout", HttpMethod.Post, AuthRefreshRequest(refreshToken = stored.refreshToken))
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

    private suspend fun sendUnit(path: String, method: HttpMethod, body: Any?) {
        val accessToken = validAccessToken()
        perform(path, method, body, accessToken)
    }

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
