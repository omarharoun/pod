package matching

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	_ "time/tzdata" // embed the zone database: the service ships as a scratch container

	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/ids"
	"github.com/omarharoun/pod/backend/internal/notify"
	"github.com/omarharoun/pod/backend/internal/reliability"
	"github.com/omarharoun/pod/backend/internal/store"
)

// WindowInterval is how often matching runs. CLAUDE.md §5 is explicit that matching is a
// scheduled job and not an on-request operation: instant matching produces bad pods,
// because the pool it can choose from is whoever happens to be there that second.
const WindowInterval = time.Hour

// NextWindowAt is the start of the next matching window after now. The client shows this
// so the waiting screen can say when rather than spin.
func NextWindowAt(now time.Time) time.Time {
	return now.UTC().Truncate(WindowInterval).Add(WindowInterval)
}

// Result is what one window did. Everything here is a count: a matching log line must
// never carry a user id next to a score.
type Result struct {
	PoolSize       int
	PodsFormed     int
	MembersMatched int
	LeftWaiting    int
}

// Job runs one matching window.
type Job struct {
	store    store.Store
	clock    clock.Clock
	log      *slog.Logger
	opts     Options
	notifier *notify.Service
}

func NewJob(s store.Store, c clock.Clock, log *slog.Logger, opts Options, n *notify.Service) *Job {
	return &Job{store: s, clock: c, log: log, opts: opts, notifier: n}
}

// RunWindow reads the whole pool, forms what pods it can, and leaves the rest queued.
//
// Leaving people queued is the normal outcome, not a failure: someone who cannot be
// matched well this hour waits for the next one. Nothing here relaxes a filter or drops a
// threshold to clear the queue.
func (j *Job) RunWindow(ctx context.Context) (Result, error) {
	now := j.clock.Now()

	waiting, err := j.store.ListPoolCandidates(ctx, 0)
	if err != nil {
		return Result{}, fmt.Errorf("matching: read pool: %w", err)
	}
	result := Result{PoolSize: len(waiting)}
	if len(waiting) == 0 {
		return result, nil
	}

	candidates := make([]Candidate, 0, len(waiting))
	for _, w := range waiting {
		// History is read per candidate rather than joined in, because the reliability
		// score is derived and never stored — there is no column to select
		// (CLAUDE.md §6). The pool is bounded and the job is hourly, so this is cheap.
		//
		// The whole history is read, not the formula's 8-week window: Compute folds
		// forward from a fixed seed and truncating the fold to the window is what made
		// the §6 movement cap fail (ADR-022). A week is one row.
		outcomes, err := j.store.RecentOutcomes(ctx, w.UserID, reliability.Replay)
		if err != nil {
			return result, fmt.Errorf("matching: read history: %w", err)
		}
		candidates = append(candidates, toCandidate(w, outcomes, now))
	}

	for _, pod := range Assemble(candidates, j.opts) {
		created, err := j.persist(ctx, pod, waiting, now)
		if err != nil {
			// One pod failing must not abandon the rest of the window. The members stay
			// queued and are picked up next hour.
			j.log.ErrorContext(ctx, "could not create pod",
				slog.Int("members", len(pod.Members)),
				slog.Any("error", err),
			)
			continue
		}
		if created {
			result.PodsFormed++
			result.MembersMatched += len(pod.Members)
		}
	}
	result.LeftWaiting = result.PoolSize - result.MembersMatched

	j.log.InfoContext(ctx, "matching window complete",
		slog.Int("pool_size", result.PoolSize),
		slog.Int("pods_formed", result.PodsFormed),
		slog.Int("members_matched", result.MembersMatched),
		slog.Int("left_waiting", result.LeftWaiting),
	)
	return result, nil
}

