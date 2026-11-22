package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	json "mddb/internal/jsonx"

	"mddb/internal/fts"
	"mddb/internal/storage"
)

// POST /v1/vector_stores/{id}/search (#446): the search Open WebUI's
// OpenAI-compatible knowledge provider sends. It is not a new search engine:
// a collection with embeddings is searched by hybrid search (the MCP
// hybrid_search path), any other by full-text search (the /v1/fts path), and
// each hit is cut to the passage around its best match with the passage
// builder the "passages" format uses, so the caller's prompt gets the
// relevant lines rather than whole documents.

const (
	// openAIDefaultResults and openAIMaxResults follow the OpenAI API:
	// max_num_results defaults to 10 and may be 1..50.
	openAIDefaultResults = 10
	openAIMaxResults     = 50
)

// openAISearchRequest is the request body. filters and rewrite_query are
// accepted and ignored: OpenAI's attribute filter grammar is not mapped onto
// MDDB's metadata filters, and MDDB does not rewrite queries.
type openAISearchRequest struct {
	Query          json.RawMessage `json:"query"`
	MaxNumResults  *int            `json:"max_num_results"`
	Filters        json.RawMessage `json:"filters"`
	RewriteQuery   bool            `json:"rewrite_query"`
	RankingOptions *struct {
		Ranker         string  `json:"ranker"`
		ScoreThreshold float64 `json:"score_threshold"`
	} `json:"ranking_options"`
}

// openAIHit is a search hit before it is shaped, whichever path found it.
type openAIHit struct {
	doc        storage.Doc
	score      float64
	terms      []string
	highlights []fts.Highlight
}

// handleOpenAIVectorStoreSubtree answers /v1/vector_stores/{id}/search.
func (s *Server) handleOpenAIVectorStoreSubtree(w http.ResponseWriter, r *http.Request) {
	id, action, ok := openAIStoreID(r)
	if !ok || action != "search" {
		openAIFail(w, http.StatusNotFound, "unknown_route", "only POST /v1/vector_stores/{id}/search is served")
		return
	}
	if r.Method != http.MethodPost {
		openAIFail(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST searches a vector store")
		return
	}
	collection := s.Aliases.Resolve(id)
	// docs/AUTHENTICATION.md: a caller without read permission is refused
	// before existence is checked, so a 403 does not say whether it exists.
	if err := s.openAIMayRead(r, collection); err != nil {
		openAIAuthFail(w, err)
		return
	}
	if !s.collectionExists(collection) {
		openAIFail(w, http.StatusNotFound, "vector_store_not_found", "no vector store (collection) named "+id)
		return
	}
	req, queries, err := readOpenAISearch(r)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			openAIFail(w, http.StatusRequestEntityTooLarge, "body_too_large", err.Error())
			return
		}
		openAIFail(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	hits, err := s.openAISearch(r.Context(), collection, strings.Join(queries, " "), openAIResultCount(req.MaxNumResults))
	if err != nil {
		// The request was checked above, so what is left is the server's: an
		// embedding provider that is down, an index not initialised.
		openAIFail(w, http.StatusInternalServerError, "search_failed", err.Error())
		return
	}
	threshold := 0.0
	if req.RankingOptions != nil {
		threshold = req.RankingOptions.ScoreThreshold
	}
	writeJSONStatus(w, http.StatusOK, openAISearchPage{Object: "vector_store.search_results.page", SearchQuery: queries,
		Data: openAIResults(collection, queries, hits, threshold)})
}

// readOpenAISearch decodes the body: query is a string or an array of
// strings, and at least one of them must have text.
func readOpenAISearch(r *http.Request) (*openAISearchRequest, []string, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, nil, err
	}
	var req openAISearchRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, nil, errors.New("the body must be a JSON object: " + err.Error())
	}
	var queries []string
	var one string
	if len(req.Query) == 0 {
		return nil, nil, errors.New("query is required")
	}
	if json.Unmarshal(req.Query, &one) == nil {
		queries = []string{one}
	} else if json.Unmarshal(req.Query, &queries) != nil {
		return nil, nil, errors.New("query must be a string or an array of strings")
	}
	kept := queries[:0]
	for _, q := range queries {
		if q = strings.TrimSpace(q); q != "" {
			kept = append(kept, q)
		}
	}
	if len(kept) == 0 {
		return nil, nil, errors.New("query is required")
	}
	if req.MaxNumResults != nil && *req.MaxNumResults < 1 {
		return nil, nil, errors.New("max_num_results must be 1 or more")
	}
	return &req, kept, nil
}

// openAIResultCount is max_num_results: 10 when absent, at most 50.
func openAIResultCount(n *int) int {
	if n == nil {
		return openAIDefaultResults
	}
	return min(*n, openAIMaxResults)
}

// openAISearch runs the search: hybrid when the collection has embeddings
// and the server an embedding provider, full text otherwise. Both ask for
// whole results (format "results") whatever the collection profile's
// format, because the passage is cut here, from each hit's document.
func (s *Server) openAISearch(ctx context.Context, collection, query string, n int) ([]openAIHit, error) {
	if s.Embedding != nil && s.collectionVectorCount(collection) > 0 {
		return s.openAIHybrid(ctx, collection, query, n)
	}
	return s.openAIFullText(ctx, collection, query, n)
}

// openAIHybrid searches through hybrid search. Its alpha fusion already
// scores in 0..1 (min-max full text, cosine vector); boosts may push a score
// past 1, so it is clamped.
func (s *Server) openAIHybrid(ctx context.Context, collection, query string, n int) ([]openAIHit, error) {
	resp, err := NewDirectClient(s).HybridSearch(ctx, &MCPHybridSearchRequest{Collection: collection, Query: query, TopK: n,
		PassageOptions: PassageOptions{Format: FormatResults}})
	if err != nil {
		return nil, err
	}
	hits := make([]openAIHit, 0, len(resp.Results))
	for _, res := range resp.Results {
		hits = append(hits, openAIHit{doc: mcpDocumentToDoc(res.Document), score: min(max(res.CombinedScore, 0), 1),
			terms: res.MatchedTerms})
	}
	return hits, nil
}

// openAIFullText searches through full-text search. BM25 scores are
// unbounded, so each is divided by the best one: the top hit scores 1.
func (s *Server) openAIFullText(ctx context.Context, collection, query string, n int) ([]openAIHit, error) {
	resp, err := s.shapedFTS(ctx, FTSSearchRequest{Collection: collection, Query: query, Limit: n, Highlight: true,
		PassageOptions: PassageOptions{Format: FormatResults}}, time.Now())
	if err != nil {
		return nil, err
	}
	best := 0.0
	for _, res := range resp.Results {
		best = max(best, res.Score)
	}
	hits := make([]openAIHit, 0, len(resp.Results))
	for _, res := range resp.Results {
		hits = append(hits, openAIHit{doc: res.Document, score: relativeScore(res.Score, best), terms: res.MatchedTerms,
			highlights: res.Highlights})
	}
	return hits, nil
}

// relativeScore is score as a share of best, in 0..1; 1 when best is not
// positive (every hit scored alike).
func relativeScore(score, best float64) float64 {
	if best <= 0 {
		return 1
	}
	return min(max(score, 0)/best, 1)
}
