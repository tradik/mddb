package mddb

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testServer routes a few canned responses and records the last request so
// tests can assert method, path, headers and body.
type capture struct {
	method, path, auth, apiKey, contentType, body string
}

func newServer(t *testing.T, cap *capture) (*Client, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		cap.auth = r.Header.Get("Authorization")
		cap.apiKey = r.Header.Get("X-API-Key")
		cap.contentType = r.Header.Get("Content-Type")
		b, _ := readBody(r)
		cap.body = string(b)

		switch r.URL.Path {
		case "/v1/add", "/v1/get", "/v1/set-ttl", "/v1/import-url":
			_, _ = w.Write([]byte(`{"id":"blog|k|en","key":"k","lang":"en","contentMd":"hi","addedAt":1,"updatedAt":2}`))
		case "/v1/search":
			_, _ = w.Write([]byte(`[{"id":"a","key":"k1"},{"id":"b","key":"k2"}]`))
		case "/v1/stats":
			_, _ = w.Write([]byte(`{"databasePath":"/x","databaseSize":2048,"mode":"rw","totalDocuments":5,"collections":[{"name":"blog","documentCount":3}]}`))
		case "/v1/webhooks":
			if r.Method == "GET" {
				_, _ = w.Write([]byte(`[{"id":"w1","url":"http://x","events":["doc.added"]}]`))
			} else {
				_, _ = w.Write([]byte(`{"id":"w1","url":"http://x","events":["doc.added"]}`))
			}
		case "/v1/boom":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"nope"}`))
		default:
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}
	}))
	return New(srv.URL), srv.Close
}

// readBody avoids importing io just for the test body read.
func readBody(r *http.Request) ([]byte, error) {
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}

func TestNewTrimsTrailingSlashAndBaseURL(t *testing.T) {
	c := New("http://localhost:8080/")
	if c.BaseURL() != "http://localhost:8080" {
		t.Errorf("BaseURL = %q, want trimmed", c.BaseURL())
	}
}

func TestOptions(t *testing.T) {
	custom := &http.Client{Timeout: time.Second}
	c := New("http://x",
		WithAPIKey("key1"),
		WithToken("tok1"),
		WithHTTPClient(custom),
		WithTimeout(5*time.Second),
		WithVerbose(&bytes.Buffer{}),
	)
	if c.apiKey != "key1" || c.token != "tok1" {
		t.Error("auth options not applied")
	}
	if c.httpc != custom {
		t.Error("WithHTTPClient not applied")
	}
	if c.timeout != 5*time.Second {
		t.Error("WithTimeout not applied")
	}
	if custom.Timeout != time.Second {
		t.Error("WithTimeout must not change the supplied http.Client")
	}
	// WithHTTPClient(nil) must be a no-op.
	before := c.httpc
	WithHTTPClient(nil)(c)
	if c.httpc != before {
		t.Error("WithHTTPClient(nil) should be a no-op")
	}
}

func TestAPIKeyTakesPrecedenceOverToken(t *testing.T) {
	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.auth = r.Header.Get("Authorization")
		cap.apiKey = r.Header.Get("X-API-Key")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := New(srv.URL, WithAPIKey("k"), WithToken("t"))
	if _, err := c.Do(context.Background(), "GET", "/x", nil); err != nil {
		t.Fatal(err)
	}
	if cap.apiKey != "k" || cap.auth != "" {
		t.Errorf("expected only X-API-Key; got apiKey=%q auth=%q", cap.apiKey, cap.auth)
	}

	// Token-only path.
	c2 := New(srv.URL, WithToken("t"))
	if _, err := c2.Do(context.Background(), "GET", "/x", nil); err != nil {
		t.Fatal(err)
	}
	if cap.auth != "Bearer t" {
		t.Errorf("token path: auth = %q, want 'Bearer t'", cap.auth)
	}
}

func TestDoErrorResponse(t *testing.T) {
	cap := &capture{}
	c, closeFn := newServer(t, cap)
	defer closeFn()

	_, err := c.Do(context.Background(), "POST", "/v1/boom", map[string]string{"a": "b"})
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("want *APIError, got %T", err)
	}
	if apiErr.StatusCode != 400 || !strings.Contains(apiErr.Error(), "nope") {
		t.Errorf("APIError = %+v", apiErr)
	}
	// Body was marshalled + content-type set.
	if cap.contentType != "application/json" || !strings.Contains(cap.body, `"a":"b"`) {
		t.Errorf("request body/content-type not set: ct=%q body=%q", cap.contentType, cap.body)
	}
}

func TestVerboseWritesRequestBody(t *testing.T) {
	var buf bytes.Buffer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := New(srv.URL, WithVerbose(&buf))
	if _, err := c.Do(context.Background(), "POST", "/x", map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"n":1`) {
		t.Errorf("verbose output = %q", buf.String())
	}
}