func (j *Job) persist(ctx context.Context, pod Pod, waiting []store.PoolCandidate, now time.Time) (bool, error) {
	id, err := ids.NewUUID()
	if err != nil {
		return false, err
	}
	memberships := make([]store.Membership, 0, len(pod.Members))
	for _, m := range pod.Members {
		memberships = append(memberships, store.Membership{UserID: m.UserID, Slot: m.Slot})
	}

	record := store.Pod{
		ID:           id,
		State:        store.PodStateActive,
		GoalCategory: pod.Members[0].GoalCategory,
		Timezone:     podTimezone(pod, waiting),
		CreatedAt:    now,
	}
	if err := j.store.CreatePod(ctx, record, memberships, now); err != nil {
		if err == store.ErrConflict {
			// Someone was matched by a concurrent window. Not an error worth alarming
			// about; they have a pod, which is the point.
			j.log.InfoContext(ctx, "pod skipped, a member was matched elsewhere")
			return false, nil
		}
		return false, err
	}

	// The offer to lower the bar was about the pod that ended. Being in a new one
	// answers it.
	for _, member := range memberships {
		if err := j.store.ClearRecoveryOffer(ctx, member.UserID); err != nil {
			j.log.ErrorContext(ctx, "could not clear a recovery offer", slog.Any("error", err))
		}
	}

	if j.notifier != nil {
		// Read the pod back so the notification can name people, which needs the
		// display names the matcher deliberately never sees.
		if formed, err := j.store.GetActivePod(ctx, memberships[0].UserID, memberships[0].Slot); err == nil {
			j.notifier.PodFormed(ctx, formed)
		}
	}
	return true, nil
}

// podTimezone picks the pod's shared zone. Cycles anchor to it rather than to each
// member's device (CLAUDE.md §5), so it has to be one of the members' real zones — an
// averaged offset would put the week boundary in the middle of somebody's evening.
//
// The earliest-queued member's zone wins. Arbitrary, but stable, and the ±3 hour filter
// has already guaranteed nobody is far from it.
func podTimezone(pod Pod, waiting []store.PoolCandidate) string {
	earliest := ""
	var earliestAt time.Time
	for _, member := range pod.Members {
		for _, w := range waiting {
			if w.UserID != member.UserID {
				continue
			}
			if earliest == "" || w.QueuedAt.Before(earliestAt) {
				earliest, earliestAt = w.Timezone, w.QueuedAt
			}
		}
	}
	if earliest == "" {
		return "UTC"
	}
	return earliest
}

func toCandidate(w store.PoolCandidate, outcomes []store.WeekOutcome, now time.Time) Candidate {
	windows := make([]Window, 0, len(w.Availability))
	for _, a := range w.Availability {
		windows = append(windows, Window{DayOfWeek: a.DayOfWeek, StartMinute: a.StartMinute, EndMinute: a.EndMinute})
	}
	return Candidate{
		UserID:                w.UserID,
		Slot:                  w.Slot,
		GoalCategory:          w.GoalCategory,
		Intensity:             w.Intensity,
		TimezoneOffsetMinutes: OffsetMinutes(w.Timezone, now),
		Embedding:             w.Embedding,
		// Derived from history, falling back to the cold start for a new account.
		Reliability:  reliability.Compute(toOutcomes(outcomes), reliability.SelfReport(w.SelfReport)),
		Availability: windows,
		TenureWeeks:  w.TenureWeeks,
	}
}

func toOutcomes(outcomes []store.WeekOutcome) []reliability.Outcome {
	out := make([]reliability.Outcome, 0, len(outcomes))
	for _, outcome := range outcomes {
		out = append(out, reliability.Outcome{Posted: outcome.Posted, PodCompleted: outcome.PodCompleted})
	}
	return out
}

// OffsetMinutes is the zone's offset from UTC at now, which is what the ±3 hour filter
// compares. Using the offset at a moment rather than a fixed one means the filter follows
// daylight saving instead of quietly widening or narrowing twice a year.
//
// An unknown zone is treated as UTC rather than rejected: a bad zone string should cost
// someone a slightly worse match, not their place in the queue.
func OffsetMinutes(zone string, now time.Time) int {
	location, err := time.LoadLocation(zone)
	if err != nil {
		return 0
	}
	_, offsetSeconds := now.In(location).Zone()
	return offsetSeconds / 60
}
