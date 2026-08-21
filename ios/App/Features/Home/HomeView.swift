import SwiftUI
import PodAPI

/// What M1 has to show for itself: you're in, and nothing is asked of you yet.
///
/// This screen is a placeholder on purpose. The real one is M3's week loop, and building
/// a rich home screen before that exists would be building the wrong thing twice.
struct HomeView: View {
    let session: SessionController
    let user: User

    @Environment(\.palette) private var palette
    @Environment(\.copy) private var copy

    var body: some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.lg) {
            Spacer()

            Text(copy(.homePlaceholderTitle))
                .podText(PodTokens.TypeScale.display)
                .foregroundStyle(palette(.contentPrimary))

            Text(copy(.homePlaceholderBody))
                .podText(PodTokens.TypeScale.body)
                .foregroundStyle(palette(.contentSecondary))

            Spacer()

            Text(copy(.homeSignedInAs, ["email": user.email]))
                .podText(PodTokens.TypeScale.caption)
                .foregroundStyle(palette(.contentTertiary))

            Button(copy(.authSignOutAction)) {
                Task { await session.signOut() }
            }
            .podText(PodTokens.TypeScale.caption)
            .foregroundStyle(palette(.accentPrimary))
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, PodTokens.Spacing.screenGutter)
        .padding(.vertical, PodTokens.Spacing.xxxl)
        .background(palette(.surfaceCanvas))
    }
}

#Preview("Home") {
    let user = User(
        id: UUID(),
        email: "sam@example.com",
        createdAt: .now,
        onboardingComplete: false
    )
    let environment = PodEnvironment.preview(state: .signedIn(user))
    return HomeView(session: environment.session, user: user)
        .environment(\.palette, PodPalette(.light))
}
