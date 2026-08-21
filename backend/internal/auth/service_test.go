package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/config"
	"github.com/omarharoun/pod/backend/internal/store"
)

// capturingMailer records the code instead of sending it, which is the only way a test
// can complete a sign-in — the service never returns the code.
type capturingMailer struct {
	email   string
	code    string
	minutes int
	err     error
	calls   int
}

func (m *capturingMailer) SendLoginCode(_ context.Context, email, code string, ttlMinutes int) error {
	m.calls++
	m.email, m.code, m.minutes = email, code, ttlMinutes
	return m.err
}

type harness struct {
	svc    *Service
	store  *store.Memory
	mailer *capturingMailer
	clock  *clock.Fixed
	cfg    config.Config
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	cfg := config.Config{
		Env:             "test",
		AuthSecret:      "test-secret-that-is-at-least-32-bytes",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 30 * 24 * time.Hour,
		LoginCodeTTL:    10 * time.Minute,
		MaxCodeAttempts: 5,
		MaxCodesPerHour: 5,
		ResendAfter:     30 * time.Second,
	}
	h := &harness{
		store:  store.NewMemory(),
		mailer: &capturingMailer{},
		clock:  clock.NewFixed(time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)),
		cfg:    cfg,
	}
	h.svc = NewService(h.store, h.mailer, h.clock, cfg)
	return h
}

func (h *harness) signIn(t *testing.T, email string) Session {
	t.Helper()
	challenge, err := h.svc.Start(context.Background(), email)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	session, err := h.svc.Verify(context.Background(), challenge.ID, h.mailer.code, "test device")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return session
}

func TestStartMailsASixDigitCode(t *testing.T) {
	h := newHarness(t)
	challenge, err := h.svc.Start(context.Background(), "Sam@Example.com ")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if challenge.CodeLength != 6 {
		t.Errorf("code length: got %d, want 6", challenge.CodeLength)
	}
	if got := challenge.ExpiresAt; !got.Equal(h.clock.Now().Add(10 * time.Minute)) {
		t.Errorf("expiry: got %v", got)
	}
	if h.mailer.email != "sam@example.com" {
		t.Errorf("address should be normalised before use, got %q", h.mailer.email)
	}
	if len(h.mailer.code) != 6 || strings.Trim(h.mailer.code, "0123456789") != "" {
		t.Errorf("code should be six digits, got %q", h.mailer.code)
	}
	if h.mailer.minutes != 10 {
		t.Errorf("mailed TTL: got %d minutes, want 10", h.mailer.minutes)
	}
}

func TestStartRejectsMalformedAddresses(t *testing.T) {
	h := newHarness(t)
	for _, address := range []string{"", "   ", "not-an-email", "sam@", "@example.com", "Sam <sam@example.com>", "a@b@c.com"} {
		if _, err := h.svc.Start(context.Background(), address); !errors.Is(err, ErrEmailInvalid) {
			t.Errorf("Start(%q): got %v, want ErrEmailInvalid", address, err)
		}
	}
}

// The endpoint must not reveal whether an address has an account (ADR-002).
func TestStartLooksIdenticalForKnownAndUnknownAddresses(t *testing.T) {
	h := newHarness(t)
	h.signIn(t, "known@example.com")

	known, err := h.svc.Start(context.Background(), "known@example.com")
	if err != nil {
		t.Fatalf("Start(known): %v", err)
	}
	unknown, err := h.svc.Start(context.Background(), "stranger@example.com")
	if err != nil {
		t.Fatalf("Start(unknown): %v", err)
	}
	if known.CodeLength != unknown.CodeLength || !known.ExpiresAt.Equal(unknown.ExpiresAt) || known.ResendAfter != unknown.ResendAfter {
		t.Fatalf("responses differ for known vs unknown address: %+v vs %+v", known, unknown)
	}
}

func TestStartRateLimitsPerAddress(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < h.cfg.MaxCodesPerHour; i++ {
		if _, err := h.svc.Start(context.Background(), "sam@example.com"); err != nil {
			t.Fatalf("Start #%d: %v", i+1, err)
		}
	}
	_, err := h.svc.Start(context.Background(), "sam@example.com")
	var limited RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("got %v, want RateLimitedError", err)
	}
	if limited.RetryAfter <= 0 {
		t.Errorf("retry-after should be positive, got %v", limited.RetryAfter)
	}

	// A different address is unaffected — the limit is per address, not global.
	if _, err := h.svc.Start(context.Background(), "other@example.com"); err != nil {
		t.Fatalf("Start(other): %v", err)
	}

	// And the window rolls.
	h.clock.Advance(time.Hour + time.Minute)
	if _, err := h.svc.Start(context.Background(), "sam@example.com"); err != nil {
		t.Fatalf("Start after the window rolled: %v", err)
	}
}

