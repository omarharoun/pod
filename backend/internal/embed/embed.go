// Package embed turns a goal sentence into a vector, for the 0.40 semantic-similarity
// signal in CLAUDE.md §6.
package embed

import (
	"context"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"unicode"
)

// Dimensions is the width of a Pod embedding. Fixed here so a stored vector and a freshly
// computed one are always comparable; changing it is a backfill (`cmd/reembed`).
//
// The first `conceptDimensions` are one-per-concept and never collide — a concept is the
// thing this embedder actually knows, so it gets a dimension of its own rather than a
// share of a hash bucket. The rest carry hashed lexical features for text the lexicon has
// never seen.
const Dimensions = 384

// Vector is a unit-length embedding. Keeping them normalised means similarity is a plain
// dot product, which is the difference between the matching job taking seconds and taking
// minutes at any pool size worth worrying about.
type Vector []float32

// Goal is one thing to embed: what somebody wrote, and the category they filed it under.
//
// The category is here because this embedder uses it (see lexicon.go) and a hosted one
// would simply ignore it. Passing something the implementation may not need is cheaper
// than a second interface, and the swap ADR-011 promised is still one line.
type Goal struct {
	Text     string
	Category string
}

// Embedder turns goals into vectors. Batched because the hosted implementation will be an
// HTTP call and one call for a whole matching window beats one per candidate.
type Embedder interface {
	Embed(ctx context.Context, goals []Goal) ([]Vector, error)
}

// Feature weights. Concepts outweigh words because a concept is a claim about meaning and
// a word is a coincidence of spelling; a parent outweighs nothing much, because "both of
// these are cardio" is a real but weak thing to have in common.
const (
	conceptWeight = 1.0
	parentWeight  = 0.45
	settingWeight = 0.35
	wordWeight    = 0.30
	bigramWeight  = 0.12
)

// Lexical is a deterministic, dependency-free embedder: a curated concept lexicon over
// the goal domain, with hashed word features underneath it for anything the lexicon does
// not cover.
//
// It is not a language model and does not pretend to be one. It knows that running is
// like jogging because somebody wrote that down (see lexicon.go), and it knows nothing at
// all about a goal written in a vocabulary nobody anticipated — which is where the hashed
// features take over, and they are exactly what this whole embedder used to be.
//
// The hosted embedder still replaces it in one line at the call site (ADR-011, ADR-031).
type Lexical struct{}

func (Lexical) Embed(_ context.Context, goals []Goal) ([]Vector, error) {
	out := make([]Vector, len(goals))
	for i, goal := range goals {
		out[i] = embedGoal(goal.Text, goal.Category)
	}
	return out, nil
}

func embedGoal(text, category string) Vector {
	vector := make(Vector, Dimensions)
	words := tokenise(text)
	words0, phrases := lookupsFor(category)

	// Phrases first, and they consume the words they are made of. "credit card" is a debt
	// and "card" is something you post to your sister; letting both fire would put half
	// of somebody's money goal in the connection part of the space.
	consumed := make([]bool, len(words))
	for i := 0; i+1 < len(words); i++ {
		ids, ok := phrases[words[i]+" "+words[i+1]]
		if !ok {
			continue
		}
		addConcepts(vector, ids)
		consumed[i], consumed[i+1] = true, true
		i++ // A word belongs to one phrase, not to two overlapping ones.
	}

	for i, word := range words {
		if !consumed[i] {
			addConcepts(vector, words0[word])
		}
		addLexical(vector, word, wordWeight)
	}

	// Adjacent pairs give the vector a little word order, which is most of what separates
	// "give up smoking" from "smoking" on its own.
	for i := 0; i+1 < len(words); i++ {
		addLexical(vector, words[i]+" "+words[i+1], bigramWeight)
	}
	return normalise(vector)
}

// addConcepts writes one token's worth of evidence.
//
// A word that means several things is weaker evidence for each of them, so its weight is
// shared rather than repeated. Otherwise "paint" — a craft and a household chore — would
// shout louder than "watercolour", which is only ever one of those.
func addConcepts(vector Vector, ids []string) {
	if len(ids) == 0 {
		return
	}
	share := float32(1) / float32(len(ids))
	for _, id := range ids {
		weight := float32(conceptWeight)
		if settingConcepts[id] {
			weight = settingWeight
		}
		addConcept(vector, id, weight*share)
		for _, parent := range conceptParents[id] {
			addConcept(vector, parent, parentWeight*share)
		}
	}
}

func addConcept(vector Vector, id string, weight float32) {
	index, ok := conceptIndex[id]
	if !ok {
		// A parent named in conceptParents with no entry of its own is still a concept:
		// the index is built from both maps, so this cannot happen. Refusing silently
		// beats writing into a lexical dimension and calling it meaning.
		return
	}
	vector[index] += weight
}

func addLexical(vector Vector, feature string, weight float32) {
	digest := fnv.New64a()
	_, _ = digest.Write([]byte(feature))
	sum := digest.Sum64()
	index := conceptDimensions + int(sum%uint64(Dimensions-conceptDimensions))
	// The top bit picks the sign, so unrelated features cancel instead of piling up.
	if sum&(1<<63) != 0 {
		weight = -weight
	}
	vector[index] += weight
}

