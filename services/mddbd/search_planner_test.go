package main

import (
	"strings"
	"testing"
)

// SRCH-013: the planner's rules, table-driven against PlanInput alone.

func allBuilt() map[string]bool {
	return map[string]bool{"flat": true, "hnsw": true, "ivf": true, "pq": true, "opq": true, "sq": true, "sq4": true, "bq": true}
}

func TestPlanVector_ChoosesBySizeFilterAndWhatIsBuilt(t *testing.T) {
	cases := []struct {
		name      string
		in        PlanInput
		want      string
		prefilter bool
		iterate   bool
		reason    string
	}{
		{"2k vectors", PlanInput{Vectors: 2_000, Dimensions: 8, Built: allBuilt(), SearchTopK: 30}, "flat", false, false, "small enough"},
		{"200k vectors", PlanInput{Vectors: 200_000, Dimensions: 8, Built: allBuilt(), SearchTopK: 30}, "hnsw", false, false, "exact scan starts costing"},
		{"2M vectors", PlanInput{Vectors: 2_000_000, Dimensions: 8, Built: allBuilt(), SearchTopK: 30}, "ivf", false, false, "ivf probes"},
		{"1% filter on 200k", PlanInput{Vectors: 200_000, Dimensions: 8, Built: allBuilt(), Filtered: true, Allowed: 2_000, SearchTopK: 30},
			"flat", true, false, "prefilter"},
		{"broad filter on 200k", PlanInput{Vectors: 200_000, Dimensions: 8, Built: allBuilt(), Filtered: true, Allowed: 100_000, SearchTopK: 30},
			"hnsw", false, true, "the scan continues"},
		{"10% filter on 200k, ivf trained: ivf rather than a short hnsw", PlanInput{Vectors: 200_000, Dimensions: 8, Built: allBuilt(), Filtered: true, Allowed: 20_000, SearchTopK: 30},
			"ivf", false, true, "a trained ivf probes clusters"},
		{"10% filter on 200k, no ivf: the prefilter", PlanInput{Vectors: 200_000, Dimensions: 8, Built: map[string]bool{"hnsw": true}, Filtered: true, Allowed: 20_000, SearchTopK: 30},
			"flat", true, false, "come up short"},
		{"2% filter on 200k, ivf trained: still the prefilter", PlanInput{Vectors: 200_000, Dimensions: 8, Built: allBuilt(), Filtered: true, Allowed: 4_000, SearchTopK: 30},
			"flat", true, false, "under 5%"},
		{"10% filter on 2M: ivf widens instead", PlanInput{Vectors: 2_000_000, Dimensions: 8, Built: allBuilt(), Filtered: true, Allowed: 200_000, SearchTopK: 30},
			"ivf", false, true, "the scan continues"},
		{"2% filter on 2M with ivf trained", PlanInput{Vectors: 2_000_000, Dimensions: 8, Built: allBuilt(), Filtered: true, Allowed: 40_000, SearchTopK: 30},
			"flat", true, false, "under 5%"},
		{"hnsw empty, ivf trained", PlanInput{Vectors: 200_000, Dimensions: 8, Built: map[string]bool{"ivf": true}, SearchTopK: 30},
			"ivf", false, false, "best of the indexes already built"},
		{"ivf not trained", PlanInput{Vectors: 2_000_000, Dimensions: 8, Built: map[string]bool{"flat": true, "hnsw": true}, SearchTopK: 30},
			"hnsw", false, false, "not built"},
		{"nothing but flat", PlanInput{Vectors: 200_000, Dimensions: 8, Built: map[string]bool{}, SearchTopK: 30},
			"flat", false, false, "never builds"},
		{"quantized collection", PlanInput{Vectors: 200_000, Quantized: true, Filtered: true, Allowed: 10, SearchTopK: 30},
			"quantized", true, false, "configured for quantized"},
		{"over a gigabyte, sq4 built", PlanInput{Vectors: 400_000, Dimensions: 1536, Built: allBuilt(), SearchTopK: 30}, "sq4", false, false, "GB of float32"},
		{"half a gigabyte, sq trained", PlanInput{Vectors: 200_000, Dimensions: 768, Built: map[string]bool{"sq": true, "hnsw": true}, SearchTopK: 30},
			"sq", false, false, "worth quantizing"},
		{"a gigabyte, only bq", PlanInput{Vectors: 400_000, Dimensions: 1536, Built: map[string]bool{"bq": true, "hnsw": true}, SearchTopK: 30},
			"bq", false, false, "best of the indexes already built"},
		{"tight latency", PlanInput{Vectors: 50_000, Dimensions: 8, Built: allBuilt(), LatencyTargetMs: 5, SearchTopK: 30}, "bq", false, false, "latency target"},
		{"tight latency, small", PlanInput{Vectors: 500, Dimensions: 8, Built: allBuilt(), LatencyTargetMs: 5, SearchTopK: 30}, "flat", false, false, "small enough"},
		{"empty collection", PlanInput{Built: allBuilt(), Filtered: true, SearchTopK: 30}, "flat", true, false, "prefilter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := planVector(c.in)
			if p.Algorithm != c.want || p.Prefilter != c.prefilter || p.mayIterate != c.iterate {
				t.Fatalf("plan = %s prefilter=%v iterate=%v, want %s %v %v; reasons %v",
					p.Algorithm, p.Prefilter, p.mayIterate, c.want, c.prefilter, c.iterate, p.Reasons)
			}
			if p.Candidates != c.in.SearchTopK {
				t.Fatalf("candidates %d, want %d", p.Candidates, c.in.SearchTopK)
			}
			if !strings.Contains(strings.Join(p.Reasons, " | "), c.reason) {
				t.Fatalf("no reason mentions %q: %v", c.reason, p.Reasons)
			}
		})
	}
}

