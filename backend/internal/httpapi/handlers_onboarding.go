package httpapi

import (
	"errors"
	"net/http"

	"github.com/omarharoun/pod/backend/internal/api"
	"github.com/omarharoun/pod/backend/internal/onboarding"
	"github.com/omarharoun/pod/backend/internal/store"
)

func (s *Server) handleOnboardingState(w http.ResponseWriter, r *http.Request) {
	state, err := s.onboarding.State(r.Context(), UserIDFrom(r.Context()))
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
	s.writeJSON(w, r, http.StatusOK, body)
}

func (s *Server) handlePutIntent(w http.ResponseWriter, r *http.Request) {
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
		SelfReport:   string(body.SelfReportReliability),
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

	intent, err := s.onboarding.SaveIntent(r.Context(), UserIDFrom(r.Context()), input)
	if err != nil {
		s.writeError(w, r, onboardingFailure(err), err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, intentResponse(intent))
}

func (s *Server) handleJoinPool(w http.ResponseWriter, r *http.Request) {
	ticket, err := s.onboarding.JoinPool(r.Context(), UserIDFrom(r.Context()))
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
	if err := s.onboarding.LeavePool(r.Context(), UserIDFrom(r.Context())); err != nil {
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
	default:
		return serverError()
	}
}
