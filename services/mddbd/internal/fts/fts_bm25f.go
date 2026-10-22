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

// Default BM25F field weights — used when FieldWeights is nil/empty.
var defaultBM25FWeights = map[string]float64{
	"content":          1.0,
	"meta.title":       3.0,
	"meta.tags":        2.0,
	"meta.category":    2.0,
	"meta.description": 1.5,
}

// fieldStats holds per-field collection statistics for BM25F.
type fieldStats struct {
	TotalDocs  float64
	TotalTerms float64
	AvgDL      float64
}

// SearchBM25F performs field-weighted BM25F search.
// tokens is the pre-tokenized query. fieldWeights overrides defaults if non-empty.
func (f *FTSIndex) SearchBM25F(collection string, tokens map[string]int, limit int, fieldWeights map[string]float64) ([]FTSResult, error) {
	if len(tokens) == 0 {
		return nil, nil
	}

	weights := defaultBM25FWeights
	if len(fieldWeights) > 0 {
		weights = fieldWeights
	}

	type docScore struct {
		score        float64
		matchedTerms []string
	}
	scores := make(map[string]*docScore)

	err := f.db.View(func(tx *bolt.Tx) error {
		sources := fieldSources(tx, collection, weights)
		fstats := loadFieldStats(sources)
		if len(fstats) == 0 {
			return nil
		}

		// N = max docs across fields
		var totalDocs float64
		for _, fs := range fstats {
			if fs.TotalDocs > totalDocs {
				totalDocs = fs.TotalDocs
			}
		}
		if totalDocs == 0 {
			return nil
		}

		for term := range tokens {
			// IDF: document frequency = union of docs containing term across fields
			docSet := make(map[string]bool)
			for field, w := range weights {
				if w <= 0 {
					continue
				}
				src, ok := sources[field]
				if !ok {
					continue
				}
				prefix := src.termPrefix(term)
				c := src.postings.Cursor()
				for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
					docID := string(k[len(prefix):])
					if docID != "" {
						docSet[docID] = true
					}
				}
			}
			df := float64(len(docSet))
			idf := math.Log((totalDocs-df+0.5)/(df+0.5) + 1)

			// Weighted TF across fields per doc
			docTFTilde := make(map[string]float64)
			for field, w := range weights {
				if w <= 0 {
					continue
				}
				fs, ok := fstats[field]
				if !ok {
					continue
				}
				avgdl := fs.AvgDL
				if avgdl == 0 {
					avgdl = 1
				}
				src := sources[field]
				prefix := src.termPrefix(term)
				c := src.postings.Cursor()
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
					if meta := src.lengths.Get(src.lenKey(docID)); len(meta) >= 4 {
						dl = float64(binary.LittleEndian.Uint32(meta))
					}
					norm := 1.0 - bm25B + bm25B*dl/avgdl
					docTFTilde[docID] += w * tf / norm
				}
			}

			// BM25F: IDF * tf_tilde / (k1 + tf_tilde)
			for docID, tfTilde := range docTFTilde {
				ds, ok := scores[docID]
				if !ok {
					ds = &docScore{}
					scores[docID] = ds
				}
				ds.score += idf * tfTilde / (bm25K1 + tfTilde)
				ds.matchedTerms = append(ds.matchedTerms, term)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	results := make([]FTSResult, 0, len(scores))
	for docID, ds := range scores {
		results = append(results, FTSResult{
			DocID:        docID,
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

// SearchBM25FFuzzy performs BM25F search with fuzzy term matching.
func (f *FTSIndex) SearchBM25FFuzzy(collection string, tokens map[string]int, limit, maxDist int, fieldWeights map[string]float64) ([]FTSResult, error) {
	if len(tokens) == 0 {
		return nil, nil
	}

	weights := defaultBM25FWeights
	if len(fieldWeights) > 0 {
		weights = fieldWeights
	}

	type docScore struct {
		score        float64
		matchedTerms []string
	}
	scores := make(map[string]*docScore)

	err := f.db.View(func(tx *bolt.Tx) error {
		sources := fieldSources(tx, collection, weights)
		fstats := loadFieldStats(sources)
		if len(fstats) == 0 {
			return nil
		}

		var totalDocs float64
		for _, fs := range fstats {
			if fs.TotalDocs > totalDocs {
				totalDocs = fs.TotalDocs
			}
		}
		if totalDocs == 0 {
			return nil
		}

		expanded := expandFuzzyFieldTerms(sources, tokens, maxDist)
		if len(expanded) == 0 {
			return nil
		}

		for term, origTerm := range expanded {
			isExact := term == origTerm

			docSet := make(map[string]bool)
			for field, w := range weights {
				if w <= 0 {
					continue
				}
				src, ok := sources[field]
				if !ok {
					continue
				}
				prefix := src.termPrefix(term)
				c := src.postings.Cursor()
				for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
					docID := string(k[len(prefix):])
					if docID != "" {
						docSet[docID] = true
					}
				}
			}
			df := float64(len(docSet))
			idf := math.Log((totalDocs-df+0.5)/(df+0.5) + 1)

			docTFTilde := make(map[string]float64)
			for field, w := range weights {
				if w <= 0 {
					continue
				}
				fs, ok := fstats[field]
				if !ok {
					continue
				}
				avgdl := fs.AvgDL
				if avgdl == 0 {
					avgdl = 1
				}
				src := sources[field]
				prefix := src.termPrefix(term)
				c := src.postings.Cursor()
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
					if meta := src.lengths.Get(src.lenKey(docID)); len(meta) >= 4 {
						dl = float64(binary.LittleEndian.Uint32(meta))
					}
					norm := 1.0 - bm25B + bm25B*dl/avgdl
					docTFTilde[docID] += w * tf / norm
				}
			}

			for docID, tfTilde := range docTFTilde {
				bm25fScore := idf * tfTilde / (bm25K1 + tfTilde)
				if !isExact {
					bm25fScore *= fuzzyPenalty
				}
				ds, ok := scores[docID]
				if !ok {
					ds = &docScore{}
					scores[docID] = ds
				}
				ds.score += bm25fScore
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
	for docID, ds := range scores {
		results = append(results, FTSResult{
			DocID:        docID,
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

// loadFieldStats reads each field's collection statistics.
func loadFieldStats(sources map[string]fieldSource) map[string]fieldStats {
	result := make(map[string]fieldStats, len(sources))
	for field, src := range sources {
		if src.stats == nil {
			continue
		}
		cs := decodeCollectionStats(src.stats)
		avgdl := float64(0)
		if cs.TotalDocs > 0 {
			avgdl = float64(cs.TotalTerms) / float64(cs.TotalDocs)
		}
		result[field] = fieldStats{
			TotalDocs:  float64(cs.TotalDocs),
			TotalTerms: float64(cs.TotalTerms),
			AvgDL:      avgdl,
		}
	}
	return result
}

// expandFuzzyFieldTerms scans the field-level FTS bucket for terms matching
// query terms within the given edit distance.
func expandFuzzyFieldTerms(sources map[string]fieldSource, queryTerms map[string]int, maxDist int) map[string]string {
	indexedTerms := make(map[string]struct{})
	for _, src := range sources {
		prefix := []byte(src.base)
		c := src.postings.Cursor()
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			rest := string(k[len(prefix):])
			pipe := strings.IndexByte(rest, '|')
			if pipe <= 0 {
				continue
			}
			indexedTerms[rest[:pipe]] = struct{}{}
		}
	}

	expanded := make(map[string]string)
	for qt := range queryTerms {
		if _, ok := indexedTerms[qt]; ok {
			expanded[qt] = qt
		}
		for it := range indexedTerms {
			if it == qt {
				continue
			}
			diff := len(it) - len(qt)
			if diff < 0 {
				diff = -diff
			}
			if diff > maxDist {
				continue
			}
			if levenshteinDistance(qt, it) <= maxDist {
				if _, exists := expanded[it]; !exists {
					expanded[it] = qt
				}
			}
		}
	}
	return expanded
}
