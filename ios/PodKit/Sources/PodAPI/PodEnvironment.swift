import Foundation

/// The one injected container.
///
/// CLAUDE.md §5 allows exactly one: everything else is passed in. Nothing here is a
/// global, and nothing reaches for a shared instance — a view gets this through the
/// SwiftUI environment and a test constructs its own.
@MainActor
public final class PodEnvironment {
    public let client: APIClient
    public let copy: Copywriter
    public let session: SessionController
    public let onboarding: OnboardingController

    public init(client: APIClient, copy: Copywriter = .bundled, deviceName: String? = nil) {
        self.client = client
        self.copy = copy
        self.session = SessionController(client: client, deviceName: deviceName)
        self.onboarding = OnboardingController(client: client)
    }

    /// The app's real environment.
    public static func live(baseURL: URL, deviceName: String?) -> PodEnvironment {
        #if canImport(Security)
        let tokens: TokenStore = KeychainTokenStore()
        #else
        let tokens: TokenStore = InMemoryTokenStore()
        #endif
        return PodEnvironment(
            client: APIClient(baseURL: baseURL, tokens: tokens),
            deviceName: deviceName
        )
    }

    /// An environment backed by nothing, for previews.
    public static func preview(
        state: SessionState = .signedOut,
        onboarding phase: OnboardingPhase = .questions(.goal)
    ) -> PodEnvironment {
        let environment = PodEnvironment(
            client: APIClient(baseURL: URL(string: "https://preview.invalid")!, tokens: InMemoryTokenStore())
        )
        environment.session.preview(state: state)
        environment.onboarding.preview(phase: phase)
        return environment
    }
}

public extension SessionController {
    /// Puts the controller into a fixed state. Previews only.
    func preview(state: SessionState) {
        setPreviewState(state)
    }
}
