package main

import (
	"context"
	"errors"
	"mddb/internal/storage"
	"mddb/internal/vector"
	"net/http"
	"strings"
	"time"

	json "mddb/internal/jsonx"
)

// CrossSearchRequest represents a cross-collection vector search request.
type CrossSearchRequest struct {
	SourceCollection  string              `json:"sourceCollection"`
	SourceDocID       string              `json:"sourceDocID"`
	Query             string              `json:"query"`
	QueryVector       []float32           `json:"queryVector"`
	TargetCollections []string            `json:"targetCollections"`
	TopK              int                 `json:"topK"`
	Threshold         float64             `json:"threshold"`
	Algorithm         string              `json:"algorithm"`
	DistanceMetric    string              `json:"distanceMetric"`
	FilterMeta        map[string][]string `json:"filterMeta"`
	Filter            MetaFilter          `json:"filter,omitempty"` // operator filter (SRCH-017): prefix, contains, gt/gte/lt/lte, not, exists; ANDed with filterMeta
	IncludeContent    bool                `json:"includeContent"`
	// Oversample is the recall/latency knob (SRCH-005): candidates asked of
	// the index per requested result, before deduplication, merging or
	// rescoring trims them. 1.0-10.0; 0 = use the collection profile, then
	// the default.
	Oversample float64  `json:"oversample,omitempty"`
	GroupBy    *GroupBy `json:"groupBy,omitempty"` // keep perGroup results per metadata value, document or collection (SRCH-016)

	IndexWait       // readLevel, waitForIndex, waitForKeys (GO-042)
	PassageOptions  // format, passageLines (AGT-002)
	PlanOptions     // explain, latencyTargetMs (SRCH-013)
	FeedbackOptions // positive, negative, feedbackMode, feedbackWeight, rocchio (SRCH-015)
	// SearchType is "vector" (default), "fts" or "hybrid" (AGT-003). Targets may
	// include "role:reference" for every reference collection.
	SearchType    string `json:"searchType,omitempty"`
	ImageOptions         // includeImages (RAG-010): each hit's images, from its own collection
	RerankRequest        // rerank: {enabled, candidates, topN, explain} (RAG-011)
}

// CrossSearchResultItem represents a single cross-collection search result.
type CrossSearchResultItem struct {
	Collection string      `json:"collection"`
	Document   storage.Doc `json:"document"`
	Score      float32     `json:"score"`
	Rank       int         `json:"rank"`
	// Group and GroupRank are set when the request had groupBy (SRCH-016):
	// the group this result counted against and its 1-based place in it.
	Group     string `json:"group,omitempty"`
	GroupRank int    `json:"groupRank,omitempty"`
	// Images are the document's images, with includeImages (RAG-010).
	Images []ImageRef `json:"images,omitempty"`
	// chunks are summaryMode expand's chunks for the document, chosen by its
	// own collection's profile (RAG-009): one passage each with passages.
	chunks []int
	// rerankText is the passage the reranking model reads (RAG-011).
	rerankText string
}

// CrossSearchResponse represents the response from cross-collection search.
type CrossSearchResponse struct {
	Results           []CrossSearchResultItem `json:"results"`
	Total             int                     `json:"total"`
	SourceCollection  string                  `json:"sourceCollection,omitempty"`
	SourceDocID       string                  `json:"sourceDocID,omitempty"`
	TargetCollections []string                `json:"targetCollections"`
	Algorithm         string                  `json:"algorithm"`
	DistanceMetric    string                  `json:"distanceMetric"`
	Stats             *SearchStats            `json:"searchStats,omitempty"`
	// Indexing reports the wait when the request set waitForIndex (GO-042).
	Indexing *IndexWaitResult `json:"indexing,omitempty"`
	// Plan is how the vector search ran and why, when the request set explain
	// (SRCH-013).
	Plan         *SearchPlan `json:"plan,omitempty"`
	LimitReport              // limit, limitCapped: set when the server's cap lowered the request's limit (#368)
	RerankResult             // rerank, rerankSkipped (RAG-011)
}

// handleCrossSearch handles POST /v1/cross-search
func (s *Server) handleCrossSearch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req CrossSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if len(req.TargetCollections) == 0 {
		bad(w, errors.New("missing targetCollections"))
		return
	}
	req.TargetCollections = s.expandTargets(req.TargetCollections)
	if s.partialTargets(w, r, &req) {
		return
	}
	if code, err := checkCrossRequest(&req); err != nil {
		writeCrossError(w, code, err)
		return
	}
	if !s.crossPermitted(r.Context(), req) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}
	if req.SearchType == CrossSearchFTS || req.SearchType == CrossSearchHybrid {
		s.respondCrossText(w, r, req)
		return
	}
	s.respondCrossVector(w, r, req, start)
}

