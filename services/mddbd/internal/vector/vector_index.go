package vector

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// VectorResult represents a single vector search result.
type VectorResult struct {
	DocID string
	Score float32
}

// VectorIndex is an in-memory flat index for vector similarity search.
// It stores vectors per collection and performs brute-force cosine similarity.
// Keys may include chunk suffixes (e.g. "docID#0", "docID#1").
type VectorIndex struct {
	mu          sync.RWMutex
	collections map[string]map[string][]float32 // collection -> key -> vector
	ready       atomic.Bool
}

// NewVectorIndex creates a new empty vector index.
func NewVectorIndex() *VectorIndex {
	return &VectorIndex{
		collections: make(map[string]map[string][]float32),
	}
}

// IsReady returns whether the index has finished loading.
func (vi *VectorIndex) IsReady() bool {
	return vi.ready.Load()
}

// SetReady marks the index as ready for queries.
func (vi *VectorIndex) SetReady() {
	vi.ready.Store(true)
}

// Add inserts or updates a vector in the index.
func (vi *VectorIndex) Add(collection, docID string, vector []float32) error {
	// An empty vector is refused before it is stored anywhere (#252): it
	// cannot be compared with anything, and in the HNSW graph it poisoned
	// every add that followed it.
	if err := CheckVector(vector, 0); err != nil {
		return err
	}
	vi.mu.Lock()
	defer vi.mu.Unlock()

	if vi.collections[collection] == nil {
		vi.collections[collection] = make(map[string][]float32)
	}
	vi.collections[collection][docID] = vector
	return nil
}

// GetVector returns the stored vector for a doc/chunk ID, or nil if absent.
// For base doc IDs it falls back to the first chunk ("id#0"), so callers can
// resolve vectors for deduplicated (parent-level) results.
func (vi *VectorIndex) GetVector(collection, docID string) []float32 {
	vi.mu.RLock()
	defer vi.mu.RUnlock()

	coll, ok := vi.collections[collection]
	if !ok {
		return nil
	}
	if v, ok := coll[docID]; ok {
		return v
	}
	return coll[docID+"#0"]
}

// Remove deletes a vector from the index.
func (vi *VectorIndex) Remove(collection, docID string) {
	vi.mu.Lock()
	defer vi.mu.Unlock()

	if coll, ok := vi.collections[collection]; ok {
		delete(coll, docID)
	}
}

