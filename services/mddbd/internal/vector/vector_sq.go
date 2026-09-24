package vector

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
)

// SQIndex implements VectorSearcher using Scalar Quantization.
// Each float32 dimension is quantized to uint8 (0-255) using per-dimension
// min/max scaling. Search uses precomputed distance tables (ADC-style).
type SQIndex struct {
	mu    sync.RWMutex
	data  map[string]*sqCollection
	ready atomic.Bool
}

type sqCollection struct {
	mins     []float32            // per-dimension minimum values
	maxs     []float32            // per-dimension maximum values
	scales   []float32            // per-dimension scale factor: 255 / (max - min)
	codes    map[string][]uint8   // docID -> quantized code
	origVecs map[string][]float32 // original vectors for re-ranking
	trained  bool
	dim      int
}

// NewSQIndex creates a new Scalar Quantization index.
func NewSQIndex() *SQIndex {
	return &SQIndex{
		data: make(map[string]*sqCollection),
	}
}

// Name implements the VectorSearcher interface.
func (s *SQIndex) Name() string { return "sq" }

// IsReady implements the VectorSearcher interface.
func (s *SQIndex) IsReady() bool { return s.ready.Load() }

// SetReady implements the VectorSearcher interface.
func (s *SQIndex) SetReady() { s.ready.Store(true) }

func (s *SQIndex) getOrCreate(collection string) *sqCollection {
	c, ok := s.data[collection]
	if !ok {
		c = &sqCollection{
			codes:    make(map[string][]uint8),
			origVecs: make(map[string][]float32),
		}
		s.data[collection] = c
	}
	return c
}

// Add implements the VectorSearcher interface.
func (s *SQIndex) Add(collection, docID string, vector []float32) error {
	// An empty vector is refused before it is stored anywhere (#252): it
	// cannot be compared with anything, and in the HNSW graph it poisoned
	// every add that followed it.
	if err := CheckVector(vector, 0); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	c := s.getOrCreate(collection)
	c.origVecs[docID] = vector

	if c.trained && len(c.scales) > 0 {
		c.codes[docID] = s.encode(c, vector)
	}
	return nil
}

// Remove implements the VectorSearcher interface.
func (s *SQIndex) Remove(collection, docID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.data[collection]
	if !ok {
		return
	}
	delete(c.origVecs, docID)
	delete(c.codes, docID)
}

// Train implements the Trainable interface.
// Computes per-dimension min/max values and encodes all vectors to uint8.
func (s *SQIndex) Train(collection string, vectors map[string][]float32) {
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

	// Compute per-dimension min and max
	mins := make([]float32, dim)
	maxs := make([]float32, dim)
	for i := range mins {
		mins[i] = float32(math.MaxFloat32)
		maxs[i] = -float32(math.MaxFloat32)
	}

	for _, v := range vectors {
		for i := 0; i < dim && i < len(v); i++ {
			if v[i] < mins[i] {
				mins[i] = v[i]
			}
			if v[i] > maxs[i] {
				maxs[i] = v[i]
			}
		}
	}

	// Compute scale factors
	scales := make([]float32, dim)
	for i := range scales {
		r := maxs[i] - mins[i]
		if r > 0 {
			scales[i] = 255.0 / r
		} else {
			scales[i] = 0
		}
	}

	// Encode all vectors
	allIDs := make([]string, 0, len(vectors))
	allVecs := make([][]float32, 0, len(vectors))
	for id, v := range vectors {
		allIDs = append(allIDs, id)
		allVecs = append(allVecs, v)
	}

	codes := make(map[string][]uint8, len(vectors))
	for i, v := range allVecs {
		code := make([]uint8, dim)
		for j := 0; j < dim && j < len(v); j++ {
			val := (v[j] - mins[j]) * scales[j]
			if val < 0 {
				val = 0
			}
			if val > 255 {
				val = 255
			}
			code[j] = uint8(val)
		}
		codes[allIDs[i]] = code
	}

	s.mu.Lock()
	c := s.getOrCreate(collection)
	c.mins = mins
	c.maxs = maxs
	c.scales = scales
	c.codes = codes
	c.trained = true
	c.dim = dim
	for i, id := range allIDs {
		c.origVecs[id] = allVecs[i]
	}
	s.mu.Unlock()
}

func (s *SQIndex) encode(c *sqCollection, vector []float32) []uint8 {
	code := make([]uint8, c.dim)
	for i := 0; i < c.dim && i < len(vector); i++ {
		val := (vector[i] - c.mins[i]) * c.scales[i]
		if val < 0 {
			val = 0
		}
		if val > 255 {
			val = 255
		}
		code[i] = uint8(val)
	}
	return code
}

// Search finds the top-K most similar vectors using ADC with scalar quantization.
func (s *SQIndex) Search(collection string, query []float32, topK int, threshold float64, metric SimilarityFunc) []VectorResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.data[collection]
	if !ok || !c.trained || len(c.codes) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 5
	}

	return s.adcSearch(c, query, topK, threshold, nil, metric)
}

// SearchWithFilter implements the VectorSearcher interface.
func (s *SQIndex) SearchWithFilter(collection string, query []float32, topK int, threshold float64, allowed map[string]bool, metric SimilarityFunc) []VectorResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.data[collection]
	if !ok || !c.trained || len(c.codes) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 5
	}

	return s.adcSearch(c, query, topK, threshold, allowed, metric)
}

func (s *SQIndex) adcSearch(c *sqCollection, query []float32, topK int, threshold float64, allowed map[string]bool, metric SimilarityFunc) []VectorResult {
	dim := c.dim

	// Build distance table: distTable[dim][level] = squared distance from query[dim] to dequantized level
	distTable := make([][]float32, dim)
	for d := 0; d < dim; d++ {
		distTable[d] = make([]float32, 256)
		for level := 0; level < 256; level++ {
			// Dequantize: value = min + level / scale
			var dequant float32
			if c.scales[d] > 0 {
				dequant = c.mins[d] + float32(level)/c.scales[d]
			} else {
				dequant = c.mins[d]
			}
			var qd float32
			if d < len(query) {
				qd = query[d]
			}
			diff := qd - dequant
			distTable[d][level] = diff * diff
		}
	}

	// Score each document using distance tables
	type candidate struct {
		docID      string
		approxDist float32
	}
	candidates := make([]candidate, 0, len(c.codes))
	for docID, code := range c.codes {
		if allowed != nil && !allowed[BaseDocID(docID)] {
			continue
		}
		var dist float32
		for d, ci := range code {
			if d < len(distTable) {
				dist += distTable[d][ci]
			}
		}
		candidates = append(candidates, candidate{docID: docID, approxDist: dist})
	}

	// Sort by approximate distance (ascending = closest)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].approxDist < candidates[j].approxDist
	})

	// Re-rank top candidates with exact similarity
	if metric == nil {
		metric = CosineSimilarity
	}
	rerank := topK * 3
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
func (s *SQIndex) CollectionSize(collection string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.data[collection]
	if !ok {
		return 0
	}
	return len(c.origVecs)
}

// Collections implements the VectorSearcher interface.
func (s *SQIndex) Collections() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.data))
	for name := range s.data {
		names = append(names, name)
	}
	return names
}
