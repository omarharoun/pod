package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/omarharoun/pod/backend/internal/api"
	"github.com/omarharoun/pod/backend/internal/copy"
	"github.com/omarharoun/pod/backend/internal/entitlement"
)

func validIntent() api.IntentRequest {
	name := "Sam"
	return api.IntentRequest{
		GoalText:              "finally sort out the spare room",
		GoalCategory:          api.GoalCategoryHome,
		Intensity:             3,
		Timezone:              "Europe/Lisbon",
		SelfReportReliability: api.SelfReportReliabilitySometimes,
		DisplayName:           &name,
		AvailabilityWindows: []api.AvailabilityWindow{
			{DayOfWeek: 5, StartMinute: 600, EndMinute: 720},
		},
	}
}

func TestOnboardingStartsAtTheIntentStep(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	var state api.OnboardingState
	decode(t, h.do(http.MethodGet, "/v1/onboarding/state", nil, session.AccessToken), http.StatusOK, &state)
	if state.Step != "intent" || state.IntentComplete || state.InPool {
		t.Fatalf("got %+v, want a fresh account at the intent step", state)
	}
	if state.PoolTicket != nil {
		t.Error("a fresh account has no pool ticket")
	}
}

func TestSaveIntentThenJoinPool(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	var intent api.Intent
	decode(t, h.do(http.MethodPut, "/v1/onboarding/intent", validIntent(), session.AccessToken), http.StatusOK, &intent)
	if intent.GoalText != "finally sort out the spare room" || intent.Intensity != 3 {
		t.Fatalf("got %+v", intent)
	}
	if len(intent.AvailabilityWindows) != 1 {
		t.Fatalf("availability windows: %+v", intent.AvailabilityWindows)
	}

	// Onboarding is complete once the intent is in; waiting for a pod is not onboarding.
	var me api.User
	decode(t, h.do(http.MethodGet, "/v1/me", nil, session.AccessToken), http.StatusOK, &me)
	if !me.OnboardingComplete {
		t.Error("onboarding_complete should be true once the intent is saved")
	}
	if me.DisplayName == nil || *me.DisplayName != "Sam" {
		t.Errorf("display name: %v", me.DisplayName)
	}

	var ticket api.PoolTicket
	decode(t, h.do(http.MethodPost, "/v1/pool", nil, session.AccessToken), http.StatusAccepted, &ticket)
	if !ticket.NextWindowAt.After(ticket.QueuedAt) {
		t.Fatalf("next window %v is not after queued %v", ticket.NextWindowAt, ticket.QueuedAt)
	}

	var state api.OnboardingState
	decode(t, h.do(http.MethodGet, "/v1/onboarding/state", nil, session.AccessToken), http.StatusOK, &state)
	if state.Step != "waiting_for_pod" || !state.InPool || state.PoolTicket == nil {
		t.Fatalf("got %+v, want waiting_for_pod with a ticket", state)
	}
}

// Re-joining must not reset the wait, or re-tapping the button would jump the queue.
func TestJoiningTheQueueTwiceKeepsTheOriginalPlace(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	decode(t, h.do(http.MethodPut, "/v1/onboarding/intent", validIntent(), session.AccessToken), http.StatusOK, nil)

	var first api.PoolTicket
	decode(t, h.do(http.MethodPost, "/v1/pool", nil, session.AccessToken), http.StatusAccepted, &first)

	// Well inside the access token's fifteen minutes, so this tests the queue and not
	// token expiry.
	h.clock.Advance(2 * time.Minute)
	var second api.PoolTicket
	decode(t, h.do(http.MethodPost, "/v1/pool", nil, session.AccessToken), http.StatusAccepted, &second)

	if !second.QueuedAt.Equal(first.QueuedAt) {
		t.Fatalf("queued_at moved from %v to %v — re-joining jumped the queue", first.QueuedAt, second.QueuedAt)
	}
}

