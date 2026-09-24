package vector

import (
	"testing"
)

// TestVectorSearcherInterface verifies all implementations satisfy the interface
func TestVectorSearcherInterface(t *testing.T) {
	implementations := []struct {
		name     string
		searcher VectorSearcher
	}{
		{"FlatIndex", NewVectorIndex()},
		{"HNSWIndex", NewHNSWIndex(16, 200, 100)},
		{"IVFIndex", NewIVFIndex(10, 20)},
		{"PQIndex", NewPQIndex(8, 256, 20)},
	}

	for _, impl := range implementations {
		t.Run(impl.name, func(t *testing.T) {
			s := impl.searcher

			// Test Name
			if s.Name() == "" {
				t.Error("Name() returned empty string")
			}

			// Test IsReady/SetReady
			if s.IsReady() {
				t.Error("expected IsReady() to be false initially")
			}
			s.SetReady()
			if !s.IsReady() {
				t.Error("expected IsReady() to be true after SetReady()")
			}

			// Test Add
			if err := s.Add("test", "doc1", []float32{1, 0, 0}); err != nil {
				t.Fatal(err)
			}
			if s.CollectionSize("test") != 1 {
				t.Errorf("expected collection size 1, got %d", s.CollectionSize("test"))
			}

			// Test Remove
			s.Remove("test", "doc1")
			if s.CollectionSize("test") != 0 {
				t.Errorf("expected collection size 0 after remove, got %d", s.CollectionSize("test"))
			}

			// Test Collections
			if err := s.Add("col1", "doc1", []float32{1, 0}); err != nil {
				t.Fatal(err)
			}
			if err := s.Add("col2", "doc2", []float32{0, 1}); err != nil {
				t.Fatal(err)
			}
			cols := s.Collections()
			if len(cols) < 2 {
				t.Errorf("expected at least 2 collections, got %d", len(cols))
			}
		})
	}
}

