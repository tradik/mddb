package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// #439 end to end: a key's scope from the middleware to the tool, over the
// JSON-RPC handler, the /tools/call shim, custom tools, prompts, completion,
// initialize, the event stream and OAuth.

// keyContext is the context the key middleware gives a request with key.
func keyContext(t *testing.T, mw *MCPAPIKeyMiddleware, key string) context.Context {
	t.Helper()
	var got context.Context
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("X-API-Key", key)
	mw.Wrap(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Context() })).ServeHTTP(rec, r)
	if got == nil {
		t.Fatalf("key %q refused: %d", key, rec.Code)
	}
	return got
}

// storedKey creates a key in the server's store and returns it.
func storedKey(t *testing.T, s *Server, spec MCPAPIKey) string {
	t.Helper()
	_, key, err := s.createMCPAPIKey(spec)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// scopeMiddleware is the key middleware over the server's key store.
func scopeMiddleware(t *testing.T, s *Server) *MCPAPIKeyMiddleware {
	t.Helper()
	s.mcpKeyStore = newMCPAPIKeyStore(s.DB)
	mw := &MCPAPIKeyMiddleware{enabled: true, keys: map[string]string{}, keyStore: s.mcpKeyStore, cache: map[string]*apiKeyCacheEntry{}}
	s.mcpAuth = mw
	return mw
}

// callTool sends tools/call through the JSON-RPC handler and returns the
// text and whether it is an error.
func callTool(h *MCPHandler, ctx context.Context, name string, args map[string]any) (string, bool) {
	resp := h.HandleContext(ctx, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args}})
	res, _ := resp["result"].(map[string]any)
	content, _ := res["content"].([]map[string]any)
	if len(content) == 0 {
		return "", true
	}
	text, _ := content[0]["text"].(string)
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

func forbidden(text, tool string) bool { return strings.Contains(text, `tool "`+tool+`": forbidden`) }

func TestScopedKeyAcceptance(t *testing.T) {
	s := scopeServer(t)
	mw := scopeMiddleware(t, s)
	ctx := keyContext(t, mw, storedKey(t, s, MCPAPIKey{Name: "one", Collections: []string{"a"}, ReadOnly: true}))
	h := NewMCPHandlerWithConfig(NewDirectClient(s), nil, MCPServerInfo{}, "", ModeRW, "")

	if text, isErr := callTool(h, ctx, "full_text_search", map[string]any{"collection": "a", "query": "alpha"}); isErr || !strings.Contains(text, "doc-a") {
		t.Errorf("full_text_search on a: %v %s", isErr, text)
	}
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"full_text_search", map[string]any{"collection": "b", "query": "alpha"}},
		{"add_document", map[string]any{"collection": "a", "key": "x", "lang": "en", "content": "y"}},
		{"get_stats", map[string]any{}},
		{"search_documents", map[string]any{}},
	} {
		if text, _ := callTool(h, ctx, c.tool, c.args); !forbidden(text, c.tool) {
			t.Errorf("%s %v: %s", c.tool, c.args, text)
		}
	}

	// tools/list leaves out what the key can never call.
	listed := map[string]bool{}
	for _, tool := range h.handleToolsList(ctx, map[string]any{})["tools"].([]MCPTool) {
		listed[tool.Name] = true
	}
	if !listed["full_text_search"] || listed["add_document"] || listed["get_stats"] || listed["create_backup"] {
		t.Errorf("tools/list for a read-only key on a: %v", listed)
	}

	// The /tools/call shim goes through the same gate.
	m := s.newMCPHTTPServer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/tools/call", strings.NewReader(`{"name":"full_text_search","arguments":{"collection":"b","query":"alpha"}}`)).WithContext(ctx)
	m.handleToolCall(rec, req)
	if !forbidden(rec.Body.String(), "full_text_search") {
		t.Errorf("/tools/call on b: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	m.handleTools(rec, httptest.NewRequest(http.MethodGet, "/tools", nil).WithContext(ctx))
	if strings.Contains(rec.Body.String(), `"add_document"`) {
		t.Error("/tools lists a write tool to a read-only key")
	}
}

func TestScopedKeyCannotEscapeThroughCustomTool(t *testing.T) {
	s := scopeServer(t)
	ctx := scoped(mcpKeyScope{collections: []string{"a"}})
	custom := []MCPCustomToolConfig{
		{Name: "pinned_b", Action: "full_text_search", Defaults: MCPCustomToolDefs{Collection: "b"}},
		{Name: "any_coll", Action: "full_text_search", Parameters: []MCPCustomToolParam{{Name: "collection", Type: "string"}, {Name: "query", Type: "string"}}},
	}
	h := NewMCPHandlerWithConfig(NewDirectClient(s), custom, MCPServerInfo{}, "", ModeRW, "")
	if text, _ := callTool(h, ctx, "pinned_b", map[string]any{"query": "alpha"}); !forbidden(text, "full_text_search") {
		t.Errorf("custom tool pinned to b: %s", text)
	}
	if text, _ := callTool(h, ctx, "any_coll", map[string]any{"collection": "b", "query": "alpha"}); !forbidden(text, "full_text_search") {
		t.Errorf("custom tool given b: %s", text)
	}
	if text, isErr := callTool(h, ctx, "any_coll", map[string]any{"collection": "a", "query": "alpha"}); isErr || !strings.Contains(text, "doc-a") {
		t.Errorf("custom tool on a: %s", text)
	}
	// A custom tool is listed by the action it wraps.
	ro := scoped(mcpKeyScope{collections: []string{"a"}, readOnly: true})
	if names := featureToolNames(mcpToolsFor(ro, []MCPCustomToolConfig{{Name: "w", Action: "add_document"}, custom[1]})); names["w"] || !names["any_coll"] {
		t.Errorf("custom tools listed: %v", names)
	}
}

func TestScopedKeyPromptsCompletionAndScenarioList(t *testing.T) {
	s := scopeServer(t)
	for coll, prompt := range map[string]string{"a": "Answer from a.", "b": "Answer from b."} {
		if err := s.CollectionManager.Set(coll, &CollectionConfig{ResponsePrompt: prompt}); err != nil {
			t.Fatal(err)
		}
	}
	putScenario(t, s, &ChatScenario{Name: "sa", Enabled: true, AllowedCollections: []string{"a"}})
	putScenario(t, s, &ChatScenario{Name: "sb", Enabled: true, AllowedCollections: []string{"b"}})
	putScenario(t, s, &ChatScenario{Name: "none", Enabled: true})
	ctx := scoped(mcpKeyScope{collections: []string{"a"}})
	client := NewDirectClient(s)

	values, _, _ := mcpComplete(ctx, client, map[string]any{"type": "ref/resource"}, map[string]any{"name": "collection"})
	if strings.Join(values, ",") != "a" {
		t.Errorf("completion offered %v", values)
	}
	if all, _, _ := mcpComplete(context.Background(), client, map[string]any{}, map[string]any{"name": "collection"}); len(all) != 2 {
		t.Errorf("unscoped completion changed: %v", all)
	}
	if p := collectionResponsePrompt(ctx, client, "b"); p != "" {
		t.Errorf("rag-pipeline read b's response prompt: %q", p)
	}
	if p := collectionResponsePrompt(ctx, client, "a"); p != "Answer from a." {
		t.Errorf("rag-pipeline on a: %q", p)
	}
	var names []string
	for _, sc := range s.ListChatScenarios(ctx) {
		names = append(names, sc.Name)
	}
	if strings.Join(names, ",") != "sa" {
		t.Errorf("chat_scenario_list for a key on a: %v", names)
	}
}

func TestScopedKeyEventStream(t *testing.T) {
	s := scopeServer(t)
	s.SSEHub = NewSSEHub(true, 10, 10)
	cases := map[string]int{"/events": http.StatusForbidden, "/events?collection=b": http.StatusForbidden, "/events?collection=a": http.StatusOK}
	for target, want := range cases {
		ctx, cancel := context.WithCancel(scoped(mcpKeyScope{collections: []string{"a"}}))
		cancel() // the stream ends as soon as it starts
		rec := httptest.NewRecorder()
		s.handleSSE(rec, httptest.NewRequest(http.MethodGet, target, nil).WithContext(ctx))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", target, rec.Code, want)
		}
	}
}

