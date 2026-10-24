package vector

import (
	"fmt"
	"testing"
)

// Edges of the quantized and IVF indexes that callers depend on and no test
// pinned down.

func TestQuantizedIndexRefusesWhatItCannotHold(t *testing.T) {
	qt := QuantNone
	qi := NewQuantizedVectorIndex(func(string) QuantizationType { return qt })

	if err := qi.Add("c", "empty", nil); err == nil {
		t.Error("an empty vector was accepted")
	}
	// A collection without quantization configured is not this index's.
	if err := qi.Add("c", "plain", []float32{1, 0}); err != nil || qi.CollectionSize("c") != 0 {
		t.Errorf("an unquantized collection was stored: err %v, size %d", err, qi.CollectionSize("c"))
	}

	qt = QuantInt8
	for i := 0; i < 8; i++ {
		_ = qi.Add("c", fmt.Sprintf("d%d#0", i), []float32{float32(i), 1, 0.5})
	}
	if got := qi.Search("c", []float32{1, 1, 0.5}, 0, -1, nil); len(got) != 5 {
		t.Errorf("topK 0 should mean the default of 5, got %d", len(got))
	}
	if got := qi.Search("c", nil, 3, -1, nil); got != nil {
		t.Errorf("an empty query returned %v", got)
	}
	if got := qi.Search("none", []float32{1, 1, 0.5}, 3, -1, nil); got != nil {
		t.Errorf("an unknown collection returned %v", got)
	}
	// A nil filter is an empty allow-list: nothing is allowed.
	if got := qi.SearchWithFilter("c", []float32{1, 1, 0.5}, 3, -1, nil, nil); len(got) != 0 {
		t.Errorf("a nil filter allowed %v", got)
	}
	if got := qi.SearchWithFilter("c", []float32{1, 1, 0.5}, 3, -1, map[string]bool{"d2": true}, nil); len(got) != 1 || got[0].DocID != "d2#0" {
		t.Errorf("filter to d2 returned %v", got)
	}
	if !qi.HasCollection("c") || qi.HasCollection("none") {
		t.Error("HasCollection disagrees with what was stored")
	}
	qi.Remove("c", "d2#0")
	qi.Remove("none", "x")
	if got := qi.CollectionSize("c"); got != 7 {
		t.Errorf("size after a remove = %d, want 7", got)
	}
}

func TestIVFEdgesOnUnknownAndEmptyCollections(t *testing.T) {
	idx := NewIVFIndex(0, 0) // defaults
	idx.Remove("none", "x")  // must not panic
	idx.Train("none", nil)   // nothing to train on
	if got := idx.Search("none", []float32{1}, 0, -1, nil); got != nil {
		t.Errorf("unknown collection returned %v", got)
	}
	if got := idx.CollectionSize("none"); got != 0 {
		t.Errorf("CollectionSize of nothing = %d", got)
	}

	for i := 0; i < 12; i++ {
		v := []float32{0.1, 0.1, 0.1}
		v[i%3] = 1
		_ = idx.Add("c", fmt.Sprintf("d%d", i), v)
	}
	if got := idx.Search("c", []float32{1, 0.1, 0.1}, 0, -1, nil); len(got) == 0 || len(got) > 5 {
		t.Errorf("topK 0 should mean the default of 5, got %d", len(got))
	}
	idx.Remove("c", "d0")
	for _, h := range idx.Search("c", []float32{1, 0.1, 0.1}, 12, -1, nil) {
		if h.DocID == "d0" {
			t.Error("a removed document is still returned")
		}
	}
	if names := idx.Collections(); len(names) != 1 || names[0] != "c" {
		t.Errorf("Collections = %v", names)
	}
}

// Every searcher answers the same way at its edges: an unknown collection is
// empty, a removed document stops coming back, topK 0 means the default, and
// Collections lists what is held.
func TestEverySearcherAgreesAtItsEdges(t *testing.T) {
	for name, s := range map[string]VectorSearcher{
		"pq": NewPQIndex(2, 4, 2), "opq": NewOPQIndex(2, 4, 2, 1), "sq": NewSQIndex(),
		"sq4": NewSQ4Index(), "bq": NewBQIndex(0), "hnsw": NewHNSWIndex(0, 0, 0),
	} {
		t.Run(name, func(t *testing.T) {
			s.Remove("none", "x")
			if got := s.Search("none", []float32{1, 0, 0, 0}, 3, -1, nil); len(got) != 0 {
				t.Errorf("unknown collection returned %v", got)
			}
			if got := s.CollectionSize("none"); got != 0 {
				t.Errorf("CollectionSize of nothing = %d", got)
			}
			for i := 0; i < 12; i++ {
				v := []float32{0.1, 0.1, 0.1, 0.1}
				v[i%4] = 1
				if err := s.Add("c", fmt.Sprintf("d%d", i), v); err != nil {
					t.Fatal(err)
				}
			}
			if got := s.Search("c", []float32{1, 0.1, 0.1, 0.1}, 0, -1, nil); len(got) == 0 || len(got) > 5 {
				t.Errorf("topK 0 should mean the default of 5, got %d", len(got))
			}
			s.Remove("c", "d0")
			for _, h := range s.Search("c", []float32{1, 0.1, 0.1, 0.1}, 12, -1, nil) {
				if h.DocID == "d0" {
					t.Error("a removed document is still returned")
				}
			}
			if got := s.CollectionSize("c"); got != 11 {
				t.Errorf("CollectionSize after a remove = %d, want 11", got)
			}
			if lister, ok := s.(interface{ Collections() []string }); ok {
				if names := lister.Collections(); len(names) != 1 || names[0] != "c" {
					t.Errorf("Collections = %v", names)
				}
			}
		})
	}
}

// Training on nothing, or on vectors with no dimensions, leaves an index
// untrained instead of failing; a dimension smaller than the configured
// number of subspaces is trained with one subspace per dimension.
func TestTrainingOnNothingAndOnTinyVectors(t *testing.T) {
	for name, tr := range map[string]Trainable{
		"pq": NewPQIndex(4, 4, 2), "opq": NewOPQIndex(4, 4, 2, 1), "sq": NewSQIndex(), "bq": NewBQIndex(0),
	} {
		t.Run(name, func(t *testing.T) {
			tr.Train("c", nil)
			tr.Train("c", map[string][]float32{"z": {}})
			s := tr.(VectorSearcher)
			if got := s.Search("c", []float32{1}, 3, -1, nil); len(got) != 0 {
				t.Errorf("an index trained on nothing answered %v", got)
			}
		})
	}
	// 4 subspaces for 2-dimensional vectors: trained with one per dimension.
	// Two dimensions rather than one, because in one dimension every
	// positive vector has cosine 1 with every other and the winner is chance.
	pq := NewPQIndex(4, 4, 2)
	for id, v := range map[string][]float32{"x": {1, 0}, "y": {0, 1}, "xy": {1, 1}, "x2": {0.9, 0.1}, "y2": {0.1, 0.9}} {
		_ = pq.Add("c", id, v)
	}
	if got := pq.Search("c", []float32{0, 1}, 1, -1, nil); len(got) != 1 || got[0].DocID != "y" {
		t.Errorf("PQ search with more subspaces than dimensions = %v, want y", got)
	}
}
