package fts

import (
	"bytes"
	"encoding/binary"
	"math"
	"mddb/internal/sliceutil"
	"sort"
	"strings"
	"sync"

	bolt "go.etcd.io/bbolt"
)

// PMISparse — Sparse Retrieval with PMI Query Expansion
// Invented by Tradik Limited.
//
// Two-phase search: (1) standard BM25 scoring for direct query terms,
// (2) automatic query expansion via Positive Pointwise Mutual Information (PPMI)
// co-occurrence statistics computed from the corpus.

// PMISparse BM25 parameters (slightly tuned for expansion workload)
const (
	pmiK1         = 1.5  // BM25 k1: term frequency saturation
	pmiB          = 0.75 // BM25 b: document length normalization
	pmiAlpha      = 0.35 // expansion weight multiplier
	pmiExpansionK = 5    // expansion terms used per query term
	pmiWindowSize = 5    // co-occurrence sliding window size
	pmiMinCount   = 2    // minimum term frequency to include in PMI
	pmiTopK       = 10   // max cached PPMI expansions per term
)

// pmiExpansion holds a single term expansion with its PPMI weight.
type pmiExpansion struct {
	Term   string
	Weight float64
}

// pmiCollectionData holds the trained PMI data for a single collection.
type pmiCollectionData struct {
	expansions map[string][]pmiExpansion // term -> sorted PPMI expansions
	trained    bool
}

// PMIData holds per-collection PMI co-occurrence data for query expansion.
type PMIData struct {
	mu          sync.RWMutex
	collections map[string]*pmiCollectionData
}

// NewPMIData creates a new PMIData store.
func NewPMIData() *PMIData {
	return &PMIData{
		collections: make(map[string]*pmiCollectionData),
	}
}

// SetPMIData sets the PMI data on the FTS index.
func (f *FTSIndex) SetPMIData(p *PMIData) { f.pmiData = p }

// InvalidatePMI marks a collection's PMI data as stale, requiring retrain.
func (f *FTSIndex) InvalidatePMI(collection string) {
	if f.pmiData == nil {
		return
	}
	f.pmiData.mu.Lock()
	defer f.pmiData.mu.Unlock()
	if cd, ok := f.pmiData.collections[collection]; ok {
		cd.trained = false
	}
}

// ensurePMITrained lazily trains the PMI matrix for a collection if needed.
func (f *FTSIndex) ensurePMITrained(collection string) error {
	if f.pmiData == nil {
		return nil
	}

	f.pmiData.mu.RLock()
	cd, ok := f.pmiData.collections[collection]
	if ok && cd.trained {
		f.pmiData.mu.RUnlock()
		return nil
	}
	f.pmiData.mu.RUnlock()

	return f.TrainPMI(collection)
}

