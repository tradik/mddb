package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/storage"
	"mddb/internal/vector"

	bolt "go.etcd.io/bbolt"
)

// The paths of /v1/find-duplicates the mode tests do not reach: refusals,
// a failing store, chunked and truncated vector sets, and documents the
// enrichment cannot load.

func TestFindDuplicatesRefusesACallerWithoutRead(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	covEDenyAll(t, s)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/find-duplicates", strings.NewReader(`{"collection":"blog"}`)).WithContext(context.Background())
	s.handleFindDuplicates(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("anonymous caller: %d %s", w.Code, w.Body.String())
	}
}

func TestFindDuplicatesReportsAStoreFailure(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	_ = s.DB.Close()

	rec := doRequest(t, s.handleFindDuplicates, FindDuplicatesRequest{Collection: "blog", Mode: "exact"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("closed store: %d %s", rec.Code, rec.Body.String())
	}
}

func TestFindDuplicatesWithoutADocsBucket(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := s.DB.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("docs")) }); err != nil {
		t.Fatal(err)
	}
	resp, err := s.findDuplicates(FindDuplicatesRequest{Collection: "blog", Mode: "exact", MaxDocs: 10})
	if err != nil || resp.TotalDocuments != 0 {
		t.Fatalf("no docs bucket: %+v, %v", resp, err)
	}
	// The enrichment has nothing to read either.
	groups := []DuplicateGroup{{Documents: []DuplicateDocInfo{{DocID: "x"}}}}
	s.enrichDuplicateGroups("blog", groups, true)
	if groups[0].Documents[0].Key != "" {
		t.Errorf("enriched from a missing bucket: %+v", groups[0].Documents[0])
	}
}

// Only a document's first chunk counts, maxDocs truncates, and two groups of
// each kind come back largest (exact) and most similar (similar) first.
func TestFindDuplicatesChunksTruncationAndOrdering(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	ids := map[string]string{}
	for _, k := range []string{"a1", "a2", "a3", "b1", "b2", "z"} {
		ids[k] = addTestDoc(t, s, "dups", k, "en", "body of "+k, nil).ID
	}
	put := func(key, hash string, v []float32) {
		t.Helper()
		if err := s.VectorStore.PutChunks("dups", ids[key], []vector.ChunkEmbedding{
			{ChunkIndex: 0, Vector: v}, {ChunkIndex: 1, Vector: []float32{0, 0, 1}},
		}, "m", hash); err != nil {
			t.Fatal(err)
		}
	}
	put("a1", "ha", []float32{1, 0, 0})
	put("a2", "ha", []float32{1, 0, 0})
	put("a3", "ha", []float32{0.99, 0.01, 0})
	put("b1", "hb", []float32{0, 1, 0})
	put("b2", "hb", []float32{0, 0.9, 0.1})
	put("z", "hz", []float32{0, 0, 1})

	resp, err := s.findDuplicates(FindDuplicatesRequest{Collection: "dups", Mode: "both", Threshold: 0.9, MaxDocs: 100})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalEmbedded != 6 {
		t.Errorf("chunks beyond the first were counted: %d embedded", resp.TotalEmbedded)
	}
	if len(resp.ExactGroups) != 2 || len(resp.ExactGroups[0].Documents) != 3 {
		t.Fatalf("exact groups not largest first: %+v", resp.ExactGroups)
	}
	if len(resp.SimilarGroups) != 2 || resp.SimilarGroups[0].Score < resp.SimilarGroups[1].Score {
		t.Fatalf("similar groups not most similar first: %+v", resp.SimilarGroups)
	}
	for _, d := range resp.ExactGroups[0].Documents {
		if d.Lang != "en" || d.Key == "" {
			t.Errorf("exact group document not enriched: %+v", d)
		}
	}

	resp, err = s.findDuplicates(FindDuplicatesRequest{Collection: "dups", Mode: "exact", Threshold: 0.9, MaxDocs: 2})
	if err != nil || resp.TotalEmbedded != 2 {
		t.Fatalf("maxDocs 2: %+v, %v", resp, err)
	}
}

func TestFindSimilarDuplicatesDefaultsTheMetric(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	docs := []docVec{{docID: "x", vector: []float32{1, 0}}, {docID: "y", vector: []float32{1, 0}}}
	if groups := s.findSimilarDuplicates("blog", docs, 0.9, nil, false); len(groups) != 1 {
		t.Fatalf("nil metric: %+v", groups)
	}
}

func TestEnrichDuplicateGroupsSkipsMissingAndUnreadableDocuments(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	good := addTestDoc(t, s, "blog", "good", "pl", "x", nil)
	if err := s.DB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put(storage.DocKey("blog", "broken"), []byte("not a document"))
	}); err != nil {
		t.Fatal(err)
	}
	groups := []DuplicateGroup{{Documents: []DuplicateDocInfo{{DocID: good.ID}, {DocID: "missing"}, {DocID: "broken"}}}}
	s.enrichDuplicateGroups("blog", groups, true)
	docs := groups[0].Documents
	if docs[0].Key != "good" || docs[0].Lang != "pl" || docs[0].ContentMD != "x" {
		t.Errorf("readable document: %+v", docs[0])
	}
	if docs[1].Key != "" || docs[2].Key != "" {
		t.Errorf("missing or unreadable documents were filled: %+v", docs[1:])
	}
}

func TestUnionFindAttachesTheShallowerTree(t *testing.T) {
	uf := newUnionFind(3)
	uf.union(0, 1) // 0 becomes a rank-1 root
	uf.union(2, 0) // 2 has rank 0: it joins 0, not the other way round
	if uf.find(2) != 0 || uf.find(1) != 0 {
		t.Fatalf("parents: %v", uf.parent)
	}
}

// A minhash scan that cannot read the documents reports it rather than
// answering with no groups.
func TestFindDuplicatesMinHashReportsAReadFailure(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	other, err := bolt.Open(t.TempDir()+"/vectors.db", 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	s.VectorStore = vector.NewVectorStore(other)
	if err := s.VectorStore.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	_ = s.DB.Close()

	if _, err := s.findDuplicates(FindDuplicatesRequest{Collection: "blog", Mode: "minhash", Threshold: 0.7, MaxDocs: 10}); err == nil {
		t.Fatal("a closed document store answered a minhash scan")
	}
}
