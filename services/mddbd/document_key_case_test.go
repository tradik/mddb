package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
)

// DOC-016: document identifiers are case-insensitive and the key index is not.
// These tests pin what that costs and what is done about it.

func TestKeysDifferingOnlyInCaseNameOneDocument(t *testing.T) {
	// The premise everything else here follows from.
	ids := map[string]bool{}
	for _, key := range []string{"README.md", "readme.md", "ReadMe.md", "readme.MD"} {
		ids[genID("c", key, "en")] = true
	}
	if len(ids) != 1 {
		t.Fatalf("expected one identifier, got %v", ids)
	}
}

func TestFindCaseCollisions(t *testing.T) {
	cases := []struct {
		name string
		keys []string
		want int
	}{
		{"no collision", []string{"readme.md", "install.md"}, 0},
		{"one collision", []string{"README.md", "readme.md"}, 1},
		{"three spellings are still one collision", []string{"README.md", "readme.md", "ReadMe.md"}, 1},
		{"two separate collisions", []string{"README.md", "readme.md", "Makefile", "makefile"}, 2},
		// A key sent twice verbatim is an ordinary overwrite. Reporting it as a
		// case collision would cry wolf on every re-import.
		{"exact repeat is not a collision", []string{"readme.md", "readme.md"}, 0},
		{"empty", nil, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := findCaseCollisions(tc.keys); len(got) != tc.want {
				t.Errorf("got %d collisions %v, want %d", len(got), got, tc.want)
			}
		})
	}
}

func TestCaseCollisionsArePresentedInArrivalOrder(t *testing.T) {
	// The order says which write replaced which, which is the only actionable
	// part of the message.
	got := findCaseCollisions([]string{"README.md", "readme.md"})
	if len(got) != 1 || got[0][0] != "README.md" || got[0][1] != "readme.md" {
		t.Fatalf("got %v, want [[README.md readme.md]]", got)
	}
}

func TestDescribeKeyCollisionsIsSilentWhenThereAreNone(t *testing.T) {
	// nil rather than an empty slice, so the field stays out of the response
	// instead of appearing on every import.
	if got := describeKeyCollisions([]IngestDocumentHTTP{{Key: "a.md"}, {Key: "b.md"}}); got != nil {
		t.Errorf("got %v, want nil", got)
	}
	if got := describeKeyCollisions(nil); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestDescribeKeyCollisionsNamesBothSpellings(t *testing.T) {
	got := describeKeyCollisions([]IngestDocumentHTTP{
		{Key: "README.md"}, {Key: "install.md"}, {Key: "readme.md"},
	})
	if len(got) != 1 {
		t.Fatalf("got %d messages: %v", len(got), got)
	}
	for _, want := range []string{`"README.md"`, `"readme.md"`, "case-insensitive", "last write wins"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("message is missing %q: %s", want, got[0])
		}
	}
	if strings.Contains(got[0], "install.md") {
		t.Errorf("an uninvolved key was named: %s", got[0])
	}
}

func TestKeysCollideOnCase(t *testing.T) {
	if !keysCollideOnCase("README.md", "readme.md") {
		t.Error("case variants should collide")
	}
	if keysCollideOnCase("readme.md", "readme.md") {
		t.Error("a key does not collide with itself")
	}
	if keysCollideOnCase("readme.md", "install.md") {
		t.Error("different keys should not collide")
	}
}

func TestByKeyVariants(t *testing.T) {
	// Requested and stored agree: one entry, no speculative second delete.
	one := byKeyVariants("c", "readme.md", "readme.md", "en")
	if len(one) != 1 {
		t.Errorf("got %d variants, want 1", len(one))
	}
	// Unknown stored key (document was not found): still one.
	if got := byKeyVariants("c", "readme.md", "", "en"); len(got) != 1 {
		t.Errorf("got %d variants for an unknown stored key, want 1", len(got))
	}
	// Written as one spelling, deleted as another: both entries have to go.
	two := byKeyVariants("c", "readme.md", "README.md", "en")
	if len(two) != 2 {
		t.Fatalf("got %d variants, want 2", len(two))
	}
	if string(two[0]) == string(two[1]) {
		t.Error("the two variants are the same key")
	}
}

// The one that matters: an index entry left pointing at a deleted document.
func TestDeletingACollectionLeavesNoOrphanedKeyEntries(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	// Both spellings: one document, two index entries.
	for _, key := range []string{"README.md", "readme.md"} {
		if _, _, err := s.addDocument("c", key, "en", nil, "content of "+key, 0, false); err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
	}
	if before := countByKeyEntries(t, s, "c"); before != 2 {
		t.Fatalf("expected two index entries for one document, got %d", before)
	}

	client := &DirectClient{server: s}
	if _, err := client.DeleteCollection(context.Background(), &MCPDeleteCollectionRequest{Collection: "c"}); err != nil {
		t.Fatalf("delete collection: %v", err)
	}

	if after := countByKeyEntries(t, s, "c"); after != 0 {
		t.Fatalf("%d index entries survived the collection they belonged to", after)
	}
}

func countByKeyEntries(t *testing.T, s *Server, collection string) int {
	t.Helper()
	var n int
	err := s.DBView(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("bykey"))
		if bucket == nil {
			return nil
		}
		prefix := []byte("bykey|" + collection + "|")
		cursor := bucket.Cursor()
		for k, _ := cursor.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = cursor.Next() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestDeleteCollectionByKeyEntriesOnAMissingBucketIsANoOp(t *testing.T) {
	// The caller reaches this on a database that has never held a document.
	if err := deleteCollectionByKeyEntries(nil, "c", nil); err != nil {
		t.Fatalf("nil bucket should be a no-op, got %v", err)
	}
}

func TestDeleteCollectionByKeyEntriesLeavesOtherCollectionsAlone(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	if _, _, err := s.addDocument("keep", "a.md", "en", nil, "kept", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.addDocument("drop", "a.md", "en", nil, "dropped", 0, false); err != nil {
		t.Fatal(err)
	}

	if err := s.DBUpdate(func(tx *bolt.Tx) error {
		return deleteCollectionByKeyEntries(tx.Bucket([]byte("bykey")), "drop", nil)
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if got := countByKeyEntries(t, s, "keep"); got != 1 {
		t.Errorf("a neighbouring collection lost %d entries", 1-got)
	}
	if got := countByKeyEntries(t, s, "drop"); got != 0 {
		t.Errorf("%d entries survived", got)
	}
}

func TestNormalizeDocumentKeyMatchesGenID(t *testing.T) {
	// If genID's normalisation ever changes, this is the test that says the
	// helper beside it was not updated.
	for _, key := range []string{"README.md", "Path/To/File.MD", "already-lower"} {
		normalized := normalizeDocumentKey(key)
		if genID("c", key, "en") != genID("c", normalized, "en") {
			t.Errorf("%q normalises to %q, which genID does not agree with", key, normalized)
		}
	}
	_ = storage.ByKeyKey
}
