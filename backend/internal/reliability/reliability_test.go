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
