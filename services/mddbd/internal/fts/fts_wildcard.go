package fts

import (
	"bytes"
	"encoding/binary"
	"math"
	"mddb/internal/sliceutil"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// SearchWildcard performs wildcard search supporting * (any chars) and ? (single char).
// The pattern is matched against indexed terms, then BM25 scores are computed for matching docs.
func (f *FTSIndex) SearchWildcard(collection, pattern string, limit int) ([]FTSResult, error) {
	pattern = strings.ToLower(pattern)
	if f.stemmer != nil && !strings.ContainsAny(pattern, "*?") {
		pattern = f.stemmer.Stem(pattern)
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

		// Load collection stats
		stats := collectionStats{}
		if raw := bRev.Get(ftsStatKey(collection)); raw != nil {
			stats = decodeCollectionStats(raw)
		}
		totalDocs := float64(stats.TotalDocs)
		avgdl := float64(1)
		if stats.TotalDocs > 0 {
			avgdl = float64(stats.TotalTerms) / totalDocs
		}

		// Find matching terms by scanning the index
		collPrefix := []byte("fts|" + collection + "|")
		matchedTerms := make(map[string]struct{})

		c := bFTS.Cursor()
		for k, _ := c.Seek(collPrefix); k != nil && bytes.HasPrefix(k, collPrefix); k, _ = c.Next() {
			rest := string(k[len(collPrefix):])
			pipe := strings.IndexByte(rest, '|')
			if pipe <= 0 {
				continue
			}
			term := rest[:pipe]
			if _, already := matchedTerms[term]; already {
				continue
			}
			if wildcardMatch(pattern, term) {
				matchedTerms[term] = struct{}{}
			}
		}

		if len(matchedTerms) == 0 {
			return nil
		}

		// Score documents using BM25 for each matched term
		for term := range matchedTerms {
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

				d, ok := scores[docID]
				if !ok {
					d = &ds{id: docID}
					scores[docID] = d
				}
				d.score += bm25Score
				d.matchedTerms = append(d.matchedTerms, term)
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

	sortByScore(results)
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// wildcardMatch matches a string against a pattern with * and ? wildcards.
// * matches zero or more characters, ? matches exactly one character.
func wildcardMatch(pattern, text string) bool {
	pi, ti := 0, 0
	starPI, starTI := -1, -1

	for ti < len(text) {
		if pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == text[ti]) {
			pi++
			ti++
		} else if pi < len(pattern) && pattern[pi] == '*' {
			starPI = pi
			starTI = ti
			pi++
		} else if starPI >= 0 {
			pi = starPI + 1
			starTI++
			ti = starTI
		} else {
			return false
		}
	}

	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}
