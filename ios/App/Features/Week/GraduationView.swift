import SwiftUI
import PodAPI

/// The pod reached the end of its cycle.
///
/// Its own screen rather than a modal over the week (docs/DESIGN.md, variant B). What
/// would be behind a modal is a week that has ended — a proof grid whose proof was
/// deleted, an action nobody can post against — so dismissing it would land someone on a
/// dead screen with the choice somewhere they have to go looking for.
///
/// This is the only moment in the product where celebration is the right register, and it
/// is still not a ranking. Nothing here is per-member: no best week, no who-posted-most,
/// no personal figure of any kind (`CLAUDE.md` §2 rule 3).
struct GraduationView: View {
    let pod: Pod
    let controller: GraduationController

    @Environment(\.palette) private var palette
    @Environment(\.copy) private var copy

    private var cycleLength: Int { pod.cycle?.lengthWeeks ?? 0 }

    private var deadline: String {
        guard let decideBy = pod.continuation?.decideBy else { return "" }
        return decideBy.formatted(date: .abbreviated, time: .omitted)
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: PodTokens.Spacing.sectionGap) {
                finished

                if let answer = pod.continuation?.youAreContinuing {
                    answered(continuing: answer)
                } else {
                    choice
                }

                if let failure = controller.failure {
                    PodNotice(error: failure)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, PodTokens.Spacing.screenGutter)
            .padding(.vertical, PodTokens.Spacing.xxxl)
        }
        .background(palette(.surfaceCanvas))
    }

    private var finished: some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.md) {
            Text(copy(.graduationTitle, ["count": String(cycleLength)]))
                .podText(PodTokens.TypeScale.display)
                .foregroundStyle(palette(.contentPrimary))

            Text(copy(.graduationBody))
                .podText(PodTokens.TypeScale.body)
                .foregroundStyle(palette(.contentSecondary))

            // The fork that matters. A pod that completed one week of six still finished
            // the cycle, and the screen may not imply otherwise — "you still got here"
            // is what refuses to make the count the point.
            Text(pod.streakWeeks > 0
                ? copy(.graduationStreak, ["count": String(pod.streakWeeks)])
                : copy(.graduationStreakNone))
                .podText(PodTokens.TypeScale.bodyStrong)
                .foregroundStyle(palette(.statePosted))
                .padding(.top, PodTokens.Spacing.sm)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// Both buttons are equal weight. "Everyone decides for themselves" is there to
    /// pre-empt the thought the product most wants to avoid — *if I say no, am I letting
    /// them down* (docs/DESIGN.md).
    private var choice: some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.md) {
            Text(copy(.graduationChoiceTitle, ["count": String(cycleLength)]))
                .podText(PodTokens.TypeScale.heading)
                .foregroundStyle(palette(.contentPrimary))

            Text(copy(.graduationChoiceBody))
                .podText(PodTokens.TypeScale.body)
                .foregroundStyle(palette(.contentSecondary))

            VStack(spacing: PodTokens.Spacing.sm) {
                PodPrimaryButton(title: copy(.graduationChoiceYes), isWorking: controller.isWorking) {
                    Task { await controller.answer(continuing: true) }
                }
                PodSecondaryButton(title: copy(.graduationChoiceNo), isEnabled: !controller.isWorking) {
                    Task { await controller.answer(continuing: false) }
                }
            }
            .padding(.top, PodTokens.Spacing.sm)

            deadlineNote
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// After answering. A count, never a list — "Alex is not continuing" is the sentence
    /// this whole design exists to make impossible, which is why the API carries counts
    /// and there is no field here that could hold a name (ADR-023).
    private func answered(continuing: Bool) -> some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.md) {
            Text(copy(continuing ? .graduationWaitingTitle : .graduationDeclinedTitle))
                .podText(PodTokens.TypeScale.heading)
                .foregroundStyle(palette(.contentPrimary))

            Text(copy(continuing ? .graduationWaitingBody : .graduationDeclinedBody))
                .podText(PodTokens.TypeScale.body)
                .foregroundStyle(palette(.contentSecondary))

            if continuing, let count = pod.continuation?.continuing, count > 0 {
                Text(copy(.graduationWaitingCount, ["count": String(count)]))
                    .podText(PodTokens.TypeScale.caption)
                    .foregroundStyle(palette(.contentTertiary))
            }

            PodSecondaryButton(title: copy(.graduationChange), isEnabled: !controller.isWorking) {
                Task { await controller.answer(continuing: !continuing) }
            }
            .padding(.top, PodTokens.Spacing.sm)

            deadlineNote
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// Stated, never counted down. A countdown here would be pressure, and pressure is
    /// what this product is built not to apply.
    @ViewBuilder
    private var deadlineNote: some View {
        if !deadline.isEmpty {
            Text(copy(.graduationChoiceDeadline, ["date": deadline]))
                .podText(PodTokens.TypeScale.caption)
                .foregroundStyle(palette(.contentTertiary))
        }
    }
}

