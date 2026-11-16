package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	vec "mddb/internal/vector"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The branches of the files GO-053 touched that no test reached: failures
// under the search slot, and two corners of the direct client's vector search.

// An evaluation's hybrid configuration that the full-text half refuses is
// reported on that configuration; the run itself answers.
func TestEvalHybridConfigReportsFTSFailure(t *testing.T) {
	s, cleanup := limitedGold(t, 1, "2000")
	defer cleanup()
	badK1 := -1.0
	res, err := s.RunEval(context.Background(), EvalRequest{Collection: "gold",
		Configs: []EvalConfig{{Name: "bad", SearchType: SearchTypeHybrid, BM25K1: &badK1}}})
	if err != nil || len(res.Results) != 1 || res.Results[0].Error == "" {
		t.Fatalf("run: %v %+v", err, res)
	}
	if inUse, _, rejected, _ := s.SearchLimiter.Stats(); inUse != 0 || rejected != 0 {
		t.Fatalf("inUse %d, rejected %d after a failed configuration", inUse, rejected)
	}
}

// Deleting an evaluation query that cannot be stored is Internal over gRPC.
func TestGRPCDeleteEvalQueryStorageFailure(t *testing.T) {
	s, cleanup := limitedGold(t, 1, "2000")
	defer cleanup()
	set := s.evalSet("gold")
	if len(set) != 1 {
		t.Fatalf("evaluation set: %+v", set)
	}
	closeStorage(t, s, "gold")
	_, err := (&GRPCServer{server: s}).DeleteEvalQuery(context.Background(),
		&proto.DeleteEvalQueryRequest{Collection: "gold", Id: set[0].ID})
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v (%v), want Internal", status.Code(err), err)
	}
}

// A page without front matter is stored with only the request's metadata.
// Loopback is opened for this test only; the SSRF guard refuses it by default.
func TestDirectImportURLWithoutFrontMatter(t *testing.T) {
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "true")
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("plain body"))
	}))
	defer page.Close()
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	doc, err := NewDirectClient(s).ImportURL(context.Background(), &MCPImportURLRequest{Collection: "c", Key: "page",
		URL: page.URL + "/page", Lang: "en", Meta: map[string][]string{"tag": {"x"}}})
	if err != nil || doc.ContentMD != "plain body" || len(doc.Meta["tag"]) != 1 {
		t.Fatalf("import: %v %+v", err, doc)
	}
}

// A vector hit in a collection whose documents bucket is gone loads nothing
// rather than failing the search.
func TestVectorLoadItemsWithoutDocsBucket(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "c", "k", "en", "body", nil)
	db, err := s.collectionDBForWrite("c")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("docs")) }); err != nil {
		t.Fatal(err)
	}
	r := &vectorRun{s: s, req: &MCPVectorSearchRequest{Collection: "c"}}
	if items := r.loadItems(context.Background(), []vec.VectorResult{{DocID: "doc|c|k|en"}}); len(items) != 0 {
		t.Fatalf("items = %+v", items)
	}
}

// readLevel indexed+pending keeps the hits the index returned and does not
// repeat them among the pending documents.
func TestVectorSearchIndexedPlusPendingKeepsHits(t *testing.T) {
	s, cleanup := goldenServer(t)
	defer cleanup()
	resp, err := NewDirectClient(s).VectorSearch(context.Background(), &MCPVectorSearchRequest{Collection: "gold",
		Query: "pump water", IndexWait: IndexWait{ReadLevel: ReadLevelIndexedPlusPending}})
	if err != nil || len(resp.Results) == 0 {
		t.Fatalf("search: %v %+v", err, resp)
	}
	seen := map[string]bool{}
	for _, it := range resp.Results {
		if seen[it.Document.Key] {
			t.Fatalf("%s returned twice: %+v", it.Document.Key, resp.Results)
		}
		seen[it.Document.Key] = true
	}
	if !seen["manual"] {
		t.Fatalf("results: %v", seen)
	}
}
