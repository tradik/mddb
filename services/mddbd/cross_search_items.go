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
}

type crossCandidate struct {
	collection string
	result     vec.VectorResult
}

// crossSearchItems searches every target collection, merges the hits by
// score, applies groupBy (SRCH-016) and loads the top results.
func (s *Server) crossSearchItems(p crossSearchParams) []CrossSearchResultItem {
	var cands []crossCandidate
	for _, coll := range p.Targets {
		var results []vec.VectorResult
		if allowedIDs, active := s.allowedDocIDs(coll, p.FilterMeta, p.Filter); active {
			if len(allowedIDs) == 0 {
				continue
			}
			results = p.Searcher.SearchWithFilter(coll, p.QueryVector, p.SearchTopK, p.Threshold, allowedIDs, p.Metric)
		} else {
			results = p.Searcher.Search(coll, p.QueryVector, p.SearchTopK, p.Threshold, p.Metric)
		}
		for _, vr := range vec.DeduplicateChunkResults(results) {
			cands = append(cands, crossCandidate{collection: coll, result: vr})
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
		items = append(items, CrossSearchResultItem{Collection: c.collection, Document: doc, Score: c.result.Score})
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
