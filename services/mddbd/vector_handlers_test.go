package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// mockEmbedding is a minimal embedding.Provider for testing.
type mockEmbedding struct {
	dims  int
	model string
}

func (m *mockEmbedding) Embed(_ context.Context, _ string) ([]float32, error) {
	vec := make([]float32, m.dims)
	for i := range vec {
		vec[i] = 0.1
	}
	return vec, nil
}

func (m *mockEmbedding) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		v, _ := m.Embed(context.Background(), texts[i])
		result[i] = v
	}
	return result, nil
}

func (m *mockEmbedding) Model() string   { return m.model }
func (m *mockEmbedding) Dimensions() int { return m.dims }

// ---------- 1. handleVectorSearch - missing collection ----------

func TestHandleVectorSearch_MissingCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.VectorIndex.SetReady()

	payload := VectorSearchRequest{
		Collection: "",
		Query:      "test query",
	}
	rec := doRequest(t, s.handleVectorSearch, payload)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "missing collection") {
		t.Errorf("expected 'missing collection' error, got %s", rec.Body.String())
	}
}

// ---------- 2. handleVectorSearch - missing query and queryVector ----------

func TestHandleVectorSearch_MissingQueryAndVector(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.VectorIndex.SetReady()

	payload := VectorSearchRequest{
		Collection: "blog",
		Query:      "",
	}
	rec := doRequest(t, s.handleVectorSearch, payload)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "query") {
		t.Errorf("expected error about query, got %s", rec.Body.String())
	}
}

// ---------- 3. handleVectorSearch - index not ready ----------

func TestHandleVectorSearch_IndexNotReady(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Do NOT call SetReady -- index is not ready
	payload := VectorSearchRequest{
		Collection:  "blog",
		QueryVector: []float32{0.1, 0.2, 0.3},
	}
	rec := doRequest(t, s.handleVectorSearch, payload)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "loading") {
		t.Errorf("expected 'loading' message, got %s", rec.Body.String())
	}
}

// ---------- 4. handleVectorSearch - no embedding provider and no queryVector ----------

func TestHandleVectorSearch_NoProviderNoVector(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.VectorIndex.SetReady()
	s.Embedding = nil

	payload := VectorSearchRequest{
		Collection: "blog",
		Query:      "find something",
	}
	rec := doRequest(t, s.handleVectorSearch, payload)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no embedding provider") {
		t.Errorf("expected 'no embedding provider' error, got %s", rec.Body.String())
	}
}

// ---------- 5. handleVectorSearch - with queryVector, empty collection returns empty results ----------

func TestHandleVectorSearch_EmptyCollectionResults(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.VectorIndex.SetReady()

	payload := VectorSearchRequest{
		Collection:  "nonexistent",
		QueryVector: []float32{0.1, 0.2, 0.3},
		TopK:        5,
	}
	rec := doRequest(t, s.handleVectorSearch, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp VectorSearchResponseHTTP
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Total != 0 {
		t.Errorf("expected 0 results, got %d", resp.Total)
	}
	if len(resp.Results) != 0 {
		t.Errorf("expected empty results slice, got %d", len(resp.Results))
	}
}

// ---------- 6. handleVectorSearch - with mock embedding provider ----------

func TestHandleVectorSearch_WithMockEmbedding(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.VectorIndex.SetReady()
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}

	// Add a document and its vector to the index
	doc := addTestDoc(t, s, "blog", "post1", "en", "# Hello World", nil)
	s.VectorIndex.Add("blog", doc.ID, []float32{0.1, 0.1, 0.1})

	payload := VectorSearchRequest{
		Collection: "blog",
		Query:      "hello",
		TopK:       5,
	}
	rec := doRequest(t, s.handleVectorSearch, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp VectorSearchResponseHTTP
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Model != "test-model" {
		t.Errorf("expected model test-model, got %q", resp.Model)
	}
	if resp.Dimensions != 3 {
		t.Errorf("expected dimensions 3, got %d", resp.Dimensions)
	}
}

// ---------- 7. handleVectorSearch - bad JSON body ----------

func TestHandleVectorSearch_BadJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.VectorIndex.SetReady()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/vector-search", bytes.NewReader([]byte("not-json")))
	req.Header.Set("Content-Type", "application/json")
	s.handleVectorSearch(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// ---------- 8. handleVectorReindex - missing collection ----------

func TestHandleVectorReindex_MissingCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := VectorReindexRequestHTTP{
		Collection: "",
	}
	rec := doRequest(t, s.handleVectorReindex, payload)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "missing collection") {
		t.Errorf("expected 'missing collection' error, got %s", rec.Body.String())
	}
}

// ---------- 9. handleVectorReindex - no embedding provider ----------

