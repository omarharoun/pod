import Foundation
import Observation

/// Drives the graduation screen: one answer, and the pod it belongs to.
///
/// Answering does not restart or close the pod — the server settles that once the answer
/// cannot change or the deadline arrives (ADR-023). So this controller reports the pod
/// back and nothing else; there is no local guess at what the others will say.
@MainActor
@Observable
public final class GraduationController {
    public private(set) var pod: Pod
    public private(set) var failure: APIError?
    public private(set) var isWorking = false

    private let client: APIClient

    public init(client: APIClient, pod: Pod) {
        self.client = client
        self.pod = pod
    }

    /// Records the caller's answer. Changing it later is an ordinary second call —
    /// someone who said no and changed their mind is exactly who this product is for.
    public func answer(continuing: Bool) async {
        isWorking = true
        failure = nil
        defer { isWorking = false }
        do {
            pod = try await client.continueCycle(podID: pod.id, continuing: continuing)
        } catch let error as APIError {
            failure = error
        } catch {
            failure = .malformedResponse
        }
    }

    /// Re-reads the pod, for when the screen comes back to the foreground. There is
    /// nothing to poll — the others answer when they answer — so this checks once.
    public func refresh() async {
        guard let latest = try? await client.currentPod() else { return }
        pod = latest
    }

    /// Previews and tests only.
    func setPreviewPod(_ pod: Pod) {
        self.pod = pod
    }
}

public extension GraduationController {
    /// Previews only.
    func preview(pod: Pod) {
        setPreviewPod(pod)
    }
}
