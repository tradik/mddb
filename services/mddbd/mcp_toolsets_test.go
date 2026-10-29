package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

func TestEveryBuiltinToolHasAToolset(t *testing.T) {
	builtin := map[string]bool{}
	for _, tool := range mcpBuiltinTools() {
		builtin[tool.Name] = true
		if _, ok := mcpToolGroup[tool.Name]; !ok {
			t.Errorf("%s is in no MCP toolset: add it to mcpToolsets in mcp_toolsets.go", tool.Name)
		}
	}
	seen := map[string]string{}
	for set, tools := range mcpToolsets {
		for _, name := range tools {
			if !builtin[name] {
				t.Errorf("toolset %s lists %s, which is not a built-in tool", set, name)
			}
			if other, dup := seen[name]; dup {
				t.Errorf("%s is in two toolsets: %s and %s", name, other, set)
			}
			seen[name] = set
		}
	}
}

func toolNames(tools []MCPTool) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, tool.Name)
	}
	sort.Strings(out)
	return out
}

func schemaBytes(tools []MCPTool) int {
	data, _ := json.Marshal(map[string]any{"tools": tools})
	return len(data)
}

func TestToolsets_ServerSetting(t *testing.T) {
	all := mcpAllTools(nil)
	t.Setenv("MDDB_MCP_TOOLSETS", "search, CODE")
	narrow := mcpAllTools(nil)
	for _, name := range toolNames(narrow) {
		if g := mcpToolGroup[name]; g != ToolsetSearch && g != ToolsetCode {
			t.Errorf("%s (%s) listed with search,code", name, g)
		}
	}
	if len(narrow) != len(mcpToolsets[ToolsetSearch])+len(mcpToolsets[ToolsetCode]) {
		t.Fatalf("search,code lists %d tools", len(narrow))
	}
	// The measurement MCP-003 asks for: schema bytes (≈ tokens × 4) per tools/list.
	t.Logf("tools/list: all %d tools = %d bytes (~%d tokens); search,code %d tools = %d bytes (~%d tokens)",
		len(all), schemaBytes(all), schemaBytes(all)/4, len(narrow), schemaBytes(narrow), schemaBytes(narrow)/4)

	t.Setenv("MDDB_MCP_TOOLSETS", "all")
	if len(mcpAllTools(nil)) != len(all) {
		t.Fatal("all lists everything")
	}
	t.Setenv("MDDB_MCP_TOOLSETS", "search,nope")
	if len(mcpAllTools(nil)) != len(all) {
		t.Fatal("an invalid setting falls back to all")
	}
	t.Setenv("MDDB_MCP_TOOLSETS", "")
	t.Setenv("MDDB_MCP_BUILTIN_TOOLS", "false")
	if got := mcpAllTools([]MCPCustomToolConfig{{Name: "mine", Action: "semantic_search"}}); len(got) != 1 || got[0].Name != "mine" {
		t.Fatalf("builtin off keeps only custom tools: %v", toolNames(got))
	}
}

func TestToolsets_ParseErrors(t *testing.T) {
	if _, err := parseToolsets([]string{"search", "bogus"}); err == nil || !strings.Contains(err.Error(), `"bogus"`) {
		t.Fatalf("unknown toolset: %v", err)
	}
	if set, err := parseToolsets([]string{" ", ""}); err != nil || set != nil {
		t.Fatal("blank list is all")
	}
	if withKeyToolsets(context.Background(), nil) != context.Background() {
		t.Fatal("nil toolsets add nothing to the context")
	}
}

// keyedHandler runs an MCP JSON-RPC request through the key middleware, as
// the HTTP transport does, and returns the response.
func keyedRPC(t *testing.T, m *MCPAPIKeyMiddleware, h *MCPHandler, key string, body map[string]any) map[string]any {
	t.Helper()
	var resp map[string]any
	wrapped := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp = h.HandleContext(r.Context(), body)
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("X-API-Key", key)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("middleware: %d %s", rec.Code, rec.Body.String())
	}
	return resp
}

