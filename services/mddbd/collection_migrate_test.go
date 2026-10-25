package main

import (
	"os"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// #336: split moves every collection key to its file and leaves the rest;
// merge puts everything back exactly.

func dumpAll(t *testing.T, db *bolt.DB) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			return b.ForEach(func(k, v []byte) error {
				out[string(name)+"/"+string(k)] = string(v)
				return nil
			})
		})
	})
	return out
}

func TestSplitAndMergeCollections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mddb.db")
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	seed := map[string]map[string]string{
		"docs":              {"doc|blog|1": "a", "doc|blog|2": "b", "doc|wiki|1": "c"},
		"fts":               {"fts|blog|word|1": "1", "fts|wiki|word|1": "1", "ftsstat|blog": "n"},
		"ttl":               {"00000000000000000099|blog|1": "x"},
		"ttlrev":            {"blog|1": "99"},
		"vector_provenance": {"wiki": "model"},
		"auth_users":        {"admin": "secret"},         // global: stays
		"colmeta":           {"blog": "{}"},              // global: stays
		"idxmeta":           {"no-collection-here": "?"}, // malformed: stays
	}
	_ = db.Update(func(tx *bolt.Tx) error {
		for b, kv := range seed {
			bk, _ := tx.CreateBucketIfNotExists([]byte(b))
			for k, v := range kv {
				_ = bk.Put([]byte(k), []byte(v))
			}
		}
		return nil
	})
	before := dumpAll(t, db)
	_ = db.Close()

	if code := runSplitCollections(path); code != 0 {
		t.Fatalf("split exit %d", code)
	}
	main, _ := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true})
	left := dumpAll(t, main)
	_ = main.Close()
	for _, k := range []string{"auth_users/admin", "colmeta/blog", "idxmeta/no-collection-here"} {
		if _, ok := left[k]; !ok {
			t.Errorf("%s left the main file", k)
		}
	}
	for _, k := range []string{"docs/doc|blog|1", "ttlrev/blog|1", "vector_provenance/wiki", "ttl/00000000000000000099|blog|1", "fts/ftsstat|blog"} {
		if _, ok := left[k]; ok {
			t.Errorf("%s stayed in the main file", k)
		}
	}
	blog, _ := bolt.Open(filepath.Join(collectionDir(path), "blog.db"), 0o600, &bolt.Options{ReadOnly: true})
	inBlog := dumpAll(t, blog)
	_ = blog.Close()
	for _, k := range []string{"docs/doc|blog|1", "docs/doc|blog|2", "fts/fts|blog|word|1", "ttl/00000000000000000099|blog|1", "ttlrev/blog|1"} {
		if inBlog[k] != before[k] {
			t.Errorf("blog.db: %s = %q, want %q", k, inBlog[k], before[k])
		}
	}
	if _, ok := inBlog["docs/doc|wiki|1"]; ok {
		t.Error("a wiki key in blog.db")
	}
	if code := runSplitCollections(path); code != 0 { // nothing left to move: a no-op
		t.Errorf("second split exit %d", code)
	}

	if code := runMergeCollections(path); code != 0 {
		t.Fatalf("merge exit %d", code)
	}
	if _, err := os.Stat(collectionDir(path)); !os.IsNotExist(err) {
		t.Error("the collection directory survived the merge")
	}
	main, _ = bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true})
	after := dumpAll(t, main)
	_ = main.Close()
	for k, v := range before {
		if after[k] != v {
			t.Errorf("after merge %s = %q, want %q", k, after[k], v)
		}
	}

	if runMergeCollections(path) != 1 {
		t.Error("merging with no collection files should fail")
	}
	if runSplitCollections(filepath.Join(t.TempDir(), "missing.db")) != 1 {
		t.Error("splitting a missing file should fail")
	}
}
