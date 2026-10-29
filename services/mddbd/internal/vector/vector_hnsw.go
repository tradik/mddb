package vector

import (
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// HNSWIndex implements VectorSearcher using Hierarchical Navigable Small World graphs.
// It provides O(log n) approximate nearest neighbor search.
type HNSWIndex struct {
	mu      sync.RWMutex
	graphs  map[string]*hnswGraph
	vectors map[string]map[string][]float32 // kept for SearchWithFilter fallback
	// deleted counts removals per collection since the graph was last rebuilt.
	// The graph cannot be asked how much tombstoned structure it carries, so
	// this is the only signal available for deciding when to compact (GO-029).
	deleted map[string]int
	// dims is the vector length a collection has settled on, taken from the
	// first vector it accepted. A graph compares every node with every other,
	// so it holds one length; a vector of another is refused (#252).
	dims map[string]int
	// nodeKeys maps each live document to the key of its node in the graph.
	// A document's node is never deleted from the graph (#267): it is
	// superseded by a node under a new key, and a node whose key is not the
	// current one for its document is a tombstone that searches skip and the
	// next compaction drops. gen makes the keys unique.
	nodeKeys       map[string]map[string]string
	gen            uint64
	ready          atomic.Bool
	m              int // links per node above layer 0; 2m on layer 0
	efConstruction int // candidates considered when a node is linked
	efSearch       int // search beam, raised to k when a search asks for more
}

// The defaults NewHNSWIndex falls back to, and the ones the server passes
// (SRCH-013). On the planner benchmark's clustered corpus — 32 dimensions,
// 100 topics, topK 10 — they give recall@10 of 1.000 at 20k and 200k vectors,
// in whichever order the documents arrive; see docs/BENCHMARK.md, "Vector
// query planner".
const (
	DefaultHNSWM              = 16
	DefaultHNSWEfConstruction = 200
	DefaultHNSWEfSearch       = 64
)

// hnswCompactRatio is the share of a collection's graph that may be
// tombstones before the graph is rebuilt from the live vectors.
//
// Until 2.15.4 deletion went through the library, which left the graph
// traversing nodes that were gone; past roughly half deleted a search
// dereferenced nil, and the ratio was 0.2 as a correctness guard. Nodes are no
// longer deleted (#267) — removed and overwritten documents become tombstones
// the search skips — so the ratio now only trades memory and search
// oversampling (at most 2x at 0.5) against rebuild cost. A rebuild costs one
// add per live vector; at 0.5 that is one extra add per overwrite, amortised,
// where 0.2 would have been four.
const hnswCompactRatio = 0.5

// NewHNSWIndex creates a new HNSW index: m links per node (2m on layer 0),
// efConstruction candidates when a node is linked, and a search beam of
// efSearch. Zero or less takes the default, as does an m under 2.
//
// Until 2.18.9 efConstruction was ignored: coder/hnsw had no such field and
// linked new nodes with the search beam (SRCH-013).
func NewHNSWIndex(m, efConstruction, efSearch int) *HNSWIndex {
	if m < 2 {
		m = DefaultHNSWM
	}
	if efConstruction <= 0 {
		efConstruction = DefaultHNSWEfConstruction
	}
	if efSearch <= 0 {
		efSearch = DefaultHNSWEfSearch
	}
	return &HNSWIndex{
		graphs:         make(map[string]*hnswGraph),
		vectors:        make(map[string]map[string][]float32),
		deleted:        make(map[string]int),
		dims:           make(map[string]int),
		nodeKeys:       make(map[string]map[string]string),
		m:              m,
		efConstruction: efConstruction,
		efSearch:       efSearch,
	}
}

// Name returns the algorithm name.
func (h *HNSWIndex) Name() string { return "hnsw" }

// IsReady returns whether the index is loaded.
func (h *HNSWIndex) IsReady() bool { return h.ready.Load() }

// SetReady marks the index as ready.
func (h *HNSWIndex) SetReady() { h.ready.Store(true) }

// newGraph is an empty graph with the index's parameters.
func (h *HNSWIndex) newGraph() *hnswGraph {
	return newHNSWGraph(h.m, h.efConstruction, h.efSearch)
}

func (h *HNSWIndex) getOrCreateGraph(collection string) *hnswGraph {
	g, ok := h.graphs[collection]
	if !ok {
		g = h.newGraph()
		h.graphs[collection] = g
	}
	return g
}

// Add inserts or updates a vector in the HNSW index.
//
// A vector the graph cannot hold is refused before anything is written, and the
// reason is returned rather than logged and forgotten (#252). Refusing early is
// what keeps one unusable vector from becoming the entry point every later add
// is compared against.
func (h *HNSWIndex) Add(collection, docID string, vector []float32) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if err := CheckVector(vector, h.dims[collection]); err != nil {
		slog.Warn("vector refused by the HNSW index; this chunk will not be searchable",
			"collection", collection, "docID", docID, "err", err)
		return err
	}

	g := h.getOrCreateGraph(collection)

	// Store for filter support
	if h.vectors[collection] == nil {
		h.vectors[collection] = make(map[string][]float32)
		h.nodeKeys[collection] = make(map[string]string)
	}
	_, exists := h.vectors[collection][docID]
	h.vectors[collection][docID] = vector
	h.dims[collection] = len(vector)

	// An overwrite leaves the old node in the graph as a tombstone rather
	// than deleting it (#267). Deleting a node from a navigable graph leaves
	// its neighbours with edges into nothing; coder/hnsw, the graph until
	// 2.18.9, dereferenced nil on them hundreds of times a week.
	if exists {
		delete(h.nodeKeys[collection], docID)
		h.deleted[collection]++
	}

	key := h.nextNodeKey(docID)
	g.Add(key, vector)
	h.nodeKeys[collection][docID] = key

	if h.shouldCompactLocked(collection) {
		h.compactLocked(collection)
	}
	return nil
}

