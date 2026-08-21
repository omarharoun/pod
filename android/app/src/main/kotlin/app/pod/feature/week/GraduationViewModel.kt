package app.pod.feature.week

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.pod.api.ApiException
import app.pod.api.GraduationRepository
import app.pod.api.PodApiClient
import app.pod.api.generated.Pod
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import javax.inject.Inject
import kotlinx.coroutines.ExperimentalCoroutinesApi

data class GraduationUiState(
    val pod: Pod? = null,
    val working: Boolean = false,
    val failure: ApiException? = null,
)

@HiltViewModel
class GraduationViewModel @Inject constructor(
    private val client: PodApiClient,
) : ViewModel() {

    private val repository = MutableStateFlow<GraduationRepository?>(null)

    @OptIn(ExperimentalCoroutinesApi::class)
    val state: StateFlow<GraduationUiState> =
        repository.flatMapLatest { current ->
            if (current == null) {
                flowOf(GraduationUiState())
            } else {
                combine(current.pod, current.isWorking, current.failure) { pod, working, failure ->
                    GraduationUiState(pod, working, failure)
                }
            }
        }.stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000),
            initialValue = GraduationUiState(),
        )

    /**
     * Seeded once per pod. Deliberately not re-seeded on every recomposition: the screen
     * is re-rendered with the pod the onboarding phase is holding, which is older than the
     * one the answer just came back with — re-seeding would flip the screen back to an
     * unanswered choice the moment somebody answered it.
     */
    fun start(pod: Pod) {
        if (repository.value?.pod?.value?.id == pod.id) return
        repository.value = GraduationRepository(client, pod)
    }

    fun answer(continuing: Boolean) {
        val current = repository.value ?: return
        viewModelScope.launch { current.answer(continuing) }
    }

    fun refresh() {
        val current = repository.value ?: return
        viewModelScope.launch { current.refresh() }
    }
}
