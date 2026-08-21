package app.pod.feature.week

import android.Manifest
import android.content.Context
import android.media.MediaRecorder
import android.net.Uri
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.core.content.FileProvider
import app.pod.api.generated.CopyKey
import app.pod.api.generated.PodTokens
import app.pod.api.generated.ProofKind
import app.pod.ui.theme.LocalCopywriter
import app.pod.ui.theme.LocalPodColors
import kotlinx.coroutines.delay
import java.io.File

/** What a completed capture produces. */
data class CapturedProof(
    val kind: ProofKind,
    val contentType: String,
    val bytes: ByteArray,
    val durationMs: Int?,
) {
    // ByteArray makes the generated equals reference-based; these are never compared, and
    // the override keeps lint honest about it.
    override fun equals(other: Any?): Boolean = this === other
    override fun hashCode(): Int = System.identityHashCode(this)
}

/**
 * Choose a photo or a voice note, capture it, hand it back.
 *
 * Two equal choices rather than a camera that opens itself: a camera that opens itself is
 * a camera pointed at whatever is in front of you, and someone who wanted a voice note
 * chose it because they are not somewhere they want to photograph (docs/DESIGN.md).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ProofCaptureSheet(
    onCapture: (CapturedProof) -> Unit,
    onDismiss: () -> Unit,
) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current
    val context = LocalContext.current
    val sheetState = rememberModalBottomSheetState()

    val photoFile = remember { File(context.cacheDir, "proof-${System.nanoTime()}.jpg") }
    val photoUri = remember(photoFile) { context.proofUri(photoFile) }
    val recorder = remember { VoiceRecorder(context) }
    var isRecording by remember { mutableStateOf(false) }
    var secondsRemaining by remember { mutableIntStateOf(VoiceRecorder.LIMIT_SECONDS) }

    // Nothing about proof outlives the week it belongs to, and that includes the frame a
    // camera app wrote before the person backed out of it (CLAUDE.md §2 rule 4). The
    // voice path already deletes on every exit; the photo path only did so on success.
    DisposableEffect(Unit) {
        onDispose {
            recorder.cancel()
            photoFile.delete()
        }
    }

    val takePhoto = rememberLauncherForActivityResult(ActivityResultContracts.TakePicture()) { saved ->
        if (!saved) {
            // Some camera apps write the frame before the user cancels. Whether one is
            // there or not, this is where it stops existing.
            photoFile.delete()
            return@rememberLauncherForActivityResult
        }
        val bytes = runCatching { photoFile.readBytes() }.getOrNull()
        photoFile.delete()
        if (bytes != null && bytes.isNotEmpty()) {
            onCapture(CapturedProof(ProofKind.PHOTO, "image/jpeg", bytes, null))
        }
    }
    val askForMicrophone = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted && recorder.start()) {
            isRecording = true
            secondsRemaining = VoiceRecorder.LIMIT_SECONDS
        }
    }

    // The ten-second cap is enforced while recording, not checked afterwards: telling
    // someone their clip was too long once they have made it is exactly the small waste
    // this product is meant to be free of.
    LaunchedEffect(isRecording, secondsRemaining) {
        if (!isRecording) return@LaunchedEffect
        if (secondsRemaining <= 0) {
            isRecording = false
            recorder.stop()?.let { onCapture(CapturedProof(ProofKind.VOICE, "audio/m4a", it.bytes, it.durationMs)) }
            return@LaunchedEffect
        }
        delay(1_000)
        secondsRemaining -= 1
    }

    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = sheetState) {
        Column(
            verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.LG.dp),
            modifier = Modifier
                .fillMaxWidth()
                .padding(
                    horizontal = PodTokens.Spacing.SCREEN_GUTTER.dp,
                    vertical = PodTokens.Spacing.LG.dp,
                ),
        ) {
            Text(copy(CopyKey.WEEK_POST_CTA), style = MaterialTheme.typography.titleLarge, color = colors.contentPrimary)
            // The product's central promise, at the moment someone decides whether to
            // believe it.
            Text(
                copy(CopyKey.WEEK_POST_EPHEMERAL_NOTE),
                style = MaterialTheme.typography.bodyLarge,
                color = colors.contentSecondary,
            )

            if (isRecording) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(PodTokens.Spacing.MD.dp),
                ) {
                    Box(
                        Modifier
                            .size(PodTokens.Spacing.MD.dp)
                            .background(MaterialTheme.colorScheme.primary, CircleShape),
                    )
                    Text(
                        secondsRemaining.toString(),
                        style = MaterialTheme.typography.displaySmall,
                        color = colors.contentPrimary,
                    )
                }
                TextButton(onClick = {
                    isRecording = false
                    recorder.stop()?.let { onCapture(CapturedProof(ProofKind.VOICE, "audio/m4a", it.bytes, it.durationMs)) }
                }) {
                    Text(copy(CopyKey.COMMON_DONE))
                }
            } else {
                Choice(copy(CopyKey.WEEK_POST_PHOTO)) { takePhoto.launch(photoUri) }
                Choice(copy(CopyKey.WEEK_POST_VOICE)) { askForMicrophone.launch(Manifest.permission.RECORD_AUDIO) }
            }

            TextButton(onClick = onDismiss) {
                Text(copy(CopyKey.WEEK_POST_CANCEL), style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}

@Composable
private fun Choice(label: String, onClick: () -> Unit) {
    val colors = LocalPodColors.current
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = PodTokens.Size.PRIMARY_BUTTON_HEIGHT.dp)
            .background(colors.sunken, RoundedCornerShape(PodTokens.Radius.MD.dp))
            .clickable(onClick = onClick)
            .padding(horizontal = PodTokens.Spacing.LG.dp),
    ) {
        Text(label, style = MaterialTheme.typography.bodyLarge, color = colors.contentPrimary)
    }
}

private fun Context.proofUri(file: File): Uri =
    FileProvider.getUriForFile(this, "$packageName.proof", file)

/** Records up to ten seconds and hands back the bytes. */
class VoiceRecorder(private val context: Context) {
    data class Finished(val bytes: ByteArray, val durationMs: Int) {
        override fun equals(other: Any?): Boolean = this === other
        override fun hashCode(): Int = System.identityHashCode(this)
    }

