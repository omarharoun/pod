package week

import (
	"context"
	"testing"
	"time"

	"github.com/omarharoun/pod/backend/internal/store"
)

// closeCurrentWeekQuietly advances past the week boundary and ticks, so the week closes
// with whatever was posted in it.
func (h *harness) closeCurrentWeekQuietly() {
	h.t.Helper()
	week := h.currentWeek()
	h.clock.Advance(week.ClosesAt.Sub(h.clock.Now()) + time.Hour)
	if _, err := h.job.Tick(context.Background()); err != nil {
		h.t.Fatalf("Tick: %v", err)
	}
}

func TestTwoQuietWeeksDissolveThePod(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	ctx := context.Background()

	h.closeCurrentWeekQuietly()
	h.closeCurrentWeekQuietly()

	if _, err := h.store.GetActivePod(ctx, h.members[0], 0); err != store.ErrNotFound {
		t.Fatalf("the pod survived two weeks with nobody in it: %v", err)
	}
	// Both members are free to be matched again.
	for _, member := range h.members {
		if _, err := h.store.GetActivePod(ctx, member, 0); err != store.ErrNotFound {
			t.Errorf("member %s is still in a pod", member)
		}
	}
}

// A pod where one person keeps showing up is not inactive — it is a pod carrying someone,
// which is what pods are for. Dissolving it would punish whoever kept turning up.
func TestAPodCarryingOnePersonIsNotDissolved(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		week := h.currentWeek()
		h.post(week.ID, h.members[0])
		h.closeCurrentWeekQuietly()
	}

	if _, err := h.store.GetActivePod(ctx, h.members[0], 0); err != nil {
		t.Fatalf("the pod was dissolved even though someone posted every week: %v", err)
	}
}

func TestOneQuietWeekIsNotEnough(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	h.closeCurrentWeekQuietly()

	if _, err := h.store.GetActivePod(context.Background(), h.members[0], 0); err != nil {
		t.Fatalf("one quiet week dissolved the pod: %v", err)
	}
}

// Only inactive members carry the count (CLAUDE.md §6).
func TestOnlyInactiveMembersCarryTheCount(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	ctx := context.Background()

	// One member posts in the first of the two quiet weeks, so only the other is
	// inactive across both.
	first := h.currentWeek()
	h.post(first.ID, h.members[0])
	h.closeCurrentWeekQuietly()
	h.closeCurrentWeekQuietly()
	h.closeCurrentWeekQuietly()

	if _, err := h.store.GetActivePod(ctx, h.members[0], 0); err != store.ErrNotFound {
		t.Fatalf("the pod should have dissolved after two quiet weeks: %v", err)
	}

	poster, err := h.store.GetUser(ctx, h.members[0])
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	quiet, err := h.store.GetUser(ctx, h.members[1])
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if poster.DissolvedPods != 0 {
		t.Errorf("the member who posted carried a count of %d", poster.DissolvedPods)
	}
	if quiet.DissolvedPods != 1 {
		t.Errorf("the member who never posted carried a count of %d, want 1", quiet.DissolvedPods)
	}
}

// Everyone gets the offer, whatever they did. It is about what happened, not about them.
func TestEveryoneIsOfferedRecoveryWhenAPodEnds(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	ctx := context.Background()
	h.closeCurrentWeekQuietly()
	h.closeCurrentWeekQuietly()

	for _, member := range h.members {
		recovery, err := h.store.RecoveryFor(ctx, member)
		if err != nil {
			t.Fatalf("RecoveryFor: %v", err)
		}
		if !recovery.OfferLowerIntensity {
			t.Errorf("member %s was not offered anything after their pod ended", member)
		}
		if recovery.OfferAlternatePath {
			t.Errorf("member %s was offered the alternate path after one pod", member)
		}
	}
}

// After three, the one-to-one path is offered. Some people need less social pressure,
// not more (CLAUDE.md §6).
func TestTheAlternatePathIsOfferedAfterThree(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	ctx := context.Background()
	user := h.members[1]

	for i := 0; i < store.AlternatePathAfter; i++ {
		podID := "pod-extra-" + string(rune('a'+i))
		if err := h.store.CreatePod(ctx, store.Pod{
			ID: podID, State: store.PodStateActive, GoalCategory: "home", Timezone: "UTC", CreatedAt: h.clock.Now(),
		}, store.FreeLane(user), h.clock.Now()); err != nil {
			// The first pod already holds this member; dissolve it first.
			if err := h.store.DissolvePod(ctx, h.podID, DissolveReasonInactive, []string{user}, h.clock.Now()); err != nil {
				t.Fatalf("DissolvePod: %v", err)
			}
			if err := h.store.CreatePod(ctx, store.Pod{
				ID: podID, State: store.PodStateActive, GoalCategory: "home", Timezone: "UTC", CreatedAt: h.clock.Now(),
			}, store.FreeLane(user), h.clock.Now()); err != nil {
				t.Fatalf("CreatePod: %v", err)
			}
		}
		if err := h.store.DissolvePod(ctx, podID, DissolveReasonInactive, []string{user}, h.clock.Now()); err != nil {
			t.Fatalf("DissolvePod: %v", err)
		}
	}

	recovery, err := h.store.RecoveryFor(ctx, user)
	if err != nil {
		t.Fatalf("RecoveryFor: %v", err)
	}
	if !recovery.OfferAlternatePath {
		t.Fatal("the alternate path was not offered after three pods ended")
	}
}

func TestDissolvingTwiceIsANoOp(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	ctx := context.Background()
	h.closeCurrentWeekQuietly()
	h.closeCurrentWeekQuietly()

	before, err := h.store.GetUser(ctx, h.members[0])
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if err := h.store.DissolvePod(ctx, h.podID, DissolveReasonInactive, h.members, h.clock.Now()); err != nil {
		t.Fatalf("DissolvePod: %v", err)
	}
	after, err := h.store.GetUser(ctx, h.members[0])
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if after.DissolvedPods != before.DissolvedPods {
		t.Fatalf("dissolving twice counted twice: %d then %d", before.DissolvedPods, after.DissolvedPods)
	}
}
