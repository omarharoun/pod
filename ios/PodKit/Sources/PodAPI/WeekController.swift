import Foundation
import Observation

/// What the week screen is doing.
public enum WeekPhase: Sendable, Equatable {
    case loading
    case ready(Week)
    /// The pod exists but has no open week — only reachable if the server is between
    /// boundaries, and the screen shows the same thing as loading.
    case none
}

/// Drives the week screen: the action, the ring, and posting.
///
/// Everything about a week is pod-level or per-member-state. There is deliberately no
/// derived count of anyone's personal weeks anywhere in this type (CLAUDE.md §2 rule 3).
@MainActor
@Observable
public final class WeekController {
    public private(set) var phase: WeekPhase = .loading
    public private(set) var failure: APIError?
    public private(set) var isPosting = false
    public private(set) var isSavingAction = false

    private let client: APIClient
    private let podID: UUID

    public init(client: APIClient, podID: UUID) {
        self.client = client
        self.podID = podID
    }

    public var week: Week? {
        if case .ready(let week) = phase { return week }
        return nil
    }

    /// The caller's own row in this week, if the week is loaded.
    public func me(userID: UUID) -> MemberWeek? {
        week?.members.first { $0.memberId == userID }
    }

    /// Whether the caller can post right now: the week is open and they have not.
    public func canPost(userID: UUID) -> Bool {
        guard let week, week.closesAt > .now else { return false }
        return me(userID: userID)?.state == .open
    }

    public func load() async {
        await perform {
            self.phase = .ready(try await self.client.currentWeek(podID: self.podID))
        }
    }

    /// Refreshes without flashing the loading state — used when returning to the screen.
    public func refresh() async {
        do {
            phase = .ready(try await client.currentWeek(podID: podID))
            failure = nil
        } catch let error as APIError {
            // A refresh that fails leaves what is on screen alone. Someone looking at
            // their pod should not lose it to a dropped connection.
            failure = error
        } catch {
            failure = .malformedResponse
        }
    }

    public func setAction(_ text: String) async {
        guard let week else { return }
        isSavingAction = true
        defer { isSavingAction = false }
        await perform {
            self.phase = .ready(try await self.client.setWeekAction(
                podID: self.podID,
                weekID: week.id,
                actionText: text
            ))
        }
    }

    /// Posts a proof and reloads the week, so the ring moves as soon as the commit lands.
    public func post(kind: ProofKind, contentType: String, data: Data, durationMs: Int? = nil) async {
        guard let week else { return }
        isPosting = true
        defer { isPosting = false }
        await perform {
            _ = try await self.client.postProof(
                podID: self.podID,
                weekID: week.id,
                kind: kind,
                contentType: contentType,
                data: data,
                durationMs: durationMs
            )
            self.phase = .ready(try await self.client.currentWeek(podID: self.podID))
        }
    }

    /// Fetches a pod-mate's proof. Returns nil when it has already gone, which is a
    /// normal outcome rather than a failure (CLAUDE.md §2 rule 4).
    public func proofData(proofID: UUID) async -> Data? {
        guard let week else { return nil }
        do {
            let proof = try await client.proof(podID: podID, weekID: week.id, proofID: proofID)
            guard let url = proof.viewUrl else { return nil }
            return try await client.proofData(at: url)
        } catch {
            return nil
        }
    }

    public func dismissFailure() {
        failure = nil
    }

    /// Previews and tests only.
    func setPreviewPhase(_ phase: WeekPhase) {
        self.phase = phase
    }

    private func perform(_ work: () async throws -> Void) async {
        failure = nil
        do {
            try await work()
        } catch let error as APIError {
            failure = error
        } catch {
            failure = .malformedResponse
        }
    }
}

public extension WeekController {
    /// Previews only.
    func preview(phase: WeekPhase) {
        setPreviewPhase(phase)
    }
}

public extension Week {
    /// How many members are in, and how many there are. The only counting this app does,
    /// and it is about the pod.
    var podProgress: (posted: Int, total: Int) {
        (members.filter { $0.state == .posted }.count, members.count)
    }

    /// Whole days until the week closes, never negative.
    func daysRemaining(from now: Date = .now) -> Int {
        max(0, Int(closesAt.timeIntervalSince(now) / 86_400))
    }

    var isOpen: Bool { closesAt > .now }
}
