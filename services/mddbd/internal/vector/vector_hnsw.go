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
	mu      sync.RWMutex
	graphs  map[string]*hnsw.Graph[string]
	vectors map[string]map[string][]float32 // kept for SearchWithFilter fallback
	// deleted counts removals per collection since the graph was last rebuilt.
	// The graph cannot be asked how much tombstoned structure it carries, so
	// this is the only signal available for deciding when to compact (GO-029).
	deleted  map[string]int
	ready    atomic.Bool
	m        int // max connections per node
	efSearch int // search beam width
}

// hnswCompactRatio is the share of a collection that may be deleted before the
// graph is rebuilt from the live vectors. Deletion leaves the coder/hnsw graph
// traversing structure whose nodes are gone, and past roughly half deleted its
// search dereferences a nil node and panics — so this is a correctness guard,
// not only a performance one. A rebuild is O(n log n) and amortised across the
// deletions that triggered it.
const hnswCompactRatio = 0.2

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
		deleted:  make(map[string]int),
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
		if _, existed := coll[docID]; existed {
			h.deleted[collection]++
		}
		delete(coll, docID)
	}

	if h.shouldCompactLocked(collection) {
		h.compactLocked(collection)
	}
}

// shouldCompactLocked reports whether enough of a collection has been deleted
// to justify rebuilding its graph. Caller must hold the write lock.
func (h *HNSWIndex) shouldCompactLocked(collection string) bool {
	live := len(h.vectors[collection])
	gone := h.deleted[collection]
	if gone == 0 {
		return false
	}
	// With nothing left alive there is no graph worth keeping, and an
	// all-deleted graph is exactly the shape that panics on search.
	if live == 0 {
		return true
	}
	return float64(gone) >= float64(live+gone)*hnswCompactRatio
}

// compactLocked rebuilds a collection's graph from the vectors still alive,
// discarding whatever the deletions left behind. Caller must hold the write
// lock.
func (h *HNSWIndex) compactLocked(collection string) {
	live := h.vectors[collection]
	if len(live) == 0 {
		delete(h.graphs, collection)
		h.deleted[collection] = 0
		return
	}

	g := hnsw.NewGraph[string]()
	g.M = h.m
	g.EfSearch = h.efSearch

	func() {
		// The library panics on some small-graph shapes (see Add); a failed
		// rebuild must not take the process with it. The old graph is still
		// referenced until the assignment below, so a failure leaves the
		// index exactly as it was.
		defer func() {
			if r := recover(); r != nil {
				slog.Warn("HNSW compaction recovered from a panic in the graph library",
					"collection", collection, "vectors", len(live), "panic", r)
				g = nil
			}
		}()
		for docID, vec := range live {
			g.Add(hnsw.MakeNode(docID, vec))
		}
	}()

	if g == nil {
		return
	}
	h.graphs[collection] = g
	h.deleted[collection] = 0
	slog.Debug("HNSW graph compacted", "collection", collection, "vectors", len(live))
}

// Compact rebuilds a collection's graph from its live vectors regardless of
// how much has been deleted, so an operator can reclaim a degraded graph on
// demand.
func (h *HNSWIndex) Compact(collection string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.compactLocked(collection)
}

// DeletedSince reports how many vectors have been removed from a collection
// since its graph was last rebuilt — the debt an operator would want to see.
func (h *HNSWIndex) DeletedSince(collection string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.deleted[collection]
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

	neighbors, ok := h.searchGraph(collection, g, query, topK)
	if !ok {
		// The graph could not be searched; the live vectors are still here, so
		// answer from them rather than returning nothing.
		return h.bruteForceLocked(collection, query, topK, threshold, metric, nil)
	}

	// coder/hnsw v0.6.1 keeps returning nodes that Delete reported as removed:
	// Delete says true and Len() drops, yet Search still hands the node back.
	// h.vectors is the authoritative record of what is alive, so results are
	// checked against it — otherwise a vector search answers with documents
	// the caller deleted (GO-029).
	live := h.vectors[collection]
	results := make([]VectorResult, 0, len(neighbors))
	for _, n := range neighbors {
		if _, alive := live[n.Key]; !alive {
			continue
		}
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
	neighbors, searched := h.searchGraph(collection, g, query, oversample)
	if !searched {
		return h.bruteForceLocked(collection, query, topK, threshold, metric, allowed)
	}

	// Deleted nodes keep coming back from the graph library (see Search), so
	// the live map decides here too.
	liveVectors := h.vectors[collection]
	results := make([]VectorResult, 0, topK)
	for _, n := range neighbors {
		if _, alive := liveVectors[n.Key]; !alive {
			continue
		}
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

// searchGraph calls into the graph library, converting a panic into a failed
// search.
//
// coder/hnsw dereferences a nil node when a graph has had a large share of its
// vectors deleted — reproducibly at roughly half of a 1000-vector collection.
// Compaction keeps graphs away from that shape, but a search must not be able
// to kill the process if some other shape reaches it: an HTTP request would
// only be caught by the panic middleware, and a gRPC or MCP search would take
// the server down.
func (h *HNSWIndex) searchGraph(collection string, g *hnsw.Graph[string], query []float32, topK int) (neighbors []hnsw.Node[string], ok bool) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("HNSW search recovered from a panic in the graph library; falling back to brute force",
				"collection", collection, "panic", r)
			neighbors, ok = nil, false
		}
	}()
	return g.Search(query, topK), true
}

// bruteForceLocked scores every live vector in a collection. Caller must hold
// at least the read lock. allowed may be nil to consider every vector.
func (h *HNSWIndex) bruteForceLocked(collection string, query []float32, topK int, threshold float64, metric SimilarityFunc, allowed map[string]bool) []VectorResult {
	coll, ok := h.vectors[collection]
	if !ok {
		return nil
	}
	results := make([]VectorResult, 0, topK)
	for docID, vec := range coll {
		if allowed != nil && !allowed[docID] {
			continue
		}
		score := metric(query, vec)
		if float64(score) >= threshold {
			results = append(results, VectorResult{DocID: docID, Score: score})
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > topK {
		results = results[:topK]
	}
	return results
}
