package main

import (
	"context"
	"fmt"
	"log/slog"
	"mddb/internal/fts"
	"mddb/internal/spell"
	"mddb/internal/storage"
	"net/http"
	"time"

	json "mddb/internal/jsonx"
)

// FTSSearchRequest is the HTTP request for full-text search.
type FTSSearchRequest struct {
	Collection      string             `json:"collection"`
	Query           string             `json:"query"`
	Limit           int                `json:"limit"`
	Algorithm       string             `json:"algorithm"`              // "tfidf", "bm25", "bm25f", "pmisparse"
	Fuzzy           int                `json:"fuzzy"`                  // typo tolerance: 0 (off), 1 (1 edit), 2 (2 edits)
	DisableStem     bool               `json:"disableStem"`            // temporarily disable stemming for this query
	DisableSynonyms bool               `json:"disableSynonyms"`        // temporarily disable synonyms for this query
	FieldWeights    map[string]float64 `json:"fieldWeights,omitempty"` // BM25F field weights
	// BM25K1 and BM25B override the BM25 constants for this query (SRCH-018);
	// nil = the collection profile's, then 1.2 / 0.75. bm25 and bm25f only.
	BM25K1     *float64            `json:"bm25K1,omitempty"`
	BM25B      *float64            `json:"bm25B,omitempty"`
	bm25       fts.BM25Params      // resolved by normalizeFTSRequest
	FilterMeta map[string][]string `json:"filterMeta,omitempty"` // metadata pre-filter (in-graph filtering)
	Filter     MetaFilter          `json:"filter,omitempty"`     // operator filter (SRCH-017): prefix, contains, gt/gte/lt/lte, not, exists; ANDed with filterMeta
	// Advanced search modes
	Mode          string             `json:"mode,omitempty"`          // "simple" (default), "boolean", "phrase", "wildcard", "proximity", "auto"
	Distance      int                `json:"distance,omitempty"`      // proximity distance (words) for mode=proximity
	RangeMeta     []RangeFilter      `json:"rangeMeta,omitempty"`     // range filters on metadata/timestamps
	Lang          string             `json:"lang,omitempty"`          // language for query tokenization (e.g. "en", "pl", "de")
	Boost         map[string]float64 `json:"boost,omitempty"`         // per-query boost: "metaKey:metaValue" → multiplier
	Highlight     bool               `json:"highlight,omitempty"`     // when true, each result carries a `highlights` array (v2.9.14+)
	HighlightTag  string             `json:"highlightTag,omitempty"`  // override wrap tag, e.g. "<strong>" (default "<mark>")
	MaxHighlights int                `json:"maxHighlights,omitempty"` // cap per-result fragments (default 3)
	FragmentSize  int                `json:"fragmentSize,omitempty"`  // approx chars per fragment (default 150)
	// Faceted search (v2.9.14+): when non-empty, the response includes per-key value counts
	// aggregated over the matched documents. Cardinality per key is capped by FacetMaxValues.
	FacetBy        []string `json:"facetBy,omitempty"`
	FacetMaxValues int      `json:"facetMaxValues,omitempty"` // 0 = unlimited
	// FacetScope picks what facetBy counts over (#311): "returned" (default)
	// is the page in `results`; "all" is every document that matched the
	// query and filters, before pagination, and also fills `matched`.
	FacetScope string `json:"facetScope,omitempty"`
	// skipContextBudget lets a caller apply the collection's context budget
	// itself — MCP does, after dropping bodies the agent did not ask for. Such
	// a caller also counts the search for the gain report (AGT-004).
	skipContextBudget bool
	// summaryMode overrides the collection's summaryMode (RAG-009) for an
	// evaluation config; "" = the profile's.
	summaryMode string
	// limitReport is set by normalizeFTSRequest when the server's cap
	// lowered Limit (#368), for the response to say so.
	limitReport LimitReport
	// CacheTTL opts this request into the search-result cache for the given
	// number of seconds (GO-031). Omitted or 0 means no caching at all, so a
	// caller that says nothing keeps today's behaviour and today's freshness.
	// Capped at searchCacheMaxTTL.
	CacheTTL       int      `json:"cacheTtl,omitempty"`
	GroupBy        *GroupBy `json:"groupBy,omitempty"` // keep perGroup results per metadata value, document or collection (SRCH-016)
	PassageOptions          // format, passageLines (AGT-002)
	ImageOptions            // includeImages (RAG-010)
	RerankRequest           // rerank: {enabled, candidates, topN, explain} (RAG-011)
}

