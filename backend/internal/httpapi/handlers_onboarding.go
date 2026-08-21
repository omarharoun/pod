package httpapi

import (
	"errors"
	"net/http"

	"strconv"

	"github.com/omarharoun/pod/backend/internal/api"
	"github.com/omarharoun/pod/backend/internal/entitlement"
	"github.com/omarharoun/pod/backend/internal/onboarding"
	"github.com/omarharoun/pod/backend/internal/store"
)

func (s *Server) handleOnboardingState(w http.ResponseWriter, r *http.Request) {
	slot, ok := slotOf(r)
	if !ok {
		s.writeError(w, r, badRequest(), nil)
		return
	}
	state, err := s.onboarding.State(r.Context(), UserIDFrom(r.Context()), slot)
	if err != nil {
		s.writeError(w, r, serverError(), err)
		return
	}

	body := api.OnboardingState{
		Step:           string(state.Step),
		IntentComplete: state.IntentComplete,
		InPool:         state.InPool,
	}
	if state.InPool {
		body.PoolTicket = &api.PoolTicket{QueuedAt: state.QueuedAt, NextWindowAt: state.NextWindowAt}
	}
	if state.Recovery.OfferLowerIntensity || state.Recovery.OfferAlternatePath {
		recovery := api.Recovery{
			OfferLowerIntensity: state.Recovery.OfferLowerIntensity,
			OfferAlternatePath:  state.Recovery.OfferAlternatePath,
		}
		if state.CurrentIntensity > 0 {
			current := api.Intensity(state.CurrentIntensity)
			recovery.CurrentIntensity = &current
		}
		if state.SuggestedIntensity > 0 {
			suggested := api.Intensity(state.SuggestedIntensity)
			recovery.SuggestedIntensity = &suggested
		}
		body.Recovery = &recovery
	}
	s.writeJSON(w, r, http.StatusOK, body)
}