// TrainPMI builds the PMI co-occurrence matrix from the FTS reverse index.
func (f *FTSIndex) TrainPMI(collection string) error {
	// Phase 1: Read all term lists from the reverse index
	type docTerms struct {
		terms []string
	}
	var docs []docTerms

	err := f.view(collection, func(tx *bolt.Tx) error {
		bRev := tx.Bucket(bucketFTSRev)
		if bRev == nil {
			return nil
		}

		prefix := ftsRevKey(collection, "")
		c := bRev.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			// Skip stat keys and meta keys
			ks := string(k)
			if strings.HasPrefix(ks, "ftsstat|") || strings.HasPrefix(ks, "ftsmeta|") {
				continue
			}
			if len(v) == 0 {
				continue
			}
			terms := strings.Split(string(v), ",")
			if len(terms) > 0 {
				docs = append(docs, docTerms{terms: terms})
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Phase 2: Build co-occurrence matrix
	termFreq := make(map[string]int)
	coOccur := make(map[string]map[string]int)
	totalTokens := 0
	totalCoOcc := 0

	for _, doc := range docs {
		totalTokens += len(doc.terms)
		for _, t := range doc.terms {
			termFreq[t]++
		}
		for i, term := range doc.terms {
			start := i - pmiWindowSize
			if start < 0 {
				start = 0
			}
			end := i + pmiWindowSize + 1
			if end > len(doc.terms) {
				end = len(doc.terms)
			}
			for j := start; j < end; j++ {
				if i != j {
					if coOccur[term] == nil {
						coOccur[term] = make(map[string]int)
					}
					coOccur[term][doc.terms[j]]++
					totalCoOcc++
				}
			}
		}
	}

	// Remove low-frequency terms
	for t, c := range termFreq {
		if c < pmiMinCount {
			delete(termFreq, t)
		}
	}

	// Phase 3: Compute PPMI and build expansion cache
	expansions := make(map[string][]pmiExpansion)
	N := float64(totalCoOcc)
	if N > 0 && totalTokens > 0 {
		for term, coMap := range coOccur {
			if _, ok := termFreq[term]; !ok {
				continue
			}
			pT := float64(termFreq[term]) / float64(totalTokens)
			if pT == 0 {
				continue
			}

			var exps []pmiExpansion
			for neighbor, count := range coMap {
				if _, ok := termFreq[neighbor]; !ok {
					continue
				}
				if neighbor == term {
					continue
				}
				pN := float64(termFreq[neighbor]) / float64(totalTokens)
				pJoint := float64(count) / N
				if pJoint > 0 && pN > 0 {
					ppmi := math.Max(0, math.Log(pJoint/(pT*pN)))
					if ppmi > 0 {
						exps = append(exps, pmiExpansion{neighbor, ppmi})
					}
				}
			}

			sort.Slice(exps, func(i, j int) bool {
				return exps[i].Weight > exps[j].Weight
			})
			if len(exps) > pmiTopK {
				exps = exps[:pmiTopK]
			}
			if len(exps) > 0 {
				expansions[term] = exps
			}
		}
	}

	// Phase 4: Store in PMIData
	f.pmiData.mu.Lock()
	f.pmiData.collections[collection] = &pmiCollectionData{
		expansions: expansions,
		trained:    true,
	}
	f.pmiData.mu.Unlock()

	return nil
}

// pmiExpand returns the top-K PPMI expansion terms for a given term.
func (f *FTSIndex) pmiExpand(collection, term string, k int) []pmiExpansion {
	if f.pmiData == nil {
		return nil
	}
	f.pmiData.mu.RLock()
	defer f.pmiData.mu.RUnlock()

	cd, ok := f.pmiData.collections[collection]
	if !ok || !cd.trained {
		return nil
	}
	exps := cd.expansions[term]
	if k < len(exps) {
		exps = exps[:k]
	}
	return exps
}

// SearchPMISparse performs full-text search using BM25 + PMI query expansion.
func (f *FTSIndex) SearchPMISparse(collection, query string, limit int) ([]FTSResult, error) {
	if err := f.ensurePMITrained(collection); err != nil {
		return nil, err
	}

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

		// Load collection stats
		stats := collectionStats{}
		if raw := bRev.Get(ftsStatKey(collection)); raw != nil {
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

		// Helper: score a term with BM25
		scoreTerm := func(term string, weight float64, isExpansion bool) {
			prefix := ftsKey(collection, term, "")

			// Count document frequency
			var df int
			c := bFTS.Cursor()
			for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
				if docID := string(k[len(prefix):]); docID != "" {
					df++
				}
			}

			idf := math.Log((totalDocs-float64(df)+0.5)/(float64(df)+0.5) + 1)

			// Score each document
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

				tfNorm := (tf * (pmiK1 + 1)) / (tf + pmiK1*(1-pmiB+pmiB*dl/avgdl))
				bm25Score := weight * idf * tfNorm

				ds, ok := scores[docID]
				if !ok {
					ds = &docScore{id: docID}
					scores[docID] = ds
				}
				ds.score += bm25Score

				if isExpansion {
					ds.matchedTerms = append(ds.matchedTerms, "~"+term)
				} else {
					ds.matchedTerms = append(ds.matchedTerms, term)
				}
			}
		}

		// Phase 1: Direct BM25 scoring
		for term := range queryTerms {
			scoreTerm(term, 1.0, false)
		}

		// Phase 2: PMI expansion scoring
		if pmiAlpha > 0 {
			for term := range queryTerms {
				for _, exp := range f.pmiExpand(collection, term, pmiExpansionK) {
					// Skip if expansion term is already a direct query term
					if _, isQuery := queryTerms[exp.Term]; isQuery {
						continue
					}
					normW := math.Min(exp.Weight/5.0, 1.0)
					scoreTerm(exp.Term, pmiAlpha*normW, true)
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

// SearchPMISparseFuzzy performs PMISparse search with fuzzy term matching.
func (f *FTSIndex) SearchPMISparseFuzzy(collection, query string, limit, maxDist int) ([]FTSResult, error) {
	if err := f.ensurePMITrained(collection); err != nil {
		return nil, err
	}

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

		// Load collection stats
		stats := collectionStats{}
		if raw := bRev.Get(ftsStatKey(collection)); raw != nil {
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

		// Expand query terms with fuzzy matching
		expanded := f.expandFuzzyTerms(tx, collection, queryTerms, maxDist)
		if len(expanded) == 0 {
			return nil
		}

		// Helper: score a term with BM25
		scoreTerm := func(term string, weight float64, matchLabel string) {
			prefix := ftsKey(collection, term, "")

			var df int
			c := bFTS.Cursor()
			for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
				if docID := string(k[len(prefix):]); docID != "" {
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

				tfNorm := (tf * (pmiK1 + 1)) / (tf + pmiK1*(1-pmiB+pmiB*dl/avgdl))
				bm25Score := weight * idf * tfNorm

				ds, ok := scores[docID]
				if !ok {
					ds = &docScore{id: docID}
					scores[docID] = ds
				}
				ds.score += bm25Score
				ds.matchedTerms = append(ds.matchedTerms, matchLabel)
			}
		}

		// Phase 1: Direct BM25 scoring with fuzzy matches
		for term, origTerm := range expanded {
			isExact := term == origTerm
			weight := 1.0
			label := origTerm
			if !isExact {
				weight = fuzzyPenalty
				label = origTerm + "~" + term
			}
			scoreTerm(term, weight, label)
		}

		// Phase 2: PMI expansion scoring (expand from original query terms, not fuzzy)
		if pmiAlpha > 0 {
			for term := range queryTerms {
				for _, exp := range f.pmiExpand(collection, term, pmiExpansionK) {
					if _, isQuery := queryTerms[exp.Term]; isQuery {
						continue
					}
					// Skip if already in fuzzy-expanded set
					if _, inExpanded := expanded[exp.Term]; inExpanded {
						continue
					}
					normW := math.Min(exp.Weight/5.0, 1.0)
					scoreTerm(exp.Term, pmiAlpha*normW, "~"+exp.Term)
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