// FTSSearchResponse is the HTTP response for full-text search.
type FTSSearchResponse struct {
	Results        []FTSResultWithDoc         `json:"results"`
	Total          int                        `json:"total"`
	Algorithm      string                     `json:"algorithm"`
	Mode           string                     `json:"mode"`
	Fuzzy          int                        `json:"fuzzy"`
	Lang           string                     `json:"lang,omitempty"`
	StemmingActive bool                       `json:"stemmingActive"`
	SynonymsActive bool                       `json:"synonymsActive"`
	FieldWeights   map[string]float64         `json:"fieldWeights,omitempty"`
	Stats          *SearchStats               `json:"searchStats,omitempty"`
	SpellCorrected *spell.SpellCorrectionInfo `json:"spellCorrected,omitempty"`
	Facets         FacetResult                `json:"facets,omitempty"` // populated when request.facetBy is set (v2.9.14+)
	// Matched is the number of documents that matched the query and filters,
	// before pagination. Reported with facetScope "all", the one case that
	// already has to find them all (#311).
	Matched int `json:"matched,omitempty"`
	// ContextTruncated reports that the collection's contextTokenBudget
	// dropped results from the tail (RAG-001). Without it, a caller cannot
	// tell a short result list from a capped one.
	ContextTruncated bool `json:"contextTruncated,omitempty"`
	// passages is set for format "passages" and is what the caller gets
	// instead of this response (AGT-002).
	passages     *PassagesResponse
	LimitReport  // limit, limitCapped: set when the server's cap lowered the request's limit (#368)
	RerankResult // rerank, rerankSkipped (RAG-011)
}

// FTSResultWithDoc includes the full document in the result.
type FTSResultWithDoc struct {
	Document     storage.Doc     `json:"document"`
	Score        float64         `json:"score"`
	MatchedTerms []string        `json:"matchedTerms"`
	Highlights   []fts.Highlight `json:"highlights,omitempty"` // populated when request.highlight=true
	Pinned       bool            `json:"pinned,omitempty"`     // set by curation rules (v2.9.14+)
	// Group and GroupRank are set when the request had groupBy (SRCH-016):
	// the group this result counted against and its 1-based place in it.
	Group     string `json:"group,omitempty"`
	GroupRank int    `json:"groupRank,omitempty"`
	// Images are the document's images, with includeImages (RAG-010).
	Images []ImageRef `json:"images,omitempty"`
}

// --- HTTP handler ---

func (s *Server) handleFTS(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req FTSSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if err := s.normalizeFTSRequest(&req); err != nil {
		bad(w, err)
		return
	}

	// GO-031: opt-in result cache. The key covers the whole request, so two
	// callers only share an entry when they asked the same question of the
	// same collection at the same index generation.
	cacheKey, cacheTTL := s.searchCacheLookup(&req)
	if cacheKey != "" {
		if cached, hit := s.SearchCache.Get(cacheKey); hit {
			w.Header().Set("X-MDDB-Cache", "hit")
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write(cached); err != nil {
				slog.Debug("writing a cached search response failed", "err", err)
			}
			if s.Metrics != nil {
				s.Metrics.IncOp("fts_search", "cache_hit")
			}
			return
		}
		w.Header().Set("X-MDDB-Cache", "miss")
	}

	resp, err := s.shapedFTS(r.Context(), req, start)
	if err != nil {
		bad(w, err)
		return
	}

	var out any = resp
	if resp.passages != nil {
		out = resp.passages
	}
	if cacheKey = cacheableKey(cacheKey, resp); cacheKey != "" {
		if body, err := json.Marshal(out); err == nil {
			s.SearchCache.Set(cacheKey, body, int64(cacheTTL))
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write(body); err != nil {
				slog.Debug("writing a search response failed", "err", err)
			}
			return
		}
	}
	ok(w, out)
}

// cacheableKey is the cache key an answer may be stored under: none for an
// answer whose requested rerank was skipped, which is not one worth repeating
// (RAG-011).
func cacheableKey(key string, resp *FTSSearchResponse) string {
	if resp.RerankSkipped != "" {
		return ""
	}
	return key
}

