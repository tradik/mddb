package main

import (
	"errors"
	"path/filepath"
	"testing"

	"mddb/internal/boltx"

	bolt "go.etcd.io/bbolt"
)

// ListDocs walks one collection's documents and nothing past them, and
// DeleteByKey removes the key index entry.
func TestBoltBackendListsAndDeletesByKey(t *testing.T) {
	srv, cleanup := backendServer(t)
	defer cleanup()
	b := NewBoltBackend(srv.DB, srv.BucketNames)

	for _, w := range []struct{ coll, id string }{{"docs", "a"}, {"docs", "b"}, {"docsx", "c"}} {
		if err := b.PutDoc(w.coll, w.id, []byte(w.id)); err != nil {
			t.Fatal(err)
		}
	}
	var listed []string
	if err := b.ListDocs("docs", func(id string, data []byte) error {
		if id != string(data) {
			t.Errorf("document %s came with %q", id, data)
		}
		listed = append(listed, id)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0] != "a" || listed[1] != "b" {
		t.Errorf("listed %v, want [a b]", listed)
	}

	// The callback's error stops the walk and reaches the caller.
	stop := errors.New("stop")
	calls := 0
	if err := b.ListDocs("docs", func(string, []byte) error { calls++; return stop }); !errors.Is(err, stop) || calls != 1 {
		t.Errorf("ListDocs = %v after %d calls, want stop after one", err, calls)
	}

	if err := b.PutByKey("docs", "k", "en", "a"); err != nil {
		t.Fatal(err)
	}
	if err := b.DeleteByKey("docs", "k", "en"); err != nil {
		t.Fatal(err)
	}
	if id, err := b.GetByKey("docs", "k", "en"); err != nil || id != "" {
		t.Errorf("GetByKey after delete = %q, %v", id, err)
	}
	if err := b.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
}

// A file without the buckets answers every call with the missing-bucket
// error, not a panic. The router (#336, #359) sends the calls there.
func TestBoltBackendWithoutBucketsThroughARouter(t *testing.T) {
	srv, cleanup := backendServer(t)
	defer cleanup()

	empty, err := bolt.Open(filepath.Join(t.TempDir(), "empty.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = empty.Close() }()

	b := NewBoltBackend(srv.DB, srv.BucketNames)
	b.SetRouter(boltx.Single{DB: empty})

	docCalls := map[string]error{
		"PutDoc":    b.PutDoc("docs", "a", nil),
		"DeleteDoc": b.DeleteDoc("docs", "a"),
		"ListDocs":  b.ListDocs("docs", func(string, []byte) error { return nil }),
	}
	_, docCalls["GetDoc"] = b.GetDoc("docs", "a")
	for name, err := range docCalls {
		if !errors.Is(err, errDocsBucketMissing) {
			t.Errorf("%s = %v, want the missing docs bucket", name, err)
		}
	}

	keyCalls := map[string]error{
		"PutByKey":    b.PutByKey("docs", "k", "en", "a"),
		"DeleteByKey": b.DeleteByKey("docs", "k", "en"),
	}
	_, keyCalls["GetByKey"] = b.GetByKey("docs", "k", "en")
	for name, err := range keyCalls {
		if !errors.Is(err, errByKeyBucketMissing) {
			t.Errorf("%s = %v, want the missing byKey bucket", name, err)
		}
	}
}
