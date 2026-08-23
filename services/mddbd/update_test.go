package main

import (
	"mddb/internal/storage"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

func TestHandleUpdate(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add a document first
	addDocUpdate(t, s, `{"collection":"blog","key":"p1","lang":"en","contentMd":"hello world","meta":{"tag":["go","rust"]}}`)

	t.Run("patch meta only", func(t *testing.T) {
		body := `{"collection":"blog","key":"p1","lang":"en","meta":{"tag":["go","updated"]}}`
		req := httptest.NewRequest("PATCH", "/v1/update", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handleUpdate(w, req)

		if w.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var doc storage.Doc
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		// Meta should be updated
		if len(doc.Meta["tag"]) != 2 || doc.Meta["tag"][1] != "updated" {
			t.Fatalf("expected meta tag [go, updated], got %v", doc.Meta["tag"])
		}
		// Content should be unchanged
		if doc.ContentMD != "hello world" {
			t.Fatalf("content should be unchanged, got %q", doc.ContentMD)
		}
	})

	t.Run("patch content only", func(t *testing.T) {
		body := `{"collection":"blog","key":"p1","lang":"en","contentMd":"new content"}`
		req := httptest.NewRequest("PATCH", "/v1/update", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handleUpdate(w, req)

		if w.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var doc storage.Doc
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.ContentMD != "new content" {
			t.Fatalf("expected new content, got %q", doc.ContentMD)
		}
		// Meta should still be from previous update
		if len(doc.Meta["tag"]) != 2 || doc.Meta["tag"][1] != "updated" {
			t.Fatalf("meta should be unchanged, got %v", doc.Meta["tag"])
		}
	})

	t.Run("patch both meta and content", func(t *testing.T) {
		body := `{"collection":"blog","key":"p1","lang":"en","meta":{"category":["tech"]},"contentMd":"both changed"}`
		req := httptest.NewRequest("PATCH", "/v1/update", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handleUpdate(w, req)

		if w.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var doc storage.Doc
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.ContentMD != "both changed" {
			t.Fatalf("expected both changed, got %q", doc.ContentMD)
		}
		if doc.Meta["category"] == nil || doc.Meta["category"][0] != "tech" {
			t.Fatalf("meta should have category=tech, got %v", doc.Meta)
		}
		// Old meta key "tag" should be gone (meta is replaced entirely)
		if doc.Meta["tag"] != nil {
			t.Fatalf("old meta key 'tag' should be gone after full meta replace, got %v", doc.Meta["tag"])
		}
	})

	t.Run("clear meta with empty object", func(t *testing.T) {
		body := `{"collection":"blog","key":"p1","lang":"en","meta":{}}`
		req := httptest.NewRequest("PATCH", "/v1/update", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handleUpdate(w, req)

		if w.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var doc storage.Doc
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.Meta) != 0 {
			t.Fatalf("meta should be empty after clear, got %v", doc.Meta)
		}
	})

	t.Run("not found returns 404", func(t *testing.T) {
		body := `{"collection":"blog","key":"nonexistent","lang":"en","meta":{"tag":["x"]}}`
		req := httptest.NewRequest("PATCH", "/v1/update", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handleUpdate(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("missing required fields returns 400", func(t *testing.T) {
		body := `{"collection":"blog","meta":{"tag":["x"]}}`
		req := httptest.NewRequest("PATCH", "/v1/update", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handleUpdate(w, req)

		if w.Code != 400 {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})

	t.Run("no fields to update returns 400", func(t *testing.T) {
		body := `{"collection":"blog","key":"p1","lang":"en"}`
		req := httptest.NewRequest("PATCH", "/v1/update", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handleUpdate(w, req)

		if w.Code != 400 {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})

	t.Run("wrong method returns 405", func(t *testing.T) {
		body := `{"collection":"blog","key":"p1","lang":"en","meta":{"tag":["x"]}}`
		req := httptest.NewRequest("POST", "/v1/update", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handleUpdate(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", w.Code)
		}
	})

	t.Run("patch TTL", func(t *testing.T) {
		body := `{"collection":"blog","key":"p1","lang":"en","ttl":3600}`
		req := httptest.NewRequest("PATCH", "/v1/update", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handleUpdate(w, req)

		if w.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var doc storage.Doc
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.ExpiresAt == 0 {
			t.Fatal("expected non-zero expiresAt after TTL update")
		}
	})
}

func TestHandleDocMeta(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add a document
	addDocUpdate(t, s, `{"collection":"blog","key":"p1","lang":"en","contentMd":"hello world","meta":{"tag":["go"],"author":["alice"]}}`)

	t.Run("returns metadata without content", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/doc-meta?collection=blog&key=p1&lang=en", nil)
		w := httptest.NewRecorder()
		s.handleDocMeta(w, req)

		if w.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}

		// Should have meta
		meta, ok := resp["meta"].(map[string]interface{})
		if !ok || meta == nil {
			t.Fatal("expected meta in response")
		}

		// Should NOT have contentMd
		if _, ok := resp["contentMd"]; ok {
			t.Fatal("response should NOT include contentMd")
		}

		// Should have timestamps
		if _, ok := resp["addedAt"]; !ok {
			t.Fatal("expected addedAt in response")
		}
		if _, ok := resp["updatedAt"]; !ok {
			t.Fatal("expected updatedAt in response")
		}
	})

	t.Run("not found returns 404", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/doc-meta?collection=blog&key=nonexistent&lang=en", nil)
		w := httptest.NewRecorder()
		s.handleDocMeta(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w.Code)
		}
	})

	t.Run("missing params returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/doc-meta?collection=blog", nil)
		w := httptest.NewRecorder()
		s.handleDocMeta(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})

	t.Run("wrong method returns 405", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/v1/doc-meta?collection=blog&key=p1&lang=en", nil)
		w := httptest.NewRecorder()
		s.handleDocMeta(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", w.Code)
		}
	})

	t.Run("default lang is en", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/doc-meta?collection=blog&key=p1", nil)
		w := httptest.NewRecorder()
		s.handleDocMeta(w, req)

		if w.Code != 200 {
			t.Fatalf("expected 200 with default lang, got %d: %s", w.Code, w.Body.String())
		}
	})
}

// addDocUpdate is a helper to add a document via the HTTP handler for update tests.
func addDocUpdate(t *testing.T, s *Server, body string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/add", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleAdd(w, req)
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("addDocUpdate failed: %d %s", w.Code, w.Body.String())
	}
}
