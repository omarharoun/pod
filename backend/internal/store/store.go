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

// AvailabilityWindow is a recurring window in the person's own timezone.
type AvailabilityWindow struct {
	DayOfWeek   int `json:"day_of_week"`
	StartMinute int `json:"start_minute"`
	EndMinute   int `json:"end_minute"`
}

// Intent is the one thing someone is working on. One per person: changing it replaces
// the row, because a change of mind is not a second goal.
type Intent struct {
	UserID              string
	GoalText            string
	GoalCategory        string
	Intensity           int
	Timezone            string
	SelfReport          string
	AvailabilityWindows []AvailabilityWindow
	Embedding           []float32
	UpdatedAt           time.Time
}

// PoolTicket is a place in the queue for the next matching window.
type PoolTicket struct {
	UserID   string
	QueuedAt time.Time
}

// PoolCandidate is one waiting person, flattened into what the matching job needs and
// nothing else. No email, no display name, no goal text — matching does not need
// identifying information, so it is not handed any.
type PoolCandidate struct {
	UserID       string
	GoalCategory string
	Intensity    int
	Timezone     string
	SelfReport   string
	Availability []AvailabilityWindow
	Embedding    []float32
	// TenureWeeks is whole weeks since the account was created. It feeds the 0.10
	// new-plus-experienced signal; once pod history exists it should count weeks in a
	// pod instead, which is an M5 change.
	TenureWeeks int
	QueuedAt    time.Time
}

type PodState string

const (
	PodStateForming   PodState = "forming"
	PodStateActive    PodState = "active"
	PodStateDissolved PodState = "dissolved"
)

// Pod is two or three people with the same goal category and an overlapping week.
type Pod struct {
	ID           string
	State        PodState
	GoalCategory string
	// Timezone is the pod's shared zone; cycles anchor here, never to a device.
	Timezone string
	// StreakWeeks is pod-level. There is no per-member equivalent anywhere in this
	// codebase, and adding one is a product bug (CLAUDE.md §2 rule 3).
	StreakWeeks int
	CreatedAt   time.Time
	DissolvedAt *time.Time
	Members     []PodMember
}

type PodMember struct {
	UserID      string
	DisplayName string
	JoinedAt    time.Time
}

// Week is one cycle of the loop: one small action, and who is in.
type Week struct {
	ID              string
	PodID           string
	Index           int
	ActionText      string
	ActionIsDefault bool
	OpensAt         time.Time
	ClosesAt        time.Time
	// PodCompleted is whether the POD completed. There is no per-member equivalent, and
	// adding one is a product bug (CLAUDE.md §2 rules 1 and 3).
	PodCompleted bool
	ClosedAt     *time.Time
	Members      []MemberWeek
}

// MemberWeekState is posted / open / resting. There is no `missed` and no `failed`, and
// there will not be — see ADR-006.
type MemberWeekState string

const (
	MemberWeekPosted  MemberWeekState = "posted"
	MemberWeekOpen    MemberWeekState = "open"
	MemberWeekResting MemberWeekState = "resting"
)

// MemberWeek is one person's standing in one week.
type MemberWeek struct {
	UserID   string
	State    MemberWeekState
	PostedAt *time.Time
	ProofID  *string
}

// Proof is ten seconds of evidence and the moment it stops existing.
type Proof struct {
	ID          string
	WeekID      string
	UserID      string
	Kind        string
	ContentType string
	ByteSize    int64
	DurationMs  *int
	BlobKey     *string
	CreatedAt   time.Time
	CommittedAt *time.Time
	// ExpiresAt is set once, at creation, and never extended. It is a promise
	// (CLAUDE.md §2 rule 4).
	ExpiresAt time.Time
	DeletedAt *time.Time
}

