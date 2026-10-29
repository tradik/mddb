package main

import (
	"testing"

	"mddb/internal/fts"
)

// A metadata-only update re-indexes FTS: every meta key is a BM25F field, and
// a PATCH of meta.title used to leave the old title searchable and the new one
// not.
func TestMetadataOnlyUpdateReindexesBM25FFields(t *testing.T) {
	s, cleanup := newTestServerWithFTS(t)
	defer cleanup()
	if _, _, err := s.addDocument("posts", "hello", "en", map[string][]string{"title": {"aurora"}}, "night sky notes", 0, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.updateDocument("posts", "hello", "en", DocPatch{HasMeta: true, Meta: map[string][]string{"title": {"zenith"}}}); err != nil {
		t.Fatal(err)
	}

	titleOnly := map[string]float64{"meta.title": 1}
	find := func(term string) int {
		hits, err := s.FTSIndex.SearchBM25F("posts", map[string]int{term: 1}, 10, titleOnly, fts.DefaultBM25)
		if err != nil {
			t.Fatal(err)
		}
		return len(hits)
	}
	if find("zenith") == 0 {
		t.Error("the new title is not searchable after a metadata-only update")
	}
	if find("aurora") != 0 {
		t.Error("the old title is still searchable after a metadata-only update")
	}
}
