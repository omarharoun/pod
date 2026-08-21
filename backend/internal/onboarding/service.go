// Package onboarding covers the one thing someone is putting off, and getting them into
// the queue for a pod.
package onboarding

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/embed"
	"github.com/omarharoun/pod/backend/internal/matching"
	"github.com/omarharoun/pod/backend/internal/reliability"
	"github.com/omarharoun/pod/backend/internal/store"
)

// The failure modes a caller needs to distinguish. Everything else is a 500.
var (
	ErrGoalTooShort        = errors.New("onboarding: goal text is too short")
	ErrGoalTooLong         = errors.New("onboarding: goal text is too long")
	ErrUnknownCategory     = errors.New("onboarding: unknown goal category")
	ErrIntensityOutOfRange = errors.New("onboarding: intensity is outside 1-5")
	ErrUnknownTimezone     = errors.New("onboarding: unknown timezone")
	ErrUnknownSelfReport   = errors.New("onboarding: unknown self-report answer")
	ErrBadWindow           = errors.New("onboarding: availability window is not a real window")
	ErrNoIntent            = errors.New("onboarding: no intent recorded yet")
	ErrAlreadyInPod        = errors.New("onboarding: already in an active pod")
	ErrAlreadyQueued       = errors.New("onboarding: already queued")
)

// Goal text bounds, matching the contract.
const (
	minGoalLength = 3
	maxGoalLength = 140
	maxWindows    = 21
)

// Categories are the nine hard-filter buckets from the contract. Kept here as a set so an
// unrecognised category is a 400 rather than a pod nobody can be matched into.
var categories = map[string]bool{
	"movement": true, "creative": true, "admin": true, "learning": true, "career": true,
	"home": true, "health": true, "connection": true, "money": true,
}

// Step is where someone is in onboarding.
type Step string

const (
	StepIntent        Step = "intent"
	StepWaitingForPod Step = "waiting_for_pod"
	StepReady         Step = "ready"
)

// State is the answer to "what should this person see next".
type State struct {
	Step           Step
	IntentComplete bool
	InPool         bool
	QueuedAt       time.Time
	NextWindowAt   time.Time
}

// Intent is a validated, stored intent.
type Intent struct {
	GoalText            string
	GoalCategory        string
	Intensity           int
	Timezone            string
	AvailabilityWindows []store.AvailabilityWindow
	UpdatedAt           time.Time
}

// Ticket is a place in the queue.
type Ticket struct {
	QueuedAt     time.Time
	NextWindowAt time.Time
}

type Service struct {
	store    store.Store
	embedder embed.Embedder
	clock    clock.Clock
}

func NewService(s store.Store, e embed.Embedder, c clock.Clock) *Service {
	return &Service{store: s, embedder: e, clock: c}
}

// Input is an unvalidated intent from the wire.
type Input struct {
	GoalText            string
	GoalCategory        string
	Intensity           int
	Timezone            string
	SelfReport          string
	AvailabilityWindows []store.AvailabilityWindow
	DisplayName         string
}

// SaveIntent validates and stores what someone is working on, and embeds the goal text
// for the 0.40 similarity signal.
//
// Embedding happens here rather than in the matching job so that a matching window never
// waits on a model call, and so a model outage delays nobody's onboarding.
func (s *Service) SaveIntent(ctx context.Context, userID string, in Input) (Intent, error) {
	goal := strings.TrimSpace(in.GoalText)
	switch {
	case utf8.RuneCountInString(goal) < minGoalLength:
		return Intent{}, ErrGoalTooShort
	case utf8.RuneCountInString(goal) > maxGoalLength:
		return Intent{}, ErrGoalTooLong
	case !categories[in.GoalCategory]:
		return Intent{}, ErrUnknownCategory
	case in.Intensity < 1 || in.Intensity > 5:
		return Intent{}, ErrIntensityOutOfRange
	}

	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return Intent{}, ErrUnknownTimezone
	}
	switch reliability.SelfReport(in.SelfReport) {
	case reliability.SelfReportRarely, reliability.SelfReportSometimes, reliability.SelfReportUsually:
	default:
		return Intent{}, ErrUnknownSelfReport
	}
	windows, err := validWindows(in.AvailabilityWindows)
	if err != nil {
		return Intent{}, err
	}

	vectors, err := s.embedder.Embed(ctx, []string{goal})
	if err != nil {
		return Intent{}, fmt.Errorf("onboarding: embed goal: %w", err)
	}
	if len(vectors) != 1 {
		return Intent{}, fmt.Errorf("onboarding: embedder returned %d vectors for one text", len(vectors))
	}

	now := s.clock.Now()
	record := store.Intent{
		UserID:              userID,
		GoalText:            goal,
		GoalCategory:        in.GoalCategory,
		Intensity:           in.Intensity,
		Timezone:            in.Timezone,
		SelfReport:          in.SelfReport,
		AvailabilityWindows: windows,
		Embedding:           vectors[0],
		UpdatedAt:           now,
	}
	if err := s.store.UpsertIntent(ctx, record); err != nil {
		return Intent{}, fmt.Errorf("onboarding: save intent: %w", err)
	}

	if name := strings.TrimSpace(in.DisplayName); name != "" {
		if err := s.store.SetDisplayName(ctx, userID, name); err != nil {
			return Intent{}, fmt.Errorf("onboarding: save display name: %w", err)
		}
	}

	return Intent{
		GoalText:            record.GoalText,
		GoalCategory:        record.GoalCategory,
		Intensity:           record.Intensity,
		Timezone:            record.Timezone,
		AvailabilityWindows: record.AvailabilityWindows,
		UpdatedAt:           record.UpdatedAt,
	}, nil
}