// tokenise lower-cases, splits on anything that is not a letter or digit, drops the
// handful of words that appear in almost every goal, and stems what is left.
func tokenise(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := fields[:0]
	for _, word := range fields {
		if len(word) < 2 || stopWords[word] {
			continue
		}
		out = append(out, stem(word))
	}
	return out
}

var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "of": true, "in": true, "on": true,
	"for": true, "and": true, "or": true, "my": true, "me": true, "i": true, "is": true,
	"it": true, "at": true, "be": true, "do": true, "am": true, "so": true, "that": true,
	"this": true, "with": true, "more": true, "some": true,
}

// conceptIndex assigns every concept and parent a dimension of its own, in sorted order so
// the layout is stable across builds — a vector stored yesterday has to mean the same
// thing today, and map iteration order would quietly break that.
var (
	conceptIndex      map[string]int
	conceptDimensions int
	wordConcepts      map[string]map[string][]string
	phraseConcepts    map[string]map[string][]string
)

// lookupsFor returns the concept tables for one category.
//
// A category the lexicon has never heard of — one added to the spec before anybody added
// its concepts here — falls back to every category at once. That is the behaviour this
// embedder had before it knew about categories: dilution rather than silence, and a goal
// that still scores on its words either way.
func lookupsFor(category string) (words, phrases map[string][]string) {
	if w, ok := wordConcepts[category]; ok {
		return w, phraseConcepts[category]
	}
	return wordConcepts[allCategories], phraseConcepts[allCategories]
}

// allCategories is not a category anybody can choose; the empty string cannot collide
// with one from the spec.
const allCategories = ""

func init() {
	names := make([]string, 0, 128)
	seen := map[string]bool{}
	for _, concepts := range conceptWords {
		for id := range concepts {
			if !seen[id] {
				seen[id] = true
				names = append(names, id)
			}
		}
	}
	for _, parents := range conceptParents {
		for _, parent := range parents {
			if !seen[parent] {
				seen[parent] = true
				names = append(names, parent)
			}
		}
	}
	sort.Strings(names)

	conceptIndex = make(map[string]int, len(names))
	for i, name := range names {
		conceptIndex[name] = i
	}
	conceptDimensions = len(names)

	// Surface forms go through the same stemmer the input does, so "jogging" in the
	// lexicon and "jogged" in somebody's goal land on the same entry. A multi-word form
	// is indexed as a phrase rather than being broken up: "emergency fund" split into
	// "emergency" and "fund" would make an emergency of any kind into a savings goal.
	wordConcepts = map[string]map[string][]string{allCategories: {}}
	phraseConcepts = map[string]map[string][]string{allCategories: {}}
	for category, concepts := range conceptWords {
		wordConcepts[category] = map[string][]string{}
		phraseConcepts[category] = map[string][]string{}
		for id, words := range concepts {
			for _, word := range words {
				parts := strings.Fields(strings.ToLower(word))
				keys := make([]string, 0, len(parts))
				for _, part := range parts {
					if key := stem(part); len(key) >= 2 {
						keys = append(keys, key)
					}
				}
				switch len(keys) {
				case 0:
				case 1:
					addIndex(wordConcepts[category], keys[0], id)
					addIndex(wordConcepts[allCategories], keys[0], id)
				default:
					phrase := strings.Join(keys, " ")
					addIndex(phraseConcepts[category], phrase, id)
					addIndex(phraseConcepts[allCategories], phrase, id)
				}
			}
		}
	}
	for _, table := range []map[string]map[string][]string{wordConcepts, phraseConcepts} {
		for _, index := range table {
			for key := range index {
				sort.Strings(index[key])
			}
		}
	}
}

func addIndex(index map[string][]string, key, id string) {
	if !contains(index[key], id) {
		index[key] = append(index[key], id)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func normalise(vector Vector) Vector {
	var sum float64
	for _, v := range vector {
		sum += float64(v) * float64(v)
	}
	if sum == 0 {
		return vector
	}
	inverse := float32(1 / math.Sqrt(sum))
	for i := range vector {
		vector[i] *= inverse
	}
	return vector
}

// Similarity is the cosine of two unit vectors, which for normalised inputs is a dot
// product. It returns 0 for a mismatched or empty pair rather than a garbage number —
// which is also what a vector stored before a lexicon change scores until `cmd/reembed`
// has run: no signal, never a wrong one.
func Similarity(a, b Vector) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var sum float32
	// Eight independent accumulators: float addition is not associative, so a single
	// running total leaves the CPU waiting on its own latency chain.
	var acc [8]float32
	i := 0
	for ; i+8 <= len(a); i += 8 {
		acc[0] += a[i] * b[i]
		acc[1] += a[i+1] * b[i+1]
		acc[2] += a[i+2] * b[i+2]
		acc[3] += a[i+3] * b[i+3]
		acc[4] += a[i+4] * b[i+4]
		acc[5] += a[i+5] * b[i+5]
		acc[6] += a[i+6] * b[i+6]
		acc[7] += a[i+7] * b[i+7]
	}
	for ; i < len(a); i++ {
		sum += a[i] * b[i]
	}
	for _, v := range acc {
		sum += v
	}
	return clampUnit(float64(sum))
}

func clampUnit(v float64) float64 {
	switch {
	case v < -1:
		return -1
	case v > 1:
		return 1
	default:
		return v
	}
}
