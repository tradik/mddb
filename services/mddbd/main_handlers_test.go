package main

import (
	"bytes"
	"fmt"
	"mddb/internal/sliceutil"
	"mddb/internal/storage"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// ---------------------------------------------------------------------------
// handleImportURL - HTTP handler tests
// ---------------------------------------------------------------------------

func TestMainHandleImportURL_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Start a local server that serves markdown content
	mdContent := `---
title: Remote storage.Doc
author: Alice
---
# Remote Document

Body content here.`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, mdContent)
	}))
	defer ts.Close()

	payload := ImportURLRequest{
		Collection: "docs",
		URL:        ts.URL + "/readme.md",
		Lang:       "en",
	}
	rec := doRequest(t, s.handleImportURL, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var doc storage.Doc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	// Key should be derived from URL: "readme"
	if doc.Key != "readme" {
		t.Errorf("expected key=readme, got %s", doc.Key)
	}
	if doc.ContentMD != "# Remote Document\n\nBody content here." {
		t.Errorf("unexpected content: %q", doc.ContentMD)
	}
}

func TestMainHandleImportURL_WithExplicitKey(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "# Hello World")
	}))
	defer ts.Close()

	payload := ImportURLRequest{
		Collection: "docs",
		URL:        ts.URL + "/something.md",
		Key:        "custom-key",
		Lang:       "en",
	}
	rec := doRequest(t, s.handleImportURL, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var doc storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	if doc.Key != "custom-key" {
		t.Errorf("expected key=custom-key, got %s", doc.Key)
	}
}

func TestMainHandleImportURL_MissingFields(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	cases := []struct {
		name    string
		payload ImportURLRequest
	}{
		{"missing collection", ImportURLRequest{URL: "http://example.com/x.md", Lang: "en"}},
		{"missing url", ImportURLRequest{Collection: "docs", Lang: "en"}},
		{"missing lang", ImportURLRequest{Collection: "docs", URL: "http://example.com/x.md"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(t, s.handleImportURL, tc.payload)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMainHandleImportURL_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/import-url", strings.NewReader("not json"))
	s.handleImportURL(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMainHandleImportURL_CannotDeriveKey(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// URL with root path -- cannot derive key
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "# Hello")
	}))
	defer ts.Close()

	payload := ImportURLRequest{
		Collection: "docs",
		URL:        ts.URL + "/",
		Lang:       "en",
	}
	rec := doRequest(t, s.handleImportURL, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "derive key") {
		t.Errorf("expected derive key error, got: %s", rec.Body.String())
	}
}

func TestMainHandleImportURL_FetchError(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	errTs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "broken", http.StatusInternalServerError)
	}))
	defer errTs.Close()

	payload := ImportURLRequest{
		Collection: "docs",
		URL:        errTs.URL + "/nonexistent.md",
		Key:        "test",
		Lang:       "en",
	}
	rec := doRequest(t, s.handleImportURL, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMainHandleImportURL_WithTTL(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "# TTL Content")
	}))
	defer ts.Close()

	payload := ImportURLRequest{
		Collection: "temp",
		URL:        ts.URL + "/ttl-doc.md",
		Key:        "ttl-doc",
		Lang:       "en",
		TTL:        3600,
	}
	rec := doRequest(t, s.handleImportURL, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var doc storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	if doc.ExpiresAt == 0 {
		t.Error("expected non-zero expiresAt with TTL")
	}
}

func TestMainHandleImportURL_MergesMeta(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	mdContent := `---
title: From Frontmatter
category: original
---
Body`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, mdContent)
	}))
	defer ts.Close()

	payload := ImportURLRequest{
		Collection: "docs",
		URL:        ts.URL + "/doc.md",
		Key:        "doc",
		Lang:       "en",
		Meta:       map[string][]string{"category": {"overridden"}, "extra": {"value"}},
	}
	rec := doRequest(t, s.handleImportURL, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var doc storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)

	// Request meta should override frontmatter for "category"
	if len(doc.Meta["category"]) != 1 || doc.Meta["category"][0] != "overridden" {
		t.Errorf("expected category=[overridden], got %v", doc.Meta["category"])
	}
	// Frontmatter "title" should be preserved
	if len(doc.Meta["title"]) != 1 || doc.Meta["title"][0] != "From Frontmatter" {
		t.Errorf("expected title=[From Frontmatter], got %v", doc.Meta["title"])
	}
	// Request "extra" should be present
	if len(doc.Meta["extra"]) != 1 || doc.Meta["extra"][0] != "value" {
		t.Errorf("expected extra=[value], got %v", doc.Meta["extra"])
	}
}

// ---------------------------------------------------------------------------
// handleSetTTL - HTTP handler tests
// ---------------------------------------------------------------------------

func TestMainHandleSetTTL_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "ttl-post", "en", "content", nil)

	payload := SetTTLRequest{
		Collection: "blog",
		Key:        "ttl-post",
		Lang:       "en",
		TTL:        3600,
	}
	rec := doRequest(t, s.handleSetTTL, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var doc storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	if doc.ExpiresAt == 0 {
		t.Error("expected non-zero expiresAt after setting TTL")
	}
	expected := time.Now().Unix() + 3600
	if doc.ExpiresAt < expected-5 || doc.ExpiresAt > expected+5 {
		t.Errorf("expiresAt %d not close to expected %d", doc.ExpiresAt, expected)
	}
}

func TestMainHandleSetTTL_ClearTTL(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add doc with TTL
	payload := AddRequest{
		Collection: "blog",
		Key:        "ttl-clear",
		Lang:       "en",
		ContentMD:  "content",
		TTL:        3600,
	}
	doRequest(t, s.handleAdd, payload)

	// Clear TTL
	clearPayload := SetTTLRequest{
		Collection: "blog",
		Key:        "ttl-clear",
		Lang:       "en",
		TTL:        0,
	}
	rec := doRequest(t, s.handleSetTTL, clearPayload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var doc storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &doc)
	if doc.ExpiresAt != 0 {
		t.Errorf("expected expiresAt=0 after clearing TTL, got %d", doc.ExpiresAt)
	}
}

