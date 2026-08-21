import XCTest
@testable import PodAPI

@MainActor
final class WeekControllerTests: XCTestCase {
    private let baseURL = URL(string: "https://api.test.invalid")!
    private let podID = UUID(uuidString: "B2FA324B-3D7C-4D3E-A22D-4A3B58121280")!
    private let mine = UUID(uuidString: "8B1F2A7C-1111-4111-8111-111111111111")!
    private let theirs = UUID(uuidString: "9B1F2A7C-1111-4111-8111-111111111111")!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
    }

    private func makeController() -> WeekController {
        let tokens = InMemoryTokenStore(session: StoredSession(
            accessToken: "access-1",
            refreshToken: "refresh-1",
            accessTokenExpiresAt: Date(timeIntervalSince1970: 1_800_000_900),
            userID: mine
        ))
        let client = APIClient(
            baseURL: baseURL,
            tokens: tokens,
            session: StubURLProtocol.session(),
            now: { Date(timeIntervalSince1970: 1_800_000_000) }
        )
        return WeekController(client: client, podID: podID)
    }

    /// A week that closes well in the future, so `isOpen` does not depend on the wall clock.
    private func weekJSON(
        mineState: String = "open",
        theirsState: String = "open",
        actionText: String = "One small thing towards it this week.",
        actionIsDefault: Bool = true,
        actionEditable: Bool = true,
        podCompleted: Bool = false,
        index: Int = 1
    ) -> String {
        """
        {"id":"55555555-5555-4555-8555-555555555555","pod_id":"\(podID.uuidString.lowercased())",
         "index":\(index),"action_text":"\(actionText)","action_is_default":\(actionIsDefault),
         "action_editable":\(actionEditable),
         "opens_at":"2099-01-05T00:00:00Z","closes_at":"2099-01-12T00:00:00Z",
         "members":[
           {"member_id":"\(mine.uuidString.lowercased())","state":"\(mineState)"},
           {"member_id":"\(theirs.uuidString.lowercased())","state":"\(theirsState)"}
         ],
         "pod_completed":\(podCompleted)}
        """
    }

    func testLoadReadsTheCurrentWeek() async {
        let controller = makeController()
        StubURLProtocol.enqueue(json: weekJSON())

        await controller.load()

        guard let week = controller.week else {
            return XCTFail("no week, phase is \(controller.phase)")
        }
        XCTAssertEqual(week.index, 1)
        XCTAssertTrue(week.actionIsDefault)
        XCTAssertEqual(week.members.count, 2)
        XCTAssertEqual(StubURLProtocol.requests.first?.url?.path, "/v1/pods/\(podID.uuidString.lowercased())/weeks/current")
    }

    // The only counting this app does, and it is about the pod.
    func testPodProgressCountsThePodNotThePerson() async {
        let controller = makeController()
        StubURLProtocol.enqueue(json: weekJSON(mineState: "posted"))
        await controller.load()

        let progress = controller.week?.podProgress
        XCTAssertEqual(progress?.posted, 1)
        XCTAssertEqual(progress?.total, 2)
    }

    func testYouCanPostWhileTheWeekIsOpenAndYouHaveNot() async {
        let controller = makeController()
        StubURLProtocol.enqueue(json: weekJSON())
        await controller.load()
        XCTAssertTrue(controller.canPost(userID: mine))
    }

    func testYouCannotPostTwice() async {
        let controller = makeController()
        StubURLProtocol.enqueue(json: weekJSON(mineState: "posted"))
        await controller.load()

        XCTAssertFalse(controller.canPost(userID: mine))
        XCTAssertEqual(controller.me(userID: mine)?.state, .posted)
    }

    func testPostingWalksTheSlotTheUploadAndTheCommitInOrder() async {
        let controller = makeController()
        StubURLProtocol.enqueue(json: weekJSON())
        await controller.load()

        StubURLProtocol.enqueue(status: 201, json: """
        {"proof_id":"66666666-6666-4666-8666-666666666666",
         "upload_url":"https://storage.test.invalid/blobs/proof/w/p?sig=x",
         "upload_method":"PUT","upload_expires_at":"2099-01-05T00:10:00Z"}
        """)
        StubURLProtocol.enqueue(status: 201, json: "")
        StubURLProtocol.enqueue(json: """
        {"id":"66666666-6666-4666-8666-666666666666","week_id":"55555555-5555-4555-8555-555555555555",
         "member_id":"\(mine.uuidString.lowercased())","kind":"photo",
         "created_at":"2099-01-05T09:00:00Z","expires_at":"2099-01-12T00:00:00Z"}
        """)
        StubURLProtocol.enqueue(json: weekJSON(mineState: "posted"))

        await controller.post(kind: .photo, contentType: "image/jpeg", data: Data(count: 64))

        let calls = StubURLProtocol.requests.map { "\($0.httpMethod ?? "") \($0.url?.path ?? "")" }
        XCTAssertEqual(calls, [
            "GET /v1/pods/\(podID.uuidString.lowercased())/weeks/current",
            "POST /v1/pods/\(podID.uuidString.lowercased())/weeks/55555555-5555-4555-8555-555555555555/proof",
            // Straight to storage, never through the API (ADR-008).
            "PUT /blobs/proof/w/p",
            "POST /v1/pods/\(podID.uuidString.lowercased())/weeks/55555555-5555-4555-8555-555555555555/proof/66666666-6666-4666-8666-666666666666/commit",
            "GET /v1/pods/\(podID.uuidString.lowercased())/weeks/current",
        ])
        XCTAssertEqual(controller.me(userID: mine)?.state, .posted)
        XCTAssertNil(controller.failure)
    }

    func testNamingTheActionReplacesTheDefault() async {
        let controller = makeController()
        StubURLProtocol.enqueue(json: weekJSON())
        await controller.load()

        StubURLProtocol.enqueue(json: weekJSON(actionText: "Clear one shelf", actionIsDefault: false))
        await controller.setAction("Clear one shelf")

        XCTAssertEqual(controller.week?.actionText, "Clear one shelf")
        XCTAssertEqual(controller.week?.actionIsDefault, false)
        XCTAssertEqual(StubURLProtocol.requests.last?.httpMethod, "PUT")
    }

    func testALockedActionSurfacesItsOwnCopyAndLeavesTheWeekAlone() async {
        let controller = makeController()
        StubURLProtocol.enqueue(json: weekJSON())
        await controller.load()

        StubURLProtocol.enqueue(status: 409, json: """
        {"code":"conflict","copy_key":"error.actionLocked.body","request_id":"r-1"}
        """)
        await controller.setAction("Something else")

        XCTAssertEqual(controller.failure?.bodyKey, .errorActionLockedBody)
        XCTAssertEqual(controller.week?.actionText, "One small thing towards it this week.")
    }

    // A proof that has already been swept is a normal outcome, not an error.
    func testASweptProofComesBackAsNothing() async {
        let controller = makeController()
        StubURLProtocol.enqueue(json: weekJSON(mineState: "posted"))
        await controller.load()

        StubURLProtocol.enqueue(status: 404, json: """
        {"code":"not_found","copy_key":"error.server.body","request_id":"r-2"}
        """)
        let data = await controller.proofData(proofID: UUID())
        XCTAssertNil(data)
    }

    // A refresh that fails leaves what is on screen alone: someone looking at their pod
    // should not lose it to a dropped connection.
    func testAFailedRefreshDoesNotBlankTheWeek() async {
        let controller = makeController()
        StubURLProtocol.enqueue(json: weekJSON())
        await controller.load()

        StubURLProtocol.enqueue(status: 500, json: """
        {"code":"server_error","copy_key":"error.server.body","request_id":"r-3"}
        """)
        await controller.refresh()

        XCTAssertNotNil(controller.week, "the week vanished on a failed refresh")
        XCTAssertNotNil(controller.failure)
    }

    func testDaysRemainingIsNeverNegative() async {
        let controller = makeController()
        StubURLProtocol.enqueue(json: weekJSON())
        await controller.load()

        guard let week = controller.week else { return XCTFail("no week") }
        XCTAssertEqual(week.daysRemaining(from: week.closesAt.addingTimeInterval(86_400)), 0)
        XCTAssertGreaterThan(week.daysRemaining(from: week.opensAt), 0)
    }
}