    private var recorder: MediaRecorder? = null
    private var file: File? = null
    private var startedAt = 0L

    fun start(): Boolean {
        if (recorder != null) return false
        val target = File(context.cacheDir, "proof-${System.nanoTime()}.m4a")
        val media = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            MediaRecorder(context)
        } else {
            @Suppress("DEPRECATION")
            MediaRecorder()
        }
        return runCatching {
            media.setAudioSource(MediaRecorder.AudioSource.MIC)
            media.setOutputFormat(MediaRecorder.OutputFormat.MPEG_4)
            media.setAudioEncoder(MediaRecorder.AudioEncoder.AAC)
            media.setAudioSamplingRate(44_100)
            media.setMaxDuration(LIMIT_SECONDS * 1_000)
            media.setOutputFile(target.absolutePath)
            media.prepare()
            media.start()
            recorder = media
            file = target
            startedAt = System.currentTimeMillis()
            true
        }.getOrElse {
            runCatching { media.release() }
            target.delete()
            false
        }
    }

    fun stop(): Finished? {
        val media = recorder ?: return null
        val target = file
        recorder = null
        file = null
        runCatching { media.stop() }
        runCatching { media.release() }

        val elapsed = (System.currentTimeMillis() - startedAt).toInt().coerceIn(1, LIMIT_SECONDS * 1_000)
        val bytes = target?.let { runCatching { it.readBytes() }.getOrNull() }
        target?.delete()
        return bytes?.takeIf { it.isNotEmpty() }?.let { Finished(it, elapsed) }
    }

    fun cancel() {
        val media = recorder ?: return
        recorder = null
        runCatching { media.stop() }
        runCatching { media.release() }
        file?.delete()
        file = null
    }

    companion object {
        const val LIMIT_SECONDS = 10
    }
}
