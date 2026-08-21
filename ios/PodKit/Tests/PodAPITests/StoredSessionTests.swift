import XCTest
@testable import PodAPI

final class StoredSessionTests: XCTestCase {
    private func session(expiringAt expiry: Date) -> StoredSession {
        StoredSession(accessToken: "a", refreshToken: "r", accessTokenExpiresAt: expiry, userID: UUID())
    }

    func testFreshnessLeavesAMargin() {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        XCTAssertTrue(session(expiringAt: now.addingTimeInterval(120)).accessTokenIsFresh(at: now))
        // Inside the margin the token counts as stale, so a request doesn't expire in
        // flight over a second of clock skew.
        XCTAssertFalse(session(expiringAt: now.addingTimeInterval(10)).accessTokenIsFresh(at: now))
        XCTAssertFalse(session(expiringAt: now.addingTimeInterval(-1)).accessTokenIsFresh(at: now))
    }

    func testRoundTripsThroughJSON() throws {
        let original = session(expiringAt: Date(timeIntervalSince1970: 1_800_000_900))
        let decoded = try JSONDecoder().decode(StoredSession.self, from: JSONEncoder().encode(original))
        XCTAssertEqual(decoded, original)
    }
}
