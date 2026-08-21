package app.pod.api

import app.pod.api.generated.MemberWeek
import app.pod.api.generated.MemberWeekState
import app.pod.api.generated.Pod
import app.pod.api.generated.ProofKind
import app.pod.api.generated.Week
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.datetime.Clock

/** What the week screen is doing. */
public sealed interface WeekPhase {
    public data object Loading : WeekPhase
    public data class Ready(val week: Week) : WeekPhase
}

/**
 * The week screen's state: the action, the ring, and posting.
 *
 * Everything here is pod-level or per-member-state. There is deliberately no derived count
 * of anyone's personal weeks in this type (CLAUDE.md §2 rule 3).
 */
public class WeekRepository(
    private val client: PodApiClient,
    private val podId: String,
    private val now: () -> Long = { Clock.System.now().toEpochMilliseconds() },
) {
    private val _phase = MutableStateFlow<WeekPhase>(WeekPhase.Loading)
    public val phase: StateFlow<WeekPhase> = _phase.asStateFlow()

    private val _failure = MutableStateFlow<ApiException?>(null)
    public val failure: StateFlow<ApiException?> = _failure.asStateFlow()

    private val _isPosting = MutableStateFlow(false)
    public val isPosting: StateFlow<Boolean> = _isPosting.asStateFlow()

    private val _isSavingAction = MutableStateFlow(false)
    public val isSavingAction: StateFlow<Boolean> = _isSavingAction.asStateFlow()

    public val week: Week?
        get() = (_phase.value as? WeekPhase.Ready)?.week

    public suspend fun load() {
        perform { _phase.value = WeekPhase.Ready(client.currentWeek(podId)) }
    }

    /**
     * Refreshes without dropping back to Loading. Someone looking at their pod should not
     * lose it to a dropped connection.
     */
    public suspend fun refresh() {
        try {
            _phase.value = WeekPhase.Ready(client.currentWeek(podId))
            _failure.value = null
        } catch (failure: ApiException) {
            _failure.value = failure
        }
    }

    public suspend fun setAction(text: String) {
        val current = week ?: return
        _isSavingAction.value = true
        try {
            perform { _phase.value = WeekPhase.Ready(client.setWeekAction(podId, current.id, text)) }
        } finally {
            _isSavingAction.value = false
        }
    }

    /** Posts a proof and reloads, so the ring moves as soon as the commit lands. */
    public suspend fun post(kind: ProofKind, contentType: String, bytes: ByteArray, durationMs: Int? = null) {
        val current = week ?: return
        _isPosting.value = true
        try {
            perform {
                client.postProof(podId, current.id, kind, contentType, bytes, durationMs)
                _phase.value = WeekPhase.Ready(client.currentWeek(podId))
            }
        } finally {
            _isPosting.value = false
        }
    }

    /**
     * Fetches a pod-mate's proof, or null when it has already gone — which is a normal
     * outcome, not a failure (CLAUDE.md §2 rule 4).
     */
    public suspend fun proofBytes(proofId: String): ByteArray? {
        val current = week ?: return null
        return try {
            val proof = client.proof(podId, current.id, proofId)
            proof.viewUrl?.let { client.proofBytes(it) }
        } catch (_: ApiException) {
            null
        }
    }

    /** Whether the caller can post right now: the week is open and they have not. */
    public fun canPost(userId: String): Boolean {
        val current = week ?: return false
        if (!current.isOpen(now())) return false
        return current.members.firstOrNull { it.memberId == userId }?.state == MemberWeekState.OPEN
    }

    public fun memberWeek(userId: String): MemberWeek? =
        week?.members?.firstOrNull { it.memberId == userId }

    public fun dismissFailure() {
        _failure.value = null
    }

    private suspend fun perform(work: suspend () -> Unit) {
        _failure.value = null
        try {
            work()
        } catch (failure: ApiException) {
            _failure.value = failure
        }
    }
}

/**
 * How many members are in, and how many there are. The only counting this app does, and it
 * is about the pod.
 */
public fun Week.podProgress(): Pair<Int, Int> =
    members.count { it.state == MemberWeekState.POSTED } to members.size

public fun Week.isOpen(nowMillis: Long): Boolean = closesAt.toEpochMilliseconds() > nowMillis

/** Whole days until the week closes, never negative. */
public fun Week.daysRemaining(nowMillis: Long): Int {
    val remaining = closesAt.toEpochMilliseconds() - nowMillis
    return if (remaining <= 0) 0 else (remaining / 86_400_000L).toInt()
}

/** The display name a pod-mate sees, matched from the pod's roster. */
public fun Pod.displayNameFor(memberId: String): String =
    members.firstOrNull { it.id == memberId }?.displayName.orEmpty()

/**
 * One [WeekRepository] per pod, kept for the process.
 *
 * A repository with no pod has nothing to show, so it cannot be a plain singleton; and a
 * new one per screen would drop the loaded week on every configuration change.
 */
public class WeekRepositories(private val client: PodApiClient) {
    private val byPod = mutableMapOf<String, WeekRepository>()

    @Synchronized
    public fun forPod(podId: String): WeekRepository =
        byPod.getOrPut(podId) { WeekRepository(client, podId) }
}
