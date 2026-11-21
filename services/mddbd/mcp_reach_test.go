package main

// #440: GET /v1/config says how MCP clients reach this server — the public
// URL, whether /mcp asks for an API key and whether OAuth runs — so the
// panel's LLM Connections can build a command that connects, and never
// reports a key or secret.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

func TestMCPPublicURLFromEnv(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"https://mcp.example.com", "https://mcp.example.com"},
		{"https://mcp.example.com/", "https://mcp.example.com"},
		{"  http://10.0.0.5:9000//  ", "http://10.0.0.5:9000"},
		{"https://example.com/mddb/", "https://example.com/mddb"},
		{"https://mcp.example.com/mcp", "https://mcp.example.com"},    // the endpoint itself
		{"https://example.com/mddb/mcp/", "https://example.com/mddb"}, // the same under a path
		{"https://example.com/mcpx", "https://example.com/mcpx"},      // not the endpoint
		{"mcp.example.com", ""},                                       // not absolute
		{"ftp://mcp.example.com", ""},                                 // not http or https
		{"https://", ""},                                              // no host
		{"http://[::1", ""},                                           // does not parse
	} {
		t.Setenv("MDDB_MCP_PUBLIC_URL", tc.in)
		if got := mcpPublicURLFromEnv(); got != tc.want {
			t.Errorf("MDDB_MCP_PUBLIC_URL=%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

// loadMCPCustomTools shares mcp_config.go with the MDDB_MCP_* readers: a
// missing, unreadable, malformed or invalid file loads no tools; a valid one
// loads its tools.
func TestLoadMCPCustomToolsFromEnv(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	valid := write("valid.yaml", `custom_tools:
  - name: kb_lookup
    description: Keyword search in the knowledge base
    action: full_text_search
    parameters:
      - name: query
        type: string
`)
	for _, tc := range []struct {
		name, path string
		want       int
	}{
		{"unset", "", 0},
		{"missing", filepath.Join(dir, "absent.yaml"), 0},
		{"a directory", dir, 0},
		{"malformed", write("bad.yaml", "custom_tools: [\n"), 0},
		{"invalid tool", write("invalid.yaml", "custom_tools:\n  - name: \"\"\n"), 0},
		{"empty", write("empty.yaml", ""), 0},
		{"valid", valid, 1},
	} {
		t.Setenv("MDDB_MCP_CONFIG", tc.path)
		if got := loadMCPCustomTools(); len(got) != tc.want {
			t.Errorf("%s: %d tools, want %d", tc.name, len(got), tc.want)
		}
	}
}

func TestMCPPublicURLIsReadIntoTheConfig(t *testing.T) {
	t.Setenv("MDDB_MCP_PUBLIC_URL", "https://mcp.example.com/")
	cfg := defaultServerConfig()
	applyEnvConfig(&cfg)
	if cfg.MCP.PublicURL != "https://mcp.example.com" {
		t.Errorf("PublicURL = %q", cfg.MCP.PublicURL)
	}
	if configKeys["MDDB_MCP_PUBLIC_URL"] != cfgURL {
		t.Error("MDDB_MCP_PUBLIC_URL must be classified as a URL")
	}
}

// mcpReach is protocols.mcp of GET /v1/config, and the raw body.
func mcpReach(t *testing.T, s *Server) (MCPProtocolStatus, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleConfig(rec, httptest.NewRequest(http.MethodGet, "/v1/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var decoded struct {
		Protocols struct {
			MCP MCPProtocolStatus `json:"mcp"`
		} `json:"protocols"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return decoded.Protocols.MCP, rec.Body.String()
}

func TestConfigReportsHowMCPIsReached(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	t.Setenv("MDDB_MCP_API_KEY_ENABLED", "")

	got, body := mcpReach(t, s)
	if got.PublicURL != "" || got.APIKeyEnabled || got.OAuthEnabled {
		t.Errorf("defaults: %+v", got)
	}
	for _, field := range []string{`"publicUrl":""`, `"apiKeyEnabled":false`, `"oauthEnabled":false`} {
		if !strings.Contains(body, field) {
			t.Errorf("body lacks %s: %s", field, body)
		}
	}

	t.Setenv("MDDB_MCP_API_KEY_ENABLED", "true")
	s.Config.MCP.PublicURL = "https://user:pw@mcp.example.com?token=x"
	if got, _ = mcpReach(t, s); got.PublicURL != "https://mcp.example.com" || !got.APIKeyEnabled || got.OAuthEnabled {
		t.Errorf("public URL with credentials and keys on: %+v", got)
	}
}

func TestConfigReportsTheOAuthStateTheListenerRuns(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	const secret = "0123456789abcdef0123456789abcdef-secret"
	t.Setenv("MDDB_MCP_API_KEY_ENABLED", "true")
	t.Setenv("MDDB_MCP_API_KEYS", "mcp_static_key_value:bot")
	t.Setenv("MDDB_MCP_OAUTH_ENABLED", "true")
	t.Setenv("MDDB_MCP_OAUTH_ISSUER", "mcp.example.com") // invalid: OAuth stays off
	t.Setenv("MDDB_MCP_OAUTH_SECRET", secret)
	keys := &MCPAPIKeyMiddleware{enabled: true, keys: map[string]string{}, cache: map[string]*apiKeyCacheEntry{}}

	s.mcpOAuth.Store(newMCPOAuthFromEnv(keys))
	if got, _ := mcpReach(t, s); got.OAuthEnabled || got.PublicURL != "" {
		t.Errorf("an invalid issuer leaves OAuth off: %+v", got)
	}

	t.Setenv("MDDB_MCP_OAUTH_ISSUER", "https://issuer.example.com/")
	s.mcpOAuth.Store(newMCPOAuthFromEnv(keys))
	got, body := mcpReach(t, s)
	if !got.OAuthEnabled || got.PublicURL != "https://issuer.example.com" {
		t.Errorf("OAuth on, no public URL: the issuer is the URL: %+v", got)
	}
	for _, leaked := range []string{secret, "mcp_static_key_value"} {
		if strings.Contains(body, leaked) {
			t.Errorf("GET /v1/config reveals %q", leaked)
		}
	}

	s.Config.MCP.PublicURL = "https://mcp.example.com"
	if got, _ = mcpReach(t, s); got.PublicURL != "https://mcp.example.com" {
		t.Errorf("MDDB_MCP_PUBLIC_URL wins over the issuer: %+v", got)
	}
	if b := s.configResponse(audienceBundle).Protocols.MCP.PublicURL; b != "<set: https>" {
		t.Errorf("the diagnose bundle keeps only the scheme, got %q", b)
	}
}
