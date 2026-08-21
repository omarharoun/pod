// Package store is the persistence boundary. Postgres is the source of truth
// (CLAUDE.md §5); the in-memory implementation exists so the rest of the service can be
// tested without one, not as a second production backend.
package store

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound is returned when a row does not exist. Callers translate it into the
	// right API response; the store never decides that.
	ErrNotFound = errors.New("store: not found")
	// ErrConflict is returned when a write loses a race it was supposed to win — most
	// importantly, two concurrent rotations of the same refresh token.
	ErrConflict = errors.New("store: conflict")
)

type User struct {
	ID                 string
	Email              string
	DisplayName        string
	CreatedAt          time.Time
	OnboardingComplete bool
}

// AuthChallenge is one outstanding login code. The code itself is never stored — only a
// peppered hash of it (ADR-002).
type AuthChallenge struct {
	ID         string
	Email      string
	CodeHash   string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Attempts   int
	ConsumedAt *time.Time
}

func (c AuthChallenge) Spent(now time.Time, maxAttempts int) bool {
	return c.ConsumedAt != nil || now.After(c.ExpiresAt) || c.Attempts >= maxAttempts
}

// RefreshToken is one link in a rotating chain. FamilyID ties a device's chain together
// so that presenting a spent token can revoke every descendant of it.
type RefreshToken struct {
	TokenHash  string
	FamilyID   string
	UserID     string
	DeviceName string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	RotatedAt  *time.Time
	RevokedAt  *time.Time
}

func (t RefreshToken) Usable(now time.Time) bool {
	return t.RotatedAt == nil && t.RevokedAt == nil && now.Before(t.ExpiresAt)
}

// Store is everything the M1 service needs from persistence. It grows a method per
// milestone; it does not grow a generic query escape hatch.
type Store interface {
	// Ping reports whether the datastore is reachable. Used by /v1/health.
	Ping(ctx context.Context) error

	CreateAuthChallenge(ctx context.Context, challenge AuthChallenge) error
	GetAuthChallenge(ctx context.Context, id string) (AuthChallenge, error)
	// RecordChallengeAttempt increments the attempt counter and returns the new count,
	// atomically — the counter is a rate limit, so a lost update is a security hole.
	RecordChallengeAttempt(ctx context.Context, id string) (int, error)
	ConsumeAuthChallenge(ctx context.Context, id string, at time.Time) error
	// CountRecentChallenges is the per-address send limit.
	CountRecentChallenges(ctx context.Context, email string, since time.Time) (int, error)

	// UpsertUserByEmail returns the existing user or creates one. There is no separate
	// sign-up: the first successful verify is the sign-up (ADR-002).
	UpsertUserByEmail(ctx context.Context, email string, now time.Time) (User, error)
	GetUser(ctx context.Context, id string) (User, error)

	CreateRefreshToken(ctx context.Context, token RefreshToken) error
	GetRefreshToken(ctx context.Context, tokenHash string) (RefreshToken, error)
	// RotateRefreshToken marks old as rotated and stores next in one transaction. It
	// returns ErrConflict if old was already rotated — the reuse-detection path.
	RotateRefreshToken(ctx context.Context, oldHash string, next RefreshToken, at time.Time) error
	// RevokeFamily revokes every token in a family. Called on reuse detection and logout.
	RevokeFamily(ctx context.Context, familyID string, at time.Time) error
}
