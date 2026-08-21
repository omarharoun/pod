package app.pod.feature.week

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import app.pod.api.ApiException
import app.pod.api.WeekPhase
import app.pod.api.WeekRepositories
import app.pod.api.generated.ProofKind
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Everything the week screen needs, in one value. */
data class WeekUiState(
    val phase: WeekPhase = WeekPhase.Loading,
    val posting: Boolean = false,
    val savingAction: Boolean = false,
    val failure: ApiException? = null,
)

@HiltViewModel
class WeekViewModel @Inject constructor(
    private val repositories: WeekRepositories,
) : ViewModel() {

    private val podId = MutableStateFlow<String?>(null)

    @OptIn(ExperimentalCoroutinesApi::class)
    val state: StateFlow<WeekUiState> = podId
        .flatMapLatest { id ->
            val repository = repositories.forPod(id.orEmpty())
            combine(
                repository.phase,
                repository.isPosting,
                repository.isSavingAction,
                repository.failure,
            ) { phase, posting, saving, failure -> WeekUiState(phase, posting, saving, failure) }
        }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000),
            initialValue = WeekUiState(),
        )

    /** Called once by the screen, with the pod it is showing. */
    fun start(pod: String) {
        if (podId.value == pod) return
        podId.value = pod
        viewModelScope.launch { repositories.forPod(pod).load() }
    }

    fun refresh() {
        val pod = podId.value ?: return
        viewModelScope.launch { repositories.forPod(pod).refresh() }
    }

    fun setAction(text: String) {
        val pod = podId.value ?: return
        viewModelScope.launch { repositories.forPod(pod).setAction(text) }
    }

    fun post(kind: ProofKind, contentType: String, bytes: ByteArray, durationMs: Int? = null) {
        val pod = podId.value ?: return
        viewModelScope.launch { repositories.forPod(pod).post(kind, contentType, bytes, durationMs) }
    }

    fun canPost(userId: String): Boolean {
        val pod = podId.value ?: return false
        return repositories.forPod(pod).canPost(userId)
    }

    fun memberState(userId: String) = podId.value?.let { repositories.forPod(it).memberWeek(userId) }
}