// checkCrossRequest validates a cross-search: the status and the error of
// the first check it fails.
func checkCrossRequest(req *CrossSearchRequest) (int, error) {
	textSearch := req.SearchType == CrossSearchFTS || req.SearchType == CrossSearchHybrid
	checks := []struct {
		code  int
		check func() error
	}{
		{http.StatusBadRequest, func() error {
			if len(req.TargetCollections) == 0 {
				return errors.New("no collection matches targetCollections")
			}
			return nil
		}},
		{http.StatusUnprocessableEntity, func() error { return validCrossSearchType(req.SearchType) }},
		{http.StatusBadRequest, func() error {
			if textSearch && req.Query == "" {
				return errors.New("searchType " + req.SearchType + " needs a query")
			}
			return nil
		}},
		// SRCH-005: out of range is 422, not 400 — the body parsed fine, the
		// number is the problem.
		{http.StatusUnprocessableEntity, func() error { return ValidateOversample(req.Oversample) }},
		{http.StatusUnprocessableEntity, func() error {
			return validateAll(req.GroupBy.Validate, req.ValidateIndexWait, req.ValidatePassageOptions,
				req.ValidatePlanOptions, req.ValidateFeedback, req.ValidateRerank)
		}},
		{http.StatusBadRequest, func() error {
			if req.Query == "" && len(req.QueryVector) == 0 && req.SourceDocID == "" {
				return errors.New("one of query, queryVector, or sourceDocID is required")
			}
			return nil
		}},
	}
	for _, c := range checks {
		if err := c.check(); err != nil {
			return c.code, err
		}
	}
	return 0, nil
}

// writeCrossError answers err with code, as bad and unprocessable do.
func writeCrossError(w http.ResponseWriter, code int, err error) {
	if code == http.StatusUnprocessableEntity {
		unprocessable(w, err)
		return
	}
	bad(w, err)
}

// crossPermitted reports whether the caller may read every target and the
// source collection.
func (s *Server) crossPermitted(ctx context.Context, req CrossSearchRequest) bool {
	if s.AuthManager == nil {
		return true
	}
	names := req.TargetCollections
	if req.SourceCollection != "" {
		names = append(append([]string(nil), names...), req.SourceCollection)
	}
	for _, coll := range names {
		if s.AuthManager.CheckPermission(ctx, coll, PermRead) != nil {
			return false
		}
	}
	return true
}

// respondCrossVector answers a vector cross-search.
func (s *Server) respondCrossVector(w http.ResponseWriter, r *http.Request, req CrossSearchRequest, start time.Time) {
	topK, limitReport := s.crossTopK(req.TargetCollections, req.TopK) // #368
	// Oversample per collection for better merging (SRCH-005). Resolved
	// against no collection: a cross-collection search has no single profile
	// that could own the factor, so only the request parameter and the
	// default apply.
	searchTopK := OversampledTopK(topK, s.ResolveOversample("", req.Oversample), 20)

	// Select algorithm (SRCH-013: "auto" plans each target).
	searcher, algo, plan, err := s.crossSearcher(req.SourceCollection, req.Algorithm, searchTopK)
	if err != nil {
		s.writeVectorUnavailable(w, err, "auto, flat, hnsw, ivf, pq, quantized")
		return
	}
	queryVector, err := s.crossQueryVector(r.Context(), &MCPCrossSearchRequest{SourceCollection: req.SourceCollection,
		SourceDocID: req.SourceDocID, Query: req.Query, QueryVector: req.QueryVector})
	if err != nil {
		bad(w, err)
		return
	}
	// SRCH-015: the examples as vectors; query mode corrects the query here.
	fb, queryVector, err := s.crossFeedback(req.FeedbackOptions, req.SourceCollection, req.TargetCollections, queryVector, plan)
	if err != nil {
		unprocessable(w, err)
		return
	}

	waited := s.waitForIndexAll(r.Context(), req.TargetCollections, req.IndexWait)
	rr := s.newRerankRun("", req.Query, req.Rerank, req.Explain) // RAG-011
	params := crossSearchParams{
		Searcher: searcher, QueryVector: queryVector, Targets: req.TargetCollections,
		SearchTopK: searchTopK, TopK: topK, Threshold: req.Threshold, Metric: vector.ResolveSimilarity(req.DistanceMetric),
		FilterMeta: req.FilterMeta, Filter: req.Filter, GroupBy: req.GroupBy,
		IncludeContent: req.IncludeContent || req.wantsPassages(),
		Plan:           plan, LatencyTargetMs: req.LatencyTargetMs, Feedback: fb, Rerank: rr,
	}
	if s.isPartial(r) { // GO-052: this group's part of a fan-out
		s.writeCrossPart(w, crossPart{Items: s.crossCandidates(params), LimitReport: limitReport,
			Algorithm: algo, Plan: plan, Indexing: waited}, req.IncludeImages)
		return
	}
	items := s.crossSearchItems(r.Context(), params)

	// Track operation
	if s.Metrics != nil {
		s.Metrics.IncOp("cross_search", algo)
	}
	s.countCross(algo, items, req.wantsPassages())
	if req.wantsPassages() {
		p := s.crossPassages(req.Query, items, req.PassageOptions, waited)
		s.passageImages(&p, req.IncludeImages, nil) // RAG-010
		p.LimitReport = limitReport
		p.RerankResult = rr.Result()
		ok(w, p)
		return
	}
	s.crossImages(items, req.IncludeImages) // RAG-010

	metricName := req.DistanceMetric
	if metricName == "" {
		metricName = "cosine"
	}
	resp := CrossSearchResponse{
		Results:           items,
		Total:             len(items),
		SourceCollection:  req.SourceCollection,
		SourceDocID:       req.SourceDocID,
		TargetCollections: req.TargetCollections,
		Algorithm:         algo,
		DistanceMetric:    metricName,
		Indexing:          waited,
		Plan:              req.explained(plan),
		LimitReport:       limitReport,
		RerankResult:      rr.Result(),
	}

	if searchStatsEnabled() {
		queryTerms := strings.Fields(req.Query)
		resp.Stats = &SearchStats{
			DurationMs:  float64(time.Since(start).Microseconds()) / 1000.0,
			QueryTerms:  queryTerms,
			IndexSize:   len(items),
			TotalTokens: len(queryTerms),
		}
	}

	ok(w, resp)
}
