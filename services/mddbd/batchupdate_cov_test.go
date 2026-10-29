package main

import (
	"context"
	"testing"

	"mddb/internal/cache"
	"mddb/internal/indexqueue"
	"mddb/internal/storage"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// covHUpdateDoc builds one batch update.
func covHUpdateDoc(key, content string, meta map[string]string, rev bool) *proto.UpdateDocument {
	d := &proto.UpdateDocument{Key: key, Lang: "en", ContentMd: content, SaveRevision: rev}
	if meta != nil {
		d.Meta = map[string]*proto.MetaValues{}
		for k, v := range meta {
			d.Meta[k] = &proto.MetaValues{Values: []string{v}}
		}
	}
	return d
}

func TestCovHBatchUpdateFailures(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "c", "ok", "en", "old", map[string][]string{"tag": {"a"}})
	addTestDoc(t, s, "c", "utf", "en", "old", nil)
	revKey := covHRevisionKeyOverflow("c", "en")
	if _, _, err := s.addDocument("c", revKey, "en", nil, "old", 0, false); err != nil {
		t.Fatal(err)
	}
	_ = s.DBUpdateC("c", func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put(storage.DocKey("c", genID("c", "corrupt", "en")), []byte{0x01})
	})

	resp, err := NewBatchUpdater(s, 2).ProcessBatchUpdate(context.Background(), "c", []*proto.UpdateDocument{
		covHUpdateDoc("ok", "new", nil, true),
		covHUpdateDoc("", "x", nil, false),
		covHUpdateDoc("missing", "x", nil, false),
		covHUpdateDoc("corrupt", "x", nil, false),
		covHUpdateDoc("utf", "bad \xff", nil, false),
		covHUpdateDoc(revKey, "new", nil, true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Updated != 1 || resp.NotFound != 1 || resp.Failed != 4 {
		t.Fatalf("response: updated=%d notFound=%d failed=%d", resp.Updated, resp.NotFound, resp.Failed)
	}
	for _, want := range []string{"missing key or lang", "corrupt/en: ", "utf/en: ", "revision error"} {
		if !covHErrorsMention(resp.Errors, want) {
			t.Errorf("no error mentioning %q", want)
		}
	}

	// The good update kept its metadata (omitted means unchanged), wrote a
	// revision and refreshed the read cache.
	got, err := s.loadDocByRef("c", "ok", "en")
	if err != nil || got.ContentMD != "new" || got.Meta["tag"][0] != "a" {
		t.Fatalf("updated document: %v %+v", err, got)
	}
	if n := covHRevCount(t, s, "c", got.ID); n != 2 {
		t.Fatalf("revisions: %d, want 2", n)
	}
	if _, hit := s.Cache.Get(cache.BuildCacheKey("c", "ok", "en")); !hit {
		t.Fatal("read cache not refreshed")
	}
	if cur, _ := s.loadDocByRef("c", "utf", "en"); cur.ContentMD != "old" {
		t.Fatal("a refused update changed the document")
	}
}

func TestCovHBatchUpdateIndexQueueRefusal(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "c", "a", "en", "x", map[string][]string{"tag": {"old"}})
	s.IndexQueue = indexqueue.NewIndexQueue(serverIndexStore{s: s}, 1)
	s.IndexQueue.Shutdown()

	resp, err := NewBatchUpdater(s, 1).ProcessBatchUpdate(context.Background(), "c", []*proto.UpdateDocument{
		covHUpdateDoc("a", "", map[string]string{"tag": "new"}, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	// The document is written; the index failure is surfaced, not swallowed.
	if resp.Updated != 1 || !covHErrorsMention(resp.Errors, genID("c", "a", "en"), "meta index error") {
		t.Fatalf("response: %+v", resp)
	}
	if got, _ := s.loadDocByRef("c", "a", "en"); got.Meta["tag"][0] != "new" || got.ContentMD != "x" {
		t.Fatalf("stored: %+v", got)
	}
}

func TestCovHBatchUpdateClosedDatabase(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "c", "a", "en", "x", nil)
	bu := NewBatchUpdater(s, 1)
	updated := bu.parallelProcess(context.Background(), "c", []*proto.UpdateDocument{covHUpdateDoc("a", "y", nil, false)}, 1)
	closeStorage(t, s, "c")

	resp := bu.commitUpdate("c", updated, 1)
	if resp.Updated != 0 || resp.Failed != 1 || !covHErrorsMention(resp.Errors, "transaction error") {
		t.Fatalf("closed database: %+v", resp)
	}
}
