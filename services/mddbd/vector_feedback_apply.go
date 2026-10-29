package main

import (
	"fmt"
	"strings"

	vec "mddb/internal/vector"

	bolt "go.etcd.io/bbolt"
)

// Turning a request's feedback examples into vectors, and applying them where
// each search path searches (SRCH-015).

// resolveFeedback turns the request's examples into vectors. Keys are looked
// up in collections in order, the first holding the document winning; a
// vector must have the query's dimensions. nil when there is no feedback.
func (s *Server) resolveFeedback(f FeedbackOptions, collections []string, dims int) (*feedback, error) {
	if !f.hasFeedback() {
		return nil, nil
	}
	fb := &feedback{mode: f.FeedbackMode, rocchio: vec.DefaultRocchio, weight: f.FeedbackWeight}
	if fb.mode == "" {
		fb.mode = FeedbackModeQuery
	}
	if fb.weight == 0 {
		fb.weight = defaultFeedbackWeight
	}
	if r := f.Rocchio; r != nil {
		fb.rocchio.Alpha = floatOr(r.Alpha, fb.rocchio.Alpha)
		fb.rocchio.Beta = floatOr(r.Beta, fb.rocchio.Beta)
		fb.rocchio.Gamma = floatOr(r.Gamma, fb.rocchio.Gamma)
	}
	var err error
	if fb.pos, err = s.exampleVectors(f.Positive, collections, dims, "positive"); err != nil {
		return nil, err
	}
	if fb.neg, err = s.exampleVectors(f.Negative, collections, dims, "negative"); err != nil {
		return nil, err
	}
	fb.applied = &FeedbackApplied{Mode: fb.mode, Positive: len(fb.pos), Negative: len(fb.neg)}
	if fb.mode == FeedbackModeQuery {
		fb.applied.Alpha, fb.applied.Beta, fb.applied.Gamma = fb.rocchio.Alpha, fb.rocchio.Beta, fb.rocchio.Gamma
	} else {
		fb.applied.Weight = fb.weight
	}
	return fb, nil
}

// exampleVectors resolves one side's examples.
func (s *Server) exampleVectors(examples []FeedbackExample, collections []string, dims int, side string) ([][]float32, error) {
	out := make([][]float32, 0, len(examples))
	for _, e := range examples {
		v := e.Vector
		if e.Key != "" {
			found, ok := s.exampleVector(collections, e.Key)
			if !ok {
				return nil, fmt.Errorf("%w: %s example %q has no embedding in %s",
					errFeedbackExample, side, e.Key, strings.Join(collections, ", "))
			}
			v = found
		}
		if dims > 0 && len(v) != dims {
			return nil, fmt.Errorf("%w: a %s example has %d dimensions, the query %d", errFeedbackExample, side, len(v), dims)
		}
		out = append(out, v)
	}
	return out, nil
}

// exampleVector is a document's vector for feedback — the mean of its chunks —
// found by ID or by key in the first collection that has it. A disk-only
// collection holds no float32 vectors in memory, so its stored first chunk
// stands for the document.
func (s *Server) exampleVector(collections []string, key string) ([]float32, bool) {
	for _, coll := range collections {
		for _, id := range s.exampleIDs(coll, key) {
			if vs := s.VectorIndex.DocumentVectors(coll, id); len(vs) > 0 {
				return vec.Centroid(vs), true
			}
			if rec, err := s.VectorStore.Get(coll, id); err == nil && rec != nil {
				return rec.Vector, true
			}
		}
	}
	return nil, false
}

// exampleIDs are the document IDs key may mean in a collection: itself, as
// search results carry IDs, then the document the by-key index holds for it.
func (s *Server) exampleIDs(collection, key string) []string {
	ids := []string{key}
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		if ref := s.lookupByKeyTx(tx, collection, key); ref != nil {
			ids = append(ids, ref.DocID)
		}
		return nil
	})
	return ids
}

// crossFeedback resolves a cross search's feedback — keys looked up in the
// source collection, then each target — records it in the plan, and returns
// the query corrected in query mode.
func (s *Server) crossFeedback(f FeedbackOptions, source string, targets []string, q []float32, plan *SearchPlan) (*feedback, []float32, error) {
	collections := targets
	if source != "" {
		collections = append([]string{source}, targets...)
	}
	fb, err := s.resolveFeedback(f, collections, len(q))
	if err != nil {
		return nil, nil, err
	}
	plan.Feedback = fb.report()
	return fb, fb.query(q), nil
}

// query returns the corrected query vector in query mode, and the query as
// given otherwise.
func (fb *feedback) query(q []float32) []float32 {
	if fb == nil || fb.mode != FeedbackModeQuery {
		return q
	}
	out := vec.Rocchio(q, fb.pos, fb.neg, fb.rocchio)
	fb.applied.QueryShift = 1 - float64(vec.CosineSimilarity(q, out))
	return out
}

// rescoreFeedback re-ranks one collection's candidates in rescore mode.
func (s *Server) rescoreFeedback(fb *feedback, collection string, results []vec.VectorResult, metric vec.SimilarityFunc) []vec.VectorResult {
	if fb == nil || fb.mode != FeedbackModeRescore || len(results) == 0 {
		return results
	}
	vectors := s.candidateVectors(collection, results)
	fb.applied.Rescored += len(vectors)
	return vec.RescoreFeedback(results, vectors, fb.pos, fb.neg, fb.weight, metric)
}

// candidateVectors are the candidates' vectors: from the in-memory flat
// index, and from the store for what it does not hold (disk-only
// collections).
func (s *Server) candidateVectors(collection string, results []vec.VectorResult) map[string][]float32 {
	out := make(map[string][]float32, len(results))
	var missing []string
	for _, r := range results {
		if v := s.VectorIndex.GetVector(collection, r.DocID); v != nil {
			out[r.DocID] = v
			continue
		}
		missing = append(missing, r.DocID)
	}
	if len(missing) > 0 {
		for id, v := range s.VectorStore.GetVectors(collection, missing) {
			out[id] = v
		}
	}
	return out
}

// report is the correction to put in the plan; nil without feedback.
func (fb *feedback) report() *FeedbackApplied {
	if fb == nil {
		return nil
	}
	return fb.applied
}
