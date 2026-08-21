package app.pod.api

import app.pod.api.generated.Pod
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * The graduation screen: one answer, and the pod it belongs to.
 *
 * Answering does not restart or close the pod — the server settles that once the answer
 * cannot change or the deadline arrives (ADR-023). So this holds the pod the server sends
 * back and nothing else; there is no local guess at what the others will say.
 */
public class GraduationRepository(
    private val client: PodApiClient,
    initial: Pod,
) {
    private val _pod = MutableStateFlow(initial)
    public val pod: StateFlow<Pod> = _pod.asStateFlow()

    private val _failure = MutableStateFlow<ApiException?>(null)
    public val failure: StateFlow<ApiException?> = _failure.asStateFlow()

    private val _isWorking = MutableStateFlow(false)
    public val isWorking: StateFlow<Boolean> = _isWorking.asStateFlow()

    /**
     * Records the caller's answer. Changing it later is an ordinary second call — someone
     * who said no and changed their mind is exactly who this product is for.
     */
    public suspend fun answer(continuing: Boolean) {
        _isWorking.value = true
        _failure.value = null
        try {
            _pod.value = client.continueCycle(_pod.value.id, continuing)
        } catch (failure: ApiException) {
            _failure.value = failure
        } finally {
            _isWorking.value = false
        }
    }

    /**
     * Re-reads the pod, for when the screen returns to the foreground. There is nothing to
     * poll — the others answer when they answer — so this checks once.
     */
    public suspend fun refresh() {
        runCatching { client.currentPod() }.getOrNull()?.let { _pod.value = it }
    }
}
