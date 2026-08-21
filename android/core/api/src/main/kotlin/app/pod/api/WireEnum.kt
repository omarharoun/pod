package app.pod.api

import kotlinx.serialization.KSerializer
import kotlinx.serialization.descriptors.PrimitiveKind
import kotlinx.serialization.descriptors.PrimitiveSerialDescriptor
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder

/** An enum with a wire representation that may outgrow this build. */
public interface WireEnum {
    public val wire: String
}

/**
 * Serialises a [WireEnum] by its wire value and decodes anything unrecognised to the
 * enum's `UNKNOWN` case.
 *
 * A closed enum would throw on a value a newer server sends, which would mean a backend
 * deploy could brick every installed app that has not updated. It cannot.
 */
public abstract class WireEnumSerializer<T>(
    serialName: String,
    private val fromWire: (String) -> T,
) : KSerializer<T> where T : Enum<T>, T : WireEnum {

    override val descriptor: SerialDescriptor =
        PrimitiveSerialDescriptor(serialName, PrimitiveKind.STRING)

    override fun serialize(encoder: Encoder, value: T) {
        encoder.encodeString(value.wire)
    }

    override fun deserialize(decoder: Decoder): T = fromWire(decoder.decodeString())
}
