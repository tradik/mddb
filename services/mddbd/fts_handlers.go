package main

import (
	"bytes"
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
	Collection      string              `json:"collection"`
	Query           string              `json:"query"`
	Limit           int                 `json:"limit"`
	Algorithm       string              `json:"algorithm"`              // "tfidf", "bm25", "bm25f", "pmisparse"
	Fuzzy           int                 `json:"fuzzy"`                  // typo tolerance: 0 (off), 1 (1 edit), 2 (2 edits)
	DisableStem     bool                `json:"disableStem"`            // temporarily disable stemming for this query
	DisableSynonyms bool                `json:"disableSynonyms"`        // temporarily disable synonyms for this query
	FieldWeights    map[string]float64  `json:"fieldWeights,omitempty"` // BM25F field weights
	FilterMeta      map[string][]string `json:"filterMeta,omitempty"`   // metadata pre-filter (in-graph filtering)
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
	// CacheTTL opts this request into the search-result cache for the given
	// number of seconds (GO-031). Omitted or 0 means no caching at all, so a
	// caller that says nothing keeps today's behaviour and today's freshness.
	// Capped at searchCacheMaxTTL.
	CacheTTL int `json:"cacheTtl,omitempty"`
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
	// ContextTruncated reports that the collection's contextTokenBudget
	// dropped results from the tail (RAG-001). Without it, a caller cannot
	// tell a short result list from a capped one.
	ContextTruncated bool `json:"contextTruncated,omitempty"`
}

// FTSResultWithDoc includes the full document in the result.
type FTSResultWithDoc struct {
	Document     storage.Doc     `json:"document"`
	Score        float64         `json:"score"`
	MatchedTerms []string        `json:"matchedTerms"`
	Highlights   []fts.Highlight `json:"highlights,omitempty"` // populated when request.highlight=true
	Pinned       bool            `json:"pinned,omitempty"`     // set by curation rules (v2.9.14+)
}

// --- HTTP handler ---