func TestVerifyCreatesTheAccountOnFirstUse(t *testing.T) {
	h := newHarness(t)
	session := h.signIn(t, "sam@example.com")
	if session.User.ID == "" {
		t.Fatal("expected a user to have been created")
	}
	if session.User.Email != "sam@example.com" {
		t.Errorf("email: got %q", session.User.Email)
	}
	if session.User.OnboardingComplete {
		t.Error("a brand new account has not onboarded")
	}
	if session.AccessToken == "" || session.RefreshToken == "" {
		t.Fatal("expected both tokens")
	}
	if session.ExpiresIn != 15*time.Minute {
		t.Errorf("access token lifetime: got %v", session.ExpiresIn)
	}

	// Signing in again returns the same account, not a second one.
	again := h.signIn(t, "SAM@example.com")
	if again.User.ID != session.User.ID {
		t.Errorf("second sign-in created a new account: %q then %q", session.User.ID, again.User.ID)
	}
}

func TestVerifyRejectsWrongCode(t *testing.T) {
	h := newHarness(t)
	challenge, err := h.svc.Start(context.Background(), "sam@example.com")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	wrong := "000000"
	if h.mailer.code == wrong {
		wrong = "111111"
	}
	if _, err := h.svc.Verify(context.Background(), challenge.ID, wrong, ""); !errors.Is(err, ErrCodeIncorrect) {
		t.Fatalf("got %v, want ErrCodeIncorrect", err)
	}
	// The right code still works afterwards.
	if _, err := h.svc.Verify(context.Background(), challenge.ID, h.mailer.code, ""); err != nil {
		t.Fatalf("Verify with the correct code: %v", err)
	}
}

func TestVerifyRejectsUnknownChallengeAsIncorrect(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Verify(context.Background(), "not-a-challenge", "123456", ""); !errors.Is(err, ErrCodeIncorrect) {
		t.Fatalf("got %v, want ErrCodeIncorrect — an unknown challenge must not be distinguishable", err)
	}
}

func TestVerifyExpiresCodes(t *testing.T) {
	h := newHarness(t)
	challenge, err := h.svc.Start(context.Background(), "sam@example.com")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.clock.Advance(10*time.Minute + time.Second)
	if _, err := h.svc.Verify(context.Background(), challenge.ID, h.mailer.code, ""); !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("got %v, want ErrCodeExpired", err)
	}
}

func TestVerifyBurnsTheCode(t *testing.T) {
	h := newHarness(t)
	challenge, err := h.svc.Start(context.Background(), "sam@example.com")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	code := h.mailer.code
	if _, err := h.svc.Verify(context.Background(), challenge.ID, code, ""); err != nil {
		t.Fatalf("first Verify: %v", err)
	}
	if _, err := h.svc.Verify(context.Background(), challenge.ID, code, ""); !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("second Verify: got %v, want ErrCodeExpired — codes are single-use", err)
	}
}

func TestVerifyStopsAfterMaxAttempts(t *testing.T) {
	h := newHarness(t)
	challenge, err := h.svc.Start(context.Background(), "sam@example.com")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	correct := h.mailer.code
	wrong := "000000"
	if correct == wrong {
		wrong = "111111"
	}
	for i := 0; i < h.cfg.MaxCodeAttempts-1; i++ {
		if _, err := h.svc.Verify(context.Background(), challenge.ID, wrong, ""); !errors.Is(err, ErrCodeIncorrect) {
			t.Fatalf("attempt %d: got %v, want ErrCodeIncorrect", i+1, err)
		}
	}
	if _, err := h.svc.Verify(context.Background(), challenge.ID, wrong, ""); !errors.Is(err, ErrTooManyTries) {
		t.Fatalf("final attempt: got %v, want ErrTooManyTries", err)
	}
	// Even the correct code is no good now — the challenge is spent.
	if _, err := h.svc.Verify(context.Background(), challenge.ID, correct, ""); !errors.Is(err, ErrTooManyTries) {
		t.Fatalf("correct code after exhausting attempts: got %v, want ErrTooManyTries", err)
	}
}

