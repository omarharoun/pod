package app.pod.api

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/**
 * Resolves a copy key into the string a person actually reads.
 *
 * The catalogue is `content/microcopy.json`, copied into this module's resources by
 * `make generate`. Nothing in the app builds user-facing text any other way — see
 * CLAUDE.md §2 rule 1.
 */
public class Copywriter private constructor(
    private val strings: Map<String, Entry>,
) {
    private sealed interface Entry {
        @JvmInline value class Single(val text: String) : Entry
        @JvmInline value class Plural(val forms: Map<String, String>) : Entry
    }

    /** Returns the string for [key], substituting `{placeholders}`. */
    public operator fun invoke(key: String, params: Map<String, String> = emptyMap()): String =
        resolve(key, count = 1, params = params)

    /** Returns the plural form appropriate to [count]. */
    public fun plural(key: String, count: Int, params: Map<String, String> = emptyMap()): String =
        resolve(key, count, params + ("count" to count.toString()))

    public fun contains(key: String): Boolean = strings.containsKey(key)

    /**
     * The catalogue string with its `{placeholders}` intact, or null if absent.
     *
     * For tests that assert on the *shape* of a string — that it interpolates a value
     * rather than spelling it out, that a deadline is a date and not a countdown. A
     * resolved string cannot answer either question.
     */
    public fun raw(key: String): String? = when (val entry = strings[key]) {
        is Entry.Single -> entry.text
        is Entry.Plural -> entry.forms["other"] ?: entry.forms.values.firstOrNull()
        null -> null
    }

    private fun resolve(key: String, count: Int, params: Map<String, String>): String {
        val entry = strings[key] ?: run {
            // The generated constants and the catalogue come from one file, so a miss is
            // a packaging problem, not a runtime condition. Never show a raw key.
            check(!DEBUG_STRICT) { "missing copy key $key — run `make generate`" }
            return ""
        }
        val template = when (entry) {
            is Entry.Single -> entry.text
            is Entry.Plural -> entry.forms[if (count == 1) "one" else "other"]
                ?: entry.forms["other"].orEmpty()
        }
        return interpolate(template, params)
    }

    public companion object {
        /** Set from BuildConfig.DEBUG by the app; tests leave it strict. */
        public var DEBUG_STRICT: Boolean = true

        /** The catalogue packaged with the app. */
        public val bundled: Copywriter by lazy {
            val stream = Copywriter::class.java.classLoader
                ?.getResourceAsStream("microcopy.json")
                ?: error("the microcopy catalogue is missing from the module's resources")
            from(stream.bufferedReader().use { it.readText() })
        }

        public fun from(json: String): Copywriter {
            val root = Json.parseToJsonElement(json).jsonObject
            val strings = buildMap {
                for ((key, value) in root) {
                    if (key.startsWith("$")) continue
                    when (value) {
                        is JsonPrimitive -> put(key, Entry.Single(value.content))
                        is JsonObject -> put(
                            key,
                            Entry.Plural(value.mapValues { (_, form) -> form.jsonPrimitive.content }),
                        )
                        else -> error("$key: expected a string or a plural object")
                    }
                }
            }
            return Copywriter(strings)
        }

        internal fun interpolate(template: String, params: Map<String, String>): String {
            if (!template.contains('{')) return template
            val out = StringBuilder(template.length)
            var index = 0
            while (index < template.length) {
                val open = template.indexOf('{', index)
                if (open < 0) {
                    out.append(template, index, template.length)
                    break
                }
                val close = template.indexOf('}', open)
                if (close < 0) {
                    out.append(template, index, template.length)
                    break
                }
                out.append(template, index, open)
                out.append(params[template.substring(open + 1, close)].orEmpty())
                index = close + 1
            }
            return out.toString()
        }
    }
}
