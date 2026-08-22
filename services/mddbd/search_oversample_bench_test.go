package main

import (
	"fmt"
	"math/rand"
	"testing"

	vec "mddb/internal/vector"
)

// SRCH-005 claims oversample is a recall/latency knob. These measure both ends
// of it, because a knob whose effect nobody has measured is a knob with a label
// and no wiring.
//
// Chunked documents are the case that matters: the index holds one entry per
// chunk, results are deduplicated to one per document, and the deduplication is
// what eats the oversampled candidates.

const (
	oversampleDocs         = 2000
	oversampleChunksPerDoc = 5
	oversampleDims         = 64
	oversampleTopK         = 10
	benchOversampleSeed    = 42
)

func buildChunkedIndex(b *testing.B) (*vec.VectorIndex, []float32) {
	b.Helper()
	rng := rand.New(rand.NewSource(benchOversampleSeed)) // #nosec G404 -- deterministic benchmark fixture

	idx := vec.NewVectorIndex()
	for d := range oversampleDocs {
		// Chunks of one document sit close together, which is what makes
		// deduplication consume candidates: a single document can otherwise
		// fill the whole result set.
		base := make([]float32, oversampleDims)
		for i := range base {
			base[i] = rng.Float32()
		}
		for c := range oversampleChunksPerDoc {
			v := make([]float32, oversampleDims)
			for i := range v {
				v[i] = base[i] + float32(rng.NormFloat64())*0.05
			}
			idx.Add("bench", fmt.Sprintf("doc-%04d#%d", d, c), v)
		}
	}
	idx.SetReady()

	query := make([]float32, oversampleDims)
	for i := range query {
		query[i] = rng.Float32()
	}
	return idx, query
}

func benchmarkOversample(b *testing.B, factor float64) {
	idx, query := buildChunkedIndex(b)
	searchTopK := OversampledTopK(oversampleTopK, factor, 20)

	b.ReportMetric(float64(searchTopK), "candidates")
	b.ResetTimer()
	for range b.N {
		_ = idx.Search("bench", query, searchTopK, 0, nil)
	}
}

func BenchmarkOversample1x(b *testing.B)  { benchmarkOversample(b, 1.0) }
func BenchmarkOversample3x(b *testing.B)  { benchmarkOversample(b, 3.0) }
func BenchmarkOversample10x(b *testing.B) { benchmarkOversample(b, 10.0) }

// TestOversampleRecall measures what the extra candidates actually buy: how
// many distinct documents survive deduplication at each factor.
//
// A test rather than a benchmark, because the answer is a count, not a duration
// — and a knob that costs latency without raising recall would be worth knowing
// about before shipping it.
func TestOversampleRaisesDistinctDocumentRecall(t *testing.T) {
	rng := rand.New(rand.NewSource(benchOversampleSeed)) // #nosec G404 -- deterministic fixture

	idx := vec.NewVectorIndex()
	for d := range 500 {
		base := make([]float32, oversampleDims)
		for i := range base {
			base[i] = rng.Float32()
		}
		for c := range oversampleChunksPerDoc {
			v := make([]float32, oversampleDims)
			for i := range v {
				v[i] = base[i] + float32(rng.NormFloat64())*0.05
			}
			idx.Add("bench", fmt.Sprintf("doc-%04d#%d", d, c), v)
		}
	}
	idx.SetReady()

	query := make([]float32, oversampleDims)
	for i := range query {
		query[i] = rng.Float32()
	}

	distinctAt := func(factor float64) int {
		results := idx.Search("bench", query, OversampledTopK(oversampleTopK, factor, 20), 0, nil)
		seen := map[string]bool{}
		for _, r := range results {
			docID := r.DocID
			if i := len(docID) - 2; i > 0 && docID[i] == '#' {
				docID = docID[:i]
			}
			seen[docID] = true
			if len(seen) >= oversampleTopK {
				break
			}
		}
		return len(seen)
	}

	low, high := distinctAt(1.0), distinctAt(10.0)
	t.Logf("distinct documents after deduplication: 1x=%d, 3x=%d, 10x=%d",
		low, distinctAt(3.0), high)

	if high < low {
		t.Errorf("a higher oversample found FEWER distinct documents (%d vs %d) — "+
			"the knob costs latency without buying recall", high, low)
	}
}
