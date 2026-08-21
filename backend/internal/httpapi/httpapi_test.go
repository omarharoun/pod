package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/omarharoun/pod/backend/internal/api"
	"github.com/omarharoun/pod/backend/internal/auth"
	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/config"
	"github.com/omarharoun/pod/backend/internal/copy"
	"github.com/omarharoun/pod/backend/internal/store"
)

type testMailer struct{ code string }

func (m *testMailer) SendLoginCode(_ context.Context, _, code string, _ int) error {
	m.code = code
	return nil
}

type harness struct {
	t      testing.TB
	server *Server
	store  *store.Memory
	mailer *testMailer
	clock  *clock.Fixed
}

func newHarness(t testing.TB) *harness {
	t.Helper()
	cfg := config.Config{
		Env:             "test",
		Version:         "test-build",
		AuthSecret:      "test-secret-that-is-at-least-32-bytes",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 30 * 24 * time.Hour,
		LoginCodeTTL:    10 * time.Minute,
		MaxCodeAttempts: 5,
		MaxCodesPerHour: 5,
		ResendAfter:     30 * time.Second,
	}
	h := &harness{
		t:      t,
		store:  store.NewMemory(),
		mailer: &testMailer{},
		clock:  clock.NewFixed(time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)),
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.server = New(auth.NewService(h.store, h.mailer, h.clock, cfg), h.store, h.clock, cfg, log)
	return h
}

func (h *harness) do(method, path string, body any, bearer string) *httptest.ResponseRecorder {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.server.ServeHTTP(rec, req)
	return rec
}

func (h *harness) signIn(email string) api.AuthSession {
	h.t.Helper()
	var start api.AuthStartResponse
	decode(h.t, h.do(http.MethodPost, "/v1/auth/start", api.AuthStartRequest{Email: email}, ""), http.StatusOK, &start)
	var session api.AuthSession
	decode(h.t, h.do(http.MethodPost, "/v1/auth/verify", api.AuthVerifyRequest{ChallengeID: start.ChallengeID, Code: h.mailer.code}, ""), http.StatusOK, &session)
	return session
}

func decode(t testing.TB, rec *httptest.ResponseRecorder, wantStatus int, dst any) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status: got %d, want %d — body: %s", rec.Code, wantStatus, rec.Body.String())
	}
	if dst == nil {
		return
	}
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
}

func decodeError(t testing.TB, rec *httptest.ResponseRecorder, wantStatus int) api.ApiError {
	t.Helper()
	var body api.ApiError
	decode(t, rec, wantStatus, &body)
	if body.RequestID == "" {
		t.Error("every error must carry a request id")
	}
	if !copy.Has(copy.Key(body.CopyKey)) {
		t.Errorf("copy_key %q does not resolve — the client would show nothing", body.CopyKey)
	}
	return body
}

func TestHealthReportsOK(t *testing.T) {
	h := newHarness(t)
	var body api.Health
	decode(t, h.do(http.MethodGet, "/v1/health", nil, ""), http.StatusOK, &body)
	if body.Status != "ok" {
		t.Errorf("status: got %q, want ok", body.Status)
	}
	if body.Version != "test-build" {
		t.Errorf("version: got %q", body.Version)
	}
	if len(body.Checks) != 1 || body.Checks[0].Name != "postgres" {
		t.Fatalf("checks: got %+v", body.Checks)
	}
	if body.Checks[0].Status != "ok" {
		t.Errorf("postgres check: got %q", body.Checks[0].Status)
	}
}

// A health endpoint that stays green while the datastore is gone is worse than none.
func TestHealthReports503WhenTheDatastoreIsUnreachable(t *testing.T) {
	h := newHarness(t)
	h.store.FailPing = errors.New("connection refused")

	var body api.Health
	decode(t, h.do(http.MethodGet, "/v1/health", nil, ""), http.StatusServiceUnavailable, &body)
	if body.Status != "down" {
		t.Errorf("status: got %q, want down", body.Status)
	}
	if body.Checks[0].Detail == nil || !strings.Contains(*body.Checks[0].Detail, "connection refused") {
		t.Errorf("expected the operator-facing detail to survive, got %+v", body.Checks[0])
	}
}

func TestHealthNeedsNoToken(t *testing.T) {
	h := newHarness(t)
	if rec := h.do(http.MethodGet, "/v1/health", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("health should be public, got %d", rec.Code)
	}
}

