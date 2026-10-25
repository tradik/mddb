package bbolt_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// Tests for the mddb patch in deferred_freelist.go.

func deferredDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "d.db")
	db, err := bolt.Open(path, 0o600, &bolt.Options{NoFreelistSync: true})
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 3; round++ { // churn, so there are free pages
		if err := db.Update(func(tx *bolt.Tx) error {
			b, _ := tx.CreateBucketIfNotExists([]byte("b"))
			for i := 0; i < 2000; i++ {
				if err := b.Put([]byte(fmt.Sprintf("k%05d", i)), bytes.Repeat([]byte{byte(round)}, 500)); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDeferredFreelistReadsBeforeLoad(t *testing.T) {
	// A large initial mmap: bbolt cannot grow the map while a read
	// transaction is open, and this test keeps one open across writes.
	db, err := bolt.Open(deferredDB(t), 0o600, &bolt.Options{NoFreelistSync: true, DeferFreelistLoad: true, InitialMmapSize: 256 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if db.FreelistLoaded() {
		t.Fatal("loaded at open")
	}

	// A reader that begins before the freelist exists...
	early, err := db.Begin(false)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), early.Bucket([]byte("b")).Get([]byte("k00042"))...)

	// ...a reader that begins and ends before it, too.
	if err := db.View(func(tx *bolt.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}

	if err := db.LoadFreelist(); err != nil || !db.FreelistLoaded() {
		t.Fatalf("load: %v", err)
	}
	// Writes now reuse free pages. None the early reader can see may be
	// among them.
	for round := 0; round < 5; round++ {
		if err := db.Update(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte("b"))
			for i := 0; i < 2000; i++ {
				if err := b.Put([]byte(fmt.Sprintf("k%05d", i)), bytes.Repeat([]byte{0xEE}, 500)); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got := early.Bucket([]byte("b")).Get([]byte("k00042")); !bytes.Equal(got, want) {
		t.Fatalf("the early reader saw its page reused: %x... want %x...", got[:4], want[:4])
	}
	if err := early.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := db.LoadFreelist(); err != nil {
		t.Error("a second load should be a no-op")
	}
}

func TestDeferredFreelistFirstWriteLoadsIt(t *testing.T) {
	path := deferredDB(t)
	db, err := bolt.Open(path, 0o600, &bolt.Options{DeferFreelistLoad: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("b")).Put([]byte("x"), []byte("y")) }); err != nil {
		t.Fatal(err)
	}
	if !db.FreelistLoaded() {
		t.Error("a write did not load the freelist")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// NoFreelistSync was off, so that commit stored the freelist.
	db, err = bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.View(func(tx *bolt.Tx) error {
		for err := range tx.Check() {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeferredFreelistReportsDamage(t *testing.T) {
	path := deferredDB(t)
	// Overwrite the bucket's root page, which every walk reads, with garbage.
	var root, pageSize int64
	db0, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	pageSize = int64(db0.Info().PageSize)
	_ = db0.View(func(tx *bolt.Tx) error { root = int64(tx.Bucket([]byte("b")).Root()); return nil })
	_ = db0.Close()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(bytes.Repeat([]byte{0xFF}, int(pageSize)), root*pageSize); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	db, err := bolt.Open(path, 0o600, &bolt.Options{NoFreelistSync: true, DeferFreelistLoad: true})
	if err != nil {
		t.Fatalf("a deferred open must not walk: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.LoadFreelist(); err == nil {
		t.Fatal("a damaged tree loaded without error")
	}
	if err := db.Update(func(*bolt.Tx) error { return nil }); err == nil {
		t.Error("a write succeeded without a freelist")
	}
	if db.FreelistLoaded() {
		t.Error("a failed load reports loaded")
	}

	// Without the option, the same damage is an error from Open, not a panic.
	_ = db.Close()
	if _, err := bolt.Open(path, 0o600, &bolt.Options{NoFreelistSync: true}); err == nil {
		t.Error("a damaged walk at open returned no error")
	}
}

func TestDeferredFreelistAfterClose(t *testing.T) {
	db, err := bolt.Open(deferredDB(t), 0o600, &bolt.Options{NoFreelistSync: true, DeferFreelistLoad: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Loading after the map is gone must report it, not read freed memory.
	if err := db.LoadFreelist(); err == nil {
		t.Error("loaded the freelist of a closed database")
	}
}
