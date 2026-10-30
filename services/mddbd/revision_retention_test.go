package main

import (
	"bytes"
	"errors"
	"fmt"
	"mddb/internal/storage"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

func newRevTestDB(t *testing.T) *bolt.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rev.db")
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("rev"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedRevs(t *testing.T, db *bolt.DB, coll, docID string, count int) {
	t.Helper()
	if err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("rev"))
		for i := 0; i < count; i++ {
			rkey := append(storage.RevPrefix(coll, docID), []byte(fmt.Sprintf("%020d", int64(1000+i)))...)
			if err := b.Put(rkey, []byte(fmt.Sprintf("v%d", i))); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func countRevs(t *testing.T, db *bolt.DB, coll, docID string) int {
	t.Helper()
	var n int
	_ = db.View(func(tx *bolt.Tx) error {
		prefix := storage.RevPrefix(coll, docID)
		c := tx.Bucket([]byte("rev")).Cursor()
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			n++
		}
		return nil
	})
	return n
}

func TestTrimRevisions_KeepZeroIsNoOp(t *testing.T) {
	db := newRevTestDB(t)
	seedRevs(t, db, "blog", "doc1", 5)
	if err := db.Update(func(tx *bolt.Tx) error {
		return trimRevisions(tx, nil, "blog", "doc1", 0)
	}); err != nil {
		t.Fatal(err)
	}
	if got := countRevs(t, db, "blog", "doc1"); got != 5 {
		t.Errorf("keep=0 must be a no-op (unlimited), got %d (want 5)", got)
	}
}

func TestTrimRevisions_NoBucketIsNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nobucket.db")
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	// Don't create the bucket — trimRevisions must not panic.
	if err := db.Update(func(tx *bolt.Tx) error {
		return trimRevisions(tx, nil, "x", "y", 3)
	}); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestTrimRevisions_DropsOldest(t *testing.T) {
	db := newRevTestDB(t)
	seedRevs(t, db, "blog", "doc1", 5)

	if err := db.Update(func(tx *bolt.Tx) error {
		return trimRevisions(tx, nil, "blog", "doc1", 2)
	}); err != nil {
		t.Fatal(err)
	}

	if got := countRevs(t, db, "blog", "doc1"); got != 2 {
		t.Errorf("expected 2 revs after trim, got %d", got)
	}

	// Verify the two remaining revs are the newest (ts 1003, 1004).
	_ = db.View(func(tx *bolt.Tx) error {
		prefix := storage.RevPrefix("blog", "doc1")
		c := tx.Bucket([]byte("rev")).Cursor()
		remaining := make([][]byte, 0, 2)
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			cp := make([]byte, len(k))
			copy(cp, k)
			remaining = append(remaining, cp)
		}
		if len(remaining) != 2 {
			t.Fatalf("expected 2 remaining, got %d", len(remaining))
		}
		wantSuffix := fmt.Sprintf("%020d", int64(1003))
		if !bytes.HasSuffix(remaining[0], []byte(wantSuffix)) {
			t.Errorf("oldest-remaining is not ts=1003, got %s", remaining[0])
		}
		return nil
	})
}

func TestTrimRevisions_KeepExceedsCountIsNoOp(t *testing.T) {
	db := newRevTestDB(t)
	seedRevs(t, db, "blog", "doc1", 3)
	if err := db.Update(func(tx *bolt.Tx) error {
		return trimRevisions(tx, nil, "blog", "doc1", 10)
	}); err != nil {
		t.Fatal(err)
	}
	if got := countRevs(t, db, "blog", "doc1"); got != 3 {
		t.Errorf("expected 3 (unchanged), got %d", got)
	}
}

func TestTrimRevisions_IsolatesByDocID(t *testing.T) {
	db := newRevTestDB(t)
	seedRevs(t, db, "blog", "docA", 4)
	seedRevs(t, db, "blog", "docB", 4)

	if err := db.Update(func(tx *bolt.Tx) error {
		return trimRevisions(tx, nil, "blog", "docA", 1)
	}); err != nil {
		t.Fatal(err)
	}

	if got := countRevs(t, db, "blog", "docA"); got != 1 {
		t.Errorf("docA: expected 1, got %d", got)
	}
	if got := countRevs(t, db, "blog", "docB"); got != 4 {
		t.Errorf("docB must be untouched, got %d", got)
	}
}

// A delete the bucket refuses (here: a read-only transaction) is returned,
// not skipped, so the caller's transaction rolls back.
func TestTrimRevisions_DeleteErrorIsReturned(t *testing.T) {
	db := newRevTestDB(t)
	seedRevs(t, db, "blog", "doc1", 3)

	err := db.View(func(tx *bolt.Tx) error {
		return trimRevisions(tx, nil, "blog", "doc1", 1)
	})
	if !errors.Is(err, bolterrors.ErrTxNotWritable) {
		t.Fatalf("err = %v, want ErrTxNotWritable", err)
	}
	if got := countRevs(t, db, "blog", "doc1"); got != 3 {
		t.Errorf("a failed trim must delete nothing, got %d revs", got)
	}
}
