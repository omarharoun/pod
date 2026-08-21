// Package auth implements passwordless sign-in: a 6-digit emailed code exchanged for a
// short-lived access token and a rotating refresh token. See ADR-002.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/config"
	"github.com/omarharoun/pod/backend/internal/ids"
	podmail "github.com/omarharoun/pod/backend/internal/mail"
	"github.com/omarharoun/pod/backend/internal/store"
)

// The failure modes a caller needs to distinguish. Everything else is a 500.
var (
	ErrEmailInvalid   = errors.New("auth: email is not a valid address")
	ErrCodeIncorrect  = errors.New("auth: code does not match")
	ErrCodeExpired    = errors.New("auth: code has expired or was already used")
	ErrTooManyTries   = errors.New("auth: too many attempts on this challenge")
	ErrRefreshInvalid = errors.New("auth: refresh token is not usable")
)

// RateLimitedError says how long to wait. The wait is surfaced to the user through
// `error.rateLimited.body`, which interpolates it.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e RateLimitedError) Error() string {
	return fmt.Sprintf("auth: rate limited, retry after %s", e.RetryAfter)
}

// Challenge is what `POST /v1/auth/start` produces.
type Challenge struct {
	ID          string
	ExpiresAt   time.Time
	CodeLength  int
	ResendAfter time.Duration
}

// Session is a signed-in device: one access token, one refresh token, one user.
type Session struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    time.Duration
	User         store.User
}

type Service struct {
	store  store.Store
	mailer podmail.Mailer
	tokens *AccessTokens
	clock  clock.Clock
	cfg    config.Config
}

func NewService(s store.Store, m podmail.Mailer, c clock.Clock, cfg config.Config) *Service {
	return &Service{
		store:  s,
		mailer: m,
		tokens: NewAccessTokens(cfg.AuthSecret, cfg.AccessTokenTTL, c),
		clock:  c,
		cfg:    cfg,
	}
}

// AccessTokens exposes the verifier for the authentication middleware.
func (s *Service) AccessTokens() *AccessTokens { return s.tokens }

// Start creates a challenge and mails the code.
//
// It behaves identically whether or not the address has an account: the response carries
// no signal either way, so this endpoint cannot be used to find out who has one.
func (s *Service) Start(ctx context.Context, email string) (Challenge, error) {
	address, err := normaliseAddress(email)
	if err != nil {
		return Challenge{}, err
	}

	now := s.clock.Now()
	recent, err := s.store.CountRecentChallenges(ctx, address, now.Add(-time.Hour))
	if err != nil {
		return Challenge{}, fmt.Errorf("auth: count recent challenges: %w", err)
	}
	if recent >= s.cfg.MaxCodesPerHour {
		return Challenge{}, RateLimitedError{RetryAfter: time.Hour}
	}

	id, err := ids.NewUUID()
	if err != nil {
		return Challenge{}, err
	}
	code, err := newLoginCode()
	if err != nil {
		return Challenge{}, err
	}
	challenge := store.AuthChallenge{
		ID:        id,
		Email:     address,
		CodeHash:  hashLoginCode(s.cfg.AuthSecret, id, code),
		CreatedAt: now,
		ExpiresAt: now.Add(s.cfg.LoginCodeTTL),
	}
	if err := s.store.CreateAuthChallenge(ctx, challenge); err != nil {
		return Challenge{}, fmt.Errorf("auth: create challenge: %w", err)
	}

	minutes := int(s.cfg.LoginCodeTTL / time.Minute)
	if err := s.mailer.SendLoginCode(ctx, address, code, minutes); err != nil {
		// The challenge row is already written and will expire on its own. Failing here
		// tells the user something went wrong rather than leaving them waiting for mail
		// that is never coming.
		return Challenge{}, fmt.Errorf("auth: send login code: %w", err)
	}

	return Challenge{
		ID:          id,
		ExpiresAt:   challenge.ExpiresAt,
		CodeLength:  codeLength,
		ResendAfter: s.cfg.ResendAfter,
	}, nil
}

// Verify exchanges a challenge and code for a session, creating the account if this is
// the first time this address has completed a challenge.
func (s *Service) Verify(ctx context.Context, challengeID, code, deviceName string) (Session, error) {
	if !looksLikeCode(code) {
		return Session{}, ErrCodeIncorrect
	}

	now := s.clock.Now()
	challenge, err := s.store.GetAuthChallenge(ctx, challengeID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// An unknown challenge and a wrong code are the same answer on purpose.
			return Session{}, ErrCodeIncorrect
		}
		return Session{}, fmt.Errorf("auth: load challenge: %w", err)
	}
	if challenge.ConsumedAt != nil || now.After(challenge.ExpiresAt) {
		return Session{}, ErrCodeExpired
	}
	if challenge.Attempts >= s.cfg.MaxCodeAttempts {
		return Session{}, ErrTooManyTries
	}

	// Count the attempt before checking it, so a crash mid-verify costs the attacker a
	// try rather than giving them a free one.
	attempts, err := s.store.RecordChallengeAttempt(ctx, challengeID)
	if err != nil {
		return Session{}, fmt.Errorf("auth: record attempt: %w", err)
	}
	if attempts > s.cfg.MaxCodeAttempts {
		return Session{}, ErrTooManyTries
	}
	if !codeMatches(s.cfg.AuthSecret, challengeID, code, challenge.CodeHash) {
		if attempts >= s.cfg.MaxCodeAttempts {
			return Session{}, ErrTooManyTries
		}
		return Session{}, ErrCodeIncorrect
	}

	if err := s.store.ConsumeAuthChallenge(ctx, challengeID, now); err != nil {
		if errors.Is(err, store.ErrConflict) {
			// Two verifies raced; the other one won. The code is single-use.
			return Session{}, ErrCodeExpired
		}
		return Session{}, fmt.Errorf("auth: consume challenge: %w", err)
	}

	user, err := s.store.UpsertUserByEmail(ctx, challenge.Email, now)
	if err != nil {
		return Session{}, fmt.Errorf("auth: upsert user: %w", err)
	}

	familyID, err := ids.NewUUID()
	if err != nil {
		return Session{}, err
	}
	return s.issue(ctx, user, familyID, deviceName, now)
}

