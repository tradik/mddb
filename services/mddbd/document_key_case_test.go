package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
)

// DOC-016 / DOC-017: document identifiers and the key index are both
// case-insensitive; these tests pin that they agree.

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

// DOC-017: any spelling reads the document a different spelling wrote.
func TestAnySpellingFindsTheDocument(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	if _, _, err := s.addDocument("c", "README.md", "EN", nil, "upper", 0, false); err != nil {
		t.Fatal(err)
	}
	client := &DirectClient{server: s}
	for _, key := range []string{"readme.md", "README.md", "ReadMe.MD"} {
		doc, err := client.Get(context.Background(), &MCPGetRequest{Collection: "c", Key: key, Lang: "en"})
		if err != nil || doc.ContentMD != "upper" {
			t.Errorf("reading %q: doc=%+v err=%v", key, doc, err)
		}
	}
}

// DOC-017: one index entry per document, whatever spellings were written,
// and a delete under any spelling leaves none behind.
func TestOneIndexEntryPerDocumentAcrossSpellings(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	for _, key := range []string{"README.md", "readme.md", "ReadMe.md"} {
		if _, _, err := s.addDocument("c", key, "en", nil, "content of "+key, 0, false); err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
	}
	if got := countByKeyEntries(t, s, "c"); got != 1 {
		t.Fatalf("three spellings left %d index entries, want 1", got)
	}
	if err := s.deleteDocumentInternal("c", "readme.MD", "en"); err != nil {
		t.Fatal(err)
	}
	if got := countByKeyEntries(t, s, "c"); got != 0 {
		t.Fatalf("%d index entries survived the document", got)
	}
}

// The one that matters: an index entry left pointing at a deleted document.
func TestDeletingACollectionLeavesNoOrphanedKeyEntries(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	for _, key := range []string{"README.md", "install.md"} {
		if _, _, err := s.addDocument("c", key, "en", nil, "content of "+key, 0, false); err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
	}
	if before := countByKeyEntries(t, s, "c"); before != 2 {
		t.Fatalf("expected one index entry per document, got %d", before)
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
	err := s.DBViewC(collection, func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("bykey"))
		if bucket == nil {
			return nil
		}
		prefix := storage.ByKeyCollectionPrefix(collection)
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

	if err := s.DBUpdateC("drop", func(tx *bolt.Tx) error {
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
		normalized := storage.NormalizeKey(key)
		if genID("c", key, "en") != genID("c", normalized, "en") {
			t.Errorf("%q normalises to %q, which genID does not agree with", key, normalized)
		}
	}
	if got, want := string(storage.ByKeyKey("c", "Path/To/File.MD", "EN")), "bykey|c|"+strings.TrimPrefix(genID("c", "Path/To/File.MD", "EN"), "c|"); got != want {
		t.Errorf("the index key %q and the identifier disagree (%q)", got, want)
	}
}
