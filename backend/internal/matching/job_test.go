package matching

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/embed"
	"github.com/omarharoun/pod/backend/internal/store"
)

type jobHarness struct {
	t     *testing.T
	job   *Job
	store *store.Memory
	clock *clock.Fixed
}

func newJobHarness(t *testing.T, opts Options) *jobHarness {
	t.Helper()
	memory := store.NewMemory()
	fixed := clock.NewFixed(time.Date(2026, 8, 21, 9, 30, 0, 0, time.UTC))
	return &jobHarness{
		t:     t,
		store: memory,
		clock: fixed,
		job:   NewJob(memory, fixed, slog.New(slog.NewTextHandler(io.Discard, nil)), opts, nil, nil),
	}
}

type person struct {
	goal      string
	category  string
	intensity int
	timezone  string
	report    string
}

func (h *jobHarness) queue(p person) string {
	h.t.Helper()
	ctx := context.Background()
	user, err := h.store.UpsertUserByEmail(ctx, p.goal+"@example.com", h.clock.Now().Add(-14*24*time.Hour))
	if err != nil {
		h.t.Fatalf("UpsertUserByEmail: %v", err)
	}
	vectors, err := embed.Hashing{}.Embed(ctx, []string{p.goal})
	if err != nil {
		h.t.Fatalf("Embed: %v", err)
	}
	category := p.category
	if category == "" {
		category = "home"
	}
	intensity := p.intensity
	if intensity == 0 {
		intensity = 3
	}
	zone := p.timezone
	if zone == "" {
		zone = "UTC"
	}
	report := p.report
	if report == "" {
		report = "sometimes"
	}
	if err := h.store.UpsertIntent(ctx, store.Intent{
		UserID: user.ID, GoalText: p.goal, GoalCategory: category, Intensity: intensity,
		Timezone: zone, SelfReport: report, Embedding: vectors[0], UpdatedAt: h.clock.Now(),
	}); err != nil {
		h.t.Fatalf("UpsertIntent: %v", err)
	}
	if err := h.store.JoinPool(ctx, user.ID, 0, h.clock.Now()); err != nil {
		h.t.Fatalf("JoinPool: %v", err)
	}
	return user.ID
}

func TestRunWindowOnAnEmptyPool(t *testing.T) {
	h := newJobHarness(t, Options{})
	result, err := h.job.RunWindow(context.Background())
	if err != nil {
		t.Fatalf("RunWindow: %v", err)
	}
	if result != (Result{}) {
		t.Fatalf("got %+v, want a zero result", result)
	}
}

func TestRunWindowFormsAPodAndDequeuesIt(t *testing.T) {
	h := newJobHarness(t, Options{ThirdThreshold: 0.3})
	a := h.queue(person{goal: "sort out the spare room"})
	b := h.queue(person{goal: "clear out the spare room"})

	result, err := h.job.RunWindow(context.Background())
	if err != nil {
		t.Fatalf("RunWindow: %v", err)
	}
	if result.PodsFormed != 1 || result.MembersMatched != 2 || result.LeftWaiting != 0 {
		t.Fatalf("got %+v", result)
	}

	ctx := context.Background()
	pod, err := h.store.GetActivePod(ctx, a, 0)
	if err != nil {
		t.Fatalf("GetActivePod: %v", err)
	}
	if len(pod.Members) != 2 {
		t.Fatalf("pod has %d members", len(pod.Members))
	}
	if pod.StreakWeeks != 0 {
		t.Errorf("a new pod starts at zero weeks, got %d", pod.StreakWeeks)
	}
	// Both are out of the queue, and the other member sees the same pod.
	for _, id := range []string{a, b} {
		if _, err := h.store.GetPoolTicket(ctx, id, 0); err != store.ErrNotFound {
			t.Errorf("%s is still queued after being matched: %v", id, err)
		}
	}
	other, err := h.store.GetActivePod(ctx, b, 0)
	if err != nil || other.ID != pod.ID {
		t.Fatalf("the two members are not in the same pod: %v / %v", pod.ID, other.ID)
	}
}

