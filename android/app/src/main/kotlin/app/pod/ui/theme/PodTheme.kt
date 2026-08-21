package app.pod.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import app.pod.api.Copywriter
import app.pod.api.generated.PodTokens
import app.pod.api.generated.PodTypeStyle

/** The copy catalogue, so a composable never builds its own text (CLAUDE.md §2 rule 1). */
val LocalCopywriter = staticCompositionLocalOf<Copywriter> {
    error("no Copywriter in this composition — wrap the tree in PodTheme and provide one")
}

/**
 * The colour roles Material 3's scheme has no slot for.
 *
 * There is deliberately no error/danger role for member state: missing a week is never
 * rendered in the colour of a mistake (CLAUDE.md §2 rule 1).
 */
@Immutable
data class PodColors(
    val canvas: Color,
    val sunken: Color,
    val contentPrimary: Color,
    val contentSecondary: Color,
    val contentTertiary: Color,
    val statePosted: Color,
    val statePostedSubtle: Color,
    val stateOpen: Color,
    val stateOpenSubtle: Color,
    val stateResting: Color,
    val stateRestingSubtle: Color,
    val hairline: Color,
)

val LocalPodColors = staticCompositionLocalOf<PodColors> {
    error("no PodColors in this composition — wrap the tree in PodTheme")
}

private fun role(role: PodTokens.ColorRole, dark: Boolean): Color =
    Color(PodTokens.color(role, dark))

private fun podColors(dark: Boolean) = PodColors(
    canvas = role(PodTokens.ColorRole.SURFACE_CANVAS, dark),
    sunken = role(PodTokens.ColorRole.SURFACE_SUNKEN, dark),
    contentPrimary = role(PodTokens.ColorRole.CONTENT_PRIMARY, dark),
    contentSecondary = role(PodTokens.ColorRole.CONTENT_SECONDARY, dark),
    contentTertiary = role(PodTokens.ColorRole.CONTENT_TERTIARY, dark),
    statePosted = role(PodTokens.ColorRole.STATE_POSTED, dark),
    statePostedSubtle = role(PodTokens.ColorRole.STATE_POSTED_SUBTLE, dark),
    stateOpen = role(PodTokens.ColorRole.STATE_OPEN, dark),
    stateOpenSubtle = role(PodTokens.ColorRole.STATE_OPEN_SUBTLE, dark),
    stateResting = role(PodTokens.ColorRole.STATE_RESTING, dark),
    stateRestingSubtle = role(PodTokens.ColorRole.STATE_RESTING_SUBTLE, dark),
    hairline = role(PodTokens.ColorRole.BORDER_HAIRLINE, dark),
)

private fun materialScheme(dark: Boolean) = if (dark) {
    darkColorScheme(
        primary = role(PodTokens.ColorRole.ACCENT_PRIMARY, true),
        onPrimary = role(PodTokens.ColorRole.CONTENT_ON_ACCENT, true),
        primaryContainer = role(PodTokens.ColorRole.ACCENT_SUBTLE, true),
        onPrimaryContainer = role(PodTokens.ColorRole.CONTENT_PRIMARY, true),
        background = role(PodTokens.ColorRole.SURFACE_CANVAS, true),
        onBackground = role(PodTokens.ColorRole.CONTENT_PRIMARY, true),
        surface = role(PodTokens.ColorRole.SURFACE_RAISED, true),
        onSurface = role(PodTokens.ColorRole.CONTENT_PRIMARY, true),
        surfaceVariant = role(PodTokens.ColorRole.SURFACE_SUNKEN, true),
        onSurfaceVariant = role(PodTokens.ColorRole.CONTENT_SECONDARY, true),
        outline = role(PodTokens.ColorRole.BORDER_STRONG, true),
    )
} else {
    lightColorScheme(
        primary = role(PodTokens.ColorRole.ACCENT_PRIMARY, false),
        onPrimary = role(PodTokens.ColorRole.CONTENT_ON_ACCENT, false),
        primaryContainer = role(PodTokens.ColorRole.ACCENT_SUBTLE, false),
        onPrimaryContainer = role(PodTokens.ColorRole.CONTENT_PRIMARY, false),
        background = role(PodTokens.ColorRole.SURFACE_CANVAS, false),
        onBackground = role(PodTokens.ColorRole.CONTENT_PRIMARY, false),
        surface = role(PodTokens.ColorRole.SURFACE_RAISED, false),
        onSurface = role(PodTokens.ColorRole.CONTENT_PRIMARY, false),
        surfaceVariant = role(PodTokens.ColorRole.SURFACE_SUNKEN, false),
        onSurfaceVariant = role(PodTokens.ColorRole.CONTENT_SECONDARY, false),
        outline = role(PodTokens.ColorRole.BORDER_STRONG, false),
    )
}

private fun PodTypeStyle.toTextStyle() = TextStyle(
    // sp, so the reader's font scale applies. Never dp, never a hardcoded size.
    fontSize = size.sp,
    lineHeight = lineHeight.sp,
    fontWeight = FontWeight(weight),
    letterSpacing = (tracking / size).em,
)

private val podTypography = Typography(
    displaySmall = PodTokens.TypeScale.DISPLAY.toTextStyle(),
    titleLarge = PodTokens.TypeScale.TITLE.toTextStyle(),
    titleMedium = PodTokens.TypeScale.HEADING.toTextStyle(),
    bodyLarge = PodTokens.TypeScale.BODY.toTextStyle(),
    labelLarge = PodTokens.TypeScale.BODY_STRONG.toTextStyle(),
    bodySmall = PodTokens.TypeScale.CAPTION.toTextStyle(),
    labelSmall = PodTokens.TypeScale.MICRO.toTextStyle(),
)

/**
 * Material 3 wired to Pod's tokens.
 *
 * Dynamic colour is deliberately off: the palette is part of the product's promise, not a
 * surface for the launcher wallpaper to redecorate.
 */
@Composable
fun PodTheme(
    dark: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit,
) {
    CompositionLocalProvider(LocalPodColors provides podColors(dark)) {
        MaterialTheme(
            colorScheme = materialScheme(dark),
            typography = podTypography,
            content = content,
        )
    }
}
