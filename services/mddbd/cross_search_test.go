package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleCrossSearch_MissingTargetCollections(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/v1/cross-search",
		strings.NewReader(`{"query":"test"}`))
	w := httptest.NewRecorder()
	s.handleCrossSearch(w, req)

	if w.Result().StatusCode != 400 {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleCrossSearch_MissingQuerySource(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/v1/cross-search",
		strings.NewReader(`{"targetCollections":["blog"]}`))
	w := httptest.NewRecorder()
	s.handleCrossSearch(w, req)

	if w.Result().StatusCode != 400 {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleCrossSearch_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/v1/cross-search",
		strings.NewReader(`not json`))
	w := httptest.NewRecorder()
	s.handleCrossSearch(w, req)

	if w.Result().StatusCode != 400 {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleCrossSearch_UnknownAlgorithm(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/v1/cross-search",
		strings.NewReader(`{"targetCollections":["blog"],"queryVector":[1.0,0.0],"algorithm":"nonexistent"}`))
	w := httptest.NewRecorder()
	s.handleCrossSearch(w, req)

	if w.Result().StatusCode != 400 {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleCrossSearch_WithQueryVector(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Mark vector index as ready
	s.VectorIndex.SetReady()

	// Add a doc and index its vector manually
	doc := addTestDoc(t, s, "blog", "post1", "en", "hello world", nil)
	vec := []float32{1.0, 0.0, 0.0}
	_ = s.VectorStore.Put("blog", doc.ID, vec, "test-model", "abc123")
	if err := s.VectorIndex.Add("blog", doc.ID, vec); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/cross-search",
		strings.NewReader(`{"targetCollections":["blog"],"queryVector":[1.0,0.0,0.0],"topK":5}`))
	w := httptest.NewRecorder()
	s.handleCrossSearch(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}
}

func TestHandleCrossSearch_SourceDocIDMissingCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.VectorIndex.SetReady()

	req := httptest.NewRequest(http.MethodPost, "/v1/cross-search",
		strings.NewReader(`{"targetCollections":["blog"],"sourceDocID":"doc1"}`))
	w := httptest.NewRecorder()
	s.handleCrossSearch(w, req)

	if w.Result().StatusCode != 400 {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}