// Store is everything the service needs from persistence. It grows a method per
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

	// SetDisplayName records the name a person's own pod sees. It is visible nowhere
	// else — there are no public profiles (CLAUDE.md §2 rule 5).
	SetDisplayName(ctx context.Context, userID, displayName string) error

	// UpsertIntent replaces the caller's intent.
	UpsertIntent(ctx context.Context, intent Intent) error
	GetIntent(ctx context.Context, userID string) (Intent, error)

	// JoinPool queues someone for the next matching window. Returns ErrConflict if they
	// are already queued or already in an active pod.
	JoinPool(ctx context.Context, userID string, at time.Time) error
	LeavePool(ctx context.Context, userID string) error
	GetPoolTicket(ctx context.Context, userID string) (PoolTicket, error)
	// ListPoolCandidates returns everyone waiting, oldest first, capped at limit. The
	// job reads the whole pool for a window rather than paging: pods are formed from the
	// pool as a whole, and a page boundary would silently change who can match whom.
	ListPoolCandidates(ctx context.Context, limit int) ([]PoolCandidate, error)

	// CreatePod writes a pod, its members and their removal from the queue in one
	// transaction. A half-formed pod would leave someone in a pod they cannot see, or in
	// the queue for a second one.
	CreatePod(ctx context.Context, pod Pod, memberIDs []string, at time.Time) error
	// GetPodForMember returns a pod only if userID belongs to it. Pod-only visibility is
	// enforced in the query, not in the handler (CLAUDE.md §2 rule 5).
	GetPodForMember(ctx context.Context, podID, userID string) (Pod, error)
	// GetActivePod returns the caller's current pod, or ErrNotFound.
	GetActivePod(ctx context.Context, userID string) (Pod, error)
	// ListActivePods is the weekly job's input.
	ListActivePods(ctx context.Context) ([]Pod, error)

	// EnsureWeek creates a week and its member rows if that window has none, and returns
	// the week either way. Idempotent, because the job that calls it may run twice.
	EnsureWeek(ctx context.Context, week Week, memberIDs []string) (Week, error)
	// GetWeekForMember returns a week only if userID belongs to its pod.
	GetWeekForMember(ctx context.Context, weekID, userID string) (Week, error)
	// GetCurrentWeek returns the pod's week containing `at`, or ErrNotFound.
	GetCurrentWeek(ctx context.Context, podID, userID string, at time.Time) (Week, error)
	// ListWeeksForMember returns a pod's weeks, newest first.
	ListWeeksForMember(ctx context.Context, podID, userID string, limit int) ([]Week, error)
	// ListWeeksForMemberless is the same, for the scheduled job, which serves no request
	// and therefore has nobody to scope by. Never reachable from a handler.
	ListWeeksForMemberless(ctx context.Context, podID string, limit int) ([]Week, error)
	// SetWeekAction names the week's action. Returns ErrConflict once someone has posted.
	SetWeekAction(ctx context.Context, weekID, actionText string) error
	// CloseWeek settles a week: every still-open member becomes resting, pod_completed is
	// recorded, and the pod's shared streak moves. Idempotent.
	CloseWeek(ctx context.Context, weekID string, at time.Time) (Week, error)

	// CreateProof reserves an upload slot. Nothing is visible to the pod until it is
	// committed.
	CreateProof(ctx context.Context, proof Proof) error
	// GetProofForViewer returns a proof only if the viewer shares its pod.
	GetProofForViewer(ctx context.Context, proofID, viewerID string) (Proof, error)
	// CommitProof publishes an uploaded proof and marks its member posted, in one
	// transaction. Returns ErrConflict if that person already posted this week.
	CommitProof(ctx context.Context, proofID, userID, blobKey string, at time.Time) error
	// ListExpiredProofs is the sweep's input: committed, undeleted, past expiry.
	ListExpiredProofs(ctx context.Context, at time.Time, limit int) ([]Proof, error)
	// MarkProofDeleted records that the bytes are gone.
	MarkProofDeleted(ctx context.Context, proofID string, at time.Time) error
}
