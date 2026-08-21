import SwiftUI
import PodAPI

/// Waiting for the next matching window.
///
/// The window time comes from the server (`pool_ticket.next_window_at`), never from a
/// client constant: an hour is the server's policy. Saying when lets someone close the
/// app, which is the right thing to do here — a spinner would invite pull-to-refresh for
/// a job that runs on a schedule (CLAUDE.md §5, docs/DESIGN.md).
struct WaitingView: View {
    let controller: OnboardingController
    let ticket: PoolTicket

    @Environment(\.palette) private var palette
    @Environment(\.copy) private var copy
    @Environment(\.scenePhase) private var scenePhase

    private var windowTime: String {
        ticket.nextWindowAt.formatted(date: .omitted, time: .shortened)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: PodTokens.Spacing.lg) {
            Spacer()

            Text(copy(.poolWaitingTitle))
                .podText(PodTokens.TypeScale.title)
                .foregroundStyle(palette(.contentPrimary))

            Text(copy(.poolWaitingBody))
                .podText(PodTokens.TypeScale.body)
                .foregroundStyle(palette(.contentSecondary))

            Text(copy(.poolWaitingNextWindow, ["time": windowTime]))
                .podText(PodTokens.TypeScale.bodyStrong)
                .foregroundStyle(palette(.accentPrimary))
                .padding(.top, PodTokens.Spacing.sm)

            if let failure = controller.failure {
                PodNotice(error: failure) {
                    Task { await controller.checkForPod() }
                }
            }

            Spacer()

            Button(copy(.poolWaitingLeave)) {
                Task { await controller.leaveQueue() }
            }
            .podText(PodTokens.TypeScale.caption)
            .foregroundStyle(palette(.contentSecondary))
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, PodTokens.Spacing.screenGutter)
        .padding(.vertical, PodTokens.Spacing.xxxl)
        .background(palette(.surfaceCanvas))
        .task { await controller.checkForPod() }
        .onChange(of: scenePhase) { _, phase in
            // Check once on return, deliberately. Matching is scheduled, so there is
            // nothing to poll for.
            if phase == .active {
                Task { await controller.checkForPod() }
            }
        }
    }
}

#Preview("Waiting for a pod") {
    WaitingView(
        controller: PodEnvironment.preview().onboarding,
        ticket: PoolTicket(queuedAt: .now, nextWindowAt: .now.addingTimeInterval(1800))
    )
    .environment(\.palette, PodPalette(.light))
}
