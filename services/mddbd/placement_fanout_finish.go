package main

import (
	"net/http"
	"sort"
)

// Finishing a cross-search fan-out (GO-052): the parts merged into one list
// and finished as handleCrossSearch finishes its own.

// finishCross merges parts and answers the cross-search.
func (s *Server) finishCross(w http.ResponseWriter, r *http.Request, req CrossSearchRequest, parts []crossPart) {
	topK, limit := mergedLimit(req.TopK, parts)
	merged := mergeParts(req.TargetCollections, parts)
	rr := s.newRerankRun("", req.Query, req.Rerank, req.Explain)
	var items []CrossSearchResultItem
	algorithm, metric := req.SearchType, ""
	if req.SearchType == CrossSearchFTS || req.SearchType == CrossSearchHybrid {
		items = s.crossTextFinish(r.Context(), crossTextParams{TopK: topK, GroupBy: req.GroupBy,
			IncludeContent: req.IncludeContent || req.wantsPassages(), Rerank: rr}, merged)
	} else {
		if pool := rr.pool(topK); req.GroupBy == nil && len(merged) > pool {
			merged = merged[:pool]
		}
		items = s.crossFinish(r.Context(), crossSearchParams{TopK: topK, GroupBy: req.GroupBy, Rerank: rr}, merged)
		algorithm, metric = partsAlgorithm(parts), req.DistanceMetric
		if metric == "" {
			metric = "cosine"
		}
	}
	indexing := firstIndexing(parts)
	s.countCross(algorithm, items, req.wantsPassages())
	if req.wantsPassages() {
		p := s.crossPassages(req.Query, items, req.PassageOptions, indexing)
		p.LimitReport, p.RerankResult = limit, rr.Result()
		ok(w, p)
		return
	}
	ok(w, CrossSearchResponse{Results: items, Total: len(items), SourceCollection: req.SourceCollection,
		SourceDocID: req.SourceDocID, TargetCollections: req.TargetCollections, Algorithm: algorithm,
		DistanceMetric: metric, Indexing: indexing, Plan: req.explained(mergedPlan(parts)),
		LimitReport: limit, RerankResult: rr.Result()})
}

// mergedLimit is the result count: the request's (10 when unset), under the
// strictest cap any part reported.
func mergedLimit(n int, parts []crossPart) (int, LimitReport) {
	if n <= 0 {
		n = 10
	}
	var rep LimitReport
	for _, p := range parts {
		if p.LimitCapped && p.Limit < n {
			n, rep = p.Limit, p.LimitReport
		}
	}
	return n, rep
}

// mergeParts is every part's hits in one list, by score, ties in the order
// the request names the targets.
func mergeParts(targets []string, parts []crossPart) []CrossSearchResultItem {
	order := map[string]int{}
	for i, t := range targets {
		if _, seen := order[t]; !seen {
			order[t] = i
		}
	}
	var all []CrossSearchResultItem
	for _, p := range parts {
		for _, h := range p.Hits {
			all = append(all, CrossSearchResultItem{Collection: h.Collection, Document: h.Document, Score: h.Score,
				Images: h.Images, chunks: h.Chunks, rerankText: h.RerankText})
		}
	}
	rank := func(c string) int {
		if i, ok := order[c]; ok {
			return i
		}
		return len(targets) // expanded from a role: after the named targets
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		ri, rj := rank(all[i].Collection), rank(all[j].Collection)
		if ri != rj {
			return ri < rj
		}
		return ri == len(targets) && all[i].Collection < all[j].Collection
	})
	return all
}

// partsAlgorithm is the algorithm the parts that searched report.
func partsAlgorithm(parts []crossPart) string {
	for _, p := range parts {
		if p.Algorithm != "" {
			return p.Algorithm
		}
	}
	return ""
}

// firstIndexing is the first part's index wait, if any waited.
func firstIndexing(parts []crossPart) *IndexWaitResult {
	for _, p := range parts {
		if p.Indexing != nil {
			return p.Indexing
		}
	}
	return nil
}

// mergedPlan joins the parts' plans: one plan, with every part's targets.
func mergedPlan(parts []crossPart) *SearchPlan {
	var out *SearchPlan
	for _, p := range parts {
		switch {
		case p.Plan == nil:
		case out == nil:
			out = p.Plan
		default:
			for c, cp := range p.Plan.Collections {
				if out.Collections == nil {
					out.Collections = map[string]*SearchPlan{}
				}
				out.Collections[c] = cp
			}
			out.Reasons = append(out.Reasons, p.Plan.Reasons...)
		}
	}
	return out
}
