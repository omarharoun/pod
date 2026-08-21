// Package reliability holds the score that decides who gets matched with whom.
//
// The score is never returned by the API, never logged next to a user id, and never
// rendered. CLAUDE.md §6: "Never expose the raw score to users. Never." Everything in
// this package is unexported at the wire level for that reason — the only thing that
// crosses a package boundary is a Band, and only into matching.
package reliability

import "fmt"

// Score is a reliability estimate in [0, 1].
//
// It is a distinct type rather than a float64 so it cannot be handed to a JSON encoder by
// accident, and it deliberately has no MarshalJSON, no String, and no exported field.
type Score float64

// Band is the coarse grouping matching works in. CLAUDE.md §6: low 0–0.3, medium
// 0.3–0.7, high 0.7–1.0; match within band, one band apart at most.
type Band int

const (
	BandLow Band = iota
	BandMedium
	BandHigh
)

// SelfReport is the answer to "how often do you keep commitments to yourself", asked once
// at onboarding. It is the only input a brand new account has.
type SelfReport string

const (
	SelfReportRarely    SelfReport = "rarely"
	SelfReportSometimes SelfReport = "sometimes"
	SelfReportUsually   SelfReport = "usually"
)

// coldStartBase is where someone with no history starts: the middle of the medium band,
// so a new account is matchable with almost anyone.
const coldStartBase Score = 0.5

// coldStartAdjustment is the ±0.1 CLAUDE.md §6 allows the self-report to move the
// starting score. It is deliberately small: it is a guess about oneself, made before
// using the product, and it must not be able to move anyone out of the medium band.
const coldStartAdjustment Score = 0.1

// ColdStart returns the starting score for an account with no week history.
//
// Every possible answer lands inside the medium band by construction (0.4, 0.5, 0.6),
// which is the point — a self-assessment should nudge who you are matched with, not
// sort you into a tier before you have done anything.
func ColdStart(report SelfReport) Score {
	switch report {
	case SelfReportRarely:
		return coldStartBase - coldStartAdjustment
	case SelfReportUsually:
		return coldStartBase + coldStartAdjustment
	case SelfReportSometimes:
		return coldStartBase
	default:
		// An unrecognised answer is treated as no answer rather than as a bad one.
		return coldStartBase
	}
}

// Band buckets a score. The boundaries are inclusive at the bottom, so 0.3 is medium and
// 0.7 is high — a score sitting exactly on a boundary is given the benefit of it.
func (s Score) Band() Band {
	switch {
	case s < 0.3:
		return BandLow
	case s < 0.7:
		return BandMedium
	default:
		return BandHigh
	}
}

// Distance is how many bands apart two people are.
func (b Band) Distance(other Band) int {
	d := int(b) - int(other)
	if d < 0 {
		return -d
	}
	return d
}

// Compatible reports whether two bands may be podded together at all. CLAUDE.md §6 caps
// this at one band: a low and a high in the same pod is the pairing where one member
// quietly carries the week, and that is what the cap exists to prevent.
func (b Band) Compatible(other Band) bool {
	return b.Distance(other) <= 1
}

func (b Band) String() string {
	switch b {
	case BandLow:
		return "low"
	case BandMedium:
		return "medium"
	case BandHigh:
		return "high"
	default:
		return fmt.Sprintf("band(%d)", int(b))
	}
}

// Clamp keeps a score inside [0, 1]. Used by whatever computes scores from history; the
// full formula in CLAUDE.md §6 needs week outcomes and lands with M5, not here.
func Clamp(s Score) Score {
	switch {
	case s < 0:
		return 0
	case s > 1:
		return 1
	default:
		return s
	}
}

// Outcome is one week in someone's history, newest first when passed to Compute.
type Outcome struct {
	// Posted is whether they put a proof in that week.
	Posted bool
	// PodCompleted is whether the pod as a whole completed it.
	PodCompleted bool
}

// The signal weights from CLAUDE.md §6. They sum to 1 and changing one is an escalation
// under §8, so they are constants on lines that say so.
const (
	WeightCompletionRate = 0.60
	WeightRecency        = 0.25
	WeightPodImpact      = 0.15
)

// Window is the rolling history the §6 formula is computed over.
const Window = 8

// Replay is how many weeks Compute folds forward. It is not the same as Window and it
// deliberately covers a long life rather than a recent slice: the fold's seed has to sit
// at a *fixed* point in someone's history, because a seed that moves forward one week at
// a time makes the score jump by up to 0.24 in a week and lets a first bad week drop a
// band. See ADR-022 — the numbers there are measured, not estimated.
//
// Someone with more history than this keeps a seed that is years old, which is as close
// to fixed as a bounded query gets.
const Replay = 520

// recencyMultiplier is how much more the last two weeks count. CLAUDE.md §6: "last 2
// weeks counted 2×".
const (
	recentWeeks       = 2
	recencyMultiplier = 2.0
)

// missDampening implements "increment on completion must be larger than decrement on
// miss" (CLAUDE.md §6). A week that pulls the score down moves it half as far as a week
// that pushes it up.
//
// This is not a nicety. It is the arithmetic of §2 rule 2 — recovery has to be cheaper
// than punishment in every scoring path — and it is why someone can come back from a bad
// month faster than they fell into one.
const missDampening = 0.5

// maxWeeklyMovement caps how far one week can move a score in either direction.
const maxWeeklyMovement = 0.1

