package httpapi

import (
	"errors"
	"net/http"

	"github.com/omarharoun/pod/backend/internal/api"
	"github.com/omarharoun/pod/backend/internal/store"
)

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
	s.writeJSON(w, r, http.StatusOK, podResponse(pod, userID))
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
	s.writeJSON(w, r, http.StatusOK, podResponse(pod, userID))
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
