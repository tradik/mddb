package vector

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"testing"
)

// SRCH-013. coder/hnsw, the graph until 2.18.9, measured recall@10 of 0.86,
// 0.83 and 0.66 at 2k, 200k and 2M on the planner benchmark's corpus, and
// 0.70–0.83 between loads of one 200k. The corpus arrives topic by topic, and
// without the neighbour-selection heuristic each topic's edges to the others
// were the first evicted. These tests hold the replacement to recall on that
// shape, in that order.

// topicCorpus is the planner benchmark's corpus in miniature: unit
// centroids, unit documents around them stored topic by topic, and one query
// per topic near its centroid.
func topicCorpus(seed uint64, dims, topics, perTopic int) (docs, queries [][]float32) {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) //nolint:gosec // G404: deterministic test data is the point here
	gauss := func(scale float64) []float32 {
		v := make([]float32, dims)
		for i := range v {
			v[i] = float32(r.NormFloat64() * scale)
		}
		return v
	}
	unit := func(v []float32) []float32 {
		inv := invNormOf(v)
		for i := range v {
			v[i] *= inv
		}
		return v
	}
	near := func(c []float32, scale float64) []float32 {
		v := gauss(scale)
		for i := range v {
			v[i] += c[i]
		}
		return unit(v)
	}
	noise := 0.9 / math.Sqrt(float64(dims))
	centroids := make([][]float32, topics)
	for t := range centroids {
		centroids[t] = unit(gauss(1))
	}
	for t := range topics {
		for range perTopic {
			docs = append(docs, near(centroids[t], noise))
		}
	}
	for t := range topics {
		queries = append(queries, near(centroids[t], noise/2))
	}
	return docs, queries
}

// exactTop is the brute-force top k of docs for q, as indexes.
func exactTop(docs [][]float32, q []float32, k int) []int {
	idx := make([]int, len(docs))
	scores := make([]float32, len(docs))
	for i, d := range docs {
		idx[i], scores[i] = i, CosineSimilarity(q, d)
	}
	sort.Slice(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
	return idx[:k]
}

// hnswRecall is recall@10 of an HNSWIndex built from docs in the order given.
func hnswRecall(t *testing.T, docs, queries [][]float32, order []int) float64 {
	t.Helper()
	idx := NewHNSWIndex(DefaultHNSWM, DefaultHNSWEfConstruction, DefaultHNSWEfSearch)
	for _, i := range order {
		if err := idx.Add("c", fmt.Sprintf("d%05d", i), docs[i]); err != nil {
			t.Fatal(err)
		}
	}
	hits := 0
	for _, q := range queries {
		got := map[string]bool{}
		for _, r := range idx.Search("c", q, 10, -1, nil) {
			got[r.DocID] = true
		}
		for _, i := range exactTop(docs, q, 10) {
			if got[fmt.Sprintf("d%05d", i)] {
				hits++
			}
		}
	}
	return float64(hits) / float64(10*len(queries))
}

func TestHNSWRecallOnAClusteredCorpus(t *testing.T) {
	docs, queries := topicCorpus(1, 32, 50, 80) // 4,000 vectors
	inOrder := make([]int, len(docs))
	for i := range inOrder {
		inOrder[i] = i
	}
	shuffled := rand.New(rand.NewPCG(7, 7)).Perm(len(docs)) //nolint:gosec // G404: deterministic test data

	byTopic := hnswRecall(t, docs, queries, inOrder)
	random := hnswRecall(t, docs, queries, shuffled)
	t.Logf("recall@10: topic by topic %.3f, shuffled %.3f", byTopic, random)
	if byTopic < 0.98 {
		t.Errorf("recall@10 with documents stored topic by topic = %.3f, want >= 0.98", byTopic)
	}
	if random < 0.98 {
		t.Errorf("recall@10 with documents stored shuffled = %.3f, want >= 0.98", random)
	}
	if math.Abs(byTopic-random) > 0.03 {
		t.Errorf("recall depends on insertion order: %.3f against %.3f", byTopic, random)
	}
}

// One insertion order builds one graph: levels come from a fixed seed, not the
// clock, so two loads of the same data search alike.
func TestHNSWGraphIsDeterministic(t *testing.T) {
	docs, queries := topicCorpus(3, 16, 10, 50)
	build := func() *hnswGraph {
		g := newHNSWGraph(8, 50, 20)
		for i, d := range docs {
			g.Add(fmt.Sprint(i), d)
		}
		return g
	}
	a, b := build(), build()
	for _, q := range queries {
		ha, hb := a.Search(q, 10), b.Search(q, 10)
		if fmt.Sprint(keysOf(ha)) != fmt.Sprint(keysOf(hb)) {
			t.Fatalf("two builds of the same data disagree: %v against %v", keysOf(ha), keysOf(hb))
		}
	}
}

func keysOf(hits []hnswHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Key
	}
	return out
}

