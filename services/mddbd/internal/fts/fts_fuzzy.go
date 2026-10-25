package fts

import (
	"bytes"
	"encoding/binary"
	"math"
	"mddb/internal/sliceutil"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// levenshteinDistance computes the edit distance between two strings.
func levenshteinDistance(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	if la > lb {
		a, b = b, a
		la, lb = lb, la
	}

	prev := make([]int, la+1)
	curr := make([]int, la+1)
	for i := range prev {
		prev[i] = i
	}
	for j := 1; j <= lb; j++ {
		curr[0] = j
		for i := 1; i <= la; i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[i] = min3(
				prev[i]+1,
				curr[i-1]+1,
				prev[i-1]+cost,
			)
		}
		prev, curr = curr, prev
	}
	return prev[la]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// expandFuzzyTerms scans the FTS bucket for indexed terms matching query terms
// within the given edit distance. Returns a map of expandedTerm -> originalQueryTerm.
func (f *FTSIndex) expandFuzzyTerms(tx *bolt.Tx, collection string, queryTerms map[string]int, maxDist int) map[string]string {
	bFTS := tx.Bucket(bucketFTS)
	if bFTS == nil {
		return nil
	}

	// Collect unique indexed terms for this collection
	collPrefix := []byte("fts|" + collection + "|")
	indexedTerms := make(map[string]struct{})

	c := bFTS.Cursor()
	for k, _ := c.Seek(collPrefix); k != nil && bytes.HasPrefix(k, collPrefix); k, _ = c.Next() {
		rest := string(k[len(collPrefix):])
		pipe := strings.IndexByte(rest, '|')
		if pipe <= 0 {
			continue
		}
		term := rest[:pipe]
		indexedTerms[term] = struct{}{}
	}

	// For each query term, find indexed terms within edit distance
	expanded := make(map[string]string)
	for qt := range queryTerms {
		// Always include exact match
		if _, ok := indexedTerms[qt]; ok {
			expanded[qt] = qt
		}
		for it := range indexedTerms {
			if it == qt {
				continue
			}
			// Length filter: skip if length difference exceeds maxDist
			diff := len(it) - len(qt)
			if diff < 0 {
				diff = -diff
			}
			if diff > maxDist {
				continue
			}
			if levenshteinDistance(qt, it) <= maxDist {
				// Map to original query term for attribution
				if _, exists := expanded[it]; !exists {
					expanded[it] = qt
				}
			}
		}
	}
	return expanded
}

const fuzzyPenalty = 0.8 // Score multiplier for fuzzy (non-exact) matches

// SearchFuzzy performs TF-IDF search with fuzzy term matching.
func (f *FTSIndex) SearchFuzzy(collection, query string, limit, maxDist int) ([]FTSResult, error) {
	queryTerms := f.TokenizeQuery(collection, query)
	if len(queryTerms) == 0 {
		return nil, nil
	}

	type docScore struct {
		id           string
		totalTF      float64
		matchedTerms []string
	}
	scores := make(map[string]*docScore)

	err := f.view(collection, func(tx *bolt.Tx) error {
		bFTS := tx.Bucket(bucketFTS)
		if bFTS == nil {
			return nil
		}

		expanded := f.expandFuzzyTerms(tx, collection, queryTerms, maxDist)
		if len(expanded) == 0 {
			return nil
		}

		for term, origTerm := range expanded {
			isExact := term == origTerm
			prefix := ftsKey(collection, term, "")
			c := bFTS.Cursor()
			for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
				docID := string(k[len(prefix):])
				if docID == "" {
					continue
				}

				tf := float64(1)
				if len(v) >= 4 {
					tf = float64(binary.LittleEndian.Uint32(v))
				}
				logTF := math.Log1p(tf)
				if !isExact {
					logTF *= fuzzyPenalty
				}

				ds, ok := scores[docID]
				if !ok {
					ds = &docScore{id: docID}
					scores[docID] = ds
				}
				ds.totalTF += logTF
				// Attribute to the original query term with fuzzy indicator
				if isExact {
					ds.matchedTerms = append(ds.matchedTerms, origTerm)
				} else {
					ds.matchedTerms = append(ds.matchedTerms, origTerm+"~"+term)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	queryTermCount := float64(len(queryTerms))
	results := make([]FTSResult, 0, len(scores))
	for _, ds := range scores {
		matchRatio := float64(len(sliceutil.Unique(ds.matchedTerms))) / queryTermCount
		avgTF := ds.totalTF / float64(len(ds.matchedTerms))
		score := matchRatio * (0.5 + 0.5*math.Min(avgTF/5.0, 1.0))

		results = append(results, FTSResult{
			DocID:        ds.id,
			Score:        score,
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

// SearchBM25Fuzzy performs BM25 search with fuzzy term matching.
func (f *FTSIndex) SearchBM25Fuzzy(collection, query string, limit, maxDist int) ([]FTSResult, error) {
	queryTerms := f.TokenizeQuery(collection, query)
	if len(queryTerms) == 0 {
		return nil, nil
	}

	type docScore struct {
		id           string
		score        float64
		matchedTerms []string
	}
	scores := make(map[string]*docScore)

	err := f.view(collection, func(tx *bolt.Tx) error {
		bFTS := tx.Bucket(bucketFTS)
		if bFTS == nil {
			return nil
		}
		bRev := tx.Bucket(bucketFTSRev)
		if bRev == nil {
			return nil
		}

		// Load collection stats for avgdl
		statKey := ftsStatKey(collection)
		stats := collectionStats{}
		if raw := bRev.Get(statKey); raw != nil {
			stats = decodeCollectionStats(raw)
		}
		totalDocs := float64(stats.TotalDocs)
		avgdl := float64(0)
		if stats.TotalDocs > 0 {
			avgdl = float64(stats.TotalTerms) / totalDocs
		}
		if avgdl == 0 {
			avgdl = 1
		}

		expanded := f.expandFuzzyTerms(tx, collection, queryTerms, maxDist)
		if len(expanded) == 0 {
			return nil
		}

		for term, origTerm := range expanded {
			isExact := term == origTerm

			// Count document frequency
			var df int
			prefix := ftsKey(collection, term, "")
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
				if !isExact {
					bm25Score *= fuzzyPenalty
				}

				ds, ok := scores[docID]
				if !ok {
					ds = &docScore{id: docID}
					scores[docID] = ds
				}
				ds.score += bm25Score
				if isExact {
					ds.matchedTerms = append(ds.matchedTerms, origTerm)
				} else {
					ds.matchedTerms = append(ds.matchedTerms, origTerm+"~"+term)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	results := make([]FTSResult, 0, len(scores))
	for _, ds := range scores {
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
