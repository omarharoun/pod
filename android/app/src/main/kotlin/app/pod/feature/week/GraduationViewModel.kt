package app.pod.feature.week

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.pod.api.ApiException
import app.pod.api.GraduationRepository
import app.pod.api.PodApiClient
import app.pod.api.SlotsRepository
import app.pod.api.generated.Pod
import dagger.hilt.android.lifecycle.HiltViewModel
import javax.inject.Inject
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

data class GraduationUiState(
    val pod: Pod? = null,
    val working: Boolean = false,
    val failure: ApiException? = null,
    /** Whether there is room for another lane. Not the same as whether to offer one. */
    val canOpenAnotherLane: Boolean = false,
    /**
     * Whether a purchase is in flight. Deliberately separate from [working]: merging them
     * let the paywall's own network call disable the continue-or-close buttons, so an
     * optional purchase could block a core-loop decision (CLAUDE.md §2 rule 6).
     */
    val buying: Boolean = false,
    val slotsFailure: ApiException? = null,
)

@HiltViewModel
class GraduationViewModel @Inject constructor(
    private val client: PodApiClient,
    private val slots: SlotsRepository,
) : ViewModel() {

    private val repository = MutableStateFlow<GraduationRepository?>(null)

    @OptIn(ExperimentalCoroutinesApi::class)
    val state: StateFlow<GraduationUiState> =
        repository.flatMapLatest { current ->
            if (current == null) {
                flowOf(GraduationUiState())
            } else {
                // Two combines rather than one of five, so that every flow the state
                // depends on is actually *observed*. Reading `.value` inside a combine
                // lambda compiles and then quietly fails to recompose when it changes,
                // which is the kind of bug that only shows up on a device.
                val graduation = combine(current.pod, current.isWorking, current.failure, ::Triple)
                val lanes = combine(slots.slots, slots.isWorking, slots.failure, ::Triple)
                combine(graduation, lanes) { (pod, working, failure), (list, buying, slotsFailure) ->
                    GraduationUiState(
                        pod = pod,
                        working = working,
                        failure = failure,
                        canOpenAnotherLane = list.isNotEmpty() && slots.canOpenAnother(),
                        buying = buying,
                        slotsFailure = slotsFailure,
                    )
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
        viewModelScope.launch { slots.load() }
    }

    /** Opens the platform purchase sheet, and the lane if it completes. */
    fun openAnotherLane() {
        viewModelScope.launch { slots.openAnotherLane() }
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
