package main

import (
	"context"
	"strings"
	"testing"

	"mddb/internal/storage"
	"mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// The extreme-mode batch processor: the same outcomes as the standard one for
// metadata, revisions, schema, cancellation and a failed transaction.

func TestFinalBatchReplacesMetadataAndTrimsRevisions(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := s.CollectionManager.Set("c", &CollectionConfig{MaxRevisions: 1}); err != nil {
		t.Fatal(err)
	}
	fbp := NewFinalBatchProcessor(s, 2)
	for i, tag := range []string{"t1", "t2"} {
		resp, err := fbp.ProcessBatch(context.Background(), "c", []*proto.BatchDocument{
			covHBatchDoc("a", "body "+tag, map[string]string{"tag": tag}, true),
		})
		if err != nil || resp.Failed != 0 {
			t.Fatalf("batch %d: %v %+v", i, err, resp)
		}
	}
	id := genID("c", "a", "en")
	if n := covHRevCount(t, s, "c", id); n != 1 {
		t.Fatalf("revisions kept: %d, want 1", n)
	}
	_ = s.DBViewC("c", func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("idxmeta"))
		if b.Get(append(storage.MetaKeyPrefix("c", "tag", "t2"), id...)) == nil || b.Get(append(storage.MetaKeyPrefix("c", "tag", "t1"), id...)) != nil {
			t.Error("the batch did not replace the metadata index")
		}
		return nil
	})
}

func TestFinalBatchSchemaAndCancellation(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := s.SchemaManager.Set("c", `{"required":["title"]}`); err != nil {
		t.Fatal(err)
	}
	fbp := NewFinalBatchProcessor(s, 1)
	resp, err := fbp.ProcessBatch(context.Background(), "c", []*proto.BatchDocument{covHBatchDoc("a", "x", nil, false)})
	if err != nil || resp.Failed != 1 || !covHErrorsMention(resp.Errors, "title") {
		t.Fatalf("schema: %v %+v", err, resp)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resp, err = fbp.ProcessBatch(ctx, "other", []*proto.BatchDocument{covHBatchDoc("a", "x", nil, false), covHBatchDoc("b", "y", nil, false)})
	if err != nil || resp.Failed != 2 || !covHErrorsMention(resp.Errors, "context canceled") {
		t.Fatalf("cancelled: %v %+v", err, resp)
	}
}

func TestFinalBatchClosedDatabase(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	fbp := NewFinalBatchProcessor(s, 1)
	docs := []*proto.BatchDocument{covHBatchDoc("a", "x", nil, false)}
	processed := fbp.parallelMarshal(context.Background(), "c", docs, map[string][]byte{}, 1)
	closeStorage(t, s, "c")
	resp, committed, err := fbp.commitBatch("c", processed, 1)
	if err != nil || committed != nil || resp.Failed != 1 || !covHErrorsMention(resp.Errors, "transaction error") {
		t.Fatalf("closed database: %v %+v %v", err, resp, committed)
	}
}

func TestFinalBatchKeyTooLongForTheIndex(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	fbp := NewFinalBatchProcessor(s, 1)
	// bbolt refuses keys over 32 KiB; the key index is keyed by the document key.
	resp, err := fbp.ProcessBatch(context.Background(), "c", []*proto.BatchDocument{covHBatchDoc(strings.Repeat("k", 40000), "x", nil, false)})
	if err != nil || resp.Failed != 1 || resp.Added != 0 {
		t.Fatalf("over-long key: %v %+v", err, resp)
	}
}

func TestProcessBatchWithDocsExtremeReportsAllFailed(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.UseExtreme, s.finalBatchProcessor = true, NewFinalBatchProcessor(s, 1)
	_, _, err := s.processBatchWithDocs(context.Background(), "c", []*proto.BatchDocument{{Key: "", Lang: "en"}})
	if err == nil || !strings.Contains(err.Error(), "all documents failed") {
		t.Fatalf("err = %v", err)
	}
}
