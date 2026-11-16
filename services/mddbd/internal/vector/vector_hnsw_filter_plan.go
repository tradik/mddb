package vector

import (
	"math"
	"sort"
)

// How HNSW answers a filtered search (SRCH-024): the graph walk of
// vector_hnsw_filter.go, or an exact scan of what the filter admits.

// The strategies a filtered index search can take, as explain names them.
const (
	// FilterStrategyGraph: the filter is applied while walking the graph.
	FilterStrategyGraph = "graph+filter"
	// FilterStrategyPrefilter: the admitted documents are scored exactly.
	FilterStrategyPrefilter = "prefilter"
)

// What each way of answering a filter costs, in units of one admitted
// document scored by the exact scan (SRCH-024). Measured on the benchmark
// harness's corpus (TEST-005: 128 dimensions, topics of 200 documents, topK
// 30 as the server asks for 10); docs/SEARCH.md, "Filtered search", has the
// tables.
//
//   - The exact scan costs one unit per admitted document: 60–62 ns.
//   - The walk passes about 1/share nodes per admitted one it keeps, so it
//     costs walkCost(vectors) / share. The constant grows with the graph's
//     depth and spread: about 800 at 20k vectors (0.10 ms at 50%) and 2000
//     at 200k (1.03 ms at 10%, 0.46 ms at 25%). walkCost interpolates it as
//     2000 × (vectors / 200k)^0.4.
//   - IVF pays 1.4 units per vector in the clusters it probes: 97 ns a vector
//     at 20k and 74 ns at 200k in-process, and through the server at 20k and
//     10% it answered in 0.59 ms where the exact scan took 0.72.
//
// The walk and the scan then cross at 20% of 20k vectors, 10% of 200k and
// 5% of 2M. On corpora with larger topics the walk pays earlier (6% of 200k
// with topics of 2,000), so the estimate errs towards the scan, which is
// exact.
const (
	walkCostAt200k = 2000
	ivfScanCost    = 1.4
)

// walkCost is the constant of the walk's cost for a graph of n vectors.
func walkCost(n int) float64 {
	return walkCostAt200k * math.Pow(float64(max(n, 1))/200_000, 0.4)
}

// FilterCosts estimates what each way of answering a filter costs, for the
// planner to compare. TopK is the number of candidates asked for.
type FilterCosts struct {
	Vectors, Admitted, TopK int
}

// share is the part of the collection the filter admits.
func (c FilterCosts) share() float64 {
	if c.Vectors <= 0 {
		return 0
	}
	return float64(c.Admitted) / float64(c.Vectors)
}

// Prefilter is the exact scan: one unit per admitted document.
func (c FilterCosts) Prefilter() float64 { return float64(c.Admitted) }

// Graph is the filtered walk.
func (c FilterCosts) Graph() float64 {
	if s := c.share(); s > 0 {
		return walkCost(c.Vectors) / s
	}
	return math.Inf(1)
}

// IVF is a filtered IVF search with the server's nProbe: the clusters
// filteredProbes takes, at what the index measured per vector probed.
func (c FilterCosts) IVF() float64 {
	s := c.share()
	if s <= 0 {
		return math.Inf(1)
	}
	probed := max(float64(DefaultIVFNProbe*c.Vectors/ivfClusters(c.Vectors)), float64(ivfFilterDepth*c.TopK)/s)
	return ivfScanCost * min(probed, float64(c.Vectors))
}

// FilterQuery is one filtered index search.
type FilterQuery struct {
	Collection string
	Query      []float32
	TopK       int
	Threshold  float64
	Allowed    map[string]bool
	Metric     SimilarityFunc
}

// FilterOutcome is a filtered search's results and how it got them.
type FilterOutcome struct {
	Results  []VectorResult
	Strategy string
	// Fallback: the graph walk came back short and the admitted documents
	// were scored exactly after it.
	Fallback bool
}

// FilterSearcher is an index whose filtered search reports the strategy it
// took, for explain and for the fallback metric.
type FilterSearcher interface {
	FilterSearch(q FilterQuery) FilterOutcome
}

// HNSWFilterStrategy is the strategy HNSW takes for a filter admitting
// admitted documents of a collection of vectors: the walk unless the exact
// scan is estimated cheaper.
func HNSWFilterStrategy(admitted, vectors int) string {
	c := FilterCosts{Vectors: vectors, Admitted: admitted}
	if c.Prefilter() < c.Graph() {
		return FilterStrategyPrefilter
	}
	return FilterStrategyGraph
}

