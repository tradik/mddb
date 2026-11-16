package main

import (
	"context"
	"time"

	"mddb/internal/fts"
	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// The steps of searchFTS after the index answered: filters, boosts, facets,
// the documents, rerank, curation, grouping, the context budget, stats,
// passages and images. fts_search_run.go has the steps before.

// countSearch records the search in the metrics and, unless the caller
// finishes the response itself and counts it there (AGT-004), the gain.
func (r *ftsRun) countSearch() {
	if r.s.Metrics != nil {
		r.s.Metrics.IncOp("fts_search", r.mode+"/"+r.algo)
	}
	if !r.req.skipContextBudget {
		r.s.Gain.Search(gainKind{Type: SearchTypeFTS, Algorithm: r.algo})
	}
}

// postFilter applies the metadata filter, the timestamp ranges and the
// per-query boosts/demotions (metadata-based score multipliers) to the
// index's answer.
func (r *ftsRun) postFilter(results []fts.FTSResult) ([]fts.FTSResult, error) {
	if r.allowed != nil {
		filtered := results[:0]
		for _, res := range results {
			if r.allowed[res.DocID] {
				filtered = append(filtered, res)
			}
		}
		results = filtered
	}
	if len(r.docRanges) > 0 && len(results) > 0 {
		var err error
		if results, err = r.s.FilterByRange(r.req.Collection, results, r.docRanges); err != nil {
			return nil, err
		}
	}
	return r.s.applyBoostFTS(r.req.Collection, results, r.req.Boost), nil
}

// facetsOverAll counts facets over every match, before pagination, for
// facetScope "all" (#311), and how many documents matched. Zero values for
// any other scope.
func (r *ftsRun) facetsOverAll(results []fts.FTSResult) (FacetResult, int) {
	if r.req.FacetScope != facetScopeAll {
		return nil, 0
	}
	ids := make(map[string]bool, len(results))
	for _, res := range results {
		ids[res.DocID] = true
	}
	return r.s.computeFacetsOverMatches(r.req.Collection, ids, r.req.FacetBy, r.req.FacetMaxValues), len(ids)
}

// newResponse is the response with what the run resolved, before results.
func (r *ftsRun) newResponse() FTSSearchResponse {
	resp := FTSSearchResponse{
		Algorithm:      r.algo,
		Mode:           r.mode,
		Fuzzy:          r.fuzzy,
		Lang:           r.req.Lang,
		StemmingActive: r.stemming && !r.req.DisableStem,
		SynonymsActive: r.s.FTSIndex.SynonymManager() != nil && !r.req.DisableSynonyms,
	}
	if r.algo == "bm25f" {
		resp.FieldWeights = r.req.FieldWeights
	}
	return resp
}

// loadWindow is how many documents to load: only the page, unless curation
// may hide some of it or grouping picks from all — then 0, all of them, and
// the truncation after curation picks the page.
func (r *ftsRun) loadWindow() int {
	if r.req.GroupBy != nil {
		return 0
	}
	if cm := r.s.CurationManager; cm != nil && len(cm.MatchingRules(r.req.Collection, r.req.Query)) > 0 {
		return 0
	}
	return r.rr.pool(r.req.Limit)
}

// loadResults loads the documents of the results, in order, up to the load
// window; a document that is gone, unreadable or expired is skipped.
func (r *ftsRun) loadResults(results []fts.FTSResult) []FTSResultWithDoc {
	window := r.loadWindow()
	out := make([]FTSResultWithDoc, 0, len(results))
	_ = r.s.DBViewC(r.req.Collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		for _, res := range results {
			if window > 0 && len(out) >= window {
				break
			}
			if item, ok := r.resultWithDoc(bDocs.Get(storage.DocKey(r.req.Collection, res.DocID)), res); ok {
				out = append(out, item)
			}
		}
		return nil
	})
	return out
}

// resultWithDoc decodes a stored document into a result, with highlights
// when asked. ok is false for a missing, unreadable or expired document.
func (r *ftsRun) resultWithDoc(v []byte, res fts.FTSResult) (FTSResultWithDoc, bool) {
	if v == nil {
		return FTSResultWithDoc{}, false
	}
	docPtr, err := loadResultDoc(v)
	if err != nil {
		return FTSResultWithDoc{}, false
	}
	if docPtr.ExpiresAt > 0 && docPtr.ExpiresAt < currentUnix() {
		return FTSResultWithDoc{}, false
	}
	item := FTSResultWithDoc{
		Document:     *docPtr,
		Score:        res.Score,
		MatchedTerms: res.MatchedTerms,
	}
	if r.req.Highlight {
		item.Highlights = fts.ExtractHighlights(docPtr.ContentMD, res.MatchedTerms, fts.HighlightOptions{
			OpenTag:      r.req.HighlightTag,
			MaxFragments: r.req.MaxHighlights,
			FragmentSize: r.req.FragmentSize,
		})
	}
	return item, true
}

