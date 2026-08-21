import XCTest
@testable import PodAPI

final class APIClientTests: XCTestCase {
    private let baseURL = URL(string: "https://api.test.invalid")!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
    }

    private func makeClient(tokens: TokenStore = InMemoryTokenStore(), now: Date = Date(timeIntervalSince1970: 1_800_000_000)) -> APIClient {
        APIClient(baseURL: baseURL, tokens: tokens, session: StubURLProtocol.session(), now: { now })
    }

    private static let sessionJSON = """
    {
      "access_token": "access-1",
      "refresh_token": "refresh-1",
      "expires_in_seconds": 900,
      "token_type": "Bearer",
      "user": {
        "id": "8B1F2A7C-1111-4111-8111-111111111111",
        "email": "sam@example.com",
        "created_at": "2026-08-21T09:00:00Z",
        "onboarding_complete": false
      }
    }
    """

    func testHealthDecodes() async throws {
        StubURLProtocol.enqueue(json: """
        {"status":"ok","version":"test","checks":[{"name":"postgres","status":"ok","latency_ms":3}]}
        """)
        let health = try await makeClient().health()
        XCTAssertEqual(health.status, "ok")
        XCTAssertEqual(health.checks.first?.name, "postgres")
        XCTAssertEqual(health.checks.first?.latencyMs, 3)
    }

    func testHealthSendsNoAuthorizationHeader() async throws {
        StubURLProtocol.enqueue(json: #"{"status":"ok","version":"t","checks":[]}"#)
        _ = try await makeClient().health()
        XCTAssertNil(StubURLProtocol.requests.first?.value(forHTTPHeaderField: "Authorization"))
    }

    func testStartSignInDecodesTheChallenge() async throws {
        StubURLProtocol.enqueue(json: """
        {"challenge_id":"1A2B3C4D-1111-4111-8111-111111111111",
         "expires_at":"2026-08-21T09:10:00Z","code_length":6,"resend_after_seconds":30}
        """)
        let challenge = try await makeClient().startSignIn(email: "sam@example.com")
        XCTAssertEqual(challenge.codeLength, 6)
        XCTAssertEqual(challenge.resendAfterSeconds, 30)
    }

    func testVerifyStoresTheSession() async throws {
        let tokens = InMemoryTokenStore()
        StubURLProtocol.enqueue(json: Self.sessionJSON)

        let now = Date(timeIntervalSince1970: 1_800_000_000)
        let client = makeClient(tokens: tokens, now: now)
        let session = try await client.verifySignIn(
            challengeID: UUID(uuidString: "1A2B3C4D-1111-4111-8111-111111111111")!,
            code: "482913",
            deviceName: "test"
        )
        XCTAssertEqual(session.user.email, "sam@example.com")

        let stored = try XCTUnwrap(try tokens.load())
        XCTAssertEqual(stored.accessToken, "access-1")
        XCTAssertEqual(stored.refreshToken, "refresh-1")
        XCTAssertEqual(stored.accessTokenExpiresAt, now.addingTimeInterval(900))
    }

    func testAuthenticatedCallSendsTheBearerToken() async throws {
        let tokens = InMemoryTokenStore(session: StoredSession(
            accessToken: "access-1",
            refreshToken: "refresh-1",
            accessTokenExpiresAt: Date(timeIntervalSince1970: 1_800_000_900),
            userID: UUID()
        ))
        StubURLProtocol.enqueue(json: """
        {"id":"8B1F2A7C-1111-4111-8111-111111111111","email":"sam@example.com",
         "created_at":"2026-08-21T09:00:00Z","onboarding_complete":false}
        """)
        _ = try await makeClient(tokens: tokens).currentUser()
        XCTAssertEqual(StubURLProtocol.requests.first?.value(forHTTPHeaderField: "Authorization"), "Bearer access-1")
    }

    func testCallWithNoSessionFailsWithoutHittingTheNetwork() async {
        do {
            _ = try await makeClient().currentUser()
            XCTFail("expected notSignedIn")
        } catch APIError.notSignedIn {
            XCTAssertTrue(StubURLProtocol.requests.isEmpty, "a signed-out call should not reach the network")
        } catch {
            XCTFail("unexpected error: \(error)")
        }
    }

    // An access token past its expiry is refreshed before the call, not after a 401.
    func testStaleAccessTokenIsRefreshedFirst() async throws {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        let tokens = InMemoryTokenStore(session: StoredSession(
            accessToken: "expired",
            refreshToken: "refresh-1",
            accessTokenExpiresAt: now.addingTimeInterval(-1),
            userID: UUID()
        ))
        StubURLProtocol.enqueue(json: Self.sessionJSON)
        StubURLProtocol.enqueue(json: """
        {"id":"8B1F2A7C-1111-4111-8111-111111111111","email":"sam@example.com",
         "created_at":"2026-08-21T09:00:00Z","onboarding_complete":false}
        """)

        _ = try await makeClient(tokens: tokens, now: now).currentUser()

        let paths = StubURLProtocol.requests.compactMap(\.url?.path)
        XCTAssertEqual(paths, ["/v1/auth/refresh", "/v1/me"])
        XCTAssertEqual(try tokens.load()?.accessToken, "access-1")
    }

    // A refresh that is itself rejected means the chain is gone: clear and stop.
    func testRejectedRefreshClearsTheSession() async throws {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        let tokens = InMemoryTokenStore(session: StoredSession(
            accessToken: "expired",
            refreshToken: "refresh-1",
            accessTokenExpiresAt: now.addingTimeInterval(-1),
            userID: UUID()
        ))
        StubURLProtocol.enqueue(status: 401, json: """
        {"code":"unauthorized","copy_key":"error.sessionExpired.body","request_id":"r-1"}
        """)

        do {
            _ = try await makeClient(tokens: tokens, now: now).currentUser()
            XCTFail("expected notSignedIn")
        } catch APIError.notSignedIn {
            XCTAssertNil(try tokens.load(), "a dead chain must not be left in the Keychain")
        } catch {
            XCTFail("unexpected error: \(error)")
        }
    }

    func testProblemBodyBecomesAPIError() async {
        StubURLProtocol.enqueue(status: 401, json: """
        {"code":"code_incorrect","copy_key":"auth.verify.error.codeIncorrect","request_id":"r-2"}
        """)
        do {
            _ = try await makeClient().verifySignIn(challengeID: UUID(), code: "000000", deviceName: nil)
            XCTFail("expected an API error")
        } catch let error as APIError {
            XCTAssertEqual(error.code, .codeIncorrect)
            XCTAssertEqual(error.bodyKey, .authVerifyErrorCodeIncorrect)
            XCTAssertEqual(error.titleKey, .errorServerTitle)
            XCTAssertFalse(error.isRetryable, "a wrong code is corrected, not retried")
        } catch {
            XCTFail("unexpected error: \(error)")
        }
    }

    func testRateLimitCarriesItsParameters() async {
        StubURLProtocol.enqueue(status: 429, json: """
        {"code":"rate_limited","copy_key":"error.rateLimited.body","request_id":"r-3",
         "params":{"seconds":"3600"},"retry_after_seconds":3600}
        """)
        do {
            _ = try await makeClient().startSignIn(email: "sam@example.com")
            XCTFail("expected an API error")
        } catch let error as APIError {
            XCTAssertEqual(error.titleKey, .errorRateLimitedTitle)
            XCTAssertEqual(error.params["seconds"], "3600")
            let rendered = Copywriter.bundled(error.bodyKey, error.params)
            XCTAssertTrue(rendered.contains("3600"), "the wait should be interpolated: \(rendered)")
        } catch {
            XCTFail("unexpected error: \(error)")
        }
    }

    // A future server may send a code this build has never heard of. It must not crash,
    // and it must still say something.
    func testUnknownErrorCodeFallsBackToGenericCopy() async {
        StubURLProtocol.enqueue(status: 418, json: """
        {"code":"a_code_from_the_future","copy_key":"some.key.we.do.not.have","request_id":"r-4"}
        """)
        do {
            _ = try await makeClient().health()
            XCTFail("expected an API error")
        } catch let error as APIError {
            XCTAssertEqual(error.bodyKey, .errorServerBody)
            XCTAssertFalse(Copywriter.bundled(error.bodyKey).isEmpty)
        } catch {
            XCTFail("unexpected error: \(error)")
        }
    }

    func testTimestampsWithAndWithoutFractionalSecondsBothDecode() async throws {
        for timestamp in ["2026-08-21T09:00:00Z", "2026-08-21T09:00:00.123456Z"] {
            StubURLProtocol.reset()
            StubURLProtocol.enqueue(json: """
            {"challenge_id":"1A2B3C4D-1111-4111-8111-111111111111",
             "expires_at":"\(timestamp)","code_length":6,"resend_after_seconds":30}
            """)
            let challenge = try await makeClient().startSignIn(email: "sam@example.com")
            XCTAssertEqual(challenge.expiresAt.timeIntervalSince1970, 1_787_302_800, accuracy: 1) // 2026-08-21T09:00:00Z
        }
    }

    func testSignOutClearsLocallyEvenWhenTheCallFails() async throws {
        let tokens = InMemoryTokenStore(session: StoredSession(
            accessToken: "access-1",
            refreshToken: "refresh-1",
            accessTokenExpiresAt: Date(timeIntervalSince1970: 1_800_000_900),
            userID: UUID()
        ))
        StubURLProtocol.enqueue(status: 500, json: """
        {"code":"server_error","copy_key":"error.server.body","request_id":"r-5"}
        """)
        await makeClient(tokens: tokens).signOut()
        XCTAssertNil(try tokens.load(), "someone who asked to sign out is signed out")
    }
}