// HNSWFilterMinShare is the smallest share of a collection of vectors a
// filter must admit for HNSW to walk its graph, where HNSWFilterStrategy
// changes: √(walkCost / vectors).
func HNSWFilterMinShare(vectors int) float64 {
	if vectors <= 0 {
		return 1
	}
	return min(1, math.Sqrt(walkCost(vectors)/float64(vectors)))
}

// SearchWithFilter implements VectorSearcher; see FilterSearch.
func (h *HNSWIndex) SearchWithFilter(collection string, query []float32, topK int, threshold float64, allowed map[string]bool, metric SimilarityFunc) []VectorResult {
	return h.FilterSearch(FilterQuery{Collection: collection, Query: query, TopK: topK,
		Threshold: threshold, Allowed: allowed, Metric: metric}).Results
}

// FilterSearch searches only among the documents q.Allowed admits: by
// walking the graph with the filter, or — for a filter admitting too little
// for the walk to pay — by scoring the admitted documents exactly.
//
// Until 2.19.8 a filtered search that came back short scored the whole
// collection. The walk comes back short only when the filter admits fewer
// vectors than topK, or the graph is in pieces; the admitted documents are
// scored exactly then, and the outcome says so for the metric.
func (h *HNSWIndex) FilterSearch(q FilterQuery) FilterOutcome {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if q.TopK <= 0 {
		q.TopK = 5
	}
	if q.Metric == nil {
		q.Metric = CosineSimilarity
	}
	g, ok := h.graphs[q.Collection]
	if !ok || len(q.Query) != h.dims[q.Collection] {
		// The graph holds one dimension (#252); a query of another cannot be
		// compared with anything in it.
		return FilterOutcome{}
	}

	exact := admittedScan{coll: h.vectors[q.Collection], chunks: h.chunkKeys[q.Collection], allowed: q.Allowed,
		query: q.Query, topK: q.TopK, threshold: q.Threshold, metric: q.Metric}
	if HNSWFilterStrategy(len(q.Allowed), len(h.vectors[q.Collection])) == FilterStrategyPrefilter {
		return FilterOutcome{Results: exact.run(), Strategy: FilterStrategyPrefilter}
	}
	share := float64(len(q.Allowed)) / float64(len(h.vectors[q.Collection]))
	hits := g.SearchFiltered(q.Query, q.TopK, filteredEf(h.efSearch, q.TopK, share), h.admitter(g, q))
	if len(hits) < q.TopK {
		return FilterOutcome{Results: exact.run(), Strategy: FilterStrategyGraph, Fallback: true}
	}
	nodeKeys := h.nodeKeys[q.Collection]
	results := make([]VectorResult, 0, len(hits))
	for _, n := range hits {
		if score := q.Metric(q.Query, n.Value); float64(score) >= q.Threshold {
			docID, _ := liveNode(nodeKeys, n.Key)
			results = append(results, VectorResult{DocID: docID, Score: score})
		}
	}
	// The graph orders by cosine; the metric may not.
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return FilterOutcome{Results: results, Strategy: FilterStrategyGraph}
}

// filteredEf is the walk's beam, in admitted nodes: efSearch × √share, and
// never under k. The beam counts admitted nodes, so filling it costs about
// beam ÷ share nodes walked; a beam of efSearch admitted nodes at 10% walks
// as far as ten unfiltered searches. Measured at 200k vectors, topK 10
// (docs/SEARCH.md): a beam of 64 took 2.77, 1.02 and 0.56 ms at 10, 25 and
// 50%, a beam of 64 × √share took 0.75, 0.47 and 0.42 ms, both at recall@10
// 1.000; a beam of topK alone fell to 0.972 at 50%.
func filteredEf(efSearch, k int, share float64) int {
	return max(k, int(math.Ceil(float64(efSearch)*math.Sqrt(min(max(share, 0), 1)))))
}

// admitter is the walk's question for one node: is it a live node of a
// document the filter admits. Without tombstones every node is live, and the
// question is one lookup. Caller holds the read lock.
func (h *HNSWIndex) admitter(g *hnswGraph, q FilterQuery) func(id int32) bool {
	nodeKeys := h.nodeKeys[q.Collection]
	if len(nodeKeys) == g.Len() {
		return func(id int32) bool {
			return q.Allowed[BaseDocID(nodeDocID(g.keys[id]))]
		}
	}
	return func(id int32) bool {
		docID, alive := liveNode(nodeKeys, g.keys[id])
		return alive && q.Allowed[BaseDocID(docID)]
	}
}
