// Package week is the loop the product is for: one small action, ten seconds of proof,
// and who is in.
package week

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/omarharoun/pod/backend/internal/blob"
	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/copy"
	"github.com/omarharoun/pod/backend/internal/cycle"
	"github.com/omarharoun/pod/backend/internal/ids"
	"github.com/omarharoun/pod/backend/internal/notify"
	"github.com/omarharoun/pod/backend/internal/store"
)

// The failure modes a caller needs to distinguish. Everything else is a 500.
var (
	ErrNotFound        = errors.New("week: no such week")
	ErrActionTooShort  = errors.New("week: action text is too short")
	ErrActionTooLong   = errors.New("week: action text is too long")
	ErrWeekClosed      = errors.New("week: the week has closed")
	ErrAlreadyActed    = errors.New("week: somebody has already posted to this week")
	ErrAlreadyPosted   = errors.New("week: you have already posted this week")
	ErrUnsupportedKind = errors.New("week: unsupported proof kind or content type")
	ErrTooLarge        = errors.New("week: the proof is too large")
	ErrTooLong         = errors.New("week: the proof is longer than ten seconds")
	ErrNothingUploaded = errors.New("week: nothing was uploaded to the slot")
)

const (
	minActionLength = 3
	maxActionLength = 120

	// Ten seconds, with a little slack for a recorder that overruns. CLAUDE.md §1 is
	// explicit about the length: it is what keeps the effort threshold tiny.
	maxProofDuration = 12 * time.Second

	maxPhotoBytes = 10 << 20
	maxVoiceBytes = 4 << 20

	// Long enough for a slow upload on a train, short enough that a leaked slot is not a
	// standing invitation.
	uploadTTL = 10 * time.Minute
	// Short, and always far below the proof's own expiry: a view link is the real leak
	// window (ADR-008).
	viewTTL = 5 * time.Minute
)

// contentTypes are the formats a client may upload, per kind. An open-ended list would
// mean storing whatever anyone sends and handing it back with that content type.
var contentTypes = map[string]map[string]bool{
	"photo": {"image/jpeg": true, "image/png": true, "image/heic": true, "image/heif": true, "image/webp": true},
	"voice": {"audio/m4a": true, "audio/mp4": true, "audio/aac": true, "audio/mpeg": true, "audio/ogg": true},
}

// Upload is a one-time slot plus the id the client commits afterwards.
type Upload struct {
	ProofID   string
	URL       string
	Method    string
	Headers   map[string]string
	ExpiresAt time.Time
}

// Proof is a committed proof and a short-lived way to look at it.
type Proof struct {
	Record           store.Proof
	ViewURL          string
	ViewURLExpiresAt time.Time
}

type Service struct {
	store    store.Store
	blobs    blob.Store
	clock    clock.Clock
	notifier *notify.Service
}

func NewService(s store.Store, b blob.Store, c clock.Clock, n *notify.Service) *Service {
	return &Service{store: s, blobs: b, clock: c, notifier: n}
}

// Current returns the pod's open week, creating it if the scheduled job has not got to it
// yet.
//
// Creating it here as well as in the job is deliberate. A pod formed at 09:15 should not
// stare at an empty screen until the next job run; and because EnsureWeek is keyed on the
// window's opening instant, the two paths cannot produce two weeks.
func (s *Service) Current(ctx context.Context, podID, userID string) (store.Week, error) {
	pod, err := s.store.GetPodForMember(ctx, podID, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Week{}, ErrNotFound
		}
		return store.Week{}, fmt.Errorf("week: load pod: %w", err)
	}

	now := s.clock.Now()
	week, err := s.store.GetCurrentWeek(ctx, podID, userID, now)
	if err == nil {
		return week, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.Week{}, fmt.Errorf("week: load current week: %w", err)
	}
	return s.open(ctx, pod, now)
}

// open creates the week containing `now` for a pod, with its members.
// Open creates a pod's week for the window containing `now`, if it has none.
//
// Exported so the matching job can open a pod's first week in the same breath as forming
// it. A pod with no week is a pod whose members were told "you've got a pod" and then
// opened the app to a 404 — up to an hour of it, until the weekly job's next tick.
//
// Idempotent: EnsureWeek is keyed on the opening instant, so the weekly job running
// immediately afterwards finds this week rather than making a second one.
func (s *Service) Open(ctx context.Context, pod store.Pod, now time.Time) (store.Week, error) {
	return s.open(ctx, pod, now)
}

