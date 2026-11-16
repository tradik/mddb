package main

import (
	"errors"
	"path/filepath"
	"testing"

	"mddb/internal/binlog"

	bolt "go.etcd.io/bbolt"
)

// Curation rules are logged writes (GO-051): their entry is in the
// transaction, refused with it when the node may not write.
func TestCurationWritesAreLoggedInTheirTransaction(t *testing.T) {
	dir := t.TempDir()
	db, err := bolt.Open(filepath.Join(dir, "c.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	bl, err := binlog.NewBinlog(filepath.Join(dir, "c.db"), binlog.BinlogConfig{RetentionInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()

	cm := NewCurationManager(db)
	cm.SetBinlog(bl)
	// Without the bucket a rule cannot be stored, and deleting is a no-op.
	if err := cm.Set(&CurationRule{Collection: "c", Query: "q"}); err == nil {
		t.Fatal("stored without a bucket")
	}
	if err := cm.Delete("nothing"); err != nil {
		t.Fatal(err)
	}
	if err := cm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	rule := &CurationRule{Collection: "c", Query: "q"}
	if err := cm.Set(rule); err != nil {
		t.Fatal(err)
	}
	if bl.CurrentLSN() != 2 { // the delete of nothing, as before, and the rule
		t.Fatalf("logged %d entries", bl.CurrentLSN())
	}
	// A corrupt record is skipped on load.
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketCuration).Put([]byte("bad"), []byte("{"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := cm.LoadAll(); err != nil {
		t.Fatal(err)
	}

	gate := errors.New("not the leader")
	bl.SetWriteGate(func() error { return gate })
	if err := cm.Set(&CurationRule{Collection: "c", Query: "x"}); !errors.Is(err, gate) {
		t.Fatalf("gated set: %v", err)
	}
	if err := cm.Delete(rule.ID); !errors.Is(err, gate) {
		t.Fatalf("gated delete: %v", err)
	}
	bl.SetWriteGate(nil)

	cm.marshaler = func(any) ([]byte, error) { return nil, errors.New("cannot encode") }
	if err := cm.Set(&CurationRule{Collection: "c", Query: "y"}); err == nil {
		t.Fatal("stored what could not be encoded")
	}
}
