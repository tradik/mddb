package vector

import (
	"math/bits"
	"sort"
	"sync"
	"sync/atomic"
)

// BQIndex implements VectorSearcher using Binary Quantization.
// Each float32 component is reduced to 1 bit (positive = 1, negative/zero = 0).
// Search uses Hamming distance for coarse ranking, then re-ranks with exact cosine.
type BQIndex struct {
	mu           sync.RWMutex
	data         map[string]*bqCollection
	rerankFactor int // multiplier for candidates to re-rank (default: 10)
	ready        atomic.Bool
}

type bqCollection struct {
	codes    map[string][]uint64  // docID -> packed binary codes
	origVecs map[string][]float32 // original vectors for re-ranking
	dim      int                  // vector dimensionality
	nWords   int                  // ceil(dim / 64)
}

// NewBQIndex creates a new Binary Quantization index.
func NewBQIndex(rerankFactor int) *BQIndex {
	if rerankFactor <= 0 {
		rerankFactor = 10
	}
	return &BQIndex{
		data:         make(map[string]*bqCollection),
		rerankFactor: rerankFactor,
	}
}

// Name implements the VectorSearcher interface.
func (b *BQIndex) Name() string { return "bq" }

// IsReady implements the VectorSearcher interface.
func (b *BQIndex) IsReady() bool { return b.ready.Load() }

// SetReady implements the VectorSearcher interface.
func (b *BQIndex) SetReady() { b.ready.Store(true) }

func (b *BQIndex) getOrCreate(collection string) *bqCollection {
	c, ok := b.data[collection]
	if !ok {
		c = &bqCollection{
			codes:    make(map[string][]uint64),
			origVecs: make(map[string][]float32),
		}
		b.data[collection] = c
	}
	return c
}

// Add implements the VectorSearcher interface.
func (b *BQIndex) Add(collection, docID string, vector []float32) error {
	// An empty vector is refused before it is stored anywhere (#252): it
	// cannot be compared with anything, and in the HNSW graph it poisoned
	// every add that followed it.
	if err := CheckVector(vector, 0); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	c := b.getOrCreate(collection)
	c.origVecs[docID] = vector

	if c.dim == 0 {
		c.dim = len(vector)
		c.nWords = (c.dim + 63) / 64
	}
	c.codes[docID] = encodeBQ(vector)
	return nil
}

// Remove implements the VectorSearcher interface.
func (b *BQIndex) Remove(collection, docID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	c, ok := b.data[collection]
	if !ok {
		return
	}
	delete(c.origVecs, docID)
	delete(c.codes, docID)
}

// Train implements the Trainable interface.
// BQ doesn't require training, but re-encodes all vectors for consistency.
func (b *BQIndex) Train(collection string, vectors map[string][]float32) {
	if len(vectors) == 0 {
		return
	}

	var dim int
	for _, v := range vectors {
		dim = len(v)
		break
	}
	if dim == 0 {
		return
	}

	codes := make(map[string][]uint64, len(vectors))
	origVecs := make(map[string][]float32, len(vectors))
	for id, v := range vectors {
		codes[id] = encodeBQ(v)
		origVecs[id] = v
	}

	b.mu.Lock()
	c := b.getOrCreate(collection)
	c.codes = codes
	c.origVecs = origVecs
	c.dim = dim
	c.nWords = (dim + 63) / 64
	b.mu.Unlock()
}

// encodeBQ converts a float32 vector to binary: positive -> 1, else -> 0.
// Bits are packed into uint64 words (64 bits each).
func encodeBQ(vector []float32) []uint64 {
	nWords := (len(vector) + 63) / 64
	code := make([]uint64, nWords)
	for i, v := range vector {
		if v > 0 {
			wordIdx := i / 64
			bitIdx := uint(i % 64)
			code[wordIdx] |= 1 << bitIdx
		}
	}
	return code
}

// hammingDistance computes the Hamming distance between two binary codes.
func hammingDistance(a, b []uint64) int {
	dist := 0
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	for i := 0; i < minLen; i++ {
		dist += bits.OnesCount64(a[i] ^ b[i])
	}
	// Count remaining bits in the longer slice
	for i := minLen; i < len(a); i++ {
		dist += bits.OnesCount64(a[i])
	}
	for i := minLen; i < len(b); i++ {
		dist += bits.OnesCount64(b[i])
	}
	return dist
}

// Search implements the VectorSearcher interface.
func (b *BQIndex) Search(collection string, query []float32, topK int, threshold float64, metric SimilarityFunc) []VectorResult {
	b.mu.RLock()
	defer b.mu.RUnlock()

	c, ok := b.data[collection]
	if !ok || len(c.codes) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 5
	}

	return b.hammingSearch(c, query, topK, threshold, nil, metric)
}

// SearchWithFilter implements the VectorSearcher interface.
func (b *BQIndex) SearchWithFilter(collection string, query []float32, topK int, threshold float64, allowed map[string]bool, metric SimilarityFunc) []VectorResult {
	b.mu.RLock()
	defer b.mu.RUnlock()

	c, ok := b.data[collection]
	if !ok || len(c.codes) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 5
	}

	return b.hammingSearch(c, query, topK, threshold, allowed, metric)
}

func (b *BQIndex) hammingSearch(c *bqCollection, query []float32, topK int, threshold float64, allowed map[string]bool, metric SimilarityFunc) []VectorResult {
	queryCode := encodeBQ(query)

	// Compute Hamming distances to all documents
	type candidate struct {
		docID   string
		hamDist int
	}
	candidates := make([]candidate, 0, len(c.codes))
	for docID, code := range c.codes {
		if allowed != nil && !allowed[BaseDocID(docID)] {
			continue
		}
		dist := hammingDistance(queryCode, code)
		candidates = append(candidates, candidate{docID: docID, hamDist: dist})
	}

	// Sort by Hamming distance (ascending = closest)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].hamDist < candidates[j].hamDist
	})

	// Re-rank top candidates with exact similarity
	if metric == nil {
		metric = CosineSimilarity
	}
	rerank := topK * b.rerankFactor
	if rerank > len(candidates) {
		rerank = len(candidates)
	}

	results := make([]VectorResult, 0, topK)
	for i := 0; i < rerank; i++ {
		cand := candidates[i]
		vec, ok := c.origVecs[cand.docID]
		if !ok {
			continue
		}
		score := metric(query, vec)
		if float64(score) >= threshold {
			results = append(results, VectorResult{DocID: cand.docID, Score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > topK {
		results = results[:topK]
	}
	return results
}

// CollectionSize implements the VectorSearcher interface.
func (b *BQIndex) CollectionSize(collection string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	c, ok := b.data[collection]
	if !ok {
		return 0
	}
	return len(c.origVecs)
}

// Collections implements the VectorSearcher interface.
func (b *BQIndex) Collections() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	names := make([]string, 0, len(b.data))
	for name := range b.data {
		names = append(names, name)
	}
	return names
}
