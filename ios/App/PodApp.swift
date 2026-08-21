import SwiftUI
import PodAPI
#if canImport(UIKit)
import UIKit
#endif

@main
struct PodApp: App {
    #if canImport(UIKit)
    @UIApplicationDelegateAdaptor(PushAppDelegate.self) private var appDelegate
    #endif

    /// The single injected container (CLAUDE.md §5). Nothing else in the app is shared
    /// state, and nothing reaches for a global.
    @State private var environment = PodEnvironment.live(baseURL: AppConfiguration.apiBaseURL, deviceName: AppConfiguration.deviceName)

    @State private var push: PushRegistrar?

    var body: some Scene {
        WindowGroup {
            RootView(environment: environment)
                .task {
                    let registrar = push ?? PushRegistrar(client: environment.client)
                    push = registrar
                    #if canImport(UIKit)
                    PushAppDelegate.registrar = registrar
                    #endif
                }
                .environment(\.pushRegistrar, push)
        }
    }
}

enum AppConfiguration {
    /// Read from the Info.plist so a debug build can point at localhost without a code
    /// change. See ios/project.yml.
    static var apiBaseURL: URL {
        guard
            let raw = Bundle.main.object(forInfoDictionaryKey: "PodAPIBaseURL") as? String,
            let url = URL(string: raw)
        else {
            preconditionFailure("PodAPIBaseURL is missing from Info.plist")
        }
        return url
    }

    /// Shown only to the account owner, in their own device list.
    static var deviceName: String? {
        #if canImport(UIKit)
        UIDevice.current.name
        #else
        nil
        #endif
    }
}
