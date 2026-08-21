import SwiftUI
import PodAPI

/// Resolves a colour role against the current appearance.
///
/// Every colour in the app comes through here. A literal `Color(red:...)` in a view is a
/// bug — the token goes in `design/tokens.json` first (CLAUDE.md §Step 3).
struct PodPalette: Sendable {
    let scheme: PodColorScheme

    init(_ colorScheme: ColorScheme) {
        self.scheme = colorScheme == .dark ? .dark : .light
    }

    func callAsFunction(_ role: PodTokens.ColorRole) -> Color {
        let value = PodTokens.color(role, scheme)
        return Color(.sRGB, red: value.red, green: value.green, blue: value.blue, opacity: value.opacity)
    }
}

private struct PodPaletteKey: EnvironmentKey {
    static let defaultValue = PodPalette(.light)
}

extension EnvironmentValues {
    var palette: PodPalette {
        get { self[PodPaletteKey.self] }
        set { self[PodPaletteKey.self] = newValue }
    }
}

/// Applies a type token, scaled by the reader's Dynamic Type setting.
///
/// The token sizes are baselines, never fixed points (`design/tokens.json`). Everything
/// is related to `.body` for now; when a screen needs headline-relative scaling, add the
/// modifier for it rather than pinning a size.
private struct PodTextStyle: ViewModifier {
    let style: PodTypeStyle
    @ScaledMetric(relativeTo: .body) private var scale: CGFloat = 1

    func body(content: Content) -> some View {
        content
            .font(.system(size: style.size * scale, weight: weight))
            .tracking(style.tracking)
            .lineSpacing(max(0, style.lineHeight - style.size) * scale)
    }

    private var weight: Font.Weight {
        switch style.weight {
        case ..<400: .light
        case 400..<500: .regular
        case 500..<600: .medium
        case 600..<700: .semibold
        default: .bold
        }
    }
}

extension View {
    func podText(_ style: PodTypeStyle) -> some View {
        modifier(PodTextStyle(style: style))
    }
}
