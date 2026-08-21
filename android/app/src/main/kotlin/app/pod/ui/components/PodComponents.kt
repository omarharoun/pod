package app.pod.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.unit.dp
import app.pod.api.ApiException
import app.pod.api.generated.CopyKey
import app.pod.api.generated.PodTokens
import app.pod.ui.theme.LocalCopywriter
import app.pod.ui.theme.LocalPodColors

/** The primary action on a screen. There is only ever one. */
@Composable
fun PodPrimaryButton(
    title: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    working: Boolean = false,
) {
    Button(
        onClick = onClick,
        enabled = enabled && !working,
        shape = RoundedCornerShape(PodTokens.Radius.MD.dp),
        modifier = modifier
            .fillMaxWidth()
            .heightIn(min = PodTokens.Size.PRIMARY_BUTTON_HEIGHT.dp),
    ) {
        if (working) {
            CircularProgressIndicator(
                strokeWidth = 2.dp,
                color = MaterialTheme.colorScheme.onPrimary,
                modifier = Modifier.heightIn(min = 20.dp),
            )
        } else {
            Text(title, style = MaterialTheme.typography.labelLarge)
        }
    }
}

/**
 * How a failure is shown: a calm block under the content, never a red alert and never a
 * snackbar that vanishes before it is read.
 *
 * The wording comes from the server as a key and is resolved here (ADR-003).
 */
@Composable
fun PodNotice(
    failure: ApiException,
    modifier: Modifier = Modifier,
    onRetry: (() -> Unit)? = null,
) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current
    val title = copy(failure.titleKey)
    val body = copy(failure.bodyKey, failure.params)

    Column(
        verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.SM.dp),
        modifier = modifier
            .fillMaxWidth()
            .background(colors.sunken, RoundedCornerShape(PodTokens.Radius.MD.dp))
            .padding(PodTokens.Spacing.LG.dp)
            .clearAndSetSemantics { contentDescription = "$title. $body" },
    ) {
        Text(title, style = MaterialTheme.typography.labelLarge, color = colors.contentPrimary)
        Text(body, style = MaterialTheme.typography.bodySmall, color = colors.contentSecondary)
        if (onRetry != null && failure.isRetryable) {
            TextButton(onClick = onRetry) {
                Text(copy(CopyKey.ERROR_NETWORK_RETRY), style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}