// Refresh rotates a refresh token. Presenting one that has already been rotated revokes
// the whole family: either the token leaked, or the client is replaying, and both mean
// this device's chain can no longer be trusted (ADR-002).
func (s *Service) Refresh(ctx context.Context, presented string) (Session, error) {
	now := s.clock.Now()
	hash := hashRefreshToken(presented)

	existing, err := s.store.GetRefreshToken(ctx, hash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Session{}, ErrRefreshInvalid
		}
		return Session{}, fmt.Errorf("auth: load refresh token: %w", err)
	}
	if existing.RotatedAt != nil {
		if err := s.store.RevokeFamily(ctx, existing.FamilyID, now); err != nil {
			return Session{}, fmt.Errorf("auth: revoke family after reuse: %w", err)
		}
		return Session{}, ErrRefreshInvalid
	}
	if !existing.Usable(now) {
		return Session{}, ErrRefreshInvalid
	}

	user, err := s.store.GetUser(ctx, existing.UserID)
	if err != nil {
		return Session{}, fmt.Errorf("auth: load user: %w", err)
	}

	next, secret, err := s.newRefreshToken(user.ID, existing.FamilyID, existing.DeviceName, now)
	if err != nil {
		return Session{}, err
	}
	if err := s.store.RotateRefreshToken(ctx, hash, next, now); err != nil {
		if errors.Is(err, store.ErrConflict) {
			// Lost the race against a concurrent rotation — same situation as reuse.
			if revokeErr := s.store.RevokeFamily(ctx, existing.FamilyID, now); revokeErr != nil {
				return Session{}, fmt.Errorf("auth: revoke family after conflict: %w", revokeErr)
			}
			return Session{}, ErrRefreshInvalid
		}
		return Session{}, fmt.Errorf("auth: rotate refresh token: %w", err)
	}

	access, expires, err := s.tokens.Issue(user.ID)
	if err != nil {
		return Session{}, err
	}
	return Session{AccessToken: access, RefreshToken: secret, ExpiresIn: expires.Sub(now), User: user}, nil
}

// Logout revokes the presented token's whole family. It is idempotent: an unknown token
// is not an error, because there is nothing useful the client could do about it.
func (s *Service) Logout(ctx context.Context, presented string) error {
	existing, err := s.store.GetRefreshToken(ctx, hashRefreshToken(presented))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("auth: load refresh token: %w", err)
	}
	return s.store.RevokeFamily(ctx, existing.FamilyID, s.clock.Now())
}

func (s *Service) issue(ctx context.Context, user store.User, familyID, deviceName string, now time.Time) (Session, error) {
	token, secret, err := s.newRefreshToken(user.ID, familyID, deviceName, now)
	if err != nil {
		return Session{}, err
	}
	if err := s.store.CreateRefreshToken(ctx, token); err != nil {
		return Session{}, fmt.Errorf("auth: create refresh token: %w", err)
	}
	access, expires, err := s.tokens.Issue(user.ID)
	if err != nil {
		return Session{}, err
	}
	return Session{AccessToken: access, RefreshToken: secret, ExpiresIn: expires.Sub(now), User: user}, nil
}

func (s *Service) newRefreshToken(userID, familyID, deviceName string, now time.Time) (store.RefreshToken, string, error) {
	secret, err := ids.NewSecret(32)
	if err != nil {
		return store.RefreshToken{}, "", err
	}
	return store.RefreshToken{
		TokenHash:  hashRefreshToken(secret),
		FamilyID:   familyID,
		UserID:     userID,
		DeviceName: deviceName,
		CreatedAt:  now,
		ExpiresAt:  now.Add(s.cfg.RefreshTokenTTL),
	}, secret, nil
}

func normaliseAddress(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 254 {
		return "", ErrEmailInvalid
	}
	parsed, err := mail.ParseAddress(trimmed)
	if err != nil || parsed.Address != trimmed {
		// Reject display-name forms ("Sam <sam@example.com>") rather than quietly
		// accepting them: the address we store is the address the person typed.
		return "", ErrEmailInvalid
	}
	if strings.Count(parsed.Address, "@") != 1 {
		return "", ErrEmailInvalid
	}
	return store.NormaliseEmail(parsed.Address), nil
}