func (s *Server) handleFTS(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req FTSSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" || req.Query == "" {
		bad(w, fmt.Errorf("missing required fields: collection, query"))
		return
	}
	// RAG-001: request > collection profile > this path's historical default.
	req.Limit = s.ResolveTopK(req.Collection, req.Limit, 50)

	if s.FTSIndex == nil {
		bad(w, fmt.Errorf("full-text search not initialized"))
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

	// Per-query stemming/synonym control (thread-safe: no mutation of shared state)
	origStemmer := s.FTSIndex.Stemmer()
	origSynonyms := s.FTSIndex.SynonymManager()
	if req.DisableStem {
		s.FTSIndex.SetStemmer(nil)
	}
	if req.DisableSynonyms {
		s.FTSIndex.SetSynonymManager(nil)
	}
	defer func() {
		s.FTSIndex.SetStemmer(origStemmer)
		s.FTSIndex.SetSynonymManager(origSynonyms)
	}()

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
	if len(req.FilterMeta) > 0 {
		allowed = s.getDocIDsByMeta(req.Collection, req.FilterMeta)
		if len(allowed) == 0 {
			ok(w, FTSSearchResponse{
				Results:   []FTSResultWithDoc{},
				Algorithm: algo,
				Mode:      mode,
				Fuzzy:     fuzzy,
			})
			return
		}
	}

	// Tokenize query (needed for bm25f, reused below) — language-aware
	tokens := s.FTSIndex.TokenizeQueryLang(req.Collection, req.Query, queryLang)

	var results []fts.FTSResult
	var err error

	switch mode {
	case "boolean":
		parsed := fts.ParseAdvancedQuery(req.Query)
		results, err = s.FTSIndex.SearchBoolean(req.Collection, parsed, req.Limit)

	case "phrase":
		// Extract phrase text (strip quotes if present)
		phraseText := strings.Trim(req.Query, "\"")
		results, err = s.FTSIndex.SearchPhrase(req.Collection, phraseText, req.Limit)

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
				results, err = s.FTSIndex.SearchProximity(req.Collection, c.Value, distance, req.Limit)
				break
			}
		}
		if results == nil && err == nil {
			phraseText := strings.Trim(req.Query, "\"")
			results, err = s.FTSIndex.SearchProximity(req.Collection, phraseText, distance, req.Limit)
		}

	case "wildcard":
		results, err = s.FTSIndex.SearchWildcard(req.Collection, req.Query, req.Limit)

	case "expression":
		var expr fts.QueryExpr
		expr, err = fts.ParseQueryExpression(req.Query)
		if err != nil {
			// Includes ErrEmptyQueryExpression: a whitespace-only expression
			// query passes the earlier non-empty check but means nothing, and
			// used to return an empty result set as though it had been asked
			// a real question (TEST-003).
			bad(w, err)
			return
		}
		results, err = s.FTSIndex.EvaluateExpression(req.Collection, expr, req.Limit)

	default: // "simple"
		mode = "simple"
		switch algo {
		case "bm25f":
			if fuzzy > 0 {
				results, err = s.FTSIndex.SearchBM25FFuzzy(req.Collection, tokens, req.Limit, fuzzy, req.FieldWeights)
			} else {
				results, err = s.FTSIndex.SearchBM25F(req.Collection, tokens, req.Limit, req.FieldWeights)
			}
		case "bm25":
			if fuzzy > 0 {
				results, err = s.FTSIndex.SearchBM25Fuzzy(req.Collection, req.Query, req.Limit, fuzzy)
			} else {
				results, err = s.FTSIndex.SearchBM25(req.Collection, req.Query, req.Limit)
			}
		case "tfidf":
			if fuzzy > 0 {
				results, err = s.FTSIndex.SearchFuzzy(req.Collection, req.Query, req.Limit, fuzzy)
			} else {
				results, err = s.FTSIndex.Search(req.Collection, req.Query, req.Limit)
			}
		case "pmisparse":
			if fuzzy > 0 {
				results, err = s.FTSIndex.SearchPMISparseFuzzy(req.Collection, req.Query, req.Limit, fuzzy)
			} else {
				results, err = s.FTSIndex.SearchPMISparse(req.Collection, req.Query, req.Limit)
			}
		default:
			bad(w, fmt.Errorf("unknown algorithm: %s, available: tfidf, bm25, bm25f, pmisparse", algo))
			return
		}
	}
	if err != nil {
		bad(w, err)
		return
	}

	// Track FTS search operation
	if s.Metrics != nil {
		s.Metrics.IncOp("fts_search", mode+"/"+algo)
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
	if len(req.RangeMeta) > 0 && len(results) > 0 {
		results, err = s.FilterByRange(req.Collection, results, req.RangeMeta)
		if err != nil {
			bad(w, err)
			return
		}
	}

	// Apply per-query boosts/demotions (metadata-based score multipliers).
	results = s.applyBoostFTS(req.Collection, results, req.Boost)

	// Load full documents
	var resp FTSSearchResponse
	resp.Algorithm = algo
	resp.Mode = mode
	resp.Fuzzy = fuzzy
	resp.Lang = queryLang
	resp.StemmingActive = origStemmer != nil && !req.DisableStem
	resp.SynonymsActive = origSynonyms != nil && !req.DisableSynonyms
	if algo == "bm25f" {
		resp.FieldWeights = req.FieldWeights
	}
	resp.Results = make([]FTSResultWithDoc, 0, len(results))
	hlOpts := fts.HighlightOptions{
		OpenTag:      req.HighlightTag,
		MaxFragments: req.MaxHighlights,
		FragmentSize: req.FragmentSize,
	}
	_ = s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		for _, res := range results {
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
	if req.Limit > 0 && len(resp.Results) > req.Limit {
		resp.Results = resp.Results[:req.Limit]
	}

	// RAG-001: cap the total context this collection hands back. Applied
	// before Total and facets so both describe what the caller actually
	// receives — a count that includes dropped results is worse than no count.
	var contextTruncated bool
	resp.Results, contextTruncated = applyContextBudget(s, req.Collection, resp.Results,
		func(r FTSResultWithDoc) int { return approxTokens(r.Document.ContentMD) })
	resp.ContextTruncated = contextTruncated

	resp.Total = len(resp.Results)

	if len(req.FacetBy) > 0 && len(resp.Results) > 0 {
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

	if cacheKey != "" {
		if body, err := json.Marshal(resp); err == nil {
			s.SearchCache.Set(cacheKey, body, int64(cacheTTL))
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write(body); err != nil {
				slog.Debug("writing a search response failed", "err", err)
			}
			return
		}
	}
	ok(w, resp)
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

	// Collect docs first (read tx), then index outside tx to avoid deadlock
	type reindexDoc struct {
		ID        string
		ContentMD string
		Lang      string
		Meta      map[string][]string
	}
	var docs []reindexDoc
	var skipped int
	_ = s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}

		prefix := []byte("doc|" + req.Collection + "|")
		c := bDocs.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			docPtr, err := loadDoc(v)
			if err != nil || docPtr.ContentMD == "" {
				skipped++
				continue
			}
			if docPtr.ExpiresAt > 0 && docPtr.ExpiresAt < currentUnix() {
				skipped++
				continue
			}
			docs = append(docs, reindexDoc{
				ID:        docPtr.ID,
				ContentMD: docPtr.ContentMD,
				Lang:      docPtr.Lang,
				Meta:      docPtr.Meta,
			})
		}
		return nil
	})

	// Index outside the read tx to avoid BoltDB nested tx deadlock
	reindexed := 0
	for _, d := range docs {
		_ = s.FTSIndex.IndexWithLang(req.Collection, d.ID, d.ContentMD, d.Lang)
		_ = s.FTSIndex.IndexPositionsWithLang(req.Collection, d.ID, d.ContentMD, d.Lang)

		fields := map[string]string{"content": d.ContentMD}
		for mk, vals := range d.Meta {
			if len(vals) > 0 {
				fields["meta."+mk] = strings.Join(vals, " ")
			}
		}
		_ = s.FTSIndex.IndexFieldsWithLang(req.Collection, d.ID, fields, d.Lang)
		reindexed++
	}

	ok(w, map[string]interface{}{
		"status":    "ok",
		"reindexed": reindexed,
		"skipped":   skipped,
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
