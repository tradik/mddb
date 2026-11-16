package vector

import (
	"fmt"
	"math"
	"testing"
)

// SRCH-024: the filter-aware walk and how HNSW chooses between it and the
// exact scan of what a filter admits.

// filterIndex is an HNSWIndex of n documents of topicCorpus, d00000...
func filterIndex(t *testing.T, n int) (*HNSWIndex, [][]float32, [][]float32) {
	t.Helper()
	docs, queries := topicCorpus(21, 16, 10, n/10)
	h := NewHNSWIndex(8, 40, 16)
	for i, d := range docs {
		if err := h.Add("c", fmt.Sprintf("d%05d", i), d); err != nil {
			t.Fatal(err)
		}
	}
	return h, docs, queries
}

// everyNth admits d00000, d0000n, ...
func everyNth(docs [][]float32, n int) map[string]bool {
	allowed := map[string]bool{}
	for i := range docs {
		if i%n == 0 {
			allowed[fmt.Sprintf("d%05d", i)] = true
		}
	}
	return allowed
}

func TestHNSWFilterStrategy(t *testing.T) {
	for _, c := range []struct {
		admitted, vectors int
		want              string
	}{
		{0, 0, FilterStrategyPrefilter}, // nothing to scan
		{10, 1_000, FilterStrategyPrefilter},
		{20_000, 200_000, FilterStrategyGraph},
		{19_999, 200_000, FilterStrategyPrefilter},
		{50_000, 200_000, FilterStrategyGraph},
		{4_000, 200_000, FilterStrategyPrefilter},
	} {
		if got := HNSWFilterStrategy(c.admitted, c.vectors); got != c.want {
			t.Errorf("HNSWFilterStrategy(%d, %d) = %s, want %s", c.admitted, c.vectors, got, c.want)
		}
	}
	if HNSWFilterMinShare(0) != 1 || HNSWFilterMinShare(10) != 1 {
		t.Error("a collection too small for the walk to pay must report a share of 1")
	}
	if got := HNSWFilterMinShare(200_000); math.Abs(got-0.1) > 0.001 {
		t.Errorf("HNSWFilterMinShare(200k) = %.4f, want 0.1", got)
	}
}

// The planner's estimates order the three ways the measurements did.
func TestFilterCosts(t *testing.T) {
	small := FilterCosts{Vectors: 20_000, Admitted: 5_000, TopK: 30} // 25% of 20k: ivf, then the walk
	if !(small.IVF() < small.Graph() && small.Graph() < small.Prefilter()) {
		t.Errorf("20k at 25%%: ivf %.0f, graph %.0f, prefilter %.0f", small.IVF(), small.Graph(), small.Prefilter())
	}
	tenth := FilterCosts{Vectors: 200_000, Admitted: 20_000, TopK: 30} // 10% of 200k: ivf
	if !(tenth.IVF() < tenth.Prefilter() && tenth.IVF() < tenth.Graph()) {
		t.Errorf("200k at 10%%: ivf %.0f, graph %.0f, prefilter %.0f", tenth.IVF(), tenth.Graph(), tenth.Prefilter())
	}
	quarter := FilterCosts{Vectors: 200_000, Admitted: 50_000, TopK: 30} // 25% of 200k: the walk
	if !(quarter.Graph() < quarter.Prefilter() && quarter.Graph() < quarter.IVF()) {
		t.Errorf("200k at 25%%: ivf %.0f, graph %.0f, prefilter %.0f", quarter.IVF(), quarter.Graph(), quarter.Prefilter())
	}
	none := FilterCosts{Vectors: 0, Admitted: 0, TopK: 10}
	if !math.IsInf(none.Graph(), 1) || !math.IsInf(none.IVF(), 1) || none.Prefilter() != 0 {
		t.Errorf("an empty filter: %v %v %v", none.Graph(), none.IVF(), none.Prefilter())
	}
	// A selective filter makes IVF probe on, up to every vector.
	tiny := FilterCosts{Vectors: 20_000, Admitted: 20, TopK: 30}
	if tiny.IVF() != ivfScanCost*20_000 {
		t.Errorf("ivf at 0.1%% = %.0f, want every vector", tiny.IVF())
	}
}

// The walk returns only admitted nodes, nearest first, and the exact top k
// of them on a graph this size.
func TestHNSWFilterSearchWalksTheGraph(t *testing.T) {
	h, docs, queries := filterIndex(t, 10_000)
	allowed := everyNth(docs, 2) // 5,000 of 10,000: the walk
	var admitted [][]float32
	for i, d := range docs {
		if i%2 == 0 {
			admitted = append(admitted, d)
		}
	}
	out := h.FilterSearch(FilterQuery{Collection: "c", Query: queries[3], TopK: 5, Threshold: -1, Allowed: allowed})
	if out.Strategy != FilterStrategyGraph || out.Fallback || len(out.Results) != 5 {
		t.Fatalf("outcome %+v", out)
	}
	for i, want := range exactTop(admitted, queries[3], 5) {
		if got := out.Results[i].DocID; got != fmt.Sprintf("d%05d", want*2) {
			t.Fatalf("result %d = %s, want d%05d", i, got, want*2)
		}
	}
	// Defaults: topK 5, cosine.
	if got := h.SearchWithFilter("c", queries[3], 0, -1, allowed, nil); len(got) != 5 || got[0] != out.Results[0] {
		t.Fatalf("defaults: %+v", got)
	}
	// The threshold drops what scores under it, and the walk is not redone.
	if got := h.SearchWithFilter("c", queries[3], 5, float64(out.Results[2].Score), allowed, nil); len(got) != 3 {
		t.Fatalf("threshold kept %d, want 3", len(got))
	}
	// Another metric reorders by its own score.
	dot := h.SearchWithFilter("c", queries[3], 5, -1, allowed, euclideanSimilarity)
	for i := 1; i < len(dot); i++ {
		if dot[i].Score > dot[i-1].Score {
			t.Fatalf("not sorted by the metric: %+v", dot)
		}
	}
}

