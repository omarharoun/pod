package cycle

import (
	"testing"
	"time"
)

func mustLoad(t *testing.T, zone string) *time.Location {
	t.Helper()
	location, err := Load(zone)
	if err != nil {
		t.Fatalf("Load(%q): %v", zone, err)
	}
	return location
}

func TestWeekRunsMondayToMondayLocal(t *testing.T) {
	lisbon := mustLoad(t, "Europe/Lisbon")
	// A Thursday afternoon in August; Lisbon is UTC+1 then.
	thursday := time.Date(2026, 8, 20, 15, 30, 0, 0, time.UTC)

	week := WeekContaining(thursday, lisbon)
	opensLocal := week.OpensAt.In(lisbon)
	closesLocal := week.ClosesAt.In(lisbon)

	if opensLocal.Weekday() != time.Monday || closesLocal.Weekday() != time.Monday {
		t.Fatalf("week runs %v to %v", opensLocal.Weekday(), closesLocal.Weekday())
	}
	if opensLocal.Hour() != 0 || opensLocal.Minute() != 0 {
		t.Fatalf("week opens at %v local, want midnight", opensLocal)
	}
	if !week.Contains(thursday) {
		t.Fatal("the week does not contain the instant it was derived from")
	}
}

func TestWeekBoundariesTileWithoutOverlap(t *testing.T) {
	lisbon := mustLoad(t, "Europe/Lisbon")
	week := WeekContaining(time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC), lisbon)
	next := Next(week, lisbon)

	if !next.OpensAt.Equal(week.ClosesAt) {
		t.Fatalf("gap or overlap: this week closes %v, next opens %v", week.ClosesAt, next.OpensAt)
	}
	// The close instant belongs to the next week, not this one.
	if week.Contains(week.ClosesAt) {
		t.Fatal("the closing instant must belong to the next week, or weeks overlap")
	}
	if !next.Contains(next.OpensAt) {
		t.Fatal("the opening instant must belong to its own week")
	}
}

// Two pods an ocean apart have different week boundaries, which is the whole point of
// anchoring to the pod's zone rather than to UTC (CLAUDE.md §5).
func TestBoundaryFollowsThePodsZone(t *testing.T) {
	auckland := mustLoad(t, "Pacific/Auckland")
	losAngeles := mustLoad(t, "America/Los_Angeles")
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

	if WeekContaining(at, auckland).OpensAt.Equal(WeekContaining(at, losAngeles).OpensAt) {
		t.Fatal("two pods on opposite sides of the world opened their week at the same instant")
	}
	for _, zone := range []*time.Location{auckland, losAngeles} {
		week := WeekContaining(at, zone)
		if local := week.OpensAt.In(zone); local.Hour() != 0 || local.Weekday() != time.Monday {
			t.Fatalf("week opens at %v in %v", local, zone)
		}
	}
}

// Across a daylight-saving change a week is 167 or 169 hours. The boundary must stay at
// local midnight regardless, which is why the maths is calendar-based and not +168h.
func TestWeekLengthAbsorbsDaylightSaving(t *testing.T) {
	berlin := mustLoad(t, "Europe/Berlin")
	// The last Sunday of March 2026: clocks go forward.
	inTheSpringWeek := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)

	week := WeekContaining(inTheSpringWeek, berlin)
	hours := week.ClosesAt.Sub(week.OpensAt).Hours()
	if hours == 168 {
		t.Fatal("expected a short week across the spring change; the maths looks like +168h")
	}
	if hours < 160 || hours > 176 {
		t.Fatalf("week is %v hours, which is not a week at all", hours)
	}
	if local := week.ClosesAt.In(berlin); local.Hour() != 0 || local.Weekday() != time.Monday {
		t.Fatalf("the boundary drifted to %v", local)
	}
}

func TestMondayMidnightBelongsToItsOwnWeek(t *testing.T) {
	lisbon := mustLoad(t, "Europe/Lisbon")
	// Exactly midnight local on a Monday.
	monday := time.Date(2026, 8, 17, 0, 0, 0, 0, lisbon)

	week := WeekContaining(monday.UTC(), lisbon)
	if !week.OpensAt.Equal(monday.UTC()) {
		t.Fatalf("week opens %v, want %v", week.OpensAt, monday.UTC())
	}
}

func TestSundayNightIsStillThisWeek(t *testing.T) {
	lisbon := mustLoad(t, "Europe/Lisbon")
	sundayLate := time.Date(2026, 8, 23, 23, 59, 0, 0, lisbon)

	week := WeekContaining(sundayLate.UTC(), lisbon)
	if !week.Contains(sundayLate.UTC()) {
		t.Fatal("Sunday night fell outside its own week")
	}
	if local := week.OpensAt.In(lisbon); local.Day() != 17 {
		t.Fatalf("week opened on the %d, want the 17th", local.Day())
	}
}