#Preview("Six weeks, everyone in") {
    let pod = Pod.previewGraduated(streakWeeks: 6, youAnswered: nil)
    return GraduationView(
        pod: pod,
        controller: GraduationController(client: PodEnvironment.preview().client, pod: pod)
    )
    .environment(\.palette, PodPalette(.light))
    .environment(\.copy, Copywriter.bundled)
}

#Preview("Six weeks, some of them landed") {
    let pod = Pod.previewGraduated(streakWeeks: 0, youAnswered: nil)
    return GraduationView(
        pod: pod,
        controller: GraduationController(client: PodEnvironment.preview().client, pod: pod)
    )
    .environment(\.palette, PodPalette(.light))
    .environment(\.copy, Copywriter.bundled)
}

#Preview("Answered, waiting on the others") {
    let pod = Pod.previewGraduated(streakWeeks: 4, youAnswered: true)
    return GraduationView(
        pod: pod,
        controller: GraduationController(client: PodEnvironment.preview().client, pod: pod)
    )
    .environment(\.palette, PodPalette(.dark))
    .environment(\.copy, Copywriter.bundled)
    .preferredColorScheme(.dark)
}

#Preview("Stopping here") {
    let pod = Pod.previewGraduated(streakWeeks: 2, youAnswered: false)
    return GraduationView(
        pod: pod,
        controller: GraduationController(client: PodEnvironment.preview().client, pod: pod)
    )
    .environment(\.palette, PodPalette(.light))
    .environment(\.copy, Copywriter.bundled)
}

private extension Pod {
    /// A graduated pod for previews. `youAnswered` nil means the choice is still open.
    static func previewGraduated(streakWeeks: Int, youAnswered: Bool?) -> Pod {
        let now = Date(timeIntervalSince1970: 1_787_000_000)
        return Pod(
            id: UUID(uuidString: "5C1F2A7C-1111-4111-8111-111111111111")!,
            state: .completed,
            goalCategory: .home,
            timezone: "Europe/Lisbon",
            streakWeeks: streakWeeks,
            members: [
                PodMember(id: UUID(), displayName: "Sam", isYou: true, joinedAt: now),
                PodMember(id: UUID(), displayName: "Alex", isYou: false, joinedAt: now),
                PodMember(id: UUID(), displayName: "Remi", isYou: false, joinedAt: now),
            ],
            cycle: Cycle(lengthWeeks: 6, weeksCompleted: 6, endsAt: now),
            continuation: Continuation(
                continuing: youAnswered == true ? 2 : 1,
                undecided: 1,
                youAnswered: youAnswered != nil,
                youAreContinuing: youAnswered,
                decideBy: now.addingTimeInterval(7 * 24 * 60 * 60)
            ),
            createdAt: now.addingTimeInterval(-42 * 24 * 60 * 60),
            completedAt: now
        )
    }
}