func (s *Service) open(ctx context.Context, pod store.Pod, now time.Time) (store.Week, error) {
	location, err := cycle.Load(pod.Timezone)
	if err != nil {
		return store.Week{}, err
	}
	bounds := cycle.WeekContaining(now, location)

	id, err := ids.NewUUID()
	if err != nil {
		return store.Week{}, err
	}
	memberIDs := make([]string, 0, len(pod.Members))
	for _, member := range pod.Members {
		memberIDs = append(memberIDs, member.UserID)
	}

	return s.store.EnsureWeek(ctx, store.Week{
		ID:    id,
		PodID: pod.ID,
		// Empty on purpose: a default sentence stored in the database would be a second
		// source of truth for copy. The API fills it from the catalogue at response time
		// while ActionIsDefault is true (ADR-003, ADR-014).
		ActionText:      "",
		ActionIsDefault: true,
		OpensAt:         bounds.OpensAt,
		ClosesAt:        bounds.ClosesAt,
	}, memberIDs)
}

// Get returns one week the caller belongs to.
func (s *Service) Get(ctx context.Context, weekID, userID string) (store.Week, error) {
	week, err := s.store.GetWeekForMember(ctx, weekID, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Week{}, ErrNotFound
		}
		return store.Week{}, fmt.Errorf("week: load week: %w", err)
	}
	return week, nil
}

// List returns a pod's weeks, newest first. The proof is long gone for closed weeks; only
// the states remain.
func (s *Service) List(ctx context.Context, podID, userID string, limit int) ([]store.Week, error) {
	weeks, err := s.store.ListWeeksForMember(ctx, podID, userID, limit)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("week: list weeks: %w", err)
	}
	return weeks, nil
}

// SetAction names the week's one small action.
func (s *Service) SetAction(ctx context.Context, weekID, userID, actionText string) (store.Week, error) {
	text := strings.TrimSpace(actionText)
	switch {
	case utf8.RuneCountInString(text) < minActionLength:
		return store.Week{}, ErrActionTooShort
	case utf8.RuneCountInString(text) > maxActionLength:
		return store.Week{}, ErrActionTooLong
	}

	week, err := s.Get(ctx, weekID, userID)
	if err != nil {
		return store.Week{}, err
	}
	if week.ClosedAt != nil || !s.clock.Now().Before(week.ClosesAt) {
		return store.Week{}, ErrWeekClosed
	}

	if err := s.store.SetWeekAction(ctx, weekID, text); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return store.Week{}, ErrAlreadyActed
		}
		if errors.Is(err, store.ErrNotFound) {
			return store.Week{}, ErrNotFound
		}
		return store.Week{}, fmt.Errorf("week: set action: %w", err)
	}
	return s.Get(ctx, weekID, userID)
}

// StartProof reserves a one-time upload slot.
//
// Nothing here is visible to the pod. An abandoned slot is swept and never becomes a
// proof, so a failed upload costs the person a retry and nothing else.
func (s *Service) StartProof(ctx context.Context, weekID, userID, kind, contentType string, byteSize int64, durationMs *int) (Upload, error) {
	allowed, known := contentTypes[kind]
	if !known || !allowed[strings.ToLower(contentType)] {
		return Upload{}, ErrUnsupportedKind
	}
	limit := int64(maxPhotoBytes)
	if kind == "voice" {
		limit = maxVoiceBytes
	}
	if byteSize <= 0 || byteSize > limit {
		return Upload{}, ErrTooLarge
	}
	if durationMs != nil && time.Duration(*durationMs)*time.Millisecond > maxProofDuration {
		return Upload{}, ErrTooLong
	}

	week, err := s.Get(ctx, weekID, userID)
	if err != nil {
		return Upload{}, err
	}
	now := s.clock.Now()
	if week.ClosedAt != nil || !now.Before(week.ClosesAt) {
		return Upload{}, ErrWeekClosed
	}
	for _, member := range week.Members {
		if member.UserID == userID && member.State == store.MemberWeekPosted {
			return Upload{}, ErrAlreadyPosted
		}
	}

	proofID, err := ids.NewUUID()
	if err != nil {
		return Upload{}, err
	}
	// Never outlive the week, exactly as ViewProof never outlives the proof. A slot
	// signed a minute before close was writable for nine minutes after it, so bytes
	// could land after the sweep had already passed over the row — and once
	// MarkProofDeleted has run, no sweep will ever return that row again.
	ttl := uploadTTL
	if remaining := week.ClosesAt.Sub(s.clock.Now()); remaining < ttl {
		ttl = remaining
	}
	upload, err := s.blobs.SignUpload(ctx, blob.Key(weekID, proofID), contentType, byteSize, ttl)
	if err != nil {
		return Upload{}, fmt.Errorf("week: sign upload: %w", err)
	}

	if err := s.store.CreateProof(ctx, store.Proof{
		ID:          proofID,
		WeekID:      weekID,
		UserID:      userID,
		Kind:        kind,
		ContentType: contentType,
		ByteSize:    byteSize,
		DurationMs:  durationMs,
		CreatedAt:   now,
		// The proof dies when the week does. Set once, never extended — that is what
		// makes CLAUDE.md §2 rule 4 a promise rather than an intention.
		ExpiresAt: week.ClosesAt,
	}); err != nil {
		return Upload{}, fmt.Errorf("week: create proof: %w", err)
	}

	return Upload{
		ProofID:   proofID,
		URL:       upload.URL,
		Method:    upload.Method,
		Headers:   upload.Headers,
		ExpiresAt: upload.ExpiresAt,
	}, nil
}

