package main

import (
	"context"
	"errors"
	"mddb/internal/embedding"
	"mddb/internal/storage"
	"mddb/internal/vector"
	"net/http"
	"sort"
	"strings"
	"time"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
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
	IncludeContent    bool                `json:"includeContent"`
	// Oversample is the recall/latency knob (SRCH-005): candidates asked of
	// the index per requested result, before deduplication, merging or
	// rescoring trims them. 1.0-10.0; 0 = use the collection profile, then
	// the default.
	Oversample float64 `json:"oversample,omitempty"`
}

// CrossSearchResultItem represents a single cross-collection search result.
type CrossSearchResultItem struct {
	Collection string      `json:"collection"`
	Document   storage.Doc `json:"document"`
	Score      float32     `json:"score"`
	Rank       int         `json:"rank"`
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
	searcher, ok2 := s.VectorSearchers[algo]
	if !ok2 {
		bad(w, errors.New("unknown algorithm: "+algo))
		return
	}
	if !searcher.IsReady() {
		searcher = s.VectorSearchers["flat"]
		algo = "flat"
	}
	if !searcher.IsReady() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"vector index is loading, please retry"}`))
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

	// Search each target collection and collect results
	type taggedResult struct {
		collection string
		result     vector.VectorResult
	}
	var allTagged []taggedResult

	for _, coll := range req.TargetCollections {
		var results []vector.VectorResult
		if len(req.FilterMeta) > 0 {
			allowedIDs := s.getDocIDsByMeta(coll, req.FilterMeta)
			if len(allowedIDs) == 0 {
				continue
			}
			results = searcher.SearchWithFilter(coll, queryVector, searchTopK, req.Threshold, allowedIDs, metric)
		} else {
			results = searcher.Search(coll, queryVector, searchTopK, req.Threshold, metric)
		}
		results = vector.DeduplicateChunkResults(results)
		for _, vr := range results {
			allTagged = append(allTagged, taggedResult{collection: coll, result: vr})
		}
	}

	// Sort all results by score descending
	sort.Slice(allTagged, func(i, j int) bool {
		return allTagged[i].result.Score > allTagged[j].result.Score
	})
	if len(allTagged) > topK {
		allTagged = allTagged[:topK]
	}

	// Track operation
	if s.Metrics != nil {
		s.Metrics.IncOp("cross_search", algo)
	}

	// Load full documents
	items := make([]CrossSearchResultItem, 0, len(allTagged))
	_ = s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		for rank, tr := range allTagged {
			v := bDocs.Get(storage.DocKey(tr.collection, tr.result.DocID))
			if v == nil {
				continue
			}
			docPtr, err := loadDoc(v)
			if err != nil {
				continue
			}
			doc := *docPtr
			if !req.IncludeContent {
				doc.ContentMD = ""
			}
			items = append(items, CrossSearchResultItem{
				Collection: tr.collection,
				Document:   doc,
				Score:      tr.result.Score,
				Rank:       rank + 1,
			})
		}
		return nil
	})

	resp := CrossSearchResponse{
		Results:           items,
		Total:             len(items),
		SourceCollection:  req.SourceCollection,
		SourceDocID:       req.SourceDocID,
		TargetCollections: req.TargetCollections,
		Algorithm:         algo,
		DistanceMetric:    metricName,
	}

	if searchStatsEnabled() {
		queryTerms := strings.Fields(req.Query)
		resp.Stats = &SearchStats{
			DurationMs:  float64(time.Since(start).Microseconds()) / 1000.0,
			QueryTerms:  queryTerms,
			IndexSize:   len(allTagged),
			TotalTokens: len(queryTerms),
		}
	}

	ok(w, resp)
}
