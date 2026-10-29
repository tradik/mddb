package boltx

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// openTestDB is a fresh file with one bucket, "b".
func openTestDB(t *testing.T, name string) *bolt.DB {
	t.Helper()
	db, err := bolt.Open(filepath.Join(t.TempDir(), name), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucket([]byte("b"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return db
}

// failingRouter refuses every write and lists files that fail to read.
type failingRouter struct{ dbs []*bolt.DB }

var errRefused = errors.New("refused")

func (f failingRouter) ForRead(string) *bolt.DB           { return f.dbs[0] }
func (f failingRouter) ForWrite(string) (*bolt.DB, error) { return nil, errRefused }
func (f failingRouter) All() []*bolt.DB                   { return f.dbs }

// Single is the one-file layout: every collection reads and writes the file.
func TestSingleRoutesEverythingToItsFile(t *testing.T) {
	db := openTestDB(t, "single.db")
	r := Single{DB: db}
	if r.ForRead("a") != db {
		t.Error("ForRead did not answer with the file")
	}
	if w, err := r.ForWrite("a"); w != db || err != nil {
		t.Errorf("ForWrite = %v, %v", w, err)
	}
	if all := r.All(); len(all) != 1 || all[0] != db {
		t.Errorf("All = %v", all)
	}

	if err := Update(r, "a", func(tx *bolt.Tx) error { return tx.Bucket([]byte("b")).Put([]byte("k"), []byte("v")) }); err != nil {
		t.Fatal(err)
	}
	var got string
	read := func(tx *bolt.Tx) error { got = string(tx.Bucket([]byte("b")).Get([]byte("k"))); return nil }
	for _, c := range []string{"a", ""} {
		got = ""
		if err := ViewOneOrAll(r, c, read); err != nil || got != "v" {
			t.Errorf("ViewOneOrAll(%q) read %q, %v", c, got, err)
		}
	}
}

// A refused write reaches the caller, and ViewAll stops at the first file
// that fails.
func TestRouterErrorsReachTheCaller(t *testing.T) {
	first, second := openTestDB(t, "first.db"), openTestDB(t, "second.db")
	r := failingRouter{dbs: []*bolt.DB{first, second}}

	if err := Update(r, "a", func(*bolt.Tx) error { return nil }); !errors.Is(err, errRefused) {
		t.Errorf("Update = %v, want the router's refusal", err)
	}
	visited := 0
	err := ViewAll(r, func(*bolt.Tx) error { visited++; return errRefused })
	if !errors.Is(err, errRefused) || visited != 1 {
		t.Errorf("ViewAll = %v after %d files, want the error after one", err, visited)
	}
}

// Current follows the server's handle when there is one.
func TestCurrentFollowsTheServerHandle(t *testing.T) {
	own, followed := openTestDB(t, "own.db"), openTestDB(t, "followed.db")
	if Current(own, nil) != own {
		t.Error("without follow, Current is not the component's own file")
	}
	if Current(own, func() *bolt.DB { return nil }) != own {
		t.Error("a follow that gives nothing did not fall back to the own file")
	}
	if Current(own, func() *bolt.DB { return followed }) != followed {
		t.Error("Current did not follow the server's handle")
	}
}

// RouterSlot (#359): empty and nil answer with the component's own file, a
// stored router answers for itself.
func TestRouterSlotGetFallsBackToTheFile(t *testing.T) {
	db, err := bolt.Open(filepath.Join(t.TempDir(), "slot.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var slot RouterSlot
	if got, ok := slot.Get(db).(Single); !ok || got.DB != db {
		t.Fatalf("empty slot = %#v, want Single on the file", slot.Get(db))
	}
	other := Single{DB: &bolt.DB{}}
	slot.Set(other)
	if got := slot.Get(db); got != other {
		t.Fatalf("stored router = %#v, want %#v", got, other)
	}
	slot.Set(nil)
	if got, ok := slot.Get(db).(Single); !ok || got.DB != db {
		t.Fatalf("slot set to nil = %#v, want Single on the file", slot.Get(db))
	}
}

// #359: the server sets a component's router again while the component is
// in use. Under -race, a plain field fails this test.
func TestRouterSlotSetWhileRead(t *testing.T) {
	var slot RouterSlot
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 1000 {
			slot.Set(Single{})
		}
	}()
	for range 1000 {
		_ = slot.Get(nil)
	}
	wg.Wait()
}
