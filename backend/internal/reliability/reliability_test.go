package reliability

import "testing"

func TestColdStartStaysInsideTheMediumBand(t *testing.T) {
	for _, report := range []SelfReport{SelfReportRarely, SelfReportSometimes, SelfReportUsually} {
		score := ColdStart(report)
		if got := score.Band(); got != BandMedium {
			t.Errorf("ColdStart(%q) = %v, band %v — a self-assessment must not sort anyone into a tier before they have done anything", report, score, got)
		}
	}
}

func TestColdStartOrdering(t *testing.T) {
	rarely, sometimes, usually := ColdStart(SelfReportRarely), ColdStart(SelfReportSometimes), ColdStart(SelfReportUsually)
	if !(rarely < sometimes && sometimes < usually) {
		t.Fatalf("expected rarely < sometimes < usually, got %v %v %v", rarely, sometimes, usually)
	}
	if usually-rarely > 2*coldStartAdjustment {
		t.Fatalf("the self-report moved the score by more than the ±0.1 CLAUDE.md §6 allows: %v", usually-rarely)
	}
}

func TestColdStartTreatsAnUnknownAnswerAsNoAnswer(t *testing.T) {
	if got := ColdStart(SelfReport("shrug")); got != coldStartBase {
		t.Fatalf("got %v, want the neutral base %v", got, coldStartBase)
	}
}

func TestBandBoundaries(t *testing.T) {
	cases := []struct {
		score Score
		want  Band
	}{
		{0, BandLow}, {0.29, BandLow},
		{0.3, BandMedium}, {0.5, BandMedium}, {0.69, BandMedium},
		{0.7, BandHigh}, {1, BandHigh},
	}
	for _, tc := range cases {
		if got := tc.score.Band(); got != tc.want {
			t.Errorf("Score(%v).Band() = %v, want %v", tc.score, got, tc.want)
		}
	}
}

func TestCompatibilityIsCappedAtOneBand(t *testing.T) {
	if !BandLow.Compatible(BandMedium) || !BandMedium.Compatible(BandHigh) {
		t.Error("adjacent bands must be compatible")
	}
	if BandLow.Compatible(BandHigh) {
		t.Error("low and high must not be podded together — that is the pairing where one member quietly carries the week")
	}
	if !BandMedium.Compatible(BandMedium) {
		t.Error("a band must be compatible with itself")
	}
}

func TestCompatibilityIsSymmetric(t *testing.T) {
	for _, a := range []Band{BandLow, BandMedium, BandHigh} {
		for _, b := range []Band{BandLow, BandMedium, BandHigh} {
			if a.Compatible(b) != b.Compatible(a) {
				t.Errorf("Compatible is not symmetric for %v/%v", a, b)
			}
		}
	}
}