func TestScopedKeyStoreAndHandlers(t *testing.T) {
	s := scopeServer(t)
	mw := scopeMiddleware(t, s)
	putScenario(t, s, &ChatScenario{Name: "support", Enabled: true, AllowedCollections: []string{"a"}})
	post := func(body string) *httptest.ResponseRecorder {
		return covDKeyRequest(s, s.handleMCPAPIKeys, http.MethodPost, body, context.Background())
	}
	rec := post(`{"name":"bot","collections":["a","role:reference"],"readOnly":true,"scenario":"support","key":"mcp_chosen","disabled":true}`)
	var created MCPAPIKey
	if err := json.Unmarshal(rec.Body.Bytes(), &created); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if created.Key == "mcp_chosen" || !created.ReadOnly || created.Scenario != "support" || len(created.Collections) != 2 {
		t.Errorf("created = %+v", created)
	}
	if g, ok := mw.validateKey(created.Key); !ok || g.scope.scenario != "support" || !g.scope.readOnly || g.scope.collections[1] != "role:reference" {
		t.Errorf("a client-chosen key or disabled flag was taken, or the scope lost: %+v %v", g, ok)
	}
	list := covDKeyRequest(s, s.handleMCPAPIKeys, http.MethodGet, "", context.Background()).Body.String()
	if !strings.Contains(list, `"collections":["a","role:reference"]`) || !strings.Contains(list, `"readOnly":true`) || !strings.Contains(list, `"scenario":"support"`) {
		t.Errorf("list lost the scope: %s", list)
	}
	for _, body := range []string{`{"name":"x","scenario":"nope"}`, `{"name":"x","collections":[" "]}`} {
		if rec := post(body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	s.ChatScenarios = nil
	if rec := post(`{"name":"x","scenario":"support"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("scenario without a store: %d", rec.Code)
	}
}

func TestPreScopeStoredKeyIsUnscoped(t *testing.T) {
	s := scopeServer(t)
	mw := scopeMiddleware(t, s)
	// A record exactly as 2.19.8 wrote it.
	raw := []byte(`{"key":"mcp_old","name":"old","createdAt":1700000000,"toolsets":["search"]}`)
	if err := s.mcpKeyStore.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte(mcpAPIKeyBucket)).Put([]byte("mcp_old"), raw) }); err != nil {
		t.Fatal(err)
	}
	g, ok := mw.validateKey("mcp_old")
	if !ok || g.name != "old" || !g.toolsets[ToolsetSearch] || g.scope.scoped() {
		t.Errorf("pre-2.19.9 key = %+v %v", g, ok)
	}
	if ctx := keyContext(t, mw, "mcp_old"); ctx.Value(mcpKeyScopeContextKey{}) != nil {
		t.Error("an old key carries a scope")
	}
}