func TestLeavePoolIsIdempotent(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	decode(t, h.do(http.MethodPut, "/v1/onboarding/intent", validIntent(), session.AccessToken), http.StatusOK, nil)
	decode(t, h.do(http.MethodPost, "/v1/pool", nil, session.AccessToken), http.StatusAccepted, nil)

	for i := 0; i < 2; i++ {
		if rec := h.do(http.MethodDelete, "/v1/pool", nil, session.AccessToken); rec.Code != http.StatusNoContent {
			t.Fatalf("leave #%d: got %d", i+1, rec.Code)
		}
	}
	var state api.OnboardingState
	decode(t, h.do(http.MethodGet, "/v1/onboarding/state", nil, session.AccessToken), http.StatusOK, &state)
	if state.InPool {
		t.Error("still in the pool after leaving")
	}
	if !state.IntentComplete {
		t.Error("leaving the queue must not discard the intent")
	}
}

func TestJoinPoolWithoutAnIntent(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	decodeError(t, h.do(http.MethodPost, "/v1/pool", nil, session.AccessToken), http.StatusBadRequest)
}

func TestIntentValidation(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	cases := map[string]func(*api.IntentRequest){
		"goal too short":     func(r *api.IntentRequest) { r.GoalText = "hi" },
		"goal blank":         func(r *api.IntentRequest) { r.GoalText = "   " },
		"goal too long":      func(r *api.IntentRequest) { r.GoalText = string(make([]byte, 200)) },
		"unknown category":   func(r *api.IntentRequest) { r.GoalCategory = api.GoalCategory("vibes") },
		"intensity zero":     func(r *api.IntentRequest) { r.Intensity = 0 },
		"intensity too high": func(r *api.IntentRequest) { r.Intensity = 6 },
		"unknown timezone":   func(r *api.IntentRequest) { r.Timezone = "Mars/Olympus" },
		"unknown self report": func(r *api.IntentRequest) {
			r.SelfReportReliability = api.SelfReportReliability("always")
		},
		"inverted window": func(r *api.IntentRequest) {
			r.AvailabilityWindows = []api.AvailabilityWindow{{DayOfWeek: 1, StartMinute: 700, EndMinute: 600}}
		},
		"empty window": func(r *api.IntentRequest) {
			r.AvailabilityWindows = []api.AvailabilityWindow{{DayOfWeek: 1, StartMinute: 600, EndMinute: 600}}
		},
		"day out of range": func(r *api.IntentRequest) {
			r.AvailabilityWindows = []api.AvailabilityWindow{{DayOfWeek: 7, StartMinute: 0, EndMinute: 60}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			body := validIntent()
			mutate(&body)
			decodeError(t, h.do(http.MethodPut, "/v1/onboarding/intent", body, session.AccessToken), http.StatusBadRequest)
		})
	}
}

func TestGoalTooShortGetsItsOwnCopy(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	body := validIntent()
	body.GoalText = "no"

	problem := decodeError(t, h.do(http.MethodPut, "/v1/onboarding/intent", body, session.AccessToken), http.StatusBadRequest)
	if problem.CopyKey != string(copy.KeyErrorGoalTooShortBody) {
		t.Fatalf("copy_key = %q — a validation failure the person can fix deserves copy that says how", problem.CopyKey)
	}
}

// Changing your mind replaces the intent; it does not create a second goal.
func TestSavingAnIntentTwiceReplacesIt(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	decode(t, h.do(http.MethodPut, "/v1/onboarding/intent", validIntent(), session.AccessToken), http.StatusOK, nil)

	second := validIntent()
	second.GoalText = "learn to make bread"
	second.GoalCategory = api.GoalCategoryCreative
	second.Intensity = 2

	var stored api.Intent
	decode(t, h.do(http.MethodPut, "/v1/onboarding/intent", second, session.AccessToken), http.StatusOK, &stored)
	if stored.GoalText != "learn to make bread" || stored.Intensity != 2 {
		t.Fatalf("got %+v", stored)
	}
}

