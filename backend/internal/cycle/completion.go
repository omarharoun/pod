package cycle

import "time"

// DefaultLengthWeeks is how long a pod's cycle runs.
//
// CLAUDE.md §7 names M6 "graduation, continue-or-close" and never says how long a cycle
// is; nothing in §2, §5 or §6 derives it. Six is a judgement, recorded in ADR-023, and
// this is the line to change. It is copied onto each pod at formation, so changing it
// never moves the finish line under a pod that is already running.
const DefaultLengthWeeks = 6

// DecideWindow is how long the choice stays open after a cycle finishes.
//
// One week: long enough that someone who checks the app weekly is not timed out by their
// own rhythm, short enough that a pod is not left in limbo. It runs from the moment the
// cycle finishes, not from local midnight — the deadline is a courtesy, not a boundary.
const DecideWindow = 7 * 24 * time.Hour

// ContinueQuorum is how many members must carry on for the pod to run another cycle.
//
// Two, because CLAUDE.md §6 is explicit that a working pair beats a forced trio. That is
// stated about assembly and it is just as true of a second cycle: three people where one
// has moved on is worse than the two who want to be there.
const ContinueQuorum = 2

// Choice is one member's answer, without the member.
//
// The type carries no user id on purpose. Everything downstream of this — the store
// query, the response shape, the screen — works in counts, so that a member id can never
// end up next to a `false` in a response body (CLAUDE.md §2 rule 1).
type Choice struct {
	Continuing int
	Declined   int
	Undecided  int
}

// Answered is how many members have said something either way.
func (c Choice) Answered() int { return c.Continuing + c.Declined }

// Settled reports whether the pod's future is already decided, so the job can act without
// waiting for the deadline.
//
// It is decided early in exactly two cases: enough people have said yes that the answer
// cannot change, or everyone has answered. Notably it is *not* decided by enough people
// saying no — someone who has not answered may still say yes, and treating their silence
// as agreement with the people who declined is the reading that closes pods early.
func (c Choice) Settled() bool {
	return c.Continuing >= ContinueQuorum || c.Undecided == 0
}

// Continues reports whether the pod runs another cycle.
//
// Called at the deadline or once Settled, and it reads silence as "no" only then. That is
// deliberate: closing is the default because holding someone in a pod they did not
// re-choose is the worse of the two mistakes.
func (c Choice) Continues() bool { return c.Continuing >= ContinueQuorum }

// FinishesAt is when the last week of a cycle closes, for a pod that started at `startedAt`.
func FinishesAt(startedAt time.Time, lengthWeeks int, location *time.Location) time.Time {
	bounds := WeekContaining(startedAt, location)
	for i := 1; i < lengthWeeks; i++ {
		bounds = Next(bounds, location)
	}
	return bounds.ClosesAt
}

// WeeksCompleted is how far into its cycle a pod is: how many of its weeks have closed.
//
// It is the position in the cycle, not a measure of anybody. A pod that missed every week
// is on week five of six exactly like a pod that posted every week, and nothing in this
// package distinguishes them — the difference belongs to the shared streak, which is
// pod-level (CLAUDE.md §2 rule 3).
func WeeksCompleted(startedAt, now time.Time, lengthWeeks int, location *time.Location) int {
	elapsed := WeeksBetween(startedAt, now, location)
	if elapsed > lengthWeeks {
		return lengthWeeks
	}
	return elapsed
}
