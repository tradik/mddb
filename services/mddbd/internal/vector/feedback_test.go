package vector

import (
	"math"
	"testing"
)

func norm(v []float32) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

func TestNormalizeAndCentroid(t *testing.T) {
	if n := norm(Normalize([]float32{3, 4})); math.Abs(n-1) > 1e-6 {
		t.Fatalf("norm %v", n)
	}
	if z := Normalize([]float32{0, 0}); z[0] != 0 || z[1] != 0 {
		t.Fatalf("zero vector: %v", z)
	}
	if Centroid(nil) != nil {
		t.Fatal("centroid of nothing")
	}
	// A long vector counts as much as a short one; another dimension not at all.
	c := Centroid([][]float32{{10, 0}, {0, 1}, {1, 2, 3}})
	if math.Abs(float64(c[0]-c[1])) > 1e-6 || len(c) != 2 {
		t.Fatalf("centroid %v", c)
	}
}

// A positive pulls the query towards it, a negative pushes it away, and the
// result is unit length.
func TestRocchio(t *testing.T) {
	q := []float32{1, 0, 0}
	pos := [][]float32{{0, 1, 0}}
	neg := [][]float32{{0, 0, 1}}
	got := Rocchio(q, pos, neg, DefaultRocchio)
	if math.Abs(norm(got)-1) > 1e-6 {
		t.Fatalf("not unit length: %v", got)
	}
	if CosineSimilarity(got, pos[0]) <= CosineSimilarity(q, pos[0]) {
		t.Fatal("the positive did not pull")
	}
	if CosineSimilarity(got, neg[0]) >= CosineSimilarity(q, neg[0]) {
		t.Fatal("the negative did not push")
	}
	// Examples of another dimension change nothing.
	same := Rocchio(q, [][]float32{{1, 1}}, nil, DefaultRocchio)
	if same[0] != 1 || same[1] != 0 {
		t.Fatalf("mismatched example moved the query: %v", same)
	}
}

// A document example stands for all of its chunks.
func TestDocumentVectors(t *testing.T) {
	vi := NewVectorIndex()
	for key, v := range map[string][]float32{"d#0": {1, 0}, "d#1": {0, 1}, "d#3": {1, 1}, "legacy": {2, 2}} {
		if err := vi.Add("c", key, v); err != nil {
			t.Fatal(err)
		}
	}
	if got := vi.DocumentVectors("c", "d"); len(got) != 2 {
		t.Fatalf("chunks stop at the first gap: %v", got)
	}
	if got := vi.DocumentVectors("c", "legacy"); len(got) != 1 {
		t.Fatalf("an unchunked document: %v", got)
	}
	if got := vi.DocumentVectors("none", "d"); got != nil {
		t.Fatalf("unknown collection: %v", got)
	}
}

func TestRescoreFeedback(t *testing.T) {
	results := []VectorResult{{DocID: "a", Score: 0.9}, {DocID: "b", Score: 0.8}, {DocID: "c", Score: 0.7}}
	vectors := map[string][]float32{"a": {1, 0}, "b": {0, 1}}
	got := RescoreFeedback(results, vectors, [][]float32{{0, 1}}, [][]float32{{1, 0}, {1, 2, 3}}, 0.5, nil)
	if got[0].DocID != "b" || got[0].Score <= 0.8 {
		t.Fatalf("the positive's neighbour did not rise: %+v", got)
	}
	if got[2].DocID != "a" || got[1].DocID != "c" || got[1].Score != 0.7 {
		t.Fatalf("the negative's neighbour did not fall, or a result without a vector moved: %+v", got)
	}
	if meanSimilarity([]float32{1}, nil, CosineSimilarity) != 0 {
		t.Fatal("no examples, no shift")
	}
}