func TestSignInRoundTrip(t *testing.T) {
	h := newHarness(t)

	var start api.AuthStartResponse
	decode(t, h.do(http.MethodPost, "/v1/auth/start", api.AuthStartRequest{Email: "sam@example.com"}, ""), http.StatusOK, &start)
	if start.ChallengeID == "" || start.CodeLength != 6 {
		t.Fatalf("start response: %+v", start)
	}
	if start.ResendAfterSeconds != 30 {
		t.Errorf("resend_after_seconds: got %d, want 30", start.ResendAfterSeconds)
	}

	var session api.AuthSession
	decode(t, h.do(http.MethodPost, "/v1/auth/verify", api.AuthVerifyRequest{ChallengeID: start.ChallengeID, Code: h.mailer.code}, ""), http.StatusOK, &session)
	if session.TokenType != "Bearer" {
		t.Errorf("token_type: got %q", session.TokenType)
	}
	if session.ExpiresInSeconds != 900 {
		t.Errorf("expires_in_seconds: got %d, want 900", session.ExpiresInSeconds)
	}
	if session.User.Email != "sam@example.com" || session.User.OnboardingComplete {
		t.Errorf("user: %+v", session.User)
	}

	var me api.User
	decode(t, h.do(http.MethodGet, "/v1/me", nil, session.AccessToken), http.StatusOK, &me)
	if me.ID != session.User.ID {
		t.Errorf("/v1/me returned a different user: %q vs %q", me.ID, session.User.ID)
	}
}

func TestStartRejectsAMalformedAddress(t *testing.T) {
	h := newHarness(t)
	body := decodeError(t, h.do(http.MethodPost, "/v1/auth/start", api.AuthStartRequest{Email: "nope"}, ""), http.StatusBadRequest)
	if body.CopyKey != string(copy.KeyAuthStartErrorEmailInvalid) {
		t.Errorf("copy_key: got %q", body.CopyKey)
	}
}

func TestStartRateLimitReportsRetryAfter(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 5; i++ {
		if rec := h.do(http.MethodPost, "/v1/auth/start", api.AuthStartRequest{Email: "sam@example.com"}, ""); rec.Code != http.StatusOK {
			t.Fatalf("start #%d: %d", i+1, rec.Code)
		}
	}
	rec := h.do(http.MethodPost, "/v1/auth/start", api.AuthStartRequest{Email: "sam@example.com"}, "")
	body := decodeError(t, rec, http.StatusTooManyRequests)
	if body.RetryAfterSeconds == nil || *body.RetryAfterSeconds <= 0 {
		t.Fatalf("expected a positive retry_after_seconds, got %+v", body.RetryAfterSeconds)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected a Retry-After header for proxies and the URLSession retry path")
	}
	if body.Params["seconds"] == "" {
		t.Error("the rate-limit copy interpolates {seconds} — it must be supplied")
	}
}

func TestVerifyWrongCode(t *testing.T) {
	h := newHarness(t)
	var start api.AuthStartResponse
	decode(t, h.do(http.MethodPost, "/v1/auth/start", api.AuthStartRequest{Email: "sam@example.com"}, ""), http.StatusOK, &start)

	wrong := "000000"
	if h.mailer.code == wrong {
		wrong = "111111"
	}
	body := decodeError(t, h.do(http.MethodPost, "/v1/auth/verify", api.AuthVerifyRequest{ChallengeID: start.ChallengeID, Code: wrong}, ""), http.StatusUnauthorized)
	if body.Code != api.ErrorCodeCodeIncorrect {
		t.Errorf("code: got %q", body.Code)
	}
	if body.CopyKey != string(copy.KeyAuthVerifyErrorCodeIncorrect) {
		t.Errorf("copy_key: got %q", body.CopyKey)
	}
}

func TestRefreshRotates(t *testing.T) {
	h := newHarness(t)
	first := h.signIn("sam@example.com")

	var second api.AuthSession
	decode(t, h.do(http.MethodPost, "/v1/auth/refresh", api.AuthRefreshRequest{RefreshToken: first.RefreshToken}, ""), http.StatusOK, &second)
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh tokens must rotate")
	}
	decodeError(t, h.do(http.MethodPost, "/v1/auth/refresh", api.AuthRefreshRequest{RefreshToken: first.RefreshToken}, ""), http.StatusUnauthorized)
}

func TestLogout(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	rec := h.do(http.MethodPost, "/v1/auth/logout", api.AuthRefreshRequest{RefreshToken: session.RefreshToken}, session.AccessToken)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: got %d, body %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("204 should have no body, got %q", rec.Body.String())
	}
	decodeError(t, h.do(http.MethodPost, "/v1/auth/refresh", api.AuthRefreshRequest{RefreshToken: session.RefreshToken}, ""), http.StatusUnauthorized)
}

