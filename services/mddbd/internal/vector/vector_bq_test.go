package vector

import (
	"math"
	"sync"
	"testing"
)

func TestBQIndexName(t *testing.T) {
	idx := NewBQIndex(10)
	if idx.Name() != "bq" {
		t.Errorf("expected name 'bq', got %s", idx.Name())
	}
}

func TestBQIndexReady(t *testing.T) {
	idx := NewBQIndex(10)
	if idx.IsReady() {
		t.Error("expected not ready initially")
	}
	idx.SetReady()
	if !idx.IsReady() {
		t.Error("expected ready after SetReady")
	}
}

func TestBQIndexBasicOps(t *testing.T) {
	idx := NewBQIndex(10)
	idx.SetReady()

	vectors := map[string][]float32{
		"doc1": {1, 0.5, 0.2, -0.1, 0, 0, 0, 0},
		"doc2": {0.9, 0.4, 0.1, -0.2, 0, 0, 0, 0},
		"doc3": {-1, -0.5, -0.2, 0.1, 0, 0, 0, 0},
		"doc4": {0, 0, 0, 0, 1, 0.5, 0.2, -0.1},
	}

	for id, vec := range vectors {
		if err := idx.Add("docs", id, vec); err != nil {
			t.Fatal(err)
		}
	}

	if idx.CollectionSize("docs") != 4 {
		t.Errorf("expected 4 docs, got %d", idx.CollectionSize("docs"))
	}

	query := []float32{1, 0.5, 0.2, -0.1, 0, 0, 0, 0}
	results := idx.Search("docs", query, 3, 0.0, nil)
	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}

	// doc1 should be exact match
	if results[0].DocID != "doc1" {
		t.Errorf("expected doc1 as top result, got %s", results[0].DocID)
	}
}

func TestBQIndexBinaryEncoding(t *testing.T) {
	vec := []float32{1.0, -0.5, 0.3, 0, -1.0, 0.1, -0.2, 0.0}
	code := encodeBQ(vec)

	// Expected: bits 0,2,5 set (positive values: 1.0, 0.3, 0.1)
	// Bit 0 = 1 (1.0 > 0), bit 2 = 1 (0.3 > 0), bit 5 = 1 (0.1 > 0)
	expected := uint64(1<<0 | 1<<2 | 1<<5)
	if code[0] != expected {
		t.Errorf("expected code %b, got %b", expected, code[0])
	}
}

func TestBQIndexHammingDistance(t *testing.T) {
	a := []uint64{0b1010}
	b := []uint64{0b1001}
	dist := hammingDistance(a, b)
	// 1010 XOR 1001 = 0011 -> 2 bits different
	if dist != 2 {
		t.Errorf("expected Hamming distance 2, got %d", dist)
	}

	// Identical vectors
	dist = hammingDistance(a, a)
	if dist != 0 {
		t.Errorf("expected Hamming distance 0 for identical, got %d", dist)
	}
}

func TestBQIndexNoTrainingRequired(t *testing.T) {
	idx := NewBQIndex(10)
	idx.SetReady()

	// BQ works immediately without Train()
	if err := idx.Add("docs", "doc1", []float32{1, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc2", []float32{0, 1, 0, 0}); err != nil {
		t.Fatal(err)
	}

	query := []float32{1, 0, 0, 0}
	results := idx.Search("docs", query, 5, 0.0, nil)
	if len(results) == 0 {
		t.Fatal("BQ should work without training")
	}
}

func TestBQIndexTrain(t *testing.T) {
	idx := NewBQIndex(10)
	idx.SetReady()

	vectors := map[string][]float32{
		"doc1": {1, 0, 0, 0},
		"doc2": {0, 1, 0, 0},
	}
	idx.Train("docs", vectors)

	if idx.CollectionSize("docs") != 2 {
		t.Errorf("expected 2 docs after train, got %d", idx.CollectionSize("docs"))
	}

	results := idx.Search("docs", []float32{1, 0, 0, 0}, 5, 0.0, nil)
	if len(results) == 0 {
		t.Fatal("expected results after training")
	}
}

func TestBQIndexSearchWithFilter(t *testing.T) {
	idx := NewBQIndex(10)
	idx.SetReady()

	if err := idx.Add("docs", "doc1", []float32{1, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc2", []float32{0.9, 0.1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc3", []float32{0, 1, 0, 0}); err != nil {
		t.Fatal(err)
	}

	allowed := map[string]bool{"doc2": true, "doc3": true}
	results := idx.SearchWithFilter("docs", []float32{1, 0, 0, 0}, 5, 0.0, allowed, nil)

	for _, r := range results {
		if r.DocID == "doc1" {
			t.Error("doc1 should have been filtered out")
		}
	}
}

func TestBQIndexRemove(t *testing.T) {
	idx := NewBQIndex(10)
	idx.SetReady()

	if err := idx.Add("docs", "doc1", []float32{1, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc2", []float32{0, 1, 0, 0}); err != nil {
		t.Fatal(err)
	}

	idx.Remove("docs", "doc1")
	if idx.CollectionSize("docs") != 1 {
		t.Errorf("expected 1 doc after remove, got %d", idx.CollectionSize("docs"))
	}
}

func TestBQIndexEmptyCollection(t *testing.T) {
	idx := NewBQIndex(10)
	idx.SetReady()

	results := idx.Search("nonexistent", []float32{1, 0}, 5, 0.0, nil)
	if results != nil {
		t.Errorf("expected nil results for nonexistent collection")
	}
}

func TestBQIndexCollections(t *testing.T) {
	idx := NewBQIndex(10)
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

func TestBQIndexConcurrent(t *testing.T) {
	idx := NewBQIndex(10)
	idx.SetReady()

	if err := idx.Add("docs", "doc1", []float32{1, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc2", []float32{0, 1, 0, 0}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results := idx.Search("docs", []float32{1, 0, 0, 0}, 2, 0.0, nil)
			if len(results) == 0 {
				t.Error("expected results from concurrent search")
			}
		}()
	}
	wg.Wait()
}

func TestBQIndexScoreRange(t *testing.T) {
	idx := NewBQIndex(10)
	idx.SetReady()

	if err := idx.Add("docs", "doc1", []float32{1, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc2", []float32{0.5, 0.5, 0, 0}); err != nil {
		t.Fatal(err)
	}

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

func TestBQIndexDefaultRerankFactor(t *testing.T) {
	idx := NewBQIndex(0) // should default to 10
	if idx.rerankFactor != 10 {
		t.Errorf("expected default rerank factor 10, got %d", idx.rerankFactor)
	}
}

func TestBQIndexLargeVector(t *testing.T) {
	idx := NewBQIndex(10)
	idx.SetReady()

	// Test with 128-dimensional vector (> 64 bits, needs 2 uint64 words)
	dim := 128
	v1 := make([]float32, dim)
	v2 := make([]float32, dim)
	for i := 0; i < dim; i++ {
		v1[i] = 1.0
		v2[i] = -1.0
	}

	if err := idx.Add("docs", "pos", v1); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "neg", v2); err != nil {
		t.Fatal(err)
	}

	results := idx.Search("docs", v1, 2, 0.0, nil)
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if results[0].DocID != "pos" {
		t.Errorf("expected pos as top result, got %s", results[0].DocID)
	}
}
