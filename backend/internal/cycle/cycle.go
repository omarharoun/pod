// Package cycle answers when a pod's week starts and stops.
//
// CLAUDE.md §5: all timestamps are UTC, but pod cycles are anchored to the pod's shared
// timezone, not to each member's device. Those two rules are why this package exists — the
// boundary is a local-calendar fact that gets stored as an instant.
package cycle

import (
	"fmt"
	"time"

	_ "time/tzdata" // embed the zone database: the service ships as a scratch container
)

// Week runs Monday 00:00 to the following Monday 00:00 in the pod's own timezone.
//
// Monday because a week of effort that resets on Sunday night is a week that ends on the
// evening people have least left. Local midnight because a boundary that lands in the
// middle of someone's Tuesday afternoon is a boundary nobody can feel.
const daysPerWeek = 7

// Bounds is one week, as instants.
type Bounds struct {
	OpensAt  time.Time
	ClosesAt time.Time
}

// Contains reports whether an instant falls inside the week. The open end is inclusive
// and the close end is not, so consecutive weeks tile without overlapping.
func (b Bounds) Contains(at time.Time) bool {
	return !at.Before(b.OpensAt) && at.Before(b.ClosesAt)
}

// Load resolves a pod's timezone. A pod always has a real zone — the matching job takes
// it from a member (ADR-013's neighbour, see matching.podTimezone) — so a failure here is
// data corruption, not a user error.
func Load(zone string) (*time.Location, error) {
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, fmt.Errorf("cycle: unknown timezone %q: %w", zone, err)
	}
	return location, nil
}

// WeekContaining returns the bounds of the week that `at` falls inside, for a pod in
// `location`.
func WeekContaining(at time.Time, location *time.Location) Bounds {
	local := at.In(location)
	daysSinceMonday := (int(local.Weekday()) + 6) % 7

	// time.Date normalises, so a local midnight that does not exist because of a
	// daylight-saving jump resolves forward rather than producing a phantom instant.
	opensLocal := time.Date(
		local.Year(), local.Month(), local.Day()-daysSinceMonday,
		0, 0, 0, 0, location,
	)
	// AddDate on a local time, not a 168-hour addition: across a daylight-saving change a
	// week is 167 or 169 hours, and the boundary must stay at local midnight either way.
	closesLocal := opensLocal.AddDate(0, 0, daysPerWeek)

	return Bounds{OpensAt: opensLocal.UTC(), ClosesAt: closesLocal.UTC()}
}

// Next returns the week after this one.
func Next(b Bounds, location *time.Location) Bounds {
	return WeekContaining(b.ClosesAt.In(location), location)
}

// WeeksBetween counts whole week boundaries crossed between two instants. Used to tell
// how many weeks a pod has been dormant.
func WeeksBetween(from, to time.Time, location *time.Location) int {
	if !to.After(from) {
		return 0
	}
	count := 0
	bounds := WeekContaining(from, location)
	for bounds.ClosesAt.Before(to) || bounds.ClosesAt.Equal(to) {
		count++
		bounds = Next(bounds, location)
	}
	return count
}