func TestProtectedRoutesRejectBadTokens(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	for name, bearer := range map[string]string{
		"missing":   "",
		"garbage":   "not-a-token",
		"truncated": session.AccessToken[:len(session.AccessToken)-3],
	} {
		t.Run(name, func(t *testing.T) {
			body := decodeError(t, h.do(http.MethodGet, "/v1/me", nil, bearer), http.StatusUnauthorized)
			if body.Code != api.ErrorCodeSessionExpired {
				t.Errorf("code: got %q", body.Code)
			}
		})
	}

	t.Run("expired", func(t *testing.T) {
		h.clock.Advance(16 * time.Minute)
		decodeError(t, h.do(http.MethodGet, "/v1/me", nil, session.AccessToken), http.StatusUnauthorized)
	})
}

func TestAuthorizationSchemeMustBeBearer(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Authorization", "Basic "+session.AccessToken)
	rec := httptest.NewRecorder()
	h.server.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
}

func TestUnknownRouteIsAContractError(t *testing.T) {
	h := newHarness(t)
	body := decodeError(t, h.do(http.MethodGet, "/v1/nothing-here", nil, ""), http.StatusNotFound)
	if body.Code != api.ErrorCodeNotFound {
		t.Errorf("code: got %q", body.Code)
	}
}

func TestMalformedBodiesAreRejected(t *testing.T) {
	h := newHarness(t)
	for name, raw := range map[string]string{
		"not json":      "{",
		"wrong type":    `{"email": 42}`,
		"unknown field": `{"email": "sam@example.com", "sneaky": true}`,
		"trailing junk": `{"email": "sam@example.com"} {"email": "again"}`,
		"empty":         ``,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/auth/start", strings.NewReader(raw))
			rec := httptest.NewRecorder()
			h.server.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("got %d, want 400 — body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRequestIDIsEchoed(t *testing.T) {
	h := newHarness(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/nothing-here", nil)
	req.Header.Set("X-Request-Id", "caller-supplied-id")
	rec := httptest.NewRecorder()
	h.server.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-Id"); got != "caller-supplied-id" {
		t.Errorf("header: got %q", got)
	}
	var body api.ApiError
	decode(t, rec, http.StatusNotFound, &body)
	if body.RequestID != "caller-supplied-id" {
		t.Errorf("body request_id: got %q", body.RequestID)
	}

	// One is generated when the caller doesn't supply one.
	fresh := h.do(http.MethodGet, "/v1/nothing-here", nil, "")
	if fresh.Header().Get("X-Request-Id") == "" {
		t.Error("expected a generated request id")
	}
}

// CLAUDE.md §2 rule 1 and ADR-003: no response may carry a display sentence. The client
// resolves everything from a key.
func TestErrorsCarryKeysNotSentences(t *testing.T) {
	h := newHarness(t)
	responses := []*httptest.ResponseRecorder{
		h.do(http.MethodGet, "/v1/nothing-here", nil, ""),
		h.do(http.MethodGet, "/v1/me", nil, ""),
		h.do(http.MethodPost, "/v1/auth/start", api.AuthStartRequest{Email: "nope"}, ""),
	}
	for _, rec := range responses {
		var raw map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
		for _, forbidden := range []string{"message", "detail", "title", "description", "error_description"} {
			if _, present := raw[forbidden]; present {
				t.Errorf("error body carries %q — the API returns copy keys, not sentences: %s", forbidden, rec.Body.String())
			}
		}
		key, _ := raw["copy_key"].(string)
		if !copy.Has(copy.Key(key)) {
			t.Errorf("copy_key %q does not resolve: %s", key, rec.Body.String())
		}
	}
}

// Nothing in an authenticated response may expose the reliability score or an individual
// streak (CLAUDE.md §2 rules 3 and 6). The contract already forbids the fields; this
// checks the wire.
func TestResponsesNeverExposeIndividualScores(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	rec := h.do(http.MethodGet, "/v1/me", nil, session.AccessToken)
	body := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"reliability", "streak", "completion_rate", "band", "score"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("/v1/me leaked %q: %s", forbidden, rec.Body.String())
		}
	}
}

func TestUnbuiltRoutesAnswer501(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	body := decodeError(t, h.do(http.MethodGet, "/v1/pods/current", nil, session.AccessToken), http.StatusNotImplemented)
	if body.Code != api.ErrorCodeNotImplementedYet {
		t.Errorf("code: got %q, want not_implemented_yet — a 404 here reads as a client typo (ADR-007)", body.Code)
	}
}
