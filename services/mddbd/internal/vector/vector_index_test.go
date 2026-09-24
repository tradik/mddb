package vector

import (
	"math"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a, b     []float32
		expected float32
		delta    float32
	}{
		{
			name:     "identical vectors",
			a:        []float32{1, 0, 0},
			b:        []float32{1, 0, 0},
			expected: 1.0,
			delta:    0.001,
		},
		{
			name:     "orthogonal vectors",
			a:        []float32{1, 0, 0},
			b:        []float32{0, 1, 0},
			expected: 0.0,
			delta:    0.001,
		},
		{
			name:     "opposite vectors",
			a:        []float32{1, 0, 0},
			b:        []float32{-1, 0, 0},
			expected: -1.0,
			delta:    0.001,
		},
		{
			name:     "similar vectors",
			a:        []float32{1, 1, 0},
			b:        []float32{1, 0, 0},
			expected: float32(1 / math.Sqrt(2)),
			delta:    0.001,
		},
		{
			name:     "empty vectors",
			a:        []float32{},
			b:        []float32{},
			expected: 0,
			delta:    0.001,
		},
		{
			name:     "different lengths",
			a:        []float32{1, 0},
			b:        []float32{1, 0, 0},
			expected: 0,
			delta:    0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CosineSimilarity(tt.a, tt.b)
			if diff := float32(math.Abs(float64(got - tt.expected))); diff > tt.delta {
				t.Errorf("CosineSimilarity() = %v, want %v (diff %v)", got, tt.expected, diff)
			}
		})
	}
}

func TestVectorIndex_AddAndSearch(t *testing.T) {
	idx := NewVectorIndex()
	idx.SetReady()

	// Add some vectors
	if err := idx.Add("docs", "doc1", []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc2", []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc3", []float32{0.9, 0.1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc4", []float32{0, 0, 1}); err != nil {
		t.Fatal(err)
	}

	// Search for something close to doc1
	results := idx.Search("docs", []float32{1, 0, 0}, 3, 0.0, nil)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// doc1 should be first (exact match)
	if results[0].DocID != "doc1" {
		t.Errorf("expected doc1 first, got %s", results[0].DocID)
	}
	if results[0].Score < 0.99 {
		t.Errorf("expected score ~1.0, got %f", results[0].Score)
	}

	// doc3 should be second (most similar)
	if results[1].DocID != "doc3" {
		t.Errorf("expected doc3 second, got %s", results[1].DocID)
	}
}

func TestVectorIndex_SearchWithThreshold(t *testing.T) {
	idx := NewVectorIndex()
	idx.SetReady()

	if err := idx.Add("docs", "doc1", []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc2", []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add("docs", "doc3", []float32{0.9, 0.1, 0}); err != nil {
		t.Fatal(err)
	}

	// With high threshold, should only get very similar results
	results := idx.Search("docs", []float32{1, 0, 0}, 10, 0.9, nil)

	if len(results) != 2 {
		t.Fatalf("expected 2 results with threshold 0.9, got %d", len(results))
	}
}

func TestVectorIndex_SearchWithFilter(t *testing.T) {
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

	// Only allow doc2 and doc3
	allowed := map[string]bool{"doc2": true, "doc3": true}
	results := idx.SearchWithFilter("docs", []float32{1, 0, 0}, 10, 0.0, allowed, nil)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// doc2 should be first (more similar to query)
	if results[0].DocID != "doc2" {
		t.Errorf("expected doc2 first, got %s", results[0].DocID)
	}
}

func TestVectorIndex_Remove(t *testing.T) {
	idx := NewVectorIndex()
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
		t.Fatalf("expected 1 vector after remove, got %d", idx.CollectionSize("docs"))
	}
}

func TestVectorIndex_EmptyCollection(t *testing.T) {
	idx := NewVectorIndex()
	idx.SetReady()

	results := idx.Search("nonexistent", []float32{1, 0, 0}, 5, 0.0, nil)
	if len(results) != 0 {
		t.Fatalf("expected 0 results for empty collection, got %d", len(results))
	}
}