// finishResults reranks, curates, groups and pages the loaded results.
// Curation (pin/hide) runs after filtering and range/boost but before facets
// so facet counts reflect what the client actually sees.
func (r *ftsRun) finishResults(ctx context.Context, results []FTSResultWithDoc) []FTSResultWithDoc {
	req := r.req
	results = r.s.rerankFTSResults(ctx, r.rr, req.Collection, results)
	req.Limit = r.rr.cut(req.Limit)
	results = r.s.applyCurationFTS(req.Collection, req.Query, results)
	results = groupTop(results, req.GroupBy,
		func(it FTSResultWithDoc) string { return it.Document.ID },
		func(it FTSResultWithDoc) string {
			return req.GroupBy.label(req.Collection, it.Document.ID, it.Document.Meta)
		},
		func(it *FTSResultWithDoc, t groupTag) { it.Group, it.GroupRank = t.Group, t.Rank })
	if req.Limit > 0 && len(results) > req.Limit {
		results = results[:req.Limit]
	}
	return results
}

// applyBudget caps the total context this collection hands back (RAG-001).
// Applied before Total and facets so both describe what the caller actually
// receives — a count that includes dropped results is worse than no count.
// With passages the budget counts the passages, which is what is sent.
func (r *ftsRun) applyBudget(resp *FTSSearchResponse) {
	if r.req.skipContextBudget || r.req.wantsPassages() {
		return
	}
	resp.Results, resp.ContextTruncated = applyContextBudget(r.s, r.req.Collection, resp.Results,
		func(it FTSResultWithDoc) int { return approxTokens(it.Document.ContentMD) })
	countHits(r.s, resp.Results, func(it FTSResultWithDoc) gainSize { return wholeDoc(it.Document.ContentMD) })
}

// setFacets reports the facets: the ones counted over every match for
// facetScope "all", else the ones of the returned results when asked.
func (r *ftsRun) setFacets(resp *FTSSearchResponse, allFacets FacetResult, matched int) {
	if r.req.FacetScope == facetScopeAll {
		resp.Facets = allFacets
		resp.Matched = matched
		return
	}
	if len(r.req.FacetBy) == 0 || len(resp.Results) == 0 {
		return
	}
	docs := make([]storage.Doc, len(resp.Results))
	for i, it := range resp.Results {
		docs[i] = it.Document
	}
	resp.Facets = computeFacets(docs, r.req.FacetBy, r.req.FacetMaxValues)
}

// stats are the search statistics, or nil when they are off.
func (r *ftsRun) stats(start time.Time, total int) *SearchStats {
	if !searchStatsEnabled() {
		return nil
	}
	terms := make([]string, 0, len(r.tokens))
	for t := range r.tokens {
		terms = append(terms, t)
	}
	return &SearchStats{
		DurationMs:  float64(time.Since(start).Microseconds()) / 1000.0,
		QueryTerms:  terms,
		IndexSize:   total,
		TotalTokens: len(terms),
	}
}

// attachExtras builds the passages for format "passages" (AGT-002), which
// the caller then gets instead of the response, and attaches the images
// asked for (RAG-010) to the passages or to each hit.
func (r *ftsRun) attachExtras(resp *FTSSearchResponse) {
	req := r.req
	if req.wantsPassages() {
		hits := make([]passageSource, 0, len(resp.Results))
		for _, it := range resp.Results {
			hits = append(hits, passageSource{doc: it.Document, score: it.Score, bestChunk: -1,
				highlights: it.Highlights, terms: it.MatchedTerms})
		}
		p := r.s.finishPassages(req.Collection, r.s.buildPassages(req.Collection, hits, req.PassageOptions, "fts"), nil)
		if req.IncludeImages {
			r.s.attachPassageImages(&p) // RAG-010
		}
		p.LimitReport = req.limitReport
		p.RerankResult = resp.RerankResult
		resp.passages = &p
		return
	}
	if req.IncludeImages {
		// RAG-010: each hit's images, by alt text and caption found or not.
		attachImages(r.s, resp.Results, func(it *FTSResultWithDoc) imageOwner { return docImages(req.Collection, &it.Document) },
			func(it *FTSResultWithDoc, refs []ImageRef) { it.Images = refs })
	}
}