// CommitProof publishes an uploaded proof to the pod.
func (s *Service) CommitProof(ctx context.Context, weekID, proofID, userID string) (Proof, error) {
	week, err := s.Get(ctx, weekID, userID)
	if err != nil {
		return Proof{}, err
	}
	now := s.clock.Now()
	if week.ClosedAt != nil || !now.Before(week.ClosesAt) {
		return Proof{}, ErrWeekClosed
	}

	key := blob.Key(weekID, proofID)
	uploaded, err := s.blobs.Exists(ctx, key)
	if err != nil {
		return Proof{}, fmt.Errorf("week: check upload: %w", err)
	}
	if !uploaded {
		// Committing a slot with nothing in it would show the pod an empty tile.
		return Proof{}, ErrNothingUploaded
	}

	if err := s.store.CommitProof(ctx, proofID, userID, key, now); err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			return Proof{}, ErrAlreadyPosted
		case errors.Is(err, store.ErrNotFound):
			return Proof{}, ErrNotFound
		}
		return Proof{}, fmt.Errorf("week: commit proof: %w", err)
	}

	// Tell the others. This is the product's core loop — low-stakes social witness,
	// arriving as it happens — and it is the only notification triggered by a person
	// rather than by the clock.
	s.announce(ctx, weekID, userID)

	return s.ViewProof(ctx, proofID, userID)
}

// announce tells the rest of the pod that somebody is in. A failure here must not fail
// the post: the proof is committed, and that is what the person asked for.
func (s *Service) announce(ctx context.Context, weekID, posterID string) {
	if s.notifier == nil {
		return
	}
	week, err := s.store.GetWeekForMember(ctx, weekID, posterID)
	if err != nil {
		return
	}
	pod, err := s.store.GetPodForMember(ctx, week.PodID, posterID)
	if err != nil {
		return
	}
	name := ""
	for _, member := range pod.Members {
		if member.UserID == posterID {
			name = member.DisplayName
		}
	}
	if name == "" {
		// A display name is optional at onboarding, and silence would be the wrong
		// answer: the pod still gets the witness, just without a name on it.
		resolved, err := copy.Resolve(copy.KeyPodSomeone, nil)
		if err != nil {
			return
		}
		name = resolved
	}
	// If the pod is now complete, that is the more interesting fact and it goes to
	// everyone, including the person who just finished it.
	if everyonePosted(week, posterID) {
		s.notifier.PodCompleted(ctx, week, pod.StreakWeeks+1)
		return
	}
	s.notifier.MemberPosted(ctx, pod, week, posterID, name)
}

// everyonePosted treats the caller as posted, since the week was read before their commit
// could be reflected in every path.
func everyonePosted(week store.Week, posterID string) bool {
	if len(week.Members) == 0 {
		return false
	}
	for _, member := range week.Members {
		if member.UserID == posterID {
			continue
		}
		if member.State != store.MemberWeekPosted {
			return false
		}
	}
	return true
}

// ViewProof returns a proof and a short-lived link to it.
func (s *Service) ViewProof(ctx context.Context, proofID, viewerID string) (Proof, error) {
	record, err := s.store.GetProofForViewer(ctx, proofID, viewerID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Proof{}, ErrNotFound
		}
		return Proof{}, fmt.Errorf("week: load proof: %w", err)
	}
	if record.CommittedAt == nil || record.BlobKey == nil {
		// An uncommitted slot is not a proof, and the pod must not be shown one.
		return Proof{}, ErrNotFound
	}
	now := s.clock.Now()
	if !now.Before(record.ExpiresAt) {
		// Past its expiry it is gone, whether or not the sweep has run yet. The promise
		// is not conditional on a job being on time.
		return Proof{}, ErrNotFound
	}

	// Never outlive the proof: if the week closes in two minutes, the link lasts two.
	ttl := viewTTL
	if remaining := record.ExpiresAt.Sub(now); remaining < ttl {
		ttl = remaining
	}
	view, err := s.blobs.SignView(ctx, *record.BlobKey, ttl)
	if err != nil {
		return Proof{}, fmt.Errorf("week: sign view: %w", err)
	}
	return Proof{Record: record, ViewURL: view.URL, ViewURLExpiresAt: view.ExpiresAt}, nil
}
