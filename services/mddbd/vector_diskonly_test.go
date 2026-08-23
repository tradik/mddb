package main

import (
	"testing"

	vec "mddb/internal/vector"

	json "mddb/internal/jsonx"
)

func TestCollectionDiskOnly(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Unconfigured collection → not disk-only
	if s.collectionDiskOnly("plain") {
		t.Error("unconfigured collection reported disk-only")
	}

	// Quantized + diskOnly → true
	if err := s.CollectionManager.Set("compact", &CollectionConfig{Quantization: "int8", DiskOnlyVectors: true}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !s.collectionDiskOnly("compact") {
		t.Error("quantized diskOnly collection not reported disk-only")
	}

	// diskOnly without quantization → ignored (no compact representation)
	if err := s.CollectionManager.Set("nofloat", &CollectionConfig{DiskOnlyVectors: true}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if s.collectionDiskOnly("nofloat") {
		t.Error("diskOnly without quantization must be ignored")
	}
}

func TestRescoreFromDisk(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Store full-precision vectors on disk only.
	if err := s.VectorStore.Put("kb", "d1", []float32{1, 0, 0}, "m", "h1"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.VectorStore.Put("kb", "d2", []float32{0, 1, 0}, "m", "h2"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Approximate (deliberately wrong) candidate order from phase 1.
	candidates := []vec.VectorResult{
		{DocID: "d2", Score: 0.9},
		{DocID: "d1", Score: 0.8},
		{DocID: "missing", Score: 0.5},
	}

	query := []float32{1, 0, 0}
	rescored, vectors := s.rescoreFromDisk("kb", query, candidates, nil)

	// Exact scores flip the order: d1 (cos=1.0) must now lead d2 (cos=0.0).
	if rescored[0].DocID != "d1" {
		t.Errorf("rescore did not promote the truly closest vector: %+v", rescored)
	}
	if rescored[0].Score < 0.99 {
		t.Errorf("d1 exact score = %f, want ~1.0", rescored[0].Score)
	}
	// Missing vector keeps its approximate score and survives.
	found := false
	for _, r := range rescored {
		if r.DocID == "missing" && r.Score == 0.5 {
			found = true
		}
	}
	if !found {
		t.Error("candidate with missing disk vector was dropped or rescored")
	}
	// Fetched vectors are returned for reuse (MMR).
	if len(vectors) != 2 {
		t.Errorf("vectors fetched = %d, want 2", len(vectors))
	}
}

func TestDiskOnlyReindexKeepsFloatIndexEmpty(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.VectorIndex.SetReady()
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}

	if err := s.CollectionManager.Set("compact", &CollectionConfig{Quantization: "int8", DiskOnlyVectors: true}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	addTestDoc(t, s, "compact", "doc1", "en", "# Compact document content", nil)

	rec := doRequest(t, s.handleVectorReindex, VectorReindexRequestHTTP{Collection: "compact", Force: true})
	if rec.Code != 200 {
		t.Fatalf("reindex status %d: %s", rec.Code, rec.Body.String())
	}

	// Float32 flat index must stay empty; quantized index must have the vector.
	if n := s.VectorIndex.CollectionSize("compact"); n != 0 {
		t.Errorf("flat index holds %d vectors for disk-only collection, want 0", n)
	}
	if s.QuantizedVecIndex == nil || !s.QuantizedVecIndex.HasCollection("compact") {
		t.Fatal("quantized index missing disk-only collection")
	}

	// Disk record must be FULL precision (v1 format), not quantized.
	recs, err := s.VectorStore.LoadCollection("compact")
	if err != nil || len(recs) == 0 {
		t.Fatalf("LoadCollection: %v (%d records)", err, len(recs))
	}

	// Two-phase search returns the document with an exact score.
	searchRec := doRequest(t, s.handleVectorSearch, VectorSearchRequest{
		Collection:  "compact",
		QueryVector: []float32{0.1, 0.1, 0.1}, // same direction as mock embedding
		TopK:        3,
	})
	if searchRec.Code != 200 {
		t.Fatalf("search status %d: %s", searchRec.Code, searchRec.Body.String())
	}
	var resp VectorSearchResponseHTTP
	if err := json.Unmarshal(searchRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("disk-only search total = %d, want 1", resp.Total)
	}
	if resp.Results[0].Score < 0.99 {
		t.Errorf("exact rescored similarity = %f, want ~1.0", resp.Results[0].Score)
	}
}
