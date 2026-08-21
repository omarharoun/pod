package app.pod.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import app.pod.api.generated.PodTokens
import app.pod.ui.theme.LocalPodColors

/** One choice in a pick-one list. */
data class PodChoice(val id: String, val label: String)

/**
 * The pick-one control used by the category, intensity and self-report screens.
 *
 * Selecting advances the flow, so there is no button to find (docs/DESIGN.md). Rows are
 * plain text: no numbers, no positions, nothing that says where anyone sits relative to
 * anyone else.
 */
@Composable
fun PodChoiceList(
    choices: List<PodChoice>,
    selected: String?,
    onSelect: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val colors = LocalPodColors.current

    Column(
        verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.SM.dp),
        modifier = modifier.fillMaxWidth(),
    ) {
        for (choice in choices) {
            val isSelected = choice.id == selected
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(min = PodTokens.Size.MIN_TOUCH_TARGET.dp)
                    .background(
                        if (isSelected) {
                            MaterialTheme.colorScheme.primaryContainer
                        } else {
                            colors.sunken
                        },
                        RoundedCornerShape(PodTokens.Radius.MD.dp),
                    )
                    .clickable { onSelect(choice.id) }
                    .padding(
                        horizontal = PodTokens.Spacing.LG.dp,
                        vertical = PodTokens.Spacing.MD.dp,
                    )
                    .semantics { this.selected = isSelected },
            ) {
                Text(
                    choice.label,
                    style = MaterialTheme.typography.bodyLarge,
                    color = colors.contentPrimary,
                )
            }
        }
    }
}
