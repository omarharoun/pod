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
