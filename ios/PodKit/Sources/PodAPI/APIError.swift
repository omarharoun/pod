import Foundation

/// Everything that can go wrong on the way to a response.
///
/// The server never sends display text (ADR-003), so this type carries a ``CopyKey``
/// instead — the caller resolves it through ``Copywriter``.
public enum APIError: Error, Sendable {
    /// The request never got an answer: offline, timed out, DNS, TLS.
    case offline(URLError)
    /// The server answered with a problem body.
    case api(ApiError, status: Int)
    /// The server answered with something this build cannot read.
    case malformedResponse
    /// A signed-in call was made with no session, or the session could not be renewed.
    case notSignedIn

    /// The key for the body text to show. Titles are chosen by the client from ``code``.
    public var bodyKey: CopyKey {
        switch self {
        case .offline:
            return .errorNetworkBody
        case .malformedResponse, .notSignedIn:
            return .errorServerBody
        case .api(let problem, _):
            let key = CopyKey(rawValue: problem.copyKey)
            return Copywriter.bundled.contains(key) ? key : .errorServerBody
        }
    }

    /// The matching title, chosen locally. The server does not decide how an error is
    /// framed on a screen it cannot see.
    public var titleKey: CopyKey {
        switch self {
        case .offline:
            return .errorNetworkTitle
        case .malformedResponse, .notSignedIn:
            return .errorServerTitle
        case .api(let problem, _):
            switch problem.code {
            case .rateLimited: return .errorRateLimitedTitle
            case .unauthorized, .sessionExpired: return .errorSessionExpiredTitle
            default: return .errorServerTitle
            }
        }
    }

    /// Interpolation values for ``bodyKey``.
    public var params: [String: String] {
        if case .api(let problem, _) = self { return problem.params ?? [:] }
        return [:]
    }

    public var code: ErrorCode? {
        if case .api(let problem, _) = self { return problem.code }
        return nil
    }

    /// Whether offering "try again" makes sense. A wrong code is not retryable in this
    /// sense — the user changes the input, they do not repeat the request.
    public var isRetryable: Bool {
        switch self {
        case .offline:
            return true
        case .api(_, let status):
            return status >= 500
        case .malformedResponse, .notSignedIn:
            return false
        }
    }
}
