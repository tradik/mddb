package main

import (
	"context"
	"strings"
	"testing"

	"mddb/internal/storage"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// covHBatchDoc builds one batch document.
func covHBatchDoc(key, content string, meta map[string]string, rev bool) *proto.BatchDocument {
	d := &proto.BatchDocument{Key: key, Lang: "en", ContentMd: content, SaveRevision: rev}
	if meta != nil {
		d.Meta = map[string]*proto.MetaValues{}
		for k, v := range meta {
			d.Meta[k] = &proto.MetaValues{Values: []string{v}}
		}
	}
	return d
}

// covHErrorsMention reports whether one of errs contains every part.
func covHErrorsMention(errs []string, parts ...string) bool {
	for _, e := range errs {
		all := true
		for _, p := range parts {
			all = all && strings.Contains(e, p)
		}
		if all {
			return true
		}
	}
	return false
}

func TestCovHBatchCancelledContext(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Used to leave nil slots that commitBatch dereferenced.
	resp, err := NewBatchProcessor(s, 2).ProcessBatch(ctx, "c", []*proto.BatchDocument{
		covHBatchDoc("a", "x", nil, false), covHBatchDoc("b", "y", nil, false), covHBatchDoc("c", "z", nil, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Failed != 3 || resp.Added != 0 || !covHErrorsMention(resp.Errors, "b: ", "context canceled") {
		t.Fatalf("cancelled batch: %+v", resp)
	}
	if n := covHDocCount(t, s, "c"); n != 0 {
		t.Fatalf("a cancelled batch stored %d documents", n)
	}
}

func TestCovHBatchPerDocumentFailures(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := s.SchemaManager.Set("c", `{"required":["author"]}`); err != nil {
		t.Fatal(err)
	}
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put(storage.DocKey("c", genID("c", "corrupt", "en")), []byte{0x01})
	})
	author := map[string]string{"author": "ann"}

	resp, err := NewBatchProcessor(s, 3).ProcessBatch(context.Background(), "c", []*proto.BatchDocument{
		covHBatchDoc("ok", "fine", author, true),
		covHBatchDoc("noschema", "x", nil, false),
		covHBatchDoc("corrupt", "x", author, false),
		covHBatchDoc("utf", "bad \xff", author, false),
		covHBatchDoc(strings.Repeat("k", bolt.MaxKeySize), "x", author, false),
		covHBatchDoc("bigmeta", "x", map[string]string{"author": strings.Repeat("v", bolt.MaxKeySize)}, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Each failing document is counted once, and only the good one is added.
	// An index failure used to count as both failed and added.
	if resp.Added != 1 || resp.Failed != 5 || len(resp.Errors) != 5 {
		t.Fatalf("response: added=%d failed=%d errors=%d", resp.Added, resp.Failed, len(resp.Errors))
	}
	for _, want := range [][]string{
		{"noschema: "}, {"corrupt: "}, {"utf: "}, {"put error"}, {"bigmeta: ", "index error"},
	} {
		if !covHErrorsMention(resp.Errors, want...) {
			t.Errorf("no error mentioning %q", want)
		}
	}

	// A key that fits the document bucket but not the revision history is
	// reported as failed.
	resp, err = NewBatchProcessor(s, 1).ProcessBatch(context.Background(), "r", []*proto.BatchDocument{
		covHBatchDoc(covHRevisionKeyOverflow("r", "en"), "x", nil, true),
	})
	if err != nil || resp.Added != 0 || resp.Failed != 1 || !covHErrorsMention(resp.Errors, "revision error") {
		t.Fatalf("revision overflow: %v added=%d failed=%d", err, resp.Added, resp.Failed)
	}
	got, err := s.loadDocByRef("c", "ok", "en")
	if err != nil || got.ContentMD != "fine" {
		t.Fatalf("good document: %v %+v", err, got)
	}
	if n := covHRevCount(t, s, "c", got.ID); n != 1 {
		t.Fatalf("revisions of the good document: %d", n)
	}
}

func TestCovHBatchRevisionRetention(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := s.CollectionManager.Set("c", &CollectionConfig{MaxRevisions: 2}); err != nil {
		t.Fatal(err)
	}
	bp := NewBatchProcessor(s, 1)
	for i := range 4 {
		resp, err := bp.ProcessBatch(context.Background(), "c", []*proto.BatchDocument{
			covHBatchDoc("a", "v"+itoa(i), map[string]string{"tag": "t" + itoa(i)}, true),
		})
		if err != nil || resp.Failed != 0 {
			t.Fatalf("batch %d: %v %+v", i, err, resp)
		}
		if i > 0 && resp.Updated != 1 {
			t.Fatalf("batch %d must update: %+v", i, resp)
		}
	}
	id := genID("c", "a", "en")
	if n := covHRevCount(t, s, "c", id); n != 2 {
		t.Fatalf("revisions kept: %d, want 2", n)
	}
	_ = s.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("idxmeta"))
		if b.Get(append(storage.MetaKeyPrefix("c", "tag", "t3"), id...)) == nil || b.Get(append(storage.MetaKeyPrefix("c", "tag", "t2"), id...)) != nil {
			t.Error("the batch did not replace the metadata index")
		}
		return nil
	})
}

func TestCovHBatchClosedDatabase(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	bp := NewBatchProcessor(s, 1)
	processed := bp.parallelProcess(context.Background(), "c", []*proto.BatchDocument{covHBatchDoc("a", "x", nil, false)}, 1)
	_ = s.DB.Close()

	resp, committed := bp.commitBatch("c", processed, 1)
	if committed != nil || resp.Failed != 1 || !covHErrorsMention(resp.Errors, "transaction error") {
		t.Fatalf("closed database: %+v %v", resp, committed)
	}
}
