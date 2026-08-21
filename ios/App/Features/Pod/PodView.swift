import SwiftUI
import PodAPI

/// Who is in your pod. Deliberately thin — the week loop is M3 and owns this screen.
///
/// There is no per-member figure here and there will not be one. Members render in join
/// order because it is stable, not because it means anything (docs/DESIGN.md).
struct PodView: View {
    let pod: Pod
    let session: SessionController

    @Environment(\.palette) private var palette
    @Environment(\.copy) private var copy

    private var membersLine: String {
        pod.members.count >= 3 ? copy(.podMembersThree) : copy(.podMembersTwo)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.lg) {
            Text(copy(.podNewlyFormedTitle))
                .podText(PodTokens.TypeScale.display)
                .foregroundStyle(palette(.contentPrimary))

            Text(copy(.podNewlyFormedBody))
                .podText(PodTokens.TypeScale.body)
                .foregroundStyle(palette(.contentSecondary))

            Text(membersLine)
                .podText(PodTokens.TypeScale.caption)
                .foregroundStyle(palette(.contentTertiary))
                .padding(.top, PodTokens.Spacing.md)

            VStack(spacing: PodTokens.Spacing.sm) {
                ForEach(pod.members, id: \.id) { member in
                    memberRow(member)
                }
            }

            if pod.streakWeeks > 0 {
                // Pod-level, always. It reads as weeks together, never as a number next
                // to a person (CLAUDE.md §2 rule 3).
                Text(copy.plural(.statePodWeeksTogether, count: pod.streakWeeks))
                    .podText(PodTokens.TypeScale.caption)
                    .foregroundStyle(palette(.contentSecondary))
            }

            Spacer()

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

    private func memberRow(_ member: PodMember) -> some View {
        HStack(spacing: PodTokens.Spacing.md) {
            Circle()
                .fill(palette(.accentSubtle))
                .frame(width: PodTokens.Size.avatarMd, height: PodTokens.Size.avatarMd)
                .overlay {
                    Text(initial(for: member))
                        .podText(PodTokens.TypeScale.bodyStrong)
                        .foregroundStyle(palette(.accentPrimary))
                }
            Text(member.isYou ? copy(.podYou) : displayName(for: member))
                .podText(PodTokens.TypeScale.body)
                .foregroundStyle(palette(.contentPrimary))
            Spacer()
        }
        .frame(minHeight: PodTokens.Size.minTouchTarget)
        .padding(.horizontal, PodTokens.Spacing.lg)
        .padding(.vertical, PodTokens.Spacing.sm)
        .background(palette(.surfaceRaised), in: .rect(cornerRadius: PodTokens.Radius.md))
        .accessibilityElement(children: .combine)
    }

    private func displayName(for member: PodMember) -> String {
        member.displayName.isEmpty ? copy(.podYou) : member.displayName
    }

    private func initial(for member: PodMember) -> String {
        let name = member.isYou ? copy(.podYou) : displayName(for: member)
        return String(name.prefix(1)).uppercased()
    }
}

#Preview("Pod") {
    PodView(
        pod: Pod(
            id: UUID(),
            state: .active,
            goalCategory: .home,
            timezone: "Europe/Lisbon",
            streakWeeks: 0,
            members: [
                PodMember(id: UUID(), displayName: "Sam", isYou: true, joinedAt: .now),
                PodMember(id: UUID(), displayName: "Alex", isYou: false, joinedAt: .now),
            ],
            createdAt: .now
        ),
        session: PodEnvironment.preview().session
    )
    .environment(\.palette, PodPalette(.light))
}
