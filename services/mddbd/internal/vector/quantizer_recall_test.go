package vector

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"testing"
)

// SRCH-003 asks for a 4-bit index quantizer, and it asks for the numbers
// first: it may only ship if its recall/storage curve beats what is already
// here. This is that measurement, kept as a test so the claim in
// docs/QUANTIZATION.md can be re-checked rather than believed.
//
// Run it with:
//
//	go test ./internal/vector -run TestQuantizerRecallCurve -v
//
// Recall@10 is measured against exact cosine search over the same vectors —
// the ground truth is what a flat index returns, because that is what a caller
// gives up when they choose a quantizer.

const (
	recallDim     = 384
	recallVectors = 2000
	recallQueries = 200
	recallTopK    = 10
)

// clusteredCorpus generates vectors with cluster structure.
//
// Uniform random vectors in high dimensions are nearly equidistant, which
// makes every quantizer look equally good and measures nothing. Real
// embeddings cluster by topic, and that structure is exactly what
// quantization has to preserve.
func clusteredCorpus(n, dim, clusters int, seed uint64) [][]float32 {
	// Deterministic, so two runs measure the same corpus and a recall change
	// means the quantizer changed.
	//
	// #nosec G404 -- benchmark corpus, deliberately reproducible
	src := rand.New(rand.NewPCG(seed, 0x9e3779b9))

	centroids := make([][]float32, clusters)
	for c := range centroids {
		centroids[c] = make([]float32, dim)
		for d := range centroids[c] {
			centroids[c][d] = float32(src.NormFloat64())
		}
		normalise(centroids[c])
	}

	out := make([][]float32, n)
	for i := range out {
		centre := centroids[src.IntN(clusters)]
		v := make([]float32, dim)
		for d := range v {
			// Close to a centroid, with enough spread that neighbours differ.
			v[d] = centre[d] + float32(src.NormFloat64())*0.35
		}
		normalise(v)
		out[i] = v
	}
	return out
}

func normalise(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}

// exactTopK is the ground truth: what a flat index returns.
func exactTopK(corpus [][]float32, ids []string, query []float32, k int) []string {
	type scored struct {
		id    string
		score float32
	}
	all := make([]scored, len(corpus))
	for i, v := range corpus {
		all[i] = scored{ids[i], CosineSimilarity(query, v)}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].score > all[j].score })

	out := make([]string, 0, k)
	for i := 0; i < k && i < len(all); i++ {
		out = append(out, all[i].id)
	}
	return out
}

func recallAt(got []VectorResult, want []string) float64 {
	if len(want) == 0 {
		return 1
	}
	truth := make(map[string]bool, len(want))
	for _, id := range want {
		truth[id] = true
	}
	hits := 0
	for _, r := range got {
		if truth[r.DocID] {
			hits++
		}
	}
	return float64(hits) / float64(len(want))
}

func TestQuantizerRecallCurve(t *testing.T) {
	corpus := clusteredCorpus(recallVectors, recallDim, 24, 42)
	ids := make([]string, len(corpus))
	for i := range ids {
		ids[i] = fmt.Sprintf("doc-%04d", i)
	}
	queries := clusteredCorpus(recallQueries, recallDim, 24, 7)

	// Bytes a quantizer needs per vector for its codes alone — the number that
	// decides whether a corpus fits in RAM. Re-ranking copies of the original
	// vectors are counted separately below, because keeping them is a choice.
	codeBytes := map[string]int{
		"flat (float32)": recallDim * 4,
		"sq  (int8)":     recallDim,
		"sq4 (int4)":     (recallDim + 1) / 2,
		"bq  (1 bit)":    (recallDim + 7) / 8,
	}

	type row struct {
		name   string
		recall float64
		bytes  int
	}
	var rows []row

	measure := func(name string, idx VectorSearcher) {
		// Train first, with the whole corpus: a scalar quantizer's ranges are
		// a property of the collection, and Add before Train stores the vector
		// without encoding it. An earlier version of this test skipped Train
		// and measured both scalar quantizers at zero recall — they had
		// nothing to search.
		vectors := make(map[string][]float32, len(corpus))
		for i, v := range corpus {
			vectors[ids[i]] = v
		}
		if trainable, ok := idx.(Trainable); ok {
			trainable.Train("bench", vectors)
		} else {
			for i, v := range corpus {
				idx.Add("bench", ids[i], v)
			}
		}
		idx.SetReady()

		var total float64
		for _, q := range queries {
			got := idx.Search("bench", q, recallTopK, 0, nil)
			total += recallAt(got, exactTopK(corpus, ids, q, recallTopK))
		}
		rows = append(rows, row{name, total / float64(len(queries)), codeBytes[name]})
	}

	measure("sq  (int8)", NewSQIndex())
	measure("bq  (1 bit)", NewBQIndex(0))
	measure("sq4 (int4)", NewSQ4Index())

	t.Log("")
	t.Logf("  %d vectors × %d dims, %d queries, recall@%d against exact cosine",
		recallVectors, recallDim, recallQueries, recallTopK)
	t.Log("")
	t.Logf("  %-16s %10s %14s %12s", "quantizer", "recall@10", "bytes/vector", "vs float32")
	t.Logf("  %-16s %10s %14d %12s", "flat (float32)", "1.000", codeBytes["flat (float32)"], "1×")
	for _, r := range rows {
		t.Logf("  %-16s %10.3f %14d %11.1f×",
			r.name, r.recall, r.bytes,
			float64(codeBytes["flat (float32)"])/float64(r.bytes))
	}
	t.Log("")

	// The gate SRCH-003 set: recall@10 at least 95% of int8's, at no more than
	// 55% of its storage.
	var sqRecall, sq4Recall float64
	for _, r := range rows {
		switch r.name {
		case "sq  (int8)":
			sqRecall = r.recall
		case "sq4 (int4)":
			sq4Recall = r.recall
		}
	}

	ratio := sq4Recall / sqRecall
	storageRatio := float64(codeBytes["sq4 (int4)"]) / float64(codeBytes["sq  (int8)"])
	t.Logf("  sq4 keeps %.1f%% of int8's recall at %.0f%% of its storage",
		ratio*100, storageRatio*100)

	if ratio < 0.95 {
		t.Errorf("sq4 recall is %.1f%% of int8's, below the 95%% gate", ratio*100)
	}
	if storageRatio > 0.55 {
		t.Errorf("sq4 storage is %.0f%% of int8's, above the 55%% gate", storageRatio*100)
	}
}
