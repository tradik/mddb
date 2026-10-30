package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCovDAPIKeyMiddlewareFromEnv(t *testing.T) {
	t.Setenv("MDDB_MCP_API_KEY_ENABLED", "")
	if m := NewMCPAPIKeyMiddleware(); m.enabled || len(m.keys) != 0 {
		t.Errorf("disabled middleware = %+v", m)
	}

	t.Setenv("MDDB_MCP_API_KEY_ENABLED", "true")
	t.Setenv("MDDB_MCP_API_KEYS", " sk-short , sk-a-very-long-key:,sk-named:ci-runner,, ")
	t.Setenv("MDDB_MCP_API_KEY_CACHE_TTL", "5s")
	m := NewMCPAPIKeyMiddleware()
	want := map[string]string{
		"sk-short":           "sk-short",    // 8 chars: kept whole
		"sk-a-very-long-key": "sk-a-ver...", // empty name: truncated key
		"sk-named":           "ci-runner",
	}
	if !m.enabled || len(m.keys) != len(want) {
		t.Fatalf("keys = %v", m.keys)
	}
	for k, v := range want {
		if m.keys[k] != v {
			t.Errorf("key %q named %q, want %q", k, m.keys[k], v)
		}
	}
	if m.cacheTTL != 5*time.Second {
		t.Errorf("cacheTTL = %v", m.cacheTTL)
	}

	for _, bad := range []string{"", "nonsense", "-1s"} {
		t.Setenv("MDDB_MCP_API_KEY_CACHE_TTL", bad)
		if got := NewMCPAPIKeyMiddleware().cacheTTL; got != 60*time.Second {
			t.Errorf("TTL %q = %v, want 60s default", bad, got)
		}
	}
}

func TestCovDAPIKeyMiddlewareStoreKeys(t *testing.T) {
	m := &MCPAPIKeyMiddleware{enabled: true, keys: map[string]string{}, cache: map[string]*apiKeyCacheEntry{}, cacheTTL: time.Minute}
	m.SetKeyStore(nil)
	if _, _, ok := m.validateKey("mcp_anything"); ok {
		t.Error("no store and no static keys must reject")
	}

	store := newMCPAPIKeyStore(testDB(t))
	_, key, err := store.Create("dyn", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.SetKeyStore(store)

	var gotName string
	h := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotName = r.Header.Get("X-MCP-Key-Name")
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || gotName != "dyn" {
		t.Errorf("stored key: code=%d name=%q", rec.Code, gotName)
	}

	req = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("X-API-Key", "mcp_unknown")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":-32001`) {
		t.Errorf("unknown key = %d %s", rec.Code, rec.Body)
	}
}

func TestCovDRateLimiterFromEnv(t *testing.T) {
	t.Setenv("MDDB_MCP_RATE_LIMIT_ENABLED", "false")
	if rl := NewMCPRateLimiter(); rl.enabled {
		t.Error("rate limiter must be off unless enabled")
	}

	t.Setenv("MDDB_MCP_RATE_LIMIT_ENABLED", "true")
	t.Setenv("MDDB_MCP_RATE_LIMIT_REQUESTS", "")
	t.Setenv("MDDB_MCP_RATE_LIMIT_WINDOW", "x")
	t.Setenv("MDDB_MCP_RATE_LIMIT_BURST", "-3")
	t.Setenv("MDDB_MCP_RATE_LIMIT_BY", "")
	rl := NewMCPRateLimiter()
	if !rl.enabled || rl.limit != 100 || rl.window != time.Minute || rl.burst != 20 || rl.by != "ip" {
		t.Errorf("defaults = limit %d window %v burst %d by %q", rl.limit, rl.window, rl.burst, rl.by)
	}

	t.Setenv("MDDB_MCP_RATE_LIMIT_REQUESTS", "7")
	t.Setenv("MDDB_MCP_RATE_LIMIT_WINDOW", "10")
	t.Setenv("MDDB_MCP_RATE_LIMIT_BURST", "2")
	t.Setenv("MDDB_MCP_RATE_LIMIT_BY", "session")
	rl = NewMCPRateLimiter()
	if rl.limit != 7 || rl.window != 10*time.Second || rl.burst != 2 || rl.by != "session" {
		t.Errorf("configured = limit %d window %v burst %d by %q", rl.limit, rl.window, rl.burst, rl.by)
	}
}

