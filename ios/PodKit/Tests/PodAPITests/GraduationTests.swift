import XCTest
@testable import PodAPI

@MainActor
final class GraduationControllerTests: XCTestCase {
    private let baseURL = URL(string: "https://api.test.invalid")!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
    }

    private static let graduatedPodJSON = """
    {
      "id": "5C1F2A7C-1111-4111-8111-111111111111",
      "state": "completed",
      "goal_category": "home",
      "timezone": "Europe/Lisbon",
      "streak_weeks": 6,
      "members": [
        {"id":"8B1F2A7C-1111-4111-8111-111111111111","display_name":"Sam","is_you":true,"joined_at":"2026-07-06T00:00:00Z"},
        {"id":"9B1F2A7C-1111-4111-8111-111111111111","display_name":"Alex","is_you":false,"joined_at":"2026-07-06T00:00:00Z"}
      ],
      "cycle": {"length_weeks":6,"weeks_completed":6,"weeks_everyone_was_in":6,"ends_at":"2026-08-17T00:00:00Z"},
      "continuation": {
        "continuing": 1, "undecided": 1, "you_answered": true,
        "you_are_continuing": true, "decide_by": "2026-08-24T00:00:00Z"
      },
      "created_at": "2026-07-06T00:00:00Z",
      "completed_at": "2026-08-17T00:00:00Z"
    }
    """

    private func makeController() -> GraduationController {
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
        let pod = Pod(
            id: UUID(uuidString: "5C1F2A7C-1111-4111-8111-111111111111")!,
            state: .completed,
            goalCategory: .home,
            timezone: "Europe/Lisbon",
            streakWeeks: 6,
            members: [],
            createdAt: Date(timeIntervalSince1970: 1_780_000_000)
        )
        return GraduationController(client: client, pod: pod)
    }

    func testAnsweringSendsTheChoiceAndTakesTheServersPodBack() async throws {
        let controller = makeController()
        StubURLProtocol.enqueue(json: Self.graduatedPodJSON)

        await controller.answer(continuing: true)

        XCTAssertNil(controller.failure)
        let call = try XCTUnwrap(StubURLProtocol.calls.first { $0.method == "POST" })
        XCTAssertEqual(call.path, "/v1/pods/5c1f2a7c-1111-4111-8111-111111111111/continue")
        XCTAssertEqual(try XCTUnwrap(call.json())["continuing"] as? Bool, true)
        XCTAssertEqual(controller.pod.continuation?.youAreContinuing, true)
    }

    /// Changing your mind is an ordinary second call. Someone who said no and reconsidered
    /// is exactly who this product is for.
    func testDecliningIsAnOrdinaryAnswer() async throws {
        let controller = makeController()
        StubURLProtocol.enqueue(json: Self.graduatedPodJSON)

        await controller.answer(continuing: false)

        let call = try XCTUnwrap(StubURLProtocol.calls.first { $0.method == "POST" })
        XCTAssertEqual(try XCTUnwrap(call.json())["continuing"] as? Bool, false)
        XCTAssertNil(controller.failure, "declining is an answer, not a failure")
    }

    /// The choice moving on is a conflict, and it must surface as copy the screen can
    /// show rather than as a silent no-op.
    func testAnsweringAfterTheChoiceClosesSurfacesTheFailure() async {
        let controller = makeController()
        StubURLProtocol.enqueue(status: 409, json: """
        {"code":"conflict","copy_key":"error.cycleChoiceClosed.body","request_id":"r1"}
        """)

        await controller.answer(continuing: true)

        guard case .api(let problem, let status)? = controller.failure else {
            return XCTFail("failure: \(String(describing: controller.failure))")
        }
        XCTAssertEqual(status, 409)
        XCTAssertEqual(problem.code, .conflict)
        XCTAssertTrue(Copywriter.bundled.contains(CopyKey(rawValue: problem.copyKey)))
    }

    /// The decoded pod must never carry a member's identity beside a decline. This is the
    /// client half of the assertion the backend makes against its own response bytes.
    func testTheDecodedPodCannotExpressWhoDeclined() throws {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        let pod = try decoder.decode(Pod.self, from: Data(Self.graduatedPodJSON.utf8))
        let continuation = try XCTUnwrap(pod.continuation)

        // Counts and the caller's own answer, and nothing that could name anyone else.
        XCTAssertEqual(continuation.continuing, 1)
        XCTAssertEqual(continuation.undecided, 1)
        XCTAssertTrue(continuation.youAnswered)

        let encoded = try JSONEncoder().encode(continuation)
        let body = String(decoding: encoded, as: UTF8.self)
        for member in pod.members {
            XCTAssertFalse(
                body.contains(member.id.uuidString.lowercased()),
                "the continuation carries a member id: \(body)"
            )
        }
    }
}

