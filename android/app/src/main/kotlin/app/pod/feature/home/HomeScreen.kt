package app.pod.feature.home

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.weight
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import app.pod.api.generated.CopyKey
import app.pod.api.generated.PodTokens
import app.pod.api.generated.User
import app.pod.ui.theme.LocalCopywriter
import app.pod.ui.theme.LocalPodColors

/**
 * What M1 has to show for itself: you're in, and nothing is asked of you yet.
 *
 * A placeholder on purpose. The real home screen is M3's week loop, and building a rich
 * one before that exists would be building the wrong thing twice (docs/DESIGN.md).
 */
@Composable
fun HomeScreen(
    user: User,
    onSignOut: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current

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

        Text(
            copy(CopyKey.HOME_PLACEHOLDER_TITLE),
            style = MaterialTheme.typography.displaySmall,
            color = colors.contentPrimary,
        )
        Text(
            copy(CopyKey.HOME_PLACEHOLDER_BODY),
            style = MaterialTheme.typography.bodyLarge,
            color = colors.contentSecondary,
        )

        Spacer(Modifier.weight(1f))

        Text(
            copy(CopyKey.HOME_SIGNED_IN_AS, mapOf("email" to user.email)),
            style = MaterialTheme.typography.bodySmall,
            color = colors.contentTertiary,
        )
        TextButton(onClick = onSignOut) {
            Text(copy(CopyKey.AUTH_SIGN_OUT_ACTION), style = MaterialTheme.typography.bodySmall)
        }
    }
}
