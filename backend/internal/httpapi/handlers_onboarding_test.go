package httpapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/omarharoun/pod/backend/internal/api"
	"github.com/omarharoun/pod/backend/internal/copy"
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
	candidate, err := h.store.GetIntent(t.Context(), session.User.ID)
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
