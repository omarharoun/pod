package app.pod.feature.onboarding

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.pod.api.ApiException
import app.pod.api.IntentDraft
import app.pod.api.OnboardingPhase
import app.pod.api.OnboardingRepositories
import app.pod.api.OnboardingRepository
import app.pod.api.SlotsRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/** Everything an onboarding screen needs, in one value. */
/** The lanes a switcher would offer, and which one is showing. */
data class LanesUiState(
    val open: List<app.pod.api.generated.Slot> = emptyList(),
    val current: Int = 0,
)

data class OnboardingUiState(
    val phase: OnboardingPhase = OnboardingPhase.Loading,
    val draft: IntentDraft = IntentDraft(),
    val working: Boolean = false,
    val failure: ApiException? = null,
)

@HiltViewModel
class OnboardingViewModel @Inject constructor(
    private val repositories: OnboardingRepositories,
    private val slots: SlotsRepository,
) : ViewModel() {

    /** The lane being looked at. Its own repository, its own draft. */
    private val onboarding: OnboardingRepository
        get() = repositories.forLane(slots.current.value)

    @OptIn(ExperimentalCoroutinesApi::class)
    val state: StateFlow<OnboardingUiState> =
        slots.current.flatMapLatest { slot ->
            val lane = repositories.forLane(slot)
            combine(
                lane.phase,
                lane.draft,
                lane.isWorking,
                lane.failure,
            ) { phase, draft, working, failure ->
                OnboardingUiState(phase, draft, working, failure)
            }
        }.stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000),
            initialValue = OnboardingUiState(),
        )

    fun load() {
        viewModelScope.launch {
            slots.load()
            onboarding.load()
        }
    }

    /** Switches to another thing, and loads where that lane is. */
    fun showLane(slot: Int) {
        slots.show(slot)
        viewModelScope.launch { repositories.forLane(slot).load() }
    }

    /** The lanes worth offering a switch between, and which is current. */
    val lanes: StateFlow<LanesUiState> =
        combine(slots.slots, slots.current) { list, current ->
            LanesUiState(open = list.filter { it.entitled }, current = current)
        }.stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000),
            initialValue = LanesUiState(),
        )

    fun checkForPod() {
        viewModelScope.launch { onboarding.checkForPod() }
    }

    fun updateDraft(transform: (IntentDraft) -> IntentDraft) = onboarding.updateDraft(transform)

    fun advance() {
        viewModelScope.launch { onboarding.advance() }
    }

    fun back() = onboarding.back()

    /** Takes the offer after a pod ended: the same intent, one rung smaller. */
    fun acceptLowerIntensity(suggested: Int) {
        viewModelScope.launch { onboarding.acceptLowerIntensity(suggested) }
    }

    /** Declines it and goes back into the pool unchanged — an equal answer, not a dismiss. */
    fun keepIntensity() {
        viewModelScope.launch { onboarding.keepIntensity() }
    }

    fun leaveQueue() {
        viewModelScope.launch { onboarding.leaveQueue() }
    }
}
