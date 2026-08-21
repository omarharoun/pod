import SwiftUI
import PodAPI

/// The week. This is the product (CLAUDE.md §7).
struct WeekView: View {
    let controller: WeekController
    let pod: Pod
    let session: SessionController

    @Environment(\.palette) private var palette
    @Environment(\.copy) private var copy
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.pushRegistrar) private var pushRegistrar
    @State private var isCapturing = false
    @State private var isEditingAction = false
    @State private var draftAction = ""

    private var me: PodMember? {
        pod.members.first(where: \.isYou)
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: PodTokens.Spacing.xl) {
                switch controller.phase {
                case .loading, .empty:
                    ProgressView()
                        .tint(palette(.accentPrimary))
                        .frame(maxWidth: .infinity, minHeight: PodTokens.Size.ringDiameter)
                        .accessibilityLabel(copy(.a11yLoading))
                case .ready(let week):
                    content(week)
                }

                if let failure = controller.failure {
                    PodNotice(error: failure) {
                        Task { await controller.refresh() }
                    }
                }
            }
            .padding(.horizontal, PodTokens.Spacing.screenGutter)
            .padding(.vertical, PodTokens.Spacing.xxl)
        }
        .background(palette(.surfaceCanvas))
        .task {
            await controller.load()
            // Asked here rather than at launch: this is the first moment the question
            // has a subject — two other people who might do something worth knowing.
            await pushRegistrar?.askIfNeeded()
        }
        .onChange(of: scenePhase) { _, phase in
            if phase == .active {
                Task { await controller.refresh() }
            }
        }
        .sheet(isPresented: $isCapturing) {
            ProofCaptureSheet { captured in
                Task {
                    await controller.post(
                        kind: captured.kind,
                        contentType: captured.contentType,
                        data: captured.data,
                        durationMs: captured.durationMs
                    )
                }
            }
        }
    }

    @ViewBuilder
    private func content(_ week: Week) -> some View {
        if week.index == 1 && week.podProgress.posted == 0 {
            newlyFormedBanner
        }
        header(week)
        ring(week)
        members(week)
        postButton(week)

        Button(copy(.authSignOutAction)) {
            Task { await session.signOut() }
        }
        .podText(PodTokens.TypeScale.caption)
        .foregroundStyle(palette(.contentTertiary))
        .frame(maxWidth: .infinity)
        .padding(.top, PodTokens.Spacing.xl)
    }

    private var newlyFormedBanner: some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.xs) {
            Text(copy(.podNewlyFormedTitle))
                .podText(PodTokens.TypeScale.heading)
                .foregroundStyle(palette(.contentPrimary))
            Text(copy(.podNewlyFormedBody))
                .podText(PodTokens.TypeScale.caption)
                .foregroundStyle(palette(.contentSecondary))
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(PodTokens.Spacing.lg)
        .background(palette(.accentSubtle), in: .rect(cornerRadius: PodTokens.Radius.md))
        .accessibilityElement(children: .combine)
    }

    // MARK: - The one thing

    @ViewBuilder
    private func header(_ week: Week) -> some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.sm) {
            Text(copy(.weekTitle))
                .podText(PodTokens.TypeScale.micro)
                .foregroundStyle(palette(.contentTertiary))

            if isEditingAction {
                actionEditor(week)
            } else {
                Text(week.actionText)
                    .podText(PodTokens.TypeScale.title)
                    .foregroundStyle(palette(.contentPrimary))

                if week.actionIsDefault && week.actionEditable {
                    // An invitation, never a nag.
                    Text(copy(.weekActionNamePrompt))
                        .podText(PodTokens.TypeScale.caption)
                        .foregroundStyle(palette(.contentSecondary))
                }
                if week.actionEditable {
                    Button(week.actionIsDefault ? copy(.weekActionNameCta) : copy(.weekActionEditCta)) {
                        draftAction = week.actionIsDefault ? "" : week.actionText
                        isEditingAction = true
                    }
                    .podText(PodTokens.TypeScale.caption)
                    .foregroundStyle(palette(.accentPrimary))
                } else if week.isOpen {
                    Text(copy(.weekActionLockedNote))
                        .podText(PodTokens.TypeScale.micro)
                        .foregroundStyle(palette(.contentTertiary))
                }
            }

            Text(closesLine(week))
                .podText(PodTokens.TypeScale.caption)
                .foregroundStyle(palette(.contentSecondary))
                .padding(.top, PodTokens.Spacing.xs)
        }
    }

    private func actionEditor(_ week: Week) -> some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.sm) {
            TextField(copy(.weekActionPlaceholder), text: $draftAction, axis: .vertical)
                .podText(PodTokens.TypeScale.body)
                .foregroundStyle(palette(.contentPrimary))
                .lineLimit(1...3)
                .padding(PodTokens.Spacing.lg)
                .background(palette(.surfaceSunken), in: .rect(cornerRadius: PodTokens.Radius.md))

            HStack(spacing: PodTokens.Spacing.lg) {
                PodPrimaryButton(
                    title: copy(.weekActionSave),
                    isEnabled: draftAction.trimmingCharacters(in: .whitespacesAndNewlines).count >= 3,
                    isWorking: controller.isSavingAction
                ) {
                    Task {
                        await controller.setAction(draftAction)
                        isEditingAction = false
                    }
                }
                Button(copy(.commonCancel)) { isEditingAction = false }
                    .podText(PodTokens.TypeScale.caption)
                    .foregroundStyle(palette(.contentSecondary))
            }
        }
    }

    private func closesLine(_ week: Week) -> String {
        guard week.isOpen else { return copy(.weekClosed) }
        let days = week.daysRemaining()
        return days == 0 ? copy(.weekClosesToday) : copy.plural(.weekClosesIn, count: days)
    }

    // MARK: - The ring

    @ViewBuilder
    private func ring(_ week: Week) -> some View {
        VStack(spacing: PodTokens.Spacing.md) {
            PodRing(members: week.members, streakWeeks: pod.streakWeeks)

            if week.podCompleted {
                Text(copy(.weekPodCompleted))
                    .podText(PodTokens.TypeScale.bodyStrong)
                    .foregroundStyle(palette(.statePosted))
            } else if !week.isOpen {
                // Nothing went wrong, so nothing reports that it did.
                Text(copy(.statePodRestartingNote))
                    .podText(PodTokens.TypeScale.caption)
                    .foregroundStyle(palette(.contentSecondary))
            }
        }
        .frame(maxWidth: .infinity)
    }

    // MARK: - Who is in

    @ViewBuilder
    private func members(_ week: Week) -> some View {
        VStack(spacing: PodTokens.Spacing.sm) {
            ForEach(week.members, id: \.memberId) { member in
                memberRow(member)
            }
        }
    }

    private func memberRow(_ member: MemberWeek) -> some View {
        let isYou = member.memberId == me?.id
        let name = isYou ? copy(.podYou) : displayName(for: member.memberId)

        return HStack(spacing: PodTokens.Spacing.md) {
            Circle()
                .fill(fill(for: member.state))
                .frame(width: PodTokens.Spacing.md, height: PodTokens.Spacing.md)
            Text(name)
                .podText(PodTokens.TypeScale.body)
                .foregroundStyle(palette(.contentPrimary))
            Spacer()
            Text(shortState(member.state))
                .podText(PodTokens.TypeScale.caption)
                .foregroundStyle(palette(.contentSecondary))
        }
        .frame(minHeight: PodTokens.Size.minTouchTarget)
        .padding(.horizontal, PodTokens.Spacing.lg)
        .background(palette(.surfaceRaised), in: .rect(cornerRadius: PodTokens.Radius.md))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityLabel(for: member, name: name))
    }

    private func displayName(for id: UUID) -> String {
        pod.members.first { $0.id == id }?.displayName ?? ""
    }

    /// The subtle fill behind a state dot. There is no error role in the palette: resting
    /// is a cool grey, visibly different from open and deliberately no louder.
    private func fill(for state: MemberWeekState) -> Color {
        switch state {
        case .posted: palette(.statePostedSubtle)
        case .resting: palette(.stateRestingSubtle)
        default: palette(.stateOpenSubtle)
        }
    }

    private func shortState(_ state: MemberWeekState) -> String {
        switch state {
        case .posted: copy(.stateMemberPostedShort)
        case .resting: copy(.stateMemberRestingShort)
        default: copy(.stateMemberOpenShort)
        }
    }

    private func accessibilityLabel(for member: MemberWeek, name: String) -> String {
        switch member.state {
        case .posted: copy(.a11yMemberPosted, ["name": name])
        case .resting: copy(.a11yMemberResting, ["name": name])
        default: copy(.a11yMemberOpen, ["name": name])
        }
    }

    // MARK: - Posting

    @ViewBuilder
    private func postButton(_ week: Week) -> some View {
        if let me, controller.canPost(userID: me.id) {
            VStack(spacing: PodTokens.Spacing.sm) {
                PodPrimaryButton(
                    title: copy(.weekPostCta),
                    isWorking: controller.isPosting
                ) {
                    isCapturing = true
                }
                .accessibilityLabel(copy(.a11yPostProof))

                Text(copy(.weekPostEphemeralNote))
                    .podText(PodTokens.TypeScale.micro)
                    .foregroundStyle(palette(.contentTertiary))
                    .multilineTextAlignment(.center)
            }
        } else if let me, controller.me(userID: me.id)?.state == .posted {
            Text(copy(.stateMemberYouPostedDetail))
                .podText(PodTokens.TypeScale.bodyStrong)
                .foregroundStyle(palette(.statePosted))
                .frame(maxWidth: .infinity)
        }
    }
}

#Preview("The week — one of two in") {
    let pod = Pod(
        id: UUID(),
        state: .active,
        goalCategory: .home,
        timezone: "Europe/Lisbon",
        streakWeeks: 3,
        members: [
            PodMember(id: UUID(), displayName: "Sam", isYou: true, joinedAt: .now),
            PodMember(id: UUID(), displayName: "Alex", isYou: false, joinedAt: .now),
        ],
        createdAt: .now
    )
    let environment = PodEnvironment.preview()
    let controller = environment.weekController(podID: pod.id)
    controller.preview(phase: .ready(Week(
        id: UUID(),
        podId: pod.id,
        index: 4,
        actionText: "Clear one shelf",
        actionIsDefault: false,
        actionEditable: false,
        opensAt: .now.addingTimeInterval(-86_400),
        closesAt: .now.addingTimeInterval(3 * 86_400),
        members: [
            MemberWeek(memberId: pod.members[0].id, state: .posted, postedAt: .now),
            MemberWeek(memberId: pod.members[1].id, state: .open),
        ],
        podCompleted: false
    )))
    return WeekView(controller: controller, pod: pod, session: environment.session)
        .environment(\.palette, PodPalette(.light))
}
