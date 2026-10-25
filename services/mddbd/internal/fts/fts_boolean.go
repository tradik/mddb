package fts

import (
	"bytes"
	"encoding/binary"
	"math"
	"mddb/internal/sliceutil"
	"sort"

	bolt "go.etcd.io/bbolt"
)

// boolDocScore accumulates scores during boolean search.
type boolDocScore struct {
	id           string
	score        float64
	matchedTerms []string
}

// SearchBoolean performs a boolean search with AND/OR/NOT operators.
// Each clause is scored independently using BM25, then results are combined.
func (f *FTSIndex) SearchBoolean(collection string, parsed *ParsedQuery, limit int) ([]FTSResult, error) {
	if len(parsed.Clauses) == 0 {
		return nil, nil
	}

	// Evaluate each clause and collect results
	var positiveResults []map[string]*boolDocScore
	var negativeDocIDs map[string]bool

	for _, clause := range parsed.Clauses {
		clauseResults := make(map[string]*boolDocScore)

		switch clause.Type {
		case "term":
			termResults, err := f.searchSingleTerm(collection, clause.Value)
			if err != nil {
				return nil, err
			}
			for _, r := range termResults {
				clauseResults[r.DocID] = &boolDocScore{
					id:           r.DocID,
					score:        r.Score,
					matchedTerms: r.MatchedTerms,
				}
			}

		case "phrase":
			phraseResults, err := f.SearchPhrase(collection, clause.Value, 0)
			if err != nil {
				return nil, err
			}
			for _, r := range phraseResults {
				clauseResults[r.DocID] = &boolDocScore{
					id:           r.DocID,
					score:        r.Score,
					matchedTerms: r.MatchedTerms,
				}
			}

		case "proximity":
			proxResults, err := f.SearchProximity(collection, clause.Value, clause.Distance, 0)
			if err != nil {
				return nil, err
			}
			for _, r := range proxResults {
				clauseResults[r.DocID] = &boolDocScore{
					id:           r.DocID,
					score:        r.Score,
					matchedTerms: r.MatchedTerms,
				}
			}

		case "wildcard":
			wcResults, err := f.SearchWildcard(collection, clause.Value, 0)
			if err != nil {
				return nil, err
			}
			for _, r := range wcResults {
				clauseResults[r.DocID] = &boolDocScore{
					id:           r.DocID,
					score:        r.Score,
					matchedTerms: r.MatchedTerms,
				}
			}
		}

		if clause.IsNegated {
			if negativeDocIDs == nil {
				negativeDocIDs = make(map[string]bool)
			}
			for docID := range clauseResults {
				negativeDocIDs[docID] = true
			}
		} else {
			positiveResults = append(positiveResults, clauseResults)
		}
	}

	// Combine positive results based on operators
	combined := combineResults(parsed, positiveResults)

	// Remove negated documents
	for docID := range negativeDocIDs {
		delete(combined, docID)
	}

	results := make([]FTSResult, 0, len(combined))
	for _, ds := range combined {
		results = append(results, FTSResult{
			DocID:        ds.id,
			Score:        ds.score,
			MatchedTerms: sliceutil.Unique(ds.matchedTerms),
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// combineResults merges clause results using AND/OR logic from the parsed query.
func combineResults(parsed *ParsedQuery, positive []map[string]*boolDocScore) map[string]*boolDocScore {
	if len(positive) == 0 {
		return nil
	}

	combined := make(map[string]*boolDocScore)

	// Start with first clause results
	for docID, ds := range positive[0] {
		combined[docID] = &boolDocScore{
			id:           ds.id,
			score:        ds.score,
			matchedTerms: append([]string{}, ds.matchedTerms...),
		}
	}

	// Apply subsequent clauses
	clauseIdx := 1
	for i := 1; i < len(parsed.Clauses); i++ {
		clause := parsed.Clauses[i]
		if clause.IsNegated {
			continue // negated clauses handled separately
		}
		if clauseIdx >= len(positive) {
			break
		}

		op := clause.Operator
		if op == "" {
			op = parsed.DefaultOp
		}

		clauseResult := positive[clauseIdx]
		clauseIdx++

		switch op {
		case "OR":
			// Union: add new results, boost existing
			for docID, ds := range clauseResult {
				if existing, ok := combined[docID]; ok {
					existing.score += ds.score
					existing.matchedTerms = append(existing.matchedTerms, ds.matchedTerms...)
				} else {
					combined[docID] = &boolDocScore{
						id:           ds.id,
						score:        ds.score,
						matchedTerms: append([]string{}, ds.matchedTerms...),
					}
				}
			}
		default: // AND
			// Intersection: keep only docs in both sets
			newCombined := make(map[string]*boolDocScore)
			for docID, existing := range combined {
				if ds, ok := clauseResult[docID]; ok {
					newCombined[docID] = &boolDocScore{
						id:           existing.id,
						score:        existing.score + ds.score,
						matchedTerms: append(existing.matchedTerms, ds.matchedTerms...),
					}
				}
			}
			combined = newCombined
		}
	}

	return combined
}

// searchSingleTerm performs BM25 search for a single term (used by boolean search).
func (f *FTSIndex) searchSingleTerm(collection, term string) ([]FTSResult, error) {
	// Tokenize the single term (applies stemming)
	queryTerms := f.Tokenize(term)
	if len(queryTerms) == 0 {
		return nil, nil
	}

	type ds struct {
		id           string
		score        float64
		matchedTerms []string
	}
	scores := make(map[string]*ds)

	err := f.view(collection, func(tx *bolt.Tx) error {
		bFTS := tx.Bucket(bucketFTS)
		if bFTS == nil {
			return nil
		}
		bRev := tx.Bucket(bucketFTSRev)
		if bRev == nil {
			return nil
		}

		statKey := ftsStatKey(collection)
		stats := collectionStats{}
		if raw := bRev.Get(statKey); raw != nil {
			stats = decodeCollectionStats(raw)
		}
		totalDocs := float64(stats.TotalDocs)
		avgdl := float64(1)
		if stats.TotalDocs > 0 {
			avgdl = float64(stats.TotalTerms) / totalDocs
		}

		for t := range queryTerms {
			var df int
			prefix := ftsKey(collection, t, "")
			c := bFTS.Cursor()
			for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
				docID := string(k[len(prefix):])
				if docID != "" {
					df++
				}
			}

			idf := math.Log((totalDocs-float64(df)+0.5)/(float64(df)+0.5) + 1)

			c = bFTS.Cursor()
			for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
				docID := string(k[len(prefix):])
				if docID == "" {
					continue
				}

				tf := float64(1)
				if len(v) >= 4 {
					tf = float64(binary.LittleEndian.Uint32(v))
				}

				dl := avgdl
				if meta := bRev.Get(ftsMetaKey(collection, docID)); len(meta) >= 4 {
					dl = float64(binary.LittleEndian.Uint32(meta))
				}

				bm25Score := idf * (tf * (bm25K1 + 1)) / (tf + bm25K1*(1-bm25B+bm25B*dl/avgdl))

				d, ok := scores[docID]
				if !ok {
					d = &ds{id: docID}
					scores[docID] = d
				}
				d.score += bm25Score
				d.matchedTerms = append(d.matchedTerms, t)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	results := make([]FTSResult, 0, len(scores))
	for _, d := range scores {
		results = append(results, FTSResult{
			DocID:        d.id,
			Score:        d.score,
			MatchedTerms: sliceutil.Unique(d.matchedTerms),
		})
	}
	return results, nil
}

// sortByScore sorts FTSResult slice by descending score.
func sortByScore(results []FTSResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
}
