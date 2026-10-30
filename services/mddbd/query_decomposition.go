package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	vec "mddb/internal/vector"
)

// Query decomposition (SRCH-020). A question with two needs — "how do I set
// up replication and how does backup work" — gets one query vector that sits
// between the topics, so the pages answering each need lose to a page vaguely
// near both. With queryDecomposition "auto" a query is split into its clauses
// (sentences, and coordinated clauses joined by "and"); each part is embedded
// and searched, and a document keeps its best score over the whole query and
// the parts (max fusion).
//
// No language model runs in the server: the split is the rule the SRCH-004
// spike measured. The whole query stays one of the searches, which is what
// keeps an ordinary question that happens to contain "and" where it was —
// see docs/BENCHMARK.md, SRCH-020, for the numbers behind each choice (max
// over RRF, no similarity gate between the parts).
//
// Every vector-backed search path — REST, MCP, gRPC, GraphQL, evaluation —
// reaches this file through queryVectors and searchQueryVectors; with "off"
// (the default) both return exactly what the path did before.

// Query decomposition modes.
const (
	QueryDecompositionOff  = "off"  // one query vector (default)
	QueryDecompositionAuto = "auto" // split a multi-clause query and fuse the parts' results
)

// maxQueryParts bounds the parts a query is split into: one embedding call
// and one index search each. Clauses past the bound join the last part.
const maxQueryParts = 4

// DecompositionOptions is the query decomposition field of a vector-backed
// search request.
type DecompositionOptions struct {
	// QueryDecomposition is "off" (default) or "auto".
	QueryDecomposition string `json:"queryDecomposition,omitempty"`
}

// ValidateDecomposition refuses a mode the server does not know.
func (o DecompositionOptions) ValidateDecomposition() error {
	switch o.QueryDecomposition {
	case "", QueryDecompositionOff, QueryDecompositionAuto:
		return nil
	}
	return fmt.Errorf("queryDecomposition %q: must be off or auto", o.QueryDecomposition)
}

// QueryDecomposition is what decomposition did, reported in the plan when
// the request set explain.
type QueryDecomposition struct {
	// Parts is how many parts the query was searched as, besides the whole
	// query; 1 means it was not split.
	Parts int `json:"parts"`
	// Texts are the parts, when there was more than one.
	Texts []string `json:"texts,omitempty"`
}

// splitQueryParts splits a query at sentence punctuation and at the word
// "and" between clauses. A one-word fragment carries no topic and joins the
// part before it; parts past maxQueryParts join the last one. A query with
// nothing to split comes back whole, as one part.
func splitQueryParts(q string) []string {
	isStop := func(r rune) bool { return strings.ContainsRune(".,;:?!", r) }
	var parts []string
	for _, sentence := range strings.FieldsFunc(q, isStop) {
		var cur []string
		for _, w := range strings.Fields(sentence) {
			if strings.EqualFold(w, "and") && len(cur) > 1 {
				parts, cur = append(parts, strings.Join(cur, " ")), nil
				continue
			}
			cur = append(cur, w)
		}
		switch {
		case len(cur) > 1 || len(parts) == 0:
			parts = append(parts, strings.Join(cur, " "))
		case len(cur) == 1:
			parts[len(parts)-1] += " " + cur[0]
		}
	}
	if len(parts) <= 1 {
		return []string{q}
	}
	if len(parts) > maxQueryParts {
		parts[maxQueryParts-1] = strings.Join(parts[maxQueryParts-1:], " ")
		parts = parts[:maxQueryParts]
	}
	return parts
}

// queryVectors are the vectors a search runs with: the whole query's, then —
// when the request asks for decomposition and the query splits — one per
// part, corrected by relevance feedback the way the whole query was. Parts
// need the query's text and the server's provider: a search given its query
// vector is not split. Each part costs one embedding call; like the whole
// query's, it is not counted against the collection's embedding budget
// (GO-043), which meters document embeddings only.
func (s *Server) queryVectors(ctx context.Context, o DecompositionOptions, query string, whole []float32,
	fb *feedback, plan *SearchPlan) ([][]float32, error) {
	vectors := [][]float32{whole}
	if o.QueryDecomposition != QueryDecompositionAuto {
		return vectors, nil
	}
	d := &QueryDecomposition{Parts: 1}
	plan.Decomposition = d
	if query == "" || s.Embedding == nil {
		plan.reason("queryDecomposition auto: the query vector was given, so there is no text to split")
		return vectors, nil
	}
	parts := splitQueryParts(query)
	if len(parts) < 2 {
		return vectors, nil
	}
	for _, part := range parts {
		v, err := s.embedQuery(ctx, part)
		if err != nil {
			return nil, fmt.Errorf("%w: part %q: %w", errEmbedQuery, part, err)
		}
		vectors = append(vectors, fb.correct(v))
	}
	d.Parts, d.Texts = len(parts), parts
	plan.reason("queryDecomposition auto: searched the whole query and %d parts; each document keeps its best score", len(parts))
	return vectors, nil
}

// searchQueryVectors runs search once per query vector and fuses the lists:
// a hit keeps its best score over the vectors, ties go to the lower ID, and
// the fused list is cut back to limit — the candidates one search returns.
// With one vector it is that search, untouched.
func searchQueryVectors(vectors [][]float32, limit int, search func([]float32) []vec.VectorResult) []vec.VectorResult {
	if len(vectors) == 1 {
		return search(vectors[0])
	}
	best := map[string]float32{}
	for _, v := range vectors {
		for _, r := range search(v) {
			if cur, ok := best[r.DocID]; !ok || r.Score > cur {
				best[r.DocID] = r.Score
			}
		}
	}
	out := make([]vec.VectorResult, 0, len(best))
	for id, score := range best {
		out = append(out, vec.VectorResult{DocID: id, Score: score})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].DocID < out[j].DocID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// searchVectorPath is the index search of vector search (REST, and the
// direct client MCP, gRPC, GraphQL and evaluation share): the planned search
// per query vector, each rescored from disk first in a disk-only collection,
// then fused. q describes the search; its Vector is ignored, each of vectors
// is searched in turn. It returns the full-precision vectors the disk rescore
// read, nil for an in-memory collection.
func (s *Server) searchVectorPath(ps plannedSearch, q indexQuery, vectors [][]float32) ([]vec.VectorResult, map[string][]float32) {
	var diskVecs map[string][]float32
	results := searchQueryVectors(vectors, q.TopK, func(v []float32) []vec.VectorResult {
		one := q
		one.Vector = v
		found := s.runPlanned(ps, one)
		if s.collectionDiskOnly(q.Collection) {
			var fetched map[string][]float32
			found, fetched = s.rescoreFromDisk(q.Collection, v, found, q.Metric)
			diskVecs = mergeVectors(diskVecs, fetched)
		}
		return found
	})
	return results, diskVecs
}

// mergeVectors adds the vectors in src to dst and returns dst; a nil dst
// becomes src itself, so a search with one query vector keeps the map the
// disk rescore returned.
func mergeVectors(dst, src map[string][]float32) map[string][]float32 {
	if dst == nil {
		return src
	}
	for id, v := range src {
		dst[id] = v
	}
	return dst
}

// textQuery is the text a search may split: none when the caller sent its
// own query vector, which the parts' vectors could not be compared with.
func textQuery(query string, queryVector []float32) string {
	if len(queryVector) > 0 {
		return ""
	}
	return query
}
