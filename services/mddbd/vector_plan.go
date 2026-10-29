package main

import (
	"errors"
	"fmt"

	vec "mddb/internal/vector"
)

// The planner on the search paths (SRCH-013): every vector-backed search —
// vector, the vector half of hybrid, cross — resolves its searcher through
// planSearch and runs it through runPlanned, so `auto`, `explain` and the
// iterative scan reach every surface from one place.

// PlanOptions are the planner fields of a vector-backed search request.
type PlanOptions struct {
	// Explain adds the plan — the index chosen and why — to the response.
	Explain bool `json:"explain,omitempty"`
	// LatencyTargetMs is an optional goal for algorithm "auto": a tight one
	// favours a quantized index on a large collection. 0 = none.
	LatencyTargetMs int `json:"latencyTargetMs,omitempty"`
}

// maxLatencyTargetMs bounds latencyTargetMs; a minute is not a target.
const maxLatencyTargetMs = 60_000

// ValidatePlanOptions refuses a latency target that cannot mean anything.
func (o PlanOptions) ValidatePlanOptions() error {
	if o.LatencyTargetMs < 0 || o.LatencyTargetMs > maxLatencyTargetMs {
		return fmt.Errorf("latencyTargetMs %d is outside 0–%d", o.LatencyTargetMs, maxLatencyTargetMs)
	}
	return nil
}

// explained returns plan when the request asked for it, nil otherwise, so a
// response without explain carries no plan field at all.
func (o PlanOptions) explained(plan *SearchPlan) *SearchPlan {
	if !o.Explain {
		return nil
	}
	return plan
}

// planRequest is what a search path knows when it picks its searcher.
type planRequest struct {
	Collection string
	// Algorithm as requested, after the collection profile: "" (flat), a
	// name, or "auto".
	Algorithm string
	// QuantizedDefault: the path searches a quantized collection's own index
	// when the request leaves the algorithm at flat, as vector search always
	// has.
	QuantizedDefault bool
	Filtered         bool
	Allowed          map[string]bool
	SearchTopK       int
	Dimensions       int
	LatencyTargetMs  int
}

// plannedSearch is the searcher a path runs, and the plan behind it.
type plannedSearch struct {
	Searcher  vec.VectorSearcher
	Algorithm string
	Plan      *SearchPlan
}

// ResolveVectorAlgorithm applies request > collection profile > default to
// the vector algorithm. "" is the default, flat.
func (s *Server) ResolveVectorAlgorithm(collection, requested string) string {
	if requested != "" {
		return requested
	}
	if p := s.RetrievalProfile(collection); p != nil {
		return p.VectorAlgorithm
	}
	return ""
}

// planSearch picks the searcher for one collection: the named algorithm as
// every path always has, or the planner's choice for "auto". Errors are
// readySearcher's: an unknown algorithm, or an index still loading.
func (s *Server) planSearch(r planRequest) (plannedSearch, error) {
	if r.Algorithm != AlgorithmAuto {
		algo := r.Algorithm
		if algo == "" {
			algo = "flat"
		}
		if r.QuantizedDefault && algo == "flat" && s.QuantizedVecIndex != nil && s.QuantizedVecIndex.HasCollection(r.Collection) {
			algo = "quantized"
		}
		searcher, ran, err := s.readySearcher(r.Collection, algo)
		if err != nil {
			return plannedSearch{}, err
		}
		requested := r.Algorithm
		if requested == "flat" && ran == "quantized" {
			requested = ran
		}
		return plannedSearch{Searcher: searcher, Algorithm: ran, Plan: explicitPlan(ran, requested, r.SearchTopK)}, nil
	}

	plan := planVector(s.planInput(r))
	searcher, ran, err := s.readySearcher(r.Collection, plan.Algorithm)
	if err != nil {
		return plannedSearch{}, err
	}
	if ran != plan.Algorithm {
		plan.reason("%s is still loading for this collection; %s answered instead", plan.Algorithm, ran)
		plan.Algorithm, plan.mayIterate = ran, false
	}
	return plannedSearch{Searcher: searcher, Algorithm: ran, Plan: plan}, nil
}

// planInput measures what the planner decides from. Every number is one the
// server already holds in memory: nothing here reads the database.
func (s *Server) planInput(r planRequest) PlanInput {
	in := PlanInput{
		Filtered: r.Filtered, Allowed: len(r.Allowed), SearchTopK: r.SearchTopK,
		Dimensions: r.Dimensions, LatencyTargetMs: r.LatencyTargetMs, Built: map[string]bool{},
	}
	if flat, ok := s.VectorSearchers["flat"]; ok {
		in.Vectors = flat.CollectionSize(r.Collection)
	}
	if s.QuantizedVecIndex != nil && s.QuantizedVecIndex.HasCollection(r.Collection) {
		in.Quantized = true
		in.Vectors = max(in.Vectors, s.QuantizedVecIndex.CollectionSize(r.Collection))
	}
	if in.Dimensions == 0 && s.Embedding != nil {
		in.Dimensions = s.Embedding.Dimensions()
	}
	for name, searcher := range s.VectorSearchers {
		if name != "quantized" && vec.Built(searcher, r.Collection) {
			in.Built[name] = true
		}
	}
	return in
}

// runPlanned searches one collection the way its plan says. A filtered index
// search that comes back short of what the filter admits is continued —
// further into the index, up to MaxWiden times its default — instead of
// quietly returning three results for topK 10 (SRCH-013, iterative scan).
func (s *Server) runPlanned(ps plannedSearch, collection string, query []float32, topK int, threshold float64,
	allowed map[string]bool, filtered bool, metric vec.SimilarityFunc) []vec.VectorResult {
	if !filtered {
		return ps.Searcher.Search(collection, query, topK, threshold, metric)
	}
	results := ps.Searcher.SearchWithFilter(collection, query, topK, threshold, allowed, metric)
	if ps.Plan == nil || !ps.Plan.mayIterate {
		return results
	}
	want := min(topK, len(allowed))
	for widen := 2; len(results) < want && widen <= searchThresholds.MaxWiden; widen *= 2 {
		got := len(results)
		results = widenedSearch(ps.Searcher, collection, query, topK, threshold, allowed, metric, widen)
		ps.Plan.IterativeScan = true
		ps.Plan.reason("the filtered index returned %d of %d; the scan continued at %d× its default", got, want, widen)
	}
	return results
}

// widenedSearch asks an index to look widen times further than it does by
// default: a Widener widens its own way (IVF probes more clusters), any other
// index is asked for more candidates and trimmed back to topK.
func widenedSearch(searcher vec.VectorSearcher, collection string, query []float32, topK int, threshold float64,
	allowed map[string]bool, metric vec.SimilarityFunc, widen int) []vec.VectorResult {
	if w, ok := searcher.(vec.Widener); ok {
		return w.SearchWithFilterWiden(collection, query, topK, threshold, allowed, metric, widen)
	}
	results := searcher.SearchWithFilter(collection, query, topK*widen, threshold, allowed, metric)
	if len(results) > topK {
		results = results[:topK]
	}
	return results
}

// validVectorAlgorithm reports whether name is one a profile may store: an
// algorithm a server offers, or auto.
func validVectorAlgorithm(name string) bool {
	switch name {
	case "", AlgorithmAuto, "flat", "hnsw", "ivf", "pq", "opq", "sq", "sq4", "bq":
		return true
	}
	return false
}

// errInvalidVectorAlgorithm is a profile's refusal of an unknown algorithm.
var errInvalidVectorAlgorithm = errors.New("must be auto, flat, hnsw, ivf, pq, opq, sq, sq4 or bq")
