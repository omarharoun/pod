package app.pod.feature.week

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import app.pod.api.generated.CopyKey
import app.pod.api.generated.MemberWeek
import app.pod.api.generated.MemberWeekState
import app.pod.api.generated.PodTokens
import app.pod.ui.theme.LocalCopywriter
import app.pod.ui.theme.LocalPodColors

/**
 * The pod's ring: one arc per member, closed only when everyone is in.
 *
 * One ring rather than one per member, deliberately. Three rings side by side are three
 * individual scores however carefully they are drawn. The streak is the pod's
 * (CLAUDE.md §2 rule 3) and this is that rule in a shape — see docs/DESIGN.md.
 */
@Composable
fun PodRing(
    members: List<MemberWeek>,
    streakWeeks: Int,
    modifier: Modifier = Modifier,
) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current
    val posted = members.count { it.state == MemberWeekState.POSTED }

    val track = colors.sunken
    val strokePx = with(LocalDensity.current) { PodTokens.Size.RING_STROKE.dp.toPx() }

    // The gap between segments, in degrees. Enough to read as separate people, small
    // enough that a closed ring still reads as closed.
    val gapDegrees = 6f
    val sweepPerMember = if (members.isEmpty()) 0f else 360f / members.size

    val description = copy(
        CopyKey.A11Y_RING_POSTED,
        mapOf("count" to posted.toString(), "total" to members.size.toString()),
    )

    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .size(PodTokens.Size.RING_DIAMETER.dp)
            .clearAndSetSemantics { contentDescription = description },
    ) {
        // A pod is two or three people and does not change size mid-week, so animating
        // per segment in a loop is stable. Cross-fade over `motion.settle`; nothing here
        // is celebratory.
        val segmentColors = ArrayList<Color>(members.size)
        for (member in members) {
            // The palette has no error role by construction: resting is a cool grey,
            // visibly different from open and deliberately no louder.
            val target = when (member.state) {
                MemberWeekState.POSTED -> colors.statePosted
                MemberWeekState.RESTING -> colors.stateResting
                else -> colors.stateOpen
            }
            val animated by animateColorAsState(
                targetValue = target,
                animationSpec = tween(PodTokens.DurationMs.SETTLE),
                label = "segment",
            )
            segmentColors += animated
        }

        Canvas(Modifier.size(PodTokens.Size.RING_DIAMETER.dp)) {
            val inset = strokePx / 2
            val arcSize = Size(size.width - strokePx, size.height - strokePx)
            val topLeft = Offset(inset, inset)

            drawArc(
                color = track,
                startAngle = 0f,
                sweepAngle = 360f,
                useCenter = false,
                topLeft = topLeft,
                size = arcSize,
                style = Stroke(width = strokePx),
            )
            segmentColors.forEachIndexed { index, color ->
                drawArc(
                    color = color,
                    startAngle = -90f + index * sweepPerMember + gapDegrees / 2,
                    sweepAngle = sweepPerMember - gapDegrees,
                    useCenter = false,
                    topLeft = topLeft,
                    size = arcSize,
                    style = Stroke(width = strokePx, cap = StrokeCap.Round),
                )
            }
        }

        if (streakWeeks > 0) {
            // Pod-level, and attached to the ring rather than to anybody's face.
            Text(
                copy.plural(CopyKey.STATE_POD_WEEKS_TOGETHER, streakWeeks),
                style = MaterialTheme.typography.bodySmall,
                color = colors.contentSecondary,
                textAlign = TextAlign.Center,
                modifier = Modifier.size(PodTokens.Size.RING_DIAMETER.dp - PodTokens.Spacing.XXXL.dp),
            )
        }
    }
}