// normalizeFTSRequest validates a full-text request and resolves its limit.
// Shared by HTTP and MCP so both reject the same requests (#328).
func (s *Server) normalizeFTSRequest(req *FTSSearchRequest) error {
	if req.Collection == "" || req.Query == "" {
		return fmt.Errorf("missing required fields: collection, query")
	}
	// RAG-001: request > collection profile > this path's historical default,
	// under the server's cap (#368). Normalising twice keeps the first report.
	var rep LimitReport
	if req.Limit, rep = s.ResolveTopK(req.Collection, req.Limit, 50); rep.LimitCapped {
		req.limitReport = rep
	}
	// #368: a facet list and highlight fragments are part of the response
	// size too. facetMaxValues 0 ("every value") means the cap.
	req.FacetMaxValues, _ = s.CapLimit(req.Collection, positiveOr(req.FacetMaxValues, searchMaxLimitCeiling), 0)
	req.MaxHighlights = min(req.MaxHighlights, maxHighlightFragments)
	req.FragmentSize = min(req.FragmentSize, maxHighlightFragmentSize)
	switch req.FacetScope {
	case "", facetScopeReturned, facetScopeAll:
	default:
		return fmt.Errorf("facetScope must be %q or %q, got %q", facetScopeReturned, facetScopeAll, req.FacetScope)
	}
	if s.FTSIndex == nil {
		return fmt.Errorf("full-text search not initialized")
	}
	var err error
	if req.bm25, err = s.ResolveBM25(req.Collection, req.BM25K1, req.BM25B); err != nil {
		return err
	}
	if req.Algorithm == "bm25f" { // RAG-009: meta.summary joins the fields while summaries are on
		req.FieldWeights = s.summaryFieldWeights(req.Collection, s.ResolveSummaryMode(req.Collection, req.summaryMode), req.FieldWeights)
	}
	req.PassageOptions = s.ResolveFormat(req.Collection, req.PassageOptions)
	return validateAll(req.ValidatePassageOptions, req.ValidateRerank)
}

// searchFTS runs a full-text search: spell correction, mode, filters, ranges,
// boosts, facets, highlights and the context budget. It is the one
// implementation behind POST /v1/fts and the MCP full_text_search tool; MCP
// had a reduced copy without filters, modes or stemming control (#328). The
// steps are in fts_search_run.go and fts_search_results.go, in the order
// they run here.
func (s *Server) searchFTS(ctx context.Context, req FTSSearchRequest, start time.Time) (*FTSSearchResponse, error) {
	if err := s.normalizeFTSRequest(&req); err != nil {
		return nil, err
	}
	req.GroupBy = s.ResolveGroupBy(req.Collection, req.GroupBy)
	if err := req.GroupBy.Validate(); err != nil {
		return nil, err
	}
	spellCorrected := s.spellCorrectFTS(&req)
	run := s.newFTSRun(&req)
	if !run.preFilter() {
		return run.emptyResponse(), nil
	}
	results, err := run.search()
	if err != nil {
		return nil, err
	}
	run.countSearch()
	if results, err = run.postFilter(results); err != nil {
		return nil, err
	}
	allFacets, matched := run.facetsOverAll(results)

	resp := run.newResponse()
	resp.Results = run.finishResults(ctx, run.loadResults(results))
	run.applyBudget(&resp)
	resp.Total = len(resp.Results)
	run.setFacets(&resp, allFacets, matched)
	resp.Stats = run.stats(start, resp.Total)
	resp.SpellCorrected = spellCorrected
	resp.LimitReport = req.limitReport
	resp.RerankResult = run.rr.Result()
	run.attachExtras(&resp)
	return &resp, nil
}

// FTSReindexRequest is the HTTP request for FTS reindexing.
type FTSReindexRequest struct {
	Collection string `json:"collection"`
}

// handleFTSReindex re-indexes all documents in a collection using their lang field.
func (s *Server) handleFTSReindex(w http.ResponseWriter, r *http.Request) {
	var req FTSReindexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	// Reindexing changes scores and can change the result set, so cached
	// responses for this collection stop being valid (GO-031).
	defer s.invalidateSearchCache(req.Collection)
	if req.Collection == "" {
		bad(w, fmt.Errorf("missing required field: collection"))
		return
	}
	if s.FTSIndex == nil {
		bad(w, fmt.Errorf("full-text search not initialized"))
		return
	}
	// Reindexing a large collection outlasts the write timeout and is not
	// cancelled by it; only the answer was lost (#315, as for add-batch).
	liftWriteDeadline(w, r)

	res, err := s.reindexCollectionFTS(req.Collection)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	ok(w, map[string]interface{}{
		"status":    res.Status,
		"reindexed": res.Reindexed,
		"skipped":   res.Skipped + res.Failed,
	})
}

// handleFTSLanguages returns the list of supported FTS languages.
func (s *Server) handleFTSLanguages(w http.ResponseWriter, _ *http.Request) {
	if s.FTSIndex == nil || s.FTSIndex.LangRegistry() == nil {
		ok(w, map[string]interface{}{"languages": []string{}})
		return
	}

	type langInfo struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}

	var langs []langInfo
	for _, code := range s.FTSIndex.LangRegistry().Languages() {
		cfg := s.FTSIndex.LangRegistry().Resolve(code)
		name := code
		if cfg != nil {
			name = cfg.Name
		}
		langs = append(langs, langInfo{Code: code, Name: name})
	}

	ok(w, map[string]interface{}{
		"languages":   langs,
		"defaultLang": s.FTSIndex.LangRegistry().DefaultLang(),
	})
}

func currentUnix() int64 {
	return time.Now().Unix()
}
