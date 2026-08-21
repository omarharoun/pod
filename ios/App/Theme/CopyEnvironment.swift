import SwiftUI
import PodAPI

private struct CopywriterKey: EnvironmentKey {
    static let defaultValue = Copywriter.bundled
}

extension EnvironmentValues {
    /// The copy catalogue. Views read every string through this — see CLAUDE.md §2 rule 1.
    var copy: Copywriter {
        get { self[CopywriterKey.self] }
        set { self[CopywriterKey.self] = newValue }
    }
}

private struct PushRegistrarKey: EnvironmentKey {
    static let defaultValue: PushRegistrar? = nil
}

extension EnvironmentValues {
    /// Nil until the app has built one, and nil forever in previews — every caller has to
    /// cope with push being unavailable, which is also true of a person who declines it.
    var pushRegistrar: PushRegistrar? {
        get { self[PushRegistrarKey.self] }
        set { self[PushRegistrarKey.self] = newValue }
    }
}