func TestExplicitPlan_SaysWhoChose(t *testing.T) {
	for _, c := range []struct{ ran, requested, want string }{
		{"flat", "", "the default"},
		{"hnsw", "hnsw", "requested"},
		{"flat", "hnsw", "cannot answer"},
	} {
		p := explicitPlan(c.ran, c.requested, 15)
		if p.Algorithm != c.ran || p.Candidates != 15 || !strings.Contains(p.Reasons[0], c.want) {
			t.Fatalf("%+v: %+v", c, p)
		}
	}
}

// The advisor reads the same table: a size the planner sends to hnsw, the
// advisor recommends hnsw.
func TestVectorTier_IsTheAdvisorsTable(t *testing.T) {
	for n, want := range map[int]string{9_999: "flat", 10_000: "hnsw", 1_000_000: "hnsw", 1_000_001: "ivf"} {
		if got, _ := vectorTier(n, 0); got != want {
			t.Errorf("vectorTier(%d) = %s, want %s", n, got, want)
		}
		rec := buildRecommendation(&CollectionProfile{Documents: n, EmbeddedDocuments: n}, true)
		if rec.VectorAlgorithm != want {
			t.Errorf("advisor at %d = %s, want %s", n, rec.VectorAlgorithm, want)
		}
	}
}

// More than one built index: the advisor writes auto into the profile.
func TestAdvisor_RecommendsAutoWhenSeveralIndexesAreBuilt(t *testing.T) {
	p := &CollectionProfile{Documents: 50, EmbeddedDocuments: 50, BuiltIndexes: []string{"flat", "hnsw"}}
	rec := buildRecommendation(p, true)
	if rec.RetrievalProfile.VectorAlgorithm != AlgorithmAuto || !strings.Contains(strings.Join(rec.Reasons, " "), "vectorAlgorithm auto") {
		t.Fatalf("auto not recommended: %+v %v", rec.RetrievalProfile, rec.Reasons)
	}
	p.BuiltIndexes = []string{"flat"}
	if rec := buildRecommendation(p, true); rec.RetrievalProfile.VectorAlgorithm != "" {
		t.Fatalf("one index is no choice: %+v", rec.RetrievalProfile)
	}
}
