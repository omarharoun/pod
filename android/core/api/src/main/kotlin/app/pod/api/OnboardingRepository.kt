package app.pod.api

import app.pod.api.generated.GoalCategory
import app.pod.api.generated.IntentRequest
import app.pod.api.generated.Pod
import app.pod.api.generated.PoolTicket
import app.pod.api.generated.SelfReportReliability
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.datetime.TimeZone

/** The four things onboarding asks, one per screen (docs/DESIGN.md). */
public enum class OnboardingQuestion {
    GOAL,
    CATEGORY,
    INTENSITY,
    RELIABILITY,
    ;

    public fun next(): OnboardingQuestion? = entries.getOrNull(ordinal + 1)

    public fun previous(): OnboardingQuestion? = entries.getOrNull(ordinal - 1)
}

/**
 * What has been answered so far. Held locally until the whole thing is submitted: a
 * half-filled intent is not something the server needs to know about.
 */
public data class IntentDraft(
    val goalText: String = "",
    val displayName: String = "",
    val category: GoalCategory? = null,
    /** 1..5 — see ADR-010. The number is never shown; the screen shows the phrase. */
    val intensity: Int? = null,
    val selfReport: SelfReportReliability? = null,
    val timezone: String = TimeZone.currentSystemDefault().id,
) {
    public fun isAnswered(question: OnboardingQuestion): Boolean = when (question) {
        OnboardingQuestion.GOAL -> goalText.trim().length >= MIN_GOAL_LENGTH
        OnboardingQuestion.CATEGORY -> category != null
        OnboardingQuestion.INTENSITY -> intensity != null
        OnboardingQuestion.RELIABILITY -> selfReport != null
    }

    internal fun toRequest(): IntentRequest? {
        val category = category ?: return null
        val intensity = intensity ?: return null
        val selfReport = selfReport ?: return null
        return IntentRequest(
            goalText = goalText.trim(),
            goalCategory = category,
            intensity = intensity,
            timezone = timezone,
            selfReportReliability = selfReport,
            displayName = displayName.trim().ifEmpty { null },
        )
    }

    private companion object {
        const val MIN_GOAL_LENGTH = 3
    }
}

/** Where a signed-in person is: still answering, waiting for a window, or in a pod. */
public sealed interface OnboardingPhase {
    public data object Loading : OnboardingPhase
    public data class Questions(val question: OnboardingQuestion) : OnboardingPhase
    public data class Waiting(val ticket: PoolTicket) : OnboardingPhase
    public data class Matched(val pod: Pod) : OnboardingPhase
}

/**
 * Everything between signing in and having a pod.
 *
 * Separate from [SessionRepository] because they answer different questions — one owns
 * whether you are signed in, this one owns what you see once you are.
 */
public class OnboardingRepository(private val client: PodApiClient) {

    private val _phase = MutableStateFlow<OnboardingPhase>(OnboardingPhase.Loading)
    public val phase: StateFlow<OnboardingPhase> = _phase.asStateFlow()

    private val _draft = MutableStateFlow(IntentDraft())
    public val draft: StateFlow<IntentDraft> = _draft.asStateFlow()

    private val _failure = MutableStateFlow<ApiException?>(null)
    public val failure: StateFlow<ApiException?> = _failure.asStateFlow()

    private val _isWorking = MutableStateFlow(false)
    public val isWorking: StateFlow<Boolean> = _isWorking.asStateFlow()

    public fun updateDraft(transform: (IntentDraft) -> IntentDraft) {
        _draft.value = transform(_draft.value)
    }

    /** Reads where the server thinks this person is. Safe to call on every appearance. */
    public suspend fun load() {
        perform {
            val state = client.onboardingState()
            _phase.value = when (state.step) {
                "ready" -> client.currentPod()
                    ?.let { OnboardingPhase.Matched(it) }
                    // Ready but no pod readable: back to the questions rather than an
                    // empty pod screen.
                    ?: OnboardingPhase.Questions(OnboardingQuestion.GOAL)

                "waiting_for_pod" -> state.poolTicket
                    ?.let { OnboardingPhase.Waiting(it) }
                    ?: OnboardingPhase.Questions(OnboardingQuestion.GOAL)

                else -> OnboardingPhase.Questions(OnboardingQuestion.GOAL)
            }
        }
    }

    /**
     * Called when the waiting screen returns to the foreground. Matching is a scheduled
     * job, so there is nothing to poll — this checks once, deliberately.
     */
    public suspend fun checkForPod() {
        if (_phase.value !is OnboardingPhase.Waiting) return
        perform {
            client.currentPod()?.let { _phase.value = OnboardingPhase.Matched(it) }
        }
    }

    /** Moves to the next question, or submits when the last one is answered. */
    public suspend fun advance() {
        val current = (_phase.value as? OnboardingPhase.Questions)?.question ?: return
        if (!_draft.value.isAnswered(current)) return
        _failure.value = null
        val next = current.next()
        if (next == null) {
            submit()
        } else {
            _phase.value = OnboardingPhase.Questions(next)
        }
    }

    public fun back() {
        val current = (_phase.value as? OnboardingPhase.Questions)?.question ?: return
        current.previous()?.let {
            _failure.value = null
            _phase.value = OnboardingPhase.Questions(it)
        }
    }

    /**
     * Saves the intent and joins the queue together — from the person's point of view they
     * answered four questions and asked for a pod, not two separate things.
     */
    public suspend fun submit() {
        val request = _draft.value.toRequest() ?: return
        perform {
            client.saveIntent(request)
            _phase.value = OnboardingPhase.Waiting(client.joinPool())
        }
    }

    public suspend fun leaveQueue() {
        perform {
            client.leavePool()
            _phase.value = OnboardingPhase.Questions(OnboardingQuestion.GOAL)
        }
    }

    public fun dismissFailure() {
        _failure.value = null
    }

    private suspend fun perform(work: suspend () -> Unit) {
        _isWorking.value = true
        _failure.value = null
        try {
            work()
        } catch (failure: ApiException) {
            _failure.value = failure
        } finally {
            _isWorking.value = false
        }
    }
}
