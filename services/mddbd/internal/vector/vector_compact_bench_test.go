package vector

import (
	"fmt"
	"math/rand/v2"
	"runtime"
	"testing"
)

// GO-029 step 1: measure the degradation before building anything.
//
// The question is what a collection actually costs after heavy deletion
// compared with a freshly built index holding the same live vectors — in
// search latency and in retained memory.

const benchDims = 128

func benchVector(r *rand.Rand) []float32 {
	v := make([]float32, benchDims)
	for i := range v {
		v[i] = r.Float32()
	}
	return v
}

// buildIndex fills an index with total vectors, then deletes deleteRatio of
// them, leaving the survivors behind.
func buildIndex(total int, deleteRatio float64) (*VectorIndex, []string) {
	r := rand.New(rand.NewPCG(42, 1024)) //nolint:gosec // G404: deterministic test data is the point here
	idx := NewVectorIndex()
	ids := make([]string, total)
	for i := range total {
		ids[i] = fmt.Sprintf("doc-%06d", i)
		idx.Add("bench", ids[i], benchVector(r))
	}
	cut := int(float64(total) * deleteRatio)
	for i := range cut {
		idx.Remove("bench", ids[i])
	}
	return idx, ids[cut:]
}

func benchmarkCompactSearch(b *testing.B, idx *VectorIndex) {
	r := rand.New(rand.NewPCG(7, 7)) //nolint:gosec // G404: deterministic test data is the point here
	q := benchVector(r)
	b.ReportAllocs()
	for b.Loop() {
		idx.Search("bench", q, 10, 0, CosineSimilarity)
	}
}

// A freshly built index of 50 000 live vectors — the reference point.
func BenchmarkVectorSearchFresh50k(b *testing.B) {
	idx, _ := buildIndex(50_000, 0)
	benchmarkCompactSearch(b, idx)
}

// The same 50 000 live vectors, but reached by deleting half of 100 000.
func BenchmarkVectorSearchAfter50PctDelete(b *testing.B) {
	idx, _ := buildIndex(100_000, 0.5)
	benchmarkCompactSearch(b, idx)
}

// TestDeletedVectorsRetainMemory reports what a half-deleted collection still
// holds compared with a fresh one of the same live size. Go maps never shrink,
// so the bucket array from the larger index stays allocated.
func TestDeletedVectorsRetainMemory(t *testing.T) {
	measure := func(build func() *VectorIndex) uint64 {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		idx := build()
		runtime.GC()
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(idx)
		return after.HeapAlloc - before.HeapAlloc
	}

	fresh := measure(func() *VectorIndex { i, _ := buildIndex(20_000, 0); return i })
	deleted := measure(func() *VectorIndex { i, _ := buildIndex(40_000, 0.5); return i })

	t.Logf("fresh 20k live:            %d KB", fresh/1024)
	t.Logf("40k with half deleted:     %d KB", deleted/1024)
	if fresh > 0 {
		t.Logf("retained overhead:         %.1f%%", float64(deleted-fresh)/float64(fresh)*100)
	}
}

// The HNSW graph is where deletion is expected to hurt: the library keeps
// nodes reachable through their neighbours' edges, so a half-deleted graph
// can keep traversing structure that no longer holds live vectors.

func buildHNSW(total int, deleteRatio float64) *HNSWIndex {
	r := rand.New(rand.NewPCG(42, 1024)) //nolint:gosec // G404: deterministic test data is the point here
	idx := NewHNSWIndex(16, 0, 100)
	ids := make([]string, total)
	for i := range total {
		ids[i] = fmt.Sprintf("doc-%06d", i)
		idx.Add("bench", ids[i], benchVector(r))
	}
	for i := range int(float64(total) * deleteRatio) {
		idx.Remove("bench", ids[i])
	}
	return idx
}

func benchmarkHNSWSearch(b *testing.B, idx *HNSWIndex) {
	r := rand.New(rand.NewPCG(7, 7)) //nolint:gosec // G404: deterministic test data is the point here
	q := benchVector(r)
	b.ReportAllocs()
	for b.Loop() {
		idx.Search("bench", q, 10, 0, CosineSimilarity)
	}
}

func BenchmarkHNSWSearchFresh10k(b *testing.B) {
	benchmarkHNSWSearch(b, buildHNSW(10_000, 0))
}

func BenchmarkHNSWSearchAfter50PctDelete(b *testing.B) {
	benchmarkHNSWSearch(b, buildHNSW(20_000, 0.5))
}

// TestHNSWReportsLiveSizeAfterDelete checks the bookkeeping the operator would
// see: whether a deleted vector still counts towards the collection.
func TestHNSWReportsLiveSizeAfterDelete(t *testing.T) {
	idx := buildHNSW(1000, 0.5)
	if got := idx.CollectionSize("bench"); got != 500 {
		t.Errorf("CollectionSize after deleting half = %d, want 500", got)
	}
}
