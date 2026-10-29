package main

import (
	"context"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
	proto "mddb/proto"
)

// A batch delete under any spelling removes the document, its one index
// entry and its revisions (DOC-017), and reports what it could not delete.
func TestBatchDeleter_AnySpellingAndUnreadableDocuments(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	defer s.closeCollectionFiles()

	if _, _, err := s.addDocument("c", "README.md", "en", map[string][]string{"tag": {"x"}}, "v1", 0, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.addDocument("c", "README.md", "en", map[string][]string{"tag": {"x"}}, "v2", 0, true); err != nil {
		t.Fatal(err)
	}
	if err := s.DBUpdateC("c", func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put(storage.DocKey("c", genID("c", "broken", "en")), []byte("not a document"))
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := NewBatchDeleter(s, 2).ProcessBatchDelete(context.Background(), "c", []*proto.DeleteDocument{
		{Key: "readme.MD", Lang: "EN"},
		{Key: "broken", Lang: "en"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Deleted != 1 || resp.Failed != 1 {
		t.Fatalf("deleted %d failed %d (%v), want 1 and 1", resp.Deleted, resp.Failed, resp.Errors)
	}
	id := genID("c", "readme.md", "en")
	if err := s.DBViewC("c", func(tx *bolt.Tx) error {
		if k, _ := tx.Bucket([]byte("bykey")).Cursor().Seek(storage.ByKeyKey("c", "readme.md", "en")); k != nil && strings.HasPrefix(string(k), "bykey|c|readme") {
			t.Errorf("index entry %q survived", k)
		}
		if k, _ := tx.Bucket([]byte("rev")).Cursor().Seek(storage.RevPrefix("c", id)); k != nil && strings.HasPrefix(string(k), string(storage.RevPrefix("c", id))) {
			t.Errorf("revision %q survived", k)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A key that names a nested bucket cannot be deleted as a value; the batch
// reports it and goes on.
func TestBatchDeleter_ReportsADocumentItCannotDelete(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	defer s.closeCollectionFiles()
	if _, _, err := s.addDocument("c", "kept", "en", nil, "x", 0, false); err != nil {
		t.Fatal(err)
	}
	odd := genID("c", "odd", "en")
	if err := s.DBUpdateC("c", func(tx *bolt.Tx) error {
		_, err := tx.Bucket([]byte("docs")).CreateBucket(storage.DocKey("c", odd))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	resp := NewBatchDeleter(s, 1).commitDelete("c", []*DeletedDoc{
		{Key: "odd", Lang: "en", DocID: odd, Found: true},
		{Key: "kept", Lang: "en", DocID: genID("c", "kept", "en"), Found: true},
	})
	if resp.Deleted != 1 || resp.Failed != 1 || !strings.Contains(strings.Join(resp.Errors, ""), "delete error") {
		t.Fatalf("deleted %d failed %d (%v), want 1 and 1 with a delete error", resp.Deleted, resp.Failed, resp.Errors)
	}
}

func TestBatchDeleter_ReportsAFailedTransaction(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "single")
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	resp := NewBatchDeleter(s, 1).commitDelete("c", []*DeletedDoc{{Key: "a", Lang: "en", Found: true}})
	if resp.Failed != 1 || len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], "transaction error") {
		t.Fatalf("delete on a closed database: %+v", resp)
	}
}