func TestWeeksBetween(t *testing.T) {
	lisbon := mustLoad(t, "Europe/Lisbon")
	start := time.Date(2026, 8, 17, 0, 0, 0, 0, lisbon).UTC()

	cases := []struct {
		name string
		to   time.Time
		want int
	}{
		{"same instant", start, 0},
		{"mid week", start.Add(3 * 24 * time.Hour), 0},
		{"one boundary", start.Add(7 * 24 * time.Hour), 1},
		{"two boundaries", start.Add(14 * 24 * time.Hour), 2},
		{"backwards", start.Add(-time.Hour), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WeeksBetween(start, tc.to, lisbon); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestLoadRejectsAnUnknownZone(t *testing.T) {
	if _, err := Load("Mars/Olympus"); err == nil {
		t.Fatal("expected an error — a pod's zone is written by us, so a bad one is corruption")
	}
}

// MARK: - Cycle completion (M6)

func TestACycleFinishesAtTheEndOfItsLastWeek(t *testing.T) {
	lisbon, err := Load("Europe/Lisbon")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// A Wednesday: the cycle's first week is the one containing it, not one starting now.
	started := time.Date(2026, 8, 19, 14, 0, 0, 0, time.UTC)
	first := WeekContaining(started, lisbon)

	finish := FinishesAt(started, 6, lisbon)

	weeks := WeeksBetween(first.OpensAt, finish, lisbon)
	if weeks != 6 {
		t.Errorf("a six-week cycle spanned %d weeks", weeks)
	}
	if !finish.After(started) {
		t.Errorf("the cycle finishes at %v, before it started", finish)
	}
}

// A cycle that crosses a daylight-saving change is still six weeks, because the boundary
// is local midnight rather than a multiple of 168 hours.
func TestACycleAcrossADaylightSavingChangeIsStillSixWeeks(t *testing.T) {
	lisbon, err := Load("Europe/Lisbon")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Late September: the six weeks run through the October change.
	started := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	finish := FinishesAt(started, 6, lisbon)

	local := finish.In(lisbon)
	if local.Hour() != 0 || local.Minute() != 0 {
		t.Errorf("the cycle finished at %v local, not local midnight", local)
	}
	if local.Weekday() != time.Monday {
		t.Errorf("the cycle finished on a %v, not a Monday", local.Weekday())
	}
}

func TestWeeksCompletedNeverExceedsTheCycle(t *testing.T) {
	lisbon, _ := Load("Europe/Lisbon")
	started := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	longAfter := started.AddDate(0, 0, 200)

	if got := WeeksCompleted(started, longAfter, 6, lisbon); got != 6 {
		t.Errorf("a long-dormant pod reported %d weeks of a 6-week cycle", got)
	}
	if got := WeeksCompleted(started, started, 6, lisbon); got != 0 {
		t.Errorf("a pod on its first day reported %d weeks completed", got)
	}
}

// The quorum is two, and the reason matters: CLAUDE.md §6 says a working pair beats a
// forced trio, which is as true of a second cycle as of assembly.
func TestTwoContinuingKeepsThePodAndOneDoesNot(t *testing.T) {
	cases := []struct {
		name   string
		choice Choice
		want   bool
	}{
		{"all three", Choice{Continuing: 3}, true},
		{"two of three", Choice{Continuing: 2, Declined: 1}, true},
		{"one of three", Choice{Continuing: 1, Declined: 2}, false},
		{"two of two", Choice{Continuing: 2}, true},
		{"one of two", Choice{Continuing: 1, Declined: 1}, false},
		{"nobody answered", Choice{Undecided: 3}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.choice.Continues(); got != tc.want {
				t.Errorf("Continues() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Silence is not a no until the deadline. Someone who has not answered may still say yes,
// and reading their silence as agreement with the people who declined is what closes pods
// out from under the person who was going to carry on.
func TestSilenceDoesNotSettleTheChoiceEarly(t *testing.T) {
	if (Choice{Declined: 2, Undecided: 1}).Settled() {
		t.Error("two declines settled the choice while someone had not answered")
	}
	if !(Choice{Continuing: 2, Undecided: 1}).Settled() {
		t.Error("two continuing did not settle it — the answer cannot change")
	}
	if !(Choice{Continuing: 1, Declined: 2}).Settled() {
		t.Error("everyone answered and it was not settled")
	}
}
