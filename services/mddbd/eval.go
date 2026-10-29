package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

// Retrieval evaluation (SRCH-019).
//
// "Decide by numbers" needs numbers from the caller's own data. A collection
// keeps an evaluation set — queries with the documents that should answer
// them — and POST /v1/eval runs it under several search configurations and
// reports recall@k, nDCG@k, MRR, latency and returned tokens for each. No
// model judges anything: relevance is what the set says it is.

const (
	maxEvalQueries = 200
	maxEvalConfigs = 8
	maxEvalK       = 100
	defaultEvalK   = 10
)

// EvalQuery is one query and the documents (by key) that should answer it.
type EvalQuery struct {
	ID           string   `json:"id"`
	Query        string   `json:"query"`
	ExpectedKeys []string `json:"expectedKeys"`
	// Grades, when given, rate relevance 1..3 per key for nDCG; a key in
	// ExpectedKeys without a grade counts 1.
	Grades map[string]int `json:"grades,omitempty"`
}

// Validate rejects a query that cannot be scored.
func (q EvalQuery) Validate() error {
	if q.Query == "" || len(q.ExpectedKeys) == 0 {
		return errors.New("an evaluation query needs query and expectedKeys")
	}
	for k, g := range q.Grades {
		if g < 0 || g > 3 {
			return fmt.Errorf("grade for %q must be 0..3", k)
		}
	}
	return nil
}

// EvalConfig is one search configuration to measure.
type EvalConfig struct {
	Name            string   `json:"name,omitempty"`
	SearchType      string   `json:"searchType"` // fts, vector, hybrid
	Algorithm       string   `json:"algorithm,omitempty"`
	VectorAlgorithm string   `json:"vectorAlgorithm,omitempty"`
	Alpha           *float64 `json:"alpha,omitempty"`
	Strategy        string   `json:"strategy,omitempty"`
	RetrievalMode   string   `json:"retrievalMode,omitempty"`
	// BM25K1 and BM25B try BM25 parameters on fts and hybrid (SRCH-018).
	BM25K1 *float64 `json:"bm25K1,omitempty"`
	BM25B  *float64 `json:"bm25B,omitempty"`
}

func (c EvalConfig) label() string {
	if c.Name != "" {
		return c.Name
	}
	l := c.SearchType
	if c.Algorithm != "" {
		l += "/" + c.Algorithm
	}
	if c.Alpha != nil {
		l += fmt.Sprintf(" alpha=%.2f", *c.Alpha)
	}
	if c.BM25K1 != nil {
		l += fmt.Sprintf(" k1=%g", *c.BM25K1)
	}
	if c.BM25B != nil {
		l += fmt.Sprintf(" b=%g", *c.BM25B)
	}
	return l
}

// EvalRequest runs a collection's evaluation set (or inline queries).
type EvalRequest struct {
	Collection string       `json:"collection"`
	K          int          `json:"k,omitempty"`
	Configs    []EvalConfig `json:"configs,omitempty"`
	// Queries replaces the stored set for this run — how a set kept in a
	// repository is evaluated without storing it.
	Queries []EvalQuery `json:"queries,omitempty"`
	// Details adds per-query results.
	Details bool `json:"details,omitempty"`
}

// EvalQueryResult is one query under one configuration.
type EvalQueryResult struct {
	ID       string   `json:"id"`
	Query    string   `json:"query"`
	Recall   float64  `json:"recall"`
	NDCG     float64  `json:"ndcg"`
	RR       float64  `json:"reciprocalRank"`
	Returned []string `json:"returned"`
}

// EvalConfigResult is one configuration's scores, averaged over the queries.
type EvalConfigResult struct {
	Config   string            `json:"config"`
	Recall   float64           `json:"recallAtK"`
	NDCG     float64           `json:"ndcgAtK"`
	MRR      float64           `json:"mrr"`
	P50Ms    float64           `json:"p50Ms"`
	P99Ms    float64           `json:"p99Ms"`
	Tokens   int               `json:"tokens"`
	Error    string            `json:"error,omitempty"`
	PerQuery []EvalQueryResult `json:"perQuery,omitempty"`
}

// EvalResult is the answer to POST /v1/eval.
type EvalResult struct {
	Collection string             `json:"collection"`
	K          int                `json:"k"`
	Queries    int                `json:"queries"`
	Results    []EvalConfigResult `json:"results"`
}

// defaultEvalConfigs compares the three search types with their defaults.
func defaultEvalConfigs() []EvalConfig {
	return []EvalConfig{{SearchType: SearchTypeFTS, Algorithm: "bm25"}, {SearchType: SearchTypeHybrid}, {SearchType: SearchTypeVector}}
}

// RunEval scores each configuration on the evaluation set. It reads only.
func (s *Server) RunEval(ctx context.Context, req EvalRequest) (*EvalResult, error) {
	if req.Collection == "" {
		return nil, errors.New("collection is required")
	}
	queries := req.Queries
	if len(queries) == 0 {
		queries = s.evalSet(req.Collection)
	}
	if len(queries) == 0 {
		return nil, errors.New("no evaluation queries: add some with POST /v1/eval-queries or pass queries")
	}
	if len(queries) > maxEvalQueries || len(req.Configs) > maxEvalConfigs {
		return nil, fmt.Errorf("at most %d queries and %d configs per run", maxEvalQueries, maxEvalConfigs)
	}
	for _, q := range queries {
		if err := q.Validate(); err != nil {
			return nil, err
		}
	}
	k := req.K
	if k <= 0 {
		k = defaultEvalK
	}
	k = min(k, maxEvalK)
	configs := req.Configs
	if len(configs) == 0 {
		configs = defaultEvalConfigs()
	}
	out := &EvalResult{Collection: req.Collection, K: k, Queries: len(queries)}
	for _, c := range configs {
		out.Results = append(out.Results, s.evalConfig(ctx, req.Collection, k, c, queries, req.Details))
	}
	return out, nil
}