func TestDocumentMethods(t *testing.T) {
	cap := &capture{}
	c, closeFn := newServer(t, cap)
	defer closeFn()
	ctx := context.Background()

	doc, err := c.Add(ctx, AddRequest{Collection: "blog", Key: "k", Lang: "en", ContentMD: "hi"})
	if err != nil || doc.ID != "blog|k|en" {
		t.Fatalf("Add: %+v err=%v", doc, err)
	}
	if cap.method != "POST" || cap.path != "/v1/add" {
		t.Errorf("Add hit %s %s", cap.method, cap.path)
	}

	if _, err := c.Get(ctx, GetRequest{Collection: "blog", Key: "k", Lang: "en"}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d, err := c.SetTTL(ctx, SetTTLRequest{Collection: "blog", Key: "k", Lang: "en", TTL: 60}); err != nil || d.Key != "k" {
		t.Fatalf("SetTTL: %v", err)
	}
	if _, err := c.ImportURL(ctx, ImportURLRequest{Collection: "blog", URL: "http://x"}); err != nil {
		t.Fatalf("ImportURL: %v", err)
	}

	docs, err := c.Search(ctx, SearchRequest{Collection: "blog", Limit: 10})
	if err != nil || len(docs) != 2 {
		t.Fatalf("Search: %d docs err=%v", len(docs), err)
	}

	stats, err := c.Stats(ctx)
	if err != nil || stats.TotalDocuments != 5 || len(stats.Collections) != 1 || stats.Collections[0].Name != "blog" {
		t.Fatalf("Stats: %+v err=%v", stats, err)
	}
}

func TestSearchAndAdminRawMethods(t *testing.T) {
	cap := &capture{}
	c, closeFn := newServer(t, cap)
	defer closeFn()
	ctx := context.Background()

	raws := []struct {
		name string
		fn   func() (json.RawMessage, error)
		path string
	}{
		{"FTS", func() (json.RawMessage, error) { return c.FTS(ctx, FTSRequest{Collection: "blog", Query: "x"}) }, "/v1/fts"},
		{"VectorSearch", func() (json.RawMessage, error) {
			return c.VectorSearch(ctx, VectorSearchRequest{Collection: "blog", Query: "x"})
		}, "/v1/vector-search"},
		{"VectorStats", func() (json.RawMessage, error) { return c.VectorStats(ctx) }, "/v1/vector-stats"},
		{"VectorReindex", func() (json.RawMessage, error) { return c.VectorReindex(ctx, "blog") }, "/v1/vector-reindex"},
		{"GetSchema", func() (json.RawMessage, error) { return c.GetSchema(ctx, "blog") }, "/v1/schema/get"},
		{"ListSchemas", func() (json.RawMessage, error) { return c.ListSchemas(ctx) }, "/v1/schema/list"},
		{"SetSchema", func() (json.RawMessage, error) { return c.SetSchema(ctx, map[string]any{"collection": "blog"}) }, "/v1/schema/set"},
		{"Validate", func() (json.RawMessage, error) { return c.ValidateDocument(ctx, map[string]any{}) }, "/v1/validate"},
		{"Restore", func() (json.RawMessage, error) { return c.Restore(ctx, map[string]any{}) }, "/v1/restore"},
		{"Truncate", func() (json.RawMessage, error) { return c.Truncate(ctx, map[string]any{}) }, "/v1/truncate"},
		{"Login", func() (json.RawMessage, error) { return c.Login(ctx, "u", "p") }, "/v1/auth/login"},
		{"CreateAPIKey", func() (json.RawMessage, error) { return c.CreateAPIKey(ctx, map[string]any{}) }, "/v1/auth/api-key"},
		{"ListAPIKeys", func() (json.RawMessage, error) { return c.ListAPIKeys(ctx) }, "/v1/auth/api-keys"},
		{"GraphQL", func() (json.RawMessage, error) { return c.GraphQL(ctx, "{x}", nil) }, "/graphql"},
	}
	for _, tc := range raws {
		if _, err := tc.fn(); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if cap.path != tc.path {
			t.Errorf("%s hit %s, want %s", tc.name, cap.path, tc.path)
		}
	}

	// Export returns raw bytes.
	if b, err := c.Export(ctx, map[string]any{"collection": "blog"}); err != nil || len(b) == 0 {
		t.Errorf("Export: %v", err)
	}
}

func TestWebhookMethods(t *testing.T) {
	cap := &capture{}
	c, closeFn := newServer(t, cap)
	defer closeFn()
	ctx := context.Background()

	wh, err := c.RegisterWebhook(ctx, RegisterWebhookRequest{URL: "http://x", Events: []string{"doc.added"}})
	if err != nil || wh.ID != "w1" {
		t.Fatalf("RegisterWebhook: %+v err=%v", wh, err)
	}
	hooks, err := c.ListWebhooks(ctx)
	if err != nil || len(hooks) != 1 || hooks[0].ID != "w1" {
		t.Fatalf("ListWebhooks: %+v err=%v", hooks, err)
	}
	if err := c.DeleteWebhook(ctx, "w1"); err != nil {
		t.Fatalf("DeleteWebhook: %v", err)
	}
	if err := c.DeleteSchema(ctx, "blog"); err != nil {
		t.Fatalf("DeleteSchema: %v", err)
	}
	if err := c.DeleteAPIKey(ctx, "k1"); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	if cap.method != "DELETE" || cap.path != "/v1/auth/api-keys/k1" {
		t.Errorf("DeleteAPIKey hit %s %s", cap.method, cap.path)
	}
}
