import Foundation

/// The tokens for one signed-in device.
public struct StoredSession: Codable, Hashable, Sendable {
    public let accessToken: String
    public let refreshToken: String
    public let accessTokenExpiresAt: Date
    public let userID: UUID

    public init(accessToken: String, refreshToken: String, accessTokenExpiresAt: Date, userID: UUID) {
        self.accessToken = accessToken
        self.refreshToken = refreshToken
        self.accessTokenExpiresAt = accessTokenExpiresAt
        self.userID = userID
    }

    /// Treats a token that is nearly expired as expired, so a request doesn't die in
    /// flight over a second's difference between this clock and the server's.
    public func accessTokenIsFresh(at now: Date, margin: TimeInterval = 30) -> Bool {
        now.addingTimeInterval(margin) < accessTokenExpiresAt
    }
}

/// Where the tokens live. Keychain in the app, in memory in tests.
public protocol TokenStore: Sendable {
    func load() throws -> StoredSession?
    func save(_ session: StoredSession) throws
    func clear() throws
}

/// A store that keeps tokens for the lifetime of the process. Tests and previews only —
/// the app uses ``KeychainTokenStore`` (CLAUDE.md §5).
public final class InMemoryTokenStore: TokenStore, @unchecked Sendable {
    private let lock = NSLock()
    private var session: StoredSession?

    public init(session: StoredSession? = nil) {
        self.session = session
    }

    public func load() throws -> StoredSession? {
        lock.withLock { session }
    }

    public func save(_ session: StoredSession) throws {
        lock.withLock { self.session = session }
    }

    public func clear() throws {
        lock.withLock { session = nil }
    }
}
