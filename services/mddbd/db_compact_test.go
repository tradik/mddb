package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// #335: compacting copies every key into a smaller file and leaves the
// source alone.

func TestCompactShrinksAndKeepsEveryKey(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src.db"), filepath.Join(dir, "dst.db")
	db, err := bolt.Open(src, 0o600, getOptimizedBoltOptions())
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Update(func(tx *bolt.Tx) error {
		b, _ := tx.CreateBucketIfNotExists([]byte("docs"))
		for i := 0; i < 3000; i++ {
			_ = b.Put(compactTestKey(i), make([]byte, 1000))
		}
		return nil
	})
	_ = db.Update(func(tx *bolt.Tx) error { // free most of it
		b := tx.Bucket([]byte("docs"))
		for i := 0; i < 2900; i++ {
			_ = b.Delete(compactTestKey(i))
		}
		return nil
	})
	_ = db.Close()

	if code := runCompact(src, dst, 16384); code != 0 {
		t.Fatalf("exit %d", code)
	}
	a, _ := os.Stat(src)
	b, _ := os.Stat(dst)
	if b.Size() >= a.Size() {
		t.Errorf("compacted %d bytes into %d", a.Size(), b.Size())
	}
	out, _ := bolt.Open(dst, 0o600, &bolt.Options{ReadOnly: true})
	defer func() { _ = out.Close() }()
	if out.Info().PageSize != 16384 {
		t.Errorf("page size %d", out.Info().PageSize)
	}
	_ = out.View(func(tx *bolt.Tx) error {
		if n := tx.Bucket([]byte("docs")).Stats().KeyN; n != 100 {
			t.Errorf("%d keys, want 100", n)
		}
		return nil
	})
	if m, _ := readBoltMeta(dst); m.needsWalk() {
		t.Error("the copy should start without a walk")
	}

	if runCompact(src, "", 0) != 2 {
		t.Error("missing -compact-to")
	}
	if runCompact(src, dst, 0) != 1 {
		t.Error("an existing destination was overwritten")
	}
	if runCompact(src, filepath.Join(dir, "x.db"), 5000) != 1 {
		t.Error("a page size that is not a power of two")
	}
	if runCompact(filepath.Join(dir, "missing.db"), filepath.Join(dir, "y.db"), 0) != 1 {
		t.Error("a missing source")
	}
}

func compactTestKey(i int) []byte { return []byte(fmt.Sprintf("%05d", i)) }
