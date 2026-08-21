import XCTest
@testable import PodAPI

@MainActor
final class OnboardingControllerTests: XCTestCase {
    private let baseURL = URL(string: "https://api.test.invalid")!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
    }

    private func makeController() -> OnboardingController {
        let tokens = InMemoryTokenStore(session: StoredSession(
            accessToken: "access-1",
            refreshToken: "refresh-1",
            accessTokenExpiresAt: Date(timeIntervalSince1970: 1_800_000_900),
            userID: UUID()
        ))
        let client = APIClient(
            baseURL: baseURL,
            tokens: tokens,
            session: StubURLProtocol.session(),
            now: { Date(timeIntervalSince1970: 1_800_000_000) }
        )
        return OnboardingController(client: client)
    }

    private static let podJSON = """
    {
      "id": "5C1F2A7C-1111-4111-8111-111111111111",
      "state": "active",
      "goal_category": "home",
      "timezone": "Europe/Lisbon",
      "streak_weeks": 0,
      "members": [
        {"id":"8B1F2A7C-1111-4111-8111-111111111111","display_name":"Sam","is_you":true,"joined_at":"2026-08-21T09:00:00Z"},
        {"id":"9B1F2A7C-1111-4111-8111-111111111111","display_name":"Alex","is_you":false,"joined_at":"2026-08-21T09:00:00Z"}
      ],
      "created_at": "2026-08-21T09:00:00Z"
    }
    """

    func testDraftCompletenessPerQuestion() {
        var draft = IntentDraft()
        XCTAssertFalse(draft.isAnswered(.goal))
        draft.goalText = "  ab  "
        XCTAssertFalse(draft.isAnswered(.goal), "whitespace should not count towards the minimum")
        draft.goalText = "sort out the spare room"
        XCTAssertTrue(draft.isAnswered(.goal))

        XCTAssertFalse(draft.isAnswered(.category))
        draft.category = .home
        draft.intensity = 3
        draft.selfReport = .sometimes
        XCTAssertTrue(draft.isAnswered(.category))
        XCTAssertTrue(draft.isAnswered(.intensity))
        XCTAssertTrue(draft.isAnswered(.reliability))
    }

    func testAdvanceRequiresAnAnswer() {
        let controller = makeController()
        controller.preview(phase: .questions(.goal))

        controller.advance()
        XCTAssertEqual(controller.phase, .questions(.goal), "an unanswered question must not advance")

        controller.draft.goalText = "sort out the spare room"
        controller.advance()
        XCTAssertEqual(controller.phase, .questions(.category))
    }

    func testBackWalksTheQuestionsAndStopsAtTheFirst() {
        let controller = makeController()
        controller.preview(phase: .questions(.intensity))

        controller.back()
        XCTAssertEqual(controller.phase, .questions(.category))
        controller.back()
        XCTAssertEqual(controller.phase, .questions(.goal))
        controller.back()
        XCTAssertEqual(controller.phase, .questions(.goal), "back from the first question is a no-op")
    }

    func testSubmitSavesTheIntentThenJoinsTheQueue() async {
        let controller = makeController()
        controller.preview(phase: .questions(.reliability))
        controller.draft.goalText = "sort out the spare room"
        controller.draft.category = .home
        controller.draft.intensity = 3
        controller.draft.selfReport = .sometimes

        StubURLProtocol.enqueue(json: """
        {"goal_text":"sort out the spare room","goal_category":"home","intensity":3,
         "timezone":"Europe/Lisbon","updated_at":"2026-08-21T09:00:00Z"}
        """)
        StubURLProtocol.enqueue(json: """
        {"queued_at":"2026-08-21T09:00:00Z","next_window_at":"2026-08-21T10:00:00Z"}
        """)

        await controller.submit()

        let paths = StubURLProtocol.requests.compactMap(\.url?.path)
        XCTAssertEqual(paths, ["/v1/onboarding/intent", "/v1/pool"])
        guard case .waiting(let ticket) = controller.phase else {
            return XCTFail("expected the waiting phase, got \(controller.phase)")
        }
        XCTAssertTrue(ticket.nextWindowAt > ticket.queuedAt)
        XCTAssertNil(controller.failure)
    }

    func testSubmitWithAnIncompleteDraftDoesNothing() async {
        let controller = makeController()
        controller.preview(phase: .questions(.reliability))
        controller.draft.goalText = "sort out the spare room"

        await controller.submit()
        XCTAssertTrue(StubURLProtocol.requests.isEmpty, "an incomplete draft must not reach the network")
    }

    func testLoadPutsAMatchedPersonOnTheirPod() async {
        let controller = makeController()
        StubURLProtocol.enqueue(json: #"{"step":"ready","intent_complete":true,"in_pool":false}"#)
        StubURLProtocol.enqueue(json: Self.podJSON)

        await controller.load()

        guard case .matched(let pod) = controller.phase else {
            return XCTFail("expected the matched phase, got \(controller.phase)")
        }
        XCTAssertEqual(pod.members.count, 2)
        XCTAssertEqual(pod.members.filter(\.isYou).count, 1)
    }

    func testLoadPutsAQueuedPersonOnTheWaitingScreen() async {
        let controller = makeController()
        StubURLProtocol.enqueue(json: """
        {"step":"waiting_for_pod","intent_complete":true,"in_pool":true,
         "pool_ticket":{"queued_at":"2026-08-21T09:00:00Z","next_window_at":"2026-08-21T10:00:00Z"}}
        """)

        await controller.load()

        guard case .waiting = controller.phase else {
            return XCTFail("expected the waiting phase, got \(controller.phase)")
        }
    }

    // No pod yet arrives as a 204, and must leave the person on the waiting screen rather
    // than looking like a failure.
    func testCheckForPodStaysOnTheWaitingScreenWhenThereIsNoPodYet() async {
        let controller = makeController()
        controller.preview(phase: .waiting(PoolTicket(queuedAt: .now, nextWindowAt: .now.addingTimeInterval(600))))
        StubURLProtocol.enqueue(status: 204, json: "")

        await controller.checkForPod()

        guard case .waiting = controller.phase else {
            return XCTFail("expected to still be waiting, got \(controller.phase)")
        }
        XCTAssertNil(controller.failure, "not being matched yet is a state, not an error")
    }

    func testCheckForPodMovesOnWhenTheWindowHasRun() async {
        let controller = makeController()
        controller.preview(phase: .waiting(PoolTicket(queuedAt: .now, nextWindowAt: .now)))
        StubURLProtocol.enqueue(json: Self.podJSON)

        await controller.checkForPod()

        guard case .matched = controller.phase else {
            return XCTFail("expected the matched phase, got \(controller.phase)")
        }
    }

    func testLeaveQueueReturnsToTheQuestions() async {
        let controller = makeController()
        controller.preview(phase: .waiting(PoolTicket(queuedAt: .now, nextWindowAt: .now)))
        StubURLProtocol.enqueue(status: 204, json: "")

        await controller.leaveQueue()

        XCTAssertEqual(controller.phase, .questions(.goal))
    }

    func testAFailureIsSurfacedRatherThanSwallowed() async {
        let controller = makeController()
        controller.preview(phase: .questions(.reliability))
        controller.draft.goalText = "no"
        controller.draft.category = .home
        controller.draft.intensity = 3
        controller.draft.selfReport = .sometimes

        StubURLProtocol.enqueue(status: 400, json: """
        {"code":"bad_request","copy_key":"error.goalTooShort.body","request_id":"r-1"}
        """)

        await controller.submit()

        XCTAssertNotNil(controller.failure)
        XCTAssertEqual(controller.failure?.bodyKey, .errorGoalTooShortBody)
    }
}

