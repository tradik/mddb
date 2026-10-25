package boltx

import (
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func TestEnsureBuckets(t *testing.T) {
	db, err := bolt.Open(filepath.Join(t.TempDir(), "b.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := EnsureBuckets(db, []byte("a"), []byte("b")); err != nil {
		t.Fatal(err)
	}
	txid := func() int { var id int; _ = db.View(func(tx *bolt.Tx) error { id = tx.ID(); return nil }); return id }
	before := txid()
	if err := EnsureBuckets(db, []byte("a"), []byte("b")); err != nil {
		t.Fatal(err)
	}
	if txid() != before {
		t.Error("buckets that exist were written again")
	}
	if err := EnsureBuckets(db, []byte("a"), []byte("c")); err != nil || txid() == before {
		t.Errorf("a missing bucket was not created: %v", err)
	}
	if err := EnsureBuckets(db, []byte("")); err == nil {
		t.Error("an invalid bucket name")
	}
	_ = db.Close()
	if err := EnsureBuckets(db, []byte("a")); err == nil {
		t.Error("closed database")
	}
}
