package vector

import (
	"fmt"
	"testing"
)

// SRCH-024. Recall gates for filtered search, next to the unfiltered ones of
// recall_gate_test.go: each index searched behind a filter admitting 1, 2,
// 10, 25 and 50% of the corpus, against the exact top 10 of what the filter
// admits. The filter takes every (100/pct)-th document, so it cuts across
// topics the way a metadata filter does, as the benchmark harness's does.

// filterSelectivities are the shares the gates and the harness measure.
var filterSelectivities = []int{1, 2, 10, 25, 50}

// filteredGate is a gate corpus behind one filter: the admitted IDs and the
// exact top 10 of each query among them.
type filteredGate struct {
	c       gateCorpus
	allowed map[string]bool
	truth   [][]string
}

// newFilteredGate admits every (100/pct)-th document of c.
func newFilteredGate(c gateCorpus, pct int) filteredGate {
	f := filteredGate{c: c, allowed: map[string]bool{}}
	var admitted [][]float32
	var ids []string
	for i, d := range c.docs {
		if i%(100/pct) == 0 {
			f.allowed[c.ids[i]] = true
			admitted = append(admitted, d)
			ids = append(ids, c.ids[i])
		}
	}
	for _, q := range c.queries {
		var want []string
		for _, i := range exactTop(admitted, q, recallGateTopK) {
			want = append(want, ids[i])
		}
		f.truth = append(f.truth, want)
	}
	return f
}

// recall is recall@10 of search over the gate's queries, failing on any
// result the filter does not admit.
func (f filteredGate) recall(t *testing.T, search func(q []float32) []VectorResult) float64 {
	t.Helper()
	var total float64
	for qi, q := range f.c.queries {
		got := search(q)
		for _, r := range got {
			if !f.allowed[r.DocID] {
				t.Fatalf("query %d returned %s, which the filter does not admit", qi, r.DocID)
			}
		}
		total += recallAt(got, f.truth[qi])
	}
	return total / float64(len(f.c.queries))
}

// fill adds the corpus to idx and trains it as the server does.
func (f filteredGate) fill(t *testing.T, idx VectorSearcher) {
	t.Helper()
	vectors := make(map[string][]float32, len(f.c.docs))
	for i, d := range f.c.docs {
		if err := idx.Add("c", f.c.ids[i], d); err != nil {
			t.Fatal(err)
		}
		vectors[f.c.ids[i]] = d
	}
	if trainable, ok := idx.(Trainable); ok {
		TrainSafely(trainable, "c", vectors)
	}
	idx.SetReady()
}

// The graph walk itself, whatever strategy FilterSearch would pick for a
// corpus this size: at 4,000 vectors it scores every filter under 71%
// exactly, so the walk is called directly. Measured: 1.000 at every
// selectivity.
func TestHNSWFilteredWalkRecall(t *testing.T) {
	c := wideGateCorpus()
	h := NewHNSWIndex(DefaultHNSWM, DefaultHNSWEfConstruction, DefaultHNSWEfSearch)
	newFilteredGate(c, 100).fill(t, h)
	g := h.graphs["c"]
	for _, pct := range filterSelectivities {
		f := newFilteredGate(c, pct)
		got := f.recall(t, func(q []float32) []VectorResult {
			fq := FilterQuery{Collection: "c", Query: q, TopK: recallGateTopK, Allowed: f.allowed}
			var out []VectorResult
			ef := filteredEf(h.efSearch, recallGateTopK, float64(len(f.allowed))/float64(len(c.docs)))
			for _, hit := range g.SearchFiltered(q, recallGateTopK, ef, h.admitter(g, fq)) {
				out = append(out, VectorResult{DocID: nodeDocID(hit.Key)})
			}
			return out
		})
		requireRecall(t, "hnsw walk at "+pctName(pct), got, 0.95)
	}
}

// FilterSearch as the server calls it: the walk or the exact scan, whichever
// it picks.
func TestHNSWFilteredRecall(t *testing.T) {
	c := wideGateCorpus()
	h := NewHNSWIndex(DefaultHNSWM, DefaultHNSWEfConstruction, DefaultHNSWEfSearch)
	newFilteredGate(c, 100).fill(t, h)
	for _, pct := range filterSelectivities {
		f := newFilteredGate(c, pct)
		got := f.recall(t, func(q []float32) []VectorResult {
			return h.SearchWithFilter("c", q, recallGateTopK, -1, f.allowed, nil)
		})
		requireRecall(t, "hnsw at "+pctName(pct), got, 0.95)
	}
}

// IVF probes nProbe clusters, and behind a selective filter those hold only
// a few admitted vectors: recall@10 was 0.773 at 2% before filteredProbes.
func TestIVFFilteredRecall(t *testing.T) {
	c := wideGateCorpus()
	idx := NewIVFIndex(prodIVFNProbe, prodKMeansMaxIter)
	newFilteredGate(c, 100).fill(t, idx)
	for _, pct := range filterSelectivities {
		f := newFilteredGate(c, pct)
		got := f.recall(t, func(q []float32) []VectorResult {
			return idx.SearchWithFilter("c", q, recallGateTopK, -1, f.allowed, nil)
		})
		requireRecall(t, "ivf at "+pctName(pct), got, 0.95)
	}
}

func pctName(pct int) string {
	return fmt.Sprintf("%d%%", pct)
}
