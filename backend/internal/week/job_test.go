package week

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/omarharoun/pod/backend/internal/blob"
	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/cycle"
	"github.com/omarharoun/pod/backend/internal/notify"
	"github.com/omarharoun/pod/backend/internal/push"
	"github.com/omarharoun/pod/backend/internal/store"
)

type harness struct {
	t       *testing.T
	service *Service
	job     *Job
	store   *store.Memory
	blobs   *blob.Local
	clock   *clock.Fixed
	podID   string
	members []string
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newHarness(t *testing.T, zone string) *harness {
	t.Helper()
	return newHarnessOf(t, zone, 2)
}

// newTrio is the harness M6 needs: a quorum of two out of three is not observable in a
// pod of two, where two continuing is everybody.
func newTrio(t *testing.T, zone string) *harness {
	t.Helper()
	return newHarnessOf(t, zone, 3)
}

func newHarnessOf(t *testing.T, zone string, size int) *harness {
	t.Helper()
	memory := store.NewMemory()
	// A Thursday, so the first week has days left in it.
	fixed := clock.NewFixed(time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC))
	blobs, err := blob.NewLocal(t.TempDir(), "http://test.invalid", "a-development-signing-secret-value")
	if err != nil {
		t.Fatalf("blob.NewLocal: %v", err)
	}

	ctx := context.Background()
	var members []string
	people := []struct{ email, name string }{
		{"sam@example.com", "Sam"},
		{"alex@example.com", "Alex"},
		{"remi@example.com", "Remi"},
	}
	for _, person := range people[:size] {
		user, err := memory.UpsertUserByEmail(ctx, person.email, fixed.Now())
		if err != nil {
			t.Fatalf("UpsertUserByEmail: %v", err)
		}
		if err := memory.SetDisplayName(ctx, user.ID, person.name); err != nil {
			t.Fatalf("SetDisplayName: %v", err)
		}
		members = append(members, user.ID)
	}
	podID := "pod-1"
	if err := memory.CreatePod(ctx, store.Pod{
		ID: podID, State: store.PodStateActive, GoalCategory: "home", Timezone: zone, CreatedAt: fixed.Now(),
	}, store.FreeLane(members...), fixed.Now()); err != nil {
		t.Fatalf("CreatePod: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	notifier := notify.NewService(memory, fixed, log)
	service := NewService(memory, blobs, fixed, notifier)
	return &harness{
		t: t, service: service,
		job:   NewJob(service, memory, blobs, fixed, log, notifier, push.Logger{Log: log}),
		store: memory, blobs: blobs, clock: fixed, podID: podID, members: members,
	}
}

func (h *harness) currentWeek() store.Week {
	h.t.Helper()
	week, err := h.service.Current(context.Background(), h.podID, h.members[0])
	if err != nil {
		h.t.Fatalf("Current: %v", err)
	}
	return week
}

func (h *harness) post(weekID, userID string) string {
	h.t.Helper()
	ctx := context.Background()
	upload, err := h.service.StartProof(ctx, weekID, userID, "photo", "image/jpeg", 2048, nil)
	if err != nil {
		h.t.Fatalf("StartProof: %v", err)
	}
	if err := h.blobs.Put(blob.Key(weekID, upload.ProofID), strings.NewReader("ten seconds")); err != nil {
		h.t.Fatalf("Put: %v", err)
	}
	if _, err := h.service.CommitProof(ctx, weekID, upload.ProofID, userID); err != nil {
		h.t.Fatalf("CommitProof: %v", err)
	}
	return upload.ProofID
}

func TestTickOpensAWeekForEveryActivePod(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	result, err := h.job.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if result.PodsSeen != 1 || result.WeeksOpened != 1 {
		t.Fatalf("got %+v", result)
	}

	// Running again must not open a second week for the same window.
	second, err := h.job.Tick(context.Background())
	if err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	if second.WeeksOpened != 0 {
		t.Fatalf("a second tick opened %d weeks", second.WeeksOpened)
	}
}

func TestTickClosesTheWeekAndOpensTheNext(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	first := h.currentWeek()
	h.post(first.ID, h.members[0])
	h.post(first.ID, h.members[1])

	h.clock.Advance(first.ClosesAt.Sub(h.clock.Now()) + time.Hour)
	result, err := h.job.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if result.WeeksClosed != 1 || result.WeeksOpened != 1 {
		t.Fatalf("got %+v, want one closed and one opened", result)
	}

	next := h.currentWeek()
	if next.ID == first.ID {
		t.Fatal("the new week is the old one")
	}
	if next.Index != first.Index+1 {
		t.Fatalf("week index went %d then %d", first.Index, next.Index)
	}
	// A new week starts everyone open again. Last week is over, whatever happened in it.
	for _, member := range next.Members {
		if member.State != store.MemberWeekOpen {
			t.Errorf("a new week starts open, got %q", member.State)
		}
	}
}

// A pod nobody opened for a fortnight still settles correctly rather than leaving weeks
// hanging open behind it.
func TestTickSettlesSeveralMissedWeeksAtOnce(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	first := h.currentWeek()

	h.clock.Advance(21 * 24 * time.Hour)
	result, err := h.job.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if result.WeeksClosed < 1 || result.WeeksOpened != 1 {
		t.Fatalf("got %+v", result)
	}

	weeks, err := h.store.ListWeeksForMemberless(context.Background(), h.podID, 0)
	if err != nil {
		t.Fatalf("ListWeeksForMemberless: %v", err)
	}
	for _, week := range weeks {
		if week.ID == first.ID && week.ClosedAt == nil {
			t.Fatal("the first week is still open three weeks later")
		}
	}
}

// The sweep is CLAUDE.md §2 rule 4 being kept, and this is the test that says so.
func TestSweepDeletesProofBytesAtCycleEnd(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	ctx := context.Background()
	week := h.currentWeek()
	proofID := h.post(week.ID, h.members[0])
	key := blob.Key(week.ID, proofID)

	exists, err := h.blobs.Exists(ctx, key)
	if err != nil || !exists {
		t.Fatalf("the proof bytes are not there to begin with: %v %v", exists, err)
	}

	h.clock.Advance(week.ClosesAt.Sub(h.clock.Now()) + time.Hour)
	result, err := h.job.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if result.ProofsDeleted != 1 || result.SweepFailures != 0 {
		t.Fatalf("got %+v", result)
	}

	// The bytes are gone from storage.
	exists, err = h.blobs.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Fatal("the proof bytes survived the sweep — the promise in §2 rule 4 is broken")
	}
	// And nothing is left holding a pointer to them.
	if _, err := h.store.GetProofForViewer(ctx, proofID, h.members[1]); err != store.ErrNotFound {
		t.Fatalf("a pod-mate can still resolve the proof: %v", err)
	}
	settled, err := h.store.GetWeekForMember(ctx, week.ID, h.members[0])
	if err != nil {
		t.Fatalf("GetWeekForMember: %v", err)
	}
	for _, member := range settled.Members {
		if member.ProofID != nil {
			t.Fatalf("member %s still carries proof id %s", member.UserID, *member.ProofID)
		}
	}
	// The fact that they posted survives. Only the bytes are ephemeral.
	for _, member := range settled.Members {
		if member.UserID == h.members[0] && member.State != store.MemberWeekPosted {
			t.Fatalf("the record of posting was lost along with the proof: %q", member.State)
		}
	}
}

func TestSweepIsIdempotent(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	week := h.currentWeek()
	h.post(week.ID, h.members[0])

	h.clock.Advance(week.ClosesAt.Sub(h.clock.Now()) + time.Hour)
	if _, err := h.job.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	second, err := h.job.Tick(context.Background())
	if err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	if second.ProofsDeleted != 0 || second.SweepFailures != 0 {
		t.Fatalf("a second sweep did work: %+v", second)
	}
}

// Two pods in different zones turn their weeks over at different instants.
func TestWeeksTurnOverInEachPodsOwnZone(t *testing.T) {
	lisbon := newHarness(t, "Europe/Lisbon")
	auckland := newHarness(t, "Pacific/Auckland")

	if lisbon.currentWeek().ClosesAt.Equal(auckland.currentWeek().ClosesAt) {
		t.Fatal("two pods on opposite sides of the world close their week at the same instant")
	}
}

func TestUnknownPodTimezoneDoesNotStopTheWholeTick(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	ctx := context.Background()
	// A second pod with a corrupt zone.
	broken, err := h.store.UpsertUserByEmail(ctx, "broken@example.com", h.clock.Now())
	if err != nil {
		t.Fatalf("UpsertUserByEmail: %v", err)
	}
	if err := h.store.CreatePod(ctx, store.Pod{
		ID: "pod-broken", State: store.PodStateActive, GoalCategory: "home",
		Timezone: "Not/AZone", CreatedAt: h.clock.Now(),
	}, store.FreeLane(broken.ID), h.clock.Now()); err != nil {
		t.Fatalf("CreatePod: %v", err)
	}

	result, err := h.job.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if result.WeeksOpened != 1 {
		t.Fatalf("the healthy pod did not get its week: %+v", result)
	}
}

// ---- M6 helpers -----------------------------------------------------------------

// everyonePosts puts every member in for the current week.
func (h *harness) everyonePosts() {
	h.t.Helper()
	week := h.currentWeek()
	for _, member := range h.members {
		h.post(week.ID, member)
	}
}

// advanceOneWeek moves past the current week's close and runs the job.
func (h *harness) advanceOneWeek() {
	h.t.Helper()
	week, err := h.service.Current(context.Background(), h.podID, h.members[0])
	if err != nil {
		// Once the pod has graduated there is no current week; move a week anyway.
		h.clock.Advance(7 * 24 * time.Hour)
	} else {
		h.clock.Advance(week.ClosesAt.Sub(h.clock.Now()) + time.Hour)
	}
	h.tick()
}

func (h *harness) tick() {
	h.t.Helper()
	if _, err := h.job.Tick(context.Background()); err != nil {
		h.t.Fatalf("Tick: %v", err)
	}
}

func (h *harness) reloadPod() store.Pod {
	h.t.Helper()
	pod, err := h.store.GetPodForMember(context.Background(), h.podID, h.members[0])
	if err != nil {
		// GetPodForMember scopes to live membership, so a closed pod is invisible
		// through it. Read it directly for the assertions that need the state.
		pods, listErr := h.store.ListPodsAwaitingContinuation(context.Background())
		if listErr == nil {
			for _, candidate := range pods {
				if candidate.ID == h.podID {
					return candidate
				}
			}
		}
		h.t.Fatalf("GetPodForMember: %v", err)
	}
	return pod
}

// graduate runs a full cycle with everyone posting, leaving the pod at its choice.
func (h *harness) graduate() {
	h.t.Helper()
	for week := 0; week < cycle.DefaultLengthWeeks; week++ {
		h.everyonePosts()
		h.advanceOneWeek()
	}
	if state := h.reloadPod().State; state != store.PodStateCompleted {
		h.t.Fatalf("the pod is %q after a full cycle, want completed", state)
	}
}

// An upload that reached storage but never reached commitProof — the client PUT the bytes
// and then lost the network — must still have its bytes deleted at cycle end.
//
// This is CLAUDE.md §2 rule 4 at its sharpest. The sweep used to skip such a proof because
// its blob_key was null, and then mark the row deleted anyway: a real photo left in
// storage forever, with the database asserting it was gone and no row any sweep would ever
// return again.
func TestSweepDeletesBytesOfAnUploadThatWasNeverCommitted(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	ctx := context.Background()
	week := h.currentWeek()

	slot, err := h.service.StartProof(ctx, week.ID, h.members[0], "photo", "image/jpeg", 2048, nil)
	if err != nil {
		t.Fatalf("StartProof: %v", err)
	}
	// The bytes land at the derivable key, exactly as a real PUT would put them.
	key := blob.Key(week.ID, slot.ProofID)
	if err := h.blobs.Put(key, strings.NewReader("ten seconds of proof")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// ...and commitProof never arrives.

	h.clock.Advance(week.ClosesAt.Sub(h.clock.Now()) + time.Hour)
	if _, _, err := h.job.Sweep(ctx, h.clock.Now()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if _, err := h.blobs.Open(key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("the bytes of an uncommitted upload survived the sweep: %v", err)
	}
}

// A slot signed just before the week closes must not stay writable after it.
//
// The view path already clamped its link to the proof's expiry; the upload path did not,
// so a slot signed a minute before close was writable for nine minutes after — long
// enough for bytes to land after the sweep had passed over the row, and once that row is
// marked deleted no sweep will ever return it again (CLAUDE.md §2 rule 4).
func TestAnUploadSlotNeverOutlivesItsWeek(t *testing.T) {
	h := newHarness(t, "Europe/Lisbon")
	ctx := context.Background()
	week := h.currentWeek()

	// One minute before the week closes.
	h.clock.Advance(week.ClosesAt.Sub(h.clock.Now()) - time.Minute)

	slot, err := h.service.StartProof(ctx, week.ID, h.members[0], "photo", "image/jpeg", 2048, nil)
	if err != nil {
		t.Fatalf("StartProof: %v", err)
	}
	if slot.ExpiresAt.After(week.ClosesAt) {
		t.Errorf("the upload slot expires at %v, after the week closes at %v", slot.ExpiresAt, week.ClosesAt)
	}
}
