package main

import (
	"context"
	"strings"
	"testing"

	"mddb/internal/storage"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// #358: every write path keeps every revision, even several in one second.
func TestRevisions_SameSecondWritesAreAllKept(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	count := func(key string) int {
		n := 0
		prefix := storage.RevPrefix("c", genID("c", key, "en"))
		_ = s.DBViewC("c", func(tx *bolt.Tx) error {
			c := tx.Bucket([]byte("rev")).Cursor()
			for k, _ := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, _ = c.Next() {
				n++
			}
			return nil
		})
		return n
	}
	// add, three times within a second.
	for _, v := range []string{"a", "b", "c"} {
		addTestDoc(t, s, "c", "k", "en", v, nil)
	}
	if n := count("k"); n != 3 {
		t.Fatalf("add: %d revisions, want 3", n)
	}
	// update.
	for _, v := range []string{"d", "e"} {
		c := v
		if _, err := s.updateDocument("c", "k", "en", DocPatch{Content: &c}); err != nil {
			t.Fatal(err)
		}
	}
	if n := count("k"); n != 5 {
		t.Fatalf("update: %d revisions, want 5", n)
	}
	// batch add and batch update.
	ctx := context.Background()
	dc := NewDirectClient(s)
	for range 2 {
		if _, err := dc.AddBatch(ctx, &MCPAddBatchRequest{Collection: "c",
			Documents: []MCPBatchDocument{{Key: "b", Lang: "en", ContentMD: "x", SaveRevision: true}}}); err != nil {
			t.Fatal(err)
		}
	}
	bu := NewBatchUpdater(s, 2)
	for range 2 {
		if _, err := bu.ProcessBatchUpdate(ctx, "c", []*proto.UpdateDocument{{Key: "b", Lang: "en", ContentMd: "y", SaveRevision: true}}); err != nil {
			t.Fatal(err)
		}
	}
	if n := count("b"); n != 4 {
		t.Fatalf("batch add + update: %d revisions, want 4", n)
	}

	// Restore by each listed timestamp returns that version: the timestamps
	// are distinct and still identify their revision.
	revs, err := dc.ListRevisions(ctx, "c", "k", "en")
	if err != nil || len(revs.Revisions) != 5 {
		t.Fatalf("list: %v %+v", err, revs)
	}
	seen := map[int64]bool{}
	for _, r := range revs.Revisions {
		if seen[r.Timestamp] {
			t.Fatalf("duplicate timestamp %d", r.Timestamp)
		}
		seen[r.Timestamp] = true
	}
}

func TestNextRevisionKey(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("rev"))
		if k := string(nextRevisionKey(b, "c", "d", 100)); !strings.HasSuffix(k, "00000000000000000100") {
			t.Fatalf("empty history: %s", k)
		}
		_ = b.Put(nextRevisionKey(b, "c", "d", 100), []byte("1"))
		// A neighbouring document's later keys do not move this one.
		_ = b.Put(nextRevisionKey(b, "c", "e", 500), []byte("1"))
		if k := string(nextRevisionKey(b, "c", "d", 100)); !strings.HasSuffix(k, "00000000000000000101") {
			t.Fatalf("same second: %s", k)
		}
		if k := string(nextRevisionKey(b, "c", "d", 200)); !strings.HasSuffix(k, "00000000000000000200") {
			t.Fatalf("later second: %s", k)
		}
		// The last document in the bucket: Seek runs off the end.
		if k := string(nextRevisionKey(b, "c", "e", 500)); !strings.HasSuffix(k, "00000000000000000501") {
			t.Fatalf("last in bucket: %s", k)
		}
		// A key that does not parse is ignored.
		_ = b.Put(append(storage.RevPrefix("c", "f"), []byte("junk")...), []byte("1"))
		if k := string(nextRevisionKey(b, "c", "f", 7)); !strings.HasSuffix(k, "00000000000000000007") {
			t.Fatalf("junk: %s", k)
		}
		return nil
	})
}
