import SwiftUI
import PodAPI

/// The pod's ring: one arc per member, closed only when everyone is in.
///
/// One ring rather than one per member, deliberately. Three rings side by side are three
/// individual scores however carefully they are drawn, and the first thing anyone does
/// with three of anything is compare them. The streak is the pod's (`CLAUDE.md` §2 rule 3)
/// and this is that rule in a shape — see docs/DESIGN.md.
struct PodRing: View {
    let members: [MemberWeek]
    let streakWeeks: Int

    @Environment(\.palette) private var palette
    @Environment(\.copy) private var copy
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    /// The gap between segments, as a fraction of the circle. Enough to read as separate
    /// people, small enough that a closed ring still reads as closed.
    private let gap = 0.02

    private var posted: Int {
        members.filter { $0.state == .posted }.count
    }

    var body: some View {
        ZStack {
            Circle()
                .stroke(palette(.surfaceSunken), lineWidth: PodTokens.Size.ringStroke)

            ForEach(Array(members.enumerated()), id: \.element.memberId) { index, member in
                Circle()
                    .trim(from: start(index), to: end(index))
                    .stroke(
                        color(for: member.state),
                        style: StrokeStyle(lineWidth: PodTokens.Size.ringStroke, lineCap: .round)
                    )
                    .rotationEffect(.degrees(-90))
                    .animation(
                        reduceMotion ? .none : .easeOut(duration: PodTokens.Duration.settle),
                        value: member.state
                    )
            }

            if streakWeeks > 0 {
                // Pod-level, and attached to the ring rather than to anybody's face.
                Text(copy.plural(.statePodWeeksTogether, count: streakWeeks))
                    .podText(PodTokens.TypeScale.caption)
                    .foregroundStyle(palette(.contentSecondary))
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, PodTokens.Spacing.xl)
            }
        }
        .frame(width: PodTokens.Size.ringDiameter, height: PodTokens.Size.ringDiameter)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(copy(.a11yRingPosted, [
            "count": String(posted),
            "total": String(members.count),
        ]))
    }

    private func segment(_ index: Int) -> (start: Double, end: Double) {
        let width = 1.0 / Double(max(members.count, 1))
        return (Double(index) * width, Double(index + 1) * width)
    }

    private func start(_ index: Int) -> CGFloat {
        CGFloat(segment(index).start + gap / 2)
    }

    private func end(_ index: Int) -> CGFloat {
        CGFloat(segment(index).end - gap / 2)
    }

    /// The palette has no error role by construction. `resting` is a cool grey — visibly
    /// different from `open` because it is a different fact, and deliberately no louder.
    private func color(for state: MemberWeekState) -> Color {
        switch state {
        case .posted: palette(.statePosted)
        case .resting: palette(.stateResting)
        default: palette(.stateOpen)
        }
    }
}

#Preview("Ring — one of two in") {
    PodRing(
        members: [
            MemberWeek(memberId: UUID(), state: .posted, postedAt: .now),
            MemberWeek(memberId: UUID(), state: .open),
        ],
        streakWeeks: 0
    )
    .padding(PodTokens.Spacing.xxl)
    .environment(\.palette, PodPalette(.light))
}

#Preview("Ring — a pod of three, six weeks in") {
    PodRing(
        members: [
            MemberWeek(memberId: UUID(), state: .posted, postedAt: .now),
            MemberWeek(memberId: UUID(), state: .posted, postedAt: .now),
            MemberWeek(memberId: UUID(), state: .resting),
        ],
        streakWeeks: 6
    )
    .padding(PodTokens.Spacing.xxl)
    .environment(\.palette, PodPalette(.light))
}
