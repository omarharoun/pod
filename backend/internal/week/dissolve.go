package week

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/omarharoun/pod/backend/internal/store"
)

// InactiveWeeksBeforeDissolving is CLAUDE.md §6: "pod inactive 2 consecutive weeks →
// dissolve → members re-enter pool".
const InactiveWeeksBeforeDissolving = 2

// DissolveReasonInactive is the only reason the system writes today.
const DissolveReasonInactive = "inactive"

// dissolveIfInactive ends a pod whose last two closed weeks had nobody in them at all.
//
// "Inactive" is read literally: a week in which **no member posted**. A pod where one
// person keeps showing up is not inactive — it is a pod carrying someone, which is what
// pods are for. Dissolving that would punish the person who kept turning up.
func (j *Job) dissolveIfInactive(ctx context.Context, pod store.Pod, now time.Time) (bool, error) {
	weeks, err := j.store.ListWeeksForMemberless(ctx, pod.ID, InactiveWeeksBeforeDissolving+1)
	if err != nil {
		return false, err
	}

	closed := make([]store.Week, 0, len(weeks))
	for _, week := range weeks {
		if week.ClosedAt != nil {
			closed = append(closed, week)
		}
	}
	if len(closed) < InactiveWeeksBeforeDissolving {
		return false, nil
	}
	for _, week := range closed[:InactiveWeeksBeforeDissolving] {
		if anyonePosted(week) {
			return false, nil
		}
	}

	// Who carries the count is computed over the pod's WHOLE life, not over the two weeks
	// that triggered this.
	//
	// CLAUDE.md §6 says only inactive members take the penalty, which only means anything
	// if a dissolution can happen while somebody was active. Under the two-quiet-weeks
	// trigger, everyone is inactive during those weeks by definition — so "inactive
	// member" has to mean something wider, and the reading that makes the clause
	// meaningful is also the gentlest one available: you carry it only if you never showed
	// up in this pod at all. Somebody who posted in week one and then went quiet is not
	// the same as somebody who was never there. See ADR-019.
	history, err := j.store.ListWeeksForMemberless(ctx, pod.ID, 0)
	if err != nil {
		return false, err
	}
	inactive := inactiveMembers(history)

	if err := j.store.DissolvePod(ctx, pod.ID, DissolveReasonInactive, inactive, now); err != nil {
		return false, fmt.Errorf("week: dissolve pod: %w", err)
	}
	j.log.InfoContext(ctx, "pod dissolved after two quiet weeks",
		slog.Int("members", len(pod.Members)),
		slog.Int("inactive_members", len(inactive)),
	)
	return true, nil
}

func anyonePosted(week store.Week) bool {
	for _, member := range week.Members {
		if member.State == store.MemberWeekPosted {
			return true
		}
	}
	return false
}

// inactiveMembers is everyone who posted in none of the given weeks at all.
func inactiveMembers(weeks []store.Week) []string {
	posted := map[string]bool{}
	seen := map[string]bool{}
	for _, week := range weeks {
		for _, member := range week.Members {
			seen[member.UserID] = true
			if member.State == store.MemberWeekPosted {
				posted[member.UserID] = true
			}
		}
	}
	var inactive []string
	for userID := range seen {
		if !posted[userID] {
			inactive = append(inactive, userID)
		}
	}
	sortStrings(inactive)
	return inactive
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
