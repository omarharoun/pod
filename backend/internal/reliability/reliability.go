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
