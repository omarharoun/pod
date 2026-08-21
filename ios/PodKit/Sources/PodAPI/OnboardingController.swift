import Foundation
import Observation

/// The four things onboarding asks, one per screen (docs/DESIGN.md).
public enum OnboardingQuestion: Int, CaseIterable, Sendable {
    case goal
    case category
    case intensity
    case reliability
}

/// What has been answered so far. Held locally until the whole thing is submitted: a
/// half-filled intent is not something the server needs to know about.
public struct IntentDraft: Sendable, Equatable {
    public var goalText: String = ""
    public var displayName: String = ""
    public var category: GoalCategory?
    /// 1...5 — see ADR-010. The number is never shown; the screen shows the phrase.
    public var intensity: Int?
    public var selfReport: SelfReportReliability?
    public var timezone: String = TimeZone.current.identifier

    public init() {}

    public func isAnswered(_ question: OnboardingQuestion) -> Bool {
        switch question {
        case .goal: goalText.trimmingCharacters(in: .whitespacesAndNewlines).count >= 3
        case .category: category != nil
        case .intensity: intensity != nil
        case .reliability: selfReport != nil
        }
    }

    /// The request, or `nil` if something is still unanswered.
    func request() -> IntentRequest? {
        guard let category, let intensity, let selfReport else { return nil }
        let trimmedName = displayName.trimmingCharacters(in: .whitespacesAndNewlines)
        return IntentRequest(
            goalText: goalText.trimmingCharacters(in: .whitespacesAndNewlines),
            goalCategory: category,
            intensity: intensity,
            timezone: timezone,
            selfReportReliability: selfReport,
            availabilityWindows: nil,
            displayName: trimmedName.isEmpty ? nil : trimmedName
        )
    }
}

/// Where a signed-in person is: still answering, waiting for a window, or in a pod.
public enum OnboardingPhase: Sendable, Equatable {
    case loading
    case questions(OnboardingQuestion)
    case waiting(PoolTicket)
    case matched(Pod)
}

/// Drives everything between signing in and having a pod.
///
/// Separate from ``SessionController`` because they answer different questions — one owns
/// whether you are signed in, this one owns what you see once you are.
@MainActor
@Observable
public final class OnboardingController {
    public private(set) var phase: OnboardingPhase = .loading
    public private(set) var failure: APIError?
    public private(set) var isWorking = false
    public var draft = IntentDraft()

    private let client: APIClient

    public init(client: APIClient) {
        self.client = client
    }

    /// Reads where the server thinks this person is. Safe to call on every appearance.
    public func load() async {
        await perform {
            let state = try await self.client.onboardingState()
            switch state.step {
            case "ready":
                if let pod = try await self.client.currentPod() {
                    self.phase = .matched(pod)
                } else {
                    // Ready but no pod readable: treat it as still waiting rather than
                    // showing an empty pod screen.
                    self.phase = .questions(.goal)
                }
            case "waiting_for_pod":
                self.phase = .waiting(state.poolTicket ?? PoolTicket(queuedAt: .now, nextWindowAt: .now))
            default:
                self.phase = .questions(self.draft.isAnswered(.goal) ? .category : .goal)
            }
        }
    }

    /// Called when the waiting screen comes back to the foreground, or when the stated
    /// window time has passed. Matching is a scheduled job, so there is nothing to poll —
    /// this checks once, deliberately, rather than on a timer.
    public func checkForPod() async {
        guard case .waiting = phase else { return }
        await perform {
            if let pod = try await self.client.currentPod() {
                self.phase = .matched(pod)
            }
        }
    }

    public func advance() {
        guard case .questions(let current) = phase, draft.isAnswered(current) else { return }
        failure = nil
        guard let next = OnboardingQuestion(rawValue: current.rawValue + 1) else {
            Task { await submit() }
            return
        }
        phase = .questions(next)
    }

    public func back() {
        guard case .questions(let current) = phase,
              let previous = OnboardingQuestion(rawValue: current.rawValue - 1)
        else { return }
        failure = nil
        phase = .questions(previous)
    }

    /// Saves the intent and joins the queue in one go — from the person's point of view
    /// they answered four questions and asked for a pod, not two separate things.
    public func submit() async {
        guard let request = draft.request() else { return }
        await perform {
            _ = try await self.client.saveIntent(request)
            let ticket = try await self.client.joinPool()
            self.phase = .waiting(ticket)
        }
    }

    public func leaveQueue() async {
        await perform {
            try await self.client.leavePool()
            self.phase = .questions(.goal)
        }
    }

    public func dismissFailure() {
        failure = nil
    }

    /// Puts the controller into a fixed phase. Previews and tests only.
    func setPreviewPhase(_ phase: OnboardingPhase) {
        self.phase = phase
    }

    private func perform(_ work: () async throws -> Void) async {
        isWorking = true
        failure = nil
        defer { isWorking = false }
        do {
            try await work()
        } catch let error as APIError {
            failure = error
        } catch {
            failure = .malformedResponse
        }
    }
}

public extension OnboardingController {
    /// Previews only.
    func preview(phase: OnboardingPhase) {
        setPreviewPhase(phase)
    }
}
