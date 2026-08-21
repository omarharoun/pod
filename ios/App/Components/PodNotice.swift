import SwiftUI
import PodAPI

/// How a failure is shown: a calm line under the field, never a red alert.
///
/// The wording comes from the server as a key and is resolved here (ADR-003). Nothing on
/// this surface tells anyone they did something wrong (CLAUDE.md §2 rule 1).
struct PodNotice: View {
    let error: APIError
    var onRetry: (() -> Void)?

    @Environment(\.palette) private var palette
    @Environment(\.copy) private var copy

    var body: some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.sm) {
            Text(copy(error.titleKey))
                .podText(PodTokens.TypeScale.bodyStrong)
                .foregroundStyle(palette(.contentPrimary))
            Text(copy(error.bodyKey, error.params))
                .podText(PodTokens.TypeScale.caption)
                .foregroundStyle(palette(.contentSecondary))
            if let onRetry, error.isRetryable {
                Button(copy(.errorNetworkRetry), action: onRetry)
                    .podText(PodTokens.TypeScale.caption)
                    .foregroundStyle(palette(.accentPrimary))
                    .padding(.top, PodTokens.Spacing.xs)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(PodTokens.Spacing.lg)
        .background(palette(.surfaceSunken), in: .rect(cornerRadius: PodTokens.Radius.md))
        .accessibilityElement(children: .combine)
    }
}
