package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/omarharoun/pod/backend/internal/api"
	"github.com/omarharoun/pod/backend/internal/auth"
	"github.com/omarharoun/pod/backend/internal/store"
)

func (s *Server) handleAuthStart(w http.ResponseWriter, r *http.Request) {
	var body api.AuthStartRequest
	if err := decodeJSON(r, &body); err != nil {
		s.writeError(w, r, badRequest(), err)
		return
	}

	challenge, err := s.auth.Start(r.Context(), body.Email)
	if err != nil {
		s.writeError(w, r, authFailure(err), err)
		return
	}

	s.writeJSON(w, r, http.StatusOK, api.AuthStartResponse{
		ChallengeID:        challenge.ID,
		ExpiresAt:          challenge.ExpiresAt,
		CodeLength:         challenge.CodeLength,
		ResendAfterSeconds: int(challenge.ResendAfter.Seconds()),
	})
}

func (s *Server) handleAuthVerify(w http.ResponseWriter, r *http.Request) {
	var body api.AuthVerifyRequest
	if err := decodeJSON(r, &body); err != nil {
		s.writeError(w, r, badRequest(), err)
		return
	}
	deviceName := ""
	if body.DeviceName != nil {
		deviceName = *body.DeviceName
	}

	session, err := s.auth.Verify(r.Context(), body.ChallengeID, body.Code, deviceName)
	if err != nil {
		s.writeError(w, r, authFailure(err), err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, sessionResponse(session))
}

func (s *Server) handleAuthRefresh(w http.ResponseWriter, r *http.Request) {
	var body api.AuthRefreshRequest
	if err := decodeJSON(r, &body); err != nil {
		s.writeError(w, r, badRequest(), err)
		return
	}

	session, err := s.auth.Refresh(r.Context(), body.RefreshToken)
	if err != nil {
		s.writeError(w, r, authFailure(err), err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, sessionResponse(session))
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	var body api.AuthRefreshRequest
	if err := decodeJSON(r, &body); err != nil {
		s.writeError(w, r, badRequest(), err)
		return
	}
	if err := s.auth.Logout(r.Context(), body.RefreshToken); err != nil {
		s.writeError(w, r, serverError(), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, err := s.store.GetUser(r.Context(), UserIDFrom(r.Context()))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// A valid token for a user who no longer exists: the session is over.
			s.writeError(w, r, unauthorized(), err)
			return
		}
		s.writeError(w, r, serverError(), err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, userResponse(user))
}

func sessionResponse(session auth.Session) api.AuthSession {
	return api.AuthSession{
		AccessToken:      session.AccessToken,
		RefreshToken:     session.RefreshToken,
		ExpiresInSeconds: int(session.ExpiresIn / time.Second),
		TokenType:        "Bearer",
		User:             userResponse(session.User),
	}
}

func userResponse(user store.User) api.User {
	out := api.User{
		ID:                 user.ID,
		Email:              user.Email,
		CreatedAt:          user.CreatedAt,
		OnboardingComplete: user.OnboardingComplete,
	}
	if user.DisplayName != "" {
		name := user.DisplayName
		out.DisplayName = &name
	}
	return out
}

// authFailure maps the service's errors onto the contract. Everything it does not
// recognise is a 500: an unmapped error is a bug here, not a message for the user.
func authFailure(err error) failure {
	var limited auth.RateLimitedError
	switch {
	case errors.Is(err, auth.ErrEmailInvalid):
		return invalidEmail()
	case errors.As(err, &limited):
		return rateLimited(int(limited.RetryAfter.Seconds()))
	case errors.Is(err, auth.ErrCodeIncorrect):
		return codeIncorrect()
	case errors.Is(err, auth.ErrCodeExpired):
		return codeExpired()
	case errors.Is(err, auth.ErrTooManyTries):
		return tooManyAttempts()
	case errors.Is(err, auth.ErrRefreshInvalid):
		return refreshRejected()
	default:
		return serverError()
	}
}
