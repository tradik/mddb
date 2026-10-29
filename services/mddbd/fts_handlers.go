package main

import (
	"fmt"
	"log/slog"
	"mddb/internal/fts"
	"mddb/internal/spell"
	"mddb/internal/storage"
	"net/http"
	"strings"
	"time"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
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
	// CacheTTL opts this request into the search-result cache for the given
	// number of seconds (GO-031). Omitted or 0 means no caching at all, so a
	// caller that says nothing keeps today's behaviour and today's freshness.
	// Capped at searchCacheMaxTTL.
	CacheTTL       int      `json:"cacheTtl,omitempty"`
	GroupBy        *GroupBy `json:"groupBy,omitempty"` // keep perGroup results per metadata value, document or collection (SRCH-016)
	PassageOptions          // format, passageLines (AGT-002)
	ImageOptions            // includeImages (RAG-010)
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
	passages *PassagesResponse
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

	resp, err := s.searchFTS(req, start)
	if err != nil {
		bad(w, err)
		return
	}

	var out any = resp
	if resp.passages != nil {
		out = resp.passages
	}
	if cacheKey != "" {
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

// normalizeFTSRequest validates a full-text request and resolves its limit.
// Shared by HTTP and MCP so both reject the same requests (#328).
func (s *Server) normalizeFTSRequest(req *FTSSearchRequest) error {
	if req.Collection == "" || req.Query == "" {
		return fmt.Errorf("missing required fields: collection, query")
	}
	// RAG-001: request > collection profile > this path's historical default.
	req.Limit = s.ResolveTopK(req.Collection, req.Limit, 50)
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
	return req.ValidatePassageOptions()
}

// searchFTS runs a full-text search: spell correction, mode, filters, ranges,
// boosts, facets, highlights and the context budget. It is the one
// implementation behind POST /v1/fts and the MCP full_text_search tool; MCP
// had a reduced copy without filters, modes or stemming control (#328).
func (s *Server) searchFTS(req FTSSearchRequest, start time.Time) (*FTSSearchResponse, error) {
	if err := s.normalizeFTSRequest(&req); err != nil {
		return nil, err
	}
	req.GroupBy = s.ResolveGroupBy(req.Collection, req.GroupBy)
	if err := req.GroupBy.Validate(); err != nil {
		return nil, err
	}
	// Spell-correct the query when enabled for this collection
	var spellCorrected *spell.SpellCorrectionInfo
	if s.SpellManager != nil && s.SpellManager.Ready() && s.CollectionManager != nil {
		if cfg, ok := s.CollectionManager.Get(req.Collection); ok && cfg.SpellCorrect {
			lang := req.Lang
			if cfg.SpellLang != "" {
				lang = cfg.SpellLang
			}
			corrected := s.SpellManager.Cleanup(req.Collection, lang, req.Query)
			if corrected != req.Query {
				spellCorrected = &spell.SpellCorrectionInfo{Original: req.Query, Corrected: corrected}
				req.Query = corrected
			}
		}
	}

	// Per-query stemming/synonym control on a view of the index, never on
	// the shared one (see FTSIndex.QueryView).
	origStemmer := s.FTSIndex.Stemmer()
	idx := s.FTSIndex.QueryView(req.DisableStem, req.DisableSynonyms)

	// Resolve query language
	queryLang := req.Lang

	algo := req.Algorithm
	if algo == "" {
		algo = "tfidf"
	}
	fuzzy := req.Fuzzy
	if fuzzy < 0 {
		fuzzy = 0
	}
	if fuzzy > 2 {
		fuzzy = 2
	}

	// Determine search mode
	mode := req.Mode
	if mode == "" || mode == "auto" {
		parsed := fts.ParseAdvancedQuery(req.Query)
		if parsed.IsAdvanced() {
			if parsed.HasPhrase && !parsed.HasBoolean && !parsed.HasWildcard && !parsed.HasProximity {
				mode = "phrase"
			} else if parsed.HasProximity && !parsed.HasBoolean && !parsed.HasWildcard {
				mode = "proximity"
			} else if parsed.HasWildcard && !parsed.HasBoolean && !parsed.HasPhrase && !parsed.HasProximity {
				mode = "wildcard"
			} else {
				mode = "boolean"
			}
		} else {
			mode = "simple"
		}
	}

	// Pre-filter by metadata (in-graph filtering)
	var allowed map[string]bool
	// rangeMeta on metadata keys joins the index filter (SRCH-017); only
	// timestamp ranges still need the documents.
	filter, docRanges := rangeMetaAsFilter(req.Filter, req.RangeMeta)
	if ids, active := s.allowedDocIDs(req.Collection, req.FilterMeta, filter); active {
		allowed = ids
		if len(allowed) == 0 {
			return &FTSSearchResponse{
				Results:   []FTSResultWithDoc{},
				Algorithm: algo,
				Mode:      mode,
				Fuzzy:     fuzzy,
			}, nil
		}
	}

	// Tokenize query (needed for bm25f, reused below) — language-aware
	tokens := idx.TokenizeQueryLang(req.Collection, req.Query, queryLang)

	// Filters, boosts and facetScope "all" act on the whole match set, so
	// the index must return all of it; the limit is applied once they have
	// run. Searching only the top req.Limit first let a filter keep just the
	// matches that happened to rank there (#311). The search functions score
	// every match either way — the limit only truncates their sorted output.
	searchLimit := req.Limit
	if allowed != nil || len(docRanges) > 0 || len(req.Boost) > 0 || req.FacetScope == facetScopeAll || req.GroupBy != nil {
		searchLimit = 0
	}

	var results []fts.FTSResult
	var err error

	switch mode {
	case "boolean":
		parsed := fts.ParseAdvancedQuery(req.Query)
		results, err = idx.SearchBoolean(req.Collection, parsed, searchLimit)

	case "phrase":
		// Extract phrase text (strip quotes if present)
		phraseText := strings.Trim(req.Query, "\"")
		results, err = idx.SearchPhrase(req.Collection, phraseText, searchLimit)

	case "proximity":
		parsed := fts.ParseAdvancedQuery(req.Query)
		distance := req.Distance
		if distance <= 0 {
			distance = 5 // default proximity distance
		}
		// If parsed has proximity clause, use its distance
		for _, c := range parsed.Clauses {
			if c.Type == "proximity" {
				distance = c.Distance
				results, err = idx.SearchProximity(req.Collection, c.Value, distance, searchLimit)
				break
			}
		}
		if results == nil && err == nil {
			phraseText := strings.Trim(req.Query, "\"")
			results, err = idx.SearchProximity(req.Collection, phraseText, distance, searchLimit)
		}

	case "wildcard":
		results, err = idx.SearchWildcard(req.Collection, req.Query, searchLimit)

	case "expression":
		var expr fts.QueryExpr
		expr, err = fts.ParseQueryExpression(req.Query)
		if err != nil {
			// Includes ErrEmptyQueryExpression: a whitespace-only expression
			// query passes the earlier non-empty check but means nothing, and
			// used to return an empty result set as though it had been asked
			// a real question (TEST-003).
			return nil, err
		}
		results, err = idx.EvaluateExpression(req.Collection, expr, searchLimit)

	default: // "simple"
		mode = "simple"
		switch algo {
		case "bm25f":
			if fuzzy > 0 {
				results, err = idx.SearchBM25FFuzzy(req.Collection, tokens, searchLimit, fuzzy, req.FieldWeights, req.bm25)
			} else {
				results, err = idx.SearchBM25F(req.Collection, tokens, searchLimit, req.FieldWeights, req.bm25)
			}
		case "bm25":
			if fuzzy > 0 {
				results, err = idx.SearchBM25Fuzzy(req.Collection, req.Query, searchLimit, fuzzy, req.bm25)
			} else {
				results, err = idx.SearchBM25(req.Collection, req.Query, searchLimit, req.bm25)
			}
		case "tfidf":
			if fuzzy > 0 {
				results, err = idx.SearchFuzzy(req.Collection, req.Query, searchLimit, fuzzy)
			} else {
				results, err = idx.Search(req.Collection, req.Query, searchLimit)
			}
		case "pmisparse":
			if fuzzy > 0 {
				results, err = idx.SearchPMISparseFuzzy(req.Collection, req.Query, searchLimit, fuzzy)
			} else {
				results, err = idx.SearchPMISparse(req.Collection, req.Query, searchLimit)
			}
		default:
			return nil, fmt.Errorf("unknown algorithm: %s, available: tfidf, bm25, bm25f, pmisparse", algo)
		}
	}
	if err != nil {
		return nil, err
	}

	// Track FTS search operation
	if s.Metrics != nil {
		s.Metrics.IncOp("fts_search", mode+"/"+algo)
	}
	// AGT-004: a caller that finishes the response itself counts it too.
	if !req.skipContextBudget {
		s.Gain.Search(gainKind{Type: SearchTypeFTS, Algorithm: algo})
	}

	// Apply metadata filter to results
	if allowed != nil {
		filtered := results[:0]
		for _, r := range results {
			if allowed[r.DocID] {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	// Apply range filters
	if len(docRanges) > 0 && len(results) > 0 {
		results, err = s.FilterByRange(req.Collection, results, docRanges)
		if err != nil {
			return nil, err
		}
	}

	// Apply per-query boosts/demotions (metadata-based score multipliers).
	results = s.applyBoostFTS(req.Collection, results, req.Boost)

	// facetScope "all": count over every match, before pagination (#311).
	var allFacets FacetResult
	var matched int
	if req.FacetScope == facetScopeAll {
		ids := make(map[string]bool, len(results))
		for _, r := range results {
			ids[r.DocID] = true
		}
		matched = len(ids)
		allFacets = s.computeFacetsOverMatches(req.Collection, ids, req.FacetBy, req.FacetMaxValues)
	}

	// Load only the page, unless curation may hide some of it: then load all
	// and let the truncation after curation pick the page.
	window := req.Limit
	if req.GroupBy != nil || (s.CurationManager != nil && len(s.CurationManager.MatchingRules(req.Collection, req.Query)) > 0) {
		window = 0
	}

	// Load full documents
	var resp FTSSearchResponse
	resp.Algorithm = algo
	resp.Mode = mode
	resp.Fuzzy = fuzzy
	resp.Lang = queryLang
	resp.StemmingActive = origStemmer != nil && !req.DisableStem
	resp.SynonymsActive = s.FTSIndex.SynonymManager() != nil && !req.DisableSynonyms
	if algo == "bm25f" {
		resp.FieldWeights = req.FieldWeights
	}
	resp.Results = make([]FTSResultWithDoc, 0, len(results))
	hlOpts := fts.HighlightOptions{
		OpenTag:      req.HighlightTag,
		MaxFragments: req.MaxHighlights,
		FragmentSize: req.FragmentSize,
	}
	_ = s.DBViewC(req.Collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		for _, res := range results {
			if window > 0 && len(resp.Results) >= window {
				break
			}
			v := bDocs.Get(storage.DocKey(req.Collection, res.DocID))
			if v == nil {
				continue
			}
			docPtr, err := loadDoc(v)
			if err != nil {
				continue
			}
			// Skip expired docs
			if docPtr.ExpiresAt > 0 && docPtr.ExpiresAt < currentUnix() {
				continue
			}
			item := FTSResultWithDoc{
				Document:     *docPtr,
				Score:        res.Score,
				MatchedTerms: res.MatchedTerms,
			}
			if req.Highlight {
				item.Highlights = fts.ExtractHighlights(docPtr.ContentMD, res.MatchedTerms, hlOpts)
			}
			resp.Results = append(resp.Results, item)
		}
		return nil
	})
	// Curation: pin/hide must run AFTER filtering and range/boost but BEFORE
	// facets so facet counts reflect what the client actually sees.
	resp.Results = s.applyCurationFTS(req.Collection, req.Query, resp.Results)
	resp.Results = groupTop(resp.Results, req.GroupBy,
		func(r FTSResultWithDoc) string { return r.Document.ID },
		func(r FTSResultWithDoc) string {
			return req.GroupBy.label(req.Collection, r.Document.ID, r.Document.Meta)
		},
		func(r *FTSResultWithDoc, t groupTag) { r.Group, r.GroupRank = t.Group, t.Rank })
	if req.Limit > 0 && len(resp.Results) > req.Limit {
		resp.Results = resp.Results[:req.Limit]
	}

	// RAG-001: cap the total context this collection hands back. Applied
	// before Total and facets so both describe what the caller actually
	// receives — a count that includes dropped results is worse than no count.
	// With passages the budget counts the passages, which is what is sent.
	if !req.skipContextBudget && !req.wantsPassages() {
		var contextTruncated bool
		resp.Results, contextTruncated = applyContextBudget(s, req.Collection, resp.Results,
			func(r FTSResultWithDoc) int { return approxTokens(r.Document.ContentMD) })
		resp.ContextTruncated = contextTruncated
		countHits(s, resp.Results, func(r FTSResultWithDoc) gainSize { return wholeDoc(r.Document.ContentMD) })
	}

	resp.Total = len(resp.Results)

	if req.FacetScope == facetScopeAll {
		resp.Facets = allFacets
		resp.Matched = matched
	} else if len(req.FacetBy) > 0 && len(resp.Results) > 0 {
		docs := make([]storage.Doc, len(resp.Results))
		for i, it := range resp.Results {
			docs[i] = it.Document
		}
		resp.Facets = computeFacets(docs, req.FacetBy, req.FacetMaxValues)
	}

	if searchStatsEnabled() {
		terms := make([]string, 0, len(tokens))
		for t := range tokens {
			terms = append(terms, t)
		}
		resp.Stats = &SearchStats{
			DurationMs:  float64(time.Since(start).Microseconds()) / 1000.0,
			QueryTerms:  terms,
			IndexSize:   resp.Total,
			TotalTokens: len(terms),
		}
	}

	resp.SpellCorrected = spellCorrected

	if req.wantsPassages() {
		hits := make([]passageSource, 0, len(resp.Results))
		for _, r := range resp.Results {
			hits = append(hits, passageSource{doc: r.Document, score: r.Score, bestChunk: -1,
				highlights: r.Highlights, terms: r.MatchedTerms})
		}
		p := s.finishPassages(req.Collection, s.buildPassages(req.Collection, hits, req.PassageOptions, "fts"), nil)
		if req.IncludeImages {
			s.attachPassageImages(&p) // RAG-010
		}
		resp.passages = &p
	} else if req.IncludeImages {
		// RAG-010: each hit's images, by alt text and caption found or not.
		attachImages(s, resp.Results, func(it *FTSResultWithDoc) imageOwner { return docImages(req.Collection, &it.Document) },
			func(it *FTSResultWithDoc, refs []ImageRef) { it.Images = refs })
	}

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
