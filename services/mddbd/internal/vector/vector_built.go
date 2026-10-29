package vector

import "sync"

// What the query planner may choose without building anything (SRCH-013).
//
// IVF, PQ, OPQ, SQ and SQ4 answer from a structure trained on a collection's
// vectors, and a search of one that was never trained trains it first and
// waits (vector_autotrain.go). The planner must never put that wait on a
// query, so it asks before choosing: an index that is not built yet is not a
// candidate, and flat answers instead.

// BuildReporter is implemented by an index that has to be trained before it
// can answer for a collection.
type BuildReporter interface {
	// Trained reports whether the collection's structure exists.
	Trained(collection string) bool
}

// Built reports whether searcher can answer for collection from what it
// already holds. A trainable index is built once trained; every other index
// (flat, HNSW, BQ, the quantized index) is built as vectors arrive, so it is
// built when it holds any.
func Built(searcher VectorSearcher, collection string) bool {
	if b, ok := searcher.(BuildReporter); ok {
		return b.Trained(collection)
	}
	return searcher.CollectionSize(collection) > 0
}

// trainedUnder reads a collection's train state under the index's read lock;
// state returns nil for a collection the index has never seen.
func trainedUnder(mu *sync.RWMutex, state func() *trainState) bool {
	mu.RLock()
	defer mu.RUnlock()
	t := state()
	return t != nil && t.trained
}

// Trained implements BuildReporter.
func (idx *IVFIndex) Trained(collection string) bool {
	return trainedUnder(&idx.mu, func() *trainState {
		if c, ok := idx.data[collection]; ok {
			return &c.trainState
		}
		return nil
	})
}

// Trained implements BuildReporter.
func (p *PQIndex) Trained(collection string) bool {
	return trainedUnder(&p.mu, func() *trainState {
		if c, ok := p.data[collection]; ok {
			return &c.trainState
		}
		return nil
	})
}

// Trained implements BuildReporter.
func (o *OPQIndex) Trained(collection string) bool {
	return trainedUnder(&o.mu, func() *trainState {
		if c, ok := o.data[collection]; ok {
			return &c.trainState
		}
		return nil
	})
}

// Trained implements BuildReporter.
func (s *SQIndex) Trained(collection string) bool {
	return trainedUnder(&s.mu, func() *trainState {
		if c, ok := s.data[collection]; ok {
			return &c.trainState
		}
		return nil
	})
}

// Trained implements BuildReporter.
func (s *SQ4Index) Trained(collection string) bool {
	return trainedUnder(&s.mu, func() *trainState {
		if c, ok := s.data[collection]; ok {
			return &c.trainState
		}
		return nil
	})
}

// Widener is implemented by an index whose filtered search can look further
// than it does by default when asked (the planner's iterative scan,
// SRCH-013). widen multiplies how much of the index is examined: for IVF, the
// clusters probed.
type Widener interface {
	SearchWithFilterWiden(collection string, query []float32, topK int, threshold float64,
		allowed map[string]bool, metric SimilarityFunc, widen int) []VectorResult
}
