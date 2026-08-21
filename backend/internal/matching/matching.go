// Package matching implements CLAUDE.md §6: hard filters, then scoring, then assembly.
//
// The weights are not tuneable from here on purpose. They are a product decision recorded
// in CLAUDE.md §6, and §8 requires an escalation to change them — so they are constants in
// this file, and changing one is a visible diff on a line that says so.
package matching

import (
	"sort"

	"github.com/omarharoun/pod/backend/internal/embed"
	"github.com/omarharoun/pod/backend/internal/reliability"
)

// The four signal weights from CLAUDE.md §6. They sum to 1.
const (
	WeightGoalSimilarity = 0.40
	WeightReliability    = 0.30
	WeightAvailability   = 0.20
	WeightTenureMix      = 0.10
)

// The hard filters from CLAUDE.md §6.
const (
	MaxTimezoneOffsetMinutes = 3 * 60
	MaxIntensityDistance     = 1
)

// DefaultThirdThreshold is the bar a third member must clear to be added to a pair.
//
// CLAUDE.md §6 says "attempt a third above threshold" without naming one. 0.55 is a
// deliberate choice, recorded in ADR-012: it sits above what two people with nothing in
// common but the category can score, and below what a genuinely good third scores. It is
// a parameter rather than a constant precisely because it is the one number here that
// wants tuning against real pools — unlike the weights, which do not.
const DefaultThirdThreshold = 0.55

// Window is a recurring availability window in the candidate's own timezone.
type Window struct {
	DayOfWeek   int // 0 = Monday
	StartMinute int
	EndMinute   int
}

func (w Window) minutes() int {
	if w.EndMinute <= w.StartMinute {
		return 0
	}
	return w.EndMinute - w.StartMinute
}

// Candidate is one person waiting in the pool, flattened into exactly what scoring needs.
//
// Note what is absent: no email, no goal text, no display name. Matching does not need
// identifying information, so it does not receive any — which also means a matching log
// line cannot leak it.
type Candidate struct {
	UserID                string
	GoalCategory          string
	Intensity             int
	TimezoneOffsetMinutes int
	Embedding             embed.Vector
	Reliability           reliability.Score
	Availability          []Window
	TenureWeeks           int
}

// Pod is an assembled group of two or three.
type Pod struct {
	Members []Candidate
	// Score is the mean pairwise score, kept for observability. It is never returned by
	// the API — the members never see how well the machine thinks they fit.
	Score float64
}

// Eligible applies the hard filters. All must pass; there is no partial credit here.
func Eligible(a, b Candidate) bool {
	if a.UserID == b.UserID {
		return false
	}
	if a.GoalCategory != b.GoalCategory {
		return false
	}
	if abs(a.TimezoneOffsetMinutes-b.TimezoneOffsetMinutes) > MaxTimezoneOffsetMinutes {
		return false
	}
	if abs(a.Intensity-b.Intensity) > MaxIntensityDistance {
		return false
	}
	// CLAUDE.md §6 caps band distance at one under the reliability rules. It is a hard
	// constraint, not a signal, even though band compatibility is also scored below.
	if !a.Reliability.Band().Compatible(b.Reliability.Band()) {
		return false
	}
	return true
}

// Score is the weighted fit of a pair, in [0, 1]. Callers must check Eligible first;
// Score does not re-check the filters and will happily score an impossible pair.
func Score(a, b Candidate) float64 {
	return WeightGoalSimilarity*goalSimilarity(a, b) +
		WeightReliability*reliabilityCompatibility(a, b) +
		WeightAvailability*availabilityOverlap(a, b) +
		WeightTenureMix*tenureMix(a, b)
}

// goalSimilarity maps embedding cosine from [-1, 1] onto [0, 1]. Two goals that are
// actively unrelated score 0.5, not 0 — the category filter has already established that
// they are at least the same kind of thing.
func goalSimilarity(a, b Candidate) float64 {
	return (embed.Similarity(a.Embedding, b.Embedding) + 1) / 2
}

// reliabilityCompatibility rewards being in the same band. One band apart is allowed but
// scores half; further apart is unreachable because Eligible rejected it.
func reliabilityCompatibility(a, b Candidate) float64 {
	switch a.Reliability.Band().Distance(b.Reliability.Band()) {
	case 0:
		return 1
	case 1:
		return 0.5
	default:
		return 0
	}
}

