package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

func TestHandleAggregate_MissingCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/v1/aggregate", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	s.handleAggregate(w, req)

	if w.Result().StatusCode != 400 {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleAggregate_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/v1/aggregate", strings.NewReader(`not json`))
	w := httptest.NewRecorder()
	s.handleAggregate(w, req)

	if w.Result().StatusCode != 400 {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestHandleAggregate_MethodNotAllowed(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/v1/aggregate", nil)
	w := httptest.NewRecorder()
	s.handleAggregate(w, req)

	if w.Result().StatusCode != 405 {
		t.Fatalf("expected 405, got %d", w.Result().StatusCode)
	}
}

func TestHandleAggregate_EmptyCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/v1/aggregate",
		strings.NewReader(`{"collection":"empty","facets":[{"field":"tag"}]}`))
	w := httptest.NewRecorder()
	s.handleAggregate(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	var resp AggregateResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.TotalDocs != 0 {
		t.Fatalf("expected 0 totalDocs, got %d", resp.TotalDocs)
	}
}

func TestHandleAggregate_Facets(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "p1", "en", "Post 1", map[string][]string{"category": {"tech"}, "author": {"Alice"}})
	addTestDoc(t, s, "blog", "p2", "en", "Post 2", map[string][]string{"category": {"tech"}, "author": {"Bob"}})
	addTestDoc(t, s, "blog", "p3", "en", "Post 3", map[string][]string{"category": {"news"}, "author": {"Alice"}})

	req := httptest.NewRequest(http.MethodPost, "/v1/aggregate",
		strings.NewReader(`{"collection":"blog","facets":[{"field":"category"},{"field":"author","orderBy":"value"}]}`))
	w := httptest.NewRecorder()
	s.handleAggregate(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d; body: %s", w.Result().StatusCode, w.Body.String())
	}

	var resp AggregateResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.TotalDocs != 3 {
		t.Fatalf("expected 3 totalDocs, got %d", resp.TotalDocs)
	}

	// Check category facet (ordered by count desc)
	catFacets := resp.Facets["category"]
	if len(catFacets) != 2 {
		t.Fatalf("expected 2 category facets, got %d", len(catFacets))
	}
	if catFacets[0].Value != "tech" || catFacets[0].Count != 2 {
		t.Fatalf("expected tech:2, got %s:%d", catFacets[0].Value, catFacets[0].Count)
	}
	if catFacets[1].Value != "news" || catFacets[1].Count != 1 {
		t.Fatalf("expected news:1, got %s:%d", catFacets[1].Value, catFacets[1].Count)
	}

	// Check author facet (ordered by value asc)
	authFacets := resp.Facets["author"]
	if len(authFacets) != 2 {
		t.Fatalf("expected 2 author facets, got %d", len(authFacets))
	}
	if authFacets[0].Value != "Alice" {
		t.Fatalf("expected Alice first (alphabetical), got %s", authFacets[0].Value)
	}
}

func TestHandleAggregate_FacetsWithFilter(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "p1", "en", "Post 1", map[string][]string{"category": {"tech"}, "status": {"published"}})
	addTestDoc(t, s, "blog", "p2", "en", "Post 2", map[string][]string{"category": {"tech"}, "status": {"draft"}})
	addTestDoc(t, s, "blog", "p3", "en", "Post 3", map[string][]string{"category": {"news"}, "status": {"published"}})

	req := httptest.NewRequest(http.MethodPost, "/v1/aggregate",
		strings.NewReader(`{"collection":"blog","filterMeta":{"status":["published"]},"facets":[{"field":"category"}]}`))
	w := httptest.NewRecorder()
	s.handleAggregate(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	var resp AggregateResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.TotalDocs != 2 {
		t.Fatalf("expected 2 totalDocs (filtered), got %d", resp.TotalDocs)
	}

	catFacets := resp.Facets["category"]
	if len(catFacets) != 2 {
		t.Fatalf("expected 2 category facets, got %d", len(catFacets))
	}
	// Both tech and news have 1 published doc each
	for _, f := range catFacets {
		if f.Count != 1 {
			t.Fatalf("expected count 1 for %s, got %d", f.Value, f.Count)
		}
	}
}

func TestHandleAggregate_Histogram(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "p1", "en", "Post 1", nil)
	addTestDoc(t, s, "blog", "p2", "en", "Post 2", nil)
	addTestDoc(t, s, "blog", "p3", "en", "Post 3", nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/aggregate",
		strings.NewReader(`{"collection":"blog","histograms":[{"field":"addedAt","interval":"month"}]}`))
	w := httptest.NewRecorder()
	s.handleAggregate(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	var resp AggregateResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	histBuckets := resp.Histograms["addedAt"]
	if len(histBuckets) == 0 {
		t.Fatal("expected at least 1 histogram bucket")
	}
	// All 3 docs should be in the same month bucket (added just now)
	total := 0
	for _, b := range histBuckets {
		total += b.Count
	}
	if total != 3 {
		t.Fatalf("expected histogram total of 3 docs, got %d", total)
	}
}

