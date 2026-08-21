package matching

import (
	"context"
	"math"
	"testing"

	"github.com/omarharoun/pod/backend/internal/embed"
	"github.com/omarharoun/pod/backend/internal/reliability"
)

func vector(t *testing.T, text string) embed.Vector {
	t.Helper()
	vectors, err := embed.Hashing{}.Embed(context.Background(), []string{text})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	return vectors[0]
}

// candidate builds a baseline person; each test perturbs the one field it is about.
func candidate(t *testing.T, id string, mutate ...func(*Candidate)) Candidate {
	t.Helper()
	c := Candidate{
		UserID:                id,
		GoalCategory:          "home",
		Intensity:             3,
		TimezoneOffsetMinutes: 0,
		Embedding:             vector(t, "sort out the spare room"),
		Reliability:           reliability.ColdStart(reliability.SelfReportSometimes),
		Availability:          []Window{{DayOfWeek: 5, StartMinute: 600, EndMinute: 720}},
		TenureWeeks:           0,
	}
	for _, m := range mutate {
		m(&c)
	}
	return c
}

// ---------------------------------------------------------------- hard filters

func TestWeightsSumToOne(t *testing.T) {
	total := WeightGoalSimilarity + WeightReliability + WeightAvailability + WeightTenureMix
	if math.Abs(total-1) > 1e-9 {
		t.Fatalf("weights sum to %v, want 1 — CLAUDE.md §6", total)
	}
}

func TestEligibleRequiresTheSameCategory(t *testing.T) {
	a := candidate(t, "a")
	b := candidate(t, "b", func(c *Candidate) { c.GoalCategory = "movement" })
	if Eligible(a, b) {
		t.Fatal("a pod is always same-category")
	}
}

func TestEligibleAllowsThreeHoursOfTimezoneAndNoMore(t *testing.T) {
	a := candidate(t, "a")
	for _, tc := range []struct {
		offset int
		want   bool
	}{{0, true}, {180, true}, {-180, true}, {181, false}, {-181, false}, {600, false}} {
		b := candidate(t, "b", func(c *Candidate) { c.TimezoneOffsetMinutes = tc.offset })
		if got := Eligible(a, b); got != tc.want {
			t.Errorf("offset %d minutes: Eligible = %v, want %v", tc.offset, got, tc.want)
		}
	}
}

func TestEligibleAllowsOneIntensityStepAndNoMore(t *testing.T) {
	for _, tc := range []struct {
		mine, theirs int
		want         bool
	}{
		{3, 3, true}, {3, 2, true}, {3, 4, true},
		{3, 1, false}, {3, 5, false},
		{1, 2, true}, {1, 3, false},
	} {
		a := candidate(t, "a", func(c *Candidate) { c.Intensity = tc.mine })
		b := candidate(t, "b", func(c *Candidate) { c.Intensity = tc.theirs })
		if got := Eligible(a, b); got != tc.want {
			t.Errorf("intensity %d vs %d: Eligible = %v, want %v", tc.mine, tc.theirs, got, tc.want)
		}
	}
}

// The pairing the ±1 filter exists to prevent: fifteen minutes a week with five hours.
func TestEligibleKeepsTheSmallestAndLargestApart(t *testing.T) {
	tiny := candidate(t, "tiny", func(c *Candidate) { c.Intensity = 1 })
	huge := candidate(t, "huge", func(c *Candidate) { c.Intensity = 5 })
	if Eligible(tiny, huge) {
		t.Fatal("a few minutes a week must never be podded with several hours")
	}
}

func TestEligibleRejectsTwoBandsApart(t *testing.T) {
	low := candidate(t, "low", func(c *Candidate) { c.Reliability = 0.1 })
	high := candidate(t, "high", func(c *Candidate) { c.Reliability = 0.9 })
	medium := candidate(t, "medium", func(c *Candidate) { c.Reliability = 0.5 })

	if Eligible(low, high) {
		t.Error("low and high are two bands apart — CLAUDE.md §6 caps this at one")
	}
	if !Eligible(low, medium) || !Eligible(medium, high) {
		t.Error("one band apart must be allowed")
	}
}

func TestEligibleRejectsSelf(t *testing.T) {
	a := candidate(t, "a")
	if Eligible(a, a) {
		t.Fatal("nobody is podded with themselves")
	}
}

func TestEligibleIsSymmetric(t *testing.T) {
	a := candidate(t, "a", func(c *Candidate) { c.Intensity = 2 })
	b := candidate(t, "b", func(c *Candidate) { c.Intensity = 3; c.TimezoneOffsetMinutes = 120 })
	if Eligible(a, b) != Eligible(b, a) {
		t.Fatal("eligibility must not depend on argument order")
	}
}

// -------------------------------------------------------------------- scoring

func TestScoreIsSymmetricAndBounded(t *testing.T) {
	a := candidate(t, "a", func(c *Candidate) { c.TenureWeeks = 6 })
	b := candidate(t, "b", func(c *Candidate) { c.Embedding = vector(t, "clear out the garage") })
	forward, backward := Score(a, b), Score(b, a)
	if math.Abs(forward-backward) > 1e-9 {
		t.Fatalf("Score is not symmetric: %v vs %v", forward, backward)
	}
	if forward < 0 || forward > 1 {
		t.Fatalf("Score %v outside [0, 1]", forward)
	}
}