// availabilityOverlap is shared minutes as a fraction of the more constrained person's
// total. Measuring against the smaller schedule is the point: someone with two hours free
// a week who shares all of it with you is a better match than someone with forty who
// shares three, even though three is more minutes.
//
// Nobody having stated any windows scores 0.5 — neutral. Punishing a blank answer would
// only push people to fill the form in with fiction.
func availabilityOverlap(a, b Candidate) float64 {
	if len(a.Availability) == 0 || len(b.Availability) == 0 {
		return 0.5
	}
	shared := 0
	for _, x := range a.Availability {
		for _, y := range b.Availability {
			if x.DayOfWeek != y.DayOfWeek {
				continue
			}
			// Both windows are in their owner's local time, so compare them in a shared
			// frame before intersecting.
			start := max(x.StartMinute+a.TimezoneOffsetMinutes, y.StartMinute+b.TimezoneOffsetMinutes)
			end := min(x.EndMinute+a.TimezoneOffsetMinutes, y.EndMinute+b.TimezoneOffsetMinutes)
			if end > start {
				shared += end - start
			}
		}
	}
	smaller := min(totalMinutes(a.Availability), totalMinutes(b.Availability))
	if smaller == 0 {
		return 0.5
	}
	return clamp01(float64(shared) / float64(smaller))
}

func totalMinutes(windows []Window) int {
	total := 0
	for _, w := range windows {
		total += w.minutes()
	}
	return total
}

// tenureMixSpan is the gap in weeks at which the tenure signal is fully satisfied.
const tenureMixSpan = 8

// tenureMix prefers a new member alongside an experienced one (CLAUDE.md §6). Two people
// who joined the same week score 0, which costs them at most 0.10 — a pod of two beginners
// is a perfectly good pod, just not the preferred shape.
func tenureMix(a, b Candidate) float64 {
	return clamp01(float64(abs(a.TenureWeeks-b.TenureWeeks)) / tenureMixSpan)
}

// Options configures one assembly pass.
type Options struct {
	// ThirdThreshold is the bar for adding a third member. Zero means DefaultThirdThreshold.
	ThirdThreshold float64
}

func (o Options) thirdThreshold() float64 {
	if o.ThirdThreshold <= 0 {
		return DefaultThirdThreshold
	}
	return o.ThirdThreshold
}

type pair struct {
	a, b  int
	score float64
}

// Assemble greedily forms pods from a pool.
//
// CLAUDE.md §6: pair the highest scores first, then attempt a third above the threshold,
// and **if no third clears it, ship a pod of two.** A working pair beats a forced trio,
// so nothing in here ever adds a third to reach a headcount.
//
// Candidates left unmatched stay in the pool for the next window. That is deliberate:
// waiting an hour for a pod that fits is better than being placed in one that does not.
func Assemble(candidates []Candidate, opts Options) []Pod {
	pairs := make([]pair, 0, len(candidates))
	for i := range candidates {
		for j := i + 1; j < len(candidates); j++ {
			if !Eligible(candidates[i], candidates[j]) {
				continue
			}
			pairs = append(pairs, pair{a: i, b: j, score: Score(candidates[i], candidates[j])})
		}
	}

	// Highest score first; ties broken by index so a given pool always produces the same
	// pods. A matching job that is not reproducible cannot be debugged after the fact.
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].score != pairs[j].score {
			return pairs[i].score > pairs[j].score
		}
		if pairs[i].a != pairs[j].a {
			return pairs[i].a < pairs[j].a
		}
		return pairs[i].b < pairs[j].b
	})

	taken := make([]bool, len(candidates))
	var pods []Pod
	for _, p := range pairs {
		if taken[p.a] || taken[p.b] {
			continue
		}
		taken[p.a] = true
		taken[p.b] = true

		members := []Candidate{candidates[p.a], candidates[p.b]}
		total := p.score

		if third, score, ok := bestThird(candidates, taken, p, opts.thirdThreshold()); ok {
			taken[third] = true
			members = append(members, candidates[third])
			// Mean of the three pairwise scores.
			total = (p.score + score*2) / 3
		}
		pods = append(pods, Pod{Members: members, Score: total})
	}
	return pods
}

// bestThird finds the unmatched candidate that fits both members best, and returns it
// only if the weaker of its two pairings clears the threshold.
//
// The weaker pairing is what matters, not the average: a third who fits one member
// brilliantly and the other not at all makes a pod of two plus a spectator.
func bestThird(candidates []Candidate, taken []bool, p pair, threshold float64) (int, float64, bool) {
	bestIndex := -1
	bestScore := 0.0
	for k := range candidates {
		if taken[k] {
			continue
		}
		if !Eligible(candidates[p.a], candidates[k]) || !Eligible(candidates[p.b], candidates[k]) {
			continue
		}
		weaker := min64(Score(candidates[p.a], candidates[k]), Score(candidates[p.b], candidates[k]))
		if weaker < threshold {
			continue
		}
		if bestIndex == -1 || weaker > bestScore {
			bestIndex, bestScore = k, weaker
		}
	}
	if bestIndex == -1 {
		return 0, 0, false
	}
	return bestIndex, bestScore, true
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func min64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}
