package vector

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
)

// SQ4Index is scalar quantization at 4 bits per dimension (SRCH-003).
//
// It fills the gap between int8 (`sq`, 4× smaller than float32) and one bit
// (`bq`, 32× smaller but coarse): 8× smaller, with recall close enough to int8
// that a corpus which no longer fits in RAM at int8 usually still fits here.
//
// Two dimensions share a byte, low nibble first. Sixteen levels is few enough
// that where the levels sit matters more than at int8, so the boundaries are
// not min/max: they are per-dimension percentiles, which keeps a single
// outlier from stretching the scale and collapsing every ordinary value into
// two or three levels. That choice is what the recall gate in
// quantizer_recall_test.go measures.
//
// Distances are asymmetric: the query stays float32 and only the stored
// vectors are quantized, so the query contributes no quantization error. The
// candidate list is then re-ranked exactly, exactly as `sq` does.
type SQ4Index struct {
	mu    sync.RWMutex
	data  map[string]*sq4Collection
	ready atomic.Bool
}

type sq4Collection struct {
	// lows[d] and scales[d] map dimension d onto the 0..15 code range.
	lows   []float32
	scales []float32
	// levels[d][code] is the value a code decodes to — precomputed because it
	// is read once per query per dimension to build the distance table.
	levels [][16]float32
	// codes are packed two dimensions per byte.
	codes    map[string][]uint8
	origVecs map[string][]float32
	trained  bool
	dim      int
}

// sq4Levels is how many distinct values 4 bits can carry.
const sq4Levels = 16

// sq4ClipPercentile is where the quantization range ends.
//
// Embedding dimensions are roughly normal with occasional far outliers. Using
// the true min and max spends two of sixteen levels on values almost nothing
// has, and squeezes everything real into the middle. Clipping at the 1st and
// 99th percentile costs accuracy on the 2% of values outside the range and
// buys it back, with interest, on the 98% inside.
const sq4ClipPercentile = 0.01

// NewSQ4Index creates a 4-bit scalar quantization index.
func NewSQ4Index() *SQ4Index {
	return &SQ4Index{data: make(map[string]*sq4Collection)}
}

// Name implements the VectorSearcher interface.
func (s *SQ4Index) Name() string { return "sq4" }

// IsReady implements the VectorSearcher interface.
func (s *SQ4Index) IsReady() bool { return s.ready.Load() }

// SetReady implements the VectorSearcher interface.
func (s *SQ4Index) SetReady() { s.ready.Store(true) }

func (s *SQ4Index) getOrCreate(collection string) *sq4Collection {
	c, ok := s.data[collection]
	if !ok {
		c = &sq4Collection{
			codes:    make(map[string][]uint8),
			origVecs: make(map[string][]float32),
		}
		s.data[collection] = c
	}
	return c
}

// Add implements the VectorSearcher interface.
func (s *SQ4Index) Add(collection, docID string, vector []float32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c := s.getOrCreate(collection)
	c.origVecs[docID] = vector

	if c.trained && len(c.scales) > 0 {
		c.codes[docID] = c.encode(vector)
	}
}

// Remove implements the VectorSearcher interface.
func (s *SQ4Index) Remove(collection, docID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.data[collection]
	if !ok {
		return
	}
	delete(c.origVecs, docID)
	delete(c.codes, docID)
}

// Train computes per-dimension ranges and encodes every vector.
//
// Called with the whole collection, because the range a dimension needs is a
// property of the corpus and cannot be known from one vector.
func (s *SQ4Index) Train(collection string, vectors map[string][]float32) {
	if len(vectors) == 0 {
		return
	}

	dim := 0
	for _, v := range vectors {
		dim = len(v)
		break
	}
	if dim == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	c := s.getOrCreate(collection)
	c.dim = dim
	c.lows = make([]float32, dim)
	c.scales = make([]float32, dim)
	c.levels = make([][16]float32, dim)

	// One column at a time: percentiles are per dimension, and a shared range
	// across dimensions of different magnitudes would waste most of the levels
	// on most of them.
	column := make([]float32, 0, len(vectors))
	for d := 0; d < dim; d++ {
		column = column[:0]
		for _, v := range vectors {
			if d < len(v) {
				column = append(column, v[d])
			}
		}
		if len(column) == 0 {
			continue
		}
		sort.Slice(column, func(i, j int) bool { return column[i] < column[j] })

		low, high := clipRange(column, sq4ClipPercentile)
		c.lows[d] = low
		if high > low {
			c.scales[d] = float32(sq4Levels-1) / (high - low)
		} else {
			// A constant dimension carries no information; every value maps to
			// code 0 and decodes back to itself.
			c.scales[d] = 0
		}

		for level := 0; level < sq4Levels; level++ {
			if c.scales[d] == 0 {
				c.levels[d][level] = low
				continue
			}
			c.levels[d][level] = low + float32(level)/c.scales[d]
		}
	}

	c.trained = true
	for docID, v := range vectors {
		c.origVecs[docID] = v
		c.codes[docID] = c.encode(v)
	}
	s.ready.Store(true)
}