func TestHandleAggregate_MaxFacetSize(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add docs with many different tags
	for i := 0; i < 10; i++ {
		key := "p" + string(rune('a'+i))
		tag := "tag" + string(rune('a'+i))
		addTestDoc(t, s, "blog", key, "en", "content", map[string][]string{"tag": {tag}})
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/aggregate",
		strings.NewReader(`{"collection":"blog","facets":[{"field":"tag"}],"maxFacetSize":3}`))
	w := httptest.NewRecorder()
	s.handleAggregate(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	var resp AggregateResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	tagFacets := resp.Facets["tag"]
	if len(tagFacets) > 3 {
		t.Fatalf("expected at most 3 tag facets, got %d", len(tagFacets))
	}
}

func TestAggregate_Internal(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "test", "d1", "en", "Hello", map[string][]string{"type": {"a"}})
	addTestDoc(t, s, "test", "d2", "en", "World", map[string][]string{"type": {"b"}})

	resp, err := s.aggregate(&AggregateRequest{
		Collection: "test",
		Facets:     []FacetRequest{{Field: "type"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalDocs != 2 {
		t.Fatalf("expected 2, got %d", resp.TotalDocs)
	}
	if len(resp.Facets["type"]) != 2 {
		t.Fatalf("expected 2 type facets, got %d", len(resp.Facets["type"]))
	}
}

// --- Storage backend tests ---

func TestMemoryBackend_Basic(t *testing.T) {
	m := NewMemoryBackend()
	defer func() { _ = m.Close() }()

	if m.Name() != "memory" {
		t.Fatalf("expected name 'memory', got %q", m.Name())
	}

	// Put and get
	if err := m.PutDoc("col", "d1", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	data, err := m.GetDoc("col", "d1")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("expected 'hello', got %q", string(data))
	}

	// Get non-existent
	data, err = m.GetDoc("col", "missing")
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		t.Fatal("expected nil for missing doc")
	}

	// Delete
	if err := m.DeleteDoc("col", "d1"); err != nil {
		t.Fatal(err)
	}
	data, _ = m.GetDoc("col", "d1")
	if data != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestMemoryBackend_ByKey(t *testing.T) {
	m := NewMemoryBackend()
	defer func() { _ = m.Close() }()

	if err := m.PutByKey("col", "mykey", "en", "doc123"); err != nil {
		t.Fatal(err)
	}
	id, err := m.GetByKey("col", "mykey", "en")
	if err != nil {
		t.Fatal(err)
	}
	if id != "doc123" {
		t.Fatalf("expected doc123, got %q", id)
	}

	// Delete
	if err := m.DeleteByKey("col", "mykey", "en"); err != nil {
		t.Fatal(err)
	}
	id, _ = m.GetByKey("col", "mykey", "en")
	if id != "" {
		t.Fatalf("expected empty after delete, got %q", id)
	}
}

func TestMemoryBackend_ListDocs(t *testing.T) {
	m := NewMemoryBackend()
	defer func() { _ = m.Close() }()

	_ = m.PutDoc("col", "d1", []byte("a"))
	_ = m.PutDoc("col", "d2", []byte("b"))
	_ = m.PutDoc("other", "d3", []byte("c"))

	var ids []string
	err := m.ListDocs("col", func(docID string, data []byte) error {
		ids = append(ids, docID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 docs in 'col', got %d", len(ids))
	}
}

func TestMemoryBackend_DataIsolation(t *testing.T) {
	m := NewMemoryBackend()
	defer func() { _ = m.Close() }()

	// Verify data is copied, not referenced
	original := []byte("original")
	_ = m.PutDoc("col", "d1", original)

	// Modify original
	original[0] = 'X'

	data, _ := m.GetDoc("col", "d1")
	if string(data) != "original" {
		t.Fatalf("data should be isolated from original, got %q", string(data))
	}
}

func TestBackendRegistry(t *testing.T) {
	defaultBackend := NewMemoryBackend()
	reg := NewBackendRegistry(defaultBackend)

	// Default fallback
	if reg.Get("any").Name() != "memory" {
		t.Fatal("expected default backend")
	}

	// Register custom
	custom := NewMemoryBackend()
	reg.Register("special", custom)
	if reg.Get("special") != custom {
		t.Fatal("expected custom backend for 'special'")
	}
	if reg.Get("other").Name() != "memory" {
		t.Fatal("expected default for unregistered collection")
	}

	// Deregister
	reg.Deregister("special")
	if reg.Get("special") != defaultBackend {
		t.Fatal("expected fallback to default after deregister")
	}
}
