package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"mddb/internal/storage"
	pb "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// #335: a collection with full-text indexing off gets no postings, and an
// fts-reindex removes the ones it had.

func ftsHits(t *testing.T, s *Server, collection, q string) int {
	t.Helper()
	resp, err := s.searchFTS(FTSSearchRequest{Collection: collection, Query: q}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return resp.Total
}

func TestCollectionWithFTSOffGetsNoPostings(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	off := false
	_ = s.CollectionManager.Set("vectors-only", &CollectionConfig{Type: "default", FTS: &CollectionFTSDef{Enabled: &off}})

	addTestDoc(t, s, "vectors-only", "a", "en", "zebra crossing", nil)
	addTestDoc(t, s, "normal", "a", "en", "zebra crossing", nil)
	rec := doRequest(t, s.handleAddBatch, map[string]any{"collection": "vectors-only",
		"documents": []map[string]any{{"key": "b", "lang": "en", "contentMd": "zebra stripes"}}})
	if rec.Code >= 300 {
		t.Fatalf("batch: %d %s", rec.Code, rec.Body.String())
	}
	if n := ftsHits(t, s, "vectors-only", "zebra"); n != 0 {
		t.Errorf("collection with FTS off has %d hits", n)
	}
	if n := ftsHits(t, s, "normal", "zebra"); n != 1 {
		t.Errorf("normal collection: %d hits", n)
	}
	if !s.ftsEnabledFor("normal") || s.ftsEnabledFor("vectors-only") || !(&Server{}).ftsEnabledFor("x") {
		t.Error("ftsEnabledFor")
	}
}

func TestReindexRemovesPostingsOfADisabledCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "c", "a", "en", "quokka island", nil)
	addTestDoc(t, s, "c", "b", "en", "quokka smile", nil)
	if n := ftsHits(t, s, "c", "quokka"); n != 2 {
		t.Fatalf("before: %d", n)
	}

	res, err := s.reindexCollectionFTS("c")
	if err != nil || res.Status != "ok" || res.Reindexed != 2 {
		t.Fatalf("enabled reindex: %+v %v", res, err)
	}

	// Off over REST, then reindex over HTTP: the postings go.
	rec := doRequest(t, s.handleCollectionConfigSet, map[string]any{"collection": "c", "fts": map[string]any{"enabled": false}})
	if rec.Code != http.StatusOK {
		t.Fatalf("config: %d %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, s.handleFTSReindex, map[string]any{"collection": "c"})
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"removed"`) {
		t.Fatalf("reindex: %d %s", rec.Code, rec.Body.String())
	}
	if n := ftsHits(t, s, "c", "quokka"); n != 0 {
		t.Errorf("postings left after reindex of a disabled collection: %d", n)
	}
}

func TestFTSSettingTravelsEveryTransport(t *testing.T) {
	gs, s, cleanup := newTestGRPCServerFull(t)
	defer cleanup()
	off := false
	if _, err := gs.SetCollectionConfig(context.Background(), &pb.SetCollectionConfigRequest{
		Collection: "g", Fts: &pb.CollectionFTSProto{Enabled: &off},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := gs.GetCollectionConfig(context.Background(), &pb.GetCollectionConfigRequest{Collection: "g"})
	if got.GetConfig().GetFts() == nil || got.GetConfig().GetFts().GetEnabled() {
		t.Errorf("gRPC round trip: %v", got.GetConfig().GetFts())
	}
	if ftsToProto(nil) != nil {
		t.Error("nil stays nil")
	}

	ts := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}
	if _, err := ts.toolSetCollectionConfig(context.Background(), map[string]interface{}{
		"collection": "m", "fts": map[string]interface{}{"enabled": false},
	}); err != nil {
		t.Fatal(err)
	}
	if s.ftsEnabledFor("m") {
		t.Error("MCP did not turn FTS off")
	}
	resp, err := NewDirectClient(s).FTSReindex(context.Background(), &MCPFTSReindexRequest{Collection: "m"})
	if err != nil || resp.Status != "removed" {
		t.Errorf("MCP reindex: %+v %v", resp, err)
	}
	g, err := gs.FTSReindex(context.Background(), &pb.FTSReindexRequest{Collection: "g"})
	if err != nil || g.GetStatus() != "removed" {
		t.Errorf("gRPC reindex: %v %v", g, err)
	}
}

// Undecodable documents are skipped; an empty one is rebuilt like the rest
// (GO-048: its meta fields are indexed). A closed database is an error.
func TestReindexFTSSkipsAndFails(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "single") // writes and closes the main file directly
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "c", "a", "en", "wombat burrow", nil)
	addTestDoc(t, s, "c", "empty", "en", "", nil)
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(s.BucketNames.Docs).Put([]byte("doc|c|broken"), []byte("not a document"))
	})
	res, err := s.reindexCollectionFTS("c")
	if err != nil || res.Reindexed != 2 || res.Skipped != 1 {
		t.Errorf("reindex: %+v %v", res, err)
	}
	_ = s.DB.Close()
	if _, err := s.reindexCollectionFTS("c"); err == nil {
		t.Error("closed database")
	}
}

// GO-048: a reindex rebuilds an emptied document (its meta fields) and skips
// an expired one; without a full-text index there is nothing to write.
func TestReindexFTSEmptiedAndExpired(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "c", "empty", "en", "", map[string][]string{"title": {"zenith"}})
	expired := storage.Doc{ID: genID("c", "gone", "en"), Key: "gone", Lang: "en", ContentMD: "wombat", ExpiresAt: 1}
	buf, err := marshalAndEncrypt(&expired, "c")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DBUpdateC("c", func(tx *bolt.Tx) error {
		return tx.Bucket(s.BucketNames.Docs).Put(storage.DocKey("c", expired.ID), buf)
	}); err != nil {
		t.Fatal(err)
	}
	res, err := s.reindexCollectionFTS("c")
	if err != nil || res.Reindexed != 1 || res.Skipped != 1 {
		t.Fatalf("reindex: %+v %v", res, err)
	}
	if titleHits(t, s, "meta.title", "zenith") != 1 || ftsHits(t, s, "c", "wombat") != 0 {
		t.Error("the emptied document lost its title, or the expired one was indexed")
	}
	if err := (&Server{}).indexDocumentFTS("c", &expired, false); err != nil {
		t.Errorf("no index: %v", err)
	}
}