// A malformed code must not consume an attempt — otherwise a typo'd paste burns the
// challenge and the user has to start over for nothing.
func TestVerifyDoesNotSpendAttemptsOnMalformedCodes(t *testing.T) {
	h := newHarness(t)
	challenge, err := h.svc.Start(context.Background(), "sam@example.com")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < 20; i++ {
		if _, err := h.svc.Verify(context.Background(), challenge.ID, "abc", ""); !errors.Is(err, ErrCodeIncorrect) {
			t.Fatalf("got %v, want ErrCodeIncorrect", err)
		}
	}
	if _, err := h.svc.Verify(context.Background(), challenge.ID, h.mailer.code, ""); err != nil {
		t.Fatalf("the real code should still work: %v", err)
	}
}

func TestRefreshRotatesAndInvalidatesThePreviousToken(t *testing.T) {
	h := newHarness(t)
	first := h.signIn(t, "sam@example.com")

	h.clock.Advance(time.Minute)
	second, err := h.svc.Refresh(context.Background(), first.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh tokens must rotate")
	}
	if second.User.ID != first.User.ID {
		t.Fatal("refresh returned a different user")
	}
	if _, err := h.svc.Refresh(context.Background(), first.RefreshToken); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("replaying the old token: got %v, want ErrRefreshInvalid", err)
	}
}

// Reuse detection: replaying a spent token kills the whole chain, including the token the
// legitimate client is currently holding.
func TestRefreshReuseRevokesTheFamily(t *testing.T) {
	h := newHarness(t)
	first := h.signIn(t, "sam@example.com")
	second, err := h.svc.Refresh(context.Background(), first.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if _, err := h.svc.Refresh(context.Background(), first.RefreshToken); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("replay: got %v, want ErrRefreshInvalid", err)
	}
	if _, err := h.svc.Refresh(context.Background(), second.RefreshToken); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("the live token should have been revoked with the family, got %v", err)
	}
}

func TestRefreshRejectsExpiredTokens(t *testing.T) {
	h := newHarness(t)
	session := h.signIn(t, "sam@example.com")
	h.clock.Advance(30*24*time.Hour + time.Minute)
	if _, err := h.svc.Refresh(context.Background(), session.RefreshToken); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("got %v, want ErrRefreshInvalid", err)
	}
}

func TestRefreshRejectsGarbage(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Refresh(context.Background(), "not-a-token"); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("got %v, want ErrRefreshInvalid", err)
	}
}

func TestLogoutRevokesTheFamilyAndIsIdempotent(t *testing.T) {
	h := newHarness(t)
	session := h.signIn(t, "sam@example.com")
	if err := h.svc.Logout(context.Background(), session.RefreshToken); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := h.svc.Refresh(context.Background(), session.RefreshToken); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("refresh after logout: got %v, want ErrRefreshInvalid", err)
	}
	if err := h.svc.Logout(context.Background(), session.RefreshToken); err != nil {
		t.Fatalf("second Logout should be a no-op, got %v", err)
	}
	if err := h.svc.Logout(context.Background(), "never-existed"); err != nil {
		t.Fatalf("Logout of an unknown token should be a no-op, got %v", err)
	}
}

// Signing out one device must not sign out the others.
func TestLogoutOnlyAffectsItsOwnDevice(t *testing.T) {
	h := newHarness(t)
	phone := h.signIn(t, "sam@example.com")
	tablet := h.signIn(t, "sam@example.com")

	if err := h.svc.Logout(context.Background(), phone.RefreshToken); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := h.svc.Refresh(context.Background(), tablet.RefreshToken); err != nil {
		t.Fatalf("the other device should still be signed in, got %v", err)
	}
}

// The code is a credential: it must never appear in anything the caller receives.
func TestTheCodeNeverLeavesTheServer(t *testing.T) {
	h := newHarness(t)
	challenge, err := h.svc.Start(context.Background(), "sam@example.com")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	code := h.mailer.code
	if strings.Contains(challenge.ID, code) {
		t.Fatal("the challenge id contains the code")
	}
	stored, err := h.store.GetAuthChallenge(context.Background(), challenge.ID)
	if err != nil {
		t.Fatalf("GetAuthChallenge: %v", err)
	}
	if strings.Contains(stored.CodeHash, code) {
		t.Fatal("the stored hash contains the code in the clear")
	}
	if stored.CodeHash == code {
		t.Fatal("the code was stored unhashed")
	}
}