// MARK: - Recovery (M5)

extension OnboardingControllerTests {
    private static let intentJSON = """
    {
      "goal_text": "finally sort out the spare room",
      "goal_category": "home",
      "intensity": 3,
      "timezone": "Europe/Lisbon",
      "availability_windows": [{"day_of_week":5,"start_minute":600,"end_minute":720}],
      "updated_at": "2026-08-21T09:00:00Z"
    }
    """

    private static let ticketJSON = """
    {"queued_at":"2026-08-21T09:00:00Z","next_window_at":"2026-08-21T10:00:00Z"}
    """

    func testAPodThatEndedShowsTheOfferBeforeAnythingElse() async {
        let controller = makeController()
        StubURLProtocol.enqueue(json: """
        {
          "step": "intent",
          "intent_complete": true,
          "in_pool": false,
          "recovery": {
            "offer_lower_intensity": true,
            "current_intensity": 3,
            "suggested_intensity": 2,
            "offer_alternate_path": false
          }
        }
        """)

        await controller.load()

        guard case .recovering(let recovery) = controller.phase else {
            return XCTFail("phase: \(controller.phase)")
        }
        XCTAssertEqual(recovery.currentIntensity, 3)
        XCTAssertEqual(recovery.suggestedIntensity, 2)
        XCTAssertFalse(recovery.offerAlternatePath)
    }