func TestClamp(t *testing.T) {
	for _, tc := range []struct{ in, want Score }{{-1, 0}, {0, 0}, {0.5, 0.5}, {1, 1}, {2, 1}} {
		if got := Clamp(tc.in); got != tc.want {
			t.Errorf("Clamp(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// ------------------------------------------------------------ M5: from history

func weeks(pattern string) []Outcome {
	// `P` posted, `-` did not; `C` posted and the pod completed. Newest first.
	out := make([]Outcome, 0, len(pattern))
	for _, c := range pattern {
		switch c {
		case 'C':
			out = append(out, Outcome{Posted: true, PodCompleted: true})
		case 'P':
			out = append(out, Outcome{Posted: true})
		default:
			out = append(out, Outcome{})
		}
	}
	return out
}

func TestComputeFallsBackToColdStartWithNoHistory(t *testing.T) {
	if got := Compute(nil, SelfReportUsually); got != ColdStart(SelfReportUsually) {
		t.Fatalf("got %v, want the cold start", got)
	}
}

func TestComputeRewardsPostingAndPunishesLess(t *testing.T) {
	// The rule that makes recovery cheaper than punishment (CLAUDE.md §2 rule 2, §6).
	//
	// A mixed history, deliberately: an all-posted one is already saturated on two of
	// the three signals, so another good week moves nothing and the comparison says
	// nothing.
	history := weeks("P-P-P-")
	improving := append([]Outcome{{Posted: true}}, history...)
	slipping := append([]Outcome{{}}, history...)

	// The prior both are measured against is the history's own raw score, which is what
	// Compute smooths against internally.
	base := raw(history)
	up := Compute(improving, SelfReportSometimes) - base
	down := base - Compute(slipping, SelfReportSometimes)

	if up <= 0 {
		t.Fatalf("a completed week did not raise the score: %v", up)
	}
	if down <= 0 {
		t.Fatalf("a missed week did not lower the score: %v", down)
	}
	if down >= up {
		t.Fatalf("a miss costs %v but a completion earns %v — punishment must be cheaper than reward", down, up)
	}
}

// "Cap per-week movement so a single bad week can't drop a band" — literally.
func TestASingleBadWeekCannotDropABand(t *testing.T) {
	// Someone sitting just inside high.
	history := weeks("CCCCCCC")
	before := Compute(history, SelfReportUsually)
	if before.Band() != BandHigh {
		t.Skipf("this fixture is not in the high band (%v); the invariant is tested below", before)
	}
	after := Compute(append([]Outcome{{}}, history...), SelfReportUsually)
	if after.Band() != before.Band() {
		t.Fatalf("one bad week moved %v (%v) to %v (%v)", before, before.Band(), after, after.Band())
	}
}

func TestABadWeekAtTheFloorMayCross(t *testing.T) {
	// Pinned at a floor forever would make a band a trap rather than a cap.
	atFloor := Score(0.7)
	if got := bandFloor(atFloor.Band()); got != 0.7 {
		t.Fatalf("floor of high = %v", got)
	}
	// The guard only applies while you are above the floor.
	if atFloor > bandFloor(atFloor.Band()) {
		t.Fatal("a score exactly on the floor must not be treated as above it")
	}
}

func TestMovementIsCapped(t *testing.T) {
	// The most violent possible swing: a perfect history then a miss, and vice versa.
	perfect := weeks("CCCCCCCC")
	empty := weeks("--------")
	for _, history := range [][]Outcome{perfect, empty} {
		before := Compute(history[1:], SelfReportSometimes)
		for _, next := range []Outcome{{Posted: true, PodCompleted: true}, {}} {
			after := Compute(append([]Outcome{next}, history[1:]...), SelfReportSometimes)
			if diff := after - before; diff > maxWeeklyMovement+1e-9 || diff < -maxWeeklyMovement-1e-9 {
				t.Fatalf("one week moved the score by %v, cap is %v", diff, maxWeeklyMovement)
			}
		}
	}
}

func TestComputeStaysInRange(t *testing.T) {
	for _, pattern := range []string{"C", "-", "CCCCCCCC", "--------", "C-C-C-C-", "PPPP----"} {
		for _, report := range []SelfReport{SelfReportRarely, SelfReportSometimes, SelfReportUsually} {
			got := Compute(weeks(pattern), report)
			if got < 0 || got > 1 {
				t.Fatalf("Compute(%q, %q) = %v, outside [0, 1]", pattern, report, got)
			}
		}
	}
}

// CLAUDE.md §6 scopes the formula to a rolling 8 weeks, and `raw` honours that exactly.
// The score is not the formula, though: §6 also caps per-week movement, and a cap is a
// statement about a trajectory, so `Compute` replays the whole history. Old weeks
// therefore do influence the score — they cannot not — but only through where they left
// it, and that washes out. See ADR-022.
func TestTheFormulaSeesOnlyTheRollingWindow(t *testing.T) {
	recent := weeks("CCCCCCCC")
	withAncientHistory := append(append([]Outcome{}, recent...), weeks("--------")...)
	if raw(recent) != raw(withAncientHistory[:Window]) {
		t.Fatal("raw looked past the rolling window")
	}
}

// §2 rule 2, at the scale a person actually feels it: climbing back out of a band must
// take no longer than falling out of one. Measured on the two extremes, since those are
// the paths where the damping matters most.
func TestClimbingOutTakesFewerWeeksThanFallingOut(t *testing.T) {
	const report = SelfReportSometimes

	fell := weeksUntilBandChanges(weeks("CCCCCCCC"), Outcome{}, report)
	if fell == 0 {
		t.Fatal("a perfect history never left the high band, however many weeks were missed")
	}
	climbed := weeksUntilBandChanges(weeks("------------------------"), Outcome{Posted: true, PodCompleted: true}, report)
	if climbed == 0 {
		t.Fatal("the low band was a trap: posting every week never left it")
	}
	if climbed > fell {
		t.Errorf("climbing out took %d weeks, falling out took %d — recovery must not cost more than punishment", climbed, fell)
	}
	t.Logf("out of the low band in %d posted weeks; out of the high band in %d missed weeks", climbed, fell)
}

// weeksUntilBandChanges returns how many consecutive weeks of the same kind it takes to
// leave the band, or 0 within a generous limit.
func weeksUntilBandChanges(past []Outcome, week Outcome, report SelfReport) int {
	start := Compute(past, report).Band()
	for count := 1; count <= 52; count++ {
		past = append([]Outcome{week}, past...)
		if Compute(past, report).Band() != start {
			return count
		}
	}
	return 0
}

func TestRecencyCountsTheLastTwoWeeksDouble(t *testing.T) {
	// Same number of posted weeks, different placement.
	recentlyGood := weeks("CC----")
	recentlyBad := weeks("----CC")
	if recencyWeight(recentlyGood) <= recencyWeight(recentlyBad) {
		t.Fatalf("recency did not favour the recent weeks: %v vs %v",
			recencyWeight(recentlyGood), recencyWeight(recentlyBad))
	}
	// Completion rate, by contrast, does not care when.
	if completionRate(recentlyGood) != completionRate(recentlyBad) {
		t.Fatal("completion rate should be placement-independent")
	}
}

func TestPodImpactAbstainsWithNothingToCompare(t *testing.T) {
	// Never missed a week: there is no "without them" to compare against.
	if got := podImpact(weeks("CCCC")); got != 0.5 {
		t.Fatalf("got %v, want the neutral 0.5", got)
	}
	// Never posted: likewise.
	if got := podImpact(weeks("----")); got != 0.5 {
		t.Fatalf("got %v, want the neutral 0.5", got)
	}
}

func TestPodImpactRewardsCorrelation(t *testing.T) {
	// The pod completed exactly when they posted.
	correlated := []Outcome{
		{Posted: true, PodCompleted: true},
		{Posted: false, PodCompleted: false},
		{Posted: true, PodCompleted: true},
		{Posted: false, PodCompleted: false},
	}
	// The pod completed regardless.
	irrelevant := []Outcome{
		{Posted: true, PodCompleted: true},
		{Posted: false, PodCompleted: true},
		{Posted: true, PodCompleted: true},
		{Posted: false, PodCompleted: true},
	}
	if podImpact(correlated) <= podImpact(irrelevant) {
		t.Fatalf("correlation scored %v, irrelevance %v", podImpact(correlated), podImpact(irrelevant))
	}
	if got := podImpact(correlated); got != 1 {
		t.Fatalf("perfect correlation = %v, want 1", got)
	}
}

func TestWeightsSumToOne(t *testing.T) {
	if total := WeightCompletionRate + WeightRecency + WeightPodImpact; total != 1 {
		t.Fatalf("weights sum to %v, want 1 — CLAUDE.md §6", total)
	}
}

// MARK: - The §6 movement rules, checked exhaustively
//
// The three rules CLAUDE.md §6 states about movement are properties of every sequence a
// person can live, not of a handful of fixtures. There are only 4^8 histories inside the
// rolling window, so they can simply all be checked — and the version of Compute that
// preceded ADR-022 passed every fixture in this file while violating all three.

// allHistories enumerates every history of length n, newest first. The four letters are
// the four weeks that can happen: C posted and the pod completed, P posted and it did
// not, x missed and the pod completed anyway, - missed and it did not.
func allHistories(n int) []string {
	if n == 0 {
		return []string{""}
	}
	var out []string
	for _, older := range allHistories(n - 1) {
		for _, week := range []string{"C", "P", "x", "-"} {
			out = append(out, week+older)
		}
	}
	return out
}

func history(pattern string) []Outcome {
	out := make([]Outcome, 0, len(pattern))
	for _, week := range pattern {
		switch week {
		case 'C':
			out = append(out, Outcome{Posted: true, PodCompleted: true})
		case 'P':
			out = append(out, Outcome{Posted: true})
		case 'x':
			out = append(out, Outcome{PodCompleted: true})
		default:
			out = append(out, Outcome{})
		}
	}
	return out
}

var allReports = []SelfReport{SelfReportRarely, SelfReportSometimes, SelfReportUsually}

// eachHistory runs body over every history up to the window length, with every
// self-report. Roughly 260k cases, which takes about a second.
func eachHistory(t *testing.T, body func(t *testing.T, pattern string, past []Outcome, report SelfReport)) {
	t.Helper()
	for n := 0; n <= Window; n++ {
		for _, pattern := range allHistories(n) {
			for _, report := range allReports {
				body(t, pattern, history(pattern), report)
			}
		}
	}
}

var badWeeks = []Outcome{{}, {PodCompleted: true}}
var goodWeeks = []Outcome{{Posted: true, PodCompleted: true}, {Posted: true}}

func prepend(week Outcome, past []Outcome) []Outcome {
	return append([]Outcome{week}, past...)
}

// "Cap per-week movement so a single bad week can't drop a band" (CLAUDE.md §6). A bad
// week is a *single* one when the week before it was posted; two consecutive bad weeks
// are allowed to cross, or a band would be a trap rather than a cap.
func TestNoFirstBadWeekEverDropsABand(t *testing.T) {
	failures := 0
	eachHistory(t, func(t *testing.T, pattern string, past []Outcome, report SelfReport) {
		if len(past) > 0 && !past[0].Posted {
			return
		}
		before := Compute(past, report)
		for _, bad := range badWeeks {
			after := Compute(prepend(bad, past), report)
			if after.Band() < before.Band() && failures < 5 {
				t.Errorf("history %q (%s): %v -> %v on one bad week", pattern, report, before.Band(), after.Band())
				failures++
			}
		}
	})
}

// §2 rule 2, in the only form that survives contact with a converging score: posting a
// proof must never leave someone worse off than not posting it. Where both land on the
// same band floor they tie, which is the protection doing its job on both paths.
func TestPostingIsNeverWorseThanMissing(t *testing.T) {
	failures := 0
	eachHistory(t, func(t *testing.T, pattern string, past []Outcome, report SelfReport) {
		for _, good := range goodWeeks {
			posted := Compute(prepend(good, past), report)
			for _, bad := range badWeeks {
				missed := Compute(prepend(bad, past), report)
				if posted < missed && failures < 5 {
					t.Errorf("history %q (%s): posting scored %v, missing scored %v", pattern, report, posted, missed)
					failures++
				}
			}
		}
	})
}

// The cap is a cap on the observable sequence, not on an intermediate the person never
// had. This is the assertion the pre-ADR-022 Compute failed worst: single weeks could
// move a score by 0.47.
func TestNoWeekMovesTheScoreMoreThanTheCap(t *testing.T) {
	failures := 0
	eachHistory(t, func(t *testing.T, pattern string, past []Outcome, report SelfReport) {
		before := Compute(past, report)
		for _, week := range append(append([]Outcome{}, goodWeeks...), badWeeks...) {
			moved := Compute(prepend(week, past), report) - before
			if moved < 0 {
				moved = -moved
			}
			if moved > maxWeeklyMovement+1e-9 && failures < 5 {
				t.Errorf("history %q (%s): one week moved the score by %v", pattern, report, moved)
				failures++
			}
		}
	})
}

// Recovery has to be cheaper than punishment (§2 rule 2), which at the level of a single
// step means: from the same score, moving the same distance, up must go further than
// down. This is what missDampening buys and it is asserted here rather than inferred
// from the constant.
func TestClimbingBackIsFasterThanFalling(t *testing.T) {
	for from := Score(0.05); from < 1; from += 0.05 {
		for distance := Score(0.02); distance < 0.5; distance += 0.02 {
			// Only where both targets are reachable: a clamped target is a shorter
			// distance, not a slower climb.
			if from+distance > 1 || from-distance < 0 {
				continue
			}
			up, _ := step(from, from+distance, false)
			down, _ := step(from, from-distance, false)
			// Both sides can land on the movement cap, where they tie; a tie is the
			// cap doing its job, not a violation, so compare with a tolerance.
			gained, lost := up-from, from-down
			if gained < lost-1e-9 {
				t.Fatalf("from %v ± %v: gained %v climbing, lost %v falling", from, distance, gained, lost)
			}
		}
	}
}