func TestMainHandleSetTTL_NotFound(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := SetTTLRequest{
		Collection: "blog",
		Key:        "nonexistent",
		Lang:       "en",
		TTL:        100,
	}
	rec := doRequest(t, s.handleSetTTL, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not found") {
		t.Errorf("expected not found error, got: %s", rec.Body.String())
	}
}

func TestMainHandleSetTTL_MissingFields(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	cases := []struct {
		name    string
		payload SetTTLRequest
	}{
		{"missing collection", SetTTLRequest{Key: "k", Lang: "en", TTL: 100}},
		{"missing key", SetTTLRequest{Collection: "c", Lang: "en", TTL: 100}},
		{"missing lang", SetTTLRequest{Collection: "c", Key: "k", TTL: 100}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(t, s.handleSetTTL, tc.payload)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMainHandleSetTTL_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/set-ttl", strings.NewReader("{bad"))
	s.handleSetTTL(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMainHandleSetTTL_ReadOnlyMode(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Mode = ModeRead

	handler := s.guardWrite(s.handleSetTTL)
	payload := SetTTLRequest{Collection: "blog", Key: "k", Lang: "en", TTL: 100}
	rec := doRequest(t, handler, payload)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleFTS - HTTP handler tests
// ---------------------------------------------------------------------------

func TestMainHandleFTS_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add a document and index it in FTS
	doc := addTestDoc(t, s, "blog", "fts-post", "en", "golang is a great programming language for building systems", nil)
	if err := s.FTSIndex.Index("blog", doc.ID, doc.ContentMD); err != nil {
		t.Fatalf("FTS index: %v", err)
	}

	payload := FTSSearchRequest{
		Collection: "blog",
		Query:      "golang",
		Limit:      10,
	}
	rec := doRequest(t, s.handleFTS, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var resp FTSSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total == 0 {
		t.Error("expected at least 1 FTS result")
	}
}

func TestMainHandleFTS_MissingFields(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	cases := []struct {
		name    string
		payload FTSSearchRequest
	}{
		{"missing collection and query", FTSSearchRequest{}},
		{"missing query", FTSSearchRequest{Collection: "blog"}},
		{"missing collection", FTSSearchRequest{Query: "hello"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(t, s.handleFTS, tc.payload)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMainHandleFTS_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/fts", strings.NewReader("bad json"))
	s.handleFTS(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMainHandleFTS_DefaultLimit(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Index a doc so we have something to search
	doc := addTestDoc(t, s, "blog", "fts-limit", "en", "testing default limit behavior", nil)
	_ = s.FTSIndex.Index("blog", doc.ID, doc.ContentMD)

	payload := FTSSearchRequest{
		Collection: "blog",
		Query:      "testing",
		// Limit: 0 -- should default to 50
	}
	rec := doRequest(t, s.handleFTS, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestMainHandleFTS_NilFTSIndex(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	s.FTSIndex = nil

	payload := FTSSearchRequest{
		Collection: "blog",
		Query:      "test",
	}
	rec := doRequest(t, s.handleFTS, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not initialized") {
		t.Errorf("expected 'not initialized' error, got: %s", rec.Body.String())
	}
}

func TestMainHandleFTS_EmptyResults(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := FTSSearchRequest{
		Collection: "blog",
		Query:      "nonexistent-term-xyz",
		Limit:      10,
	}
	rec := doRequest(t, s.handleFTS, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp FTSSearchResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Total != 0 {
		t.Errorf("expected 0 results, got %d", resp.Total)
	}
}

// ---------------------------------------------------------------------------
// handleWebhooks - HTTP handler tests
// ---------------------------------------------------------------------------

func TestMainHandleWebhooks_GET_ListEmpty(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/webhooks", nil)
	s.handleWebhooks(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var hooks []interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &hooks)
	if len(hooks) != 0 {
		t.Errorf("expected 0 webhooks, got %d", len(hooks))
	}
}

func TestMainHandleWebhooks_POST_Register(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := RegisterWebhookRequest{
		URL:        "http://example.com/hook",
		Events:     []string{"doc.added", "doc.updated"},
		Collection: "blog",
	}
	b, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	s.handleWebhooks(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var wh map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &wh)
	if wh["id"] == "" {
		t.Error("expected non-empty webhook id")
	}
	if wh["url"] != "http://example.com/hook" {
		t.Errorf("expected url, got %v", wh["url"])
	}
}

func TestMainHandleWebhooks_POST_ReadOnly(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Mode = ModeRead

	payload := RegisterWebhookRequest{
		URL:    "http://example.com/hook",
		Events: []string{"doc.added"},
	}
	b, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	s.handleWebhooks(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestMainHandleWebhooks_POST_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks", strings.NewReader("{bad"))
	s.handleWebhooks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMainHandleWebhooks_MethodNotAllowed(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/webhooks", nil)
	s.handleWebhooks(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestMainHandleWebhooks_NilManager(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.WebhookManager = nil

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/webhooks", nil)
	s.handleWebhooks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleWebhookDelete - HTTP handler tests
// ---------------------------------------------------------------------------

func TestMainHandleWebhookDelete_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Register a webhook first
	wh, err := s.WebhookManager.Register("http://example.com/hook", []string{"doc.added"}, "")
	if err != nil {
		t.Fatal(err)
	}

	payload := DeleteWebhookRequest{ID: wh.ID}
	rec := doRequest(t, s.handleWebhookDelete, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"deleted"`) {
		t.Errorf("expected deleted status, got: %s", rec.Body.String())
	}

	// Verify it's gone
	hooks := s.WebhookManager.List()
	if len(hooks) != 0 {
		t.Errorf("expected 0 webhooks after delete, got %d", len(hooks))
	}
}

func TestMainHandleWebhookDelete_MissingID(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := DeleteWebhookRequest{}
	rec := doRequest(t, s.handleWebhookDelete, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "missing id") {
		t.Errorf("expected 'missing id' error, got: %s", rec.Body.String())
	}
}

func TestMainHandleWebhookDelete_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/delete", strings.NewReader("not json"))
	s.handleWebhookDelete(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMainHandleWebhookDelete_NilManager(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.WebhookManager = nil

	payload := DeleteWebhookRequest{ID: "some-id"}
	rec := doRequest(t, s.handleWebhookDelete, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleSchemaSet - HTTP handler tests
// ---------------------------------------------------------------------------

func TestMainHandleSchemaSet_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := map[string]string{
		"collection": "articles",
		"schema":     `{"required":["title"],"properties":{"title":{"type":"string"}}}`,
	}
	rec := doRequest(t, s.handleSchemaSet, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("expected ok status, got: %s", rec.Body.String())
	}
}

func TestMainHandleSchemaSet_MissingCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := map[string]string{"schema": `{}`}
	rec := doRequest(t, s.handleSchemaSet, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMainHandleSchemaSet_MissingSchema(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := map[string]string{"collection": "articles"}
	rec := doRequest(t, s.handleSchemaSet, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMainHandleSchemaSet_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/schema/set", strings.NewReader("not json"))
	s.handleSchemaSet(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMainHandleSchemaSet_InvalidSchemaContent(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := map[string]string{
		"collection": "articles",
		"schema":     "not valid json",
	}
	rec := doRequest(t, s.handleSchemaSet, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid schema JSON, got %d; body=%s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleSchemaGet - HTTP handler tests
// ---------------------------------------------------------------------------

func TestMainHandleSchemaGet_Found(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	schema := `{"required":["title"]}`
	_ = s.SchemaManager.Set("articles", schema)

	payload := map[string]string{"collection": "articles"}
	rec := doRequest(t, s.handleSchemaGet, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["enabled"] != true {
		t.Errorf("expected enabled=true, got %v", resp["enabled"])
	}
	if resp["schema"] != schema {
		t.Errorf("expected schema=%s, got %v", schema, resp["schema"])
	}
}

func TestMainHandleSchemaGet_NotFound(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := map[string]string{"collection": "nonexistent"}
	rec := doRequest(t, s.handleSchemaGet, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["enabled"] != false {
		t.Errorf("expected enabled=false for missing schema, got %v", resp["enabled"])
	}
}

func TestMainHandleSchemaGet_MissingCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := map[string]string{}
	rec := doRequest(t, s.handleSchemaGet, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMainHandleSchemaGet_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/schema/get", strings.NewReader("{bad"))
	s.handleSchemaGet(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleSchemaDelete - HTTP handler tests
// ---------------------------------------------------------------------------

func TestMainHandleSchemaDelete_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	_ = s.SchemaManager.Set("articles", `{"required":["title"]}`)

	payload := map[string]string{"collection": "articles"}
	rec := doRequest(t, s.handleSchemaDelete, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("expected ok status, got: %s", rec.Body.String())
	}

	// Verify deleted
	_, found := s.SchemaManager.Get("articles")
	if found {
		t.Error("expected schema to be deleted")
	}
}

func TestMainHandleSchemaDelete_MissingCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := map[string]string{}
	rec := doRequest(t, s.handleSchemaDelete, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMainHandleSchemaDelete_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/schema/delete", strings.NewReader("not json"))
	s.handleSchemaDelete(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleSchemaList - HTTP handler tests
// ---------------------------------------------------------------------------

func TestMainHandleSchemaList_Empty(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/schema/list", nil)
	s.handleSchemaList(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	schemas, ok := resp["schemas"].([]interface{})
	if !ok {
		t.Fatal("expected schemas array in response")
	}
	if len(schemas) != 0 {
		t.Errorf("expected 0 schemas, got %d", len(schemas))
	}
}

func TestMainHandleSchemaList_WithSchemas(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	_ = s.SchemaManager.Set("blog", `{"required":["author"]}`)
	_ = s.SchemaManager.Set("docs", `{"required":["version"]}`)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/schema/list", nil)
	s.handleSchemaList(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	schemas, ok := resp["schemas"].([]interface{})
	if !ok {
		t.Fatal("expected schemas array in response")
	}
	if len(schemas) != 2 {
		t.Errorf("expected 2 schemas, got %d", len(schemas))
	}
}

// ---------------------------------------------------------------------------
// handleValidate - HTTP handler tests
// ---------------------------------------------------------------------------

func TestMainHandleValidate_Valid(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	_ = s.SchemaManager.Set("articles", `{"required":["title"],"properties":{"title":{"type":"string"}}}`)

	payload := map[string]interface{}{
		"collection": "articles",
		"meta":       map[string][]string{"title": {"My Article"}},
	}
	rec := doRequest(t, s.handleValidate, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["valid"] != true {
		t.Errorf("expected valid=true, got %v", resp["valid"])
	}
}

func TestMainHandleValidate_Invalid(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	_ = s.SchemaManager.Set("articles", `{"required":["title"]}`)

	payload := map[string]interface{}{
		"collection": "articles",
		"meta":       map[string][]string{}, // missing required "title"
	}
	rec := doRequest(t, s.handleValidate, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["valid"] != false {
		t.Errorf("expected valid=false, got %v", resp["valid"])
	}
	errs, ok := resp["errors"].([]interface{})
	if !ok || len(errs) == 0 {
		t.Error("expected validation errors")
	}
}

func TestMainHandleValidate_NoSchema(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := map[string]interface{}{
		"collection": "no-schema",
		"meta":       map[string][]string{"anything": {"goes"}},
	}
	rec := doRequest(t, s.handleValidate, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["valid"] != true {
		t.Errorf("expected valid=true when no schema set, got %v", resp["valid"])
	}
}

func TestMainHandleValidate_MissingCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := map[string]interface{}{
		"meta": map[string][]string{},
	}
	rec := doRequest(t, s.handleValidate, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMainHandleValidate_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/validate", strings.NewReader("not json"))
	s.handleValidate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleSearch - additional sort tests
// ---------------------------------------------------------------------------

func TestMainHandleSearch_SortByUpdatedAtAsc(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add docs with slight time differences
	addTestDoc(t, s, "blog", "old", "en", "old content", nil)
	time.Sleep(10 * time.Millisecond)
	addTestDoc(t, s, "blog", "new", "en", "new content", nil)

	payload := SearchRequest{
		Collection: "blog",
		Sort:       "updatedAt",
		Asc:        true,
	}
	rec := doRequest(t, s.handleSearch, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var docs []storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 2 {
		t.Fatalf("expected 2, got %d", len(docs))
	}
	// updatedAt asc = oldest first
	if docs[0].UpdatedAt > docs[1].UpdatedAt {
		t.Errorf("expected ascending updatedAt order")
	}
}

func TestMainHandleSearch_SortByUpdatedAtDesc(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "old", "en", "old content", nil)
	time.Sleep(10 * time.Millisecond)
	addTestDoc(t, s, "blog", "new", "en", "new content", nil)

	payload := SearchRequest{
		Collection: "blog",
		Sort:       "updatedAt",
		Asc:        false,
	}
	rec := doRequest(t, s.handleSearch, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var docs []storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 2 {
		t.Fatalf("expected 2, got %d", len(docs))
	}
	// updatedAt desc = newest first
	if docs[0].UpdatedAt < docs[1].UpdatedAt {
		t.Errorf("expected descending updatedAt order")
	}
}

func TestMainHandleSearch_SortByKeyDesc(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "apple", "en", "a", nil)
	addTestDoc(t, s, "blog", "cherry", "en", "c", nil)
	addTestDoc(t, s, "blog", "banana", "en", "b", nil)

	payload := SearchRequest{
		Collection: "blog",
		Sort:       "key",
		Asc:        false,
	}
	rec := doRequest(t, s.handleSearch, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var docs []storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 3 {
		t.Fatalf("expected 3, got %d", len(docs))
	}
	if docs[0].Key != "cherry" || docs[1].Key != "banana" || docs[2].Key != "apple" {
		t.Errorf("expected cherry, banana, apple; got %s, %s, %s", docs[0].Key, docs[1].Key, docs[2].Key)
	}
}

func TestMainHandleSearch_PaginationOffsetBeyondTotal(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "a", "en", "content", nil)
	addTestDoc(t, s, "blog", "b", "en", "content", nil)

	payload := SearchRequest{
		Collection: "blog",
		Limit:      10,
		Offset:     100, // beyond total docs
	}
	rec := doRequest(t, s.handleSearch, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var docs []storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 0 {
		t.Errorf("expected 0 docs with offset beyond total, got %d", len(docs))
	}
}

// ---------------------------------------------------------------------------
// handleExport - additional tests
// ---------------------------------------------------------------------------

func TestMainHandleExport_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/export", strings.NewReader("not json"))
	s.handleExport(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleRestore - additional tests
// ---------------------------------------------------------------------------

func TestMainHandleRestore_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/restore", strings.NewReader("not json"))
	s.handleRestore(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMainHandleRestore_NonexistentFile(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := map[string]string{"from": "/nonexistent/path/db.backup"}
	rec := doRequest(t, s.handleRestore, payload)

	// The restore handler closes the DB, tries to copy, and that should fail
	// But we need the DB to still work, so this test is a bit destructive
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body=%s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleTruncate - additional tests
// ---------------------------------------------------------------------------

func TestMainHandleTruncate_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/truncate", strings.NewReader("{bad"))
	s.handleTruncate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMainHandleTruncate_EmptyCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Truncate on a collection with no docs should succeed
	payload := TruncateRequest{Collection: "empty-coll", KeepRevs: 1}
	rec := doRequest(t, s.handleTruncate, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "truncated") {
		t.Errorf("expected truncated status, got: %s", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleDeleteCollection - additional tests
// ---------------------------------------------------------------------------

func TestMainHandleDeleteCollection_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/delete-collection", strings.NewReader("invalid"))
	s.handleDeleteCollection(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestMainHandleDeleteCollection_ReadOnly(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Mode = ModeRead

	handler := s.guardWrite(s.handleDeleteCollection)
	payload := DeleteCollectionRequest{Collection: "test"}
	rec := doRequest(t, handler, payload)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestMainHandleDeleteCollection_WithMeta(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add docs with metadata to verify cleanup
	addTestDoc(t, s, "notes", "n1", "en", "note 1", map[string][]string{
		"author": {"alice"},
		"tag":    {"important"},
	})
	addTestDoc(t, s, "notes", "n2", "en", "note 2", map[string][]string{
		"author": {"bob"},
	})

	payload := DeleteCollectionRequest{Collection: "notes"}
	rec := doRequest(t, s.handleDeleteCollection, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["deletedCount"] != float64(2) {
		t.Errorf("expected 2 deleted, got %v", resp["deletedCount"])
	}

	// Verify meta index entries are cleaned up
	var metaCount int
	_ = s.DB.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte("idxmeta")).Cursor()
		prefix := []byte("meta|notes|")
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			metaCount++
		}
		return nil
	})
	if metaCount != 0 {
		t.Errorf("expected 0 meta index entries after delete collection, got %d", metaCount)
	}
}

// ---------------------------------------------------------------------------
// handleDelete - additional tests
// ---------------------------------------------------------------------------

func TestMainHandleDelete_InvalidJSON(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/delete", strings.NewReader("not json"))
	s.handleDelete(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Utility function tests
// ---------------------------------------------------------------------------

func TestMainSortDocs(t *testing.T) {
	docs := []storage.Doc{
		{Key: "c", AddedAt: 3, UpdatedAt: 1},
		{Key: "a", AddedAt: 1, UpdatedAt: 3},
		{Key: "b", AddedAt: 2, UpdatedAt: 2},
	}

	// Sort by key ascending
	sortDocs(docs, "key", true)
	if docs[0].Key != "a" || docs[1].Key != "b" || docs[2].Key != "c" {
		t.Errorf("sort by key asc: got %s, %s, %s", docs[0].Key, docs[1].Key, docs[2].Key)
	}

	// Sort by key descending
	sortDocs(docs, "key", false)
	if docs[0].Key != "c" || docs[1].Key != "b" || docs[2].Key != "a" {
		t.Errorf("sort by key desc: got %s, %s, %s", docs[0].Key, docs[1].Key, docs[2].Key)
	}

	// Sort by addedAt ascending
	sortDocs(docs, "addedAt", true)
	if docs[0].AddedAt != 1 || docs[1].AddedAt != 2 || docs[2].AddedAt != 3 {
		t.Errorf("sort by addedAt asc: got %d, %d, %d", docs[0].AddedAt, docs[1].AddedAt, docs[2].AddedAt)
	}

	// Sort by addedAt descending
	sortDocs(docs, "addedAt", false)
	if docs[0].AddedAt != 3 || docs[1].AddedAt != 2 || docs[2].AddedAt != 1 {
		t.Errorf("sort by addedAt desc: got %d, %d, %d", docs[0].AddedAt, docs[1].AddedAt, docs[2].AddedAt)
	}

	// Sort by updatedAt ascending
	sortDocs(docs, "updatedAt", true)
	if docs[0].UpdatedAt != 1 || docs[1].UpdatedAt != 2 || docs[2].UpdatedAt != 3 {
		t.Errorf("sort by updatedAt asc: got %d, %d, %d", docs[0].UpdatedAt, docs[1].UpdatedAt, docs[2].UpdatedAt)
	}

	// Sort by updatedAt descending
	sortDocs(docs, "updatedAt", false)
	if docs[0].UpdatedAt != 3 || docs[1].UpdatedAt != 2 || docs[2].UpdatedAt != 1 {
		t.Errorf("sort by updatedAt desc: got %d, %d, %d", docs[0].UpdatedAt, docs[1].UpdatedAt, docs[2].UpdatedAt)
	}

	// Sort by unknown field (defaults to updatedAt)
	sortDocs(docs, "unknown", true)
	if docs[0].UpdatedAt != 1 || docs[1].UpdatedAt != 2 || docs[2].UpdatedAt != 3 {
		t.Errorf("sort by unknown field should default to updatedAt")
	}
}

func TestMainUnique(t *testing.T) {
	tests := []struct {
		in  []string
		out int
	}{
		{[]string{"a", "b", "c"}, 3},
		{[]string{"a", "a", "b"}, 2},
		{[]string{"a", "a", "a"}, 1},
		{[]string{}, 0},
		{nil, 0},
	}

	for _, tc := range tests {
		result := sliceutil.Unique(tc.in)
		if len(result) != tc.out {
			t.Errorf("sliceutil.Unique(%v): expected %d, got %d", tc.in, tc.out, len(result))
		}
	}
}

func TestMainIntersect(t *testing.T) {
	tests := []struct {
		name string
		sets [][]string
		want int
	}{
		{"empty", nil, 0},
		{"single set", [][]string{{"a", "b", "c"}}, 3},
		{"two overlapping", [][]string{{"a", "b", "c"}, {"b", "c", "d"}}, 2},
		{"three overlapping", [][]string{{"a", "b"}, {"b", "c"}, {"b", "d"}}, 1},
		{"no overlap", [][]string{{"a"}, {"b"}}, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := intersect(tc.sets...)
			if len(result) != tc.want {
				t.Errorf("expected %d, got %d (%v)", tc.want, len(result), result)
			}
		})
	}
}

func TestMainCopyFile(t *testing.T) {
	// Create a source file
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "source.txt")
	dstPath := filepath.Join(dir, "dest.txt")

	if err := os.WriteFile(srcPath, []byte("hello world"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(srcPath, dstPath); err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}

	// Verify contents
	data, err := os.ReadFile( //nolint:gosec // G304: temp path in test
		dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(data))
	}
}

func TestMainCopyFile_SourceNotFound(t *testing.T) {
	dir := t.TempDir()
	err := copyFile(filepath.Join(dir, "nonexistent"), filepath.Join(dir, "dest"))
	if err == nil {
		t.Error("expected error for nonexistent source")
	}
}

func TestMainEnv(t *testing.T) {
	// Default value
	result := env("MDDB_NONEXISTENT_TEST_VAR_12345", "default")
	if result != "default" {
		t.Errorf("expected default, got %s", result)
	}

	// Set and read
	_ = os.Setenv("MDDB_TEST_ENV_VAR_12345", "custom")
	defer func() { _ = os.Unsetenv("MDDB_TEST_ENV_VAR_12345") }()
	result = env("MDDB_TEST_ENV_VAR_12345", "default")
	if result != "custom" {
		t.Errorf("expected custom, got %s", result)
	}
}

func TestMainGetOptimizedBoltOptions(t *testing.T) {
	opts := getOptimizedBoltOptions()
	if opts == nil {
		t.Fatal("expected non-nil options")
		return
	}
	if opts.Timeout != 2*time.Second {
		t.Errorf("expected 2s timeout, got %v", opts.Timeout)
	}
	if !opts.NoFreelistSync {
		t.Error("expected NoFreelistSync=true")
	}
	if opts.FreelistType != bolt.FreelistMapType {
		t.Errorf("expected FreelistMapType, got %v", opts.FreelistType)
	}
}

// ---------------------------------------------------------------------------
// addDocument - edge cases
// ---------------------------------------------------------------------------

func TestMainAddDocument_WithMetadata(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	meta := map[string][]string{
		"author":   {"alice"},
		"tags":     {"go", "database"},
		"category": {"tech"},
	}
	doc, isNew, err := s.addDocument("blog", "meta-doc", "en", meta, "# Meta storage.Doc", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if !isNew {
		t.Error("expected isNew=true for first insert")
	}
	if len(doc.Meta["tags"]) != 2 {
		t.Errorf("expected 2 tags, got %d", len(doc.Meta["tags"]))
	}

	// Update the same doc
	doc2, isNew2, err := s.addDocument("blog", "meta-doc", "en", map[string][]string{"author": {"bob"}}, "# Updated", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if isNew2 {
		t.Error("expected isNew=false for update")
	}
	if doc2.AddedAt != doc.AddedAt {
		t.Error("addedAt should be preserved on update")
	}
}

func TestMainAddDocument_WithTTL(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	doc, _, err := s.addDocument("temp", "expiring", "en", nil, "content", 3600, true)
	if err != nil {
		t.Fatal(err)
	}
	if doc.ExpiresAt == 0 {
		t.Error("expected non-zero expiresAt with TTL")
	}
	// TTL=0 means no expiry
	doc2, _, err := s.addDocument("temp", "permanent", "en", nil, "content", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if doc2.ExpiresAt != 0 {
		t.Errorf("expected expiresAt=0 with no TTL, got %d", doc2.ExpiresAt)
	}
}

// ---------------------------------------------------------------------------
// deleteDocumentInternal - edge cases
// ---------------------------------------------------------------------------

func TestMainDeleteDocumentInternal_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "del-me", "en", "content", map[string][]string{"tag": {"go"}})

	err := s.deleteDocumentInternal("blog", "del-me", "en")
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// Verify gone from docs bucket
	var found bool
	_ = s.DB.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte("docs")).Get(storage.DocKey("blog", genID("blog", "del-me", "en"))) != nil {
			found = true
		}
		return nil
	})
	if found {
		t.Error("document should be deleted from docs bucket")
	}
}

func TestMainDeleteDocumentInternal_NotFound(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	err := s.deleteDocumentInternal("blog", "nonexistent", "en")
	if err == nil {
		t.Error("expected error for deleting nonexistent doc")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// handleGet - expired document test
// ---------------------------------------------------------------------------

func TestMainHandleGet_ExpiredDocument(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add a document with TTL in the past
	docID := genID("temp", "expired", "en")
	doc := storage.Doc{
		ID:        docID,
		Key:       "expired",
		Lang:      "en",
		ContentMD: "expired content",
		AddedAt:   time.Now().Unix() - 100,
		UpdatedAt: time.Now().Unix() - 100,
		ExpiresAt: time.Now().Unix() - 10, // expired 10 seconds ago
	}
	buf, _ := marshalDoc(&doc)
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bByK := tx.Bucket([]byte("bykey"))
		_ = bDocs.Put(storage.DocKey("temp", docID), buf)
		_ = bByK.Put(storage.ByKeyKey("temp", "expired", "en"), []byte(docID))
		return nil
	})

	payload := GetRequest{
		Collection: "temp",
		Key:        "expired",
		Lang:       "en",
	}
	rec := doRequest(t, s.handleGet, payload)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for expired doc, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not found") {
		t.Errorf("expected 'not found' for expired doc, got: %s", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleSearch - expired documents should be filtered
// ---------------------------------------------------------------------------

func TestMainHandleSearch_FiltersExpiredDocs(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add a normal doc
	addTestDoc(t, s, "temp", "active", "en", "active content", nil)

	// Add an expired doc directly
	docID := genID("temp", "expired", "en")
	doc := storage.Doc{
		ID:        docID,
		Key:       "expired",
		Lang:      "en",
		ContentMD: "expired content",
		AddedAt:   time.Now().Unix() - 100,
		UpdatedAt: time.Now().Unix() - 100,
		ExpiresAt: time.Now().Unix() - 10,
	}
	buf, _ := marshalDoc(&doc)
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put(storage.DocKey("temp", docID), buf)
	})

	payload := SearchRequest{Collection: "temp"}
	rec := doRequest(t, s.handleSearch, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var docs []storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 1 {
		t.Errorf("expected 1 doc (expired should be filtered), got %d", len(docs))
	}
	if len(docs) > 0 && docs[0].Key != "active" {
		t.Errorf("expected active doc, got %s", docs[0].Key)
	}
}

// ---------------------------------------------------------------------------
// handleHealth - verify mode is reported correctly
// ---------------------------------------------------------------------------

func TestMainHandleHealth_Modes(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	modes := []AccessMode{ModeRead, ModeWrite, ModeRW}
	for _, mode := range modes {
		s.Mode = mode
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		s.handleHealth(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("mode %s: expected 200, got %d", mode, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), string(mode)) {
			t.Errorf("mode %s: expected mode in body, got %s", mode, rec.Body.String())
		}
	}
}

// ---------------------------------------------------------------------------
// ensureBuckets test
// ---------------------------------------------------------------------------

func TestMainEnsureBuckets(t *testing.T) {
	dir := t.TempDir()
	db, err := bolt.Open(filepath.Join(dir, "test.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	s := &Server{
		DB: db,
		BucketNames: BucketNames{
			Docs:    []byte("docs"),
			IdxMeta: []byte("idxmeta"),
			Rev:     []byte("rev"),
			ByKey:   []byte("bykey"),
		},
	}

	if err := s.ensureBuckets(); err != nil {
		t.Fatalf("ensureBuckets failed: %v", err)
	}

	// Idempotent
	if err := s.ensureBuckets(); err != nil {
		t.Fatalf("second ensureBuckets failed: %v", err)
	}

	// Verify buckets exist
	err = db.View(func(tx *bolt.Tx) error {
		for _, name := range []string{"docs", "idxmeta", "rev", "bykey", "embedding_configs"} {
			if tx.Bucket([]byte(name)) == nil {
				t.Errorf("bucket %q not found", name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Key builder functions
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// handleBackup - error cases
// ---------------------------------------------------------------------------

func TestMainHandleBackup_InvalidDestination(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/backup?to=/nonexistent/dir/backup.db", nil)
	s.handleBackup(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body=%s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleAdd + handleSearch integration for metadata filter with expired docs
// ---------------------------------------------------------------------------

func TestMainHandleSearch_MetaFilterExcludesExpired(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add a normal doc with meta
	addTestDoc(t, s, "blog", "active-meta", "en", "active", map[string][]string{"tag": {"go"}})

	// Add an expired doc with the same meta directly
	docID := genID("blog", "expired-meta", "en")
	doc := storage.Doc{
		ID:        docID,
		Key:       "expired-meta",
		Lang:      "en",
		ContentMD: "expired",
		Meta:      map[string][]string{"tag": {"go"}},
		AddedAt:   time.Now().Unix() - 100,
		UpdatedAt: time.Now().Unix() - 100,
		ExpiresAt: time.Now().Unix() - 10,
	}
	buf, _ := marshalDoc(&doc)
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		_ = bDocs.Put(storage.DocKey("blog", docID), buf)
		// Add meta index entry
		mkey := append(storage.MetaKeyPrefix("blog", "tag", "go"), []byte(docID)...)
		_ = bIdx.Put(mkey, []byte("1"))
		return nil
	})

	payload := SearchRequest{
		Collection: "blog",
		FilterMeta: map[string][]string{"tag": {"go"}},
	}
	rec := doRequest(t, s.handleSearch, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var docs []storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 1 {
		t.Errorf("expected 1 doc (expired filtered), got %d", len(docs))
	}
	if len(docs) > 0 && docs[0].Key != "active-meta" {
		t.Errorf("expected active-meta doc, got %s", docs[0].Key)
	}
}

// ---------------------------------------------------------------------------
// Add document - metadata change detection (updates meta index)
// ---------------------------------------------------------------------------

func TestMainAddDocument_MetadataUpdateReindex(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add with initial meta
	_, _, err := s.addDocument("blog", "meta-update", "en",
		map[string][]string{"tag": {"go", "rust"}}, "content", 0, true)
	if err != nil {
		t.Fatal(err)
	}

	// Update with different meta
	_, _, err = s.addDocument("blog", "meta-update", "en",
		map[string][]string{"tag": {"python"}}, "updated content", 0, true)
	if err != nil {
		t.Fatal(err)
	}

	// Search for old meta should not find
	payload := SearchRequest{
		Collection: "blog",
		FilterMeta: map[string][]string{"tag": {"rust"}},
	}
	rec := doRequest(t, s.handleSearch, payload)
	var docs []storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 0 {
		t.Errorf("expected 0 docs with old tag 'rust', got %d", len(docs))
	}

	// Search for new meta should find
	payload2 := SearchRequest{
		Collection: "blog",
		FilterMeta: map[string][]string{"tag": {"python"}},
	}
	rec2 := doRequest(t, s.handleSearch, payload2)
	var docs2 []storage.Doc
	_ = json.Unmarshal(rec2.Body.Bytes(), &docs2)
	if len(docs2) != 1 {
		t.Errorf("expected 1 doc with new tag 'python', got %d", len(docs2))
	}
}

// ---------------------------------------------------------------------------
// handleStats - verify database size is reported
// ---------------------------------------------------------------------------

func TestMainHandleStats_DatabaseSizeReported(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/stats", nil)
	s.handleStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var stats struct {
		DatabasePath string `json:"databasePath"`
		DatabaseSize int64  `json:"databaseSize"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &stats)
	if stats.DatabasePath == "" {
		t.Error("expected non-empty database path")
	}
	if stats.DatabaseSize <= 0 {
		t.Errorf("expected positive database size, got %d", stats.DatabaseSize)
	}
}

// ---------------------------------------------------------------------------
// BucketNames struct test
// ---------------------------------------------------------------------------

func TestMainBucketNames(t *testing.T) {
	bn := BucketNames{
		Docs:    []byte("docs"),
		IdxMeta: []byte("idxmeta"),
		Rev:     []byte("rev"),
		ByKey:   []byte("bykey"),
	}
	if string(bn.Docs) != "docs" {
		t.Errorf("expected docs, got %s", string(bn.Docs))
	}
	if string(bn.IdxMeta) != "idxmeta" {
		t.Errorf("expected idxmeta, got %s", string(bn.IdxMeta))
	}
	if string(bn.Rev) != "rev" {
		t.Errorf("expected rev, got %s", string(bn.Rev))
	}
	if string(bn.ByKey) != "bykey" {
		t.Errorf("expected bykey, got %s", string(bn.ByKey))
	}
}

// ---------------------------------------------------------------------------
// handleAdd - large content test
// ---------------------------------------------------------------------------

func TestMainHandleAdd_LargeContent(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	largeContent := strings.Repeat("# Large Document\n\nThis is a paragraph. ", 1000)

	payload := AddRequest{
		Collection: "blog",
		Key:        "large-doc",
		Lang:       "en",
		ContentMD:  largeContent,
	}
	rec := doRequest(t, s.handleAdd, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Verify we can read it back
	getRec := doRequest(t, s.handleGet, GetRequest{Collection: "blog", Key: "large-doc", Lang: "en"})
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on get, got %d", getRec.Code)
	}
	var doc storage.Doc
	_ = json.Unmarshal(getRec.Body.Bytes(), &doc)
	if len(doc.ContentMD) != len(largeContent) {
		t.Errorf("expected content length %d, got %d", len(largeContent), len(doc.ContentMD))
	}
}

// ---------------------------------------------------------------------------
// genID - additional edge cases
// ---------------------------------------------------------------------------

func TestMainGenID_EmptyParts(t *testing.T) {
	id := genID("", "", "")
	if id != "||" {
		t.Errorf("expected '||', got %q", id)
	}
}

func TestMainGenID_SpecialCharacters(t *testing.T) {
	id := genID("blog", "hello-world_123", "en")
	if id != "blog|hello-world_123|en" {
		t.Errorf("expected 'blog|hello-world_123|en', got %q", id)
	}
}

// ---------------------------------------------------------------------------
// handleImportURL readOnly mode
// ---------------------------------------------------------------------------

func TestMainHandleImportURL_ReadOnlyMode(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Mode = ModeRead

	handler := s.guardWrite(s.handleImportURL)
	payload := ImportURLRequest{
		Collection: "docs",
		URL:        "http://example.com/test.md",
		Key:        "test",
		Lang:       "en",
	}
	rec := doRequest(t, handler, payload)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// handleSearch - addedAt sort ascending
// ---------------------------------------------------------------------------

func TestMainHandleSearch_SortByAddedAtAsc(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "first", "en", "first", nil)
	addTestDoc(t, s, "blog", "second", "en", "second", nil)

	payload := SearchRequest{
		Collection: "blog",
		Sort:       "addedAt",
		Asc:        true,
	}
	rec := doRequest(t, s.handleSearch, payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var docs []storage.Doc
	_ = json.Unmarshal(rec.Body.Bytes(), &docs)
	if len(docs) != 2 {
		t.Fatalf("expected 2, got %d", len(docs))
	}
	// addedAt asc = oldest first
	for i := 1; i < len(docs); i++ {
		if docs[i].AddedAt < docs[i-1].AddedAt {
			t.Errorf("expected ascending addedAt order at index %d", i)
		}
	}
}

// ---------------------------------------------------------------------------
// FTS handler - with document that was fetched from DB
// ---------------------------------------------------------------------------

func TestMainHandleFTS_WithMultipleDocs(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add and index multiple documents
	doc1 := addTestDoc(t, s, "blog", "go-post", "en", "golang programming is amazing for backend systems", nil)
	_ = s.FTSIndex.Index("blog", doc1.ID, doc1.ContentMD)

	doc2 := addTestDoc(t, s, "blog", "rust-post", "en", "rust programming is great for systems", nil)
	_ = s.FTSIndex.Index("blog", doc2.ID, doc2.ContentMD)

	doc3 := addTestDoc(t, s, "blog", "cooking", "en", "pasta recipe for dinner tonight", nil)
	_ = s.FTSIndex.Index("blog", doc3.ID, doc3.ContentMD)

	// Search for "programming" should find 2 docs
	payload := FTSSearchRequest{
		Collection: "blog",
		Query:      "programming",
		Limit:      10,
	}
	rec := doRequest(t, s.handleFTS, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var resp FTSSearchResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Total < 2 {
		t.Errorf("expected at least 2 results for 'programming', got %d", resp.Total)
	}
}

// ---------------------------------------------------------------------------
// AccessMode constants
// ---------------------------------------------------------------------------

func TestMainAccessModeConstants(t *testing.T) {
	if ModeRead != "read" {
		t.Errorf("expected ModeRead=read, got %s", ModeRead)
	}
	if ModeWrite != "write" {
		t.Errorf("expected ModeWrite=write, got %s", ModeWrite)
	}
	if ModeRW != "wr" {
		t.Errorf("expected ModeRW=wr, got %s", ModeRW)
	}
}

// ---------------------------------------------------------------------------
// VERSION constant
// ---------------------------------------------------------------------------

func TestMainVersionConstant(t *testing.T) {
	if VERSION == "" {
		t.Error("expected non-empty VERSION constant")
	}
}

// ---------------------------------------------------------------------------
// handleWebhooks + handleWebhookDelete integration
// ---------------------------------------------------------------------------

func TestMainWebhookRegisterAndDelete_Integration(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Register via HTTP handler
	regPayload := RegisterWebhookRequest{
		URL:        "http://example.com/hook",
		Events:     []string{"doc.added"},
		Collection: "blog",
	}
	b, _ := json.Marshal(regPayload)
	regRec := httptest.NewRecorder()
	regReq := httptest.NewRequest(http.MethodPost, "/v1/webhooks", bytes.NewReader(b))
	regReq.Header.Set("Content-Type", "application/json")
	s.handleWebhooks(regRec, regReq)

	if regRec.Code != http.StatusOK {
		t.Fatalf("register failed: %d; body=%s", regRec.Code, regRec.Body.String())
	}

	var wh map[string]interface{}
	_ = json.Unmarshal(regRec.Body.Bytes(), &wh)
	whID := wh["id"].(string)

	// List via HTTP handler
	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/v1/webhooks", nil)
	s.handleWebhooks(listRec, listReq)

	var hooks []interface{}
	_ = json.Unmarshal(listRec.Body.Bytes(), &hooks)
	if len(hooks) != 1 {
		t.Errorf("expected 1 webhook, got %d", len(hooks))
	}

	// Delete via HTTP handler
	delPayload := DeleteWebhookRequest{ID: whID}
	delRec := doRequest(t, s.handleWebhookDelete, delPayload)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete failed: %d; body=%s", delRec.Code, delRec.Body.String())
	}

	// Verify gone
	listRec2 := httptest.NewRecorder()
	listReq2 := httptest.NewRequest(http.MethodGet, "/v1/webhooks", nil)
	s.handleWebhooks(listRec2, listReq2)

	var hooks2 []interface{}
	_ = json.Unmarshal(listRec2.Body.Bytes(), &hooks2)
	if len(hooks2) != 0 {
		t.Errorf("expected 0 webhooks after delete, got %d", len(hooks2))
	}
}

// ---------------------------------------------------------------------------
// Schema CRUD integration via HTTP handlers
// ---------------------------------------------------------------------------

func TestMainSchemaCRUD_Integration(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Set schema
	setPayload := map[string]string{
		"collection": "articles",
		"schema":     `{"required":["title"]}`,
	}
	setRec := doRequest(t, s.handleSchemaSet, setPayload)
	if setRec.Code != http.StatusOK {
		t.Fatalf("set failed: %d", setRec.Code)
	}

	// Get schema
	getPayload := map[string]string{"collection": "articles"}
	getRec := doRequest(t, s.handleSchemaGet, getPayload)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get failed: %d", getRec.Code)
	}
	var getResp map[string]interface{}
	_ = json.Unmarshal(getRec.Body.Bytes(), &getResp)
	if getResp["enabled"] != true {
		t.Errorf("expected enabled=true")
	}

	// List schemas
	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/v1/schema/list", nil)
	s.handleSchemaList(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list failed: %d", listRec.Code)
	}

	// Validate
	validatePayload := map[string]interface{}{
		"collection": "articles",
		"meta":       map[string][]string{}, // missing title
	}
	valRec := doRequest(t, s.handleValidate, validatePayload)
	var valResp map[string]interface{}
	_ = json.Unmarshal(valRec.Body.Bytes(), &valResp)
	if valResp["valid"] != false {
		t.Error("expected validation to fail for missing title")
	}

	// Delete schema
	delPayload := map[string]string{"collection": "articles"}
	delRec := doRequest(t, s.handleSchemaDelete, delPayload)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete failed: %d", delRec.Code)
	}

	// Verify schema is gone
	getRec2 := doRequest(t, s.handleSchemaGet, getPayload)
	var getResp2 map[string]interface{}
	_ = json.Unmarshal(getRec2.Body.Bytes(), &getResp2)
	if getResp2["enabled"] != false {
		t.Error("expected schema to be disabled after delete")
	}
}
