package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestMcpGetMetaMap(t *testing.T) {
	in := map[string]interface{}{
		"meta": map[string]interface{}{
			"tag":     "go",
			"authors": []interface{}{"a", "b", 7, "c"}, // non-string element should be skipped
			"empty":   []interface{}{},
		},
		"other": "x",
	}
	got := mcpGetMetaMap(in, "meta")
	if !reflect.DeepEqual(got["tag"], []string{"go"}) {
		t.Errorf("tag: got %v", got["tag"])
	}
	// A non-string entry is dropped, not kept as "". An empty metadata value
	// is indexed and searchable, so blanking one invents a value the caller
	// never wrote — this used to leave a phantom at index 2 (TEST-002).
	if len(got["authors"]) != 3 {
		t.Errorf("authors: got %v, want the three strings with the non-string dropped", got["authors"])
	}
	for _, v := range got["authors"] {
		if v == "" {
			t.Errorf("authors contains a phantom empty value: %v", got["authors"])
		}
	}
	if len(got["empty"]) != 0 {
		t.Errorf("empty: got %v", got["empty"])
	}

	if mm := mcpGetMetaMap(in, "missing"); len(mm) != 0 {
		t.Errorf("missing key should yield empty map, got %v", mm)
	}
	if mm := mcpGetMetaMap(in, "other"); len(mm) != 0 {
		t.Errorf("non-map value should yield empty map, got %v", mm)
	}
}

func TestMcpGetFloat64Map(t *testing.T) {
	in := map[string]interface{}{
		"weights": map[string]interface{}{"a": 1.5, "b": 2.0, "c": "skip"},
		"empty":   map[string]interface{}{"x": "no"},
	}
	got := mcpGetFloat64Map(in, "weights")
	if got["a"] != 1.5 || got["b"] != 2.0 {
		t.Errorf("got %v", got)
	}
	if _, ok := got["c"]; ok {
		t.Errorf("non-numeric should be filtered")
	}
	if mcpGetFloat64Map(in, "missing") != nil {
		t.Error("missing key should yield nil")
	}
	if mcpGetFloat64Map(in, "empty") != nil {
		t.Error("all-skipped should yield nil")
	}
}

func TestMCPRateLimiter_ClientID(t *testing.T) {
	cases := []struct {
		name    string
		by      string
		header  map[string]string
		wantPfx string
	}{
		{"api_key with name", "api_key", map[string]string{"X-MCP-Key-Name": "alice"}, "key:alice"},
		{"api_key fallback to ip", "api_key", nil, "ip:"},
		{"session with id", "session", map[string]string{"MCP-Session-Id": "s1"}, "session:s1"},
		{"session fallback to ip", "session", nil, "ip:"},
		{"default ip", "ip", nil, "ip:"},
		{"unknown defaults to ip", "weird", nil, "ip:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rl := &MCPRateLimiter{by: tc.by}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "10.0.0.5:1234"
			for k, v := range tc.header {
				req.Header.Set(k, v)
			}
			got := rl.clientID(req)
			if got == "" || (got[:len(tc.wantPfx)] != tc.wantPfx) {
				t.Errorf("got %q, want prefix %q", got, tc.wantPfx)
			}
		})
	}
}

func TestAuditAuth_NilSafe(t *testing.T) {
	// Both nil and {server:nil} paths must early-return without panicking.
	var am *AuthManager
	am.auditAuth(httptest.NewRequest(http.MethodGet, "/", nil), "u", "login", "ok", "")

	am2 := &AuthManager{}
	am2.auditAuth(httptest.NewRequest(http.MethodGet, "/", nil), "u", "login", "ok", "")
}

func TestMcpGetBool(t *testing.T) {
	in := map[string]interface{}{"a": true, "b": false, "c": "yes", "d": 1}
	if !mcpGetBool(in, "a") {
		t.Error("a should be true")
	}
	if mcpGetBool(in, "b") {
		t.Error("b should be false")
	}
	// "yes" is true. mcpGetBool now delegates to mcpCoerceBool, which exists
	// precisely because "silently ignoring a stringified bool would be a
	// footgun" — its own words. This test pinned the opposite, so the file
	// held two contradictory decisions about the same question (TEST-002).
	if !mcpGetBool(in, "c") {
		t.Error(`"yes" must be read as true — LLM clients send it`)
	}
	if mcpGetBool(in, "d") {
		t.Error("non-bool int must yield false")
	}
	if mcpGetBool(in, "missing") {
		t.Error("missing key must yield false")
	}
}

