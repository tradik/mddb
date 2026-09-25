package vector

import (
	"math"
	"sync"
	"testing"
)

func TestSQIndexName(t *testing.T) {
	idx := NewSQIndex()
	if idx.Name() != "sq" {
		t.Errorf("expected name 'sq', got %s", idx.Name())
	}
}

func TestSQIndexReady(t *testing.T) {
	idx := NewSQIndex()
	if idx.IsReady() {
		t.Error("expected not ready initially")
	}
	idx.SetReady()
	if !idx.IsReady() {
		t.Error("expected ready after SetReady")
	}
}

func TestSQIndexBasicOps(t *testing.T) {
	idx := NewSQIndex()
	idx.SetReady()

	vectors := map[string][]float32{
		"doc1": {1, 0, 0, 0, 0, 0, 0, 0},
		"doc2": {0.9, 0.1, 0, 0, 0, 0, 0, 0},
		"doc3": {0, 1, 0, 0, 0, 0, 0, 0},
		"doc4": {0, 0, 1, 0, 0, 0, 0, 0},
	}

	for id, vec := range vectors {
		if err := idx.Add("docs", id, vec); err != nil {
			t.Fatal(err)
		}
	}
	idx.Train("docs", vectors)

	if idx.CollectionSize("docs") != 4 {
		t.Errorf("expected 4 docs, got %d", idx.CollectionSize("docs"))
	}

	query := []float32{1, 0, 0, 0, 0, 0, 0, 0}
	results := idx.Search("docs", query, 3, 0.0, nil)
	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}

	// doc1 should be the top result (exact match)
	if results[0].DocID != "doc1" {
		t.Errorf("expected doc1 as top result, got %s", results[0].DocID)
	}
	// doc2 should be second (most similar)
	if len(results) > 1 && results[1].DocID != "doc2" {
		t.Errorf("expected doc2 as second result, got %s", results[1].DocID)
	}
}

func TestSQIndexTrainsOnFirstSearch(t *testing.T) {
	// A collection nobody trained used to answer every search with nothing
	// until a restart or reindex; the first search now trains it.
	idx := NewSQIndex()
	idx.SetReady()

	if err := idx.Add("docs", "doc1", []float32{1, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}

	query := []float32{1, 0, 0, 0}
	results := idx.Search("docs", query, 10, 0.0, nil)
	if len(results) == 0 || results[0].DocID != "doc1" {
		t.Fatalf("first search on an untrained collection = %+v, want doc1 first", results)
	}
}

func TestSQIndexSearchWithFilter(t *testing.T) {
	idx := NewSQIndex()
	idx.SetReady()

	vectors := map[string][]float32{
		"doc1": {1, 0, 0, 0},
		"doc2": {0.9, 0.1, 0, 0},
		"doc3": {0, 1, 0, 0},
	}
	for id, vec := range vectors {
		if err := idx.Add("docs", id, vec); err != nil {
			t.Fatal(err)
		}
	}
	idx.Train("docs", vectors)

	allowed := map[string]bool{"doc2": true, "doc3": true}
	query := []float32{1, 0, 0, 0}
	results := idx.SearchWithFilter("docs", query, 5, 0.0, allowed, nil)

	for _, r := range results {
		if r.DocID == "doc1" {
			t.Error("doc1 should have been filtered out")
		}
	}
}

func TestSQIndexQuantizationRange(t *testing.T) {
	idx := NewSQIndex()

	vectors := map[string][]float32{
		"doc1": {-10, 5, 0, 100},
		"doc2": {10, -5, 0, -100},
	}
	for id, vec := range vectors {
		if err := idx.Add("docs", id, vec); err != nil {
			t.Fatal(err)
		}
	}
	idx.Train("docs", vectors)

	c := idx.data["docs"]
	for _, code := range c.codes {
		if len(code) == 0 {
			t.Error("expected non-empty quantized code")
		}
	}
}

func TestSQIndexRemove(t *testing.T) {
	idx := NewSQIndex()
	idx.SetReady()

	vectors := map[string][]float32{
		"doc1": {1, 0, 0, 0},
		"doc2": {0, 1, 0, 0},
	}
	for id, vec := range vectors {
		if err := idx.Add("docs", id, vec); err != nil {
			t.Fatal(err)
		}
	}
	idx.Train("docs", vectors)

	idx.Remove("docs", "doc1")
	if idx.CollectionSize("docs") != 1 {
		t.Errorf("expected 1 doc after remove, got %d", idx.CollectionSize("docs"))
	}
}

func TestSQIndexEmptyCollection(t *testing.T) {
	idx := NewSQIndex()
	idx.SetReady()

	results := idx.Search("nonexistent", []float32{1, 0}, 5, 0.0, nil)
	if results != nil {
		t.Errorf("expected nil results for nonexistent collection")
	}

	if idx.CollectionSize("nonexistent") != 0 {
		t.Error("expected 0 size for nonexistent collection")
	}
}

func TestSQIndexCollections(t *testing.T) {
	idx := NewSQIndex()
	if err := idx.Add("a", "doc1", []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("b", "doc1", []float32{0, 1}); err != nil {
		t.Fatal(err)
	}

	colls := idx.Collections()
	if len(colls) != 2 {
		t.Errorf("expected 2 collections, got %d", len(colls))
	}
}

func TestSQIndexConcurrent(t *testing.T) {
	idx := NewSQIndex()
	idx.SetReady()

	vectors := map[string][]float32{
		"doc1": {1, 0, 0, 0},
		"doc2": {0, 1, 0, 0},
	}
	for id, vec := range vectors {
		if err := idx.Add("docs", id, vec); err != nil {
			t.Fatal(err)
		}
	}
	idx.Train("docs", vectors)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			query := []float32{1, 0, 0, 0}
			results := idx.Search("docs", query, 2, 0.0, nil)
			if len(results) == 0 {
				t.Error("expected results from concurrent search")
			}
		}()
	}
	wg.Wait()
}

func TestSQIndexScoreRange(t *testing.T) {
	idx := NewSQIndex()
	idx.SetReady()

	vectors := map[string][]float32{
		"doc1": {1, 0, 0, 0},
		"doc2": {0.5, 0.5, 0, 0},
	}
	for id, vec := range vectors {
		if err := idx.Add("docs", id, vec); err != nil {
			t.Fatal(err)
		}
	}
	idx.Train("docs", vectors)

	results := idx.Search("docs", []float32{1, 0, 0, 0}, 5, 0.0, nil)
	for _, r := range results {
		if r.Score < -1 || r.Score > 1 {
			t.Errorf("score %f out of cosine similarity range [-1, 1]", r.Score)
		}
		if math.IsNaN(float64(r.Score)) {
			t.Error("score is NaN")
		}
	}
}
