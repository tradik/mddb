package main

import (
	"errors"
	"strings"
	"testing"

	vec "mddb/internal/vector"
)

// stubSearcher is an index whose size, readiness and answers a test sets.
// Methods a test does not need are left to the nil embedded interface.
type stubSearcher struct {
	vec.VectorSearcher
	size    int
	ready   bool
	answers func(topK int) []vec.VectorResult
}

func (s *stubSearcher) CollectionSize(string) int { return s.size }
func (s *stubSearcher) IsReady() bool             { return s.ready }
func (s *stubSearcher) Search(_ string, _ []float32, topK int, _ float64, _ vec.SimilarityFunc) []vec.VectorResult {
	return s.answers(topK)
}
func (s *stubSearcher) SearchWithFilter(_ string, _ []float32, topK int, _ float64, _ map[string]bool, _ vec.SimilarityFunc) []vec.VectorResult {
	return s.answers(topK)
}

// widenStub is a Widener: it finds more the further it is asked to look.
type widenStub struct {
	stubSearcher
	widened []int
}

func (w *widenStub) SearchWithFilterWiden(_ string, _ []float32, topK int, _ float64, _ map[string]bool, _ vec.SimilarityFunc, widen int) []vec.VectorResult {
	w.widened = append(w.widened, widen)
	return hits(min(topK, widen*2))
}

func hits(n int) []vec.VectorResult {
	out := make([]vec.VectorResult, n)
	for i := range out {
		out[i] = vec.VectorResult{DocID: "d" + string(rune('a'+i%26)), Score: 1 - float32(i)/100}
	}
	return out
}

func allowedN(n int) map[string]bool {
	m := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		m[string(rune('a'+i))+"x"] = true
	}
	return m
}

func TestPlanOptions_ValidateAndExplain(t *testing.T) {
	if err := (PlanOptions{LatencyTargetMs: -1}).ValidatePlanOptions(); err == nil {
		t.Fatal("a negative latency target was accepted")
	}
	if err := (PlanOptions{LatencyTargetMs: maxLatencyTargetMs + 1}).ValidatePlanOptions(); err == nil {
		t.Fatal("a latency target over a minute was accepted")
	}
	if err := (PlanOptions{LatencyTargetMs: 20}).ValidatePlanOptions(); err != nil {
		t.Fatal(err)
	}
	plan := &SearchPlan{Algorithm: "flat"}
	if (PlanOptions{}).explained(plan) != nil || (PlanOptions{Explain: true}).explained(plan) != plan {
		t.Fatal("explained must return the plan only when asked")
	}
	for _, name := range []string{"", "auto", "flat", "hnsw", "ivf", "pq", "opq", "sq", "sq4", "bq"} {
		if !validVectorAlgorithm(name) {
			t.Errorf("%q refused", name)
		}
	}
	if validVectorAlgorithm("quantized") || validVectorAlgorithm("nope") {
		t.Error("an algorithm a profile cannot name was accepted")
	}
}

func TestRetrievalProfile_VectorAlgorithm(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()
	if err := (&RetrievalProfileDef{VectorAlgorithm: "fastest"}).Validate(); !errors.Is(err, errInvalidVectorAlgorithm) {
		t.Fatalf("unknown algorithm: %v", err)
	}
	if got := srv.ResolveVectorAlgorithm("kb", ""); got != "" {
		t.Fatalf("no profile: %q", got)
	}
	setProfile(t, srv, "kb", &RetrievalProfileDef{VectorAlgorithm: AlgorithmAuto})
	if got := srv.ResolveVectorAlgorithm("kb", ""); got != AlgorithmAuto {
		t.Fatalf("profile: %q", got)
	}
	if got := srv.ResolveVectorAlgorithm("kb", "hnsw"); got != "hnsw" {
		t.Fatalf("the request wins: %q", got)
	}
	wire := retrievalProfileToProto(&RetrievalProfileDef{VectorAlgorithm: AlgorithmAuto})
	if wire.VectorAlgorithm != AlgorithmAuto || retrievalProfileFromProto(wire).VectorAlgorithm != AlgorithmAuto {
		t.Fatalf("proto round trip lost vectorAlgorithm: %+v", wire)
	}
}

func TestPlanToProto(t *testing.T) {
	if planToProto(nil) != nil {
		t.Fatal("nil plan must stay nil")
	}
	p := &SearchPlan{Algorithm: "auto", Candidates: 30, Reasons: []string{"r"},
		Collections: map[string]*SearchPlan{"a": {Algorithm: "flat", Prefilter: true, IterativeScan: true}}}
	got := planToProto(p)
	if got.Algorithm != "auto" || got.Candidates != 30 || got.Collections["a"].Algorithm != "flat" ||
		!got.Collections["a"].Prefilter || !got.Collections["a"].IterativeScan {
		t.Fatalf("%+v", got)
	}
	if o := planOptions(true, 7); !o.Explain || o.LatencyTargetMs != 7 {
		t.Fatalf("%+v", o)
	}
}

