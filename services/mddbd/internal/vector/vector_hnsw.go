package vector

import (
	"github.com/coder/hnsw"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
)

// HNSWIndex implements VectorSearcher using Hierarchical Navigable Small World graphs.
// It provides O(log n) approximate nearest neighbor search.
type HNSWIndex struct {
	mu       sync.RWMutex
	graphs   map[string]*hnsw.Graph[string]
	vectors  map[string]map[string][]float32 // kept for SearchWithFilter fallback
	ready    atomic.Bool
	m        int // max connections per node
	efSearch int // search beam width
}

// NewHNSWIndex creates a new HNSW index with the given parameters.
// The second parameter is unused (kept for API compatibility).
func NewHNSWIndex(m, _ int, efSearch int) *HNSWIndex {
	if m <= 0 {
		m = 16
	}
	if efSearch <= 0 {
		efSearch = 100
	}
	return &HNSWIndex{
		graphs:   make(map[string]*hnsw.Graph[string]),
		vectors:  make(map[string]map[string][]float32),
		m:        m,
		efSearch: efSearch,
	}
}

// Name returns the algorithm name.
func (h *HNSWIndex) Name() string { return "hnsw" }

// IsReady returns whether the index is loaded.
func (h *HNSWIndex) IsReady() bool { return h.ready.Load() }

// SetReady marks the index as ready.
func (h *HNSWIndex) SetReady() { h.ready.Store(true) }

func (h *HNSWIndex) getOrCreateGraph(collection string) *hnsw.Graph[string] {
	g, ok := h.graphs[collection]
	if !ok {
		g = hnsw.NewGraph[string]()
		g.M = h.m
		g.EfSearch = h.efSearch
		h.graphs[collection] = g
	}
	return g
}

// Add inserts or updates a vector in the HNSW index.
func (h *HNSWIndex) Add(collection, docID string, vector []float32) {
	h.mu.Lock()
	defer h.mu.Unlock()

	g := h.getOrCreateGraph(collection)

	// Store for filter support
	if h.vectors[collection] == nil {
		h.vectors[collection] = make(map[string][]float32)
	}
	_, exists := h.vectors[collection][docID]
	h.vectors[collection][docID] = vector

	if exists {
		g.Delete(docID)
	}

	// Recover from panics in the hnsw library (known issue with empty/small graphs)
	defer func() {
		if r := recover(); r != nil {
			// Log but don't crash — flat index still works as fallback
			slog.Info("HNSW Add recovered from panic", "collection", collection, "docID", docID, "r", r)
		}
	}()
	node := hnsw.MakeNode(docID, vector)
	g.Add(node)
}

// Remove deletes a vector from the index.
func (h *HNSWIndex) Remove(collection, docID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if g, ok := h.graphs[collection]; ok {
		g.Delete(docID)
	}
	if coll, ok := h.vectors[collection]; ok {
		delete(coll, docID)
	}
}

// Search finds the top-K most similar vectors using HNSW.
func (h *HNSWIndex) Search(collection string, query []float32, topK int, threshold float64, metric SimilarityFunc) []VectorResult {
	h.mu.RLock()
	defer h.mu.RUnlock()

	g, ok := h.graphs[collection]
	if !ok {
		return nil
	}

	if topK <= 0 {
		topK = 5
	}
	if metric == nil {
		metric = CosineSimilarity
	}

	neighbors := g.Search(query, topK)

	results := make([]VectorResult, 0, len(neighbors))
	for _, n := range neighbors {
		score := metric(query, n.Value)
		if float64(score) >= threshold {
			results = append(results, VectorResult{DocID: n.Key, Score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results
}

// SearchWithFilter performs HNSW search filtered by allowed doc IDs.
// Strategy: oversample 3x from HNSW, then filter. If insufficient results,
// fall back to brute-force on the allowed set.
func (h *HNSWIndex) SearchWithFilter(collection string, query []float32, topK int, threshold float64, allowed map[string]bool, metric SimilarityFunc) []VectorResult {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if topK <= 0 {
		topK = 5
	}
	if metric == nil {
		metric = CosineSimilarity
	}

	g, ok := h.graphs[collection]
	if !ok {
		return nil
	}

	// Oversample: fetch 3x topK from HNSW, then filter
	oversample := topK * 3
	if oversample < 50 {
		oversample = 50
	}
	neighbors := g.Search(query, oversample)

	results := make([]VectorResult, 0, topK)
	for _, n := range neighbors {
		if !allowed[BaseDocID(n.Key)] {
			continue
		}
		score := metric(query, n.Value)
		if float64(score) >= threshold {
			results = append(results, VectorResult{DocID: n.Key, Score: score})
		}
	}

	// If not enough results from HNSW, fall back to brute-force on allowed set
	if len(results) < topK {
		coll := h.vectors[collection]
		if coll != nil {
			seen := make(map[string]bool, len(results))
			for _, r := range results {
				seen[r.DocID] = true
			}
			for docID, vec := range coll {
				if seen[docID] || !allowed[docID] {
					continue
				}
				score := metric(query, vec)
				if float64(score) >= threshold {
					results = append(results, VectorResult{DocID: docID, Score: score})
				}
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > topK {
		results = results[:topK]
	}

	return results
}

// CollectionSize returns the number of vectors in a collection.
func (h *HNSWIndex) CollectionSize(collection string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.vectors[collection])
}

// Collections returns all collection names.
func (h *HNSWIndex) Collections() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	names := make([]string, 0, len(h.vectors))
	for name := range h.vectors {
		names = append(names, name)
	}
	return names
}