func TestOnboardingRoutesRequireAuth(t *testing.T) {
	h := newHarness(t)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/v1/onboarding/state"},
		{http.MethodPut, "/v1/onboarding/intent"},
		{http.MethodPost, "/v1/pool"},
		{http.MethodDelete, "/v1/pool"},
		{http.MethodGet, "/v1/pods/current"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			body := decodeError(t, h.do(route.method, route.path, nil, ""), http.StatusUnauthorized)
			if body.Code != api.ErrorCodeSessionExpired {
				t.Errorf("code = %q", body.Code)
			}
		})
	}
}

// The recovery flow re-offers a lower intensity without making anyone re-answer the rest,
// which only works if a client can read its own intent back.
func TestReadIntentBack(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	saved := validIntent()
	decode(t, h.do(http.MethodPut, "/v1/onboarding/intent", saved, session.AccessToken), http.StatusOK, nil)

	var got api.Intent
	decode(t, h.do(http.MethodGet, "/v1/onboarding/intent", nil, session.AccessToken), http.StatusOK, &got)
	if got.GoalText != saved.GoalText || got.GoalCategory != saved.GoalCategory {
		t.Errorf("goal: got %q/%q", got.GoalText, got.GoalCategory)
	}
	if got.Intensity != saved.Intensity || got.Timezone != saved.Timezone {
		t.Errorf("intensity/timezone: got %d/%q", got.Intensity, got.Timezone)
	}
	if len(got.AvailabilityWindows) != 1 || got.AvailabilityWindows[0].DayOfWeek != 5 {
		t.Errorf("availability windows: got %+v", got.AvailabilityWindows)
	}
}

func TestReadIntentBeforeThereIsOne(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	body := decodeError(t, h.do(http.MethodGet, "/v1/onboarding/intent", nil, session.AccessToken), http.StatusNotFound)
	if body.Code != api.ErrorCodeNotFound {
		t.Errorf("code: got %q, want not_found", body.Code)
	}
}

// Lowering the bar resends the intent with one field changed. The self-report is a
// question we do not want to ask twice, so omitting it keeps what is stored.
func TestOmittingTheSelfReportKeepsTheStoredOne(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	first := validIntent()
	first.SelfReportReliability = api.SelfReportReliabilityRarely
	decode(t, h.do(http.MethodPut, "/v1/onboarding/intent", first, session.AccessToken), http.StatusOK, nil)

	lowered := first
	lowered.Intensity = 1
	lowered.SelfReportReliability = ""
	decode(t, h.do(http.MethodPut, "/v1/onboarding/intent", lowered, session.AccessToken), http.StatusOK, nil)

	// The score is never exposed, so read the effect through what the pool sees.
	candidate, err := h.store.GetIntent(t.Context(), session.User.ID, 0)
	if err != nil {
		t.Fatalf("GetIntent: %v", err)
	}
	if candidate.Intensity != 1 {
		t.Errorf("intensity: got %d, want 1", candidate.Intensity)
	}
	if candidate.SelfReport != string(api.SelfReportReliabilityRarely) {
		t.Errorf("self report: got %q, want the stored %q", candidate.SelfReport, api.SelfReportReliabilityRarely)
	}
}

// After a pod ends, the offer names both rungs. Deriving "what they picked" from
// "what we suggest" would put an invariant in a client comment (ADR-021).
func TestRecoveryCarriesBothRungs(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	decode(t, h.do(http.MethodPut, "/v1/onboarding/intent", validIntent(), session.AccessToken), http.StatusOK, nil)

	podID := h.seedPod(session.User.ID)
	if err := h.store.DissolvePod(t.Context(), podID, "inactive", nil, h.clock.Now()); err != nil {
		t.Fatalf("DissolvePod: %v", err)
	}

	var state api.OnboardingState
	decode(t, h.do(http.MethodGet, "/v1/onboarding/state", nil, session.AccessToken), http.StatusOK, &state)

	recovery := state.Recovery
	if recovery == nil {
		t.Fatal("a pod ended and nothing was offered")
	}
	if !recovery.OfferLowerIntensity {
		t.Error("offer_lower_intensity is false after a pod ended")
	}
	if recovery.CurrentIntensity == nil || *recovery.CurrentIntensity != 3 {
		t.Errorf("current_intensity: got %v, want 3", recovery.CurrentIntensity)
	}
	if recovery.SuggestedIntensity == nil || *recovery.SuggestedIntensity != 2 {
		t.Errorf("suggested_intensity: got %v, want 2", recovery.SuggestedIntensity)
	}
	if recovery.OfferAlternatePath {
		t.Error("the alternate path was offered after one pod")
	}
}

