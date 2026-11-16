package main

import (
	"strings"
	"testing"

	vec "mddb/internal/vector"
)

// filterStub is a FilterSearcher: it says which strategy answered.
type filterStub struct {
	stubSearcher
	outcome vec.FilterOutcome
	asked   vec.FilterQuery
}

func (f *filterStub) FilterSearch(q vec.FilterQuery) vec.FilterOutcome {
	f.asked = q
	return f.outcome
}

// SRCH-024: an index that reports its filter strategy is asked once — no
// iterative scan — and explain carries what actually ran, including a
// fallback to the exact scan.
func TestRunPlanned_FilterSearcherReportsItsStrategy(t *testing.T) {
	s := &Server{}
	filtered := indexQuery{Collection: "c", TopK: 3, Threshold: 0.2, Allowed: allowedN(5), Filtered: true}

	graph := &filterStub{outcome: vec.FilterOutcome{Results: hits(3), Strategy: vec.FilterStrategyGraph}}
	plan := &SearchPlan{Candidates: 3, mayIterate: true, Prefilter: true}
	if got := s.runPlanned(plannedSearch{Searcher: graph, Plan: plan}, filtered); len(got) != 3 {
		t.Fatalf("got %d", len(got))
	}
	if plan.Strategy != vec.FilterStrategyGraph || plan.Prefilter || plan.IterativeScan || len(plan.Reasons) != 0 {
		t.Fatalf("plan %+v", plan)
	}
	if graph.asked.Collection != "c" || graph.asked.TopK != 3 || graph.asked.Threshold != 0.2 || len(graph.asked.Allowed) != 5 {
		t.Fatalf("asked %+v", graph.asked)
	}

	exact := &filterStub{outcome: vec.FilterOutcome{Results: hits(1), Strategy: vec.FilterStrategyPrefilter}}
	plan = &SearchPlan{}
	s.runPlanned(plannedSearch{Searcher: exact, Plan: plan}, filtered)
	if plan.Strategy != vec.FilterStrategyPrefilter || !plan.Prefilter {
		t.Fatalf("plan %+v", plan)
	}

	fell := &filterStub{outcome: vec.FilterOutcome{Results: hits(2), Strategy: vec.FilterStrategyGraph, Fallback: true}}
	plan = &SearchPlan{Candidates: 3}
	s.runPlanned(plannedSearch{Searcher: fell, Plan: plan}, filtered)
	if !strings.Contains(strings.Join(plan.Reasons, " "), "scored exactly") {
		t.Fatalf("no fallback reason: %v", plan.Reasons)
	}
	// Without a plan the outcome is still returned.
	if got := s.runPlanned(plannedSearch{Searcher: fell}, filtered); len(got) != 2 {
		t.Fatalf("got %d", len(got))
	}

	// An index that does not report its strategy gets the planner's name.
	plain := &stubSearcher{answers: hits}
	plan = &SearchPlan{}
	s.runPlanned(plannedSearch{Searcher: plain, Algorithm: "ivf", Plan: plan}, filtered)
	if plan.Strategy != "ivf" {
		t.Fatalf("plan %+v", plan)
	}
}

// The strategy names explain uses, one per kind of index.
func TestFilterStrategyNames(t *testing.T) {
	for _, c := range []struct {
		algorithm string
		prefilter bool
		want      string
	}{
		{"flat", false, vec.FilterStrategyPrefilter},
		{"quantized", true, vec.FilterStrategyPrefilter},
		{"hnsw", false, vec.FilterStrategyGraph},
		{"ivf", false, "ivf"},
		{"bq", false, "scan+filter"},
		{"quantized", false, "scan+filter"},
	} {
		if got := filterStrategy(c.algorithm, c.prefilter); got != c.want {
			t.Errorf("filterStrategy(%s, %v) = %s, want %s", c.algorithm, c.prefilter, got, c.want)
		}
	}
}

// An empty collection admits nothing of nothing: a share of 0, not NaN.
func TestPlanInput_SelectivityOfAnEmptyCollection(t *testing.T) {
	if s := (PlanInput{Allowed: 5}).selectivity(); s != 0 {
		t.Fatalf("selectivity = %v", s)
	}
}

// The planner and HNSW decide by one rule: the crossover the planner reports
// is where HNSW itself changes strategy.
func TestPlanVector_FilterStrategyFollowsTheCrossover(t *testing.T) {
	for _, vectors := range []int{20_000, 200_000, 2_000_000} {
		edge := int(vec.HNSWFilterMinShare(vectors)*float64(vectors)) + 1
		below := planVector(PlanInput{Vectors: vectors, Dimensions: 8, Built: map[string]bool{"hnsw": true}, Filtered: true, Allowed: edge - 2, SearchTopK: 10})
		above := planVector(PlanInput{Vectors: vectors, Dimensions: 8, Built: map[string]bool{"hnsw": true}, Filtered: true, Allowed: edge, SearchTopK: 10})
		if below.Strategy != vec.FilterStrategyPrefilter || !below.Prefilter {
			t.Errorf("%d vectors, %d admitted: %+v", vectors, edge-2, below)
		}
		if above.Strategy == vec.FilterStrategyPrefilter || above.Prefilter {
			t.Errorf("%d vectors, %d admitted: %+v", vectors, edge, above)
		}
	}
}