// nextNodeKey is a graph key for docID that no earlier node has used.
// Caller must hold the write lock.
func (h *HNSWIndex) nextNodeKey(docID string) string {
	h.gen++
	return docID + "\x00" + strconv.FormatUint(h.gen, 36)
}

// liveDocID resolves a graph node to the document it stands for, or reports
// that the node is a tombstone. Caller must hold at least the read lock.
func (h *HNSWIndex) liveDocID(collection, nodeKey string) (string, bool) {
	// Every key nextNodeKey makes has the separator; a key without one would
	// resolve to "" and still not be the live key of any document.
	docID := nodeKey[:max(strings.LastIndexByte(nodeKey, 0), 0)]
	return docID, h.nodeKeys[collection][docID] == nodeKey
}

// Remove deletes a vector from the index.
func (h *HNSWIndex) Remove(collection, docID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// The node stays in the graph as a tombstone; see Add (#267).
	delete(h.nodeKeys[collection], docID)
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
	// With nothing left alive there is no graph worth keeping.
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
		h.nodeKeys[collection] = make(map[string]string)
		h.deleted[collection] = 0
		return
	}

	// Sorted, so a rebuild of the same vectors is the same graph: recall must
	// not change with Go's map iteration order (SRCH-013).
	docIDs := make([]string, 0, len(live))
	for docID := range live {
		docIDs = append(docIDs, docID)
	}
	sort.Strings(docIDs)

	g := h.newGraph()
	keys := make(map[string]string, len(live))
	skipped := 0
	for _, docID := range docIDs {
		vec := live[docID]
		// A rebuild must not be aborted by one unusable vector. Add refuses
		// these now, but a process that has been running since before it did
		// may still hold one (#252).
		if err := CheckVector(vec, h.dims[collection]); err != nil {
			skipped++
			continue
		}
		key := h.nextNodeKey(docID)
		g.Add(key, vec)
		keys[docID] = key
	}
	if skipped > 0 {
		slog.Warn("HNSW compaction skipped vectors the graph cannot hold",
			"collection", collection, "skipped", skipped, "vectors", len(live))
	}

	h.graphs[collection] = g
	h.nodeKeys[collection] = keys
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
	if !ok || len(query) != h.dims[collection] {
		// The graph holds one dimension (#252); a query of another cannot be
		// compared with anything in it.
		return nil
	}

	if topK <= 0 {
		topK = 5
	}
	if metric == nil {
		metric = CosineSimilarity
	}

	neighbors := g.Search(query, h.withTombstones(collection, topK))

	// Overwritten and removed documents leave tombstones in the graph (see
	// Add); nodeKeys decides which nodes are alive (GO-029, #267).
	results := make([]VectorResult, 0, len(neighbors))
	for _, n := range neighbors {
		docID, alive := h.liveDocID(collection, n.Key)
		if !alive {
			continue
		}
		score := metric(query, n.Value)
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

// withTombstones widens a request for k neighbours by the share of the graph
// that is tombstones, so a search still returns k live documents. Compaction
// keeps that share under hnswCompactRatio. Caller must hold the read lock.
func (h *HNSWIndex) withTombstones(collection string, k int) int {
	live, dead := len(h.vectors[collection]), h.deleted[collection]
	if dead == 0 || live == 0 {
		return k
	}
	return k*(live+dead)/live + 1
}

// HNSWFilterOversample is how many times topK a filtered HNSW search takes
// from the graph before filtering. A filter admitting less than its inverse
// of the collection leaves fewer than topK, and the search falls back to
// scoring the collection — which is why the planner sends such filters to the
// prefilter instead (SRCH-013).
const HNSWFilterOversample = 3

// SearchWithFilter performs HNSW search filtered by allowed doc IDs.
// Strategy: oversample HNSWFilterOversample× from HNSW, then filter. If
// insufficient results, fall back to brute-force on the allowed set.
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
	if !ok || len(query) != h.dims[collection] {
		// The graph holds one dimension (#252); a query of another cannot be
		// compared with anything in it.
		return nil
	}

	// Oversample: fetch HNSWFilterOversample × topK from HNSW, then filter
	oversample := max(topK*HNSWFilterOversample, 50)
	neighbors := g.Search(query, oversample)

	// Tombstones come back from the graph too (see Search), so the live map
	// decides here as well.
	results := make([]VectorResult, 0, topK)
	for _, n := range neighbors {
		docID, alive := h.liveDocID(collection, n.Key)
		if !alive || !allowed[BaseDocID(docID)] {
			continue
		}
		score := metric(query, n.Value)
		if float64(score) >= threshold {
			results = append(results, VectorResult{DocID: docID, Score: score})
		}
	}

	// If not enough results from HNSW, fall back to brute-force on allowed set
	if len(results) < topK {
		coll := h.vectors[collection]
		seen := make(map[string]bool, len(results))
		for _, r := range results {
			seen[r.DocID] = true
		}
		for docID, vec := range coll {
			if seen[docID] || !allowed[BaseDocID(docID)] {
				continue
			}
			score := metric(query, vec)
			if float64(score) >= threshold {
				results = append(results, VectorResult{DocID: docID, Score: score})
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
