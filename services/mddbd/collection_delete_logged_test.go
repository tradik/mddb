package main

import (
	"path/filepath"
	"testing"

	"mddb/internal/binlog"

	bolt "go.etcd.io/bbolt"
)

// A collection delete that fails part-way logs nothing (GO-051): its entries
// are written by the transaction, which rolls back. A key under the
// collection's image prefix that is a nested bucket cannot be deleted as a
// key, which is the failure here.
func TestAFailedCollectionDeleteLogsNothing(t *testing.T) {
	s := newHandlerTestServerAt(t, filepath.Join(t.TempDir(), "d.db"))
	bl, err := binlog.NewBinlog(s.Path, binlog.BinlogConfig{RetentionInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()
	s.Binlog = bl
	if _, _, err := s.addDocument("gone", "a", "en", nil, "text", 0, false); err != nil {
		t.Fatal(err)
	}
	if err := s.DBUpdateC("gone", func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucketImages)
		if err == nil {
			_, err = b.CreateBucket(imageBytesKey("gone", "nested"))
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before := bl.CurrentLSN()
	if _, err := s.deleteCollection("gone"); err == nil {
		t.Fatal("the delete went through a nested bucket")
	}
	if bl.CurrentLSN() != before {
		t.Fatalf("a failed delete logged %d entries", bl.CurrentLSN()-before)
	}
}
