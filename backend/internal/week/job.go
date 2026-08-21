package week

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/omarharoun/pod/backend/internal/blob"
	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/cycle"
	"github.com/omarharoun/pod/backend/internal/store"
)

// TickInterval is how often the week job runs. Weeks turn over at local midnight in every
// timezone the product has a pod in, so the job has to run more often than that — hourly
// is the coarsest cadence that never leaves a pod a whole day behind its own boundary.
const TickInterval = time.Hour

// JobResult is what one tick did. Counts only: a job log line must never carry a user id.
type JobResult struct {
	PodsSeen      int
	WeeksOpened   int
	WeeksClosed   int
	ProofsDeleted int
	SweepFailures int
}

type Job struct {
	service *Service
	store   store.Store
	blobs   blob.Store
	clock   clock.Clock
	log     *slog.Logger
}

func NewJob(service *Service, s store.Store, b blob.Store, c clock.Clock, log *slog.Logger) *Job {
	return &Job{service: service, store: s, blobs: b, clock: c, log: log}
}

// Tick closes weeks that are over, opens the ones that are due, and sweeps proof that has
// expired — in that order, because closing a week is what makes its proof expired.
func (j *Job) Tick(ctx context.Context) (JobResult, error) {
	now := j.clock.Now()
	result := JobResult{}

	pods, err := j.store.ListActivePods(ctx)
	if err != nil {
		return result, fmt.Errorf("week: list pods: %w", err)
	}
	result.PodsSeen = len(pods)

	for _, pod := range pods {
		closed, opened, err := j.turnOver(ctx, pod, now)
		if err != nil {
			// One pod failing must not stop the rest. Its week turns over next tick.
			j.log.ErrorContext(ctx, "could not turn over a pod's week", slog.Any("error", err))
			continue
		}
		result.WeeksClosed += closed
		result.WeeksOpened += opened
	}

	deleted, failures, err := j.Sweep(ctx, now)
	if err != nil {
		return result, err
	}
	result.ProofsDeleted = deleted
	result.SweepFailures = failures

	j.log.InfoContext(ctx, "week tick complete",
		slog.Int("pods_seen", result.PodsSeen),
		slog.Int("weeks_opened", result.WeeksOpened),
		slog.Int("weeks_closed", result.WeeksClosed),
		slog.Int("proofs_deleted", result.ProofsDeleted),
		slog.Int("sweep_failures", result.SweepFailures),
	)
	return result, nil
}

func (j *Job) turnOver(ctx context.Context, pod store.Pod, now time.Time) (closed, opened int, err error) {
	location, err := cycle.Load(pod.Timezone)
	if err != nil {
		return 0, 0, err
	}
	current := cycle.WeekContaining(now, location)

	// Close anything that is over. Looking at the recent history rather than only the
	// last week means a pod nobody opened for a fortnight still settles correctly.
	weeks, err := j.store.ListWeeksForMemberless(ctx, pod.ID, 8)
	if err != nil {
		return 0, 0, err
	}
	for _, week := range weeks {
		if week.ClosedAt == nil && !now.Before(week.ClosesAt) {
			if _, err := j.store.CloseWeek(ctx, week.ID, now); err != nil {
				return closed, opened, fmt.Errorf("week: close: %w", err)
			}
			closed++
		}
	}

	// Open the current one if it does not exist. EnsureWeek is keyed on the opening
	// instant, so running twice cannot produce two weeks.
	existing := false
	for _, week := range weeks {
		if week.OpensAt.Equal(current.OpensAt) {
			existing = true
		}
	}
	if !existing {
		if _, err := j.service.open(ctx, pod, now); err != nil {
			return closed, opened, fmt.Errorf("week: open: %w", err)
		}
		opened++
	}
	return closed, opened, nil
}

// Sweep deletes proof that has reached its expiry.
//
// This is CLAUDE.md §2 rule 4 being kept. The bytes go first and the row is marked only
// once they are gone, so a crash between the two leaves a proof that is swept again next
// tick rather than one that is marked deleted while its bytes remain.
func (j *Job) Sweep(ctx context.Context, now time.Time) (deleted, failures int, err error) {
	expired, err := j.store.ListExpiredProofs(ctx, now, 0)
	if err != nil {
		return 0, 0, fmt.Errorf("week: list expired proofs: %w", err)
	}

	for _, proof := range expired {
		if proof.BlobKey != nil {
			if err := j.blobs.Delete(ctx, *proof.BlobKey); err != nil && !errors.Is(err, blob.ErrNotFound) {
				// Leave the row undeleted so the next tick tries again. A proof whose
				// bytes we failed to remove must not look like one we removed.
				j.log.ErrorContext(ctx, "could not delete proof bytes", slog.Any("error", err))
				failures++
				continue
			}
		}
		if err := j.store.MarkProofDeleted(ctx, proof.ID, now); err != nil {
			j.log.ErrorContext(ctx, "could not mark proof deleted", slog.Any("error", err))
			failures++
			continue
		}
		deleted++
	}
	return deleted, failures, nil
}
