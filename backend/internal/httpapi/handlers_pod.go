package httpapi

import (
	"errors"
	"net/http"

	"github.com/omarharoun/pod/backend/internal/api"
	"github.com/omarharoun/pod/backend/internal/store"
	"github.com/omarharoun/pod/backend/internal/week"
)

// handleContinueCycle records one member's answer to "are you carrying on".
//
// It does not restart or close the pod. That is the weekly job's, once the answer cannot
// change or the deadline arrives — so the last person to answer never appears to be the
// one who ended it.
func (s *Server) handleContinueCycle(w http.ResponseWriter, r *http.Request) {
	var body api.ContinueRequest
	if err := decodeJSON(r, &body); err != nil {
		s.writeError(w, r, badRequest(), err)
		return
	}
	userID := UserIDFrom(r.Context())
	pod, err := s.weeks.Continue(r.Context(), r.PathValue("podId"), userID, body.Continuing)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, r, notFound(), err)
		return
	case errors.Is(err, week.ErrNotGraduated), errors.Is(err, week.ErrChoiceClosed):
		s.writeError(w, r, cycleChoiceClosed(), err)
		return
	case err != nil:
		s.writeError(w, r, serverError(), err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, s.podResponseWithCycle(r, pod, userID))
}

func (s *Server) handleCurrentPod(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFrom(r.Context())
	pod, err := s.store.GetActivePod(r.Context(), userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// No pod yet is a state, not a failure: the client shows the waiting screen.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.writeError(w, r, serverError(), err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, s.podResponseWithCycle(r, pod, userID))
}

func (s *Server) handleGetPod(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFrom(r.Context())
	// The store scopes this to membership, so a pod id belonging to someone else answers
	// exactly as a pod id that does not exist. Pod-only visibility, always
	// (CLAUDE.md §2 rule 5).
	pod, err := s.store.GetPodForMember(r.Context(), r.PathValue("podId"), userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, r, notFound(), err)
			return
		}
		s.writeError(w, r, serverError(), err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, s.podResponseWithCycle(r, pod, userID))
}

// podResponseWithCycle adds where the pod is in its cycle, and — once it has graduated —
// how the choice is going.
//
// The continuation is counts only. There is no branch here that could attach a member id
// to a "no", because the value it is given does not contain one (CLAUDE.md §2 rule 1).
func (s *Server) podResponseWithCycle(r *http.Request, pod store.Pod, viewerID string) api.Pod {
	out := podResponse(pod, viewerID)

	if length, completed, endsAt, err := s.weeks.CycleOf(r.Context(), pod); err == nil {
		out.Cycle = &api.Cycle{
			LengthWeeks:    length,
			WeeksCompleted: completed,
			EndsAt:         endsAt,
		}
	}

	if pod.State != store.PodStateCompleted {
		return out
	}
	counts, err := s.weeks.Continuation(r.Context(), pod.ID, viewerID)
	if err != nil {
		return out
	}
	continuation := api.Continuation{
		Continuing:  counts.Continuing,
		Undecided:   counts.Undecided,
		YouAnswered: counts.YouAnswered,
	}
	if pod.DecideBy != nil {
		continuation.DecideBy = *pod.DecideBy
	}
	if counts.YouAnswered {
		answer := counts.YouAreContinuing
		continuation.YouAreContinuing = &answer
	}
	out.Continuation = &continuation
	return out
}

func podResponse(pod store.Pod, viewerID string) api.Pod {
	out := api.Pod{
		ID:           pod.ID,
		State:        api.PodState(pod.State),
		GoalCategory: api.GoalCategory(pod.GoalCategory),
		Timezone:     pod.Timezone,
		// Pod-level, always. There is no per-member equivalent to put here
		// (CLAUDE.md §2 rule 3).
		StreakWeeks: pod.StreakWeeks,
		CreatedAt:   pod.CreatedAt,
		CompletedAt: pod.CompletedAt,
		DissolvedAt: pod.DissolvedAt,
	}
	for _, member := range pod.Members {
		out.Members = append(out.Members, api.PodMember{
			ID:          member.UserID,
			DisplayName: member.DisplayName,
			IsYou:       member.UserID == viewerID,
			JoinedAt:    member.JoinedAt,
		})
	}
	return out
}
