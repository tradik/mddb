package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

func covGVector(t *testing.T, s *Server, body any) (VectorSearchResponseHTTP, *httptest.ResponseRecorder) {
	t.Helper()
	rec := doRequest(t, s.handleVectorSearch, body)
	var resp VectorSearchResponseHTTP
	if rec.Code == http.StatusOK {
		covGDecode(t, rec, &resp)
	}
	return resp, rec
}

func TestCovGVectorSearchEmbeddingFailure(t *testing.T) {
	s := covGServer(t)
	s.VectorIndex.SetReady()
	s.Embedding = covGFailEmbedding{&mockEmbedding{dims: 3, model: "m"}}
	_, rec := covGVector(t, s, map[string]any{"collection": "blog", "query": "x"})
	covGWantCode(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "failed to embed query") {
		t.Errorf("body: %s", rec.Body.String())
	}
}

func TestCovGVectorSearchFilters(t *testing.T) {
	s := covGVectorServer(t)
	goDoc := covGEmbedded(t, s, "blog", "a", "golang", map[string][]string{"tag": {"go"}})
	covGEmbedded(t, s, "blog", "b", "rust", map[string][]string{"tag": {"rust"}})

	resp, rec := covGVector(t, s, map[string]any{"collection": "blog", "query": "x",
		"filterMeta": map[string][]string{"tag": {"go"}}})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Total != 1 || resp.Results[0].Document.ID != goDoc.ID {
		t.Errorf("filtered: %+v", resp.Results)
	}

	resp, rec = covGVector(t, s, map[string]any{"collection": "blog", "query": "x", "distanceMetric": "euclidean",
		"filterMeta": map[string][]string{"tag": {"cobol"}}})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Total != 0 || resp.Results == nil || resp.DistanceMetric != "euclidean" || resp.Algorithm != "flat" {
		t.Errorf("no match: %+v", resp)
	}
}

func TestCovGVectorSearchModesAndBudget(t *testing.T) {
	s := covGVectorServer(t)
	covGEmbedded(t, s, "blog", "a", "line one\nline two\nline three", nil)

	// window mode with no size defaults to one neighbour per side.
	resp, rec := covGVector(t, s, map[string]any{"collection": "blog", "query": "x", "retrievalMode": "window"})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Total != 1 || resp.Results[0].ChunkIndex == nil || resp.Results[0].ChunkText == "" {
		t.Errorf("window: %+v", resp.Results)
	}

	// graph mode on prose expands to nothing and says so by omission.
	resp, _ = covGVector(t, s, map[string]any{"collection": "blog", "query": "x", "retrievalMode": "graph"})
	if resp.Total != 1 || resp.GraphExpansions != nil {
		t.Errorf("graph on prose: %+v", resp)
	}

	// In chunk mode the budget is charged for the passage.
	covGSetConfig(t, s, "blog", &CollectionConfig{Retrieval: &RetrievalProfileDef{ContextTokenBudget: 1}})
	resp, _ = covGVector(t, s, map[string]any{"collection": "blog", "query": "x", "retrievalMode": "chunk"})
	if resp.Total != 1 || resp.ContextTruncated {
		t.Errorf("the first passage is always kept: %+v", resp)
	}
	// In parent mode it is charged for the whole document.
	resp, _ = covGVector(t, s, map[string]any{"collection": "blog", "query": "x", "includeContent": true})
	if resp.Total != 1 || resp.Results[0].Document.ContentMD == "" {
		t.Errorf("parent mode under a budget: %+v", resp)
	}
}

func TestCovGVectorSearchSkipsMissingCorruptAndNoBucket(t *testing.T) {
	s := covGVectorServer(t)
	gone := covGEmbedded(t, s, "blog", "gone", "x", nil)
	bad := covGEmbedded(t, s, "blog", "bad", "y", nil)
	if err := s.DBUpdateC("blog", func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Delete(storage.DocKey("blog", gone.ID))
	}); err != nil {
		t.Fatal(err)
	}
	covGPutRaw(t, s, "blog", bad.ID, []byte{0xff, 0xfe})
	resp, rec := covGVector(t, s, map[string]any{"collection": "blog", "query": "x"})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Total != 0 {
		t.Errorf("neither a missing nor a corrupt document can be returned: %+v", resp.Results)
	}

	if err := s.DBUpdateC("blog", func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("docs")) }); err != nil {
		t.Fatal(err)
	}
	resp, _ = covGVector(t, s, map[string]any{"collection": "blog", "query": "x"})
	if resp.Total != 0 {
		t.Errorf("no docs bucket: %+v", resp.Results)
	}
}

