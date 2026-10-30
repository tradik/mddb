package main

import (
	"mddb/internal/fts"
)

// runFTSSearch executes the FTS portion of hybrid search.
func (s *Server) runFTSSearch(req HybridSearchRequest) ([]fts.FTSResult, error) {
	if s.FTSIndex == nil {
		return nil, nil
	}
	bm25, err := s.ResolveBM25(req.Collection, req.BM25K1, req.BM25B)
	if err != nil {
		return nil, err
	}

	// Per-query stemming/synonym control on a view of the index, never on
	// the shared one (see FTSIndex.QueryView).
	idx := s.FTSIndex.QueryView(req.DisableStem, req.DisableSynonyms)

	allowed, none := s.hybridFTSAllowed(req)
	if none {
		return nil, nil
	}

	// Oversample: get more results for better merging (SRCH-005). A higher
	// factor gives the fusion more to work with on both sides.
	searchLimit := OversampledTopK(req.TopK, s.ResolveOversample(req.Collection, req.Oversample), 50)
	// A metadata filter applies to every text match, not to the top
	// searchLimit of the unfiltered ranking (#311): search them all, filter,
	// then keep searchLimit.
	candidates := searchLimit
	if allowed != nil {
		candidates = 0
	}

	results, err := s.hybridFTSQuery(idx, req, candidates, bm25)
	if err != nil {
		return nil, err
	}
	return keepAllowedFTS(results, allowed, searchLimit), nil
}

// hybridFTSAllowed is the set of documents the full-text half may return:
// the metadata pre-filter, intersected with the request's own ID restriction.
// A nil set means unrestricted; none reports a restriction nothing passes.
func (s *Server) hybridFTSAllowed(req HybridSearchRequest) (allowed map[string]bool, none bool) {
	if ids, active := s.allowedDocIDs(req.Collection, req.FilterMeta, req.Filter); active {
		if len(ids) == 0 {
			return nil, true
		}
		allowed = ids
	}
	if req.onlyIDs == nil {
		return allowed, false
	}
	if allowed == nil {
		allowed = req.onlyIDs
	} else {
		allowed = intersectSets(allowed, req.onlyIDs)
	}
	return allowed, len(allowed) == 0
}

// hybridFTSQuery runs the request's text algorithm, fuzzy when asked. An
// unknown algorithm runs as bm25.
func (s *Server) hybridFTSQuery(idx *fts.FTSIndex, req HybridSearchRequest, candidates int, bm25 fts.BM25Params) ([]fts.FTSResult, error) {
	switch req.Algorithm {
	case "bm25f":
		// RAG-009: meta.summary joins the fields while summaries are on.
		weights := s.summaryFieldWeights(req.Collection, s.ResolveSummaryMode(req.Collection, req.summaryMode), req.FieldWeights)
		tokens := idx.TokenizeQueryLang(req.Collection, req.Query, req.Lang)
		if req.Fuzzy > 0 {
			return idx.SearchBM25FFuzzy(req.Collection, tokens, candidates, req.Fuzzy, weights, bm25)
		}
		return idx.SearchBM25F(req.Collection, tokens, candidates, weights, bm25)
	case "pmisparse":
		if req.Fuzzy > 0 {
			return idx.SearchPMISparseFuzzy(req.Collection, req.Query, candidates, req.Fuzzy)
		}
		return idx.SearchPMISparse(req.Collection, req.Query, candidates)
	default: // "bm25"
		if req.Fuzzy > 0 {
			return idx.SearchBM25Fuzzy(req.Collection, req.Query, candidates, req.Fuzzy, bm25)
		}
		return idx.SearchBM25(req.Collection, req.Query, candidates, bm25)
	}
}

// keepAllowedFTS drops results outside the allowed set and keeps searchLimit
// of the rest. A nil set leaves the results as the index ranked them.
func keepAllowedFTS(results []fts.FTSResult, allowed map[string]bool, searchLimit int) []fts.FTSResult {
	if allowed == nil {
		return results
	}
	filtered := results[:0]
	for _, r := range results {
		if allowed[r.DocID] {
			filtered = append(filtered, r)
		}
	}
	if searchLimit > 0 && len(filtered) > searchLimit {
		filtered = filtered[:searchLimit]
	}
	return filtered
}
