package vector

import (
	"fmt"
	"testing"
)

// SRCH-013: the planner asks whether an index is built, and asking must not
// build it.
func TestBuilt_TrainableIndexesAreBuiltOnlyOnceTrained(t *testing.T) {
	vecs := map[string][]float32{}
	for i := 0; i < 64; i++ {
		vecs[fmt.Sprintf("d%d", i)] = []float32{float32(i % 7), float32(i % 5), float32(i % 3), 1}
	}
	trainables := map[string]VectorSearcher{
		"ivf": NewIVFIndex(2, 5), "pq": NewPQIndex(2, 16, 5), "opq": NewOPQIndex(2, 16, 5, 2),
		"sq": NewSQIndex(), "sq4": NewSQ4Index(),
	}
	for name, idx := range trainables {
		if Built(idx, "c") {
			t.Fatalf("%s: built with nothing in it", name)
		}
		for id, v := range vecs {
			if err := idx.Add("c", id, v); err != nil {
				t.Fatal(err)
			}
		}
		if Built(idx, "c") {
			t.Fatalf("%s: built before training", name)
		}
		if Built(idx, "c") {
			t.Fatalf("%s: asking trained it", name)
		}
		idx.(Trainable).Train("c", vecs)
		if !Built(idx, "c") {
			t.Fatalf("%s: not built after training", name)
		}
	}

	flat := NewVectorIndex()
	if Built(flat, "c") {
		t.Fatal("flat: built while empty")
	}
	if err := flat.Add("c", "d", []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	if !Built(flat, "c") {
		t.Fatal("flat: holding a vector is built")
	}
}

// IVF's widened filtered search probes more clusters and finds what the
// nearest ones do not hold.
func TestIVF_SearchWithFilterWiden(t *testing.T) {
	idx := NewIVFIndex(1, 10)
	vecs := map[string][]float32{}
	for i := 0; i < 100; i++ {
		// Ten well-separated groups of ten.
		g := float32(i / 10)
		vecs[fmt.Sprintf("d%02d", i)] = []float32{g * 10, g * 10, 1}
	}
	for id, v := range vecs {
		if err := idx.Add("c", id, v); err != nil {
			t.Fatal(err)
		}
	}
	idx.Train("c", vecs)

	// The query sits on group 0; the filter admits only group 9.
	allowed := map[string]bool{}
	for i := 90; i < 100; i++ {
		allowed[fmt.Sprintf("d%02d", i)] = true
	}
	query := []float32{0, 0, 1}
	narrow := idx.SearchWithFilter("c", query, 5, -1, allowed, nil)
	wide := idx.SearchWithFilterWiden("c", query, 5, -1, allowed, nil, 64)
	if len(wide) != 5 || len(narrow) >= len(wide) {
		t.Fatalf("narrow %d, wide %d", len(narrow), len(wide))
	}
	var _ Widener = idx

	// The parallel path filters the same way.
	defer swapParallelMinSize(1)()
	par := idx.SearchWithFilterWiden("c", query, 5, -1, allowed, nil, 64)
	if len(par) != 5 {
		t.Fatalf("parallel widened search found %d", len(par))
	}
	for _, r := range par {
		if !allowed[r.DocID] {
			t.Fatalf("the filter let %s through", r.DocID)
		}
	}
	if kmeansInit(nil, 3) != nil {
		t.Fatal("k-means of nothing")
	}
}
