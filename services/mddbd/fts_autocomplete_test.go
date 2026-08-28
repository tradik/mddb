package main

import (
	"mddb/internal/fts"
	"net/http/httptest"
	"testing"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// newAutocompleteServer creates a server with FTS buckets ready for
// Autocomplete. Separate helper so each test can seed terms deterministically
// without depending on the full batch ingest path.
func newAutocompleteServer(t *testing.T) (*Server, func()) {
	t.Helper()
	s, cleanup := newTestServer(t)
	idx := fts.NewFTSIndex(s.DB)
	if err := idx.EnsureBuckets(); err != nil {
		cleanup()
		t.Fatalf("ensure fts buckets: %v", err)
	}
	s.FTSIndex = idx
	return s, cleanup
}

// indexDoc is the shortest path to put a single-field document into both the
// global and field-scoped indices so Autocomplete can find it. Real document
// ingestion goes through the batch processor; tests don't need that weight.
func indexDoc(t *testing.T, s *Server, collection, docID, field, content string) {
	t.Helper()
	if err := s.FTSIndex.Index(collection, docID, content); err != nil {
		t.Fatalf("fts index: %v", err)
	}
	if err := s.FTSIndex.IndexFields(collection, docID, map[string]string{field: content}); err != nil {
		t.Fatalf("fts index fields: %v", err)
	}
}

func TestHandleAutocomplete_HTTP(t *testing.T) {
	s, cleanup := newAutocompleteServer(t)
	defer cleanup()

	indexDoc(t, s, "blog", "p1", "title", "markdown")
	indexDoc(t, s, "blog", "p2", "title", "marker pens")

	req := httptest.NewRequest("GET", "/v1/autocomplete?collection=blog&q=mar&topN=5", nil)
	rec := httptest.NewRecorder()
	s.handleAutocomplete(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp AutocompleteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Query != "mar" {
		t.Errorf("expected query=mar, got %q", resp.Query)
	}
	if resp.Total == 0 {
		t.Errorf("expected matches, got empty items")
	}
}

func TestHandleAutocomplete_MissingCollection(t *testing.T) {
	s, cleanup := newAutocompleteServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/autocomplete?q=mar", nil)
	rec := httptest.NewRecorder()
	s.handleAutocomplete(rec, req)
	if rec.Code != 400 {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleAutocomplete_EmptyQueryReturnsEmpty(t *testing.T) {
	s, cleanup := newAutocompleteServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/autocomplete?collection=c&q=", nil)
	rec := httptest.NewRecorder()
	s.handleAutocomplete(rec, req)
	if rec.Code != 200 {
		t.Errorf("expected 200 even for empty query, got %d", rec.Code)
	}
	var resp AutocompleteResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Items) != 0 {
		t.Errorf("expected empty items, got %+v", resp.Items)
	}
}

func TestHandleAutocomplete_WrongMethod(t *testing.T) {
	s, cleanup := newAutocompleteServer(t)
	defer cleanup()

	req := httptest.NewRequest("POST", "/v1/autocomplete", nil)
	rec := httptest.NewRecorder()
	s.handleAutocomplete(rec, req)
	if rec.Code != 405 {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestParseIntParam(t *testing.T) {
	tests := []struct {
		qs   string
		key  string
		def  int
		want int
	}{
		{"n=42", "n", 10, 42},
		{"n=abc", "n", 10, 10},
		{"n=-5", "n", 10, 10}, // negative ignored
		{"n=0", "n", 10, 10},  // zero → default
		{"", "n", 7, 7},       // missing → default
	}
	for _, tc := range tests {
		req := httptest.NewRequest("GET", "/?"+tc.qs, nil)
		if got := parseIntParam(req, tc.key, tc.def); got != tc.want {
			t.Errorf("parseIntParam(%q)=%d; want %d", tc.qs, got, tc.want)
		}
	}
}

// Keep reference to the bolt import so gofmt doesn't reorder it — we touch
// bolt buckets indirectly via the FTSIndex helpers.
var _ = bolt.Open
