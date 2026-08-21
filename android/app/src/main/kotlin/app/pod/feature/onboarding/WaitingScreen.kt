package app.pod.feature.onboarding

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import app.pod.api.ApiException
import app.pod.api.generated.CopyKey
import app.pod.api.generated.PodTokens
import app.pod.api.generated.PoolTicket
import app.pod.ui.components.PodNotice
import app.pod.ui.theme.LocalCopywriter
import app.pod.ui.theme.LocalPodColors
import kotlinx.datetime.TimeZone
import kotlinx.datetime.toJavaInstant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

/**
 * Waiting for the next matching window.
 *
 * The window time comes from the server (`pool_ticket.next_window_at`), never from a
 * client constant: an hour is the server's policy. Saying when lets someone close the app,
 * which is the right thing here — a spinner would invite pull-to-refresh for a job that
 * runs on a schedule (CLAUDE.md §5, docs/DESIGN.md).
 */
@Composable
fun WaitingScreen(
    ticket: PoolTicket,
    failure: ApiException?,
    onCheckForPod: () -> Unit,
    onLeaveQueue: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current
    val configuration = LocalConfiguration.current

    val windowTime = remember(ticket.nextWindowAt, configuration) {
        DateTimeFormatter
            .ofLocalizedTime(FormatStyle.SHORT)
            .withZone(ZoneId.of(TimeZone.currentSystemDefault().id))
            .format(ticket.nextWindowAt.toJavaInstant())
    }

    LaunchedEffect(Unit) { onCheckForPod() }
    // Check once on return, deliberately. Matching is scheduled; there is nothing to poll.
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { onCheckForPod() }

    Column(
        verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.LG.dp),
        modifier = modifier
            .fillMaxSize()
            .padding(
                horizontal = PodTokens.Spacing.SCREEN_GUTTER.dp,
                vertical = PodTokens.Spacing.XXXL.dp,
            ),
    ) {
        Spacer(Modifier.weight(1f))

        Text(copy(CopyKey.POOL_WAITING_TITLE), style = MaterialTheme.typography.titleLarge, color = colors.contentPrimary)
        Text(copy(CopyKey.POOL_WAITING_BODY), style = MaterialTheme.typography.bodyLarge, color = colors.contentSecondary)
        Text(
            copy(CopyKey.POOL_WAITING_NEXT_WINDOW, mapOf("time" to windowTime)),
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.primary,
        )

        if (failure != null) {
            PodNotice(failure = failure, onRetry = onCheckForPod)
        }

        Spacer(Modifier.weight(1f))

        TextButton(onClick = onLeaveQueue) {
            Text(copy(CopyKey.POOL_WAITING_LEAVE), style = MaterialTheme.typography.bodySmall)
        }
    }
}