func (s *Server) handleGetIntent(w http.ResponseWriter, r *http.Request) {
	slot, ok := slotOf(r)
	if !ok {
		s.writeError(w, r, badRequest(), nil)
		return
	}
	intent, err := s.onboarding.Intent(r.Context(), UserIDFrom(r.Context()), slot)
	if err != nil {
		if errors.Is(err, onboarding.ErrNoIntent) {
			s.writeError(w, r, notFound(), err)
			return
		}
		s.writeError(w, r, serverError(), err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, intentResponse(intent))
}

func (s *Server) handlePutIntent(w http.ResponseWriter, r *http.Request) {
	slot, ok := slotOf(r)
	if !ok {
		s.writeError(w, r, badRequest(), nil)
		return
	}
	var body api.IntentRequest
	if err := decodeJSON(r, &body); err != nil {
		s.writeError(w, r, badRequest(), err)
		return
	}

	input := onboarding.Input{
		GoalText:     body.GoalText,
		GoalCategory: string(body.GoalCategory),
		Intensity:    int(body.Intensity),
		Timezone:     body.Timezone,
	}
	// The generator never pointerises a $ref-to-enum field: the zero value "" is
	// already "absent" for a string enum. Omitted means "keep what is stored".
	if body.SelfReportReliability != "" {
		input.SelfReport = string(body.SelfReportReliability)
	}
	if body.DisplayName != nil {
		input.DisplayName = *body.DisplayName
	}
	for _, window := range body.AvailabilityWindows {
		input.AvailabilityWindows = append(input.AvailabilityWindows, store.AvailabilityWindow{
			DayOfWeek:   window.DayOfWeek,
			StartMinute: window.StartMinute,
			EndMinute:   window.EndMinute,
		})
	}

	intent, err := s.onboarding.SaveIntent(r.Context(), UserIDFrom(r.Context()), slot, input)
	if err != nil {
		s.writeError(w, r, onboardingFailure(err), err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, intentResponse(intent))
}

func (s *Server) handleJoinPool(w http.ResponseWriter, r *http.Request) {
	slot, ok := slotOf(r)
	if !ok {
		s.writeError(w, r, badRequest(), nil)
		return
	}
	ticket, err := s.onboarding.JoinPool(r.Context(), UserIDFrom(r.Context()), slot)
	switch {
	case errors.Is(err, onboarding.ErrAlreadyQueued):
		// Already in the queue is the state the caller wanted, so answer with the
		// existing ticket rather than an error the client would have to special-case.
		s.writeJSON(w, r, http.StatusAccepted, api.PoolTicket{
			QueuedAt: ticket.QueuedAt, NextWindowAt: ticket.NextWindowAt,
		})
		return
	case err != nil:
		s.writeError(w, r, onboardingFailure(err), err)
		return
	}
	s.writeJSON(w, r, http.StatusAccepted, api.PoolTicket{
		QueuedAt: ticket.QueuedAt, NextWindowAt: ticket.NextWindowAt,
	})
}

func (s *Server) handleLeavePool(w http.ResponseWriter, r *http.Request) {
	slot, ok := slotOf(r)
	if !ok {
		s.writeError(w, r, badRequest(), nil)
		return
	}
	if err := s.onboarding.LeavePool(r.Context(), UserIDFrom(r.Context()), slot); err != nil {
		s.writeError(w, r, serverError(), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func intentResponse(intent onboarding.Intent) api.Intent {
	out := api.Intent{
		GoalText:     intent.GoalText,
		GoalCategory: api.GoalCategory(intent.GoalCategory),
		Intensity:    api.Intensity(intent.Intensity),
		Timezone:     intent.Timezone,
		UpdatedAt:    intent.UpdatedAt,
	}
	for _, window := range intent.AvailabilityWindows {
		out.AvailabilityWindows = append(out.AvailabilityWindows, api.AvailabilityWindow{
			DayOfWeek:   window.DayOfWeek,
			StartMinute: window.StartMinute,
			EndMinute:   window.EndMinute,
		})
	}
	return out
}

// onboardingFailure maps the service's errors onto the contract. Anything unrecognised is
// a 500: an unmapped error is a bug here, not a message for the user.
func onboardingFailure(err error) failure {
	switch {
	case errors.Is(err, onboarding.ErrGoalTooShort):
		return goalTooShort()
	case errors.Is(err, onboarding.ErrGoalTooLong),
		errors.Is(err, onboarding.ErrUnknownCategory),
		errors.Is(err, onboarding.ErrIntensityOutOfRange),
		errors.Is(err, onboarding.ErrUnknownTimezone),
		errors.Is(err, onboarding.ErrUnknownSelfReport),
		errors.Is(err, onboarding.ErrBadWindow),
		errors.Is(err, onboarding.ErrNoIntent):
		return badRequest()
	case errors.Is(err, onboarding.ErrAlreadyInPod):
		return alreadyInPod()
	case errors.Is(err, onboarding.ErrNoSuchLane):
		return badRequest()
	case errors.Is(err, onboarding.ErrLaneNotOpen):
		// Not found, not forbidden. A 403 would confirm the lane exists and is merely
		// closed, which is a fact about somebody's purchases that nothing needs to
		// disclose — and the client's response to the two is identical anyway.
		return notFound()
	default:
		return serverError()
	}
}

// slotOf reads the `slot` query parameter, defaulting to the free lane.
//
// Absent means 0, so a client that has never heard of lanes behaves exactly as it did
// before M7 — the core loop is never gated by a parameter nobody sent (CLAUDE.md §2
// rule 6, ADR-026). A malformed value is a 400 rather than a silent 0: quietly writing
// someone's second goal over their first would be worse than refusing.
func slotOf(r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("slot")
	if raw == "" {
		return entitlement.FreeSlot, true
	}
	slot, err := strconv.Atoi(raw)
	if err != nil || !entitlement.InRange(slot) {
		return 0, false
	}
	return slot, true
}

func (s *Server) handleListSlots(w http.ResponseWriter, r *http.Request) {
	lanes, err := s.onboarding.Lanes(r.Context(), UserIDFrom(r.Context()))
	if err != nil {
		s.writeError(w, r, serverError(), err)
		return
	}
	body := api.SlotList{MaxSlots: entitlement.MaxSlots}
	for _, lane := range lanes {
		body.Slots = append(body.Slots, slotResponse(lane))
	}
	s.writeJSON(w, r, http.StatusOK, body)
}

func (s *Server) handleClaimSlot(w http.ResponseWriter, r *http.Request) {
	var body api.ClaimSlotRequest
	if err := decodeJSON(r, &body); err != nil {
		s.writeError(w, r, badRequest(), err)
		return
	}
	lane, err := s.onboarding.Claim(r.Context(), UserIDFrom(r.Context()), string(body.Source), body.Receipt)
	switch {
	case errors.Is(err, entitlement.ErrReceiptRejected):
		s.writeError(w, r, receiptRejected(), err)
		return
	case errors.Is(err, entitlement.ErrAtCeiling):
		s.writeError(w, r, atSlotCeiling(), err)
		return
	case err != nil:
		s.writeError(w, r, serverError(), err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, slotResponse(lane))
}

func slotResponse(lane onboarding.Lane) api.Slot {
	out := api.Slot{
		Slot:      lane.Slot,
		Entitled:  lane.Entitled,
		HasIntent: lane.HasIntent,
		InPool:    lane.InPool,
		HasPod:    lane.HasPod,
	}
	if lane.GoalText != "" {
		goal := lane.GoalText
		out.GoalText = &goal
	}
	return out
}
