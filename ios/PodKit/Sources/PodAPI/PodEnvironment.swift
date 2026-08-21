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
    /// The free lane's onboarding. Every other lane goes through ``onboarding(for:)``.
    public let onboarding: OnboardingController
    public let slots: SlotsController

    /// Created per pod, because a week controller with no pod has nothing to show.
    public func weekController(podID: UUID) -> WeekController {
        if let existing = weekControllers[podID] { return existing }
        let controller = WeekController(client: client, podID: podID)
        weekControllers[podID] = controller
        return controller
    }

    /// Created per pod, once.
    ///
    /// Deliberately not re-seeded from the argument on every call. `RootView` re-renders
    /// with the pod the onboarding phase is holding, which is older than the one the
    /// answer just came back with — re-seeding would flip the screen back to an
    /// unanswered choice the moment somebody answered it. The controller owns its pod
    /// after creation and refreshes it itself.
    public func graduationController(for pod: Pod) -> GraduationController {
        if let existing = graduationControllers[pod.id] { return existing }
        let controller = GraduationController(client: client, pod: pod)
        graduationControllers[pod.id] = controller
        return controller
    }

    /// One onboarding controller per lane, created once.
    ///
    /// Per lane rather than one switchable controller: a half-answered draft belongs to
    /// the thing it is about, and moving between lanes must not carry it across.
    public func onboarding(for slot: Int) -> OnboardingController {
        if slot == 0 { return onboarding }
        if let existing = onboardingControllers[slot] { return existing }
        let controller = OnboardingController(client: client, slot: slot)
        onboardingControllers[slot] = controller
        return controller
    }

    private var onboardingControllers: [Int: OnboardingController] = [:]
    private var weekControllers: [UUID: WeekController] = [:]
    private var graduationControllers: [UUID: GraduationController] = [:]

    public init(
        client: APIClient,
        copy: Copywriter = .bundled,
        deviceName: String? = nil,
        broker: PurchaseBroker = UnavailablePurchaseBroker()
    ) {
        self.client = client
        self.copy = copy
        self.session = SessionController(client: client, deviceName: deviceName)
        self.onboarding = OnboardingController(client: client)
        // The purchase broker is a seam: an app with no store configuration behaves like
        // someone who cancelled, so the product works without a paywall rather than
        // breaking around one.
        self.slots = SlotsController(client: client, broker: broker)
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
