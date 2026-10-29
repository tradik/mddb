package main

import (
	"strings"
	"testing"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// covHStoreWithID stores a document under the key index of (collection, key,
// en) whose own ID field is id — the ID an update writes back to.
func covHStoreWithID(t *testing.T, s *Server, collection, key, id string) {
	t.Helper()
	buf, err := marshalDoc(&storage.Doc{ID: id, Key: key, Lang: "en", ContentMD: "old", AddedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	docID := genID(collection, key, "en")
	if err := s.DBUpdateC(collection, func(tx *bolt.Tx) error {
		if err := tx.Bucket([]byte("docs")).Put(storage.DocKey(collection, docID), buf); err != nil {
			return err
		}
		return tx.Bucket([]byte("bykey")).Put(storage.ByKeyKey(collection, key, "en"), []byte(docID))
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCovHUpdateDocumentWriteFailures(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Content protobuf cannot encode is refused and nothing changes.
	addTestDoc(t, s, "c", "a", "en", "old", nil)
	bad := "bad \xff"
	if _, err := s.updateDocument("c", "a", "en", DocPatch{Content: &bad}); err == nil {
		t.Fatal("invalid UTF-8 content must fail")
	}

	// A document whose ID is too long for a key cannot be written back.
	covHStoreWithID(t, s, "c", "longid", strings.Repeat("x", bolt.MaxKeySize))
	content := "new"
	if _, err := s.updateDocument("c", "longid", "en", DocPatch{Content: &content}); err == nil {
		t.Fatal("an oversized document key must fail")
	}

	// One whose document key just fits fails at the revision, 21 bytes longer.
	covHStoreWithID(t, s, "c", "revid", strings.Repeat("x", bolt.MaxKeySize-len(storage.DocKey("c", ""))))
	if _, err := s.updateDocument("c", "revid", "en", DocPatch{Content: &content}); err == nil {
		t.Fatal("an oversized revision key must fail")
	}

	// Every failure rolled back.
	for _, key := range []string{"a", "longid", "revid"} {
		if got, err := s.loadDocByRef("c", key, "en"); err != nil || got.ContentMD != "old" {
			t.Fatalf("%s changed by a failed update: %v %+v", key, err, got)
		}
	}
}