// Someone already on the smallest rung has nothing smaller to be offered. Suggesting
// rung 0 — or rung 1 again — would be a question with one answer.
func TestNothingSmallerIsOfferedOnTheSmallestRung(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	intent := validIntent()
	intent.Intensity = 1
	decode(t, h.do(http.MethodPut, "/v1/onboarding/intent", intent, session.AccessToken), http.StatusOK, nil)

	podID := h.seedPod(session.User.ID)
	if err := h.store.DissolvePod(t.Context(), podID, "inactive", nil, h.clock.Now()); err != nil {
		t.Fatalf("DissolvePod: %v", err)
	}

	var state api.OnboardingState
	decode(t, h.do(http.MethodGet, "/v1/onboarding/state", nil, session.AccessToken), http.StatusOK, &state)

	recovery := state.Recovery
	if recovery == nil {
		t.Fatal("a pod ended and nothing was offered")
	}
	if recovery.SuggestedIntensity != nil {
		t.Errorf("suggested_intensity: got %v, want nothing smaller", *recovery.SuggestedIntensity)
	}
	if recovery.CurrentIntensity == nil || *recovery.CurrentIntensity != 1 {
		t.Errorf("current_intensity: got %v, want 1", recovery.CurrentIntensity)
	}
}

// ---- M7: lanes ---------------------------------------------------------------------

// The most important test in M7. CLAUDE.md §2 rule 6 gives everyone one free pod forever,
// so every free-lane path must work with no entitlement rows in existence.
func TestTheFreeLaneWorksWithNoEntitlementsAtAll(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	// The whole free loop, without ever touching /v1/slots.
	decode(t, h.do(http.MethodPut, "/v1/onboarding/intent", validIntent(), session.AccessToken), http.StatusOK, nil)
	decode(t, h.do(http.MethodPost, "/v1/pool", nil, session.AccessToken), http.StatusAccepted, nil)

	var state api.OnboardingState
	decode(t, h.do(http.MethodGet, "/v1/onboarding/state", nil, session.AccessToken), http.StatusOK, &state)
	if !state.IntentComplete || !state.InPool {
		t.Fatalf("the free lane did not work: %+v", state)
	}
}

// And explicitly asking for lane 0 is the same as not asking.
func TestAskingForTheFreeLaneExplicitlyIsTheSame(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	decode(t, h.do(http.MethodPut, "/v1/onboarding/intent?slot=0", validIntent(), session.AccessToken), http.StatusOK, nil)

	var got api.Intent
	decode(t, h.do(http.MethodGet, "/v1/onboarding/intent", nil, session.AccessToken), http.StatusOK, &got)
	if got.GoalText != validIntent().GoalText {
		t.Errorf("slot=0 and no slot disagree: %q", got.GoalText)
	}
}

