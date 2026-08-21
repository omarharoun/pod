// Package embed turns a goal sentence into a vector, for the 0.40 semantic-similarity
// signal in CLAUDE.md §6.
package embed

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// Dimensions is the width of a Pod embedding. Fixed here so a stored vector and a freshly
// computed one are always comparable; changing it is a backfill.
const Dimensions = 256

// Vector is a unit-length embedding. Keeping them normalised means similarity is a plain
// dot product, which is the difference between the matching job taking seconds and taking
// minutes at any pool size worth worrying about.
type Vector []float32

// Embedder turns goal text into vectors. Batched because the hosted implementation will
// be an HTTP call and one call for a whole matching window beats one per candidate.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([]Vector, error)
}

// Hashing is a deterministic, dependency-free embedder: hashed word and word-pair
// features projected into a fixed space, then normalised.
//
// It is a real implementation, not a stub — the matching pipeline runs end to end on it,
// and it is what the tests and the seeded pool use. It is also weak: it matches on shared
// vocabulary, not on meaning, so "run every morning" and "start jogging" look unrelated
// to it. ADR-011 records that the hosted embedder replaces it before real users are
// matched, and that swapping it is one line plus a backfill.
type Hashing struct{}

func (Hashing) Embed(_ context.Context, texts []string) ([]Vector, error) {
	out := make([]Vector, len(texts))
	for i, text := range texts {
		out[i] = hashText(text)
	}
	return out, nil
}

func hashText(text string) Vector {
	vector := make(Vector, Dimensions)
	words := tokenise(text)
	for _, word := range words {
		addFeature(vector, word, 1)
	}
	// Adjacent pairs give the vector a little word order, which is most of what
	// separates "give up smoking" from "smoking" on its own.
	for i := 0; i+1 < len(words); i++ {
		addFeature(vector, words[i]+" "+words[i+1], 0.5)
	}
	return normalise(vector)
}

func addFeature(vector Vector, feature string, weight float32) {
	digest := fnv.New64a()
	_, _ = digest.Write([]byte(feature))
	sum := digest.Sum64()
	index := int(sum % uint64(Dimensions))
	// The top bit picks the sign, so unrelated features cancel instead of piling up.
	if sum&(1<<63) != 0 {
		weight = -weight
	}
	vector[index] += weight
}

// tokenise lower-cases, splits on anything that is not a letter or digit, and drops the
// handful of words that appear in almost every goal.
func tokenise(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := fields[:0]
	for _, word := range fields {
		if len(word) < 2 || stopWords[word] {
			continue
		}
		out = append(out, word)
	}
	return out
}

var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "of": true, "in": true, "on": true,
	"for": true, "and": true, "or": true, "my": true, "me": true, "i": true, "is": true,
	"it": true, "at": true, "be": true, "do": true, "am": true, "so": true, "that": true,
	"this": true, "with": true, "more": true, "some": true,
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
// product. It returns 0 for a mismatched or empty pair rather than a garbage number.
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
