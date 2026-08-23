package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

func TestHandleMetaKeys(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add documents with metadata
	addDoc(t, s, `{"collection":"blog","key":"p1","lang":"en","contentMd":"hello","meta":{"tag":["go","rust"],"author":["alice"]}}`)
	addDoc(t, s, `{"collection":"blog","key":"p2","lang":"en","contentMd":"world","meta":{"tag":["go","python"],"level":["beginner"]}}`)
	addDoc(t, s, `{"collection":"other","key":"o1","lang":"en","contentMd":"test","meta":{"cat":["x"]}}`)

	t.Run("returns meta keys for collection", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/meta-keys?collection=blog", nil)
		w := httptest.NewRecorder()
		s.handleMetaKeys(w, req)

		if w.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Meta map[string][]string `json:"meta"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}

		// Check tag values
		tags := resp.Meta["tag"]
		if len(tags) != 3 {
			t.Fatalf("expected 3 tag values, got %d: %v", len(tags), tags)
		}
		wantTags := map[string]bool{"go": true, "rust": true, "python": true}
		for _, v := range tags {
			if !wantTags[v] {
				t.Errorf("unexpected tag value: %s", v)
			}
		}

		// Check author
		authors := resp.Meta["author"]
		if len(authors) != 1 || authors[0] != "alice" {
			t.Fatalf("expected [alice], got %v", authors)
		}

		// Check level
		levels := resp.Meta["level"]
		if len(levels) != 1 || levels[0] != "beginner" {
			t.Fatalf("expected [beginner], got %v", levels)
		}

		// Should not include 'other' collection's metadata
		if _, ok := resp.Meta["cat"]; ok {
			t.Error("should not include metadata from other collections")
		}
	})

	t.Run("empty collection returns empty map", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/meta-keys?collection=nonexistent", nil)
		w := httptest.NewRecorder()
		s.handleMetaKeys(w, req)

		if w.Code != 200 {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		var resp struct {
			Meta map[string][]string `json:"meta"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Meta) != 0 {
			t.Fatalf("expected empty meta, got %v", resp.Meta)
		}
	})

	t.Run("missing collection returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/meta-keys", nil)
		w := httptest.NewRecorder()
		s.handleMetaKeys(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})

	t.Run("wrong method returns 405", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/v1/meta-keys?collection=blog", nil)
		w := httptest.NewRecorder()
		s.handleMetaKeys(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", w.Code)
		}
	})
}

// addDoc is a helper to add a document via the HTTP handler.
func addDoc(t *testing.T, s *Server, body string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/add", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleAdd(w, req)
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("addDoc failed: %d %s", w.Code, w.Body.String())
	}
}
