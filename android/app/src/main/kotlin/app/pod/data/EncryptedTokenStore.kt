package app.pod.data

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import app.pod.api.StoredSession
import app.pod.api.TokenStore
import kotlinx.serialization.json.Json

/** Tokens in `EncryptedSharedPreferences`, as CLAUDE.md §5 requires. */
class EncryptedTokenStore(context: Context) : TokenStore {
    private val preferences: SharedPreferences = EncryptedSharedPreferences.create(
        context.applicationContext,
        FILE_NAME,
        MasterKey.Builder(context.applicationContext)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build(),
        EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
        EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
    )

    override fun load(): StoredSession? {
        val raw = preferences.getString(KEY_SESSION, null) ?: return null
        return runCatching { Json.decodeFromString(StoredSession.serializer(), raw) }
            .onFailure { clear() } // an unreadable session is no session
            .getOrNull()
    }

    override fun save(session: StoredSession) {
        // The access and refresh tokens are only ever written together: two entries could
        // disagree after a crash between writes.
        preferences.edit().putString(KEY_SESSION, Json.encodeToString(StoredSession.serializer(), session)).apply()
    }

    override fun clear() {
        preferences.edit().remove(KEY_SESSION).apply()
    }

    private companion object {
        const val FILE_NAME = "app.pod.session"
        const val KEY_SESSION = "session"
    }
}
