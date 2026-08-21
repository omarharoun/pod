import SwiftUI
import PodAPI

/// One choice in a pick-one list.
struct PodChoice: Identifiable, Hashable {
    let id: String
    let label: String
}

/// The pick-one control used by the category, intensity and self-report screens.
///
/// Selecting advances the flow, so there is no button to find (docs/DESIGN.md). Rows are
/// plain text: no numbers, no positions, nothing that tells anyone where they sit
/// relative to anyone else.
struct PodChoiceList: View {
    let choices: [PodChoice]
    @Binding var selection: String?
    var onSelect: () -> Void = {}

    @Environment(\.palette) private var palette
    @Environment(\.copy) private var copy

    var body: some View {
        VStack(spacing: PodTokens.Spacing.sm) {
            ForEach(choices) { choice in
                Button {
                    selection = choice.id
                    onSelect()
                } label: {
                    HStack(spacing: PodTokens.Spacing.md) {
                        Text(choice.label)
                            .podText(PodTokens.TypeScale.body)
                            .foregroundStyle(palette(.contentPrimary))
                            .multilineTextAlignment(.leading)
                        Spacer(minLength: PodTokens.Spacing.sm)
                        if selection == choice.id {
                            Image(systemName: "checkmark")
                                .foregroundStyle(palette(.accentPrimary))
                                .accessibilityLabel(copy(.a11ySelected))
                        }
                    }
                    .frame(maxWidth: .infinity, minHeight: PodTokens.Size.minTouchTarget, alignment: .leading)
                    .padding(.horizontal, PodTokens.Spacing.lg)
                    .padding(.vertical, PodTokens.Spacing.md)
                    .background(
                        selection == choice.id ? palette(.accentSubtle) : palette(.surfaceSunken),
                        in: .rect(cornerRadius: PodTokens.Radius.md)
                    )
                }
                .buttonStyle(.plain)
                .accessibilityAddTraits(selection == choice.id ? [.isButton, .isSelected] : .isButton)
            }
        }
    }
}

#Preview("Choice list") {
    PodChoiceList(
        choices: [
            PodChoice(id: "1", label: "A few minutes"),
            PodChoice(id: "2", label: "Half an hour"),
            PodChoice(id: "3", label: "An hour or two"),
        ],
        selection: .constant("2")
    )
    .padding(PodTokens.Spacing.screenGutter)
    .environment(\.palette, PodPalette(.light))
}