/// The graduation screen is the only place the product celebrates, and celebration is the
/// easiest place to accidentally rank people.
final class GraduationCopyTests: XCTestCase {
    private let copy = Copywriter.bundled

    private static let keys: [CopyKey] = [
        .graduationTitle, .graduationBody, .graduationStreak, .graduationStreakNone,
        .graduationChoiceTitle, .graduationChoiceBody, .graduationChoiceYes,
        .graduationChoiceNo, .graduationChoiceDeadline,
        .graduationWaitingTitle, .graduationWaitingBody, .graduationWaitingCount,
        .graduationDeclinedTitle, .graduationDeclinedBody, .graduationChange,
    ]

    func testEveryStringOnTheScreenResolves() {
        for key in Self.keys {
            XCTAssertTrue(copy.contains(key), "missing \(key.rawValue)")
            XCTAssertFalse(copy(key).isEmpty, "empty \(key.rawValue)")
        }
    }

    /// Nothing here may be about an individual. The cycle belongs to the pod.
    func testNothingCelebratesOrBlamesAnIndividual() {
        // Whole words, not substrings: "I'll stop here" contains "top", and the point is
        // the word, not the letters.
        let forbidden = [
            "you posted", "your streak", "best", "most", "top", "winner", "leader",
            "everyone except", "only you", "everyone else", "let them down", "failed",
            "waiting on you", "up to you", "the last one",
        ]
        for key in Self.keys {
            let text = copy(key).lowercased()
            for phrase in forbidden {
                XCTAssertFalse(text.containsWord(phrase), "\(key.rawValue) says \"\(phrase)\": \(copy(key))")
            }
        }
    }

    /// The zero case is the one that matters. A pod that completed one week of six still
    /// finished the cycle, and the copy may not imply otherwise.
    func testTheZeroStreakLineDoesNotReportAFailure() {
        let text = copy(.graduationStreakNone).lowercased()
        for phrase in ["0", "none", "no weeks", "never", "didn't get", "unfortunately"] {
            XCTAssertFalse(text.containsWord(phrase), "the zero-streak line reports a shortfall: \(copy(.graduationStreakNone))")
        }
    }

    /// The cycle length is interpolated, never spelled out — ADR-023's number can change.
    func testTheCycleLengthIsInterpolatedNotSpelledOut() {
        for key in [CopyKey.graduationTitle, .graduationChoiceTitle] {
            let raw = copy.raw(key) ?? ""
            XCTAssertTrue(raw.contains("{count}"), "\(key.rawValue) does not interpolate the cycle length: \(raw)")
            for spelled in ["six", "6 ", "eight", "four"] {
                XCTAssertFalse(raw.lowercased().contains(spelled), "\(key.rawValue) spells the cycle length out: \(raw)")
            }
        }
        let resolved = copy(.graduationTitle, ["count": "6"])
        XCTAssertTrue(resolved.contains("6"), resolved)
        XCTAssertFalse(resolved.contains("{"), "unresolved placeholder: \(resolved)")
    }

    /// The deadline is stated, never counted down. A countdown here is pressure.
    func testTheDeadlineIsADateNotACountdown() {
        let raw = copy.raw(.graduationChoiceDeadline) ?? ""
        XCTAssertTrue(raw.contains("{date}"), raw)
        for unit in ["seconds", "minutes", "hours left", "remaining"] {
            XCTAssertFalse(raw.lowercased().contains(unit), "the deadline is a countdown: \(raw)")
        }
    }
}

private extension String {
    /// Whole-word containment, so "stop" does not match "top".
    func containsWord(_ word: String) -> Bool {
        range(of: "\\b\(NSRegularExpression.escapedPattern(for: word))\\b", options: .regularExpression) != nil
    }
}
