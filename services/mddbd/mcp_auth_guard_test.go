package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestApplyMCPAuth_MCPKeyWithoutKeyAuth — #445: with main auth guarding the
// listener, an mcp_ key (either header) gets a 401 that names
// MDDB_MCP_API_KEY_ENABLED, never reaching the backend; other credentials,
// public paths and a disabled manager are untouched.
func TestApplyMCPAuth_MCPKeyWithoutKeyAuth(t *testing.T) {
	t.Setenv("MDDB_AUTH_ENABLED", "true")
	t.Setenv("MDDB_MCP_API_KEY_ENABLED", "")

	am, cleanup := authMwSetup(t)
	defer cleanup()
	s := &Server{AuthManager: am}
	var reached bool
	handler := s.applyMCPAuth(mcpProbeHandler(&reached), ":9000")

	send := func(path, header, value string) *httptest.ResponseRecorder {
		reached = false
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.Header.Set(header, value)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	for _, h := range [][2]string{{"Authorization", "Bearer mcp_0123abcd"}, {"X-API-Key", "mcp_0123abcd"}} {
		rec := send("/mcp", h[0], h[1])
		if rec.Code != http.StatusUnauthorized || reached {
			t.Errorf("%s: got %d reached=%v, want 401 false", h[0], rec.Code, reached)
		}
		if !strings.Contains(rec.Body.String(), "MDDB_MCP_API_KEY_ENABLED=true") || strings.Contains(rec.Body.String(), "invalid") {
			t.Errorf("%s: body %q does not name the setting", h[0], rec.Body)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: Content-Type %q", h[0], ct)
		}
	}

	// Any other credential still meets the AuthManager's own refusal.
	if rec := send("/mcp", "Authorization", "Bearer not-a-jwt"); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "invalid token") {
		t.Errorf("bad JWT: got %d %s", rec.Code, rec.Body)
	}
	// A public path needs no credential, whatever is sent.
	if rec := send("/health", "X-API-Key", "mcp_0123abcd"); rec.Code != http.StatusOK || !reached {
		t.Errorf("public path: got %d reached=%v", rec.Code, reached)
	}

	// A disabled manager guards nothing: the wrapper passes through too.
	am.enabled = false
	reached = false
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Header.Set("X-API-Key", "mcp_0123abcd")
	rec := httptest.NewRecorder()
	refuseMCPKeyWithoutKeyAuth(am, mcpProbeHandler(&reached)).ServeHTTP(rec, req)
	if !reached {
		t.Errorf("disabled manager: got %d, want pass-through", rec.Code)
	}
}

// TestDecideMCPAuth covers every flag combination of the SEC-002 decision.
func TestDecideMCPAuth(t *testing.T) {
	tests := []struct {
		name          string
		authEnabled   bool
		mcpKeyEnabled bool
		loopback      bool
		wantWrap      bool
		wantWarn      bool
	}{
		{"auth on, mcp key off, public bind -> wrap", true, false, false, true, false},
		{"auth on, mcp key off, loopback -> wrap", true, false, true, true, false},
		{"auth on, mcp key on -> nothing (mcp keys guard)", true, true, false, false, false},
		{"auth off, mcp key on -> nothing", false, true, false, false, false},
		{"auth off, mcp key off, public bind -> warn", false, false, false, false, true},
		{"auth off, mcp key off, loopback -> nothing (dev)", false, false, true, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := decideMCPAuth(tt.authEnabled, tt.mcpKeyEnabled, tt.loopback)
			if d.wrapMainAuth != tt.wantWrap {
				t.Errorf("wrapMainAuth = %v, want %v", d.wrapMainAuth, tt.wantWrap)
			}
			if d.warnInsecure != tt.wantWarn {
				t.Errorf("warnInsecure = %v, want %v", d.warnInsecure, tt.wantWarn)
			}
			if (tt.wantWrap || tt.wantWarn) && d.reason == "" {
				t.Error("expected a non-empty reason for an actionable decision")
			}
		})
	}
}

func TestIsLoopbackListenAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{":9000", false},         // all interfaces
		{"0.0.0.0:9000", false},  // all interfaces, explicit
		{"127.0.0.1:9000", true}, // IPv4 loopback
		{"[::1]:9000", true},     // IPv6 loopback
		{"localhost:9000", true}, // loopback hostname
		{"192.168.1.5:9000", false},
		{"mddb.example.com:9000", false},
		{"unix:/tmp/mddb-mcp.sock", true}, // filesystem-scoped
		{"localhost", true},               // no port → SplitHostPort errors, host=addr
		{"10.0.0.1", false},               // bare IP, no port
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := isLoopbackListenAddr(tt.addr); got != tt.want {
				t.Errorf("isLoopbackListenAddr(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

// mcpProbeHandler is a sentinel that records whether a request reached the MCP
// backend (i.e. passed authentication).
func mcpProbeHandler(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

// TestApplyMCPAuth_GatesWithMainAuth — SEC-002 core: with MDDB_AUTH_ENABLED=true
// and no MCP key auth, an anonymous tools/call is rejected (401), while a request
// carrying a valid main-API key reaches the handler (200).
func TestApplyMCPAuth_GatesWithMainAuth(t *testing.T) {
	t.Setenv("MDDB_AUTH_ENABLED", "true")
	t.Setenv("MDDB_MCP_API_KEY_ENABLED", "") // MCP has no key auth of its own

	am, cleanup := authMwSetup(t)
	defer cleanup()
	s := &Server{AuthManager: am}

	apiKey, err := am.CreateAPIKey("admin", "sec-002-test", 0)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	var reached bool
	handler := s.applyMCPAuth(mcpProbeHandler(&reached), ":9000")

	// 1) Anonymous /tools/call -> 401, never reaches the backend.
	reached = false
	req := httptest.NewRequest(http.MethodPost, "/tools/call", strings.NewReader(`{"name":"ping"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous /tools/call: got %d, want 401", rec.Code)
	}
	if reached {
		t.Error("anonymous request reached the MCP backend — auth bypass still open")
	}

	// 2) With a valid API key -> reaches the backend (200).
	reached = false
	req = httptest.NewRequest(http.MethodPost, "/tools/call", strings.NewReader(`{"name":"ping"}`))
	req.Header.Set("X-API-Key", apiKey)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !reached {
		t.Errorf("authenticated /tools/call: got code=%d reached=%v, want 200 true", rec.Code, reached)
	}
}

// TestApplyMCPAuth_NoDoubleGateWhenMCPKeyEnabled — when MCP runs its own key
// auth, applyMCPAuth must NOT additionally wrap with the main AuthManager
// (avoid double-gating); the handler is returned unchanged.
func TestApplyMCPAuth_NoDoubleGateWhenMCPKeyEnabled(t *testing.T) {
	t.Setenv("MDDB_AUTH_ENABLED", "true")
	t.Setenv("MDDB_MCP_API_KEY_ENABLED", "true")

	am, cleanup := authMwSetup(t)
	defer cleanup()
	s := &Server{AuthManager: am}

	var reached bool
	handler := s.applyMCPAuth(mcpProbeHandler(&reached), ":9000")

	req := httptest.NewRequest(http.MethodPost, "/tools/call", strings.NewReader(`{"name":"ping"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !reached {
		t.Errorf("with MCP key auth, applyMCPAuth must pass through: got code=%d reached=%v, want 200 true", rec.Code, reached)
	}
}

// TestApplyMCPAuth_WarnsWhenUnauthenticatedPublic — auth off + no MCP key on a
// non-loopback bind: applyMCPAuth logs a warning and passes the handler through
// unchanged (dev/legacy behaviour preserved, but operators are alerted).
func TestApplyMCPAuth_WarnsWhenUnauthenticatedPublic(t *testing.T) {
	t.Setenv("MDDB_AUTH_ENABLED", "false")
	t.Setenv("MDDB_MCP_API_KEY_ENABLED", "")

	s := &Server{}
	var reached bool
	handler := s.applyMCPAuth(mcpProbeHandler(&reached), ":9000")

	req := httptest.NewRequest(http.MethodPost, "/tools/call", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !reached {
		t.Error("unauthenticated public MCP should pass through (warn-only), handler not reached")
	}
}

// TestApplyMCPAuth_NilAuthManagerFailsOpenWithWarning — defensive path: if main
// auth is on but AuthManager is somehow nil, applyMCPAuth cannot gate and must
// return the handler unchanged (the warning is logged, not asserted here).
func TestApplyMCPAuth_NilAuthManager(t *testing.T) {
	t.Setenv("MDDB_AUTH_ENABLED", "true")
	t.Setenv("MDDB_MCP_API_KEY_ENABLED", "")

	s := &Server{} // AuthManager nil

	var reached bool
	handler := s.applyMCPAuth(mcpProbeHandler(&reached), ":9000")

	req := httptest.NewRequest(http.MethodPost, "/tools/call", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !reached {
		t.Error("nil AuthManager: handler should be returned unchanged (cannot gate)")
	}
}
