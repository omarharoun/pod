package httpapi

import (
	"errors"
	"net/http"

	"github.com/omarharoun/pod/backend/internal/api"
	"github.com/omarharoun/pod/backend/internal/copy"
	"github.com/omarharoun/pod/backend/internal/store"
	"github.com/omarharoun/pod/backend/internal/week"
)

func (s *Server) handleCurrentWeek(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFrom(r.Context())
	current, err := s.weeks.Current(r.Context(), r.PathValue("podId"), userID)
	if err != nil {
		s.writeError(w, r, weekFailure(err), err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, s.weekResponse(current))
}

func (s *Server) handleListWeeks(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFrom(r.Context())
	weeks, err := s.weeks.List(r.Context(), r.PathValue("podId"), userID, 0)
	if err != nil {
		s.writeError(w, r, weekFailure(err), err)
		return
	}
	body := api.WeekList{Weeks: []api.Week{}}
	for _, record := range weeks {
		body.Weeks = append(body.Weeks, s.weekResponse(record))
	}
	s.writeJSON(w, r, http.StatusOK, body)
}

func (s *Server) handleSetWeekAction(w http.ResponseWriter, r *http.Request) {
	var body api.WeekActionRequest
	if err := decodeJSON(r, &body); err != nil {
		s.writeError(w, r, badRequest(), err)
		return
	}
	updated, err := s.weeks.SetAction(r.Context(), r.PathValue("weekId"), UserIDFrom(r.Context()), body.ActionText)
	if err != nil {
		s.writeError(w, r, weekFailure(err), err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, s.weekResponse(updated))
}

func (s *Server) handleCreateProofUpload(w http.ResponseWriter, r *http.Request) {
	var body api.ProofUploadRequest
	if err := decodeJSON(r, &body); err != nil {
		s.writeError(w, r, badRequest(), err)
		return
	}

	upload, err := s.weeks.StartProof(
		r.Context(),
		r.PathValue("weekId"),
		UserIDFrom(r.Context()),
		string(body.Kind),
		body.ContentType,
		int64(body.ByteSize),
		body.DurationMs,
	)
	if err != nil {
		s.writeError(w, r, weekFailure(err), err)
		return
	}
	s.writeJSON(w, r, http.StatusCreated, api.ProofUpload{
		ProofID:         upload.ProofID,
		UploadURL:       upload.URL,
		UploadMethod:    upload.Method,
		UploadHeaders:   upload.Headers,
		UploadExpiresAt: upload.ExpiresAt,
	})
}

func (s *Server) handleCommitProof(w http.ResponseWriter, r *http.Request) {
	proof, err := s.weeks.CommitProof(
		r.Context(),
		r.PathValue("weekId"),
		r.PathValue("proofId"),
		UserIDFrom(r.Context()),
	)
	if err != nil {
		s.writeError(w, r, weekFailure(err), err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, proofResponse(proof))
}

func (s *Server) handleGetProof(w http.ResponseWriter, r *http.Request) {
	proof, err := s.weeks.ViewProof(r.Context(), r.PathValue("proofId"), UserIDFrom(r.Context()))
	if err != nil {
		s.writeError(w, r, weekFailure(err), err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, proofResponse(proof))
}

// weekResponse fills a default action from the copy catalogue rather than storing an
// English sentence in the database (ADR-003, ADR-014).
func (s *Server) weekResponse(record store.Week) api.Week {
	actionText := record.ActionText
	if record.ActionIsDefault || actionText == "" {
		resolved, err := copy.Resolve(copy.KeyWeekDefaultAction, nil)
		if err == nil {
			actionText = resolved
		}
	}

	out := api.Week{
		ID:              record.ID,
		PodID:           record.PodID,
		Index:           record.Index,
		ActionText:      actionText,
		ActionIsDefault: record.ActionIsDefault,
		// Editable until the week closes or somebody acts on what it said.
		ActionEditable: record.ClosedAt == nil && s.clock.Now().Before(record.ClosesAt) && !anyonePosted(record),
		OpensAt:        record.OpensAt,
		ClosesAt:       record.ClosesAt,
		// True as soon as everyone is in, not only once the week has been closed out —
		// the pod should see that it completed on the day it completed. Still pod-level,
		// and still never a per-member verdict (CLAUDE.md §2 rule 3).
		PodCompleted: record.PodCompleted || everyonePosted(record),
	}
	for _, member := range record.Members {
		out.Members = append(out.Members, api.MemberWeek{
			MemberID: member.UserID,
			State:    api.MemberWeekState(member.State),
			ProofID:  member.ProofID,
			PostedAt: member.PostedAt,
		})
	}
	return out
}

// everyonePosted reports whether the pod as a whole is in. An empty week is not complete.
func everyonePosted(record store.Week) bool {
	if len(record.Members) == 0 {
		return false
	}
	for _, member := range record.Members {
		if member.State != store.MemberWeekPosted {
			return false
		}
	}
	return true
}

func anyonePosted(record store.Week) bool {
	for _, member := range record.Members {
		if member.State == store.MemberWeekPosted {
			return true
		}
	}
	return false
}

func proofResponse(proof week.Proof) api.Proof {
	out := api.Proof{
		ID:        proof.Record.ID,
		WeekID:    proof.Record.WeekID,
		MemberID:  proof.Record.UserID,
		Kind:      api.ProofKind(proof.Record.Kind),
		CreatedAt: proof.Record.CreatedAt,
		ExpiresAt: proof.Record.ExpiresAt,
	}
	if proof.Record.DurationMs != nil {
		duration := *proof.Record.DurationMs
		out.DurationMs = &duration
	}
	if proof.ViewURL != "" {
		url := proof.ViewURL
		expires := proof.ViewURLExpiresAt
		out.ViewURL = &url
		out.ViewURLExpiresAt = &expires
	}
	return out
}

// weekFailure maps the service's errors onto the contract. Anything unrecognised is a
// 500: an unmapped error is a bug here, not a message for the user.
func weekFailure(err error) failure {
	switch {
	case errors.Is(err, week.ErrNotFound):
		return notFound()
	case errors.Is(err, week.ErrWeekClosed):
		return weekClosed()
	case errors.Is(err, week.ErrAlreadyPosted):
		return alreadyPosted()
	case errors.Is(err, week.ErrAlreadyActed):
		return actionLocked()
	case errors.Is(err, week.ErrTooLong):
		return proofTooLong()
	case errors.Is(err, week.ErrTooLarge):
		return proofTooLarge()
	case errors.Is(err, week.ErrNothingUploaded):
		return uploadIncomplete()
	case errors.Is(err, week.ErrActionTooShort), errors.Is(err, week.ErrActionTooLong),
		errors.Is(err, week.ErrUnsupportedKind):
		return badRequest()
	default:
		return serverError()
	}
}
