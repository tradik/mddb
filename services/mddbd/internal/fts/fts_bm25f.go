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
func (f *FTSIndex) SearchBM25F(collection string, tokens map[string]int, limit int, fieldWeights map[string]float64, p BM25Params) ([]FTSResult, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	scores := make(map[string]*bm25fDocScore)
	err := f.view(collection, func(tx *bolt.Tx) error {
		scorer, ok := newBM25FScorer(tx, collection, bm25fWeights(fieldWeights), p)
		if !ok {
			return nil
		}
		for term := range tokens {
			for docID, score := range scorer.termScores(term) {
				addBM25FScore(scores, docID, score, term)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rankBM25F(scores, limit), nil
}

// SearchBM25FFuzzy performs BM25F search with fuzzy term matching.
func (f *FTSIndex) SearchBM25FFuzzy(collection string, tokens map[string]int, limit, maxDist int, fieldWeights map[string]float64, p BM25Params) ([]FTSResult, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	scores := make(map[string]*bm25fDocScore)
	err := f.view(collection, func(tx *bolt.Tx) error {
		scorer, ok := newBM25FScorer(tx, collection, bm25fWeights(fieldWeights), p)
		if !ok {
			return nil
		}
		for term, origTerm := range expandFuzzyFieldTerms(scorer.sources, tokens, maxDist) {
			label, penalty := origTerm, 1.0
			if term != origTerm {
				label, penalty = origTerm+"~"+term, fuzzyPenalty
			}
			for docID, score := range scorer.termScores(term) {
				addBM25FScore(scores, docID, score*penalty, label)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rankBM25F(scores, limit), nil
}

func bm25fWeights(fieldWeights map[string]float64) map[string]float64 {
	if len(fieldWeights) > 0 {
		return fieldWeights
	}
	return defaultBM25FWeights
}

// bm25fScorer scores one query term at a time against a collection's fields.
type bm25fScorer struct {
	sources   map[string]fieldSource
	fstats    map[string]fieldStats
	weights   map[string]float64
	totalDocs float64 // N: the most documents any field holds
	params    BM25Params
}

// newBM25FScorer is false when the collection has no field statistics.
func newBM25FScorer(tx *bolt.Tx, collection string, weights map[string]float64, p BM25Params) (*bm25fScorer, bool) {
	sources := fieldSources(tx, collection, weights)
	s := &bm25fScorer{sources: sources, fstats: loadFieldStats(sources), weights: weights, params: p}
	for _, fs := range s.fstats {
		s.totalDocs = max(s.totalDocs, fs.TotalDocs)
	}
	return s, s.totalDocs > 0
}

// termScores returns each matching document's BM25F score for term:
// IDF over the union of fields, times tf~ / (k1 + tf~), where tf~ sums each
// field's weighted, length-normalised term frequency.
func (s *bm25fScorer) termScores(term string) map[string]float64 {
	docSet := make(map[string]bool)
	for _, src := range s.sources {
		prefix := src.termPrefix(term)
		c := src.postings.Cursor()
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			if docID := string(k[len(prefix):]); docID != "" {
				docSet[docID] = true
			}
		}
	}
	df := float64(len(docSet))
	idf := math.Log((s.totalDocs-df+0.5)/(df+0.5) + 1)

	tfTilde := make(map[string]float64)
	for field, src := range s.sources {
		fs, ok := s.fstats[field]
		if !ok {
			continue
		}
		s.addFieldTF(tfTilde, src, fs, s.weights[field], term)
	}

	out := make(map[string]float64, len(tfTilde))
	for docID, t := range tfTilde {
		out[docID] = idf * t / (s.params.K1 + t)
	}
	return out
}

// addFieldTF adds one field's weighted, length-normalised term frequencies.
func (s *bm25fScorer) addFieldTF(tfTilde map[string]float64, src fieldSource, fs fieldStats, w float64, term string) {
	avgdl := fs.AvgDL
	if avgdl == 0 {
		avgdl = 1
	}
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
		tfTilde[docID] += w * tf / s.params.norm(dl, avgdl)
	}
}

type bm25fDocScore struct {
	score        float64
	matchedTerms []string
}

func addBM25FScore(scores map[string]*bm25fDocScore, docID string, score float64, term string) {
	ds, ok := scores[docID]
	if !ok {
		ds = &bm25fDocScore{}
		scores[docID] = ds
	}
	ds.score += score
	ds.matchedTerms = append(ds.matchedTerms, term)
}

func rankBM25F(scores map[string]*bm25fDocScore, limit int) []FTSResult {
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
	return results
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

// expandFuzzyFieldTerms maps every indexed term within maxDist of a query
// term to that query term; exact matches map to themselves.
func expandFuzzyFieldTerms(sources map[string]fieldSource, queryTerms map[string]int, maxDist int) map[string]string {
	indexed := indexedFieldTerms(sources)
	expanded := make(map[string]string)
	for qt := range queryTerms {
		if _, ok := indexed[qt]; ok {
			expanded[qt] = qt
		}
		for it := range indexed {
			if it == qt || !withinEditDistance(qt, it, maxDist) {
				continue
			}
			if _, exists := expanded[it]; !exists {
				expanded[it] = qt
			}
		}
	}
	return expanded
}

// indexedFieldTerms lists the distinct terms the given fields hold.
func indexedFieldTerms(sources map[string]fieldSource) map[string]struct{} {
	terms := make(map[string]struct{})
	for _, src := range sources {
		prefix := []byte(src.base)
		c := src.postings.Cursor()
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			rest := string(k[len(prefix):])
			if pipe := strings.IndexByte(rest, '|'); pipe > 0 {
				terms[rest[:pipe]] = struct{}{}
			}
		}
	}
	return terms
}

// withinEditDistance checks the cheap length bound before the Levenshtein
// distance.
func withinEditDistance(a, b string, maxDist int) bool {
	diff := len(a) - len(b)
	if diff < 0 {
		diff = -diff
	}
	return diff <= maxDist && levenshteinDistance(a, b) <= maxDist
}
