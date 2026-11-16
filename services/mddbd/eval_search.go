package main

import (
	"context"
	"fmt"
	"time"
)

// One evaluation query under one configuration (SRCH-019): each search type
// runs through the implementation its surface uses, so a measured number is
// the number a caller would get.

// evalAnswer is one query's answer under one configuration: the ranked
// document keys, the estimated tokens of what came back, and why a requested
// rerank did not run (RAG-011).
type evalAnswer struct {
	keys          []string
	tokens        int
	rerankSkipped string
}

// add records one returned document.
func (a *evalAnswer) add(key, content string) {
	a.keys, a.tokens = append(a.keys, key), a.tokens+approxTokens(content)
}

// evalQuery is one query of one configuration.
type evalQuery struct {
	collection string
	k          int
	c          EvalConfig
	query      string
}

// evalSearch runs one query and returns its answer.
func (s *Server) evalSearch(ctx context.Context, q evalQuery) (evalAnswer, error) {
	if err := validateAll(func() error { return validateSummarySettings(q.c.SummaryMode, nil) },
		DecompositionOptions{QueryDecomposition: q.c.QueryDecomposition}.ValidateDecomposition,
		q.c.Rerank.Validate); err != nil {
		return evalAnswer{}, err
	}
	var a evalAnswer
	var err error
	switch q.c.SearchType {
	case SearchTypeFTS:
		a, err = s.evalFTS(ctx, q)
	case SearchTypeVector:
		a, err = s.evalVector(ctx, q)
	case SearchTypeHybrid:
		a, err = s.evalHybrid(ctx, q)
	default:
		return evalAnswer{}, fmt.Errorf("searchType must be fts, vector or hybrid, got %q", q.c.SearchType)
	}
	a.keys = uniqueKeys(a.keys)
	return a, err
}

// evalFTS runs one query as a full-text search.
func (s *Server) evalFTS(ctx context.Context, q evalQuery) (evalAnswer, error) {
	c := q.c
	resp, err := s.searchFTS(ctx, FTSSearchRequest{Collection: q.collection, Query: q.query, Limit: q.k, Algorithm: c.Algorithm,
		BM25K1: c.BM25K1, BM25B: c.BM25B, skipContextBudget: true, summaryMode: c.SummaryMode,
		RerankRequest: RerankRequest{Rerank: c.Rerank}}, time.Now())
	if err != nil {
		return evalAnswer{}, err
	}
	a := evalAnswer{rerankSkipped: resp.RerankSkipped}
	for _, r := range resp.Results {
		a.add(r.Document.Key, r.Document.ContentMD)
	}
	return a, nil
}

// evalVector runs one query as a vector search.
func (s *Server) evalVector(ctx context.Context, q evalQuery) (evalAnswer, error) {
	c := q.c
	resp, err := NewDirectClient(s).VectorSearch(ctx, &MCPVectorSearchRequest{Collection: q.collection, Query: q.query, TopK: q.k,
		Algorithm: c.VectorAlgorithm, RetrievalMode: c.RetrievalMode, IncludeContent: true, noGain: true, summaryMode: c.SummaryMode,
		DecompositionOptions: DecompositionOptions{QueryDecomposition: c.QueryDecomposition},
		RerankRequest:        RerankRequest{Rerank: c.Rerank}})
	if err != nil {
		return evalAnswer{}, err
	}
	a := evalAnswer{rerankSkipped: resp.RerankSkipped}
	for _, r := range resp.Results {
		a.add(r.Document.Key, r.Document.ContentMD)
	}
	return a, nil
}

// evalHybrid runs one query as a hybrid search.
func (s *Server) evalHybrid(ctx context.Context, q evalQuery) (evalAnswer, error) {
	c := q.c
	items, ran, err := s.hybridItems(ctx, HybridSearchRequest{Collection: q.collection, Query: q.query, TopK: q.k,
		Algorithm: c.Algorithm, VectorAlgorithm: c.VectorAlgorithm, Alpha: c.Alpha, Strategy: c.Strategy, IncludeContent: true,
		BM25K1: c.BM25K1, BM25B: c.BM25B, summaryMode: c.SummaryMode,
		DecompositionOptions: DecompositionOptions{QueryDecomposition: c.QueryDecomposition},
		RerankRequest:        RerankRequest{Rerank: c.Rerank}})
	if err != nil {
		return evalAnswer{}, err
	}
	a := evalAnswer{rerankSkipped: ran.rerankRun.Result().RerankSkipped}
	for _, it := range items {
		a.add(it.Document.Key, it.Document.ContentMD)
	}
	return a, nil
}
