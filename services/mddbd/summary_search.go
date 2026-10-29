package main

import (
	"errors"
	"fmt"
	"maps"
	"sort"
	"strconv"

	"mddb/internal/fts"
	vec "mddb/internal/vector"
)

// Summaries on the search paths (RAG-009).
//
// Questions like "what is this document about" match the language of a
// summary, not of any one chunk. The collection's summaryMode decides what a
// summary does on a search, and every vector-backed path — vector search over
// REST, MCP, gRPC and GraphQL, and the vector half of hybrid — applies it here,
// on the chunk-level hits, before they are deduplicated, grouped or cut:
//
//   - off (the default): nothing; results are byte-for-byte today's.
//   - blend: a document scores the better of its best chunk and its summary.
//     A document only its summary matched joins with its best chunk.
//   - expand: blend, and a summary hit brings the document's best n chunks,
//     ranked against the query, at the summary's score. As passages
//     (AGT-002) they are one passage each; n is 3, fewer when the
//     collection's contextTokenBudget cannot hold n chunks for every result.
//
// The keyword side is the meta.summary field of BM25F, weighted by
// summaryWeight while summaryMode is not off.

// Summary modes.
const (
	SummaryModeOff    = "off"
	SummaryModeBlend  = "blend"
	SummaryModeExpand = "expand"
)

const (
	// defaultSummaryWeight is meta.summary's BM25F weight: as much as tags,
	// less than a title.
	defaultSummaryWeight = 2.0
	maxSummaryWeight     = 10.0
	// maxSummaryExpandChunks is how many chunks a summary hit brings in expand.
	maxSummaryExpandChunks = 3
	summaryField           = "meta." + summaryMetaKey
)

var errSummaryMode = errors.New("summaryMode must be off, blend or expand")

// validateSummarySettings refuses a mode or weight that cannot mean anything.
func validateSummarySettings(mode string, weight *float64) error {
	switch mode {
	case "", SummaryModeOff, SummaryModeBlend, SummaryModeExpand:
	default:
		return fmt.Errorf("%w, got %q", errSummaryMode, mode)
	}
	if weight != nil && (*weight < 0 || *weight > maxSummaryWeight) {
		return fmt.Errorf("summaryWeight %g is outside 0-%g", *weight, maxSummaryWeight)
	}
	return nil
}

// ResolveSummaryMode is override (an evaluation config's) > collection
// profile > off. Empty and "off" both come back as "".
func (s *Server) ResolveSummaryMode(collection, override string) string {
	mode := override
	if mode == "" {
		if p := s.RetrievalProfile(collection); p != nil {
			mode = p.SummaryMode
		}
	}
	if mode == SummaryModeOff {
		return ""
	}
	return mode
}

// summaryFieldWeights adds meta.summary to the BM25F weights while summaries
// are on — to the defaults when the request names none. A request that names
// meta.summary itself keeps its own weight, 0 included.
func (s *Server) summaryFieldWeights(collection, mode string, weights map[string]float64) map[string]float64 {
	if mode == "" {
		return weights
	}
	if _, set := weights[summaryField]; set {
		return weights
	}
	out := fts.DefaultBM25FWeights()
	if len(weights) > 0 {
		out = maps.Clone(weights)
	}
	out[summaryField] = defaultSummaryWeight
	if p := s.RetrievalProfile(collection); p != nil && p.SummaryWeight != nil {
		out[summaryField] = *p.SummaryWeight
	}
	return out
}

// summarySearch is the summary step of one vector search.
type summarySearch struct {
	Collection string
	Mode       string // resolved: "", blend or expand
	Query      []float32
	SearchTopK int
	TopK       int
	Threshold  float64
	Allowed    map[string]bool
	Filtered   bool
	Metric     vec.SimilarityFunc
}

// applySummaries merges summary hits into chunk-level results (keys
// "docID#N") and records expand's chunks on the plan, for the passages.
func (s *Server) applySummaries(ss summarySearch, results []vec.VectorResult, plan *SearchPlan) []vec.VectorResult {
	if ss.Mode == "" {
		return results
	}
	hits := s.searchSummaries(ss.Collection, ss.Query, ss.SearchTopK, ss.Threshold, ss.Allowed, ss.Filtered, ss.Metric)
	if len(hits) == 0 {
		return results
	}
	at := map[string]int{} // result key -> position
	best := map[string]int{}
	for i, r := range results {
		at[r.DocID] = i
		id := vec.BaseDocID(r.DocID)
		if j, ok := best[id]; !ok || r.Score > results[j].Score {
			best[id] = i
		}
	}
	n := 1
	if ss.Mode == SummaryModeExpand {
		n = s.summaryExpandChunks(ss.Collection, ss.TopK)
		plan.summaryChunks = map[string][]int{}
	}
	only := 0
	for _, h := range hits {
		i, found := best[h.DocID]
		if found {
			results[i].Score = max(results[i].Score, h.Score)
		} else {
			only++
		}
		if found && ss.Mode == SummaryModeBlend {
			continue // blend: a document its chunks found keeps its best chunk
		}
		chunks := s.bestChunks(ss.Collection, h.DocID, ss.Query, ss.Metric, n)
		if ss.Mode == SummaryModeExpand {
			plan.summaryChunks[h.DocID] = chunks
		}
		for _, c := range chunks {
			key := h.DocID + "#" + strconv.Itoa(c)
			if i, ok := at[key]; ok {
				results[i].Score = max(results[i].Score, h.Score)
				continue
			}
			at[key] = len(results)
			results = append(results, vec.VectorResult{DocID: key, Score: h.Score})
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	plan.reason("summaryMode %s: %d documents matched by their summaries, %d by nothing else", ss.Mode, len(hits), only)
	return results
}

// bestChunks ranks a document's chunks against the query and returns the
// indexes of the best n — from the flat index, or from disk for a collection
// whose float32 vectors are not in RAM. Chunk 0 when it has none to rank.
func (s *Server) bestChunks(collection, docID string, query []float32, metric vec.SimilarityFunc, n int) []int {
	vectors := s.VectorIndex.DocumentVectors(collection, docID)
	if len(vectors) == 0 {
		vectors = s.VectorStore.DocumentChunkVectors(collection, docID)
	}
	if len(vectors) == 0 {
		return []int{0}
	}
	if metric == nil {
		metric = vec.CosineSimilarity
	}
	idx := make([]int, len(vectors))
	scores := make([]float32, len(vectors))
	for i, v := range vectors {
		idx[i], scores[i] = i, metric(query, v)
	}
	sort.SliceStable(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
	return idx[:min(n, len(idx))]
}

// summaryExpandChunks is n for expand: maxSummaryExpandChunks, fewer when the
// collection's context budget cannot hold that many chunks for each of topK
// results — never fewer than one.
func (s *Server) summaryExpandChunks(collection string, topK int) int {
	budget := s.ContextTokenBudget(collection)
	if budget <= 0 {
		return maxSummaryExpandChunks
	}
	perChunk := max(s.chunkSizeFor(collection)/4, 1)
	return min(maxSummaryExpandChunks, max(budget/(perChunk*max(topK, 1)), 1))
}

// expandedChunks is the chunks summaryMode expand chose for a document, nil
// when there is no plan or it chose none.
func (p *SearchPlan) expandedChunks(docID string) []int {
	if p == nil {
		return nil
	}
	return p.summaryChunks[docID]
}
