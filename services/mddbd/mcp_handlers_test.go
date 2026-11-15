package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// ---------- 1. handleMCPConfig - GET success ----------

func TestHandleMCPConfig_Success(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/config", nil)
	s.handleMCPConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "text/yaml" {
		t.Errorf("expected Content-Type text/yaml, got %q", ct)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "mcp:") {
		t.Errorf("expected YAML with mcp: key, got %s", body)
	}
	if !strings.Contains(body, "mddb:") {
		t.Errorf("expected YAML with mddb: key, got %s", body)
	}
}

// ---------- 2. handleMCPConfig - wrong method ----------

func TestHandleMCPConfig_MethodNotAllowed(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete}
	for _, method := range methods {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/v1/mcp/config", nil)
		s.handleMCPConfig(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: expected 405, got %d", method, rec.Code)
		}
	}
}

// ---------- 3. handleMCPConfig - contains default gRPC address ----------

func TestHandleMCPConfig_DefaultGRPCAddr(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	_ = os.Unsetenv("MDDB_GRPC_ADDR")
	_ = os.Unsetenv("MDDB_ADDR")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/config", nil)
	s.handleMCPConfig(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, ":11024") {
		t.Errorf("expected default gRPC port :11024, got %s", body)
	}
	if !strings.Contains(body, ":11023") {
		t.Errorf("expected default HTTP port :11023, got %s", body)
	}
}

// ---------- 4. handleMCPConfig - contains transport mode ----------

func TestHandleMCPConfig_ContainsTransportMode(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/config", nil)
	s.handleMCPConfig(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "transportMode") {
		t.Errorf("expected transportMode in YAML, got %s", body)
	}
	if !strings.Contains(body, "grpc_with_rest_fallback") {
		t.Errorf("expected grpc_with_rest_fallback in YAML, got %s", body)
	}
}

// ---------- 5. handleMCPConfig - contains commented listenAddress ----------

func TestHandleMCPConfig_ContainsListenAddress(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/config", nil)
	s.handleMCPConfig(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "listenAddress") {
		t.Errorf("expected listenAddress in YAML, got %s", body)
	}
	if !strings.Contains(body, "0.0.0.0:9000") {
		t.Errorf("expected MCP listen address 0.0.0.0:9000, got %s", body)
	}
}

// ---------- 6. handleMCPConfig - contains timeout and retry settings ----------

func TestHandleMCPConfig_ContainsTimeoutAndRetries(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/config", nil)
	s.handleMCPConfig(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "timeoutSeconds") {
		t.Errorf("expected timeoutSeconds in YAML, got %s", body)
	}
	if !strings.Contains(body, "maxRetries") {
		t.Errorf("expected maxRetries in YAML, got %s", body)
	}
}

// ---------- 7. handleMCPConfig - custom addresses from env ----------

func TestHandleMCPConfig_CustomAddresses(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	_ = os.Setenv("MDDB_ADDR", ":9999")
	_ = os.Setenv("MDDB_GRPC_ADDR", ":8888")
	defer func() { _ = os.Unsetenv("MDDB_ADDR") }()
	defer func() { _ = os.Unsetenv("MDDB_GRPC_ADDR") }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/config", nil)
	s.handleMCPConfig(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, ":8888") {
		t.Errorf("expected custom gRPC port :8888, got %s", body)
	}
	if !strings.Contains(body, ":9999") {
		t.Errorf("expected custom HTTP port :9999, got %s", body)
	}
}

// ---------- 8. handleMCPConfig - YAML is not empty ----------

func TestHandleMCPConfig_NonEmptyBody(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/config", nil)
	s.handleMCPConfig(rec, req)

	if rec.Body.Len() == 0 {
		t.Error("expected non-empty response body")
	}

	// Verify it looks like valid YAML (has key: value structure)
	body := rec.Body.String()
	lines := strings.Split(body, "\n")
	hasKeyValue := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, ":") {
			hasKeyValue = true
			break
		}
	}
	if !hasKeyValue {
		t.Error("expected YAML key:value pairs in response")
	}
}

// ---------- advertisedAddr and the generated addresses (2.19.6) ----------

func TestHandleMCPConfig_UnwritableAnswer(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	s.handleMCPConfig(brokenResponse{rec}, httptest.NewRequest(http.MethodGet, "/v1/mcp/config", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("unwritable answer = %d, want 500", rec.Code)
	}
}

func TestAdvertisedAddr(t *testing.T) {
	cases := []struct{ domain, listen, want string }{
		{"", ":11024", "localhost:11024"},
		{"", "0.0.0.0:11024", "localhost:11024"},
		{"", "[::]:11024", "localhost:11024"},
		{"", "127.0.0.1:18924", "127.0.0.1:18924"},
		{"db.example.com", "127.0.0.1:18924", "db.example.com:18924"},
		{"db.example.com", ":11023", "db.example.com:11023"},
		{"", "unix-socket", "unix-socket"},
	}
	for _, c := range cases {
		if got := advertisedAddr(c.domain, c.listen); got != c.want {
			t.Errorf("advertisedAddr(%q, %q) = %q, want %q", c.domain, c.listen, got, c.want)
		}
	}
}

func TestHandleMCPConfig_BoundAddressAndDomain(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	t.Setenv("MDDB_GRPC_ADDR", "127.0.0.1:18924")
	t.Setenv("MDDB_ADDR", "127.0.0.1:18923")
	t.Setenv("MDDB_MCP_DOMAIN", "")
	get := func() string {
		rec := httptest.NewRecorder()
		s.handleMCPConfig(rec, httptest.NewRequest(http.MethodGet, "/v1/mcp/config", nil))
		return rec.Body.String()
	}

	body := get()
	if strings.Contains(body, "localhost127.0.0.1") {
		t.Fatalf("listen host concatenated onto localhost: %s", body)
	}
	for _, want := range []string{`grpcAddress: "127.0.0.1:18924"`, `restBaseUrl: "http://127.0.0.1:18923"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in %s", want, body)
		}
	}

	t.Setenv("MDDB_MCP_DOMAIN", "db.example.com")
	body = get()
	for _, want := range []string{`grpcAddress: "db.example.com:18924"`, `restBaseUrl: "http://db.example.com:18923"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in %s", want, body)
		}
	}
}