// JoinPool queues someone for the next matching window.
//
// It answers with when the next window is, rather than with a pod: CLAUDE.md §5 makes
// matching a scheduled job, so there is nothing to poll for and the client can say when.
func (s *Service) JoinPool(ctx context.Context, userID string) (Ticket, error) {
	if _, err := s.store.GetIntent(ctx, userID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Ticket{}, ErrNoIntent
		}
		return Ticket{}, fmt.Errorf("onboarding: read intent: %w", err)
	}
	if _, err := s.store.GetActivePod(ctx, userID); err == nil {
		return Ticket{}, ErrAlreadyInPod
	} else if !errors.Is(err, store.ErrNotFound) {
		return Ticket{}, fmt.Errorf("onboarding: check pod: %w", err)
	}

	now := s.clock.Now()
	if err := s.store.JoinPool(ctx, userID, now); err != nil {
		if errors.Is(err, store.ErrConflict) {
			// Already queued. Report the existing ticket rather than resetting the wait —
			// re-joining must not let anyone jump the queue.
			ticket, ticketErr := s.store.GetPoolTicket(ctx, userID)
			if ticketErr != nil {
				return Ticket{}, ErrAlreadyQueued
			}
			return Ticket{QueuedAt: ticket.QueuedAt, NextWindowAt: matching.NextWindowAt(now)}, ErrAlreadyQueued
		}
		return Ticket{}, fmt.Errorf("onboarding: join pool: %w", err)
	}
	return Ticket{QueuedAt: now, NextWindowAt: matching.NextWindowAt(now)}, nil
}

func (s *Service) LeavePool(ctx context.Context, userID string) error {
	if err := s.store.LeavePool(ctx, userID); err != nil {
		return fmt.Errorf("onboarding: leave pool: %w", err)
	}
	return nil
}

// State reports where someone is, which is what decides the screen they see.
func (s *Service) State(ctx context.Context, userID string) (State, error) {
	now := s.clock.Now()
	state := State{Step: StepIntent}

	if _, err := s.store.GetIntent(ctx, userID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return state, nil
		}
		return State{}, fmt.Errorf("onboarding: read intent: %w", err)
	}
	state.IntentComplete = true

	if _, err := s.store.GetActivePod(ctx, userID); err == nil {
		state.Step = StepReady
		return state, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return State{}, fmt.Errorf("onboarding: check pod: %w", err)
	}

	ticket, err := s.store.GetPoolTicket(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Onboarded but not queued: the client offers the join button again.
			return state, nil
		}
		return State{}, fmt.Errorf("onboarding: read ticket: %w", err)
	}
	state.Step = StepWaitingForPod
	state.InPool = true
	state.QueuedAt = ticket.QueuedAt
	state.NextWindowAt = matching.NextWindowAt(now)
	return state, nil
}

// validWindows rejects windows that are not windows. A zero-length or inverted window
// would silently contribute nothing to the overlap signal, which is worse than a 400:
// the person would look unmatchable for a reason nobody could see.
func validWindows(windows []store.AvailabilityWindow) ([]store.AvailabilityWindow, error) {
	if len(windows) > maxWindows {
		return nil, ErrBadWindow
	}
	out := make([]store.AvailabilityWindow, 0, len(windows))
	for _, w := range windows {
		if w.DayOfWeek < 0 || w.DayOfWeek > 6 {
			return nil, ErrBadWindow
		}
		if w.StartMinute < 0 || w.StartMinute > 1439 {
			return nil, ErrBadWindow
		}
		if w.EndMinute <= w.StartMinute || w.EndMinute > 1440 {
			return nil, ErrBadWindow
		}
		out = append(out, w)
	}
	return out, nil
}
