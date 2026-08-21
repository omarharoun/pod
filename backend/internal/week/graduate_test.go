package week

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/omarharoun/pod/backend/internal/cycle"
	"github.com/omarharoun/pod/backend/internal/store"
)

// A pod that reaches the end of its cycle graduates. It does not get a seventh week.
func TestAPodGraduatesAtTheEndOfItsCycle(t *testing.T) {
	h := newTrio(t, "Europe/Lisbon")
	ctx := context.Background()

	for week := 0; week < cycle.DefaultLengthWeeks; week++ {
		h.everyonePosts()
		h.advanceOneWeek()
	}

	pod := h.reloadPod()
	if pod.State != store.PodStateCompleted {
		t.Fatalf("after %d weeks the pod is %q, want completed", cycle.DefaultLengthWeeks, pod.State)
	}
	if pod.CompletedAt == nil {
		t.Error("a completed pod has no completed_at")
	}
	if pod.DissolvedAt != nil {
		t.Error("graduating set dissolved_at — those are different events")
	}
	if pod.DecideBy == nil {
		t.Fatal("graduating did not open the choice")
	}

	// No seventh week.
	weeks, err := h.store.ListWeeksForMemberless(ctx, pod.ID, 0)
	if err != nil {
		t.Fatalf("ListWeeksForMemberless: %v", err)
	}
	if len(weeks) > cycle.DefaultLengthWeeks {
		t.Errorf("a graduated pod has %d weeks, want at most %d", len(weeks), cycle.DefaultLengthWeeks)
	}
}

// Graduating is not dissolving. CLAUDE.md §6's penalty is for pods that stopped early;
// finishing six weeks is the opposite of that, and must cost nobody anything.
func TestGraduatingCostsNobodyAnything(t *testing.T) {
	h := newTrio(t, "Europe/Lisbon")
	ctx := context.Background()

	for week := 0; week < cycle.DefaultLengthWeeks; week++ {
		h.everyonePosts()
		h.advanceOneWeek()
	}

	for _, member := range h.members {
		recovery, err := h.store.RecoveryFor(ctx, member)
		if err != nil {
			t.Fatalf("RecoveryFor: %v", err)
		}
		if recovery.OfferLowerIntensity {
			t.Errorf("member %s was offered a gentler restart after finishing a cycle", member)
		}
		if recovery.OfferAlternatePath {
			t.Errorf("member %s was offered the alternate path after finishing a cycle", member)
		}
	}
}

// A pod that went quiet for its last two weeks dissolved. It did not graduate, and both
// must not fire — the copy for the two is completely different.
func TestAQuietPodDissolvesRatherThanGraduating(t *testing.T) {
	h := newTrio(t, "Europe/Lisbon")

	for week := 0; week < cycle.DefaultLengthWeeks-2; week++ {
		h.everyonePosts()
		h.advanceOneWeek()
	}
	h.advanceOneWeek()
	h.advanceOneWeek()

	// A dissolved pod is unreachable through membership, which is the point — so this
	// asserts on what is observable: nobody is in it, and it is not asking anyone to
	// choose whether to carry on.
	for _, member := range h.members {
		if _, err := h.store.GetActivePod(context.Background(), member, 0); err == nil {
			t.Errorf("member %s is still in a pod that went quiet for two weeks", member)
		}
	}
	awaiting, err := h.store.ListPodsAwaitingContinuation(context.Background())
	if err != nil {
		t.Fatalf("ListPodsAwaitingContinuation: %v", err)
	}
	for _, pod := range awaiting {
		if pod.ID == h.podID {
			t.Fatal("a pod that dissolved is being asked whether it wants another cycle")
		}
	}

	// And the members carry the dissolve consequence, which graduation never applies.
	recovery, err := h.store.RecoveryFor(context.Background(), h.members[0])
	if err != nil {
		t.Fatalf("RecoveryFor: %v", err)
	}
	if !recovery.OfferLowerIntensity {
		t.Error("a pod dissolved and nobody was offered a gentler restart")
	}
}

// Two continuing keeps the pod, and the third leaves with nothing held against them.
func TestTwoContinuingKeepsThePod(t *testing.T) {
	h := newTrio(t, "Europe/Lisbon")
	ctx := context.Background()
	h.graduate()

	pod := h.reloadPod()
	for _, member := range h.members[:2] {
		if _, err := h.service.Continue(ctx, pod.ID, member, true); err != nil {
			t.Fatalf("Continue: %v", err)
		}
	}
	h.tick()

	restarted := h.reloadPod()
	if restarted.State != store.PodStateActive {
		t.Fatalf("two members carried on and the pod is %q", restarted.State)
	}
	if len(restarted.Members) != 2 {
		t.Errorf("the next cycle has %d members, want the 2 who said yes", len(restarted.Members))
	}
	// The shared streak belongs to the pod, and the pod did not stop.
	if restarted.StreakWeeks == 0 {
		t.Error("carrying on reset the shared streak")
	}
	recovery, err := h.store.RecoveryFor(ctx, h.members[2])
	if err != nil {
		t.Fatalf("RecoveryFor: %v", err)
	}
	if recovery.OfferAlternatePath {
		t.Error("the member who stopped was treated as having had a pod dissolve")
	}
}

