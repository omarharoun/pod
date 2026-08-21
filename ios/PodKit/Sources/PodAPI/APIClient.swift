import Foundation

/// The one way this app talks to Pod.
///
/// `URLSession` and the generated models, no third-party networking (CLAUDE.md §5). It is
/// an actor because it owns the refresh path: two calls hitting a stale access token at
/// once must produce one refresh, not two, and the second one to arrive must not revoke
/// the first one's freshly rotated token (ADR-002).
public actor APIClient {
    private let baseURL: URL
    private let session: URLSession
    private let tokens: TokenStore
    private let now: @Sendable () -> Date

    private let encoder: JSONEncoder
    private let decoder: JSONDecoder

    /// A refresh already in flight. Everything else awaits it rather than starting another.
    private var refreshInFlight: Task<StoredSession, Error>?

    public init(
        baseURL: URL,
        tokens: TokenStore,
        session: URLSession = .shared,
        now: @escaping @Sendable () -> Date = { Date() }
    ) {
        self.baseURL = baseURL
        self.tokens = tokens
        self.session = session
        self.now = now

        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        self.encoder = encoder

        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let text = try decoder.singleValueContainer().decode(String.self)
            guard let date = ISO8601DateFormatter.podParsers.lazy.compactMap({ $0.date(from: text) }).first else {
                throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "not an RFC 3339 timestamp: \(text)"))
            }
            return date
        }
        self.decoder = decoder
    }

    // MARK: - System

    public func health() async throws -> Health {
        try await send(Request(method: "GET", path: "/v1/health", authenticated: false))
    }

    // MARK: - Auth

    public func startSignIn(email: String) async throws -> AuthStartResponse {
        try await send(Request(
            method: "POST",
            path: "/v1/auth/start",
            body: try encoder.encode(AuthStartRequest(email: email)),
            authenticated: false
        ))
    }

    /// Verifies the code and stores the resulting session.
    @discardableResult
    public func verifySignIn(challengeID: UUID, code: String, deviceName: String?) async throws -> AuthSession {
        let response: AuthSession = try await send(Request(
            method: "POST",
            path: "/v1/auth/verify",
            body: try encoder.encode(AuthVerifyRequest(challengeId: challengeID, code: code, deviceName: deviceName)),
            authenticated: false
        ))
        try store(response)
        return response
    }

    public func currentUser() async throws -> User {
        try await send(Request(method: "GET", path: "/v1/me"))
    }

    // MARK: - Onboarding

    public func onboardingState() async throws -> OnboardingState {
        try await send(Request(method: "GET", path: "/v1/onboarding/state"))
    }

    public func saveIntent(_ intent: IntentRequest) async throws -> Intent {
        try await send(Request(
            method: "PUT",
            path: "/v1/onboarding/intent",
            body: try encoder.encode(intent)
        ))
    }

    public func joinPool() async throws -> PoolTicket {
        try await send(Request(method: "POST", path: "/v1/pool"))
    }

    public func leavePool() async throws {
        let _: EmptyResponse = try await send(Request(method: "DELETE", path: "/v1/pool"))
    }

    // MARK: - Pod

    // MARK: - The week

    public func currentWeek(podID: UUID) async throws -> Week {
        try await send(Request(method: "GET", path: "/v1/pods/\(podID.uuidString.lowercased())/weeks/current"))
    }

    public func weeks(podID: UUID) async throws -> WeekList {
        try await send(Request(method: "GET", path: "/v1/pods/\(podID.uuidString.lowercased())/weeks"))
    }

    public func setWeekAction(podID: UUID, weekID: UUID, actionText: String) async throws -> Week {
        try await send(Request(
            method: "PUT",
            path: "/v1/pods/\(podID.uuidString.lowercased())/weeks/\(weekID.uuidString.lowercased())/action",
            body: try encoder.encode(WeekActionRequest(actionText: actionText))
        ))
    }

    // MARK: - Proof

    /// Posts a proof: reserve a slot, PUT the bytes straight to storage, then commit.
    ///
    /// The three steps live together because they are one action from the person's point
    /// of view, and because a slot with no bytes in it must never be committed (ADR-008).
    public func postProof(
        podID: UUID,
        weekID: UUID,
        kind: ProofKind,
        contentType: String,
        data: Data,
        durationMs: Int? = nil
    ) async throws -> Proof {
        let base = "/v1/pods/\(podID.uuidString.lowercased())/weeks/\(weekID.uuidString.lowercased())/proof"
        let slot: ProofUpload = try await send(Request(
            method: "POST",
            path: base,
            body: try encoder.encode(ProofUploadRequest(
                kind: kind,
                contentType: contentType,
                byteSize: data.count,
                durationMs: durationMs
            ))
        ))

        try await putBytes(data, to: slot)

        return try await send(Request(
            method: "POST",
            path: "\(base)/\(slot.proofId.uuidString.lowercased())/commit"
        ))
    }

    public func proof(podID: UUID, weekID: UUID, proofID: UUID) async throws -> Proof {
        try await send(Request(
            method: "GET",
            path: "/v1/pods/\(podID.uuidString.lowercased())/weeks/\(weekID.uuidString.lowercased())/proof/\(proofID.uuidString.lowercased())"
        ))
    }

    /// Fetches the bytes behind a short-lived view link.
    public func proofData(at url: URL) async throws -> Data {
        do {
            let (data, response) = try await session.data(from: url)
            guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                // A view link that has expired, or a proof already swept. Neither is an
                // error worth a message — the thing is simply not there.
                throw APIError.malformedResponse
            }
            return data
        } catch let error as URLError {
            throw APIError.offline(error)
        }
    }

    /// PUTs proof bytes to the signed URL. This never touches the API: it is the whole
    /// point of the two-step upload that the bytes go straight to storage (ADR-008).
    private func putBytes(_ data: Data, to slot: ProofUpload) async throws {
        var request = URLRequest(url: slot.uploadUrl)
        request.httpMethod = slot.uploadMethod
        for (name, value) in slot.uploadHeaders ?? [:] {
            // Content-Length is set by URLSession from the body; setting it here as well
            // makes the request malformed.
            guard name.lowercased() != "content-length" else { continue }
            request.setValue(value, forHTTPHeaderField: name)
        }

        do {
            let (_, response) = try await session.upload(for: request, from: data)
            guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                throw APIError.malformedResponse
            }
        } catch let error as URLError {
            throw APIError.offline(error)
        }
    }

    // MARK: - Pod

    /// The caller's active pod, or `nil` when they have not been matched yet.
    ///
    /// The server answers 204 rather than 404 for "no pod": not being matched yet is a
    /// state, not a failure, and the two should not look the same to the client.
    public func currentPod() async throws -> Pod? {
        let response: PodOrNothing = try await send(Request(method: "GET", path: "/v1/pods/current"))
        return response.pod
    }

    /// Ends this device's session. The local tokens are cleared even if the call fails —
    /// someone who asked to sign out is signed out.
    public func signOut() async {
        defer { try? tokens.clear() }
        guard let stored = try? tokens.load(),
              let body = try? encoder.encode(AuthRefreshRequest(refreshToken: stored.refreshToken))
        else { return }
        let _: EmptyResponse? = try? await send(Request(method: "POST", path: "/v1/auth/logout", body: body))
    }

    /// The session restored from storage, if there is one.
    public func restoredSession() throws -> StoredSession? {
        try tokens.load()
    }

    // MARK: - Sending

    private struct Request {
        var method: String
        var path: String
        var body: Data?
        var authenticated = true
    }

    /// A 204 has no body; `EmptyResponse` gives `send` something to decode into.
    private struct EmptyResponse: Decodable {}

    /// Carries the difference between "here is your pod" and "you do not have one yet",
    /// which arrive as 200 and 204 on the same route.
    private struct PodOrNothing: Decodable {
        let pod: Pod?

        init(from decoder: Decoder) throws {
            pod = try Pod(from: decoder)
        }

        init() {
            pod = nil
        }
    }

    private func send<Response: Decodable>(_ request: Request) async throws -> Response {
        var accessToken: String?
        if request.authenticated {
            accessToken = try await validAccessToken()
        }

        do {
            return try await perform(request, accessToken: accessToken)
        } catch APIError.api(let problem, let status) where status == 401 && request.authenticated && problem.code == .sessionExpired {
            // The access token was rejected even though it looked fresh — clock skew, or
            // a server-side revocation. One refresh, one retry, then give up.
            let renewed = try await refreshSession()
            return try await perform(request, accessToken: renewed.accessToken)
        }
    }

    private func perform<Response: Decodable>(_ request: Request, accessToken: String?) async throws -> Response {
        var urlRequest = URLRequest(url: baseURL.appendingPathComponent(request.path))
        urlRequest.httpMethod = request.method
        urlRequest.setValue("application/json", forHTTPHeaderField: "Accept")
        if let body = request.body {
            urlRequest.httpBody = body
            urlRequest.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        if let accessToken {
            urlRequest.setValue("Bearer \(accessToken)", forHTTPHeaderField: "Authorization")
        }

        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: urlRequest)
        } catch let error as URLError {
            throw APIError.offline(error)
        }

        guard let http = response as? HTTPURLResponse else { throw APIError.malformedResponse }

        guard (200..<300).contains(http.statusCode) else {
            guard let problem = try? decoder.decode(ApiError.self, from: data) else {
                throw APIError.malformedResponse
            }
            throw APIError.api(problem, status: http.statusCode)
        }

        if http.statusCode == 204 || data.isEmpty {
            if let nothing = PodOrNothing() as? Response { return nothing }
            guard let empty = EmptyResponse() as? Response else { throw APIError.malformedResponse }
            return empty
        }
        if Response.self == EmptyResponse.self {
            guard let empty = EmptyResponse() as? Response else { throw APIError.malformedResponse }
            return empty
        }
        do {
            return try decoder.decode(Response.self, from: data)
        } catch {
            throw APIError.malformedResponse
        }
    }

    // MARK: - Session

    private func validAccessToken() async throws -> String {
        guard let stored = try tokens.load() else { throw APIError.notSignedIn }
        if stored.accessTokenIsFresh(at: now()) {
            return stored.accessToken
        }
        return try await refreshSession().accessToken
    }

    private func refreshSession() async throws -> StoredSession {
        if let existing = refreshInFlight {
            return try await existing.value
        }
        let task = Task<StoredSession, Error> {
            guard let stored = try self.tokens.load() else { throw APIError.notSignedIn }
            do {
                let renewed: AuthSession = try await self.perform(
                    Request(
                        method: "POST",
                        path: "/v1/auth/refresh",
                        body: try self.encoder.encode(AuthRefreshRequest(refreshToken: stored.refreshToken)),
                        authenticated: false
                    ),
                    accessToken: nil
                )
                return try self.store(renewed)
            } catch APIError.api(_, let status) where status == 401 {
                // The chain is gone: either it expired or reuse was detected. There is
                // nothing left to retry, so the person signs in again.
                try? self.tokens.clear()
                throw APIError.notSignedIn
            }
        }
        refreshInFlight = task
        defer { refreshInFlight = nil }
        return try await task.value
    }

    @discardableResult
    private func store(_ session: AuthSession) throws -> StoredSession {
        let stored = StoredSession(
            accessToken: session.accessToken,
            refreshToken: session.refreshToken,
            accessTokenExpiresAt: now().addingTimeInterval(TimeInterval(session.expiresInSeconds)),
            userID: session.user.id
        )
        try tokens.save(stored)
        return stored
    }
}

private extension ISO8601DateFormatter {
    /// The API sends RFC 3339 with or without fractional seconds depending on the value,
    /// and `.iso8601` only accepts one of those shapes.
    static let podParsers: [ISO8601DateFormatter] = {
        let withFraction = ISO8601DateFormatter()
        withFraction.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let plain = ISO8601DateFormatter()
        plain.formatOptions = [.withInternetDateTime]
        return [withFraction, plain]
    }()
}
