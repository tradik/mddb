package main

import (
	"fmt"
	"strings"

	"mddb/internal/fts"
	"mddb/internal/spell"
)

// The steps of searchFTS up to the index's answer: spell correction, the
// resolved algorithm, mode and typo tolerance, the metadata pre-filter and
// the search itself. fts_search_results.go turns the answer into the
// response.

// ftsModeProximity is the search mode, and the query clause type, of words
// within a distance of each other.
const ftsModeProximity = "proximity"

// ftsRun is one full-text search in progress: the request and what searchFTS
// resolved from it, handed from one step to the next.
type ftsRun struct {
	s   *Server
	req *FTSSearchRequest
	rr  *rerankRun // RAG-011: reads the corrected query, the search fetches its pool
	// idx is a view of the index with the request's stemming and synonym
	// control, never the shared one (see FTSIndex.QueryView).
	idx *fts.FTSIndex
	// stemming is whether the shared index has a stemmer at all.
	stemming bool
	algo     string // tfidf when the request named none
	mode     string // the request's, or the one its syntax implies for "auto"
	fuzzy    int    // typo tolerance, clamped to 0..2
	// allowed is the set the metadata filter lets through; nil when the
	// request has no metadata filter.
	allowed map[string]bool
	// docRanges are the timestamp ranges that need the documents; ranges on
	// metadata keys joined the index filter (SRCH-017).
	docRanges []RangeFilter
	tokens    map[string]int // the query, tokenized for its language
}

// spellCorrectFTS replaces req.Query with its spelling correction when the
// collection has spell correction on, and reports what it changed; nil when
// it changed nothing.
func (s *Server) spellCorrectFTS(req *FTSSearchRequest) *spell.SpellCorrectionInfo {
	if s.SpellManager == nil || !s.SpellManager.Ready() || s.CollectionManager == nil {
		return nil
	}
	cfg, ok := s.CollectionManager.Get(req.Collection)
	if !ok || !cfg.SpellCorrect {
		return nil
	}
	lang := req.Lang
	if cfg.SpellLang != "" {
		lang = cfg.SpellLang
	}
	corrected := s.SpellManager.Cleanup(req.Collection, lang, req.Query)
	if corrected == req.Query {
		return nil
	}
	info := &spell.SpellCorrectionInfo{Original: req.Query, Corrected: corrected}
	req.Query = corrected
	return info
}

// newFTSRun resolves a normalized, spell-corrected request into a run.
func (s *Server) newFTSRun(req *FTSSearchRequest) *ftsRun {
	algo := req.Algorithm
	if algo == "" {
		algo = "tfidf"
	}
	return &ftsRun{
		s:        s,
		req:      req,
		rr:       s.newRerankRun(req.Collection, req.Query, req.Rerank, false),
		stemming: s.FTSIndex.Stemmer() != nil,
		idx:      s.FTSIndex.QueryView(req.DisableStem, req.DisableSynonyms),
		algo:     algo,
		fuzzy:    min(max(req.Fuzzy, 0), 2),
		mode:     resolveFTSMode(req.Mode, req.Query),
	}
}

// resolveFTSMode is the search mode: the requested one, or for "" and "auto"
// the one the query's syntax implies — a lone phrase, proximity or wildcard
// clause picks that mode, any other advanced syntax boolean, plain words
// simple.
func resolveFTSMode(mode, query string) string {
	if mode != "" && mode != "auto" {
		return mode
	}
	parsed := fts.ParseAdvancedQuery(query)
	if !parsed.IsAdvanced() {
		return "simple"
	}
	switch {
	case parsed.HasPhrase && !parsed.HasBoolean && !parsed.HasWildcard && !parsed.HasProximity:
		return "phrase"
	case parsed.HasProximity && !parsed.HasBoolean && !parsed.HasWildcard:
		return ftsModeProximity
	case parsed.HasWildcard && !parsed.HasBoolean && !parsed.HasPhrase && !parsed.HasProximity:
		return "wildcard"
	default:
		return "boolean"
	}
}

// preFilter resolves the metadata filter (in-graph filtering) to the
// documents it allows. It returns false when the filter allows none, so there
// is nothing to search.
func (r *ftsRun) preFilter() bool {
	filter, docRanges := rangeMetaAsFilter(r.req.Filter, r.req.RangeMeta)
	r.docRanges = docRanges
	ids, active := r.s.allowedDocIDs(r.req.Collection, r.req.FilterMeta, filter)
	if !active {
		return true
	}
	r.allowed = ids
	return len(ids) > 0
}

// emptyResponse is the answer when the metadata filter allows no document.
func (r *ftsRun) emptyResponse() *FTSSearchResponse {
	return &FTSSearchResponse{
		Results:   []FTSResultWithDoc{},
		Algorithm: r.algo,
		Mode:      r.mode,
		Fuzzy:     r.fuzzy,
	}
}

