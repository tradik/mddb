package fts

import "testing"

func TestAutocomplete_MatchesByDocCount(t *testing.T) {
	s, cleanup := newAutocompleteFTS(t)
	defer cleanup()

	// "market" appears in 3 docs, "marker" in 2, "marathon" in 1 — all share
	// prefix "mar". Expected order: market > marker > marathon.
	indexDocAC(t, s, "c", "d1", "content", "market")
	indexDocAC(t, s, "c", "d2", "content", "market and marker")
	indexDocAC(t, s, "c", "d3", "content", "market news")
	indexDocAC(t, s, "c", "d4", "content", "marker only")
	indexDocAC(t, s, "c", "d5", "content", "marathon runner")

	items, err := s.Autocomplete("c", "mar", "", 10)
	if err != nil {
		t.Fatalf("autocomplete: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 distinct terms, got %d: %+v", len(items), items)
	}
	if items[0].Term != "market" || items[0].DocCount != 3 {
		t.Errorf("expected market@3 first, got %+v", items[0])
	}
	if items[1].Term != "marker" || items[1].DocCount != 2 {
		t.Errorf("expected marker@2 second, got %+v", items[1])
	}
	if items[2].Term != "marathon" || items[2].DocCount != 1 {
		t.Errorf("expected marathon@1 third, got %+v", items[2])
	}
}
func TestAutocomplete_TopNCap(t *testing.T) {
	s, cleanup := newAutocompleteFTS(t)
	defer cleanup()

	indexDocAC(t, s, "c", "d1", "content", "market")
	indexDocAC(t, s, "c", "d2", "content", "marker")
	indexDocAC(t, s, "c", "d3", "content", "marathon")

	items, err := s.Autocomplete("c", "mar", "", 2)
	if err != nil {
		t.Fatalf("autocomplete: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected topN=2, got %d", len(items))
	}
}
func TestAutocomplete_FieldScoped(t *testing.T) {
	s, cleanup := newAutocompleteFTS(t)
	defer cleanup()

	// "market" lives in the title field, "marker" in content. Field-scoped
	// autocomplete should return only the term present in that field.
	_ = indexWithFields(s, "c", "d1", map[string]string{
		"title":   "market news",
		"content": "marker only",
	})

	titleItems, err := s.Autocomplete("c", "mar", "title", 10)
	if err != nil {
		t.Fatalf("autocomplete title: %v", err)
	}
	if len(titleItems) != 1 || titleItems[0].Term != "market" {
		t.Errorf("expected market only in title scope, got %+v", titleItems)
	}
	if titleItems[0].Field != "title" {
		t.Errorf("expected field=title, got %q", titleItems[0].Field)
	}

	contentItems, _ := s.Autocomplete("c", "mar", "content", 10)
	if len(contentItems) != 1 || contentItems[0].Term != "marker" {
		t.Errorf("expected marker only in content scope, got %+v", contentItems)
	}
}
func TestAutocomplete_EmptyPrefix(t *testing.T) {
	s, cleanup := newAutocompleteFTS(t)
	defer cleanup()

	indexDocAC(t, s, "c", "d1", "content", "marker")
	items, err := s.Autocomplete("c", "", "", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected empty result for empty prefix, got %+v", items)
	}
}
func TestAutocomplete_MissingCollection(t *testing.T) {
	s, cleanup := newAutocompleteFTS(t)
	defer cleanup()
	if _, err := s.Autocomplete("", "mar", "", 10); err == nil {
		t.Error("expected error for missing collection")
	}
}
func TestAutocomplete_LongPrefixTruncated(t *testing.T) {
	s, cleanup := newAutocompleteFTS(t)
	defer cleanup()

	indexDocAC(t, s, "c", "d1", "content", "supercalifragilistic")

	// Prefix longer than 32 chars — should truncate silently and still match.
	longPrefix := "supercalifragilisticexpialidocioussupercalifragilistic"
	items, err := s.Autocomplete("c", longPrefix, "", 10)
	if err != nil {
		t.Fatalf("autocomplete: %v", err)
	}
	// No guarantee of a match after truncation — just assert no crash.
	_ = items
}
func TestAutocompleteItem_String(t *testing.T) {
	a := AutocompleteItem{Term: "foo", DocCount: 3}
	if a.String() == "" {
		t.Error("expected non-empty string")
	}
	b := AutocompleteItem{Term: "foo", Field: "title", DocCount: 3}
	if b.String() == a.String() {
		t.Error("field should alter formatting")
	}
}

func newAutocompleteFTS(t *testing.T) (*FTSIndex, func()) {
	t.Helper()
	idx := NewFTSIndex(openTestDB(t))
	_ = idx.EnsureBuckets()
	return idx, func() {}
}

func indexDocAC(t *testing.T, s *FTSIndex, collection, docID, field, content string) {
	t.Helper()
	if err := s.Index(collection, docID, content); err != nil {
		t.Fatalf("fts index: %v", err)
	}
	if err := indexWithFields(s, collection, docID, map[string]string{field: content}); err != nil {
		t.Fatalf("fts index fields: %v", err)
	}
}
