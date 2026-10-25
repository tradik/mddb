package main

import (
	"bytes"
	"fmt"
	"mddb/internal/storage"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// revCount counts revisions under the given doc prefix directly in bolt.
func revCount(t *testing.T, s *Server, coll, docID string) int {
	t.Helper()
	var n int
	_ = s.DBViewC(coll, func(tx *bolt.Tx) error {
		prefix := storage.RevPrefix(coll, docID)
		c := tx.Bucket([]byte("rev")).Cursor()
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			n++
		}
		return nil
	})
	return n
}

// seedFakeRevisions writes N entries under the rev bucket with synthetic
// timestamps so tests bypass addDocument's 1-second clock resolution.
// Returns the docID that matches the (coll, key, lang) triple.
func seedFakeRevisions(t *testing.T, s *Server, coll, key, lang string, count int) string {
	t.Helper()
	docID := genID(coll, key, lang)
	if err := s.DBUpdateC(coll, func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("rev"))
		for i := 0; i < count; i++ {
			rkey := append(storage.RevPrefix(coll, docID), []byte(fmt.Sprintf("%020d", int64(100+i)))...)
			if err := b.Put(rkey, []byte(fmt.Sprintf("v%d", i))); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return docID
}

// TestAddDocumentTriggersTrimWhenCapSet pre-seeds 10 fake revisions, then
// calls addDocument once. With MaxRevisions=3 configured, the post-write
// trim hook must bring total back to 3 (2 old + 1 new newest).
func TestAddDocumentTriggersTrimWhenCapSet(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	s.CollectionManager = NewCollectionManager(s.DB)
	if err := s.CollectionManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := s.CollectionManager.Set("blog", &CollectionConfig{Type: "default", MaxRevisions: 3}); err != nil {
		t.Fatal(err)
	}

	docID := seedFakeRevisions(t, s, "blog", "post-1", "en_US", 10)
	if got := revCount(t, s, "blog", docID); got != 10 {
		t.Fatalf("seed verification failed: got %d revisions", got)
	}

	// addDocument writes 1 new rev (live clock) then the trim hook prunes.
	if _, _, err := s.addDocument("blog", "post-1", "en_US", nil, "final", 0, true); err != nil {
		t.Fatal(err)
	}

	if got := revCount(t, s, "blog", docID); got != 3 {
		t.Errorf("expected 3 revisions after trim, got %d", got)
	}
}

// TestAddDocumentNoTrimWhenCapZero asserts the default (MaxRevisions=0) is
// unlimited — pre-seed stays intact after a new write.
func TestAddDocumentNoTrimWhenCapZero(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	s.CollectionManager = NewCollectionManager(s.DB)
	if err := s.CollectionManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := s.CollectionManager.Set("blog", &CollectionConfig{Type: "default", MaxRevisions: 0}); err != nil {
		t.Fatal(err)
	}

	docID := seedFakeRevisions(t, s, "blog", "post-1", "en_US", 5)

	if _, _, err := s.addDocument("blog", "post-1", "en_US", nil, "final", 0, true); err != nil {
		t.Fatal(err)
	}

	// 5 seeded + 1 new rev = 6 — nothing trimmed.
	if got := revCount(t, s, "blog", docID); got != 6 {
		t.Errorf("expected 6 revisions (unlimited), got %d", got)
	}
}

// TestAddDocumentWithoutCollectionManager makes sure the feature is
// optional — if the manager is nil the code path is skipped cleanly.
func TestAddDocumentWithoutCollectionManager(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.CollectionManager = nil

	if _, _, err := s.addDocument("blog", "p", "en_US", nil, "x", 0, true); err != nil {
		t.Errorf("addDocument must not crash with nil CollectionManager: %v", err)
	}
}