// TestFlatIndexBasicOps tests basic FlatIndex operations
func TestFlatIndexBasicOps(t *testing.T) {
	idx := NewVectorIndex()
	idx.SetReady()

	if idx.Name() != "flat" {
		t.Errorf("expected name 'flat', got %s", idx.Name())
	}

	// Add vectors
	if err := idx.Add("docs", "doc1", []float32{1, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc2", []float32{0, 1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc3", []float32{0, 0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc4", []float32{1, 1, 0, 0}); err != nil {
		t.Fatal(err)
	}

	if idx.CollectionSize("docs") != 4 {
		t.Fatalf("expected 4 vectors, got %d", idx.CollectionSize("docs"))
	}

	// Search
	query := []float32{1, 0, 0, 0}
	results := idx.Search("docs", query, 3, 0.0, nil)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Verify results are sorted by score descending
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: [%d].Score=%f > [%d].Score=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}

	// doc1 should be first (exact match)
	if results[0].DocID != "doc1" {
		t.Errorf("expected doc1 first, got %s", results[0].DocID)
	}
}

// TestFlatIndexSearchWithFilter tests filtered search
func TestFlatIndexSearchWithFilter(t *testing.T) {
	idx := NewVectorIndex()
	idx.SetReady()

	if err := idx.Add("docs", "doc1", []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc2", []float32{0.9, 0.1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc3", []float32{0.8, 0.2, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc4", []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}

	// Filter to only doc2 and doc3
	allowed := map[string]bool{"doc2": true, "doc3": true}
	query := []float32{1, 0, 0}
	results := idx.SearchWithFilter("docs", query, 10, 0.0, allowed, nil)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Verify only allowed docs are returned
	for _, r := range results {
		if !allowed[r.DocID] {
			t.Errorf("unexpected doc %s in filtered results", r.DocID)
		}
	}

	// doc2 should be first (more similar)
	if results[0].DocID != "doc2" {
		t.Errorf("expected doc2 first, got %s", results[0].DocID)
	}
}

// TestHNSWIndexBasicOps tests HNSW index operations
func TestHNSWIndexBasicOps(t *testing.T) {
	idx := NewHNSWIndex(16, 200, 100)
	idx.SetReady()

	if idx.Name() != "hnsw" {
		t.Errorf("expected name 'hnsw', got %s", idx.Name())
	}

	// Add vectors
	if err := idx.Add("docs", "doc1", []float32{1, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc2", []float32{0, 1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc3", []float32{0, 0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc4", []float32{0.9, 0.1, 0, 0}); err != nil {
		t.Fatal(err)
	}

	if idx.CollectionSize("docs") != 4 {
		t.Fatalf("expected 4 vectors, got %d", idx.CollectionSize("docs"))
	}

	// Search
	query := []float32{1, 0, 0, 0}
	results := idx.Search("docs", query, 3, 0.0, nil)

	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	// Verify results are sorted by score descending
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: [%d].Score=%f > [%d].Score=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}

	// doc1 should be first (exact match)
	if results[0].DocID != "doc1" {
		t.Errorf("expected doc1 first, got %s", results[0].DocID)
	}
}

// TestHNSWIndexSearchWithFilter tests HNSW filtered search
func TestHNSWIndexSearchWithFilter(t *testing.T) {
	idx := NewHNSWIndex(16, 200, 100)
	idx.SetReady()

	// Add multiple vectors
	if err := idx.Add("docs", "doc1", []float32{1, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc2", []float32{0.9, 0.1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc3", []float32{0, 1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc4", []float32{0, 0, 1, 0}); err != nil {
		t.Fatal(err)
	}

	// Search with filter allowing only doc2 and doc3
	allowed := map[string]bool{"doc2": true, "doc3": true}
	query := []float32{1, 0, 0, 0}
	results := idx.SearchWithFilter("docs", query, 10, 0.0, allowed, nil)

	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	// Verify only allowed docs are returned
	for _, r := range results {
		if !allowed[r.DocID] {
			t.Errorf("unexpected doc %s in filtered results", r.DocID)
		}
	}

	// doc2 should be first (more similar to query and allowed)
	if results[0].DocID != "doc2" {
		t.Logf("Note: expected doc2 first, got %s (HNSW approximate results may vary)", results[0].DocID)
	}
}

// TestIVFIndexBasicOps tests IVF index operations
func TestIVFIndexBasicOps(t *testing.T) {
	idx := NewIVFIndex(2, 10)
	idx.SetReady()

	if idx.Name() != "ivf" {
		t.Errorf("expected name 'ivf', got %s", idx.Name())
	}

	// Add vectors
	vectors := map[string][]float32{
		"doc1": {1, 0, 0, 0},
		"doc2": {0.9, 0.1, 0, 0},
		"doc3": {0, 1, 0, 0},
		"doc4": {0, 0.9, 0.1, 0},
		"doc5": {0, 0, 1, 0},
		"doc6": {0, 0, 0.9, 0.1},
	}

	for id, vec := range vectors {
		if err := idx.Add("docs", id, vec); err != nil {
			t.Fatal(err)
		}
	}

	// Train the index
	trainVecs := make(map[string][]float32)
	for id, vec := range vectors {
		trainVecs[id] = vec
	}
	idx.Train("docs", trainVecs)

	if idx.CollectionSize("docs") != 6 {
		t.Fatalf("expected 6 vectors, got %d", idx.CollectionSize("docs"))
	}

	// Search
	query := []float32{1, 0, 0, 0}
	results := idx.Search("docs", query, 3, 0.0, nil)

	if len(results) == 0 {
		t.Fatal("expected at least one result after training")
	}

	// Verify results are sorted by score descending
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: [%d].Score=%f > [%d].Score=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}

// TestIVFIndexUntrained tests that untrained IVF returns empty results
func TestIVFIndexUntrained(t *testing.T) {
	idx := NewIVFIndex(2, 10)
	idx.SetReady()

	if err := idx.Add("docs", "doc1", []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc2", []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}

	// Search without training should return nil
	query := []float32{1, 0, 0}
	results := idx.Search("docs", query, 10, 0.0, nil)

	if results != nil {
		t.Error("expected nil results from untrained IVF index")
	}
}

// TestPQIndexBasicOps tests PQ index operations
func TestPQIndexBasicOps(t *testing.T) {
	idx := NewPQIndex(4, 16, 10)
	idx.SetReady()

	if idx.Name() != "pq" {
		t.Errorf("expected name 'pq', got %s", idx.Name())
	}

	// Add vectors (need enough for meaningful clustering)
	vectors := map[string][]float32{
		"doc1": {1, 0, 0, 0, 0, 0, 0, 0},
		"doc2": {0.9, 0.1, 0, 0, 0, 0, 0, 0},
		"doc3": {0, 1, 0, 0, 0, 0, 0, 0},
		"doc4": {0, 0.9, 0.1, 0, 0, 0, 0, 0},
		"doc5": {0, 0, 1, 0, 0, 0, 0, 0},
		"doc6": {0, 0, 0.9, 0.1, 0, 0, 0, 0},
		"doc7": {0, 0, 0, 0, 1, 0, 0, 0},
		"doc8": {0, 0, 0, 0, 0, 1, 0, 0},
	}

	for id, vec := range vectors {
		if err := idx.Add("docs", id, vec); err != nil {
			t.Fatal(err)
		}
	}

	// Train the index
	trainVecs := make(map[string][]float32)
	for id, vec := range vectors {
		trainVecs[id] = vec
	}
	idx.Train("docs", trainVecs)

	if idx.CollectionSize("docs") != 8 {
		t.Fatalf("expected 8 vectors, got %d", idx.CollectionSize("docs"))
	}

	// Search
	query := []float32{1, 0, 0, 0, 0, 0, 0, 0}
	results := idx.Search("docs", query, 3, 0.0, nil)

	if len(results) == 0 {
		t.Fatal("expected at least one result after training")
	}

	// Verify results are sorted by score descending
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: [%d].Score=%f > [%d].Score=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}

// TestPQIndexUntrained tests that untrained PQ returns empty results
func TestPQIndexUntrained(t *testing.T) {
	idx := NewPQIndex(4, 16, 10)
	idx.SetReady()

	if err := idx.Add("docs", "doc1", []float32{1, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc2", []float32{0, 1, 0, 0}); err != nil {
		t.Fatal(err)
	}

	// Search without training should return nil
	query := []float32{1, 0, 0, 0}
	results := idx.Search("docs", query, 10, 0.0, nil)

	if results != nil {
		t.Error("expected nil results from untrained PQ index")
	}
}

// TestVectorSearchersWithThreshold tests threshold filtering
func TestVectorSearchersWithThreshold(t *testing.T) {
	implementations := []struct {
		name     string
		searcher VectorSearcher
		trained  bool
	}{
		{"FlatIndex", NewVectorIndex(), false},
		{"HNSWIndex", NewHNSWIndex(16, 200, 100), false},
	}

	for _, impl := range implementations {
		t.Run(impl.name, func(t *testing.T) {
			idx := impl.searcher
			idx.SetReady()

			if err := idx.Add("docs", "doc1", []float32{1, 0, 0}); err != nil {
				t.Fatal(err)
			}
			if err := idx.Add("docs", "doc2", []float32{0.7, 0.7, 0}); err != nil {
				t.Fatal(err)
			}
			if err := idx.Add("docs", "doc3", []float32{0, 1, 0}); err != nil {
				t.Fatal(err)
			}

			query := []float32{1, 0, 0}

			// High threshold should filter out dissimilar results
			results := idx.Search("docs", query, 10, 0.9, nil)

			if len(results) == 0 {
				t.Fatal("expected at least one result with threshold 0.9")
			}

			// Verify all results meet the threshold
			for _, r := range results {
				if float64(r.Score) < 0.9 {
					t.Errorf("result %s has score %f below threshold 0.9", r.DocID, r.Score)
				}
			}
		})
	}
}

// TestVectorSearchersEmptyCollection tests behavior with empty collections
func TestVectorSearchersEmptyCollection(t *testing.T) {
	implementations := []struct {
		name     string
		searcher VectorSearcher
	}{
		{"FlatIndex", NewVectorIndex()},
		{"HNSWIndex", NewHNSWIndex(16, 200, 100)},
		{"IVFIndex", NewIVFIndex(2, 10)},
		{"PQIndex", NewPQIndex(4, 16, 10)},
		{"OPQIndex", NewOPQIndex(4, 16, 10, 3)},
	}

	for _, impl := range implementations {
		t.Run(impl.name, func(t *testing.T) {
			idx := impl.searcher
			idx.SetReady()

			query := []float32{1, 0, 0}
			results := idx.Search("nonexistent", query, 10, 0.0, nil)

			if len(results) != 0 {
				t.Errorf("expected 0 results for empty collection, got %d", len(results))
			}
		})
	}
}

// TestVectorSearchersTopK tests topK limiting
func TestVectorSearchersTopK(t *testing.T) {
	idx := NewVectorIndex()
	idx.SetReady()

	// Add 10 vectors
	for i := 0; i < 10; i++ {
		vec := []float32{float32(i) / 10.0, 0, 0}
		if err := idx.Add("docs", string(rune('a'+i)), vec); err != nil {
			t.Fatal(err)
		}
	}

	query := []float32{1, 0, 0}

	// Request only top 3
	results := idx.Search("docs", query, 3, 0.0, nil)

	if len(results) != 3 {
		t.Errorf("expected 3 results with topK=3, got %d", len(results))
	}

	// Request more than available
	results = idx.Search("docs", query, 100, 0.0, nil)

	if len(results) != 10 {
		t.Errorf("expected 10 results when requesting 100, got %d", len(results))
	}
}

// TestVectorSearchersRemove tests remove functionality
func TestVectorSearchersRemove(t *testing.T) {
	implementations := []struct {
		name     string
		searcher VectorSearcher
	}{
		{"FlatIndex", NewVectorIndex()},
		{"HNSWIndex", NewHNSWIndex(16, 200, 100)},
		{"IVFIndex", NewIVFIndex(2, 10)},
		{"PQIndex", NewPQIndex(4, 16, 10)},
		{"OPQIndex", NewOPQIndex(4, 16, 10, 3)},
	}

	for _, impl := range implementations {
		t.Run(impl.name, func(t *testing.T) {
			idx := impl.searcher
			idx.SetReady()

			if err := idx.Add("docs", "doc1", []float32{1, 0, 0}); err != nil {
				t.Fatal(err)
			}
			if err := idx.Add("docs", "doc2", []float32{0, 1, 0}); err != nil {
				t.Fatal(err)
			}

			if idx.CollectionSize("docs") != 2 {
				t.Fatalf("expected 2 vectors, got %d", idx.CollectionSize("docs"))
			}

			idx.Remove("docs", "doc1")

			if idx.CollectionSize("docs") != 1 {
				t.Errorf("expected 1 vector after remove, got %d", idx.CollectionSize("docs"))
			}

			// Remove non-existent should not error
			idx.Remove("docs", "nonexistent")
		})
	}
}

// TestVectorSearchersCollections tests collection management
func TestVectorSearchersCollections(t *testing.T) {
	idx := NewVectorIndex()
	idx.SetReady()

	if err := idx.Add("blog", "doc1", []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc2", []float32{0, 1}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("news", "doc3", []float32{1, 1}); err != nil {
		t.Fatal(err)
	}

	cols := idx.Collections()

	if len(cols) != 3 {
		t.Errorf("expected 3 collections, got %d", len(cols))
	}

	// Verify all collections are present
	colMap := make(map[string]bool)
	for _, col := range cols {
		colMap[col] = true
	}

	expected := []string{"blog", "docs", "news"}
	for _, exp := range expected {
		if !colMap[exp] {
			t.Errorf("expected collection %s not found", exp)
		}
	}
}

// TestTrainableInterface tests the Trainable interface
func TestTrainableInterface(t *testing.T) {
	trainables := []struct {
		name     string
		searcher interface{}
	}{
		{"IVFIndex", NewIVFIndex(2, 10)},
		{"PQIndex", NewPQIndex(4, 16, 10)},
		{"OPQIndex", NewOPQIndex(4, 16, 10, 3)},
	}

	for _, tr := range trainables {
		t.Run(tr.name, func(t *testing.T) {
			trainable, ok := tr.searcher.(Trainable)
			if !ok {
				t.Fatalf("%s does not implement Trainable interface", tr.name)
			}

			// Train with empty vectors should not panic
			trainable.Train("test", map[string][]float32{})

			// Train with actual vectors
			vectors := map[string][]float32{
				"doc1": {1, 0, 0, 0},
				"doc2": {0, 1, 0, 0},
				"doc3": {0, 0, 1, 0},
			}
			trainable.Train("test", vectors)

			// After training, the searcher should work
			searcher := tr.searcher.(VectorSearcher)
			query := []float32{1, 0, 0, 0}
			results := searcher.Search("test", query, 3, 0.0, nil)

			if len(results) == 0 {
				t.Error("expected results after training")
			}
		})
	}
}

// TestVectorResultScoreRange tests that scores are in valid range
func TestVectorResultScoreRange(t *testing.T) {
	idx := NewVectorIndex()
	idx.SetReady()

	if err := idx.Add("docs", "doc1", []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc2", []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc3", []float32{-1, 0, 0}); err != nil {
		t.Fatal(err)
	}

	query := []float32{1, 0, 0}
	results := idx.Search("docs", query, 10, -2.0, nil)

	for _, r := range results {
		// Cosine similarity should be in range [-1, 1]
		if r.Score < -1.0 || r.Score > 1.0 {
			t.Errorf("score %f out of valid range [-1, 1] for doc %s", r.Score, r.DocID)
		}
	}
}

// TestVectorSearchersConcurrentOps tests thread safety (basic smoke test)
func TestVectorSearchersConcurrentOps(t *testing.T) {
	idx := NewVectorIndex()
	idx.SetReady()

	// Pre-populate
	for i := 0; i < 10; i++ {
		if err := idx.Add("docs", string(rune('a'+i)), []float32{float32(i), 0, 0}); err != nil {
			t.Fatal(err)
		}
	}

	// Concurrent searches (should not panic)
	done := make(chan bool, 5)
	for i := 0; i < 5; i++ {
		go func() {
			query := []float32{1, 0, 0}
			_ = idx.Search("docs", query, 5, 0.0, nil)
			done <- true
		}()
	}

	for i := 0; i < 5; i++ {
		<-done
	}
}

// TestVectorSearchersZeroTopK tests behavior with topK=0
func TestVectorSearchersZeroTopK(t *testing.T) {
	idx := NewVectorIndex()
	idx.SetReady()

	if err := idx.Add("docs", "doc1", []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc2", []float32{0, 1}); err != nil {
		t.Fatal(err)
	}

	query := []float32{1, 0}
	results := idx.Search("docs", query, 0, 0.0, nil)

	// topK=0 should default to some reasonable number (typically 5)
	if len(results) == 0 {
		t.Error("expected results with topK=0 (should use default)")
	}
}
