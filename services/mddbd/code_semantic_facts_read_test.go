package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/binlog"
	json "mddb/internal/jsonx"
	"mddb/internal/storage"
	proto "mddb/proto"
)

// CODE-010 on the read side: results hide semantic facts, single reads and
// followers have them.

func TestSemanticFactsHiddenFromResults(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addFactsDoc(t, s, "svc/run.go")

	noFacts := func(surface string, meta map[string][]string) {
		t.Helper()
		if len(meta) == 0 {
			t.Errorf("%s: no meta at all — did the search find the document?", surface)
		}
		if got := symMeta(meta); len(got) != 0 {
			t.Errorf("%s: result carries %v", surface, got)
		}
	}

	w := callHandler(t, s.handleSearch, http.MethodPost, "/v1/search", map[string]any{"collection": factsCollection})
	var list []storage.Doc
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("search: %v %s", err, w.Body.String())
	}
	noFacts("REST search", list[0].Meta)

	w = callHandler(t, s.handleFTS, http.MethodPost, "/v1/fts", map[string]any{"collection": factsCollection, "query": "Run"})
	var fts FTSSearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &fts); err != nil || len(fts.Results) == 0 {
		t.Fatalf("fts: %v %s", err, w.Body.String())
	}
	noFacts("REST fts", fts.Results[0].Document.Meta)

	ctx := context.Background()
	mcp := NewDirectClient(s)
	if res, err := mcp.Search(ctx, &MCPSearchRequest{Collection: factsCollection, Limit: 10, IncludeContent: true}); err != nil || len(res.Documents) != 1 {
		t.Fatalf("MCP search: %v %+v", err, res)
	} else {
		noFacts("MCP search", res.Documents[0].Meta)
	}
	if res, err := mcp.FTSSearch(ctx, &MCPFTSSearchRequest{Collection: factsCollection, Query: "Run", Limit: 10}); err != nil || len(res.Results) == 0 {
		t.Fatalf("MCP fts: %v %+v", err, res)
	} else {
		noFacts("MCP fts", res.Results[0].Document.Meta)
	}

	g := &GRPCServer{server: s}
	if res, err := g.Search(ctx, &proto.SearchRequest{Collection: factsCollection, Limit: 10}); err != nil || len(res.Documents) != 1 {
		t.Fatalf("gRPC search: %v %+v", err, res)
	} else {
		noFacts("gRPC search", metaValuesFromProto(res.Documents[0].Meta))
	}

	// One document by key still has them, on every transport.
	wantAllFacts(t, getDocMeta(t, s, factsCollection, "svc/run.go"))
	if doc, err := mcp.Get(ctx, &MCPGetRequest{Collection: factsCollection, Key: "svc/run.go", Lang: "en"}); err != nil {
		t.Fatal(err)
	} else {
		wantAllFacts(t, doc.Meta)
	}
	if doc, err := g.Get(ctx, &proto.GetRequest{Collection: factsCollection, Key: "svc/run.go", Lang: "en"}); err != nil {
		t.Fatal(err)
	} else {
		wantAllFacts(t, metaValuesFromProto(doc.Meta))
	}
}

func TestCodeLanguagesAnnouncesSemanticLanguages(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	w := httptest.NewRecorder()
	s.handleCodeLanguages(w, httptest.NewRequest(http.MethodGet, "/v1/code-languages", nil))
	var resp CodeLanguagesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"go": true, "php": true, "python": true}
	if !reflect.DeepEqual(resp.Semantic, want) {
		t.Errorf("semantic = %v, want %v", resp.Semantic, want)
	}
	for lang := range resp.Semantic {
		found := false
		for _, l := range resp.Languages {
			found = found || l == lang
		}
		if !found {
			t.Errorf("%s is semantic but not a code language", lang)
		}
	}
}

func TestSemanticFactsReplicateLikeAnyMeta(t *testing.T) {
	// No replication code knows about facts: the document and its metadata
	// index entries travel through the binlog as bytes.
	leader, cleanup := newHandlerTestServer(t)
	defer cleanup()
	bl, err := binlog.NewBinlog(filepath.Join(t.TempDir(), "binlog.db"), binlog.BinlogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bl.Close() })
	leader.Binlog = bl
	addFactsDoc(t, leader, "svc/run.go")
	entries, err := bl.ReadFrom(0)
	if err != nil {
		t.Fatal(err)
	}

	follower, cleanupF := newTestServer(t)
	defer cleanupF()
	if err := NewReplicationApplier(follower).ApplyBatch(entries); err != nil {
		t.Fatal(err)
	}
	docID := genID(factsCollection, "svc/run.go", "en")
	var doc *storage.Doc
	var hits []metaValueHit
	_ = follower.DBViewC(factsCollection, func(tx *bolt.Tx) error {
		doc, _ = loadDoc(tx.Bucket(follower.BucketNames.Docs).Get(storage.DocKey(factsCollection, docID)))
		hits, _ = metaValuesWithPrefix(tx.Bucket(follower.BucketNames.IdxMeta), factsCollection, MetaKeySymRefs, "svc/log.go#Log ", 10)
		return nil
	})
	if doc == nil {
		t.Fatal("the follower has no document")
	}
	wantAllFacts(t, doc.Meta)
	if len(hits) != 1 || hits[0].DocID != docID {
		t.Errorf("follower index hits = %+v", hits)
	}
}
