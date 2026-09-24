package vector

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// QuantizedVectorIndex is an in-memory flat index that stores vectors in quantized form (int8 or int4).
// Search is performed directly on quantized data for both storage and compute savings.
// Falls back to float32 VectorIndex for collections without quantization configured.
type QuantizedVectorIndex struct {
	mu          sync.RWMutex
	collections map[string]*quantizedCollection
	ready       atomic.Bool
	// getQuantType resolves the quantization type for a collection.
	getQuantType func(collection string) QuantizationType
}

type quantizedCollection struct {
	quantType QuantizationType
	vectors   map[string]*QuantizedVector // key -> quantized vector
}

// NewQuantizedVectorIndex creates a new quantized vector index.
// getQuantType is called to resolve the quantization type for each collection.
func NewQuantizedVectorIndex(getQuantType func(string) QuantizationType) *QuantizedVectorIndex {
	return &QuantizedVectorIndex{
		collections:  make(map[string]*quantizedCollection),
		getQuantType: getQuantType,
	}
}

func (qi *QuantizedVectorIndex) IsReady() bool { return qi.ready.Load() }
func (qi *QuantizedVectorIndex) SetReady()     { qi.ready.Store(true) }
func (qi *QuantizedVectorIndex) Name() string  { return "quantized" }

// Add quantizes and stores a float32 vector.
func (qi *QuantizedVectorIndex) Add(collection, docID string, vector []float32) error {
	// An empty vector is refused before it is stored anywhere (#252): it
	// cannot be compared with anything, and in the HNSW graph it poisoned
	// every add that followed it.
	if err := CheckVector(vector, 0); err != nil {
		return err
	}
	qt := qi.resolveQuantType(collection)
	if qt == QuantNone {
		return nil // not quantized, skip
	}

	qv := QuantizeFloat32(vector, qt)
	if qv == nil {
		return nil
	}

	qi.mu.Lock()
	defer qi.mu.Unlock()

	coll := qi.collections[collection]
	if coll == nil {
		coll = &quantizedCollection{
			quantType: qt,
			vectors:   make(map[string]*QuantizedVector),
		}
		qi.collections[collection] = coll
	}
	coll.vectors[docID] = qv
	return nil
}

// Remove deletes a vector from the quantized index.
func (qi *QuantizedVectorIndex) Remove(collection, docID string) {
	qi.mu.Lock()
	defer qi.mu.Unlock()
	if coll, ok := qi.collections[collection]; ok {
		delete(coll.vectors, docID)
	}
}

// Search performs brute-force search on quantized vectors.
// The query vector (float32) is quantized using per-collection calibration (global min/max of stored vectors).
func (qi *QuantizedVectorIndex) Search(collection string, query []float32, topK int, threshold float64, _ SimilarityFunc) []VectorResult {
	qi.mu.RLock()
	defer qi.mu.RUnlock()

	coll, ok := qi.collections[collection]
	if !ok || len(coll.vectors) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 5
	}

	// Compute global min/max for query quantization calibration
	qQuery := qi.quantizeQuery(query, coll)
	if qQuery == nil {
		return nil
	}

	simFunc := qi.selectSimFunc(coll.quantType)

	results := make([]VectorResult, 0, len(coll.vectors))
	for docID, qv := range coll.vectors {
		score := simFunc(qQuery, qv)
		if float64(score) >= threshold {
			results = append(results, VectorResult{DocID: docID, Score: score})
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

// SearchWithFilter searches only among allowed doc IDs.
func (qi *QuantizedVectorIndex) SearchWithFilter(collection string, query []float32, topK int, threshold float64, allowed map[string]bool, _ SimilarityFunc) []VectorResult {
	qi.mu.RLock()
	defer qi.mu.RUnlock()

	coll, ok := qi.collections[collection]
	if !ok || len(coll.vectors) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 5
	}

	qQuery := qi.quantizeQuery(query, coll)
	if qQuery == nil {
		return nil
	}

	simFunc := qi.selectSimFunc(coll.quantType)

	results := make([]VectorResult, 0, min(len(allowed), len(coll.vectors)))
	for docID, qv := range coll.vectors {
		baseID := baseDocIDQ(docID)
		if !allowed[baseID] {
			continue
		}
		score := simFunc(qQuery, qv)
		if float64(score) >= threshold {
			results = append(results, VectorResult{DocID: docID, Score: score})
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

// CollectionSize returns the number of vectors in a collection.
func (qi *QuantizedVectorIndex) CollectionSize(collection string) int {
	qi.mu.RLock()
	defer qi.mu.RUnlock()
	if coll, ok := qi.collections[collection]; ok {
		return len(coll.vectors)
	}
	return 0
}

// Collections returns all collection names that have vectors.
func (qi *QuantizedVectorIndex) Collections() []string {
	qi.mu.RLock()
	defer qi.mu.RUnlock()
	names := make([]string, 0, len(qi.collections))
	for name := range qi.collections {
		names = append(names, name)
	}
	return names
}

// HasCollection returns true if the quantized index has vectors for the given collection.
func (qi *QuantizedVectorIndex) HasCollection(collection string) bool {
	qi.mu.RLock()
	defer qi.mu.RUnlock()
	coll, ok := qi.collections[collection]
	return ok && len(coll.vectors) > 0
}

// quantizeQuery quantizes the float32 query using the collection's global min/max.
func (qi *QuantizedVectorIndex) quantizeQuery(query []float32, coll *quantizedCollection) *QuantizedVector {
	// Find global min/max across all stored vectors for calibration
	var globalMin, globalMax float32
	first := true
	for _, qv := range coll.vectors {
		if first {
			globalMin = qv.Min
			globalMax = qv.Max
			first = false
		}
		if qv.Min < globalMin {
			globalMin = qv.Min
		}
		if qv.Max > globalMax {
			globalMax = qv.Max
		}
	}

	switch coll.quantType {
	case QuantInt8:
		return QuantizeQueryForInt8(query, globalMin, globalMax)
	case QuantInt4:
		return QuantizeQueryForInt4(query, globalMin, globalMax)
	default:
		return nil
	}
}

func (qi *QuantizedVectorIndex) selectSimFunc(qt QuantizationType) func(*QuantizedVector, *QuantizedVector) float32 {
	switch qt {
	case QuantInt8:
		return CosineSimInt8
	case QuantInt4:
		return CosineSimInt4
	default:
		return CosineSimInt8
	}
}

func (qi *QuantizedVectorIndex) resolveQuantType(collection string) QuantizationType {
	if qi.getQuantType != nil {
		return qi.getQuantType(collection)
	}
	return QuantNone
}

// baseDocIDQ is equivalent to BaseDocID but local to avoid import cycles.
func baseDocIDQ(key string) string {
	if idx := strings.IndexByte(key, '#'); idx >= 0 {
		return key[:idx]
	}
	return key
}
