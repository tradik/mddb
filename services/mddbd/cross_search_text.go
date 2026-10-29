package main

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Cross-collection full-text and hybrid search (AGT-003).
//
// Cross-search was vector-only, so a question that names a symbol — exact
// words, where embeddings blur — could not be asked of several collections at
// once. searchType "fts" and "hybrid" run the existing single-collection
// search in each target and fuse the lists by reciprocal rank: a collection's
// scores are not comparable with another's, its ranks are.
//
// A target of the form "role:<role>" stands for every collection whose
// code.role is <role>, so "role:reference" searches all reference code.

// Cross-search types.
const (
	CrossSearchVector = "vector"
	CrossSearchFTS    = "fts"
	CrossSearchHybrid = "hybrid"

	crossRRFK  = 60
	rolePrefix = "role:"
)

// validCrossSearchType rejects a search type cross-search does not run.
func validCrossSearchType(t string) error {
	switch t {
	case "", CrossSearchVector, CrossSearchFTS, CrossSearchHybrid:
		return nil
	}
	return errors.New("searchType must be vector, fts or hybrid")
}

// expandTargets replaces each "role:<role>" with the collections that have
// that code role, sorted, and drops duplicates.
func (s *Server) expandTargets(targets []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(c string) {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	for _, t := range targets {
		role, isRole := strings.CutPrefix(t, rolePrefix)
		if !isRole {
			add(t)
			continue
		}
		var names []string
		if s.CollectionManager != nil {
			for name, cfg := range s.CollectionManager.ListAll() {
				if cfg != nil && cfg.Code != nil && cfg.Code.Role == role {
					names = append(names, name)
				}
			}
		}
		sort.Strings(names)
		for _, n := range names {
			add(n)
		}
	}
	return out
}

// crossTextParams is a cross-collection full-text or hybrid search.
type crossTextParams struct {
	SearchType     string
	Query          string
	Targets        []string
	TopK           int
	FilterMeta     map[string][]string
	Filter         MetaFilter
	GroupBy        *GroupBy
	IncludeContent bool
}

// crossTextItems searches each target and fuses the ranked lists with RRF.
// A target that cannot answer (no full-text index, no such collection) adds
// nothing rather than failing the search.
func (s *Server) crossTextItems(ctx context.Context, p crossTextParams) []CrossSearchResultItem {
	var fused []CrossSearchResultItem
	for _, coll := range p.Targets {
		for rank, it := range s.oneCollectionText(ctx, coll, p) {
			it.Score = float32(1.0 / float64(crossRRFK+rank+1))
			fused = append(fused, it)
		}
	}
	sort.SliceStable(fused, func(i, j int) bool { return fused[i].Score > fused[j].Score })
	if p.GroupBy != nil {
		fused = groupTop(fused, p.GroupBy,
			func(it CrossSearchResultItem) string { return it.Document.ID },
			func(it CrossSearchResultItem) string {
				return p.GroupBy.label(it.Collection, it.Document.ID, it.Document.Meta)
			},
			func(it *CrossSearchResultItem, t groupTag) { it.Group, it.GroupRank = t.Group, t.Rank })
	}
	if len(fused) > p.TopK {
		fused = fused[:p.TopK]
	}
	for i := range fused {
		fused[i].Rank = i + 1
		if !p.IncludeContent {
			fused[i].Document.ContentMD = ""
		}
	}
	return fused
}

// oneCollectionText runs the single-collection search of p's type.
func (s *Server) oneCollectionText(ctx context.Context, coll string, p crossTextParams) []CrossSearchResultItem {
	var out []CrossSearchResultItem
	if p.SearchType == CrossSearchFTS {
		resp, err := s.searchFTS(FTSSearchRequest{Collection: coll, Query: p.Query, Limit: p.TopK,
			FilterMeta: p.FilterMeta, Filter: p.Filter, skipContextBudget: true}, time.Now())
		if err != nil {
			return nil
		}
		for _, r := range resp.Results {
			out = append(out, CrossSearchResultItem{Collection: coll, Document: r.Document})
		}
		return out
	}
	items, _, err := s.hybridItems(ctx, HybridSearchRequest{Collection: coll, Query: p.Query, TopK: p.TopK,
		FilterMeta: p.FilterMeta, Filter: p.Filter, IncludeContent: true})
	if err != nil {
		return nil
	}
	for _, it := range items {
		out = append(out, CrossSearchResultItem{Collection: coll, Document: it.Document})
	}
	return out
}

// respondCrossText answers a REST cross-search of type fts or hybrid.
func (s *Server) respondCrossText(w http.ResponseWriter, r *http.Request, req CrossSearchRequest) {
	waited := s.waitForIndexAll(r.Context(), req.TargetCollections, req.IndexWait)
	items := s.crossTextItems(r.Context(), crossTextParams{SearchType: req.SearchType, Query: req.Query,
		Targets: req.TargetCollections, TopK: crossTopK(req.TopK), FilterMeta: req.FilterMeta, Filter: req.Filter,
		GroupBy: req.GroupBy, IncludeContent: req.IncludeContent || req.wantsPassages()})
	if s.Metrics != nil {
		s.Metrics.IncOp("cross_search", req.SearchType)
	}
	s.countCross("", items, req.wantsPassages())
	if req.wantsPassages() {
		ok(w, s.crossPassages(req.Query, items, req.PassageOptions, waited))
		return
	}
	ok(w, CrossSearchResponse{Results: items, Total: len(items), TargetCollections: req.TargetCollections,
		Algorithm: req.SearchType, Indexing: waited})
}

// crossTopK is the result count of a cross-search: 10 unless asked.
func crossTopK(n int) int {
	if n <= 0 {
		return 10
	}
	return n
}