func TestHandleVectorReindex_NoProvider(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = nil

	payload := VectorReindexRequestHTTP{
		Collection: "blog",
	}
	rec := doRequest(t, s.handleVectorReindex, payload)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no embedding provider") {
		t.Errorf("expected 'no embedding provider' error, got %s", rec.Body.String())
	}
}

// ---------- 10. handleVectorReindex - empty collection succeeds with zero counts ----------

func TestHandleVectorReindex_EmptyCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}

	payload := VectorReindexRequestHTTP{
		Collection: "empty-coll",
	}
	rec := doRequest(t, s.handleVectorReindex, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	embedded, _ := resp["embedded"].(float64)
	skipped, _ := resp["skipped"].(float64)
	failed, _ := resp["failed"].(float64)
	if embedded != 0 || skipped != 0 || failed != 0 {
		t.Errorf("expected all 0 counts for empty collection, got embedded=%v skipped=%v failed=%v",
			embedded, skipped, failed)
	}
}

// ---------- 11. handleVectorReindex - bad JSON body ----------

func TestHandleVectorReindex_BadJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/vector-reindex", bytes.NewReader([]byte("{invalid")))
	req.Header.Set("Content-Type", "application/json")
	s.handleVectorReindex(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// ---------- 12. handleVectorReindex - with documents embeds them ----------

func TestHandleVectorReindex_WithDocuments(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}

	addTestDoc(t, s, "blog", "p1", "en", "# Post 1 content", nil)
	addTestDoc(t, s, "blog", "p2", "en", "# Post 2 content", nil)

	payload := VectorReindexRequestHTTP{
		Collection: "blog",
		Force:      true,
	}
	rec := doRequest(t, s.handleVectorReindex, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	embedded, _ := resp["embedded"].(float64)
	if embedded != 2 {
		t.Errorf("expected 2 embedded documents, got %v", embedded)
	}
}

// ---------- 13. handleVectorStats - no embedding provider ----------

func TestHandleVectorStats_NoProvider(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = nil
	s.VectorIndex.SetReady()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/vector-stats", nil)
	s.handleVectorStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	enabled, _ := resp["enabled"].(bool)
	if enabled {
		t.Error("expected enabled=false when no embedding provider")
	}
}

// ---------- 14. handleVectorStats - with embedding provider ----------

func TestHandleVectorStats_WithProvider(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = &mockEmbedding{dims: 768, model: "nomic-embed-text"}
	s.VectorIndex.SetReady()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/vector-stats", nil)
	s.handleVectorStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	enabled, _ := resp["enabled"].(bool)
	if !enabled {
		t.Error("expected enabled=true with embedding provider")
	}

	model, _ := resp["model"].(string)
	if model != "nomic-embed-text" {
		t.Errorf("expected model nomic-embed-text, got %q", model)
	}

	dims, _ := resp["dimensions"].(float64)
	if dims != 768 {
		t.Errorf("expected dimensions 768, got %v", dims)
	}
}

// ---------- 15. handleVectorStats - index_ready field ----------

func TestHandleVectorStats_IndexReady(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = nil

	// Not ready
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/vector-stats", nil)
	s.handleVectorStats(rec, req)

	var resp1 map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ready1, _ := resp1["index_ready"].(bool)
	if ready1 {
		t.Error("expected index_ready=false before SetReady")
	}

	// Now mark ready
	s.VectorIndex.SetReady()
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/v1/vector-stats", nil)
	s.handleVectorStats(rec2, req2)

	var resp2 map[string]interface{}
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ready2, _ := resp2["index_ready"].(bool)
	if !ready2 {
		t.Error("expected index_ready=true after SetReady")
	}
}

// ---------- 16. handleVectorStats - collections with docs and vectors ----------

func TestHandleVectorStats_CollectionCounts(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}
	s.VectorIndex.SetReady()

	// Add documents
	doc1 := addTestDoc(t, s, "blog", "p1", "en", "# Post 1", nil)
	addTestDoc(t, s, "blog", "p2", "en", "# Post 2", nil)

	// Add a vector for just one doc
	vec := []float32{0.1, 0.2, 0.3}
	if err := s.VectorStore.Put("blog", doc1.ID, vec, "test", "hash1"); err != nil {
		t.Fatalf("put vector: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/vector-stats", nil)
	s.handleVectorStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	collections, ok := resp["collections"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected collections map, got %T", resp["collections"])
	}

	blogStats, ok := collections["blog"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected blog collection stats, got %v", collections["blog"])
	}

	totalDocs, _ := blogStats["total_documents"].(float64)
	embeddedDocs, _ := blogStats["embedded_documents"].(float64)
	if totalDocs != 2 {
		t.Errorf("expected 2 total_documents, got %v", totalDocs)
	}
	if embeddedDocs != 1 {
		t.Errorf("expected 1 embedded_documents, got %v", embeddedDocs)
	}
}
