import XCTest
@testable import PodAPI

@MainActor
final class SlotsControllerTests: XCTestCase {
    private let baseURL = URL(string: "https://api.test.invalid")!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
    }

    private static let oneOpenLane = """
    {"max_slots":5,"slots":[
      {"slot":0,"entitled":true,"has_intent":true,"in_pool":false,"has_pod":true,"goal_text":"sort out the spare room"},
      {"slot":1,"entitled":false,"has_intent":false,"in_pool":false,"has_pod":false},
      {"slot":2,"entitled":false,"has_intent":false,"in_pool":false,"has_pod":false},
      {"slot":3,"entitled":false,"has_intent":false,"in_pool":false,"has_pod":false},
      {"slot":4,"entitled":false,"has_intent":false,"in_pool":false,"has_pod":false}
    ]}
    """

    private struct CancellingBroker: PurchaseBroker {
        func purchaseAdditionalLane() async throws -> String? { nil }
    }

    private final class BuyingBroker: PurchaseBroker, @unchecked Sendable {
        let receipt: String
        private(set) var calls = 0
        init(receipt: String) { self.receipt = receipt }
        func purchaseAdditionalLane() async throws -> String? {
            calls += 1
            return receipt
        }
    }

    private func makeClient() -> APIClient {
        let tokens = InMemoryTokenStore(session: StoredSession(
            accessToken: "access-1",
            refreshToken: "refresh-1",
            accessTokenExpiresAt: Date(timeIntervalSince1970: 1_800_000_900),
            userID: UUID()
        ))
        return APIClient(
            baseURL: baseURL,
            tokens: tokens,
            session: StubURLProtocol.session(),
            now: { Date(timeIntervalSince1970: 1_800_000_000) }
        )
    }

    func testTheFreeLaneIsAlwaysOpen() async {
        let controller = SlotsController(client: makeClient(), broker: CancellingBroker())
        StubURLProtocol.enqueue(json: Self.oneOpenLane)

        await controller.load()

        XCTAssertEqual(controller.openLanes.count, 1)
        XCTAssertEqual(controller.openLanes.first?.slot, 0)
        XCTAssertTrue(controller.canOpenAnother)
        XCTAssertNil(controller.failure)
    }

    /// Cancelling a purchase is a decision, not an error.
    func testCancellingThePurchaseSheetProducesNoFailure() async {
        let controller = SlotsController(client: makeClient(), broker: CancellingBroker())
        StubURLProtocol.enqueue(json: Self.oneOpenLane)
        await controller.load()
        StubURLProtocol.reset()

        let opened = await controller.openAnotherLane()

        XCTAssertNil(opened)
        XCTAssertNil(controller.failure, "cancelling is a decision, not a failure")
        XCTAssertTrue(StubURLProtocol.calls.isEmpty, "cancelling must not reach the server")
        XCTAssertFalse(controller.isWorking)
    }

    func testACompletedPurchaseClaimsTheLaneAndReloads() async throws {
        let broker = BuyingBroker(receipt: "dev:order-1")
        let controller = SlotsController(client: makeClient(), broker: broker)
        StubURLProtocol.enqueue(json: Self.oneOpenLane)
        await controller.load()

        StubURLProtocol.enqueue(json: #"{"slot":1,"entitled":true,"has_intent":false,"in_pool":false,"has_pod":false}"#)
        StubURLProtocol.enqueue(json: Self.oneOpenLane.replacingOccurrences(
            of: #"{"slot":1,"entitled":false"#, with: #"{"slot":1,"entitled":true"#
        ))

        let opened = await controller.openAnotherLane()

        XCTAssertEqual(opened?.slot, 1)
        XCTAssertEqual(broker.calls, 1)
        let post = try XCTUnwrap(StubURLProtocol.calls.first { $0.method == "POST" })
        XCTAssertEqual(post.path, "/v1/slots")
        XCTAssertEqual(try XCTUnwrap(post.json())["receipt"] as? String, "dev:order-1")
        XCTAssertEqual(controller.openLanes.count, 2)
    }

    /// A rejected receipt must not take away the free lane.
    func testARejectedReceiptLeavesTheFreeLaneAlone() async {
        let controller = SlotsController(client: makeClient(), broker: BuyingBroker(receipt: "dev:forged"))
        StubURLProtocol.enqueue(json: Self.oneOpenLane)
        await controller.load()

        StubURLProtocol.enqueue(status: 402, json: """
        {"code":"bad_request","copy_key":"error.receiptRejected.body","request_id":"r1"}
        """)

        let opened = await controller.openAnotherLane()

        XCTAssertNil(opened)
        XCTAssertNotNil(controller.failure)
        XCTAssertEqual(controller.openLanes.count, 1, "the free lane survives a rejected purchase")
        XCTAssertTrue(controller.openLanes.first?.entitled == true)
    }

    /// The free lane's requests carry no slot parameter at all (ADR-026), so every call a
    /// pre-M7 client made is byte-identical after it.
    func testTheFreeLaneSendsNoSlotParameter() async throws {
        let client = makeClient()
        StubURLProtocol.enqueue(json: #"{"queued_at":"2026-08-21T09:00:00Z","next_window_at":"2026-08-21T10:00:00Z"}"#)
        StubURLProtocol.enqueue(json: #"{"queued_at":"2026-08-21T09:00:00Z","next_window_at":"2026-08-21T10:00:00Z"}"#)

        _ = try await client.joinPool()
        _ = try await client.joinPool(slot: 2)

        let paths = StubURLProtocol.requests.compactMap { $0.url?.absoluteString }
        XCTAssertTrue(paths[0].hasSuffix("/v1/pool"), paths[0])
        XCTAssertTrue(paths[1].hasSuffix("/v1/pool?slot=2"), paths[1])
    }
}

/// The paywall's copy carries the product's whole position on paying for this.
final class SlotsCopyTests: XCTestCase {
    private let copy = Copywriter.bundled

    private static let keys: [CopyKey] = [
        .slotsOfferTitle, .slotsOfferBody, .slotsOfferAction,
        .slotsEmpty, .slotsNewTitle, .slotsNewBody,
        .errorReceiptRejectedTitle, .errorReceiptRejectedBody,
        .errorSlotCeilingTitle, .errorSlotCeilingBody,
    ]

    func testEveryStringResolves() {
        for key in Self.keys {
            XCTAssertTrue(copy.contains(key), "missing \(key.rawValue)")
            XCTAssertFalse(copy(key).isEmpty, "empty \(key.rawValue)")
        }
    }

    /// Nothing about the free pod is locked, and calling it that would be a lie about the
    /// product's own shape (docs/DESIGN.md, §2 rule 6).
    func testTheOfferDoesNotCallTheFreePodLocked() {
        for key in Self.keys {
            let text = copy(key).lowercased()
            for word in ["unlock", "upgrade", "premium", "pro ", "locked", "limited", "free trial"] {
                XCTAssertFalse(text.contains(word), "\(key.rawValue) says \"\(word)\": \(copy(key))")
            }
        }
    }

    /// The price belongs to StoreKit and Play, not to this catalogue.
    func testNoCopyNamesAPrice() {
        for key in Self.keys {
            let text = copy(key)
            for marker in ["$", "£", "€", "/month", "per month", "a month", "/year"] {
                XCTAssertFalse(text.contains(marker), "\(key.rawValue) names a price: \(text)")
            }
        }
    }

    /// A failed purchase must say nothing was charged. Someone who sees an error after
    /// tapping a buy button assumes the worst, and the worst is easy to rule out.
    func testARejectedReceiptSaysNothingWasCharged() {
        let body = copy(.errorReceiptRejectedBody).lowercased()
        XCTAssertTrue(body.contains("charged"), "the rejection does not address the money: \(copy(.errorReceiptRejectedBody))")
    }

    /// The ceiling may not count what somebody has not bought.
    func testTheCeilingDoesNotCountUnboughtLanes() {
        let text = copy(.errorSlotCeilingBody).lowercased()
        for marker in ["of 5", "5 slots", "slots used", "0 of", "remaining"] {
            XCTAssertFalse(text.contains(marker), "the ceiling counts: \(copy(.errorSlotCeilingBody))")
        }
    }
}
