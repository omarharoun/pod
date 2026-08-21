package app.pod.feature.onboarding

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.pod.api.ApiException
import app.pod.api.IntentDraft
import app.pod.api.OnboardingPhase
import app.pod.api.OnboardingRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Everything an onboarding screen needs, in one value. */
data class OnboardingUiState(
    val phase: OnboardingPhase = OnboardingPhase.Loading,
    val draft: IntentDraft = IntentDraft(),
    val working: Boolean = false,
    val failure: ApiException? = null,
)

@HiltViewModel
class OnboardingViewModel @Inject constructor(
    private val onboarding: OnboardingRepository,
) : ViewModel() {

    val state: StateFlow<OnboardingUiState> =
        combine(
            onboarding.phase,
            onboarding.draft,
            onboarding.isWorking,
            onboarding.failure,
        ) { phase, draft, working, failure ->
            OnboardingUiState(phase, draft, working, failure)
        }.stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000),
            initialValue = OnboardingUiState(),
        )

    fun load() {
        viewModelScope.launch { onboarding.load() }
    }

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
