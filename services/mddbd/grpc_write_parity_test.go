package main

import (
	"context"
	"testing"

	"mddb/internal/storage"
	pb "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// GO-001 parity tests: a document written over gRPC must be indistinguishable
// from one written over HTTP — it must be FTS-indexed, meta-indexed, and (when
// requested) revisioned. Before the single-write-path refactor, gRPC Add/AddBatch
// re-implemented the BoltDB insert and skipped FTS/geo/webhooks/revisions, so
// gRPC-written docs were invisible to search.

func ftsHasDoc(t *testing.T, s *Server, collection, query, wantDocID string) bool {
	t.Helper()
	results, err := s.FTSIndex.Search(collection, query, 10)
	if err != nil {
		t.Fatalf("FTS Search(%q): %v", query, err)
	}
	for _, r := range results {
		if r.DocID == wantDocID {
			return true
		}
	}
	return false
}

// TestGRPCAdd_FTSParity — gRPC Add must FTS-index content immediately.
func TestGRPCAdd_FTSParity(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	addDocViaGRPC(t, gs, "blog", "fts-doc", "en", "the quick brown fox", nil)

	docID := genID("blog", "fts-doc", "en")
	if !ftsHasDoc(t, s, "blog", "brown", docID) {
		t.Fatal("gRPC-added doc is not findable via FTS — write paths diverge (GO-001)")
	}
}

// TestGRPCAdd_MetaIndexParity — gRPC Add must index metadata synchronously, in
// the write transaction, not via the lossy async IndexQueue.
func TestGRPCAdd_MetaIndexParity(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	addDocViaGRPC(t, gs, "blog", "meta-doc", "en", "body", map[string]*pb.MetaValues{
		"author": {Values: []string{"alice"}},
	})

	docID := genID("blog", "meta-doc", "en")
	found := false
	err := s.DBViewC("blog", func(tx *bolt.Tx) error {
		bIdx := tx.Bucket(s.BucketNames.IdxMeta)
		mkey := append(storage.MetaKeyPrefix("blog", "author", "alice"), []byte(docID)...)
		found = bIdx.Get(mkey) != nil
		return nil
	})
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if !found {
		t.Error("gRPC-added doc has no synchronous meta index entry (GO-001)")
	}
}

// TestGRPCAdd_RevisionFlagHonored — the SaveRevision flag must still be
// respected after routing through the shared addDocument path.
func TestGRPCAdd_RevisionFlagHonored(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()
	ctx := context.Background()

	// SaveRevision=false → no revision recorded.
	if _, err := gs.Add(ctx, &pb.AddRequest{
		Collection: "blog", Key: "norev", Lang: "en", ContentMd: "x", SaveRevision: false,
	}); err != nil {
		t.Fatal(err)
	}
	revs, err := gs.ListRevisions(ctx, &pb.ListRevisionsRequest{Collection: "blog", Key: "norev", Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if revs.Total != 0 {
		t.Errorf("SaveRevision=false must record no revision, got %d", revs.Total)
	}

	// SaveRevision=true → revision recorded.
	if _, err := gs.Add(ctx, &pb.AddRequest{
		Collection: "blog", Key: "yesrev", Lang: "en", ContentMd: "x", SaveRevision: true,
	}); err != nil {
		t.Fatal(err)
	}
	revs, err = gs.ListRevisions(ctx, &pb.ListRevisionsRequest{Collection: "blog", Key: "yesrev", Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if revs.Total == 0 {
		t.Error("SaveRevision=true must record a revision")
	}
}

// TestGRPCAddBatch_FTSParity — batch writes over gRPC must FTS-index every doc
// (both the standard and extreme processors run the shared post-write hooks).
func TestGRPCAddBatch_FTSParity(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()

	if _, err := gs.AddBatch(context.Background(), &pb.AddBatchRequest{
		Collection: "blog",
		Documents: []*pb.BatchDocument{
			{Key: "b1", Lang: "en", ContentMd: "alpha beta gamma"},
			{Key: "b2", Lang: "en", ContentMd: "delta epsilon zeta"},
		},
	}); err != nil {
		t.Fatalf("AddBatch: %v", err)
	}

	if !ftsHasDoc(t, s, "blog", "beta", genID("blog", "b1", "en")) {
		t.Error("batch doc b1 not FTS-indexed (GO-001)")
	}
	if !ftsHasDoc(t, s, "blog", "epsilon", genID("blog", "b2", "en")) {
		t.Error("batch doc b2 not FTS-indexed (GO-001)")
	}
}