// A filter admitting too little for the walk is scored exactly, and the
// whole collection is not.
func TestHNSWFilterSearchScoresASelectiveFilterExactly(t *testing.T) {
	h, docs, queries := filterIndex(t, 10_000)
	visited := 0
	prefilterVisited = func(n int) { visited += n }
	defer func() { prefilterVisited = nil }()
	out := h.FilterSearch(FilterQuery{Collection: "c", Query: queries[0], TopK: 3, Threshold: -1, Allowed: everyNth(docs, 100)})
	if out.Strategy != FilterStrategyPrefilter || len(out.Results) != 3 || visited != 100 {
		t.Fatalf("outcome %+v, visited %d", out, visited)
	}
}

// A walk that ends short — the filter admits documents without vectors —
// is answered by the exact scan, and the outcome says so.
func TestHNSWFilterSearchFallsBackWhenTheWalkEndsShort(t *testing.T) {
	h, docs, queries := filterIndex(t, 10_000)
	allowed := everyNth(docs, 2)
	for i := range 5_000 {
		allowed[fmt.Sprintf("missing%d", i)] = true // documents never embedded
	}
	delete(allowed, "d00000")
	for k := range allowed {
		if k != "d00002" && k[0] == 'd' {
			delete(allowed, k)
		}
	}
	out := h.FilterSearch(FilterQuery{Collection: "c", Query: queries[0], TopK: 3, Threshold: -1, Allowed: allowed})
	if out.Strategy != FilterStrategyGraph || !out.Fallback || len(out.Results) != 1 || out.Results[0].DocID != "d00002" {
		t.Fatalf("outcome %+v", out)
	}
}

// Tombstones guide the walk but are never returned, and a chunked
// document's chunks are found under its ID.
func TestHNSWFilterSearchSkipsTombstonesAndFindsChunks(t *testing.T) {
	h, docs, _ := filterIndex(t, 10_000)
	// Overwrite d00000 with a far vector: its old node becomes a tombstone.
	far := make([]float32, len(docs[0]))
	far[0] = -1
	if err := h.Add("c", "d00000", far); err != nil {
		t.Fatal(err)
	}
	h.Remove("c", "d00004")
	if err := h.Add("c", "doc#0", docs[0]); err != nil {
		t.Fatal(err)
	}
	allowed := everyNth(docs, 2)
	allowed["doc"] = true
	out := h.FilterSearch(FilterQuery{Collection: "c", Query: docs[0], TopK: 4, Threshold: -1, Allowed: allowed})
	if out.Strategy != FilterStrategyGraph {
		t.Fatalf("outcome %+v", out)
	}
	seen := map[string]bool{}
	for _, r := range out.Results {
		seen[r.DocID] = true
	}
	// d00000's old node holds the query itself; its new vector is far away.
	if out.Results[0].DocID != "doc#0" || seen["d00000"] || seen["d00004"] {
		t.Fatalf("results %+v", out.Results)
	}
	// The exact scan reaches the chunk too.
	exact := h.SearchWithFilter("c", docs[0], 1, -1, map[string]bool{"doc": true}, nil)
	if len(exact) != 1 || exact[0].DocID != "doc#0" {
		t.Fatalf("exact %+v", exact)
	}
	h.Remove("c", "doc#0")
	if _, ok := h.chunkKeys["c"]["doc"]; ok {
		t.Fatal("a removed chunk is still listed")
	}
}

func TestHNSWFilterSearchRefusals(t *testing.T) {
	h, _, queries := filterIndex(t, 100)
	if out := h.FilterSearch(FilterQuery{Collection: "none", Query: queries[0]}); out.Strategy != "" || out.Results != nil {
		t.Fatalf("unknown collection: %+v", out)
	}
	if out := h.FilterSearch(FilterQuery{Collection: "c", Query: []float32{1}}); out.Strategy != "" {
		t.Fatalf("wrong dimension: %+v", out)
	}
	g := newHNSWGraph(4, 10, 10)
	if g.SearchFiltered(queries[0], 3, 10, func(int32) bool { return true }) != nil {
		t.Fatal("an empty graph found something")
	}
	if hg := h.graphs["c"]; hg.SearchFiltered(queries[0], 0, 10, func(int32) bool { return true }) != nil {
		t.Fatal("k 0 found something")
	}
}

// Dropping a collection drops its chunk keys; a restored checkpoint
// rebuilds them.
func TestHNSWChunkKeysFollowTheCollection(t *testing.T) {
	h := NewHNSWIndex(8, 40, 16)
	if err := h.Add("c", "a#0", []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	h.DropCollection("c")
	if _, ok := h.chunkKeys["c"]; ok {
		t.Fatal("chunk keys survived the drop")
	}
	ci := chunkIndex{}
	ci.rebuild("c", map[string][]float32{"a#0": {1}, "a#1": {1}, "b": {1}})
	if len(ci["c"]["a"]) != 2 || len(ci["c"]) != 1 {
		t.Fatalf("rebuild: %+v", ci)
	}
	ci.forget("c", "a#0")
	ci.forget("c", "a#7") // not there: nothing changes
	if len(ci["c"]["a"]) != 1 {
		t.Fatalf("forget: %+v", ci)
	}
}
