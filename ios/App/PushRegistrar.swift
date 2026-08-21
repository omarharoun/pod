import SwiftUI
import PodAPI
#if canImport(UIKit)
import UIKit
import UserNotifications
#endif

/// Asks for notification permission and hands the resulting token to the API.
///
/// Permission is asked for **when a pod forms**, not at launch. At launch the app has
/// nothing to notify anyone about and the request is a tax on a stranger; once there are
/// two other people involved, "can we let you know when your pod does something" is a
/// question with an obvious subject (`notifications.permission.*`).
@MainActor
@Observable
public final class PushRegistrar {
    public private(set) var hasAsked = false

    private let client: APIClient
    private var token: String?

    public init(client: APIClient) {
        self.client = client
    }

    /// Asks once, and registers whatever token comes back. Declining is a normal outcome:
    /// the product works without push and must never behave as though it does not.
    public func askIfNeeded() async {
        #if canImport(UIKit)
        guard !hasAsked else { return }
        hasAsked = true

        let centre = UNUserNotificationCenter.current()
        // Only the status crosses back, not the settings object. `UNNotificationSettings`
        // is a non-Sendable class, so `await centre.notificationSettings()` would hand a
        // reference across an actor boundary; the status is an enum and travels safely.
        let status: UNAuthorizationStatus = await withCheckedContinuation { continuation in
            centre.getNotificationSettings { settings in
                continuation.resume(returning: settings.authorizationStatus)
            }
        }
        guard status == .notDetermined else {
            if status == .authorized {
                UIApplication.shared.registerForRemoteNotifications()
            }
            return
        }
        let granted = (try? await centre.requestAuthorization(options: [.alert, .sound])) ?? false
        guard granted else { return }
        UIApplication.shared.registerForRemoteNotifications()
        #endif
    }

    /// Called by the app delegate when APNs hands over a token.
    public func received(deviceToken data: Data) {
        let token = data.map { String(format: "%02x", $0) }.joined()
        guard token != self.token else { return }
        self.token = token
        Task {
            // A registration that fails is retried next launch, not surfaced: nobody
            // needs to be told their notifications did not register.
            try? await client.registerDevice(
                token: token,
                platform: .ios,
                // BCP 47, as the contract specifies. `Locale.identifier` is the ICU
                // form — `en_US` — which is not the same string and is what Android
                // already sends correctly.
                locale: Locale.current.identifier(.bcp47)
            )
        }
    }

    /// Called on sign-out, so a shared device stops receiving somebody else's pod.
    public func unregister() async {
        guard let token else { return }
        try? await client.unregisterDevice(token: token, platform: .ios)
        self.token = nil
    }
}

#if canImport(UIKit)
/// The one thing that has to be a `UIApplicationDelegate`: APNs hands the device token to
/// a delegate method and nowhere else.
final class PushAppDelegate: NSObject, UIApplicationDelegate {
    /// Set by `PodApp` before the delegate can receive anything.
    ///
    /// Main-actor isolated rather than `nonisolated(unsafe)`: both ends genuinely are on
    /// the main actor — `PodApp` writes it from a `.task`, and the delegate reads it from
    /// a `@MainActor` hop — so the compiler can check this instead of being told to trust
    /// it.
    @MainActor static var registrar: PushRegistrar?

    func application(
        _ application: UIApplication,
        didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data
    ) {
        Task { @MainActor in
            Self.registrar?.received(deviceToken: deviceToken)
        }
    }

    func application(
        _ application: UIApplication,
        didFailToRegisterForRemoteNotificationsWithError error: Error
    ) {
        // Simulators and devices without a provisioning profile land here. Push is a
        // nicety; the week loop does not depend on it.
    }
}
#endif