func TestSimilarGoalsOutscoreUnrelatedOnes(t *testing.T) {
	a := candidate(t, "a")
	near := candidate(t, "near", func(c *Candidate) { c.Embedding = vector(t, "sort out the spare room finally") })
	far := candidate(t, "far", func(c *Candidate) { c.Embedding = vector(t, "call my accountant") })
	if Score(a, near) <= Score(a, far) {
		t.Fatalf("similar goals scored %v, unrelated %v", Score(a, near), Score(a, far))
	}
}

func TestSameBandOutscoresAdjacentBand(t *testing.T) {
	a := candidate(t, "a", func(c *Candidate) { c.Reliability = 0.5 })
	same := candidate(t, "same", func(c *Candidate) { c.Reliability = 0.55 })
	adjacent := candidate(t, "adjacent", func(c *Candidate) { c.Reliability = 0.85 })
	if Score(a, same) <= Score(a, adjacent) {
		t.Fatal("being in the same band should score higher than being one band apart")
	}
}

func TestAvailabilityOverlapMeasuresAgainstTheSmallerSchedule(t *testing.T) {
	// Two free hours, entirely shared with someone who has ten.
	constrained := candidate(t, "constrained", func(c *Candidate) {
		c.Availability = []Window{{DayOfWeek: 5, StartMinute: 600, EndMinute: 720}}
	})
	generous := candidate(t, "generous", func(c *Candidate) {
		c.Availability = []Window{{DayOfWeek: 5, StartMinute: 540, EndMinute: 1080}}
	})
	if got := availabilityOverlap(constrained, generous); math.Abs(got-1) > 1e-9 {
		t.Fatalf("overlap = %v, want 1 — all of the constrained person's time is shared", got)
	}

	disjoint := candidate(t, "disjoint", func(c *Candidate) {
		c.Availability = []Window{{DayOfWeek: 2, StartMinute: 600, EndMinute: 720}}
	})
	if got := availabilityOverlap(constrained, disjoint); got != 0 {
		t.Fatalf("overlap = %v, want 0 for different days", got)
	}
}

func TestAvailabilityIsComparedInASharedTimezone(t *testing.T) {
	// 10:00–12:00 local for both, but two hours apart in real time: no overlap.
	here := candidate(t, "here", func(c *Candidate) {
		c.TimezoneOffsetMinutes = 0
		c.Availability = []Window{{DayOfWeek: 5, StartMinute: 600, EndMinute: 720}}
	})
	there := candidate(t, "there", func(c *Candidate) {
		c.TimezoneOffsetMinutes = 180
		c.Availability = []Window{{DayOfWeek: 5, StartMinute: 600, EndMinute: 720}}
	})
	if got := availabilityOverlap(here, there); got != 0 {
		t.Fatalf("overlap = %v, want 0 — the same local hours in different zones do not overlap", got)
	}
}

func TestBlankAvailabilityIsNeutralNotPunished(t *testing.T) {
	stated := candidate(t, "stated")
	blank := candidate(t, "blank", func(c *Candidate) { c.Availability = nil })
	if got := availabilityOverlap(stated, blank); got != 0.5 {
		t.Fatalf("overlap = %v, want the neutral 0.5 — punishing a blank answer only produces fiction", got)
	}
}

func TestTenureMixPrefersNewWithExperienced(t *testing.T) {
	newcomer := candidate(t, "new", func(c *Candidate) { c.TenureWeeks = 0 })
	sameAge := candidate(t, "same", func(c *Candidate) { c.TenureWeeks = 0 })
	veteran := candidate(t, "veteran", func(c *Candidate) { c.TenureWeeks = 8 })

	if tenureMix(newcomer, sameAge) != 0 {
		t.Error("two people of the same tenure are not a mix")
	}
	if got := tenureMix(newcomer, veteran); got != 1 {
		t.Errorf("tenureMix = %v, want 1 at the full span", got)
	}
	// And it is only worth 0.10 — two beginners are still a perfectly good pod.
	if Score(newcomer, veteran)-Score(newcomer, sameAge) > WeightTenureMix+1e-9 {
		t.Error("tenure mix moved the score by more than its weight")
	}
}

// -------------------------------------------------------------------- assembly

func TestAssembleShipsAPairWhenNoThirdClearsTheThreshold(t *testing.T) {
	a := candidate(t, "a")
	b := candidate(t, "b")
	// Same category so it survives the filters, but nothing else in common.
	loner := candidate(t, "loner", func(c *Candidate) {
		c.Embedding = vector(t, "reconcile the quarterly accounts spreadsheet")
		c.Availability = []Window{{DayOfWeek: 1, StartMinute: 0, EndMinute: 60}}
		c.Intensity = 2
	})

	pods := Assemble([]Candidate{a, b, loner}, Options{ThirdThreshold: 0.95})
	if len(pods) != 1 {
		t.Fatalf("got %d pods, want 1", len(pods))
	}
	if len(pods[0].Members) != 2 {
		t.Fatalf("got a pod of %d — a working pair beats a forced trio (CLAUDE.md §6)", len(pods[0].Members))
	}
	if pods[0].Members[0].UserID == "loner" || pods[0].Members[1].UserID == "loner" {
		t.Fatal("the odd one out should have been left in the pool, not forced in")
	}
}

