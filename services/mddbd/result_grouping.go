package main

import (
	"errors"
	"fmt"

	"mddb/internal/sliceutil"
	"mddb/internal/storage"
	vec "mddb/internal/vector"

	bolt "go.etcd.io/bbolt"
)

// Result grouping (SRCH-016).
//
// Parent-mode deduplication — one hit per document — is one case of a general
// operation: split the candidates by a key, keep the k best of each group,
// flatten, cut to topK. groupBy exposes the general form. An agent reading
// code wants one passage per file, a reviewer two per author, a caller
// searching several collections one per collection, and sometimes three
// chunks of one document instead of one.
//
// Order of operations, documented in SEARCH.md: candidates (topK ×
// oversample) → parent dedupe → groupBy → MMR → topK. Grouping before MMR
// means diversification picks among what grouping kept, so it cannot bring
// back a hit grouping removed.

// Group keys that are not metadata.
const (
	groupByDocument   = "document"
	groupByCollection = "collection"
	maxPerGroup       = 100
)

// GroupBy keeps at most PerGroup results for each value of Key.
type GroupBy struct {
	// Key is a metadata key, "document" (chunks of one document) or
	// "collection" (cross-collection search).
	Key string `json:"key"`
	// PerGroup is how many results each group keeps; 0 means 1.
	PerGroup int `json:"perGroup,omitempty"`
}

// Validate rejects a groupBy that cannot be applied.
func (g *GroupBy) Validate() error {
	if g == nil {
		return nil
	}
	if g.Key == "" {
		return errors.New("groupBy.key is required (a metadata key, \"document\" or \"collection\")")
	}
	if g.PerGroup < 0 || g.PerGroup > maxPerGroup {
		return fmt.Errorf("groupBy.perGroup must be between 1 and %d", maxPerGroup)
	}
	return nil
}

func (g *GroupBy) perGroup() int {
	if g.PerGroup < 1 {
		return 1
	}
	return g.PerGroup
}

// label returns the group a document falls in. A document without the
// metadata key gets "", and groupTop keeps such documents apart from each
// other: one missing value is not the same value as another.
func (g *GroupBy) label(collection, docID string, meta map[string][]string) string {
	switch g.Key {
	case groupByDocument:
		return vec.BaseDocID(docID)
	case groupByCollection:
		return collection
	}
	if vals := meta[g.Key]; len(vals) > 0 {
		return vals[0]
	}
	return ""
}

// groupTag is what a kept result reports about its group.
type groupTag struct {
	Group string
	Rank  int
}

// groupTop applies g to best-first items. id names the document (for the
// "missing value" case), label its group; set receives each kept item's tag.
func groupTop[T any](items []T, g *GroupBy, id, label func(T) string, set func(*T, groupTag)) []T {
	if g == nil || len(items) == 0 {
		return items
	}
	out, ranks := sliceutil.GroupTop(items, g.perGroup(), func(it T) string {
		if l := label(it); l != "" {
			return "=" + l
		}
		return "#" + vec.BaseDocID(id(it)) // alone in its group
	})
	for i := range out {
		set(&out[i], groupTag{Group: label(out[i]), Rank: ranks[i]})
	}
	return out
}

// groupLabels resolves the group of each candidate ID in one read. Only
// metadata keys need the documents; document and collection groups do not.
func (s *Server) groupLabels(collection string, g *GroupBy, ids []string) map[string]string {
	out := make(map[string]string, len(ids))
	if g.Key == groupByDocument || g.Key == groupByCollection {
		for _, id := range ids {
			out[id] = g.label(collection, id, nil)
		}
		return out
	}
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		for _, id := range ids {
			doc, err := loadDoc(bDocs.Get(storage.DocKey(collection, vec.BaseDocID(id))))
			if err == nil && doc != nil {
				out[id] = g.label(collection, id, doc.Meta)
			}
		}
		return nil
	})
	return out
}

// vectorShaping is what turns raw ANN candidates into the ranked list a
// vector search returns. REST and MCP vector search shared everything here
// by copy; now they share it by call.
type vectorShaping struct {
	Collection string
	ChunkMode  bool
	GroupBy    *GroupBy
	MMR        bool
	MMRLambda  float64
	TopK       int
	DiskVecs   map[string][]float32
}

// shapeVectorResults dedupes (parent mode), groups, diversifies and cuts the
// candidates, and returns the group tag of each kept result by its ID.
func (s *Server) shapeVectorResults(sh vectorShaping, results []vec.VectorResult) ([]vec.VectorResult, map[string]groupTag) {
	if !sh.ChunkMode {
		results = vec.DeduplicateChunkResults(results)
	}
	var tags map[string]groupTag
	if sh.GroupBy != nil {
		ids := make([]string, len(results))
		for i, r := range results {
			ids[i] = r.DocID
		}
		labels := s.groupLabels(sh.Collection, sh.GroupBy, ids)
		tags = make(map[string]groupTag, len(results))
		results = groupTop(results, sh.GroupBy,
			func(r vec.VectorResult) string { return r.DocID },
			func(r vec.VectorResult) string { return labels[r.DocID] },
			func(r *vec.VectorResult, t groupTag) { tags[r.DocID] = t })
	}
	if sh.MMR {
		results = vec.MMRRerank(results, mmrLambdaOrDefault(sh.MMRLambda), sh.TopK, func(id string) []float32 {
			if v, ok := sh.DiskVecs[id]; ok {
				return v
			}
			return s.VectorIndex.GetVector(sh.Collection, id)
		})
	}
	if len(results) > sh.TopK {
		results = results[:sh.TopK]
	}
	return results, tags
}

// groupedCandidates is how many fused candidates a grouped hybrid search
// keeps before grouping: grouping removes results, so topK alone would leave
// fewer than topK groups to fill the answer.
func groupedCandidates(topK int) int {
	return max(topK*10, 50)
}

// groupHybridItems applies groupBy to loaded hybrid results, renumbers their
// ranks and cuts them to topK. REST and MCP hybrid search both end here.
func groupHybridItems(collection string, items []HybridSearchResultItem, g *GroupBy, topK int) []HybridSearchResultItem {
	if g == nil {
		return items
	}
	items = groupTop(items, g,
		func(it HybridSearchResultItem) string { return it.Document.ID },
		func(it HybridSearchResultItem) string { return g.label(collection, it.Document.ID, it.Document.Meta) },
		func(it *HybridSearchResultItem, t groupTag) { it.Group, it.GroupRank = t.Group, t.Rank })
	if topK > 0 && len(items) > topK {
		items = items[:topK]
	}
	for i := range items {
		items[i].Rank = i + 1
	}
	return items
}