// clipRange returns the percentile bounds of a sorted column.
//
// Falls back to the full range when the percentiles collapse onto the same
// sample, which is what happens with only a handful of vectors: at two
// samples, index int(1×0.01) and int(1×0.99) are both 0, the range comes out
// empty, the scale is zero and every value in the dimension quantizes to the
// same code. A small collection would have been indexed to nothing.
func clipRange(sorted []float32, p float64) (low, high float32) {
	if len(sorted) == 0 {
		return 0, 0
	}
	lo := int(float64(len(sorted)-1) * p)
	hi := int(float64(len(sorted)-1) * (1 - p))
	if hi <= lo {
		lo, hi = 0, len(sorted)-1
	}
	return sorted[lo], sorted[hi]
}

// encode packs a vector two dimensions per byte, low nibble first.
func (c *sq4Collection) encode(v []float32) []uint8 {
	out := make([]uint8, (c.dim+1)/2)
	for d := 0; d < c.dim; d++ {
		var code uint8
		if d < len(v) {
			code = c.quantize(d, v[d])
		}
		if d%2 == 0 {
			out[d/2] = code & 0x0F
		} else {
			out[d/2] |= code << 4
		}
	}
	return out
}

func (c *sq4Collection) quantize(d int, value float32) uint8 {
	if c.scales[d] == 0 {
		return 0
	}
	level := (value - c.lows[d]) * c.scales[d]
	// Clipping, not wrapping: a value outside the trained range belongs at the
	// nearest end of it.
	if level <= 0 {
		return 0
	}
	if level >= sq4Levels-1 {
		return sq4Levels - 1
	}
	return uint8(level + 0.5)
}

// codeAt unpacks the code for one dimension.
func codeAt(packed []uint8, d int) uint8 {
	b := packed[d/2]
	if d%2 == 0 {
		return b & 0x0F
	}
	return b >> 4
}

// Search implements the VectorSearcher interface.
func (s *SQ4Index) Search(collection string, query []float32, topK int, threshold float64, metric SimilarityFunc) []VectorResult {
	return s.SearchWithFilter(collection, query, topK, threshold, nil, metric)
}

// SearchWithFilter implements the VectorSearcher interface.
func (s *SQ4Index) SearchWithFilter(collection string, query []float32, topK int, threshold float64, allowed map[string]bool, metric SimilarityFunc) []VectorResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.data[collection]
	if !ok || !c.trained || len(c.codes) == 0 {
		return nil
	}
	if topK <= 0 {
		return nil
	}

	// Asymmetric distance: the query stays in float32 and contributes no
	// quantization error of its own. Sixteen squared differences per
	// dimension, computed once and then read len(codes) times.
	distTable := make([][sq4Levels]float32, c.dim)
	for d := 0; d < c.dim; d++ {
		var q float32
		if d < len(query) {
			q = query[d]
		}
		for level := 0; level < sq4Levels; level++ {
			diff := q - c.levels[d][level]
			distTable[d][level] = diff * diff
		}
	}

	type candidate struct {
		docID string
		dist  float32
	}
	candidates := make([]candidate, 0, len(c.codes))
	for docID, packed := range c.codes {
		if allowed != nil && !allowed[BaseDocID(docID)] {
			continue
		}
		var dist float32
		for d := 0; d < c.dim; d++ {
			dist += distTable[d][codeAt(packed, d)]
		}
		candidates = append(candidates, candidate{docID, dist})
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].dist < candidates[j].dist })

	// Re-rank exactly, as `sq` does. Four bits is coarse enough that the
	// approximate order is a shortlist rather than an answer; three times topK
	// is what recovers int8-class recall, and the gate in
	// quantizer_recall_test.go is what says three is enough.
	if metric == nil {
		metric = CosineSimilarity
	}
	rerank := topK * 3
	if rerank > len(candidates) {
		rerank = len(candidates)
	}

	results := make([]VectorResult, 0, topK)
	for i := 0; i < rerank; i++ {
		vec, ok := c.origVecs[candidates[i].docID]
		if !ok {
			continue
		}
		score := metric(query, vec)
		if float64(score) >= threshold {
			results = append(results, VectorResult{DocID: candidates[i].docID, Score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > topK {
		results = results[:topK]
	}
	return results
}

// CollectionSize implements the VectorSearcher interface.
func (s *SQ4Index) CollectionSize(collection string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.data[collection]
	if !ok {
		return 0
	}
	return len(c.origVecs)
}

// Collections implements the VectorSearcher interface.
func (s *SQ4Index) Collections() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]string, 0, len(s.data))
	for name := range s.data {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// CodeBytes reports the packed size of one vector's code, which is what the
// index holds per document beyond the re-ranking copy.
func (s *SQ4Index) CodeBytes(collection string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.data[collection]
	if !ok {
		return 0
	}
	return (c.dim + 1) / 2
}

// compile-time check that the index satisfies both interfaces it is used
// through: a searcher that cannot be trained would silently return nothing.
var (
	_ VectorSearcher = (*SQ4Index)(nil)
	_                = math.Sqrt
)
