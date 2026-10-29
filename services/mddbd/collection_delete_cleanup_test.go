package main

import (
	"errors"
	"os"
	"runtime"
	"testing"

	"mddb/internal/geo"
	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// A collection delete cleans up best effort: once the documents are gone, a
// step that fails is logged, and the delete still reports what it removed. A
// step inside the transaction that fails fails the delete. Keys the buckets
// cannot delete — a nested bucket where a value belongs — stand in for the
// failures.

type failingDeleteBackend struct{ *MemoryBackend }

func (failingDeleteBackend) DeleteDoc(string, string) error { return errors.New("backend down") }

// nestBucket replaces key in bucket with a nested bucket.
func nestBucket(t *testing.T, s *Server, collection, bucket string, key []byte) {
	t.Helper()
	if err := s.DBUpdateC(collection, func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		_ = b.Delete(key)
		_, err := b.CreateBucket(key)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteCollection_CleanUpIsBestEffort(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.GeoIndex, s.GeoHashIndex, s.GeoStore = geo.NewGeoIndex(), geo.NewGeoHashIndex(), geo.NewGeoStore(s.DB)
	if err := s.GeoStore.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	doc := addTestDoc(t, s, "c", "a", "en", "some text", nil)
	if err := s.DBUpdateC("c", func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put(storage.DocKey("c", "corrupt"), []byte{0xff, 0x00})
	}); err != nil {
		t.Fatal(err)
	}
	nestBucket(t, s, "c", "ftsrev", []byte("ftsrev|c|"+doc.ID))
	nestBucket(t, s, "c", "vectors", []byte("vec|c|nested"))
	s.Backends = NewBackendRegistry(failingDeleteBackend{NewMemoryBackend()})

	n, err := s.deleteCollection("c")
	if err != nil || n != 1 {
		t.Fatalf("deleted %d (%v), want 1 and no error", n, err)
	}
}

func TestDeleteCollection_TransactionFailures(t *testing.T) {
	for _, c := range []struct {
		name, bucket string
		key          func(docID string) []byte
	}{
		{"revision", "rev", func(id string) []byte { return append(storage.RevPrefix("c", id), 'z') }},
		{"metadata index", "idxmeta", func(id string) []byte { return append(storage.MetaKeyPrefix("c", "tag", "x"), id...) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, cleanup := newHandlerTestServer(t)
			defer cleanup()
			doc := addTestDoc(t, s, "c", "a", "en", "text", map[string][]string{"tag": {"x"}})
			nestBucket(t, s, "c", c.bucket, c.key(doc.ID))
			if _, err := s.deleteCollection("c"); err == nil {
				t.Fatal("a failed delete inside the transaction was reported as done")
			}
		})
	}
}

// In the per-collection layout a file that cannot be removed is logged; the
// delete has already happened.
func TestDeleteCollection_FileRemovalFailureIsLogged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("an open file cannot be removed from under bbolt on Windows")
	}
	s := perCollectionServer(t)
	addTestDoc(t, s, "c", "a", "en", "text", nil)
	if err := os.Remove(s.collectionDB("c").Path()); err != nil {
		t.Fatal(err)
	}
	if n, err := s.deleteCollection("c"); err != nil || n != 1 {
		t.Fatalf("deleted %d (%v)", n, err)
	}
}
