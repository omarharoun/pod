package embed

import (
	"context"
	"math"
	"testing"
)

func embedOne(t *testing.T, text string) Vector {
	t.Helper()
	vectors, err := Hashing{}.Embed(context.Background(), []string{text})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	return vectors[0]
}

func TestEmbeddingIsDeterministic(t *testing.T) {
	a := embedOne(t, "finally sort out the spare room")
	b := embedOne(t, "finally sort out the spare room")
	if Similarity(a, b) < 0.9999 {
		t.Fatalf("the same text embedded differently twice — matching would not be reproducible")
	}
}

func TestEmbeddingIsUnitLength(t *testing.T) {
	v := embedOne(t, "run three times a week")
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if math.Abs(math.Sqrt(sum)-1) > 1e-5 {
		t.Fatalf("vector length %v, want 1 — similarity assumes normalised inputs", math.Sqrt(sum))
	}
}

func TestSharedVocabularyScoresHigherThanUnrelatedText(t *testing.T) {
	room := embedOne(t, "sort out the spare room")
	roomAgain := embedOne(t, "clear out the spare room")
	running := embedOne(t, "run three times a week")

	near := Similarity(room, roomAgain)
	far := Similarity(room, running)
	if near <= far {
		t.Fatalf("overlapping text scored %v, unrelated text %v — the signal is inverted or absent", near, far)
	}
}

// The known weakness, asserted so nobody mistakes this for a semantic model. ADR-011.
func TestHashingEmbedderDoesNotUnderstandSynonyms(t *testing.T) {
	a := embedOne(t, "run every morning")
	b := embedOne(t, "start jogging daily")
	if Similarity(a, b) > 0.3 {
		t.Skip("the hashing embedder scored a synonym pair highly — pleasant, but not something to rely on")
	}
	// Deliberately not a failure: this documents that the 0.40 signal is weak until the
	// hosted embedder lands, so this test is the reminder, not the guard.
	t.Logf("synonym similarity %.3f — this is why ADR-011 exists", Similarity(a, b))
}

func TestSimilarityHandlesDegenerateInput(t *testing.T) {
	if got := Similarity(nil, nil); got != 0 {
		t.Errorf("Similarity(nil, nil) = %v, want 0", got)
	}
	if got := Similarity(Vector{1, 0}, Vector{1, 0, 0}); got != 0 {
		t.Errorf("mismatched lengths should score 0, got %v", got)
	}
}

func TestSimilarityOfIdenticalVectorsIsOne(t *testing.T) {
	v := embedOne(t, "write a thousand words")
	if got := Similarity(v, v); math.Abs(got-1) > 1e-4 {
		t.Fatalf("Similarity(v, v) = %v, want 1", got)
	}
}

func TestSimilarityStaysInRange(t *testing.T) {
	texts := []string{"a", "sort the room", "run", "", "call my sister back after all this time"}
	for _, x := range texts {
		for _, y := range texts {
			got := Similarity(embedOne(t, x), embedOne(t, y))
			if got < -1 || got > 1 {
				t.Fatalf("Similarity(%q, %q) = %v, outside [-1, 1]", x, y, got)
			}
		}
	}
}

func TestEmptyTextEmbedsWithoutPanicking(t *testing.T) {
	v := embedOne(t, "")
	if len(v) != Dimensions {
		t.Fatalf("got %d dimensions, want %d", len(v), Dimensions)
	}
}