// A lane nobody has paid for is refused, and refused as a *not found*: a 403 would confirm
// the lane exists and is merely closed, which is a fact about somebody's purchases.
func TestAnUnopenedLaneIsRefused(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	rec := h.do(http.MethodPut, "/v1/onboarding/intent?slot=1", validIntent(), session.AccessToken)
	if rec.Code == http.StatusOK {
		t.Fatal("an unopened lane accepted an intent")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

func TestASlotOutsideTheRangeIsABadRequest(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	for _, raw := range []string{"9", "-1", "one", "0.5", ""} {
		rec := h.do(http.MethodGet, "/v1/onboarding/state?slot="+raw, nil, session.AccessToken)
		want := http.StatusBadRequest
		if raw == "" {
			// An empty value means the parameter was not really given: the free lane.
			want = http.StatusOK
		}
		if rec.Code != want {
			t.Errorf("slot=%q: got %d, want %d", raw, rec.Code, want)
		}
	}
}

func TestSlotsListsEveryLaneAndAlwaysEntitlesTheFirst(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	var body api.SlotList
	decode(t, h.do(http.MethodGet, "/v1/slots", nil, session.AccessToken), http.StatusOK, &body)
	if body.MaxSlots < 2 {
		t.Fatalf("max_slots: got %d", body.MaxSlots)
	}
	if len(body.Slots) != body.MaxSlots {
		t.Fatalf("listed %d lanes, want %d", len(body.Slots), body.MaxSlots)
	}
	if !body.Slots[0].Entitled {
		t.Error("the free lane is not entitled")
	}
	for _, slot := range body.Slots[1:] {
		if slot.Entitled {
			t.Errorf("lane %d is entitled without a purchase", slot.Slot)
		}
	}
	// Nothing about money reaches the client beyond `entitled`.
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leak := range []string{"price", "currency", "receipt", "transaction", "external_id", "source", "expires"} {
		if strings.Contains(string(encoded), leak) {
			t.Errorf("the slot list carries %q: %s", leak, encoded)
		}
	}
}

// A verified receipt opens the next lane, and the lane then works like any other.
func TestClaimingALaneOpensItAndItThenWorks(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	var slot api.Slot
	decode(t, h.do(http.MethodPost, "/v1/slots", api.ClaimSlotRequest{
		Source: api.PurchaseSourceAppStore, Receipt: "dev:order-1",
	}, session.AccessToken), http.StatusOK, &slot)
	if slot.Slot != 1 || !slot.Entitled {
		t.Fatalf("claimed lane: %+v", slot)
	}

	second := validIntent()
	second.GoalText = "learn to make bread properly"
	decode(t, h.do(http.MethodPut, "/v1/onboarding/intent?slot=1", second, session.AccessToken), http.StatusOK, nil)

	// The two lanes hold different things — that is the whole point (ADR-026).
	var first, other api.Intent
	decode(t, h.do(http.MethodGet, "/v1/onboarding/intent?slot=1", nil, session.AccessToken), http.StatusOK, &other)
	if other.GoalText != second.GoalText {
		t.Errorf("lane 1 holds %q", other.GoalText)
	}
	decode(t, h.do(http.MethodPut, "/v1/onboarding/intent", validIntent(), session.AccessToken), http.StatusOK, nil)
	decode(t, h.do(http.MethodGet, "/v1/onboarding/intent", nil, session.AccessToken), http.StatusOK, &first)
	if first.GoalText == other.GoalText {
		t.Error("writing lane 0 overwrote lane 1")
	}
}

// Every store SDK retries. Replaying a receipt must return the lane it already opened
// rather than spending another one.
func TestReplayingAReceiptDoesNotOpenASecondLane(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	claim := api.ClaimSlotRequest{Source: api.PurchaseSourceAppStore, Receipt: "dev:order-1"}

	var first, again api.Slot
	decode(t, h.do(http.MethodPost, "/v1/slots", claim, session.AccessToken), http.StatusOK, &first)
	decode(t, h.do(http.MethodPost, "/v1/slots", claim, session.AccessToken), http.StatusOK, &again)

	if first.Slot != again.Slot {
		t.Errorf("a replayed receipt opened lane %d then lane %d", first.Slot, again.Slot)
	}
	var body api.SlotList
	decode(t, h.do(http.MethodGet, "/v1/slots", nil, session.AccessToken), http.StatusOK, &body)
	opened := 0
	for _, slot := range body.Slots {
		if slot.Entitled {
			opened++
		}
	}
	if opened != 2 {
		t.Errorf("%d lanes are open after one receipt claimed twice", opened)
	}
}

// One receipt must not open a lane for a second account.
func TestAReceiptCannotOpenALaneForSomebodyElse(t *testing.T) {
	h := newHarness(t)
	sam := h.signIn("sam@example.com")
	alex := h.signIn("alex@example.com")
	claim := api.ClaimSlotRequest{Source: api.PurchaseSourceAppStore, Receipt: "dev:order-1"}

	decode(t, h.do(http.MethodPost, "/v1/slots", claim, sam.AccessToken), http.StatusOK, nil)
	rec := h.do(http.MethodPost, "/v1/slots", claim, alex.AccessToken)
	if rec.Code == http.StatusOK {
		t.Fatal("one receipt opened a lane for two accounts")
	}

	// And Alex still has their free lane, which no purchase can take away.
	var body api.SlotList
	decode(t, h.do(http.MethodGet, "/v1/slots", nil, alex.AccessToken), http.StatusOK, &body)
	if !body.Slots[0].Entitled {
		t.Error("a rejected receipt took away somebody's free lane")
	}
}

func TestAnUnverifiableReceiptIsRefusedWithoutEchoingIt(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	rec := h.do(http.MethodPost, "/v1/slots", api.ClaimSlotRequest{
		Source: api.PurchaseSourceAppStore, Receipt: "forged-receipt-abc123",
	}, session.AccessToken)
	body := decodeError(t, rec, http.StatusPaymentRequired)
	if !copy.Has(copy.Key(body.CopyKey)) {
		t.Errorf("copy_key %q does not resolve", body.CopyKey)
	}
	// The receipt must not come back in the error.
	if strings.Contains(rec.Body.String(), "forged-receipt-abc123") {
		t.Errorf("the error echoed the receipt: %s", rec.Body.String())
	}
}

func TestClaimingPastTheCeilingIsAConflict(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	for i := 1; i < entitlement.MaxSlots; i++ {
		decode(t, h.do(http.MethodPost, "/v1/slots", api.ClaimSlotRequest{
			Source: api.PurchaseSourceAppStore, Receipt: fmt.Sprintf("dev:order-%d", i),
		}, session.AccessToken), http.StatusOK, nil)
	}
	decodeError(t, h.do(http.MethodPost, "/v1/slots", api.ClaimSlotRequest{
		Source: api.PurchaseSourceAppStore, Receipt: "dev:one-too-many",
	}, session.AccessToken), http.StatusConflict)
}

// The free lane must survive the entitlements store being broken.
//
// This is CLAUDE.md §2 rule 6 taken literally: "one free pod, forever" cannot be
// conditional on a query succeeding. `requireLane` short-circuits before touching the
// store for exactly this reason, and this test is what stops someone reordering it.
func TestTheFreeLaneSurvivesABrokenEntitlementsStore(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	h.store.FailEntitlements = errors.New("entitlements table is on fire")

	decode(t, h.do(http.MethodPut, "/v1/onboarding/intent", validIntent(), session.AccessToken), http.StatusOK, nil)
	decode(t, h.do(http.MethodPost, "/v1/pool", nil, session.AccessToken), http.StatusAccepted, nil)

	var state api.OnboardingState
	decode(t, h.do(http.MethodGet, "/v1/onboarding/state", nil, session.AccessToken), http.StatusOK, &state)
	if !state.IntentComplete || !state.InPool {
		t.Fatalf("a broken entitlements store took away the free lane: %+v", state)
	}

	// A paid lane, by contrast, is refused rather than granted when the store is down.
	// Failing open here would hand out lanes nobody paid for.
	rec := h.do(http.MethodPut, "/v1/onboarding/intent?slot=1", validIntent(), session.AccessToken)
	if rec.Code == http.StatusOK {
		t.Error("a broken entitlements store granted an unpaid lane")
	}
}