func TestMcpGetPrompt(t *testing.T) {
	ctx := context.Background()

	// Each known prompt with a valid required argument should return a single
	// user-role message and a non-empty title.
	cases := []struct {
		prompt string
		args   map[string]interface{}
		title  string
	}{
		{"analyze-collection", map[string]interface{}{"collection": "blog"}, "Analyze collection: blog"},
		{"search-help", map[string]interface{}{"use_case": "find docs"}, "Search help for: find docs"},
		{"summarize-collection", map[string]interface{}{"collection": "blog"}, "Summarize collection: blog"},
		{"import-guide", map[string]interface{}{"source": "wordpress"}, "Import guide: wordpress"},
		{"rag-pipeline", map[string]interface{}{"collection": "blog", "model": "claude"}, "RAG pipeline for: blog"},
		{"rag-pipeline", map[string]interface{}{"collection": "blog"}, "RAG pipeline for: blog"}, // model defaults
	}
	for _, tc := range cases {
		t.Run(tc.prompt, func(t *testing.T) {
			msgs, title, err := mcpGetPrompt(ctx, nil, tc.prompt, tc.args)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if title != tc.title {
				t.Errorf("title=%q want=%q", title, tc.title)
			}
			if len(msgs) != 1 || msgs[0].Role != "user" {
				t.Errorf("messages: %+v", msgs)
			}
		})
	}

	// Missing required argument should error.
	missing := []struct {
		prompt string
		args   map[string]interface{}
	}{
		{"analyze-collection", map[string]interface{}{}},
		{"search-help", map[string]interface{}{}},
		{"summarize-collection", map[string]interface{}{}},
		{"import-guide", map[string]interface{}{}},
		{"rag-pipeline", map[string]interface{}{}},
	}
	for _, tc := range missing {
		t.Run("missing-"+tc.prompt, func(t *testing.T) {
			if _, _, err := mcpGetPrompt(ctx, nil, tc.prompt, tc.args); err == nil {
				t.Error("expected error for missing argument")
			}
		})
	}

	// Unknown prompt name.
	if _, _, err := mcpGetPrompt(ctx, nil, "no-such", nil); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("expected unknown-prompt error, got %v", err)
	}
}

func TestMcpCompletePromptArg(t *testing.T) {
	cases := []struct {
		argName, argValue string
		wantHas           string // expected substring in returned options (or "" if no options)
		wantTotal         int
	}{
		{"source", "", "wordpress", 5},
		{"source", "wo", "wordpress", 5},
		{"model", "", "claude", 4},
		{"algorithm", "bm", "bm25", 4},
		{"unknown", "", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.argName+"-"+tc.argValue, func(t *testing.T) {
			opts, total, _ := mcpCompletePromptArg(tc.argName, tc.argValue)
			if total != tc.wantTotal {
				t.Errorf("total=%d, want %d", total, tc.wantTotal)
			}
			if tc.wantHas != "" {
				found := false
				for _, o := range opts {
					if strings.Contains(o, tc.wantHas) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected %q in %v", tc.wantHas, opts)
				}
			}
		})
	}
}

func TestMcpCompleteCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "blog", "k1", "en", "x", nil)
	addTestDoc(t, s, "blog2", "k1", "en", "x", nil)
	addTestDoc(t, s, "news", "k1", "en", "x", nil)

	client := NewDirectClient(s)

	all, total, _ := mcpCompleteCollection(context.Background(), client, "")
	if total < 3 {
		t.Errorf("expected total>=3, got %d (%v)", total, all)
	}

	// Prefix match should narrow.
	got, _, _ := mcpCompleteCollection(context.Background(), client, "blo")
	for _, c := range got {
		if !strings.HasPrefix(strings.ToLower(c), "blo") {
			t.Errorf("unexpected match: %q", c)
		}
	}
}

func TestSafeBackupPath_DefaultDir(t *testing.T) {
	// When MDDB_BACKUP_DIR is unset, the helper falls back to "./backups".
	// We point it at a tempdir so the test does not pollute the cwd.
	dir := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", dir)
	got, err := safeBackupPath("snap.db", false)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("empty path")
	}
}