// evalConfig runs every query under one configuration.
func (s *Server) evalConfig(ctx context.Context, collection string, k int, c EvalConfig, queries []EvalQuery, details bool) EvalConfigResult {
	res := EvalConfigResult{Config: c.label()}
	var lat []float64
	for _, q := range queries {
		start := time.Now()
		keys, tokens, err := s.evalSearch(ctx, collection, k, c, q.Query)
		lat = append(lat, float64(time.Since(start).Microseconds())/1000)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		r := scoreQuery(q, keys, k)
		res.Recall += r.Recall
		res.NDCG += r.NDCG
		res.MRR += r.RR
		res.Tokens += tokens
		if details {
			res.PerQuery = append(res.PerQuery, r)
		}
	}
	n := float64(len(queries))
	res.Recall, res.NDCG, res.MRR = round3(res.Recall/n), round3(res.NDCG/n), round3(res.MRR/n)
	res.P50Ms, res.P99Ms = percentile(lat, 0.5), percentile(lat, 0.99)
	return res
}

// evalSearch runs one query and returns the ranked document keys and the
// estimated tokens of what came back.
func (s *Server) evalSearch(ctx context.Context, collection string, k int, c EvalConfig, query string) ([]string, int, error) {
	var keys []string
	tokens := 0
	switch c.SearchType {
	case SearchTypeFTS:
		resp, err := s.searchFTS(FTSSearchRequest{Collection: collection, Query: query, Limit: k, Algorithm: c.Algorithm,
			BM25K1: c.BM25K1, BM25B: c.BM25B, skipContextBudget: true}, time.Now())
		if err != nil {
			return nil, 0, err
		}
		for _, r := range resp.Results {
			keys, tokens = append(keys, r.Document.Key), tokens+approxTokens(r.Document.ContentMD)
		}
	case SearchTypeVector:
		resp, err := NewDirectClient(s).VectorSearch(ctx, &MCPVectorSearchRequest{Collection: collection, Query: query, TopK: k,
			Algorithm: c.VectorAlgorithm, RetrievalMode: c.RetrievalMode, IncludeContent: true, noGain: true})
		if err != nil {
			return nil, 0, err
		}
		for _, r := range resp.Results {
			keys, tokens = append(keys, r.Document.Key), tokens+approxTokens(r.Document.ContentMD)
		}
	case SearchTypeHybrid:
		items, _, err := s.hybridItems(ctx, HybridSearchRequest{Collection: collection, Query: query, TopK: k,
			Algorithm: c.Algorithm, VectorAlgorithm: c.VectorAlgorithm, Alpha: c.Alpha, Strategy: c.Strategy, IncludeContent: true,
			BM25K1: c.BM25K1, BM25B: c.BM25B})
		if err != nil {
			return nil, 0, err
		}
		for _, it := range items {
			keys, tokens = append(keys, it.Document.Key), tokens+approxTokens(it.Document.ContentMD)
		}
	default:
		return nil, 0, fmt.Errorf("searchType must be fts, vector or hybrid, got %q", c.SearchType)
	}
	return uniqueKeys(keys), tokens, nil
}

func uniqueKeys(keys []string) []string {
	seen := map[string]bool{}
	out := keys[:0]
	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// scoreQuery computes recall@k, nDCG@k and the reciprocal rank.
func scoreQuery(q EvalQuery, returned []string, k int) EvalQueryResult {
	if len(returned) > k {
		returned = returned[:k]
	}
	grade := func(key string) int {
		if g, ok := q.Grades[key]; ok {
			return g
		}
		if containsString(q.ExpectedKeys, key) {
			return 1
		}
		return 0
	}
	r := EvalQueryResult{ID: q.ID, Query: q.Query, Returned: returned}
	found, dcg := 0, 0.0
	for i, key := range returned {
		g := grade(key)
		if g > 0 {
			found++
			if r.RR == 0 {
				r.RR = 1 / float64(i+1)
			}
		}
		dcg += (math.Pow(2, float64(g)) - 1) / math.Log2(float64(i+2))
	}
	var ideal []int
	for _, key := range q.ExpectedKeys {
		ideal = append(ideal, grade(key))
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ideal)))
	idcg := 0.0
	for i, g := range ideal[:min(len(ideal), k)] {
		idcg += (math.Pow(2, float64(g)) - 1) / math.Log2(float64(i+2))
	}
	r.Recall = float64(found) / float64(len(q.ExpectedKeys))
	if idcg > 0 {
		r.NDCG = dcg / idcg
	}
	return r
}

func percentile(xs []float64, p float64) float64 {
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	return round3(sorted[min(int(math.Ceil(p*float64(len(sorted))))-1+boolInt(p == 0), len(sorted)-1)])
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func round3(x float64) float64 { return math.Round(x*1000) / 1000 }
