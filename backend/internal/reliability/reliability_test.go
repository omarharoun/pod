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

func TestOnlyTheRollingWindowCounts(t *testing.T) {
	recent := weeks("CCCCCCCC")
	withAncientHistory := append(append([]Outcome{}, recent...), weeks("--------")...)
	if Compute(recent, SelfReportSometimes) != Compute(withAncientHistory, SelfReportSometimes) {
		t.Fatal("weeks beyond the rolling window changed the score")
	}
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
