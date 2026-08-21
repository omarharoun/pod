package week

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/omarharoun/pod/backend/internal/blob"
	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/copy"
	"github.com/omarharoun/pod/backend/internal/cycle"
	"github.com/omarharoun/pod/backend/internal/notify"
	"github.com/omarharoun/pod/backend/internal/push"
	"github.com/omarharoun/pod/backend/internal/store"
)

// TickInterval is how often the week job runs. Weeks turn over at local midnight in every
// timezone the product has a pod in, so the job has to run more often than that — hourly
// is the coarsest cadence that never leaves a pod a whole day behind its own boundary.
const TickInterval = time.Hour

// JobResult is what one tick did. Counts only: a job log line must never carry a user id.
type JobResult struct {
	PodsSeen           int
	PodsDissolved      int
	PodsGraduated      int
	PodsClosed         int
	CyclesRestarted    int
	WeeksOpened        int
	WeeksClosed        int
	ProofsDeleted      int
	SweepFailures      int
	NotificationsSent  int
	NotificationsQuiet int
}

type Job struct {
	service  *Service
	store    store.Store
	blobs    blob.Store
	clock    clock.Clock
	log      *slog.Logger
	notifier *notify.Service
	sender   push.Sender
}

func NewJob(service *Service, s store.Store, b blob.Store, c clock.Clock, log *slog.Logger, n *notify.Service, sender push.Sender) *Job {
	return &Job{service: service, store: s, blobs: b, clock: c, log: log, notifier: n, sender: sender}
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
		// Closing comes first, so a pod's second quiet week is settled before we ask
		// whether it has had two.
		closed, opened, err := j.turnOver(ctx, pod, now)
		if err != nil {
			// One pod failing must not stop the rest. Its week turns over next tick.
			j.log.ErrorContext(ctx, "could not turn over a pod's week", slog.Any("error", err))
			continue
		}
		result.WeeksClosed += closed
		result.WeeksOpened += opened

		dissolved, err := j.dissolveIfInactive(ctx, pod, now)
		if err != nil {
			j.log.ErrorContext(ctx, "could not dissolve a pod", slog.Any("error", err))
			continue
		}
		if dissolved {
			result.PodsDissolved++
			continue
		}

		// Graduation comes after dissolution is ruled out. A pod that went quiet for its
		// last two weeks dissolved; it did not graduate, and the two must not both fire.
		graduated, err := j.completeIfCycleIsOver(ctx, pod, now)
		if err != nil {
			j.log.ErrorContext(ctx, "could not complete a cycle", slog.Any("error", err))
			continue
		}
		if graduated {
			result.PodsGraduated++
		}
	}

	// Held rather than returned here: the sweep and the notification drain still need to
	// run, and a broken continuation settle must not silently take proof deletion with
	// it. Reported at the end, so the tick fails rather than logging a clean result over
	// a subsystem that did nothing (ADR-028, ADR-029).
	restarted, closedPods, settleErr := j.settleContinuations(ctx, now)
	if settleErr != nil {
		j.log.ErrorContext(ctx, "could not settle continuations", slog.Any("error", settleErr))
	}
	result.CyclesRestarted = restarted
	result.PodsClosed = closedPods

	deleted, failures, err := j.Sweep(ctx, now)
	if err != nil {
		return result, err
	}
	result.ProofsDeleted = deleted
	result.SweepFailures = failures

	// Draining the outbox last means everything this tick queued goes out in the same
	// tick rather than an hour later.
	if j.notifier != nil && j.sender != nil {
		sent, err := j.notifier.Send(ctx, j.sender, 0)
		if err != nil {
			j.log.ErrorContext(ctx, "could not drain the notification outbox", slog.Any("error", err))
		}
		result.NotificationsSent = sent.Sent
		result.NotificationsQuiet = sent.NoDevices
	}

	j.log.InfoContext(ctx, "week tick complete",
		slog.Int("pods_seen", result.PodsSeen),
		slog.Int("weeks_opened", result.WeeksOpened),
		slog.Int("weeks_closed", result.WeeksClosed),
		slog.Int("pods_dissolved", result.PodsDissolved),
		slog.Int("pods_graduated", result.PodsGraduated),
		slog.Int("pods_closed", result.PodsClosed),
		slog.Int("cycles_restarted", result.CyclesRestarted),
		slog.Int("proofs_deleted", result.ProofsDeleted),
		slog.Int("sweep_failures", result.SweepFailures),
		slog.Int("notifications_sent", result.NotificationsSent),
		slog.Int("notifications_quiet", result.NotificationsQuiet),
	)
	return result, settleErr
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
			settled, err := j.store.CloseWeek(ctx, week.ID, now)
			if err != nil {
				return closed, opened, fmt.Errorf("week: close: %w", err)
			}
			closed++
			if j.notifier != nil && settled.PodCompleted {
				// Only the good news is announced at close. A week that did not
				// complete produces no notification at all: nothing went wrong, so
				// nothing reports that it did (CLAUDE.md §2 rule 1).
				j.notifier.PodCompleted(ctx, settled, pod.StreakWeeks+1)
			}
			continue
		}
		if j.notifier != nil && j.notifier.DueForClosingNotice(week) {
			j.notifier.WeekClosing(ctx, week)
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
	// A week that starts at or after the cycle's finish is a week past the end. Opening
	// it and graduating in the same tick would leave a graduated pod holding an open
	// seventh week that nobody can post to and nothing will close.
	length := pod.CycleLengthWeeks
	if length <= 0 {
		length = cycle.DefaultLengthWeeks
	}
	if !current.OpensAt.Before(cycle.FinishesAt(cycleStart(pod), length, location)) {
		existing = true
	}
	if !existing {
		fresh, err := j.service.open(ctx, pod, now)
		if err != nil {
			return closed, opened, fmt.Errorf("week: open: %w", err)
		}
		opened++
		if j.notifier != nil {
			action, err := copy.Resolve(copy.KeyWeekDefaultAction, nil)
			if err == nil {
				if fresh.ActionText != "" {
					action = fresh.ActionText
				}
				j.notifier.WeekOpened(ctx, pod, fresh, action)
			}
		}
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
		// The key is derivable, so it is used whether or not the proof was ever
		// committed. An upload that reached storage and then lost the network has a null
		// blob_key and real bytes; skipping it because the column is null left a photo in
		// storage forever while the row was marked deleted and became unlistable. There
		// is no second chance to find those bytes — nothing else scans by prefix.
		key := blob.Key(proof.WeekID, proof.ID)
		if proof.BlobKey != nil {
			key = *proof.BlobKey
		}
		if err := j.blobs.Delete(ctx, key); err != nil && !errors.Is(err, blob.ErrNotFound) {
			// Leave the row undeleted so the next tick tries again. A proof whose bytes
			// we failed to remove must not look like one we removed.
			j.log.ErrorContext(ctx, "could not delete proof bytes", slog.Any("error", err))
			failures++
			continue
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