// Search finds the top-K most similar vectors to the query vector.
// Uses goroutine parallelism for collections above parallelSearchConfig.minSize.
//
// IMPORTANT: The RLock is released BEFORE the parallel scoring phase.
// snapshotMap() copies slice headers into an owned slice; subsequent
// concurrent Add/Remove calls swap whole vector entries, so the snapshot
// keeps old underlying arrays alive via their own references until
// scoring finishes. Holding the lock through parallelScore would serialize
// writers against every multi-millisecond search — defeating parallelism.
func (vi *VectorIndex) Search(collection string, query []float32, topK int, threshold float64, metric SimilarityFunc) []VectorResult {
	vi.mu.RLock()
	coll, ok := vi.collections[collection]
	if !ok || len(coll) == 0 {
		vi.mu.RUnlock()
		return nil
	}

	if topK <= 0 {
		topK = 5
	}
	if metric == nil {
		metric = CosineSimilarity
	}

	// Parallel path for large collections: snapshot under lock, score without it.
	if len(coll) >= parallelSearchConfig.MinSize() {
		entries := snapshotMap(coll)
		vi.mu.RUnlock()
		return parallelScore(entries, query, topK, threshold, metric, nil)
	}

	// Sequential path for small collections — scoring runs under the read lock
	// because the overhead of snapshotting + goroutine dispatch would dominate.
	defer vi.mu.RUnlock()
	results := make([]VectorResult, 0, len(coll))
	for docID, vec := range coll {
		score := metric(query, vec)
		if float64(score) >= threshold {
			results = append(results, VectorResult{DocID: docID, Score: score})
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

// SearchWithFilter performs vector search only on documents matching the given docID set.
// Handles chunk keys: if the index has "docID#0" and allowed has "docID", it matches.
// Uses goroutine parallelism for collections above parallelSearchConfig.minSize.
// See Search() for the locking contract — RLock is released before parallel scoring.
func (vi *VectorIndex) SearchWithFilter(collection string, query []float32, topK int, threshold float64, allowedDocIDs map[string]bool, metric SimilarityFunc) []VectorResult {
	vi.mu.RLock()
	coll, ok := vi.collections[collection]
	if !ok || len(coll) == 0 {
		vi.mu.RUnlock()
		return nil
	}

	if topK <= 0 {
		topK = 5
	}
	if metric == nil {
		metric = CosineSimilarity
	}

	filter := func(docID string) bool {
		return allowedDocIDs[BaseDocID(docID)]
	}

	// Parallel path for large collections: snapshot under lock, score without it.
	if len(coll) >= parallelSearchConfig.MinSize() {
		entries := snapshotMap(coll)
		vi.mu.RUnlock()
		return parallelScore(entries, query, topK, threshold, metric, filter)
	}

	// Sequential path for small collections — scoring runs under the read lock.
	defer vi.mu.RUnlock()
	results := make([]VectorResult, 0, min(len(allowedDocIDs), len(coll)))
	for docID, vec := range coll {
		if !filter(docID) {
			continue
		}
		score := metric(query, vec)
		if float64(score) >= threshold {
			results = append(results, VectorResult{DocID: docID, Score: score})
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
func (vi *VectorIndex) CollectionSize(collection string) int {
	vi.mu.RLock()
	defer vi.mu.RUnlock()

	return len(vi.collections[collection])
}

// Collections returns all collection names that have vectors.
func (vi *VectorIndex) Collections() []string {
	vi.mu.RLock()
	defer vi.mu.RUnlock()

	names := make([]string, 0, len(vi.collections))
	for name := range vi.collections {
		names = append(names, name)
	}
	return names
}

// Name returns the algorithm name.
func (vi *VectorIndex) Name() string {
	return "flat"
}

// SimilarityFunc computes similarity between two vectors.
// Higher values = more similar. Used as a configurable distance metric.
// Implementations are in vector_math_scalar.go (pure Go) and
// vector_math_arm64.go (NEON/SME accelerated).
type SimilarityFunc func(a, b []float32) float32

// ResolveSimilarity returns the SimilarityFunc for a given metric name.
// Defaults to CosineSimilarity if the name is unknown or empty.
func ResolveSimilarity(name string) SimilarityFunc {
	switch name {
	case "dot_product":
		return dotProductSimilarity
	case "euclidean":
		return euclideanSimilarity
	default:
		return CosineSimilarity
	}
}

// BaseDocID extracts the base document ID from a possibly chunked key.
// "docID#0" -> "docID", "docID" -> "docID"
func BaseDocID(key string) string {
	if idx := strings.IndexByte(key, '#'); idx >= 0 {
		return key[:idx]
	}
	return key
}

// DeduplicateChunkResults groups vector results by base docID and returns
// the highest score per document. Useful when chunks produce multiple results
// for the same document.
func DeduplicateChunkResults(results []VectorResult) []VectorResult {
	if len(results) == 0 {
		return results
	}

	best := make(map[string]float32)
	for _, r := range results {
		base := BaseDocID(r.DocID)
		if score, exists := best[base]; !exists || r.Score > score {
			best[base] = r.Score
		}
	}

	deduped := make([]VectorResult, 0, len(best))
	for docID, score := range best {
		deduped = append(deduped, VectorResult{DocID: docID, Score: score})
	}

	sort.Slice(deduped, func(i, j int) bool {
		return deduped[i].Score > deduped[j].Score
	})

	return deduped
}