// One continuing closes the pod. Everyone goes back to the pool, nobody carries a count.
func TestOneContinuingClosesThePod(t *testing.T) {
	h := newTrio(t, "Europe/Lisbon")
	ctx := context.Background()
	h.graduate()
	pod := h.reloadPod()

	if _, err := h.service.Continue(ctx, pod.ID, h.members[0], true); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	for _, member := range h.members[1:] {
		if _, err := h.service.Continue(ctx, pod.ID, member, false); err != nil {
			t.Fatalf("Continue: %v", err)
		}
	}
	h.tick()

	closed := h.reloadPod()
	if closed.State == store.PodStateActive {
		t.Fatal("one member carrying on restarted the pod")
	}
	for _, member := range h.members {
		if _, err := h.store.GetActivePod(ctx, member, 0); err == nil {
			t.Errorf("member %s is still in a pod after it closed", member)
		}
	}
}

// Silence holds the pod open until the deadline, then closes it. Closing is the default
// because keeping someone in a pod they did not re-choose is the worse mistake.
func TestSilenceHoldsThePodOpenThenClosesIt(t *testing.T) {
	h := newTrio(t, "Europe/Lisbon")
	h.graduate()

	h.tick()
	if state := h.reloadPod().State; state != store.PodStateCompleted {
		t.Fatalf("nobody had answered and the pod is already %q", state)
	}

	h.clock.Advance(cycle.DecideWindow + time.Hour)
	h.tick()
	if state := h.reloadPod().State; state == store.PodStateActive {
		t.Fatal("the deadline passed with nobody answering and the pod restarted anyway")
	}
}

// Two declines must not settle it while someone has not answered — they may still say
// yes, and reading their silence as agreement is what closes a pod out from under them.
func TestTwoDeclinesDoNotCloseThePodEarly(t *testing.T) {
	h := newTrio(t, "Europe/Lisbon")
	ctx := context.Background()
	h.graduate()
	pod := h.reloadPod()

	for _, member := range h.members[1:] {
		if _, err := h.service.Continue(ctx, pod.ID, member, false); err != nil {
			t.Fatalf("Continue: %v", err)
		}
	}
	h.tick()

	if state := h.reloadPod().State; state != store.PodStateCompleted {
		t.Fatalf("two declines closed the pod while the third had not answered: %q", state)
	}
}

// The single most important assertion in M6. "Alex is not continuing" is the most shaming
// sentence this product could show, and it is one join away at all times — so this checks
// the serialised bytes, not the struct.
func TestNoResponseEverAttributesADeclineToAMember(t *testing.T) {
	h := newTrio(t, "Europe/Lisbon")
	ctx := context.Background()
	h.graduate()
	pod := h.reloadPod()

	// One yes, one no, one silent — every combination present at once.
	if _, err := h.service.Continue(ctx, pod.ID, h.members[0], true); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if _, err := h.service.Continue(ctx, pod.ID, h.members[1], false); err != nil {
		t.Fatalf("Continue: %v", err)
	}

	// Read it as each member in turn: nobody may learn who declined, including from
	// their own view.
	for _, viewer := range h.members {
		counts, err := h.service.Continuation(ctx, pod.ID, viewer)
		if err != nil {
			t.Fatalf("Continuation: %v", err)
		}
		encoded, err := json.Marshal(counts)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		body := string(encoded)
		for _, member := range h.members {
			if member == viewer {
				continue
			}
			if strings.Contains(body, member) {
				t.Errorf("viewer %s can see member %s in the continuation: %s", viewer, member, body)
			}
		}
	}
}

// Answering again replaces the previous answer while the choice is open. Someone who
// said no and changed their mind is exactly who this product is for.
func TestChangingYourMindIsAllowedWhileTheChoiceIsOpen(t *testing.T) {
	h := newTrio(t, "Europe/Lisbon")
	ctx := context.Background()
	h.graduate()
	pod := h.reloadPod()

	if _, err := h.service.Continue(ctx, pod.ID, h.members[0], false); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if _, err := h.service.Continue(ctx, pod.ID, h.members[0], true); err != nil {
		t.Fatalf("Continue again: %v", err)
	}

	counts, err := h.service.Continuation(ctx, pod.ID, h.members[0])
	if err != nil {
		t.Fatalf("Continuation: %v", err)
	}
	if counts.Continuing != 1 || counts.Declined != 0 {
		t.Errorf("changing your mind left %+v", counts)
	}
	if !counts.YouAnswered || !counts.YouAreContinuing {
		t.Errorf("the caller's own answer is wrong: %+v", counts)
	}
}

func TestAnsweringAPodThatIsNotAskingIsRejected(t *testing.T) {
	h := newTrio(t, "Europe/Lisbon")
	ctx := context.Background()
	pod := h.reloadPod()

	if _, err := h.service.Continue(ctx, pod.ID, h.members[0], true); err == nil {
		t.Fatal("an active pod accepted a continuation answer")
	}
}

func TestAnsweringAfterTheDeadlineIsRejected(t *testing.T) {
	h := newTrio(t, "Europe/Lisbon")
	ctx := context.Background()
	h.graduate()
	pod := h.reloadPod()

	h.clock.Advance(cycle.DecideWindow + time.Hour)
	if _, err := h.service.Continue(ctx, pod.ID, h.members[0], true); err == nil {
		t.Fatal("an answer was accepted after the deadline")
	}
}
