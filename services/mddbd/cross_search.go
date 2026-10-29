package main

import (
	"context"
	"errors"
	"mddb/internal/embedding"
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

	IndexWait // readLevel, waitForIndex, waitForKeys (GO-042)
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
	// SRCH-005: out of range is 422, not 400 — the body parsed fine, the
	// number is the problem.
	if err := ValidateOversample(req.Oversample); err != nil {
		unprocessable(w, err)
		return
	}
	if err := req.GroupBy.Validate(); err != nil {
		unprocessable(w, err)
		return
	}
	if err := req.ValidateIndexWait(); err != nil {
		unprocessable(w, err)
		return
	}
	if req.Query == "" && len(req.QueryVector) == 0 && req.SourceDocID == "" {
		bad(w, errors.New("one of query, queryVector, or sourceDocID is required"))
		return
	}

	// Check read permission on each target collection
	if s.AuthManager != nil {
		for _, coll := range req.TargetCollections {
			if err := s.AuthManager.CheckPermission(r.Context(), coll, PermRead); err != nil {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
		}
		if req.SourceCollection != "" {
			if err := s.AuthManager.CheckPermission(r.Context(), req.SourceCollection, PermRead); err != nil {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
		}
	}

	// Select algorithm
	algo := req.Algorithm
	if algo == "" {
		algo = "flat"
	}
	searcher, algo, err := s.readySearcher(req.SourceCollection, algo)
	if err != nil {
		s.writeVectorUnavailable(w, err, "flat, hnsw, ivf, pq, quantized")
		return
	}

	// Resolve query vector from one of 3 sources
	var queryVector []float32

	if len(req.QueryVector) > 0 {
		queryVector = req.QueryVector
	} else if req.SourceDocID != "" {
		if req.SourceCollection == "" {
			bad(w, errors.New("sourceCollection required when using sourceDocID"))
			return
		}
		rec, err := s.VectorStore.Get(req.SourceCollection, req.SourceDocID)
		if err != nil || rec == nil {
			bad(w, errors.New("source document has no embedding: "+req.SourceCollection+"/"+req.SourceDocID))
			return
		}
		queryVector = rec.Vector
	} else if s.Embedding != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		var err error
		queryVector, err = s.Embedding.Embed(ctx, req.Query, embedding.RoleQuery)
		if err != nil {
			bad(w, errors.New("failed to embed query: "+err.Error()))
			return
		}
	} else {
		bad(w, errors.New("no embedding provider configured and no queryVector/sourceDocID provided"))
		return
	}

	topK := req.TopK
	if topK <= 0 {
		topK = 10
	}

	metric := vector.ResolveSimilarity(req.DistanceMetric)
	metricName := req.DistanceMetric
	if metricName == "" {
		metricName = "cosine"
	}

	// Oversample per collection for better merging (SRCH-005). Resolved
	// against no collection: a cross-collection search has no single profile
	// that could own the factor, so only the request parameter and the
	// default apply.
	searchTopK := OversampledTopK(topK, s.ResolveOversample("", req.Oversample), 20)

	waited := s.waitForIndexAll(r.Context(), req.TargetCollections, req.IndexWait)
	items := s.crossSearchItems(crossSearchParams{
		Searcher: searcher, QueryVector: queryVector, Targets: req.TargetCollections,
		SearchTopK: searchTopK, TopK: topK, Threshold: req.Threshold, Metric: metric,
		FilterMeta: req.FilterMeta, Filter: req.Filter, GroupBy: req.GroupBy,
		IncludeContent: req.IncludeContent,
	})

	// Track operation
	if s.Metrics != nil {
		s.Metrics.IncOp("cross_search", algo)
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