// Compute returns someone's reliability from their recent weeks, newest first.
//
// The score is derived from history rather than incremented in place, so it is
// backfill-safe (CLAUDE.md §5) and there is no stored number to drift.
//
// It is a fold, not a single adjustment: the whole history is replayed forward from the
// cold start, one week at a time, applying the §6 asymmetry and cap at every step. That
// matters more than it looks. An earlier version compared the newest week against
// raw(older weeks) — the undamped formula over the shorter history — and called that
// "the score before this week". It was not. The score before this week was itself
// damped, so measuring against the raw value re-opened the whole gap every week: the
// per-week cap and the band floor were computed from a number the person never had, and
// neither guarantee held on the sequence they actually lived. Weeks where posting a
// proof *lowered* the score were reachable, which is §2 rule 2 exactly backwards.
func Compute(outcomes []Outcome, report SelfReport) Score {
	// Only the rolling window is replayed. CLAUDE.md §6 scopes the formula to 8 weeks,
	// and a fold that replayed everything would quietly make the score depend on a bad
	// month two years ago — recovery is meant to be cheaper than punishment (§2 rule 2),
	// and a history that never drops out is punishment with no end.
	score := ColdStart(report)
	// heldAtFloor is whether last week's downward move was stopped by a band floor. The
	// floor holds for exactly one week: a single bad week can never drop a band, two
	// consecutive ones can. Without the memory the guard would have to key off position
	// instead, and someone who arrives exactly on a boundary by doing well would lose a
	// band to their first bad week — the opposite of what the cap is for.
	heldAtFloor := false
	// Oldest first: outcomes are newest-first, so walk the slice backwards.
	for i := len(outcomes) - 1; i >= 0; i-- {
		var held bool
		score, held = step(score, raw(outcomes[i:]), heldAtFloor)
		// Posting clears the protection. A week someone showed up for is not one of the
		// "consecutive bad weeks" the floor is allowed to give way to — otherwise a week
		// they posted could spend the protection and leave the next miss free to drop a
		// band, which is the case this rule exists to prevent.
		heldAtFloor = held && !outcomes[i].Posted
	}
	return score
}

// step moves a score one week towards what the history now says, under the two rules §6
// states about movement. It reports whether a band floor stopped the move, which is what
// lets the floor hold for one week and no longer.
func step(previous, target Score, heldLastWeek bool) (Score, bool) {
	delta := target - previous
	// "Increment on completion must be larger than decrement on miss" (CLAUDE.md §6).
	if delta < 0 {
		delta *= missDampening
	}
	if delta > maxWeeklyMovement {
		delta = maxWeeklyMovement
	}
	if delta < -maxWeeklyMovement {
		delta = -maxWeeklyMovement
	}

	candidate := Clamp(previous + delta)

	// "Cap per-week movement so a single bad week can't drop a band" (CLAUDE.md §6). A
	// downward move may take someone to the floor of their band but not through it —
	// unless it was already stopped there last week, in which case the band would be a
	// trap rather than a cap.
	if candidate < previous && candidate < bandFloor(previous.Band()) {
		if !heldLastWeek {
			return bandFloor(previous.Band()), true
		}
		return candidate, false
	}
	return candidate, false
}

// raw is the §6 formula with no smoothing.
func raw(outcomes []Outcome) Score {
	return Score(
		WeightCompletionRate*completionRate(outcomes) +
			WeightRecency*recencyWeight(outcomes) +
			WeightPodImpact*podImpact(outcomes),
	)
}

func completionRate(outcomes []Outcome) float64 {
	if len(outcomes) == 0 {
		return 0
	}
	posted := 0
	for _, outcome := range outcomes {
		if outcome.Posted {
			posted++
		}
	}
	return float64(posted) / float64(len(outcomes))
}

// recencyWeight is the same rate with the two most recent weeks counted double.
func recencyWeight(outcomes []Outcome) float64 {
	var weighted, total float64
	for i, outcome := range outcomes {
		weight := 1.0
		if i < recentWeeks {
			weight = recencyMultiplier
		}
		total += weight
		if outcome.Posted {
			weighted += weight
		}
	}
	if total == 0 {
		return 0
	}
	return weighted / total
}

// podImpact answers "did their presence correlate with pod completion" (CLAUDE.md §6),
// which the brief states without defining. See ADR-017.
//
// It is the difference between how often the pod completed in weeks they posted and how
// often it completed in weeks they did not, mapped from [-1, 1] onto [0, 1]. Someone with
// no weeks on one side of that comparison scores the neutral 0.5 — there is nothing to
// compare, and guessing would be worse than abstaining.
func podImpact(outcomes []Outcome) float64 {
	var withThem, withoutThem, completedWith, completedWithout int
	for _, outcome := range outcomes {
		if outcome.Posted {
			withThem++
			if outcome.PodCompleted {
				completedWith++
			}
			continue
		}
		withoutThem++
		if outcome.PodCompleted {
			completedWithout++
		}
	}
	if withThem == 0 || withoutThem == 0 {
		return 0.5
	}
	difference := float64(completedWith)/float64(withThem) - float64(completedWithout)/float64(withoutThem)
	return (difference + 1) / 2
}

func bandFloor(band Band) Score {
	switch band {
	case BandHigh:
		return 0.7
	case BandMedium:
		return 0.3
	default:
		return 0
	}
}
