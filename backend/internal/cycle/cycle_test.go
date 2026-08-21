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