func TestCovGVectorSearchPendingWithoutContent(t *testing.T) {
	s, _, cleanup := indexingServer(t)
	defer cleanup()
	// p0 already has a vector (put there by hand); p1 is still queued.
	p0 := addViaHTTP(t, s, "p0", "indexed pelican story")
	if err := s.indexChunk("notes", p0.ID+"#0", []float32{0.1, 0.1, 0.1}, false); err != nil {
		t.Fatal(err)
	}
	addViaHTTP(t, s, "p1", "pending pelican story")
	resp := vectorSearchNotes(t, s, VectorSearchRequest{Collection: "notes", Query: "pelican", TopK: 5,
		IndexWait: IndexWait{ReadLevel: ReadLevelIndexedPlusPending}})
	if len(resp.Results) != 2 || resp.Results[0].Document.Key != "p0" || resp.Results[0].PendingEmbedding {
		t.Fatalf("the indexed match comes first, once: %+v", resp.Results)
	}
	if p := resp.Results[1]; !p.PendingEmbedding || p.Document.Key != "p1" || p.Document.ContentMD != "" {
		t.Fatalf("pending without includeContent: %+v", p)
	}
}

func TestCovGVectorReindexStoreFailure(t *testing.T) {
	s := covGServer(t)
	s.Embedding = &mockEmbedding{dims: 3, model: "m"}
	_ = s.DB.Close()
	rec := doRequest(t, s.handleVectorReindex, map[string]any{"collection": "blog"})
	covGWantCode(t, rec, http.StatusBadRequest)
}

func TestCovGVectorStatsPendingAndQuantization(t *testing.T) {
	s := covGVectorServer(t)
	covGEmbedded(t, s, "blog", "a", "x", nil)
	covGSetConfig(t, s, "blog", &CollectionConfig{Quantization: "int8", DiskOnlyVectors: true})

	rec := covGRaw(s.handleVectorStats, http.MethodGet, "/", "")
	var out struct {
		Collections map[string]map[string]any
	}
	covGDecode(t, rec, &out)
	blog := out.Collections["blog"]
	if blog["quantization"] != "int8" || blog["diskOnlyVectors"] != true {
		t.Errorf("quantization info: %+v", blog)
	}

	// No snapshot yet and no time to wait for one: say so.
	s2 := covGServer(t)
	t.Setenv("MDDB_STATS_WAIT", "0")
	rec = covGRaw(s2.handleVectorStats, http.MethodGet, "/", "")
	var pending map[string]any
	covGDecode(t, rec, &pending)
	if pending["statsPending"] != true {
		t.Errorf("with no snapshot and no wait, stats are pending: %+v", pending)
	}
}

func TestCovGLoadVectorIndexRefusesEmptyAndKeepsDiskOnlyOutOfRAM(t *testing.T) {
	s := covGServer(t)
	for _, put := range []struct {
		coll, id string
		vec      []float32
	}{
		{"mem", "good#0", []float32{0.1, 0.2, 0.3}},
		{"mem", "empty#0", []float32{}},
		{"disk", "d#0", []float32{0.3, 0.2, 0.1}},
	} {
		if err := s.VectorStore.Put(put.coll, put.id, put.vec, "m", "h"); err != nil {
			t.Fatal(err)
		}
	}
	covGSetConfig(t, s, "disk", &CollectionConfig{Quantization: "int8", DiskOnlyVectors: true})

	s.loadVectorIndex()

	if !s.VectorIndex.IsReady() {
		t.Fatal("the index must be ready after loading")
	}
	if s.VectorIndex.GetVector("mem", "good#0") == nil {
		t.Error("a valid vector was not loaded")
	}
	if s.VectorIndex.GetVector("mem", "empty#0") != nil {
		t.Error("an empty vector must be refused, not loaded")
	}
	if s.VectorIndex.GetVector("disk", "d#0") != nil {
		t.Error("a disk-only collection must not hold float32 vectors in RAM")
	}
	if !s.QuantizedVecIndex.HasCollection("disk") {
		t.Error("the disk-only collection should be in the quantized index")
	}
}
