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
	// DissolvedPods counts pods that ended under this person while they were inactive.
	// Never returned by the API — it is an input to what they are offered, not a mark.
	DissolvedPods int
	// There is deliberately no recovery flag here. The offer belongs to a lane, not to
	// a person: a pod ending in one lane must not put "that pod has wound down" in front
	// of a lane whose pod is running (migration 0008).
}

// AlternatePathAfter is how many dissolved pods it takes before someone is offered the
// one-to-one path. CLAUDE.md §6: "After 3 failed pods, flag the user for the alternate
// path — some people need less social pressure, not more."
const AlternatePathAfter = 3

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
	UserID string
	// Slot is which lane this goal belongs to. 0 is the free one.
	Slot                int
	GoalText            string
	GoalCategory        string
	Intensity           int
	Timezone            string
	SelfReport          string
	AvailabilityWindows []AvailabilityWindow
	Embedding           []float32
	UpdatedAt           time.Time
}

// IntentKey identifies one intent: a person and a lane.
type IntentKey struct {
	UserID string
	Slot   int
}

// PoolTicket is a place in the queue for the next matching window.
type PoolTicket struct {
	UserID   string
	Slot     int
	QueuedAt time.Time
}

// PoolCandidate is one waiting person, flattened into what the matching job needs and
// nothing else. No email, no display name, no goal text — matching does not need
// identifying information, so it is not handed any.
type PoolCandidate struct {
	UserID string
	// Slot is which of this person's lanes is waiting. Matching never compares it —
	// your slot-1 goal and someone else's slot-0 goal are the same kind of thing — but
	// creating the pod needs it, because a membership belongs to a lane (ADR-026).
	Slot         int
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
	PodStateForming PodState = "forming"
	PodStateActive  PodState = "active"
	// PodStateCompleted is a pod that finished its cycle. It is not PodStateDissolved
	// and must never be treated as one: graduation carries no reliability consequence
	// and no dissolved-pod count (ADR-023).
	PodStateCompleted PodState = "completed"
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
	// CycleLengthWeeks is fixed when the pod forms, so changing the default never moves
	// the finish line under a pod that is already running.
	CycleLengthWeeks int
	CreatedAt        time.Time
	// CycleStartedAt is when the CURRENT cycle began. Equal to CreatedAt for a pod's
	// first cycle, and reset by StartNextCycle — everything about a cycle is measured
	// from here, and measuring from CreatedAt made every cycle after the first end
	// before it began.
	CycleStartedAt time.Time
	// CompletedAt is when the cycle finished. Graduation, not an ending.
	CompletedAt *time.Time
	// DecideBy is when an unanswered pod closes. Set at completion.
	DecideBy    *time.Time
	DissolvedAt *time.Time
	Members     []PodMember
}

// Continuation is how many members are carrying on after graduation.
//
// Counts, not rows. There is no store method that returns who said no, because there is
// no screen that may show it and no response that may carry it (CLAUDE.md §2 rule 1).
// The caller's own answer is separate, because it is the one per-member fact the caller
// is entitled to.
type Continuation struct {
	Continuing int
	Declined   int
	Undecided  int
	// YouAnswered and YouAreContinuing describe the caller only.
	YouAnswered      bool
	YouAreContinuing bool
}

// Membership is one person's place in a pod, in one of their lanes.
//
// The slot belongs here rather than on Pod because your slot-1 pod is somebody else's
// slot-0 pod (ADR-026).
type Membership struct {
	UserID string
	Slot   int
}

// FreeLane is every one of these people in their free lane. Most callers — and every
// test that predates M7 — mean exactly this.
func FreeLane(userIDs ...string) []Membership {
	out := make([]Membership, 0, len(userIDs))
	for _, userID := range userIDs {
		out = append(out, Membership{UserID: userID})
	}
	return out
}

