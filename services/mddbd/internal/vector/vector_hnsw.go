package vector

import (
	"fmt"
	"strconv"
	"strings"

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
	deleted map[string]int
	// dims is the vector length a collection has settled on, taken from the
	// first vector it accepted. The library needs every node in a graph to
	// have the same length and panics when they differ (#252), so the index
	// has to know what that length is rather than find out by crashing.
	dims map[string]int
	// nodeKeys maps each live document to the key of its node in the graph.
	// A document's node is never deleted from the graph (#267): it is
	// superseded by a node under a new key, and a node whose key is not the
	// current one for its document is a tombstone that searches skip and the
	// next compaction drops. gen makes the keys unique.
	nodeKeys map[string]map[string]string
	gen      uint64
	ready    atomic.Bool
	m        int // max connections per node
	efSearch int // search beam width
}

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
		dims:     make(map[string]int),
		nodeKeys: make(map[string]map[string]string),
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
	// than deleting it (#267). The library's Delete leaves other nodes with
	// one-way edges to the node it removed, a search descending through one
	// lands on a node the layer below no longer has, and the next Add or
	// Search dereferences nil. Collections rewritten in place hit it hundreds
	// of times a week; each hit lost the chunk until a reindex.
	if exists {
		delete(h.nodeKeys[collection], docID)
		h.deleted[collection]++
	}

	key := h.nextNodeKey(docID)
	if panicked := addToGraph(g, key, vector); panicked != nil {
		slog.Warn("HNSW Add panicked; rebuilding the collection's graph",
			"collection", collection, "docID", docID, "panic", panicked)
		// The vector is in the live set, so a rebuild includes it. Only if
		// that fails too is the chunk missing from the graph, and the caller
		// is told.
		h.compactLocked(collection)
		if _, ok := h.nodeKeys[collection][docID]; !ok {
			return fmt.Errorf("hnsw add panicked: %v", panicked)
		}
		return nil
	}
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
	i := strings.LastIndexByte(nodeKey, 0)
	if i < 0 {
		return "", false
	}
	docID := nodeKey[:i]
	return docID, h.nodeKeys[collection][docID] == nodeKey
}

// addToGraph adds one node and returns what the library panicked with, or nil.
//
// CheckVector already keeps out the vectors that were known to panic it
// (#252), but the library can still panic on its own account — a
// half-tombstoned graph did, before compaction (GO-029). A separate function
// so the recovery can be exercised directly: the vectors that used to reach it
// through Add are now refused before they get here.
func addToGraph(g *hnsw.Graph[string], docID string, vector []float32) (panicked any) {
	defer func() { panicked = recover() }()
	g.Add(hnsw.MakeNode(docID, vector))
	return nil
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
		h.nodeKeys[collection] = make(map[string]string)
		h.deleted[collection] = 0
		return
	}
	keys := make(map[string]string, len(live))

	g := hnsw.NewGraph[string]()
	g.M = h.m
	g.EfSearch = h.efSearch
	skipped := 0

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
			// A rebuild must not be aborted by one unusable vector. Add
			// refuses these now, but a process that has been running since
			// before it did may still hold one (#252).
			if err := CheckVector(vec, h.dims[collection]); err != nil {
				skipped++
				continue
			}
			key := h.nextNodeKey(docID)
			g.Add(hnsw.MakeNode(key, vec))
			keys[docID] = key
		}
	}()
	if skipped > 0 {
		slog.Warn("HNSW compaction skipped vectors the graph cannot hold",
			"collection", collection, "skipped", skipped, "vectors", len(live))
	}

	if g == nil {
		return
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
		// The graph holds one dimension (#252); a query of another would
		// panic inside the library and be answered by brute force over
		// vectors it cannot be compared with.
		return nil
	}

	if topK <= 0 {
		topK = 5
	}
	if metric == nil {
		metric = CosineSimilarity
	}

	neighbors, ok := h.searchGraph(collection, g, query, h.withTombstones(collection, topK))
	if !ok {
		// The graph could not be searched; the live vectors are still here, so
		// answer from them rather than returning nothing.
		return h.bruteForceLocked(collection, query, topK, threshold, metric, nil)
	}

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
	if !ok || len(query) != h.dims[collection] {
		// The graph holds one dimension (#252); a query of another would
		// panic inside the library and be answered by brute force over
		// vectors it cannot be compared with.
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
		if coll != nil {
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
		if allowed != nil && !allowed[BaseDocID(docID)] {
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