func TestHNSWGraphEdges(t *testing.T) {
	g := newHNSWGraph(4, 10, 2)
	if hits := g.Search([]float32{1, 0}, 3); hits != nil {
		t.Errorf("an empty graph found %v", hits)
	}
	g.Add("a", []float32{1, 0})
	g.Add("zero", []float32{0, 0}) // no direction: distance 1 from everything
	g.Add("b", []float32{0, 1})
	if hits := g.Search([]float32{1, 0}, 0); hits != nil {
		t.Errorf("k=0 found %v", hits)
	}
	// k above efSearch widens the beam to k.
	if hits := g.Search([]float32{1, 0}, 3); len(hits) != 3 || hits[0].Key != "a" {
		t.Errorf("search = %v, want all three, a first", keysOf(hits))
	}
	if g.Len() != 3 {
		t.Errorf("Len = %d, want 3", g.Len())
	}
}

// Every node's link lists stay within m (2m on layer 0) however many nodes
// crowd one neighbourhood: the overflow is pruned by the heuristic.
func TestHNSWGraphPrunesLinksToTheLimit(t *testing.T) {
	docs, _ := topicCorpus(5, 8, 1, 300)
	g := newHNSWGraph(4, 20, 10)
	for i, d := range docs {
		g.Add(fmt.Sprint(i), d)
	}
	for id, layers := range g.links {
		for l, ns := range layers {
			limit := g.m
			if l == 0 {
				limit = g.m0
			}
			if len(ns) > limit {
				t.Fatalf("node %d has %d links on layer %d, limit %d", id, len(ns), l, limit)
			}
		}
	}
}

// The visited set is cleared by bumping a generation; when the counter wraps
// the marks are wiped, or a node marked 2^32 searches ago would read as seen.
func TestVisitSetWraps(t *testing.T) {
	g := newHNSWGraph(4, 10, 10)
	g.Add("a", []float32{1, 0})
	s := g.visitSet()
	s.mark(0)
	s.gen = math.MaxUint32
	g.visits.Put(s)
	s = g.visitSet()
	if s.gen != 1 || !s.mark(0) {
		t.Errorf("after wrapping, gen = %d and node 0 still reads as visited", s.gen)
	}
	if s.mark(0) {
		t.Error("a node marked twice in one search was new both times")
	}
}

// A filter the graph's oversampled neighbours cannot satisfy falls back to
// scoring the admitted vectors, and the answer is still the exact filtered
// top k.
func TestHNSWFilterFallsBackToTheAdmittedVectors(t *testing.T) {
	docs, queries := topicCorpus(11, 16, 10, 40) // 400 vectors
	idx := NewHNSWIndex(8, 40, 16)
	allowed := map[string]bool{}
	var admitted [][]float32
	for i, d := range docs {
		id := fmt.Sprintf("d%03d", i)
		if err := idx.Add("c", id, d); err != nil {
			t.Fatal(err)
		}
		if i%20 == 0 { // 20 documents, spread over every topic
			allowed[id] = true
			admitted = append(admitted, d)
		}
	}
	got := idx.SearchWithFilter("c", queries[0], 5, -1, allowed, nil)
	if len(got) != 5 {
		t.Fatalf("got %d results, want 5", len(got))
	}
	want := exactTop(admitted, queries[0], 5)
	for i, r := range got {
		if r.DocID != fmt.Sprintf("d%03d", want[i]*20) {
			t.Fatalf("result %d = %s, want d%03d", i, r.DocID, want[i]*20)
		}
	}
}

func TestNewHNSWIndexDefaults(t *testing.T) {
	idx := NewHNSWIndex(0, 0, 0)
	if idx.m != DefaultHNSWM || idx.efConstruction != DefaultHNSWEfConstruction || idx.efSearch != DefaultHNSWEfSearch {
		t.Errorf("defaults = %d/%d/%d", idx.m, idx.efConstruction, idx.efSearch)
	}
	idx = NewHNSWIndex(8, 40, 30)
	if idx.m != 8 || idx.efConstruction != 40 || idx.efSearch != 30 {
		t.Errorf("explicit = %d/%d/%d, want 8/40/30", idx.m, idx.efConstruction, idx.efSearch)
	}
	if err := idx.Add("c", "a", []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	if g := idx.graphs["c"]; g.efConstruction != 40 || g.efSearch != 30 || g.m0 != 16 {
		t.Errorf("the graph got %d/%d/%d, want efConstruction 40, efSearch 30, m0 16", g.efConstruction, g.efSearch, g.m0)
	}
}
