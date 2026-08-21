import XCTest
@testable import PodAPI

/// The API emits RFC 3339 with fractional seconds on some fields and without on others,
/// and every screen in the product is driven by a timestamp — a cycle's end, a week's
/// close, the moment proof stops existing. A parser that silently handled one shape and
/// not the other would fail as a decode error on whichever screen drew the short straw,
/// so both shapes are pinned here rather than left to whichever field happened to be
/// covered by another test.
final class TimestampTests: XCTestCase {
    private let baseURL = URL(string: "https://api.test.invalid")!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
    }

    func testParsesWithoutFractionalSeconds() {
        XCTAssertEqual(
            PodTimestamp.parse("2026-08-21T09:10:00Z"),
            Date(timeIntervalSince1970: 1_787_303_400)
        )
    }

    func testParsesWithFractionalSeconds() {
        XCTAssertEqual(
            PodTimestamp.parse("2026-08-21T09:10:00.250Z"),
            Date(timeIntervalSince1970: 1_787_303_400.25)
        )
    }

    func testRefusesSomethingThatIsNotATimestamp() {
        XCTAssertNil(PodTimestamp.parse("soon"))
        XCTAssertNil(PodTimestamp.parse(""))
        XCTAssertNil(PodTimestamp.parse("2026-08-21"))
    }

    /// The parser is reached through `JSONDecoder`, so the wiring is worth a test of its
    /// own: a correct parser that the decoder never calls would still break every screen.
    func testTheDecoderAcceptsBothShapes() async throws {
        StubURLProtocol.enqueue(json: """
        {"challenge_id":"1A2B3C4D-1111-4111-8111-111111111111",
         "expires_at":"2026-08-21T09:10:00Z","code_length":6,"resend_after_seconds":30}
        """)
        StubURLProtocol.enqueue(json: """
        {"challenge_id":"1A2B3C4D-1111-4111-8111-111111111111",
         "expires_at":"2026-08-21T09:10:00.250Z","code_length":6,"resend_after_seconds":30}
        """)
        let client = APIClient(
            baseURL: baseURL,
            tokens: InMemoryTokenStore(),
            session: StubURLProtocol.session(),
            now: { Date(timeIntervalSince1970: 1_800_000_000) }
        )

        let plain = try await client.startSignIn(email: "sam@example.com")
        XCTAssertEqual(plain.expiresAt, Date(timeIntervalSince1970: 1_787_303_400))

        let fractional = try await client.startSignIn(email: "sam@example.com")
        XCTAssertEqual(fractional.expiresAt, Date(timeIntervalSince1970: 1_787_303_400.25))
    }
}
