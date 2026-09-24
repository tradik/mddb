package vector

import (
	"os"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func trainCorpus() map[string][]float32 {
	return map[string][]float32{
		"d1": {1, 0, 0, 0, 0, 0, 0, 0}, "d2": {0.9, 0.1, 0, 0, 0, 0, 0, 0},
		"d3": {0, 1, 0, 0, 0, 0, 0, 0}, "d4": {0, 0.9, 0.1, 0, 0, 0, 0, 0},
		"d5": {0, 0, 1, 0, 0, 0, 0, 0}, "d6": {0, 0, 0.9, 0.1, 0, 0, 0, 0},
		"d7": {0, 0, 0, 1, 0, 0, 0, 0}, "d8": {0, 0, 0, 0, 1, 0, 0, 0},
	}
}

// TestSearcherInterfaceCoverage drives the full VectorSearcher surface
// (Name/IsReady/Collections/CollectionSize/SearchWithFilter/Remove) across the
// quantizing index implementations, which the package's other tests reach only
// indirectly.
func TestSearcherInterfaceCoverage(t *testing.T) {
	corpus := trainCorpus()
	pq := NewPQIndex(4, 16, 10)
	opq := NewOPQIndex(4, 16, 10, 3)
	sq := NewSQIndex()
	ivf := NewIVFIndex(2, 10)
	bq := NewBQIndex(2)
	trained := []VectorSearcher{pq, opq, sq, ivf}
	for id, v := range corpus {
		for _, idx := range trained {
			if err := idx.Add("c", id, v); err != nil {
				t.Fatal(err)
			}
		}
		if err := bq.Add("c", id, v); err != nil {
			t.Fatal(err)
		}
	}
	pq.Train("c", corpus)
	opq.Train("c", corpus)
	sq.Train("c", corpus)
	ivf.Train("c", corpus)
	// Adding after training exercises the per-index encode paths.
	for _, idx := range trained {
		if err := idx.Add("c", "d9", []float32{0, 0, 0, 0, 0, 0, 0, 1}); err != nil {
			t.Fatal(err)
		}
	}

	q := []float32{1, 0, 0, 0, 0, 0, 0, 0}
	allowed := map[string]bool{"d1": true, "d2": true}
	for _, idx := range []VectorSearcher{pq, opq, sq, ivf, bq} {
		idx.SetReady()
		if idx.Name() == "" {
			t.Error("Name() empty")
		}
		if !idx.IsReady() {
			t.Errorf("%s: IsReady() false after SetReady", idx.Name())
		}
		_ = idx.Collections()
		_ = idx.CollectionSize("c")
		_ = idx.Search("c", q, 5, 0.0, nil)
		_ = idx.SearchWithFilter("c", q, 5, 0.0, allowed, nil)
		idx.Remove("c", "d1")
	}
}

func TestQuantizedVectorIndexCoverage(t *testing.T) {
	qi := NewQuantizedVectorIndex(func(string) QuantizationType { return QuantInt8 })
	for id, v := range trainCorpus() {
		if err := qi.Add("c", id, v); err != nil {
			t.Fatal(err)
		}
	}
	qi.SetReady()
	if qi.Name() == "" {
		t.Error("Name() empty")
	}
	if !qi.IsReady() {
		t.Error("IsReady() false after SetReady")
	}
	if !qi.HasCollection("c") {
		t.Error("HasCollection(c) = false")
	}
	if qi.CollectionSize("c") == 0 {
		t.Error("CollectionSize(c) = 0")
	}
	_ = qi.Collections()
	_ = qi.SearchWithFilter("c", []float32{1, 0, 0, 0, 0, 0, 0, 0}, 5, 0.0, map[string]bool{"d1": true}, nil)
	qi.Remove("c", "d1")
}

func TestQuantizeQueryHelpers(t *testing.T) {
	q := []float32{0.1, 0.5, 0.9}
	if QuantizeQueryForInt8(q, 0, 1) == nil {
		t.Error("QuantizeQueryForInt8 nil")
	}
	if QuantizeQueryForInt4(q, 0, 1) == nil {
		t.Error("QuantizeQueryForInt4 nil")
	}
	dups := []VectorResult{{DocID: "a:0", Score: 0.9}, {DocID: "a:1", Score: 0.8}, {DocID: "b:0", Score: 0.7}}
	if got := DeduplicateChunkResults(dups); len(got) == 0 {
		t.Error("DeduplicateChunkResults returned empty")
	}
}

func newTestVectorStore(t *testing.T) *VectorStore {
	t.Helper()
	f, err := os.CreateTemp("", "vec_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	db, err := bolt.Open(f.Name(), 0600, nil)
	if err != nil {
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(); _ = os.Remove(f.Name()) })
	vs := NewVectorStore(db)
	if err := vs.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	return vs
}

func TestVectorStoreChunks(t *testing.T) {
	vs := newTestVectorStore(t)
	vs.SetBinlog(nil)
	chunks := []ChunkEmbedding{
		{ChunkIndex: 0, Vector: []float32{1, 0, 0}},
		{ChunkIndex: 1, Vector: []float32{0, 1, 0}},
	}
	if err := vs.PutChunks("c", "doc1", chunks, "model-x", "hash1"); err != nil {
		t.Fatal(err)
	}
	counts, err := vs.CountChunksByCollection()
	if err != nil {
		t.Fatal(err)
	}
	if counts["c"] == 0 {
		t.Errorf("expected chunks counted for 'c', got %v", counts)
	}
	// currentChunkCount=1 prunes the chunk at index >= 1.
	vs.CleanStaleChunks("c", "doc1", 1, NewVectorIndex())
}

func TestIVFSearchCoverage(t *testing.T) {
	// Lower the parallel-search threshold so the small corpus exercises the
	// goroutine-parallel cluster scan in searchClusters.
	defer swapParallelConfig(4, 2)()
	corpus := trainCorpus()
	ivf := NewIVFIndex(4, 15)
	for id, v := range corpus {
		if err := ivf.Add("c", id, v); err != nil {
			t.Fatal(err)
		}
	}
	ivf.Train("c", corpus)
	ivf.SetReady()
	queries := [][]float32{
		{1, 0, 0, 0, 0, 0, 0, 0},
		{0, 0, 0, 0, 1, 0, 0, 0},
		{0.5, 0.5, 0, 0, 0, 0, 0, 0},
	}
	for _, q := range queries {
		_ = ivf.Search("c", q, 3, 0.0, nil)
		_ = ivf.SearchWithFilter("c", q, 3, 0.1, map[string]bool{"d1": true, "d5": true}, nil)
	}
}