func TestToolsets_PerKey(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	t.Setenv("MDDB_MCP_API_KEY_ENABLED", "true")
	t.Setenv("MDDB_MCP_API_KEYS", "static-full:full,static-code:coder:search|code,static-bad:bad:nope")
	m := NewMCPAPIKeyMiddleware()
	store := newMCPAPIKeyStore(s.DB)
	m.SetKeyStore(store)
	_, stored, err := store.Create("stored-code", 0, "code")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Create("x", 0, "bogus"); err == nil {
		t.Fatal("creating a key with an unknown toolset must fail")
	}
	h := NewMCPHandlerWithConfig(NewDirectClient(s), nil, MCPServerInfo{}, "", ModeRW, "")

	list := func(key string) []string {
		resp := keyedRPC(t, m, h, key, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
		result, _ := resp["result"].(map[string]any)
		tools, _ := result["tools"].([]MCPTool)
		return toolNames(tools)
	}
	if n := len(list("static-full")); n != len(mcpBuiltinTools()) {
		t.Fatalf("full key lists %d", n)
	}
	if got := list("static-code"); len(got) != len(mcpToolsets[ToolsetSearch])+len(mcpToolsets[ToolsetCode]) {
		t.Fatalf("search|code key lists %v", got)
	}
	if got := list(stored); len(got) != len(mcpToolsets[ToolsetCode]) || got[0] != "code_graph" {
		t.Fatalf("stored code key lists %v", got)
	}
	if got := list("static-bad"); len(got) != 0 {
		t.Fatalf("a key with unreadable toolsets fails closed, lists %v", got)
	}

	// Calling outside the set is -32601, not a hidden success.
	resp := keyedRPC(t, m, h, stored, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "delete_collection", "arguments": map[string]any{"collection": "x"}}})
	errObj, _ := resp["error"].(map[string]any)
	if errObj == nil || errObj["code"] != mcpErrMethodNotFound {
		t.Fatalf("out-of-set call: %+v", resp)
	}
	// Inside the set the call runs.
	resp = keyedRPC(t, m, h, stored, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "code_graph", "arguments": map[string]any{"collection": "x", "key": "k"}}})
	if resp["error"] != nil {
		t.Fatalf("in-set call refused: %+v", resp)
	}

	// The key list reports toolsets.
	keys, _ := store.List()
	found := false
	for _, k := range keys {
		if k.Name == "stored-code" && len(k.Toolsets) == 1 && k.Toolsets[0] == "code" {
			found = true
		}
	}
	if !found {
		t.Fatalf("list: %+v", keys)
	}
	// Cached lookups keep the toolsets.
	if got := list(stored); len(got) != len(mcpToolsets[ToolsetCode]) {
		t.Fatalf("cached key lists %v", got)
	}
}

func TestToolsets_RESTKeyCreateAndHTTPCall(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.mcpKeyStore = newMCPAPIKeyStore(s.DB)

	rec := doRequest(t, s.handleMCPAPIKeyCreate, map[string]any{"name": "k", "toolsets": []string{"search", "nope"}})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "nope") {
		t.Fatalf("invalid toolset: %d %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, s.handleMCPAPIKeyCreate, map[string]any{"name": "k", "toolsets": []string{"search"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"toolsets":["search"]`) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}

	// The plain HTTP tool endpoint refuses an out-of-set tool with 404.
	t.Setenv("MDDB_MCP_TOOLSETS", "search")
	srv := &MCPHTTPServer{client: NewDirectClient(s), observer: newMCPToolObserver(nil)}
	rec = httptest.NewRecorder()
	srv.handleToolCall(rec, httptest.NewRequest(http.MethodPost, "/tools/call",
		strings.NewReader(`{"name":"delete_collection","arguments":{"collection":"x"}}`)))
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "-32601") {
		t.Fatalf("HTTP out-of-set: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	srv.handleTools(rec, httptest.NewRequest(http.MethodGet, "/tools", nil))
	if strings.Contains(rec.Body.String(), "delete_collection") || !strings.Contains(rec.Body.String(), "hybrid_search") {
		t.Fatalf("HTTP list: %s", rec.Body.String())
	}
}
