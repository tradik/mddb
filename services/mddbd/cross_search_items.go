package main

import (
	"sort"

	vec "mddb/internal/vector"
)

// crossSearchParams is what both cross-search surfaces (REST and MCP) pass to
// the one implementation of the search itself. They each carried a copy of
// it, which is how one copy gains a feature and the other does not.
type crossSearchParams struct {
	Searcher       vec.VectorSearcher
	QueryVector    []float32
	Targets        []string
	SearchTopK     int
	TopK           int
	Threshold      float64
	Metric         vec.SimilarityFunc
	FilterMeta     map[string][]string
	Filter         MetaFilter
	GroupBy        *GroupBy
	IncludeContent bool
	// Plan is the search's plan (SRCH-013). For algorithm "auto" each target
	// is planned on its own and its plan recorded under Plan.Collections.
	Plan            *SearchPlan
	LatencyTargetMs int
	// Feedback rescores each target's candidates in rescore mode (SRCH-015);
	// query mode has already corrected QueryVector.
	Feedback *feedback
}

// crossSearcher resolves a cross search's searcher and plan (SRCH-013): the
// named algorithm, checked against the source collection as before, or for
// "auto" flat as the fallback with each target planned in crossSearchItems.
func (s *Server) crossSearcher(source, requested string, searchTopK int) (vec.VectorSearcher, string, *SearchPlan, error) {
	algo := requested
	if algo == "" || algo == AlgorithmAuto {
		algo = "flat"
	}
	searcher, ran, err := s.readySearcher(source, algo)
	if err != nil {
		return nil, ran, nil, err
	}
	if requested != AlgorithmAuto {
		return searcher, ran, explicitPlan(ran, requested, searchTopK), nil
	}
	plan := &SearchPlan{Algorithm: AlgorithmAuto, Candidates: searchTopK, Collections: map[string]*SearchPlan{}}
	plan.reason("auto: each target collection is planned on its own; see collections")
	return searcher, AlgorithmAuto, plan, nil
}

type crossCandidate struct {
	collection string
	result     vec.VectorResult
	chunks     []int // summaryMode expand's chunks for the document (RAG-009)
}

// crossSearchItems searches every target collection, merges the hits by
// score, applies groupBy (SRCH-016) and loads the top results.
func (s *Server) crossSearchItems(p crossSearchParams) []CrossSearchResultItem {
	var cands []crossCandidate
	for _, coll := range p.Targets {
		allowedIDs, active := s.allowedDocIDs(coll, p.FilterMeta, p.Filter)
		if active && len(allowedIDs) == 0 {
			continue
		}
		ps := plannedSearch{Searcher: p.Searcher}
		if p.Plan != nil && p.Plan.Algorithm == AlgorithmAuto {
			// A target the planner cannot serve yet keeps the fallback.
			if planned, err := s.planSearch(planRequest{Collection: coll, Algorithm: AlgorithmAuto, Filtered: active,
				Allowed: allowedIDs, SearchTopK: p.SearchTopK, Dimensions: len(p.QueryVector),
				LatencyTargetMs: p.LatencyTargetMs}); err == nil {
				ps = planned
				p.Plan.Collections[coll] = planned.Plan
			}
		}
		results := s.runPlanned(ps, coll, p.QueryVector, p.SearchTopK, p.Threshold, allowedIDs, active, p.Metric)
		results = s.rescoreFeedback(p.Feedback, coll, results, p.Metric)
		results, summaryPlan := s.crossSummaries(p, coll, ps.Plan, results, allowedIDs, active)
		for _, vr := range vec.DeduplicateChunkResults(results) {
			cands = append(cands, crossCandidate{collection: coll, result: vr, chunks: summaryPlan.expandedChunks(vr.DocID)})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].result.Score > cands[j].result.Score })
	// Grouping removes results, so it needs the whole candidate list.
	if p.GroupBy == nil && len(cands) > p.TopK {
		cands = cands[:p.TopK]
	}

	// #336: documents of several collections, each read from its own file.
	items := make([]CrossSearchResultItem, 0, len(cands))
	for _, c := range cands {
		docPtr, err := loadDoc(s.docBytes(c.collection, c.result.DocID))
		if err != nil || docPtr == nil {
			continue
		}
		doc := *docPtr
		if !p.IncludeContent {
			doc.ContentMD = ""
		}
		items = append(items, CrossSearchResultItem{Collection: c.collection, Document: doc, Score: c.result.Score, chunks: c.chunks})
	}
	if p.GroupBy != nil {
		items = groupTop(items, p.GroupBy,
			func(it CrossSearchResultItem) string { return it.Document.ID },
			func(it CrossSearchResultItem) string {
				return p.GroupBy.label(it.Collection, it.Document.ID, it.Document.Meta)
			},
			func(it *CrossSearchResultItem, t groupTag) { it.Group, it.GroupRank = t.Group, t.Rank })
		if len(items) > p.TopK {
			items = items[:p.TopK]
		}
	}
	for i := range items {
		items[i].Rank = i + 1
	}
	return items
}

// crossSummaries applies one target's summaryMode (RAG-009) to that target's
// hits, through the same applySummaries single-collection vector search uses:
// each target's own profile decides, so a target with summaries off adds
// nothing. The plan it returns holds expand's chunks — the target's own plan
// under "auto", else one of its own — and is nil when the mode is off. Under
// the one plan of a named algorithm the explain line is copied there,
// prefixed by the target, since every target shares that plan.
func (s *Server) crossSummaries(p crossSearchParams, coll string, own *SearchPlan, results []vec.VectorResult,
	allowed map[string]bool, filtered bool) ([]vec.VectorResult, *SearchPlan) {
	mode := s.ResolveSummaryMode(coll, "")
	if mode == "" {
		return results, nil
	}
	plan := own
	if plan == nil {
		plan = &SearchPlan{}
	}
	results = s.applySummaries(summarySearch{Collection: coll, Mode: mode, Query: p.QueryVector,
		SearchTopK: p.SearchTopK, TopK: p.TopK, Threshold: p.Threshold, Allowed: allowed,
		Filtered: filtered, Metric: p.Metric}, results, plan)
	if own == nil && p.Plan != nil {
		for _, r := range plan.Reasons {
			p.Plan.reason("%s: %s", coll, r)
		}
	}
	return results, plan
}
