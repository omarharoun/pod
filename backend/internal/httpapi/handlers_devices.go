package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/omarharoun/pod/backend/internal/api"
	"github.com/omarharoun/pod/backend/internal/store"
)

func (s *Server) handleRegisterDevice(w http.ResponseWriter, r *http.Request) {
	var body api.DeviceRequest
	if err := decodeJSON(r, &body); err != nil {
		s.writeError(w, r, badRequest(), err)
		return
	}
	token := strings.TrimSpace(body.Token)
	platform := string(body.Platform)
	if token == "" || (platform != "ios" && platform != "android") {
		s.writeError(w, r, badRequest(), nil)
		return
	}

	locale := "en-US"
	if body.Locale != nil && *body.Locale != "" {
		locale = *body.Locale
	}
	now := s.clock.Now()

	if err := s.store.RegisterDevice(r.Context(), store.Device{
		TokenHash:    hashToken(token),
		UserID:       UserIDFrom(r.Context()),
		Platform:     platform,
		Locale:       locale,
		RegisteredAt: now,
		LastSeenAt:   now,
	}); err != nil {
		s.writeError(w, r, serverError(), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnregisterDevice(w http.ResponseWriter, r *http.Request) {
	var body api.DeviceRequest
	if err := decodeJSON(r, &body); err != nil {
		s.writeError(w, r, badRequest(), err)
		return
	}
	// Idempotent: an unknown token also answers 204. Someone turning notifications off
	// should not have to care whether they were ever on.
	if err := s.store.RetireDevice(r.Context(), hashToken(strings.TrimSpace(body.Token)), s.clock.Now()); err != nil {
		s.writeError(w, r, serverError(), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// hashToken is what is stored. A push token is a credential for reaching somebody's
// phone: it is never persisted in the clear and never logged.
//
// A plain SHA-256 is enough — the token is high-entropy and there is nothing to
// brute-force, the same reasoning as refresh tokens in ADR-002.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
