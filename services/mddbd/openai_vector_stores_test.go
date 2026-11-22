package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// #446: GET /v1/vector_stores and the path and error helpers, driven the way
// Open WebUI's OpenAI-compatible knowledge provider calls them.

// openAIDo sends a request as Open WebUI does (Accept: application/json,
// a Bearer key when one is given) and decodes the JSON object answer.
func openAIDo(t *testing.T, h http.Handler, method, path, body, key string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Accept", "application/json")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

// openAIErrCode is the code of an OpenAI error body.
func openAIErrCode(out map[string]any) string {
	e, _ := out["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

// openAIStoreIDs are the ids of a list page.
func openAIStoreIDs(out map[string]any) []string {
	var ids []string
	data, _ := out["data"].([]any)
	for _, d := range data {
		ids = append(ids, d.(map[string]any)["id"].(string))
	}
	return ids
}

// openAIListServer holds alpha, beta (described) and acme/docs with
// documents, and gamma with a configuration only.
func openAIListServer(t *testing.T) *Server {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(func() { s.closeCollectionFiles(); cleanup() })
	for _, c := range []string{"alpha", "beta", "acme/docs"} {
		addTestDoc(t, s, c, "readme", "en", "# Readme\n\nThe "+c+" collection.", nil)
	}
	if err := s.CollectionManager.Set("beta", &CollectionConfig{Type: "default", Description: "Beta handbook"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CollectionManager.Set("gamma", &CollectionConfig{Type: "default"}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOpenAIVectorStoreListPages(t *testing.T) {
	for _, layout := range []string{"single", "per-collection"} {
		t.Run(layout, func(t *testing.T) {
			t.Setenv("MDDB_STORAGE_LAYOUT", layout)
			s := openAIListServer(t)
			h := esHandler(t, s, false)

			rec, out := openAIDo(t, h, http.MethodGet, "/v1/vector_stores?limit=100", "", "")
			if rec.Code != http.StatusOK || out["object"] != "list" || out["has_more"] != false {
				t.Fatalf("list: %d %v", rec.Code, out)
			}
			if got := strings.Join(openAIStoreIDs(out), ","); got != "acme/docs,alpha,beta,gamma" {
				t.Fatalf("stores %s", got)
			}
			if out["first_id"] != "acme/docs" || out["last_id"] != "gamma" {
				t.Fatalf("cursors %v %v", out["first_id"], out["last_id"])
			}
			beta := out["data"].([]any)[2].(map[string]any)
			if beta["object"] != "vector_store" || beta["name"] != "beta" ||
				beta["metadata"].(map[string]any)["description"] != "Beta handbook" ||
				beta["file_counts"].(map[string]any)["completed"] != float64(1) {
				t.Fatalf("beta %v", beta)
			}
			gamma := out["data"].([]any)[3].(map[string]any)
			if gamma["file_counts"].(map[string]any)["total"] != float64(0) {
				t.Fatalf("a configured collection without documents: %v", gamma)
			}

			// Open WebUI pages with has_more and after=<last_id>.
			_, out = openAIDo(t, h, http.MethodGet, "/v1/vector_stores?limit=2", "", "")
			if got := strings.Join(openAIStoreIDs(out), ","); got != "acme/docs,alpha" || out["has_more"] != true {
				t.Fatalf("first page %s %v", got, out["has_more"])
			}
			_, out = openAIDo(t, h, http.MethodGet, "/v1/vector_stores?limit=2&after=alpha", "", "")
			if got := strings.Join(openAIStoreIDs(out), ","); got != "beta,gamma" || out["has_more"] != false {
				t.Fatalf("second page %s %v", got, out["has_more"])
			}
			_, out = openAIDo(t, h, http.MethodGet, "/v1/vector_stores?after=zzz", "", "")
			if len(openAIStoreIDs(out)) != 0 || out["first_id"] != nil || out["last_id"] != nil {
				t.Fatalf("past the end: %v", out)
			}
		})
	}
}

func TestOpenAIVectorStoreListRefusals(t *testing.T) {
	s := openAIListServer(t)
	h := esHandler(t, s, false)
	for _, q := range []string{"limit=0", "limit=101", "limit=x"} {
		rec, out := openAIDo(t, h, http.MethodGet, "/v1/vector_stores?"+q, "", "")
		if rec.Code != http.StatusBadRequest || openAIErrCode(out) != "invalid_limit" {
			t.Fatalf("%s: %d %v", q, rec.Code, out)
		}
		if out["error"].(map[string]any)["type"] != "invalid_request_error" {
			t.Fatalf("%s: error type %v", q, out)
		}
	}
	rec, out := openAIDo(t, h, http.MethodPost, "/v1/vector_stores", "{}", "")
	if rec.Code != http.StatusMethodNotAllowed || openAIErrCode(out) != "method_not_allowed" {
		t.Fatalf("POST: %d %v", rec.Code, out)
	}
}

func TestOpenAIVectorStoreListWhileStatsPending(t *testing.T) {
	t.Setenv("MDDB_STATS_WAIT", "0") // the first walk is not waited for
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	rec := httptest.NewRecorder()
	s.handleOpenAIVectorStores(rec, httptest.NewRequest(http.MethodGet, "/v1/vector_stores", nil))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusServiceUnavailable || openAIErrCode(out) != "stats_pending" ||
		out["error"].(map[string]any)["type"] != "server_error" {
		t.Fatalf("pending: %d %v", rec.Code, out)
	}
}

func TestOpenAIVectorStoreListFiltersByPermission(t *testing.T) {
	s := openAIListServer(t)
	am, cleanup := authMwSetup(t)
	defer cleanup()
	s.AuthManager = am
	h := esHandler(t, s, true)
	if _, err := am.CreateUser("reader", "pw"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"alpha", "gamma"} {
		if err := am.SetPermission(&Permission{Username: "reader", Collection: c, Read: true}); err != nil {
			t.Fatal(err)
		}
	}
	key, err := am.CreateAPIKey("reader", "open-webui", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Read on two collections lists those two, not a 403 for the whole list.
	rec, out := openAIDo(t, h, http.MethodGet, "/v1/vector_stores", "", key)
	if got := strings.Join(openAIStoreIDs(out), ","); rec.Code != http.StatusOK || got != "alpha,gamma" {
		t.Fatalf("reader: %d %s", rec.Code, got)
	}
	if rec, _ = openAIDo(t, h, http.MethodGet, "/v1/vector_stores", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credentials: %d", rec.Code)
	}
}

func TestOpenAIVectorStoreListTenantAndKeyScope(t *testing.T) {
	s := openAIListServer(t)
	list := func(ctx context.Context) string {
		r := httptest.NewRequest(http.MethodGet, "/v1/vector_stores", nil).WithContext(ctx)
		stores, err := s.openAIReadableStores(r)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, st := range stores {
			ids = append(ids, st.ID)
		}
		return strings.Join(ids, ",")
	}
	tenant := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "t", Tenant: "acme"})
	if got := list(tenant); got != "acme/docs" {
		t.Fatalf("tenant sees %s", got)
	}
	scoped := withKeyScope(context.Background(), mcpKeyScope{collections: []string{"beta"}})
	if got := list(scoped); got != "beta" {
		t.Fatalf("scoped MCP key sees %s", got)
	}
}

func TestOpenAIStoreID(t *testing.T) {
	for _, c := range []struct{ raw, id, action string }{
		{"/v1/vector_stores/alpha/search", "alpha", "search"},
		{"/v1/vector_stores/acme%2Fdocs/search", "acme/docs", "search"},
		{"/v1/vector_stores/acme/docs/search", "acme/docs", "search"},
		{"/v1/vector_stores/a%20b/files", "a b", "files"},
	} {
		u, err := url.Parse(c.raw)
		if err != nil {
			t.Fatal(err)
		}
		id, action, ok := openAIStoreID(&http.Request{URL: u})
		if !ok || id != c.id || action != c.action {
			t.Fatalf("%s: %q %q %v", c.raw, id, action, ok)
		}
	}
	for _, raw := range []string{"/v1/vector_stores/alpha", "/v1/vector_stores//search", "/v1/vector_stores/"} {
		u, _ := url.Parse(raw)
		if _, _, ok := openAIStoreID(&http.Request{URL: u}); ok {
			t.Fatalf("%s parsed", raw)
		}
	}
}

func TestOpenAIAuthFail(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		typ    string
	}{
		{ErrUnauthorized, http.StatusUnauthorized, "authentication_error"},
		{ErrForbidden, http.StatusForbidden, "permission_error"},
	} {
		rec := httptest.NewRecorder()
		openAIAuthFail(rec, c.err)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != c.status || out["error"].(map[string]any)["type"] != c.typ {
			t.Fatalf("%v: %d %v", c.err, rec.Code, out)
		}
	}
}
