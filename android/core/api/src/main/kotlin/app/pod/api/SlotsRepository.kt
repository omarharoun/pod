package app.pod.api

import app.pod.api.generated.PurchaseSource
import app.pod.api.generated.Slot
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * Where a purchase comes from.
 *
 * A seam, exactly as the server's receipt verifier is: Play Billing needs a real app
 * record and store credentials this repository does not and should not contain.
 * Everything on this side of it is real — the claim call, the entitlement, the second lane.
 */
public interface PurchaseBroker {
    /**
     * Presents the platform's purchase sheet and returns a receipt, or null if the person
     * changed their mind.
     *
     * Cancelling is an ordinary outcome and must not surface as a failure: deciding not to
     * buy something is a decision, not an error.
     */
    public suspend fun purchaseAdditionalLane(): String?
}

/**
 * The default. An app with no store configuration behaves like someone who cancelled,
 * which keeps the whole product working without a paywall rather than breaking around it.
 */
public class UnavailablePurchaseBroker : PurchaseBroker {
    override suspend fun purchaseAdditionalLane(): String? = null
}

/** The caller's lanes, and opening another. */
public class SlotsRepository(
    private val client: PodApiClient,
    private val broker: PurchaseBroker = UnavailablePurchaseBroker(),
    private val source: PurchaseSource = PurchaseSource.PLAY_STORE,
) {
    private val _slots = MutableStateFlow<List<Slot>>(emptyList())
    public val slots: StateFlow<List<Slot>> = _slots.asStateFlow()

    private val _maxSlots = MutableStateFlow(0)
    public val maxSlots: StateFlow<Int> = _maxSlots.asStateFlow()

    private val _failure = MutableStateFlow<ApiException?>(null)
    public val failure: StateFlow<ApiException?> = _failure.asStateFlow()

    private val _isWorking = MutableStateFlow(false)
    public val isWorking: StateFlow<Boolean> = _isWorking.asStateFlow()

    private val _current = MutableStateFlow(0)

    /**
     * The lane the caller is looking at. Every lane-aware screen reads this.
     *
     * Held here rather than in each screen because it is one fact about the whole session
     * — which thing you are currently looking at — and two screens disagreeing about it is
     * how someone posts proof against the wrong goal.
     */
    public val current: StateFlow<Int> = _current.asStateFlow()

    /**
     * Switches to a lane the caller actually has. A lane they do not is ignored rather
     * than refused: there is no state in which showing an error about it helps.
     */
    public fun show(slot: Int) {
        if (_slots.value.any { it.slot == slot && it.entitled }) {
            _current.value = slot
        }
    }

    /** The lanes that are actually usable, in order. Lane 0 is always among them. */
    public fun openLanes(): List<Slot> = _slots.value.filter { it.entitled }

    /**
     * Whether there is room for another. Not the same as whether to *offer* one — that is
     * the graduation screen's decision (docs/DESIGN.md).
     */
    public fun canOpenAnother(): Boolean = openLanes().size < _maxSlots.value

    public suspend fun load() {
        perform {
            val list = client.slots()
            _slots.value = list.slots
            _maxSlots.value = list.maxSlots
        }
    }

    /**
     * Presents the purchase sheet and, if it completes, opens the lane.
     *
     * Returns the new lane, or null when the person cancelled — which is a decision, not a
     * failure, and produces no error state.
     */
    public suspend fun openAnotherLane(): Slot? {
        var opened: Slot? = null
        perform {
            val receipt = broker.purchaseAdditionalLane() ?: return@perform
            opened = client.claimSlot(source, receipt)
            val list = client.slots()
            _slots.value = list.slots
            _maxSlots.value = list.maxSlots
            // Someone who has just bought a second thing wants to name it, not to be
            // returned to the one they finished.
            opened?.let { _current.value = it.slot }
        }
        return opened
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