func TestAssembleFormsATrioWhenTheThirdFits(t *testing.T) {
	members := []Candidate{candidate(t, "a"), candidate(t, "b"), candidate(t, "c")}
	pods := Assemble(members, Options{ThirdThreshold: 0.3})
	if len(pods) != 1 || len(pods[0].Members) != 3 {
		t.Fatalf("got %d pods with sizes %v, want one pod of three", len(pods), sizes(pods))
	}
}

func TestAssembleNeverExceedsThree(t *testing.T) {
	var pool []Candidate
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		pool = append(pool, candidate(t, id))
	}
	pods := Assemble(pool, Options{ThirdThreshold: 0.1})
	for _, pod := range pods {
		if len(pod.Members) < 2 || len(pod.Members) > 3 {
			t.Fatalf("pod of %d — pods are two or three, always", len(pod.Members))
		}
	}
}

func TestAssembleNeverPlacesSomeoneTwice(t *testing.T) {
	var pool []Candidate
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		pool = append(pool, candidate(t, id))
	}
	seen := map[string]int{}
	for _, pod := range Assemble(pool, Options{}) {
		for _, m := range pod.Members {
			seen[m.UserID]++
			if seen[m.UserID] > 1 {
				t.Fatalf("%s was placed in %d pods", m.UserID, seen[m.UserID])
			}
		}
	}
}

func TestAssembleRespectsHardFiltersAcrossWholePods(t *testing.T) {
	pool := []Candidate{
		candidate(t, "home-1"),
		candidate(t, "home-2"),
		candidate(t, "move-1", func(c *Candidate) { c.GoalCategory = "movement" }),
		candidate(t, "move-2", func(c *Candidate) { c.GoalCategory = "movement" }),
	}
	for _, pod := range Assemble(pool, Options{ThirdThreshold: 0.1}) {
		for _, m := range pod.Members[1:] {
			if m.GoalCategory != pod.Members[0].GoalCategory {
				t.Fatalf("pod mixes categories: %v", categories(pod))
			}
		}
	}
}

func TestAssembleIsReproducible(t *testing.T) {
	var pool []Candidate
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		pool = append(pool, candidate(t, id))
	}
	first := sizes(Assemble(pool, Options{}))
	for i := 0; i < 5; i++ {
		if got := sizes(Assemble(pool, Options{})); !equal(got, first) {
			t.Fatalf("run %d produced %v, first run produced %v — a matching job that is not reproducible cannot be debugged", i, got, first)
		}
	}
}

func TestAssembleLeavesTheUnmatchableInThePool(t *testing.T) {
	pods := Assemble([]Candidate{candidate(t, "alone")}, Options{})
	if len(pods) != 0 {
		t.Fatalf("got %d pods from one candidate", len(pods))
	}
	// Two people who cannot be podded together stay unpodded rather than being forced.
	pods = Assemble([]Candidate{
		candidate(t, "a", func(c *Candidate) { c.Intensity = 1 }),
		candidate(t, "b", func(c *Candidate) { c.Intensity = 5 }),
	}, Options{})
	if len(pods) != 0 {
		t.Fatalf("got %d pods from an ineligible pair — waiting an hour beats a bad pod", len(pods))
	}
}

func TestAssembleHandlesAnEmptyPool(t *testing.T) {
	if pods := Assemble(nil, Options{}); len(pods) != 0 {
		t.Fatalf("got %d pods from an empty pool", len(pods))
	}
}

// The third is judged on its weaker pairing, so a pod of two plus a spectator never forms.
func TestThirdIsJudgedOnItsWeakerPairing(t *testing.T) {
	a := candidate(t, "a")
	b := candidate(t, "b", func(c *Candidate) { c.Embedding = vector(t, "clear out the loft boxes") })
	// Fits a's goal text closely, shares nothing with b's schedule.
	lopsided := candidate(t, "lopsided", func(c *Candidate) {
		c.Embedding = vector(t, "sort out the spare room")
		c.Availability = []Window{{DayOfWeek: 0, StartMinute: 0, EndMinute: 30}}
	})

	pods := Assemble([]Candidate{a, b, lopsided}, Options{ThirdThreshold: 0.72})
	if len(pods) != 1 {
		t.Fatalf("got %d pods", len(pods))
	}
	for _, m := range pods[0].Members {
		if m.UserID == "lopsided" {
			t.Fatal("a third who fits one member and not the other makes a pod of two plus a spectator")
		}
	}
}

func sizes(pods []Pod) []int {
	out := make([]int, len(pods))
	for i, p := range pods {
		out[i] = len(p.Members)
	}
	return out
}

func categories(p Pod) []string {
	out := make([]string, len(p.Members))
	for i, m := range p.Members {
		out[i] = m.GoalCategory
	}
	return out
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