func TestCovDRateLimiterClientID(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.RemoteAddr = "203.0.113.9:4000"
	ip := "ip:" + ClientIP(req)

	byKey := &MCPRateLimiter{by: "api_key"}
	if got := byKey.clientID(req); got != ip {
		t.Errorf("api_key without name = %q, want %q", got, ip)
	}
	req.Header.Set("X-MCP-Key-Name", "ci")
	if got := byKey.clientID(req); got != "key:ci" {
		t.Errorf("api_key = %q", got)
	}

	bySession := &MCPRateLimiter{by: "session"}
	if got := bySession.clientID(req); got != ip {
		t.Errorf("session without id = %q, want %q", got, ip)
	}
	req.Header.Set("MCP-Session-Id", "s1")
	if got := bySession.clientID(req); got != "session:s1" {
		t.Errorf("session = %q", got)
	}

	if got := (&MCPRateLimiter{by: "ip"}).clientID(req); got != ip {
		t.Errorf("ip = %q", got)
	}
}

func TestCovDRateLimiterRejectsAndCleansUp(t *testing.T) {
	// A zero budget rejects the first request; a window shorter than a second
	// makes the computed Retry-After zero, which is raised to one.
	rl := &MCPRateLimiter{enabled: true, limit: 0, burst: 0, window: time.Millisecond, by: "ip",
		clients: map[string]*rateLimitBucket{"ip:gone": {count: 3, windowAt: time.Now().Add(-time.Hour)}}}
	reached := false
	h := rl.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if reached || rec.Code != http.StatusTooManyRequests {
		t.Fatalf("code=%d reached=%v", rec.Code, reached)
	}
	if rec.Header().Get("Retry-After") != "1" || rec.Header().Get("X-RateLimit-Remaining") != "0" ||
		!strings.Contains(rec.Body.String(), `"code":-32429`) {
		t.Errorf("headers=%v body=%s", rec.Header(), rec.Body)
	}
	rl.mu.Lock()
	_, stale := rl.clients["ip:gone"]
	rl.mu.Unlock()
	if stale {
		t.Error("expired bucket survived the cleanup")
	}
}

func TestCovDRequestLoggerFromEnv(t *testing.T) {
	t.Setenv("MDDB_MCP_LOGGING_ENABLED", "")
	t.Setenv("MDDB_MCP_LOGGING_LEVEL", "")
	if rl := NewMCPRequestLogger(); rl.enabled || rl.level != "info" {
		t.Errorf("default logger = %+v", rl)
	}

	t.Setenv("MDDB_MCP_LOGGING_ENABLED", "true")
	t.Setenv("MDDB_MCP_LOGGING_LEVEL", "debug")
	rl := NewMCPRequestLogger()
	if !rl.enabled || rl.level != "debug" {
		t.Fatalf("configured logger = %+v", rl)
	}

	recs := captureLogs(t, func() {
		h := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}))
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Header.Set("X-MCP-Key-Name", "ci")
		req.Header.Set("MCP-Session-Id", "s9")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusTeapot {
			t.Errorf("status = %d", rec.Code)
		}
	})
	var audit string
	for _, r := range recs {
		if s, ok := r["string"].(string); ok && strings.Contains(s, `"path":"/mcp"`) {
			audit = s
		}
	}
	for _, want := range []string{`"status":418`, `"key_name":"ci"`, `"session_id":"s9"`, `"method":"POST"`} {
		if !strings.Contains(audit, want) {
			t.Errorf("audit entry %q lacks %s (records %v)", audit, want, recs)
		}
	}
}

func TestCovDAPIKeyStaticToolsets(t *testing.T) {
	t.Setenv("MDDB_MCP_TOOLSETS", "")
	t.Setenv("MDDB_MCP_API_KEY_ENABLED", "true")
	t.Setenv("MDDB_MCP_API_KEYS", "sk-coder:coder:search|code,sk-broken:broken:nope,sk-all:everything:all,sk-plain:plain")
	m := NewMCPAPIKeyMiddleware()

	if s := m.keyToolsets["sk-coder"]; len(s) != 2 || !s[ToolsetSearch] || !s[ToolsetCode] {
		t.Errorf("coder toolsets = %v", s)
	}
	if s, ok := m.keyToolsets["sk-broken"]; !ok || len(s) != 0 {
		t.Errorf("an unreadable limit must fail closed, got %v %v", s, ok)
	}
	if _, ok := m.keyToolsets["sk-all"]; ok {
		t.Error("\"all\" must leave the key unrestricted")
	}
	if _, ok := m.keyToolsets["sk-plain"]; ok {
		t.Error("a key without a third field is unrestricted")
	}

	// The limit travels in the request context to the handler.
	var codeOK, adminOK bool
	h := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		codeOK = builtinToolAllowed(r.Context(), "code_graph")
		adminOK = builtinToolAllowed(r.Context(), "get_stats")
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("X-API-Key", "sk-coder")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !codeOK || adminOK {
		t.Errorf("coder key: code_graph=%v get_stats=%v; want true false", codeOK, adminOK)
	}
}
