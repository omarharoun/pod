package app.pod.api

import app.pod.api.generated.GoalCategory
import app.pod.api.generated.IntentRequest
import app.pod.api.generated.Pod
import app.pod.api.generated.PoolTicket
import app.pod.api.generated.Recovery
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

    /**
     * A pod has ended and there is an offer to make before anything else. Its own beat
     * rather than a banner: the person who needs it is the likeliest to stop opening the
     * app, and a banner over a pre-lowered answer implies a correction rather than a
     * question (docs/DESIGN.md).
     */
    public data class Recovering(val recovery: Recovery) : OnboardingPhase
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

                else -> state.recovery
                    ?.takeIf { it.offerLowerIntensity || it.offerAlternatePath }
                    ?.let { OnboardingPhase.Recovering(it) }
                    ?: OnboardingPhase.Questions(OnboardingQuestion.GOAL)
            }
        }
    }

    /**
     * Takes the offer: the same intent with a smaller intensity, which is all "lowering
     * the bar" is. There is no dedicated endpoint because it is not a special kind of
     * change (ADR-018).
     */
    public suspend fun acceptLowerIntensity(suggested: Int) {
        perform { resubmit(suggested) }
    }

    /**
     * Declines the offer and goes back into the pool unchanged. "Keep it as it is" is not
     * a secondary action — for plenty of people the pod simply did not work out.
     */
    public suspend fun keepIntensity() {
        perform { resubmit(intensity = null) }
    }

    /**
     * Re-saves the intent on file and re-enters the pool, changing at most the intensity.
     *
     * The answers come from the server, not from [draft]: someone recovering from a pod
     * that ended answered these weeks ago, on a device that may not be this one. The
     * self-report is deliberately not sent — the server keeps the stored answer, and "how
     * often do you keep commitments to yourself" is not a question to ask twice.
     */
    private suspend fun resubmit(intensity: Int?) {
        val existing = client.intent()
        if (existing == null) {
            // Nothing on file, so there is nothing to re-offer. Ask the questions.
            _phase.value = OnboardingPhase.Questions(OnboardingQuestion.GOAL)
            return
        }
        client.saveIntent(
            IntentRequest(
                goalText = existing.goalText,
                goalCategory = existing.goalCategory,
                intensity = intensity ?: existing.intensity,
                timezone = TimeZone.currentSystemDefault().id,
                selfReportReliability = null,
                availabilityWindows = existing.availabilityWindows,
                displayName = null,
            ),
        )
        _phase.value = OnboardingPhase.Waiting(client.joinPool())
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