    /// Taking the offer re-sends the intent on file with one field changed. It must not
    /// re-send the self-report: the server keeps the stored answer, and that question is
    /// asked once (ADR-018).
    func testTakingTheOfferResendsTheStoredIntentWithASmallerIntensity() async throws {
        let controller = makeController()
        StubURLProtocol.enqueue(json: Self.intentJSON)   // GET  /v1/onboarding/intent
        StubURLProtocol.enqueue(json: Self.intentJSON)   // PUT  /v1/onboarding/intent
        StubURLProtocol.enqueue(json: Self.ticketJSON)   // POST /v1/pool

        await controller.acceptLowerIntensity(2)

        XCTAssertNil(controller.failure)
        guard case .waiting = controller.phase else {
            return XCTFail("phase: \(controller.phase)")
        }

        let put = try XCTUnwrap(StubURLProtocol.calls.first { $0.method == "PUT" })
        let sent = try XCTUnwrap(put.json())
        XCTAssertEqual(sent["intensity"] as? Int, 2)
        XCTAssertEqual(sent["goal_text"] as? String, "finally sort out the spare room")
        XCTAssertNil(sent["self_report_reliability"], "the self-report is asked once, not again")
    }

    /// Declining sends the same intent unchanged. "Keep it as it is" is a real answer,
    /// not a way of dismissing the screen.
    func testKeepingTheIntensityGoesBackIntoThePoolUnchanged() async throws {
        let controller = makeController()
        StubURLProtocol.enqueue(json: Self.intentJSON)
        StubURLProtocol.enqueue(json: Self.intentJSON)
        StubURLProtocol.enqueue(json: Self.ticketJSON)

        await controller.keepIntensity()

        guard case .waiting = controller.phase else {
            return XCTFail("phase: \(controller.phase)")
        }
        let put = try XCTUnwrap(StubURLProtocol.calls.first { $0.method == "PUT" })
        let sent = try XCTUnwrap(put.json())
        XCTAssertEqual(sent["intensity"] as? Int, 3)
    }

    /// Nothing on file — a rare state, but re-sending a half-formed intent would be
    /// worse than asking the questions again.
    func testNoIntentOnFileFallsBackToTheQuestions() async {
        let controller = makeController()
        StubURLProtocol.enqueue(status: 404, json: """
        {"code":"not_found","copy_key":"error.server.body","request_id":"r1"}
        """)

        await controller.keepIntensity()

        guard case .questions(.goal) = controller.phase else {
            return XCTFail("phase: \(controller.phase)")
        }
        XCTAssertNil(controller.failure, "no intent yet is a state, not a failure")
    }
}