// searchLimit is how many results to ask the index for. Filters, boosts and
// facetScope "all" act on the whole match set, so the index must return all
// of it; the limit is applied once they have run. Searching only the top
// req.Limit first let a filter keep just the matches that happened to rank
// there (#311). The search functions score every match either way — the
// limit only truncates their sorted output.
func (r *ftsRun) searchLimit() int {
	limit := r.rr.pool(r.req.Limit)
	if r.allowed != nil || len(r.docRanges) > 0 || len(r.req.Boost) > 0 ||
		r.req.FacetScope == facetScopeAll || r.req.GroupBy != nil {
		return 0
	}
	return limit
}

// search tokenizes the query and asks the index in the run's mode. A mode it
// does not know searches as "simple", and the run reports it so.
func (r *ftsRun) search() ([]fts.FTSResult, error) {
	// Tokenize query (needed for bm25f, reused for the stats) — language-aware
	r.tokens = r.idx.TokenizeQueryLang(r.req.Collection, r.req.Query, r.req.Lang)
	limit := r.searchLimit()
	collection, query := r.req.Collection, r.req.Query
	switch r.mode {
	case "boolean":
		return r.idx.SearchBoolean(collection, fts.ParseAdvancedQuery(query), limit)
	case "phrase":
		// Extract phrase text (strip quotes if present)
		return r.idx.SearchPhrase(collection, strings.Trim(query, "\""), limit)
	case ftsModeProximity:
		return r.searchProximity(limit)
	case "wildcard":
		return r.idx.SearchWildcard(collection, query, limit)
	case "expression":
		return r.searchExpression(limit)
	default:
		r.mode = "simple"
		return r.searchSimple(limit)
	}
}

// searchProximity searches the query's first proximity clause at its own
// distance. With no such clause, or one that found nothing, it searches the
// whole query (quotes stripped) at that distance, else the request's, else 5.
func (r *ftsRun) searchProximity(limit int) ([]fts.FTSResult, error) {
	distance := r.req.Distance
	if distance <= 0 {
		distance = 5 // default proximity distance
	}
	for _, c := range fts.ParseAdvancedQuery(r.req.Query).Clauses {
		if c.Type != ftsModeProximity {
			continue
		}
		distance = c.Distance
		results, err := r.idx.SearchProximity(r.req.Collection, c.Value, distance, limit)
		if results != nil || err != nil {
			return results, err
		}
		break
	}
	phraseText := strings.Trim(r.req.Query, "\"")
	return r.idx.SearchProximity(r.req.Collection, phraseText, distance, limit)
}

// searchExpression evaluates a query expression.
func (r *ftsRun) searchExpression(limit int) ([]fts.FTSResult, error) {
	expr, err := fts.ParseQueryExpression(r.req.Query)
	if err != nil {
		// Includes ErrEmptyQueryExpression: a whitespace-only expression
		// query passes the earlier non-empty check but means nothing, and
		// used to return an empty result set as though it had been asked
		// a real question (TEST-003).
		return nil, err
	}
	return r.idx.EvaluateExpression(r.req.Collection, expr, limit)
}

// searchSimple ranks plain words with the run's algorithm, typo-tolerant
// when fuzzy is set.
func (r *ftsRun) searchSimple(limit int) ([]fts.FTSResult, error) {
	req, fuzzy := r.req, r.fuzzy
	switch r.algo {
	case "bm25f":
		if fuzzy > 0 {
			return r.idx.SearchBM25FFuzzy(req.Collection, r.tokens, limit, fuzzy, req.FieldWeights, req.bm25)
		}
		return r.idx.SearchBM25F(req.Collection, r.tokens, limit, req.FieldWeights, req.bm25)
	case "bm25":
		if fuzzy > 0 {
			return r.idx.SearchBM25Fuzzy(req.Collection, req.Query, limit, fuzzy, req.bm25)
		}
		return r.idx.SearchBM25(req.Collection, req.Query, limit, req.bm25)
	case "tfidf":
		if fuzzy > 0 {
			return r.idx.SearchFuzzy(req.Collection, req.Query, limit, fuzzy)
		}
		return r.idx.Search(req.Collection, req.Query, limit)
	case "pmisparse":
		if fuzzy > 0 {
			return r.idx.SearchPMISparseFuzzy(req.Collection, req.Query, limit, fuzzy)
		}
		return r.idx.SearchPMISparse(req.Collection, req.Query, limit)
	default:
		return nil, fmt.Errorf("unknown algorithm: %s, available: tfidf, bm25, bm25f, pmisparse", r.algo)
	}
}
