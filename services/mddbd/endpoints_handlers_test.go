package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	json "mddb/internal/jsonx"
)

// ---------- 1. handleEndpoints - GET success ----------

func TestHandleEndpoints_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	_ = os.Unsetenv("MDDB_AUTH_ENABLED")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/endpoints", nil)
	s.handleEndpoints(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var resp EndpointsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(resp.HTTP) == 0 {
		t.Error("expected non-empty HTTP endpoints list")
	}
	if len(resp.GRPC) == 0 {
		t.Error("expected non-empty GRPC methods list")
	}
	if len(resp.MCP) == 0 {
		t.Error("expected non-empty MCP tools list")
	}
}

// ---------- 2. handleEndpoints - wrong method ----------

func TestHandleEndpoints_MethodNotAllowed(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete}
	for _, method := range methods {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/v1/endpoints", nil)
		s.handleEndpoints(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: expected 405, got %d", method, rec.Code)
		}
	}
}

// ---------- 3. handleEndpoints - contains core endpoints ----------

func TestHandleEndpoints_ContainsCoreEndpoints(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	_ = os.Unsetenv("MDDB_AUTH_ENABLED")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/endpoints", nil)
	s.handleEndpoints(rec, req)

	var resp EndpointsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Check for essential HTTP endpoints
	expectedPaths := map[string]bool{
		"/v1/health":        false,
		"/v1/stats":         false,
		"/v1/add":           false,
		"/v1/get":           false,
		"/v1/search":        false,
		"/v1/delete":        false,
		"/v1/vector-search": false,
		"/v1/geo-search":    false,
		"/v1/endpoints":     false,
		"/v1/system/info":   false,
		"/v1/config":        false,
	}

	for _, ep := range resp.HTTP {
		if _, ok := expectedPaths[ep.Path]; ok {
			expectedPaths[ep.Path] = true
		}
	}

	for path, found := range expectedPaths {
		if !found {
			t.Errorf("expected endpoint %q not found in HTTP list", path)
		}
	}
}

// ---------- 4. handleEndpoints - health endpoint does not require auth ----------

func TestHandleEndpoints_HealthNoAuth(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	_ = os.Setenv("MDDB_AUTH_ENABLED", "true")
	defer func() { _ = os.Unsetenv("MDDB_AUTH_ENABLED") }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/endpoints", nil)
	s.handleEndpoints(rec, req)

	var resp EndpointsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, ep := range resp.HTTP {
		if ep.Path == "/v1/health" {
			if ep.RequiresAuth {
				t.Error("health endpoint should not require auth")
			}
			return
		}
	}
	t.Error("health endpoint not found in list")
}

// ---------- 5. handleEndpoints - auth enabled adds auth endpoints ----------

func TestHandleEndpoints_AuthEnabledAddsAuthEndpoints(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	_ = os.Setenv("MDDB_AUTH_ENABLED", "true")
	defer func() { _ = os.Unsetenv("MDDB_AUTH_ENABLED") }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/endpoints", nil)
	s.handleEndpoints(rec, req)

	var resp EndpointsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// With auth enabled, auth endpoints should be present
	foundLogin := false
	foundRegister := false
	for _, ep := range resp.HTTP {
		if ep.Path == "/v1/auth/login" {
			foundLogin = true
		}
		if ep.Path == "/v1/auth/register" {
			foundRegister = true
		}
	}

	if !foundLogin {
		t.Error("expected /v1/auth/login endpoint when auth is enabled")
	}
	if !foundRegister {
		t.Error("expected /v1/auth/register endpoint when auth is enabled")
	}
}

// ---------- 6. handleEndpoints - auth disabled has no auth endpoints ----------

func TestHandleEndpoints_AuthDisabledNoAuthEndpoints(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	_ = os.Unsetenv("MDDB_AUTH_ENABLED")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/endpoints", nil)
	s.handleEndpoints(rec, req)

	var resp EndpointsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, ep := range resp.HTTP {
		if ep.Path == "/v1/auth/login" {
			t.Error("auth/login should not be listed when auth is disabled")
		}
	}
}

// ---------- 7. handleEndpoints - gRPC methods contain expected entries ----------

func TestHandleEndpoints_GRPCMethods(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/endpoints", nil)
	s.handleEndpoints(rec, req)

	var resp EndpointsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	expectedGRPC := map[string]bool{
		"Add":          false,
		"Get":          false,
		"Search":       false,
		"VectorSearch": false,
		"Stats":        false,
		"HybridSearch": false,
	}

	for _, m := range resp.GRPC {
		if _, ok := expectedGRPC[m.Name]; ok {
			expectedGRPC[m.Name] = true
		}
	}

	for name, found := range expectedGRPC {
		if !found {
			t.Errorf("expected gRPC method %q not found", name)
		}
	}
}

// ---------- 8. handleEndpoints - MCP tools contain expected entries ----------

func TestHandleEndpoints_MCPTools(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/endpoints", nil)
	s.handleEndpoints(rec, req)

	var resp EndpointsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	expectedMCP := map[string]bool{
		"add_document":     false,
		"search_documents": false,
		"delete_document":  false,
		"semantic_search":  false,
		"get_stats":        false,
		"hybrid_search":    false,
	}

	for _, tool := range resp.MCP {
		if _, ok := expectedMCP[tool.Name]; ok {
			expectedMCP[tool.Name] = true
		}
	}

	for name, found := range expectedMCP {
		if !found {
			t.Errorf("expected MCP tool %q not found", name)
		}
	}
}

// ---------- 9. handleEndpoints - endpoints endpoint itself does not require auth ----------

func TestHandleEndpoints_SelfNoAuth(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	_ = os.Setenv("MDDB_AUTH_ENABLED", "true")
	defer func() { _ = os.Unsetenv("MDDB_AUTH_ENABLED") }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/endpoints", nil)
	s.handleEndpoints(rec, req)

	var resp EndpointsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, ep := range resp.HTTP {
		if ep.Path == "/v1/endpoints" {
			if ep.RequiresAuth {
				t.Error("endpoints endpoint should not require auth")
			}
			return
		}
	}
	t.Error("endpoints endpoint not found in list")
}

// ---------- 10. handleEndpoints - all endpoints have descriptions ----------

func TestHandleEndpoints_AllHaveDescriptions(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/endpoints", nil)
	s.handleEndpoints(rec, req)

	var resp EndpointsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, ep := range resp.HTTP {
		if ep.Description == "" {
			t.Errorf("HTTP endpoint %s %s has empty description", ep.Method, ep.Path)
		}
	}
	for _, m := range resp.GRPC {
		if m.Description == "" {
			t.Errorf("gRPC method %q has empty description", m.Name)
		}
	}
	for _, tool := range resp.MCP {
		if tool.Description == "" {
			t.Errorf("MCP tool %q has empty description", tool.Name)
		}
	}
}