func TestPlanSearch_ExplicitAutoAndRefusals(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	flat := &stubSearcher{size: 20_000, ready: true, answers: hits}
	hnsw := &stubSearcher{size: 20_000, ready: false, answers: hits}
	s.VectorSearchers = map[string]vec.VectorSearcher{"flat": flat, "hnsw": hnsw}

	// Named: as always.
	ps, err := s.planSearch(planRequest{Collection: "c", Algorithm: "flat", SearchTopK: 30})
	if err != nil || ps.Algorithm != "flat" || ps.Plan.Reasons[0] != "flat: requested" {
		t.Fatalf("%v %+v", err, ps.Plan)
	}
	if _, err := s.planSearch(planRequest{Collection: "c", Algorithm: "nope"}); !errors.Is(err, errUnknownAlgorithm) {
		t.Fatalf("unknown algorithm: %v", err)
	}

	// Auto: 20k vectors wants hnsw, which is built but still loading.
	ps, err = s.planSearch(planRequest{Collection: "c", Algorithm: AlgorithmAuto, SearchTopK: 30})
	if err != nil || ps.Algorithm != "flat" || ps.Plan.Algorithm != "flat" || ps.Plan.mayIterate ||
		!strings.Contains(strings.Join(ps.Plan.Reasons, " "), "still loading") {
		t.Fatalf("%v %+v", err, ps.Plan)
	}
	hnsw.ready = true
	ps, err = s.planSearch(planRequest{Collection: "c", Algorithm: AlgorithmAuto, SearchTopK: 30, Filtered: true, Allowed: allowedN(10_000)})
	if err != nil || ps.Algorithm != "hnsw" || !ps.Plan.mayIterate {
		t.Fatalf("%v %+v", err, ps.Plan)
	}

	// Nothing ready: auto refuses as a named algorithm would.
	flat.ready, hnsw.ready = false, false
	if _, err := s.planSearch(planRequest{Collection: "c", Algorithm: AlgorithmAuto}); err == nil {
		t.Fatal("auto answered with no index ready")
	}
}

// A quantized collection asked for flat is searched in its own index, and
// its plan says so.
func TestPlanSearch_QuantizedDefault(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.QuantizedVecIndex = vec.NewQuantizedVectorIndex(func(string) vec.QuantizationType { return vec.QuantInt8 })
	if err := s.QuantizedVecIndex.Add("q", "d#0", []float32{0.1, 0.2, 0.3}); err != nil {
		t.Fatal(err)
	}
	s.VectorSearchers = newVectorSearchers(s.VectorIndex, s.QuantizedVecIndex, 10)
	for _, sr := range s.VectorSearchers {
		sr.SetReady()
	}
	ps, err := s.planSearch(planRequest{Collection: "q", Algorithm: "flat", QuantizedDefault: true})
	if err != nil || ps.Algorithm != "quantized" || ps.Plan.Reasons[0] != "quantized: requested" {
		t.Fatalf("%v %+v", err, ps.Plan)
	}
	in := s.planInput(planRequest{Collection: "q"})
	if !in.Quantized || in.Vectors != 1 {
		t.Fatalf("%+v", in)
	}
}

// SRCH-013 addendum: a filtered index search that comes back short is
// continued, and the plan says so.
func TestRunPlanned_IterativeScan(t *testing.T) {
	s := &Server{}
	allowed := allowedN(10)

	// A Widener widens its own way until it has topK.
	w := &widenStub{stubSearcher: stubSearcher{answers: func(int) []vec.VectorResult { return hits(1) }}}
	plan := &SearchPlan{mayIterate: true}
	got := s.runPlanned(plannedSearch{Searcher: w, Plan: plan}, "c", nil, 8, 0, allowed, true, nil)
	if len(got) != 8 || !plan.IterativeScan || len(w.widened) != 2 || w.widened[1] != 4 {
		t.Fatalf("got %d, plan %+v, widened %v", len(got), plan, w.widened)
	}

	// Any other index is asked for more and trimmed back to topK.
	plain := &stubSearcher{answers: func(k int) []vec.VectorResult { return hits(max(k-25, 0)) }}
	plan = &SearchPlan{mayIterate: true}
	if got := s.runPlanned(plannedSearch{Searcher: plain, Plan: plan}, "c", nil, 8, 0, allowed, true, nil); len(got) != 8 || !plan.IterativeScan {
		t.Fatalf("got %d, plan %+v", len(got), plan)
	}

	// The scan stops at MaxWiden and accepts what it found.
	never := &stubSearcher{answers: func(int) []vec.VectorResult { return hits(2) }}
	plan = &SearchPlan{mayIterate: true}
	if got := s.runPlanned(plannedSearch{Searcher: never, Plan: plan}, "c", nil, 8, 0, allowed, true, nil); len(got) != 2 || len(plan.Reasons) != 3 {
		t.Fatalf("got %d, reasons %v", len(got), plan.Reasons)
	}

	// Without the planner's leave, a short answer stands — as before.
	plan = &SearchPlan{}
	if got := s.runPlanned(plannedSearch{Searcher: never, Plan: plan}, "c", nil, 8, 0, allowed, true, nil); len(got) != 2 || plan.IterativeScan {
		t.Fatalf("explicit algorithm continued the scan: %+v", plan)
	}
	// Unfiltered: one search.
	if got := s.runPlanned(plannedSearch{Searcher: never}, "c", nil, 8, 0, nil, false, nil); len(got) != 2 {
		t.Fatalf("unfiltered: %d", len(got))
	}
}