type PodMember struct {
	UserID string
	// Slot is which of *this member's* lanes the pod occupies. It is per member, not per
	// pod: your slot-1 pod is somebody else's slot-0 pod (ADR-026). It is never sent to
	// another member — a client only ever learns its own.
	Slot        int
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

// Device is one phone registered for push. The token is stored hashed: it is a
// credential for reaching somebody's phone.
type Device struct {
	TokenHash    string
	UserID       string
	Platform     string
	Locale       string
	RegisteredAt time.Time
	LastSeenAt   time.Time
}

// Notification is one queued push. It stores a copy key and parameters rather than a
// rendered sentence, so a wording fix reaches everything not yet sent (ADR-003).
type Notification struct {
	ID        string
	UserID    string
	Kind      string
	SubjectID string
	CopyKey   string
	Params    map[string]string
	CreatedAt time.Time
}

// WeekOutcome is one week of somebody's history, as the reliability calculation needs
// it. Nothing identifying, and no score — the score is derived from these, never stored.
type WeekOutcome struct {
	WeekID       string
	Posted       bool
	PodCompleted bool
	ClosesAt     time.Time
}

// Recovery is what a person is offered after a pod ends. Both fields describe an offer,
// never a judgement, and neither exposes anything about why (CLAUDE.md §6).
type Recovery struct {
	// OfferLowerIntensity is set after a pod dissolves under them.
	OfferLowerIntensity bool
	// OfferAlternatePath is set after three. Some people need less social pressure, not
	// more.
	OfferAlternatePath bool
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

	// CompleteCycle moves a pod to completed and opens the choice. It is not
	// DissolvePod: no member carries a count and no recovery offer is made.
	CompleteCycle(ctx context.Context, podID string, at, decideBy time.Time) error
	// AnswerContinuation records one member's answer, replacing any previous one.
	AnswerContinuation(ctx context.Context, podID, userID string, continuing bool, at time.Time) error
	// ContinuationFor returns the counts, plus the caller's own answer.
	ContinuationFor(ctx context.Context, podID, userID string) (Continuation, error)
	// ListPodsAwaitingContinuation returns completed pods whose choice is still open.
	ListPodsAwaitingContinuation(ctx context.Context) ([]Pod, error)
	// CloseCompletedPod ends a pod that graduated and is not carrying on. Members go
	// back to the pool; nobody carries a count and nobody is offered "that pod wound
	// down", because this one did not (ADR-023).
	CloseCompletedPod(ctx context.Context, podID string, at time.Time) error
	// StartNextCycle keeps the pod running with whoever said yes.
	StartNextCycle(ctx context.Context, podID string, at time.Time) error

	// UpsertIntent replaces the caller's intent for one lane.
	UpsertIntent(ctx context.Context, intent Intent) error
	GetIntent(ctx context.Context, userID string, slot int) (Intent, error)

	// ListIntents walks every intent, ordered by (user_id, slot), for the one caller that
	// needs to: `cmd/reembed`, when the embedder has changed under stored vectors.
	//
	// Keyset-paged rather than offset-paged because somebody saving a goal mid-backfill
	// must not shuffle a page and hide a row. Pass a zero After to start.
	ListIntents(ctx context.Context, after IntentKey, limit int) ([]Intent, error)
	// ReplaceEmbedding writes one intent's vector and touches nothing else.
	//
	// Deliberately not UpsertIntent with the embedding swapped: a backfill that can
	// rewrite a goal, a timezone or `updated_at` is a backfill that can quietly tell
	// somebody they edited their intent when they did not (ADR-021).
	ReplaceEmbedding(ctx context.Context, key IntentKey, embedding []float32) error

	// ListEntitlements returns which additional lanes the caller has opened.
	//
	// Lane 0 is deliberately absent from the result and must never be read from it: it
	// is free forever, and making its availability depend on this query succeeding is
	// exactly the mistake CLAUDE.md §2 rule 6 forbids (ADR-026).
	ListEntitlements(ctx context.Context, userID string) (map[int]bool, error)
	// GrantSlot records a verified purchase. Idempotent on (source, externalID), so
	// replaying a receipt returns the lane it already opened.
	GrantSlot(ctx context.Context, userID string, slot int, source, externalID string, at time.Time) error
	// SlotForExternalID returns the lane a transaction already opened, or ErrNotFound.
	SlotForExternalID(ctx context.Context, source, externalID string) (string, int, error)

	// JoinPool queues someone for the next matching window. Returns ErrConflict if they
	// are already queued or already in an active pod.
	JoinPool(ctx context.Context, userID string, slot int, at time.Time) error
	LeavePool(ctx context.Context, userID string, slot int) error
	GetPoolTicket(ctx context.Context, userID string, slot int) (PoolTicket, error)
	// ListPoolCandidates returns everyone waiting, oldest first, capped at limit. The
	// job reads the whole pool for a window rather than paging: pods are formed from the
	// pool as a whole, and a page boundary would silently change who can match whom.
	ListPoolCandidates(ctx context.Context, limit int) ([]PoolCandidate, error)

	// CreatePod writes a pod, its members and their removal from the queue in one
	// transaction. A half-formed pod would leave someone in a pod they cannot see, or in
	// the queue for a second one.
	CreatePod(ctx context.Context, pod Pod, members []Membership, at time.Time) error
	// GetPodForMember returns a pod only if userID belongs to it. Pod-only visibility is
	// enforced in the query, not in the handler (CLAUDE.md §2 rule 5).
	GetPodForMember(ctx context.Context, podID, userID string) (Pod, error)
	// GetActivePod returns the caller's current pod in one lane, or ErrNotFound.
	GetActivePod(ctx context.Context, userID string, slot int) (Pod, error)
	// ListActivePodsForUser is every lane the caller currently has a pod in.
	ListActivePodsForUser(ctx context.Context, userID string) ([]Pod, error)
	// CountWeeksEveryoneWasIn is how many of a pod's closed weeks since `since` the pod
	// completed.
	//
	// Distinct from Pod.StreakWeeks, which is the trailing run and is reset by any miss.
	// The graduation screen needs this one; the ring needs the streak.
	//
	// `since` is the current cycle's first opening instant. Counting a pod's whole life
	// would let a second cycle's graduation report more weeks than a cycle has.
	CountWeeksEveryoneWasIn(ctx context.Context, podID string, since time.Time) (int, error)
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

	// RegisterDevice records a phone for push. Idempotent on the token hash.
	RegisterDevice(ctx context.Context, device Device) error
	// RetireDevice stops sending to a token, whether the person asked or the provider
	// told us it was dead.
	RetireDevice(ctx context.Context, tokenHash string, at time.Time) error
	// DevicesFor returns a user's live devices.
	DevicesFor(ctx context.Context, userID string) ([]Device, error)

	// QueueNotification writes to the outbox. Returns ErrConflict if this exact
	// notification has already been queued — which is what stops an hourly job sending
	// the same "new week" push every hour for a week.
	QueueNotification(ctx context.Context, notification Notification) error
	// PendingNotifications is the sender's input.
	PendingNotifications(ctx context.Context, limit int) ([]Notification, error)
	// MarkNotificationSent records delivery, or a permanent failure.
	MarkNotificationSent(ctx context.Context, id string, at time.Time, failed bool) error

	// RecentOutcomes returns a person's last `limit` closed weeks, newest first. The
	// only input the reliability calculation has.
	RecentOutcomes(ctx context.Context, userID string, limit int) ([]WeekOutcome, error)
	// DissolvePod ends a pod, records why, and counts it against the members who were
	// inactive in it. Idempotent.
	DissolvePod(ctx context.Context, podID, reason string, inactiveMemberIDs []string, at time.Time) error
	// RecoveryFor reports what a person should be offered in one lane after a pod ended.
	//
	// The lower-the-bar offer is per lane; the alternate path is not. Three pods ending
	// under someone is a fact about them whichever lanes it happened in, and that is what
	// CLAUDE.md §6 keys the one-to-one path on.
	RecoveryFor(ctx context.Context, userID string, slot int) (Recovery, error)
	// ClearRecoveryOffer is called when someone acts on the offer, or joins a new pod.
	ClearRecoveryOffer(ctx context.Context, userID string, slot int) error
}