func TestRunWindowLeavesTheUnmatchableQueued(t *testing.T) {
	h := newJobHarness(t, Options{})
	h.queue(person{goal: "sort out the spare room"})
	h.queue(person{goal: "clear out the spare room"})
	lonely := h.queue(person{goal: "run a half marathon", category: "movement"})

	result, err := h.job.RunWindow(context.Background())
	if err != nil {
		t.Fatalf("RunWindow: %v", err)
	}
	if result.LeftWaiting != 1 {
		t.Fatalf("got %+v, want one person left waiting", result)
	}
	if _, err := h.store.GetPoolTicket(context.Background(), lonely, 0); err != nil {
		t.Fatalf("the unmatched person should still be queued: %v", err)
	}
	if _, err := h.store.GetActivePod(context.Background(), lonely, 0); err != store.ErrNotFound {
		t.Fatal("the unmatched person must not have been forced into a pod")
	}
}

func TestRunWindowIsIdempotentOnceEveryoneIsMatched(t *testing.T) {
	h := newJobHarness(t, Options{})
	h.queue(person{goal: "sort out the spare room"})
	h.queue(person{goal: "clear out the spare room"})

	if _, err := h.job.RunWindow(context.Background()); err != nil {
		t.Fatalf("first window: %v", err)
	}
	second, err := h.job.RunWindow(context.Background())
	if err != nil {
		t.Fatalf("second window: %v", err)
	}
	if second != (Result{}) {
		t.Fatalf("a second window over an empty pool did something: %+v", second)
	}
}

// The pod's zone anchors its week boundary, so it has to be a zone somebody actually
// lives in — an averaged offset would put the boundary in the middle of someone's evening.
func TestPodTimezoneIsTheEarliestQueuedMembersRealZone(t *testing.T) {
	h := newJobHarness(t, Options{})
	first := h.queue(person{goal: "sort out the spare room", timezone: "Europe/Lisbon"})
	h.clock.Advance(time.Minute)
	h.queue(person{goal: "clear out the spare room", timezone: "Europe/Berlin"})

	if _, err := h.job.RunWindow(context.Background()); err != nil {
		t.Fatalf("RunWindow: %v", err)
	}
	pod, err := h.store.GetActivePod(context.Background(), first, 0)
	if err != nil {
		t.Fatalf("GetActivePod: %v", err)
	}
	if pod.Timezone != "Europe/Lisbon" {
		t.Fatalf("pod timezone = %q, want the earliest-queued member's Europe/Lisbon", pod.Timezone)
	}
	if _, err := time.LoadLocation(pod.Timezone); err != nil {
		t.Fatalf("pod timezone %q is not a real zone: %v", pod.Timezone, err)
	}
}

func TestNextWindowIsTheNextHour(t *testing.T) {
	now := time.Date(2026, 8, 21, 9, 30, 0, 0, time.UTC)
	want := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	if got := NextWindowAt(now); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// On the hour, the next window is the following one, never "now".
	onTheHour := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	if got := NextWindowAt(onTheHour); !got.After(onTheHour) {
		t.Fatalf("NextWindowAt(%v) = %v, which is not in the future", onTheHour, got)
	}
}

func TestOffsetMinutesFollowsDaylightSaving(t *testing.T) {
	winter := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	summer := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	if OffsetMinutes("Europe/Berlin", winter) == OffsetMinutes("Europe/Berlin", summer) {
		t.Fatal("the offset should differ across daylight saving — otherwise the ±3 hour filter quietly shifts twice a year")
	}
	if got := OffsetMinutes("UTC", winter); got != 0 {
		t.Errorf("UTC offset = %d", got)
	}
}

func TestUnknownTimezoneCostsAMatchNotAPlaceInTheQueue(t *testing.T) {
	if got := OffsetMinutes("Not/AZone", time.Now()); got != 0 {
		t.Fatalf("got %d, want 0 — an unparseable zone is treated as UTC, not rejected", got)
	}
}
