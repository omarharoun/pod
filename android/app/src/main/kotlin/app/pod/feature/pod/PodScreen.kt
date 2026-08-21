package app.pod.feature.pod

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.weight
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.unit.dp
import app.pod.api.generated.CopyKey
import app.pod.api.generated.Pod
import app.pod.api.generated.PodMember
import app.pod.api.generated.PodTokens
import app.pod.ui.theme.LocalCopywriter
import app.pod.ui.theme.LocalPodColors

/**
 * Who is in your pod. Deliberately thin — the week loop is M3 and owns this screen.
 *
 * There is no per-member figure here and there will not be one. Members render in join
 * order because it is stable, not because it means anything (docs/DESIGN.md).
 */
@Composable
fun PodScreen(
    pod: Pod,
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
        Text(
            copy(CopyKey.POD_NEWLY_FORMED_TITLE),
            style = MaterialTheme.typography.displaySmall,
            color = colors.contentPrimary,
        )
        Text(
            copy(CopyKey.POD_NEWLY_FORMED_BODY),
            style = MaterialTheme.typography.bodyLarge,
            color = colors.contentSecondary,
        )
        Text(
            copy(if (pod.members.size >= 3) CopyKey.POD_MEMBERS_THREE else CopyKey.POD_MEMBERS_TWO),
            style = MaterialTheme.typography.bodySmall,
            color = colors.contentTertiary,
        )

        Column(verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.SM.dp)) {
            for (member in pod.members) {
                MemberRow(member)
            }
        }

        if (pod.streakWeeks > 0) {
            // Pod-level, always. It reads as weeks together, never as a number next to a
            // person (CLAUDE.md §2 rule 3).
            Text(
                copy.plural(CopyKey.STATE_POD_WEEKS_TOGETHER, pod.streakWeeks),
                style = MaterialTheme.typography.bodySmall,
                color = colors.contentSecondary,
            )
        }

        Spacer(Modifier.weight(1f))

        TextButton(onClick = onSignOut) {
            Text(copy(CopyKey.AUTH_SIGN_OUT_ACTION), style = MaterialTheme.typography.bodySmall)
        }
    }
}

@Composable
private fun MemberRow(member: PodMember) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current
    val name = if (member.isYou) copy(CopyKey.POD_YOU) else member.displayName.ifEmpty { copy(CopyKey.POD_YOU) }

    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(PodTokens.Spacing.MD.dp),
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = PodTokens.Size.MIN_TOUCH_TARGET.dp)
            .background(MaterialTheme.colorScheme.surface, RoundedCornerShape(PodTokens.Radius.MD.dp))
            .padding(horizontal = PodTokens.Spacing.LG.dp, vertical = PodTokens.Spacing.SM.dp)
            .clearAndSetSemantics { contentDescription = name },
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(PodTokens.Size.AVATAR_MD.dp)
                .background(MaterialTheme.colorScheme.primaryContainer, CircleShape),
        ) {
            Text(
                name.take(1).uppercase(),
                style = MaterialTheme.typography.labelLarge,
                color = MaterialTheme.colorScheme.primary,
            )
        }
        Text(name, style = MaterialTheme.typography.bodyLarge, color = colors.contentPrimary)
    }
}
