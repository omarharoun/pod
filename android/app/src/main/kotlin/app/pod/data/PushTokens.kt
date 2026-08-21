package app.pod.data

import app.pod.api.PodApiClient
import app.pod.api.generated.DevicePlatform
import java.util.Locale

/**
 * Where a push token comes from.
 *
 * Firebase Cloud Messaging needs a `google-services.json` that this repository does not
 * and should not contain, so the production implementation is wired in per environment —
 * the same seam as the mailer (ADR-002), the blob store (ADR-015) and the push sender.
 * Everything on this side of it is real: the registration call, the retirement on sign
 * out, and the fact that the app works perfectly well when no token ever arrives.
 */
interface PushTokenSource {
    /** The current token, or null when push is unavailable on this device or build. */
    suspend fun currentToken(): String?
}

/**
 * The default. Push is a nicety and the week loop does not depend on it, so an app with
 * no FCM configuration behaves exactly like a person who declined notifications.
 */
class NoPushTokenSource : PushTokenSource {
    override suspend fun currentToken(): String? = null
}

/** Registers whatever token the source has, and retires it on sign out. */
class PushRegistrar(
    private val client: PodApiClient,
    private val tokens: PushTokenSource,
) {
    private var registered: String? = null

    /**
     * Called once a pod exists — the first moment "can we let you know when your pod does
     * something" has a subject. A failure is not surfaced: nobody needs to be told their
     * notifications did not register.
     */
    suspend fun registerIfPossible() {
        val token = tokens.currentToken() ?: return
        if (token == registered) return
        runCatching {
            client.registerDevice(token, DevicePlatform.ANDROID, Locale.getDefault().toLanguageTag())
            registered = token
        }
    }

    /** Called on sign out, so a shared device stops receiving somebody else's pod. */
    suspend fun unregister() {
        val token = registered ?: return
        runCatching { client.unregisterDevice(token, DevicePlatform.ANDROID) }
        registered = null
    }
}
