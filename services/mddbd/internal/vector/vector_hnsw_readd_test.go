package vector

import (
	"fmt"
	"math/rand"
	"testing"
)

// #267: collections whose documents are re-added on the same key — an
// automation rewriting its state, a CMS saving a page — reported 642 HNSW
// panics in a week ("invalid memory address or nil pointer dereference" in
// Add), and each panic left a chunk unsearchable until a reindex. Collections
// whose keys are never reused reported none.
//
// An overwrite deletes the old node from the graph before adding the new
// one. Remove counted its deletions and rebuilt the graph once enough had
// accumulated, which is what keeps the library away from the shapes it
// crashes on (GO-029); the overwrite in Add deleted without counting, so a
// collection that was only ever rewritten was never rebuilt.
func TestRewritingTheSameKeysKeepsEveryChunkSearchable(t *testing.T) {
	rng := rand.New(rand.NewSource(267)) //nolint:gosec // G404: deterministic test data is the point here
	randVec := func() []float32 {
		v := make([]float32, 16)
		for i := range v {
			v[i] = rng.Float32()
		}
		return v
	}

	h := NewHNSWIndex(16, 200, 100)
	const keys = 200
	for round := 0; round < 30; round++ {
		for k := 0; k < keys; k++ {
			if err := h.Add("c", fmt.Sprintf("doc%d#0", k), randVec()); err != nil {
				t.Fatalf("round %d, key %d: %v", round, k, err)
			}
		}
	}

	if got := h.CollectionSize("c"); got != keys {
		t.Errorf("CollectionSize = %d, want %d", got, keys)
	}
	// Every key must still be reachable through the graph, not only by the
	// brute-force fallback.
	for k := 0; k < keys; k += 17 {
		id := fmt.Sprintf("doc%d#0", k)
		v := h.vectors["c"][id]
		hits := h.Search("c", v, 1, -1, nil)
		if len(hits) == 0 || hits[0].DocID != id {
			t.Errorf("%s is not its own nearest neighbour: %+v", id, hits)
		}
	}
}

// Delete then re-add, the other half of the report.
func TestDeletingAndReaddingTheSameKeys(t *testing.T) {
	rng := rand.New(rand.NewSource(2671)) //nolint:gosec // G404: deterministic test data is the point here
	h := NewHNSWIndex(16, 200, 100)
	for round := 0; round < 20; round++ {
		for k := 0; k < 100; k++ {
			v := make([]float32, 8)
			for i := range v {
				v[i] = rng.Float32()
			}
			if err := h.Add("c", fmt.Sprintf("doc%d", k), v); err != nil {
				t.Fatalf("round %d, key %d: %v", round, k, err)
			}
		}
		for k := 0; k < 100; k += 2 {
			h.Remove("c", fmt.Sprintf("doc%d", k))
		}
	}
}
