import Foundation
import Observation

/// Where a purchase comes from.
///
/// A seam, exactly as the server's receipt verifier is: StoreKit needs a real app record
/// and store credentials that this repository does not and should not contain. Everything
/// on this side of it is real — the claim call, the entitlement, the second lane.
public protocol PurchaseBroker: Sendable {
    /// Presents the platform's purchase sheet and returns a receipt, or `nil` if the
    /// person changed their mind.
    ///
    /// Cancelling is an ordinary outcome and must not surface as a failure: deciding not
    /// to buy something is a decision, not an error.
    func purchaseAdditionalLane() async throws -> String?
}

/// The default. An app with no store configuration behaves like someone who cancelled,
/// which keeps the whole product working without a paywall rather than breaking around it.
public struct UnavailablePurchaseBroker: PurchaseBroker {
    public init() {}
    public func purchaseAdditionalLane() async throws -> String? { nil }
}

/// The caller's lanes, and opening another.
@MainActor
@Observable
public final class SlotsController {
    public private(set) var slots: [Slot] = []
    public private(set) var maxSlots = 0
    public private(set) var failure: APIError?
    public private(set) var isWorking = false

    private let client: APIClient
    private let broker: PurchaseBroker
    private let source: PurchaseSource

    public init(client: APIClient, broker: PurchaseBroker = UnavailablePurchaseBroker(), source: PurchaseSource = .appStore) {
        self.client = client
        self.broker = broker
        self.source = source
    }

    /// The lanes that are actually usable, in order. Lane 0 is always among them.
    public var openLanes: [Slot] { slots.filter(\.entitled) }

    /// Whether there is room for another. Not the same as whether to *offer* one — that
    /// is the graduation screen's decision (docs/DESIGN.md).
    public var canOpenAnother: Bool { openLanes.count < maxSlots }

    public func load() async {
        await perform {
            let list = try await self.client.slots()
            self.slots = list.slots
            self.maxSlots = list.maxSlots
        }
    }

    /// The lane the caller is looking at. Every lane-aware screen reads this.
    ///
    /// Held here rather than in each screen because it is one fact about the whole
    /// session — which thing you are currently looking at — and two screens disagreeing
    /// about it is how someone posts proof against the wrong goal.
    public private(set) var current = 0

    /// Switches to a lane the caller actually has. A lane they do not is ignored rather
    /// than refused: there is no state in which showing an error about it helps.
    public func show(slot: Int) {
        guard slots.contains(where: { $0.slot == slot && $0.entitled }) else { return }
        current = slot
    }

    /// Presents the purchase sheet and, if it completes, opens the lane.
    ///
    /// Returns the new lane, or `nil` when the person cancelled — which is a decision,
    /// not a failure, and produces no error state. On success the app moves to the new
    /// lane: someone who has just bought a second thing wants to name it, not to be
    /// returned to the one they finished.
    @discardableResult
    public func openAnotherLane() async -> Slot? {
        var opened: Slot?
        await perform {
            guard let receipt = try await self.broker.purchaseAdditionalLane() else { return }
            opened = try await self.client.claimSlot(source: self.source, receipt: receipt)
            await self.reload()
            if let opened {
                self.current = opened.slot
            }
        }
        return opened
    }

    private func reload() async {
        if let list = try? await client.slots() {
            slots = list.slots
            maxSlots = list.maxSlots
        }
    }

    public func dismissFailure() {
        failure = nil
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
