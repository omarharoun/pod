import Foundation
import Observation

/// Where the person is in the sign-in flow.
///
/// Sign-in is three states and no more, because it is three screens and no more — a
/// person should be in the app inside fifteen seconds (CLAUDE.md §2 rule 7).
public enum SessionState: Sendable, Equatable {
    /// Nothing known yet: reading the Keychain, or checking a restored session.
    case restoring
    /// No session. The email screen.
    case signedOut
    /// A code is outstanding for `email`.
    case awaitingCode(email: String, expiresAt: Date, resendAfter: TimeInterval)
    /// In.
    case signedIn(User)
}

/// Drives the sign-in flow and holds the signed-in user.
///
/// `@Observable` and main-actor-bound, per CLAUDE.md §5. It is created once by
/// ``PodEnvironment`` and injected; there is no shared instance to reach for.
@MainActor
@Observable
public final class SessionController {
    public private(set) var state: SessionState = .restoring
    /// The last failure, for the screen to render. Cleared on the next attempt.
    public private(set) var failure: APIError?
    public private(set) var isWorking = false

    private let client: APIClient
    private var challengeID: UUID?
    private let deviceName: String?

    public init(client: APIClient, deviceName: String? = nil) {
        self.client = client
        self.deviceName = deviceName
    }

    /// Restores a session from storage if there is one. Safe to call on every launch.
    public func restore() async {
        state = .restoring
        do {
            guard try await client.restoredSession() != nil else {
                state = .signedOut
                return
            }
            state = .signedIn(try await client.currentUser())
        } catch APIError.notSignedIn {
            state = .signedOut
        } catch {
            // A restored session that cannot be confirmed right now is not a reason to
            // sign someone out — they may just be on a train.
            failure = error as? APIError
            state = .signedOut
        }
    }

    public func sendCode(to email: String) async {
        await perform {
            let challenge = try await self.client.startSignIn(email: email)
            self.challengeID = challenge.challengeId
            self.state = .awaitingCode(
                email: email,
                expiresAt: challenge.expiresAt,
                resendAfter: TimeInterval(challenge.resendAfterSeconds)
            )
        }
    }

    /// Sends a fresh code for the address already on screen.
    public func resendCode() async {
        guard case .awaitingCode(let email, _, _) = state else { return }
        await sendCode(to: email)
    }

    public func submit(code: String) async {
        guard let challengeID else { return }
        await perform {
            let session = try await self.client.verifySignIn(
                challengeID: challengeID,
                code: code,
                deviceName: self.deviceName
            )
            self.challengeID = nil
            self.state = .signedIn(session.user)
        }
    }

    /// Returns to the email screen, abandoning the outstanding code.
    public func changeEmail() {
        challengeID = nil
        failure = nil
        state = .signedOut
    }

    public func signOut() async {
        await client.signOut()
        challengeID = nil
        failure = nil
        state = .signedOut
    }

    public func dismissFailure() {
        failure = nil
    }

    /// Sets the state directly. Previews and tests only — the app moves through the flow.
    func setPreviewState(_ state: SessionState) {
        self.state = state
    }

    private func perform(_ work: @Sendable @escaping () async throws -> Void) async {
        isWorking = true
        failure = nil
        defer { isWorking = false }
        do {
            try await work()
        } catch let error as APIError {
            failure = error
        } catch {
            failure = .malformedResponse
        }
    }
}
