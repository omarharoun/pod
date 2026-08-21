package week

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/omarharoun/pod/backend/internal/cycle"
	"github.com/omarharoun/pod/backend/internal/store"
)

var (
	// ErrNotGraduated is returned when someone answers the continue question for a pod
	// that is not asking it.
	ErrNotGraduated = errors.New("week: the pod is not waiting on that choice")
	// ErrChoiceClosed is returned after the deadline has passed.
	ErrChoiceClosed = errors.New("week: the choice has closed")
)

// Continue records one member's answer to "are you carrying on".
//
// Answering does not, by itself, restart or close the pod. That is settled by the job,
// once the answer cannot change or the deadline arrives — so that the last person to
// answer is not the one who appears to have ended it.
func (s *Service) Continue(ctx context.Context, podID, userID string, continuing bool) (store.Pod, error) {
	pod, err := s.store.GetPodForMember(ctx, podID, userID)
	if err != nil {
		return store.Pod{}, err
	}
	if pod.State != store.PodStateCompleted {
		return store.Pod{}, ErrNotGraduated
	}
	now := s.clock.Now()
	if pod.DecideBy != nil && !now.Before(*pod.DecideBy) {
		return store.Pod{}, ErrChoiceClosed
	}
	if err := s.store.AnswerContinuation(ctx, podID, userID, continuing, now); err != nil {
		return store.Pod{}, fmt.Errorf("week: answer continuation: %w", err)
	}
	return s.store.GetPodForMember(ctx, podID, userID)
}

// Continuation returns the counts for a pod, plus the caller's own answer.
func (s *Service) Continuation(ctx context.Context, podID, userID string) (store.Continuation, error) {
	return s.store.ContinuationFor(ctx, podID, userID)
}

// Cycle describes where a pod is in its cycle.
type Cycle struct {
	LengthWeeks int
	// WeeksElapsed is the position in the cycle: how many of its weeks have closed. Not
	// a measure of anybody.
	WeeksElapsed int
	// WeeksEveryoneWasIn is how many of those the pod completed. Distinct from the
	// shared streak, which is the trailing run and is reset by any miss.
	WeeksEveryoneWasIn int
	EndsAt             time.Time
}

// CycleOf describes where a pod is in its cycle.
func (s *Service) CycleOf(ctx context.Context, pod store.Pod) (Cycle, error) {
	location, err := cycle.Load(pod.Timezone)
	if err != nil {
		return Cycle{}, err
	}
	length := pod.CycleLengthWeeks
	if length <= 0 {
		length = cycle.DefaultLengthWeeks
	}
	everyoneIn, err := s.store.CountWeeksEveryoneWasIn(ctx, pod.ID)
	if err != nil {
		return Cycle{}, fmt.Errorf("week: count completed weeks: %w", err)
	}
	return Cycle{
		LengthWeeks:        length,
		WeeksElapsed:       cycle.WeeksCompleted(pod.CreatedAt, s.clock.Now(), length, location),
		WeeksEveryoneWasIn: everyoneIn,
		EndsAt:             cycle.FinishesAt(pod.CreatedAt, length, location),
	}, nil
}

// completeIfCycleIsOver graduates a pod that has reached the end of its cycle.
//
// Called from the weekly job after weeks are closed, so the last week of a cycle settles
// before the pod stops being active.
func (j *Job) completeIfCycleIsOver(ctx context.Context, pod store.Pod, now time.Time) (bool, error) {
	if pod.State != store.PodStateActive {
		return false, nil
	}
	location, err := cycle.Load(pod.Timezone)
	if err != nil {
		return false, err
	}
	length := pod.CycleLengthWeeks
	if length <= 0 {
		length = cycle.DefaultLengthWeeks
	}
	if now.Before(cycle.FinishesAt(pod.CreatedAt, length, location)) {
		return false, nil
	}
	if err := j.store.CompleteCycle(ctx, pod.ID, now, now.Add(cycle.DecideWindow)); err != nil {
		return false, fmt.Errorf("week: complete cycle: %w", err)
	}
	if j.notifier != nil {
		j.notifier.CycleCompleted(ctx, pod)
	}
	return true, nil
}

// settleContinuations restarts or closes each graduated pod whose choice is resolved.
//
// A pod restarts as soon as two members have said yes, because that answer cannot change.
// It closes only at the deadline, or once everyone has answered: someone who has not
// answered may still say yes, and reading their silence as agreement with the people who
// declined is what would close a pod out from under them.
func (j *Job) settleContinuations(ctx context.Context, now time.Time) (restarted, closed int, err error) {
	pods, err := j.store.ListPodsAwaitingContinuation(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("week: list graduated pods: %w", err)
	}
	for _, pod := range pods {
		counts, err := j.store.ContinuationFor(ctx, pod.ID, "")
		if err != nil {
			j.log.ErrorContext(ctx, "could not read a graduated pod's choice")
			continue
		}
		choice := cycle.Choice{
			Continuing: counts.Continuing,
			Declined:   counts.Declined,
			Undecided:  counts.Undecided,
		}
		deadlinePassed := pod.DecideBy != nil && !now.Before(*pod.DecideBy)
		if !choice.Settled() && !deadlinePassed {
			continue
		}
		if choice.Continues() {
			if err := j.store.StartNextCycle(ctx, pod.ID, now); err != nil {
				j.log.ErrorContext(ctx, "could not start the next cycle")
				continue
			}
			restarted++
			continue
		}
		if err := j.store.CloseCompletedPod(ctx, pod.ID, now); err != nil {
			j.log.ErrorContext(ctx, "could not close a graduated pod")
			continue
		}
		closed++
	}
	return restarted, closed, nil
}
