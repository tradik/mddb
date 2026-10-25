package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// #336: one file per collection.

func TestCollectionFileNamesRoundTrip(t *testing.T) {
	seen := map[string]string{}
	for _, c := range []string{"blog", "Blog", "BLOG", "my-docs_2", "a.b", "a/b", "zażółć", "..", "con", "CON", "nul", "x%y", " "} {
		if other, dup := seen[strings.ToLower(collectionFileName(c))]; dup {
			t.Errorf("%q and %q share a file on a case-folding file system", c, other)
		}
		seen[strings.ToLower(collectionFileName(c))] = c
		if base := strings.TrimSuffix(collectionFileName(c), ".db"); windowsReservedName(base) {
			t.Errorf("%q maps to a Windows reserved name", c)
		}
		name := collectionFileName(c)
		if filepath.Base(name) != name || name == ".db" {
			t.Errorf("%q -> %q is not a plain file name", c, name)
		}
		back, ok := collectionFromFileName(name)
		if !ok || back != c {
			t.Errorf("%q -> %q -> %q (%v)", c, name, back, ok)
		}
	}
	for _, bad := range []string{"blog", ".db", "a%zz.db", "a.b.db"} {
		if _, ok := collectionFromFileName(bad); ok {
			t.Errorf("%q accepted as a collection file", bad)
		}
	}
}

func TestCollectionFilesLifecycle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "main.db.d")
	opts := func(string) *bolt.Options { return getOptimizedBoltOptions() }
	cf, err := openCollectionFiles(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	if cf.get("blog") != nil {
		t.Fatal("a file before any write")
	}
	db, err := cf.getOrCreate("blog")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := cf.getOrCreate("blog"); again != db {
		t.Error("a second handle for the same collection")
	}
	_ = db.View(func(tx *bolt.Tx) error {
		for _, b := range collectionBuckets {
			if tx.Bucket(b) == nil {
				t.Errorf("bucket %s missing in a new collection file", b)
			}
		}
		return nil
	})
	if _, err := cf.getOrCreate(""); err == nil {
		t.Error("a file for an unnamed collection")
	}
	_, _ = cf.getOrCreate("a/b")
	cf.closeAll()

	// Reopened: both files are found and opened.
	cf, err = openCollectionFiles(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := cf.names(); len(got) != 2 || got[0] != "a/b" || got[1] != "blog" {
		t.Fatalf("names after reopen: %v", got)
	}
	if len(cf.all()) != 2 {
		t.Error("all()")
	}
	if err := cf.remove("blog"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "blog.db")); !os.IsNotExist(err) {
		t.Error("removing a collection left its file")
	}
	if err := cf.remove("never"); err != nil {
		t.Error(err)
	}
	cf.closeAll()

	// A file that is not a database stops the open with its collection named.
	_ = os.WriteFile(filepath.Join(dir, "broken.db"), []byte("not bolt"), 0o600)
	if _, err := openCollectionFiles(dir, opts); err == nil {
		t.Error("a broken collection file was accepted")
	}
}

func TestMainFileHoldsCollections(t *testing.T) {
	db, err := bolt.Open(filepath.Join(t.TempDir(), "m.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if held, _ := mainFileHoldsCollections(db); held {
		t.Error("empty file")
	}
	_ = db.Update(func(tx *bolt.Tx) error {
		b, _ := tx.CreateBucketIfNotExists([]byte("docs"))
		_, _ = tx.CreateBucketIfNotExists([]byte("auth_users"))
		return b.Put([]byte("doc|blog|1"), []byte("x"))
	})
	if held, _ := mainFileHoldsCollections(db); !held {
		t.Error("collection keys not noticed")
	}
	if !isCollectionBucket([]byte("ftsp")) || isCollectionBucket([]byte("auth_users")) {
		t.Error("isCollectionBucket")
	}
}
