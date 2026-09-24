package vector

import (
	"errors"
	"testing"
)

// #252: one unusable vector cost a whole collection its HNSW graph.
//
// These are the exact shapes measured before the fix. Four vectors into an
// empty collection, the first of them empty, left three panics and no usable
// graph node, while CollectionSize reported all four as indexed and every
// search fell back to brute force.

func TestCheckVector(t *testing.T) {
	cases := []struct {
		name string
		v    []float32
		want int
		err  error
	}{
		{"nil", nil, 0, ErrEmptyVector},
		{"empty", []float32{}, 0, ErrEmptyVector},
		{"first vector sets nothing to compare against", []float32{1, 2}, 0, nil},
		{"matching dimension", []float32{1, 2, 3}, 3, nil},
		{"shorter than the collection", []float32{1, 2}, 3, ErrDimensionMismatch},
		{"longer than the collection", []float32{1, 2, 3, 4}, 3, ErrDimensionMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckVector(tc.v, tc.want)
			if !errors.Is(err, tc.err) {
				t.Errorf("CheckVector = %v, want %v", err, tc.err)
			}
		})
	}
}

// The poisoning case itself. The empty vector used to become the graph's entry
// point, and every good vector after it panicked against it.
func TestAnEmptyFirstVectorNoLongerPoisonsTheGraph(t *testing.T) {
	idx := NewHNSWIndex(16, 0, 100)

	if err := idx.Add("c", "empty", []float32{}); !errors.Is(err, ErrEmptyVector) {
		t.Fatalf("an empty vector was accepted: %v", err)
	}
	for i, v := range [][]float32{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}} {
		if err := idx.Add("c", string(rune('a'+i)), v); err != nil {
			t.Fatalf("good vector %d was refused after an empty one: %v", i, err)
		}
	}

	if got := idx.CollectionSize("c"); got != 3 {
		t.Errorf("CollectionSize = %d, want 3 — the refused vector must not be counted", got)
	}
	if hits := idx.Search("c", []float32{1, 2, 3}, 10, 0, nil); len(hits) != 3 {
		t.Errorf("search found %d of 3 good vectors", len(hits))
	}
}

func TestANilVectorIsRefusedByEveryIndex(t *testing.T) {
	for name, s := range map[string]VectorSearcher{
		"flat": NewVectorIndex(),
		"hnsw": NewHNSWIndex(16, 0, 100),
		"ivf":  NewIVFIndex(2, 5),
		"pq":   NewPQIndex(2, 4, 5),
		"opq":  NewOPQIndex(2, 4, 5, 2),
		"sq":   NewSQIndex(),
		"sq4":  NewSQ4Index(),
		"bq":   NewBQIndex(0),
	} {
		t.Run(name, func(t *testing.T) {
			if err := s.Add("c", "nil", nil); !errors.Is(err, ErrEmptyVector) {
				t.Errorf("%s accepted a nil vector: %v", name, err)
			}
			if got := s.CollectionSize("c"); got != 0 {
				t.Errorf("%s counts %d vectors after refusing the only one", name, got)
			}
		})
	}
}

// A vector of the wrong length is the other way to reach the same panic — an
// embedding model changed under a collection that was not reindexed.
func TestADimensionMismatchIsRefusedAndTheGraphSurvives(t *testing.T) {
	idx := NewHNSWIndex(16, 0, 100)

	if err := idx.Add("c", "a", []float32{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("c", "short", []float32{1, 2}); !errors.Is(err, ErrDimensionMismatch) {
		t.Fatalf("a 2-dimensional vector joined a 3-dimensional collection: %v", err)
	}
	if err := idx.Add("c", "b", []float32{4, 5, 6}); err != nil {
		t.Fatalf("a good vector was refused after a mismatched one: %v", err)
	}
	if hits := idx.Search("c", []float32{1, 2, 3}, 10, 0, nil); len(hits) != 2 {
		t.Errorf("search found %d of 2 good vectors", len(hits))
	}
}

// Collections are independent: a dimension set by one must not constrain another.
func TestEachCollectionHasItsOwnDimension(t *testing.T) {
	idx := NewHNSWIndex(16, 0, 100)
	if err := idx.Add("small", "a", []float32{1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("large", "a", []float32{1, 2, 3, 4}); err != nil {
		t.Errorf("a second collection was held to the first one's dimension: %v", err)
	}
}

// The library can still panic on its own account, and the recovery has to hold
// when it does. Reached directly, because the vectors that used to reach it
// through Add are now refused before they get there.
func TestAGraphPanicIsRecoveredAndReported(t *testing.T) {
	idx := NewHNSWIndex(16, 0, 100)
	if err := idx.Add("c", "a", []float32{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	g := idx.graphs["c"]

	// A node the graph cannot compare with its entry point — the panic that
	// used to fire inside Add.
	if panicked := addToGraph(g, "short", []float32{1, 2}); panicked == nil {
		t.Fatal("the library did not panic on a mismatched node; the recovery is untested")
	}
	if panicked := addToGraph(g, "b", []float32{4, 5, 6}); panicked != nil {
		t.Errorf("a good node panicked after the recovered one: %v", panicked)
	}
}

// Re-adding a document replaces it in the graph rather than duplicating it.
func TestReAddingAVectorReplacesIt(t *testing.T) {
	idx := NewHNSWIndex(16, 0, 100)
	for _, v := range [][]float32{{1, 0, 0}, {0, 1, 0}} {
		if err := idx.Add("c", "doc", v); err != nil {
			t.Fatal(err)
		}
	}
	if got := idx.CollectionSize("c"); got != 1 {
		t.Errorf("CollectionSize = %d after re-adding one document, want 1", got)
	}
}

// A process running since before Add refused unusable vectors can still hold
// one in its fallback map. A rebuild must skip it rather than abort, or one bad
// vector would stop the whole collection from being compacted.
func TestCompactionSkipsAVectorTheGraphCannotHold(t *testing.T) {
	idx := NewHNSWIndex(16, 0, 100)
	for i, v := range [][]float32{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}} {
		if err := idx.Add("c", string(rune('a'+i)), v); err != nil {
			t.Fatal(err)
		}
	}
	// What a pre-2.15.1 process could have stored: a vector of the wrong
	// length, placed where only the fallback map holds it.
	idx.mu.Lock()
	idx.vectors["c"]["legacy"] = []float32{1, 2}
	idx.mu.Unlock()

	idx.Compact("c")

	if hits := idx.Search("c", []float32{1, 0, 0}, 10, 0, nil); len(hits) != 3 {
		t.Errorf("search after compaction found %d of 3 good vectors", len(hits))
	}
}
