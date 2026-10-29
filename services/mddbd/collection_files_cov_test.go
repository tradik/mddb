package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// The failure paths of the per-collection files (#336), with the file
// operations made to fail through their variables.

var errFileOp = errors.New("file operation failed")

func TestCollectionFilesOpenFailures(t *testing.T) {
	opts := func(string) *bolt.Options { return getOptimizedBoltOptions() }
	dir := filepath.Join(t.TempDir(), "main.db.d")

	old := collReadDir
	collReadDir = func(string) ([]os.DirEntry, error) { return nil, errFileOp }
	_, err := openCollectionFiles(dir, opts)
	collReadDir = old
	if !errors.Is(err, errFileOp) {
		t.Fatalf("read dir: %v", err)
	}

	// A file that names no collection, and a directory, are not opened.
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600)
	_ = os.MkdirAll(filepath.Join(dir, "sub.db"), 0o700)
	cf, err := openCollectionFiles(dir, opts)
	if err != nil || len(cf.names()) != 0 {
		t.Fatalf("junk opened: %v %v", err, cf.names())
	}
	// A collection whose file cannot be created: a directory in its place.
	if _, err := cf.getOrCreate("sub"); err == nil {
		t.Fatal("a directory opened as a collection file")
	}
	// A collection whose file opens read-only cannot get its buckets.
	path := filepath.Join(dir, collectionFileName("ro"))
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	cf.opts = func(string) *bolt.Options { o := *getOptimizedBoltOptions(); o.ReadOnly = true; return &o }
	if _, err := cf.getOrCreate("ro"); err == nil {
		t.Fatal("buckets created in a read-only file")
	}
	cf.closeAll()
}

func TestSwapCollectionFilesFailures(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	live := collectionDir(s.Path)
	if err := os.MkdirAll(live, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(live, "keep.db"), []byte("live"), 0o600)

	if _, err := s.swapCollectionFiles("../escape"); err == nil || !strings.Contains(err.Error(), "invalid backup path") {
		t.Fatalf("..: %v", err)
	}
	src := filepath.Join(t.TempDir(), "backup.d")
	_ = os.MkdirAll(src, 0o700)
	_ = os.WriteFile(filepath.Join(src, "broken.db"), []byte("not bolt"), 0o600)
	if _, err := s.swapCollectionFiles(src); err == nil || !strings.Contains(err.Error(), "not a usable database") {
		t.Fatalf("broken: %v", err)
	}
	_ = os.Remove(filepath.Join(src, "broken.db"))
	db, err := bolt.Open(filepath.Join(src, "blog.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	_ = os.WriteFile(filepath.Join(src, "README"), []byte("not a collection"), 0o600)

	restoreOps := func() func() {
		r, m, c := collRename, collMkdirAll, collCopyFile
		return func() { collRename, collMkdirAll, collCopyFile = r, m, c }
	}
	reset := restoreOps()
	collRename = func(string, string) error { return errFileOp }
	if _, err := s.swapCollectionFiles(src); !errors.Is(err, errFileOp) {
		t.Fatalf("rename: %v", err)
	}
	reset()
	collMkdirAll = func(string, os.FileMode) error { return errFileOp }
	if _, err := s.swapCollectionFiles(src); !errors.Is(err, errFileOp) {
		t.Fatalf("mkdir: %v", err)
	}
	reset()
	collCopyFile = func(string, string) error { return errFileOp }
	if _, err := s.swapCollectionFiles(src); !errors.Is(err, errFileOp) {
		t.Fatalf("copy: %v", err)
	}
	reset()
	// Each failure put the live files back.
	if data, err := os.ReadFile(filepath.Join(live, "keep.db")); err != nil || string(data) != "live" { // #nosec G304 -- a test file
		t.Fatalf("live files after a failed swap: %v", err)
	}

	// A swap that worked, undone.
	undo, err := s.swapCollectionFiles(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(live, "blog.db")); err != nil {
		t.Fatalf("swapped in: %v", err)
	}
	if _, err := s.collectionDBForWrite("open-one"); err != nil {
		t.Fatal(err)
	}
	undo()
	if _, err := os.Stat(filepath.Join(live, "keep.db")); err != nil {
		t.Fatalf("undone: %v", err)
	}
}
