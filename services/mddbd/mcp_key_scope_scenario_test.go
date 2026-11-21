package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// #439: a key bound to a chat scenario, static keys with a scope, and OAuth
// tokens minted from a scoped key.

func TestScenarioBoundKey(t *testing.T) {
	s := scopeServer(t)
	s.Features = NewFeatureManager(s.DB)
	if err := s.Features.LoadAll(); err != nil {
		t.Fatal(err)
	}
	sc := &ChatScenario{Name: "support", Enabled: true, OperatorPrompt: "You are Ada.", Guardrails: "No legal advice.",
		AllowedCollections: []string{"a"}}
	putScenario(t, s, sc)
	mw := scopeMiddleware(t, s)
	ctx := keyContext(t, mw, storedKey(t, s, MCPAPIKey{Name: "widget", Scenario: "support"}))
	h := NewMCPHandlerWithConfig(NewDirectClient(s), nil, MCPServerInfo{}, "server default", ModeRW, "")
	instructions := func() any {
		return h.HandleContext(ctx, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize",
			"params": map[string]any{}})["result"].(map[string]any)["instructions"]
	}

	want := s.ChatScenarioPrompt(sc).Prompt
	if got := instructions(); got != want || !strings.Contains(want, "No legal advice.") {
		t.Errorf("initialize instructions = %q, want the composed prompt %q", got, want)
	}
	if got := h.handleDiscover(ctx)["instructions"]; got != want {
		t.Errorf("server/discover instructions = %q", got)
	}
	if text, isErr := callTool(h, ctx, "full_text_search", map[string]any{"collection": "a", "query": "alpha"}); isErr || !strings.Contains(text, "doc-a") {
		t.Errorf("bound key on a: %s", text)
	}
	for tool, args := range map[string]map[string]any{
		"full_text_search": {"collection": "b", "query": "alpha"},
		"add_document":     {"collection": "a", "key": "x", "lang": "en", "content": "y"}, // always read-only
		"get_stats":        {},
	} {
		if text, _ := callTool(h, ctx, tool, args); !forbidden(text, tool) {
			t.Errorf("bound key ran %s: %s", tool, text)
		}
	}

	// Editing the scenario changes the key's reach: nothing was copied.
	sc.AllowedCollections = []string{"b"}
	putScenario(t, s, sc)
	if text, _ := callTool(h, ctx, "full_text_search", map[string]any{"collection": "a", "query": "alpha"}); !forbidden(text, "full_text_search") {
		t.Errorf("after the edit, a: %s", text)
	}
	if _, isErr := callTool(h, ctx, "full_text_search", map[string]any{"collection": "b", "query": "alpha"}); isErr {
		t.Error("after the edit, b refused")
	}

	// An empty allowedCollections reaches no collection: mddbd does not know
	// the default collection mddb-chat falls back to.
	sc.AllowedCollections = nil
	putScenario(t, s, sc)
	if text, _ := callTool(h, ctx, "full_text_search", map[string]any{"collection": "b", "query": "alpha"}); !forbidden(text, "full_text_search") {
		t.Errorf("empty allowedCollections reached b: %s", text)
	}
	if _, isErr := callTool(h, ctx, "fts_languages", map[string]any{}); isErr {
		t.Error("a tool without collection data was refused while the scenario is served")
	}

	// Disabled, chat off, deleted: every call is refused and the prompt is
	// not shown.
	refusedAll := func(state string) {
		t.Helper()
		if text, _ := callTool(h, ctx, "fts_languages", map[string]any{}); !forbidden(text, "fts_languages") {
			t.Errorf("%s: %s", state, text)
		}
		if got := instructions(); got != "server default" {
			t.Errorf("%s: initialize shows %q", state, got)
		}
	}
	sc.Enabled = false
	putScenario(t, s, sc)
	refusedAll("disabled")
	sc.Enabled = true
	putScenario(t, s, sc)
	if err := s.Features.Set(FeatureChat, false, "test"); err != nil {
		t.Fatal(err)
	}
	refusedAll("chat off")
	if err := s.Features.Set(FeatureChat, true, "test"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteChatScenario("support"); err != nil {
		t.Fatal(err)
	}
	refusedAll("deleted")
	s.ChatScenarios = nil
	refusedAll("no store")
}

func TestUnboundKeyKeepsServerInstructions(t *testing.T) {
	s := scopeServer(t)
	h := NewMCPHandlerWithConfig(NewDirectClient(s), nil, MCPServerInfo{}, "server default", ModeRW, "")
	for _, ctx := range []context.Context{context.Background(), scoped(mcpKeyScope{collections: []string{"a"}})} {
		if got := h.handleDiscover(ctx)["instructions"]; got != "server default" {
			t.Errorf("instructions = %q", got)
		}
	}
	other := NewMCPHandlerWithConfig(covBNewAllErr(), nil, MCPServerInfo{}, "", ModeRW, "")
	if _, ok := other.handleDiscover(scoped(mcpKeyScope{scenario: "x"}))["instructions"]; ok {
		t.Error("a handler without an in-process server invented instructions")
	}
}

func TestStaticKeyFormats(t *testing.T) {
	cases := []struct {
		entry, key, name string
		toolsets         int // -1 = all
		collections      string
		readOnly         bool
	}{
		{"plainkey", "plainkey", "plainkey", -1, "", false},
		{"averylongkey", "averylongkey", "averylon...", -1, "", false},
		{"k:bot", "k", "bot", -1, "", false},
		{"k:bot:search|code", "k", "bot", 2, "", false},
		{"k:bot:", "k", "bot", -1, "", false},
		{"k:bot:bogus", "k", "bot", 0, "", false},
		{"k:bot::a|b", "k", "bot", -1, "a,b", false},
		{"k:bot:search:a|role:reference:ro", "k", "bot", 1, "a,role:reference", true},
		{"k:bot:search::ro", "k", "bot", 1, "", true},
		{"k:bot::ro", "k", "bot", -1, "ro", false},
	}
	for _, c := range cases {
		key, g := parseStaticMCPKey(c.entry)
		toolsets := len(g.toolsets)
		if g.toolsets == nil {
			toolsets = -1
		}
		if key != c.key || g.name != c.name || toolsets != c.toolsets || strings.Join(g.scope.collections, ",") != c.collections || g.scope.readOnly != c.readOnly {
			t.Errorf("%q = %q %+v", c.entry, key, g)
		}
	}

	t.Setenv("MDDB_MCP_API_KEY_ENABLED", "true")
	t.Setenv("MDDB_MCP_API_KEYS", "old:legacy:search, scoped:one::a:ro")
	mw := NewMCPAPIKeyMiddleware()
	if g, ok := mw.validateKey("old"); !ok || g.scope.scoped() || !g.toolsets[ToolsetSearch] {
		t.Errorf("old static key = %+v", g)
	}
	if _, scopedOld := mw.keyScopes["old"]; scopedOld {
		t.Error("an old static key got a scope")
	}
	if g, ok := mw.validateKey("scoped"); !ok || !g.scope.readOnly || g.scope.collections[0] != "a" {
		t.Errorf("scoped static key = %+v", g)
	}
}

func TestOAuthTokenInheritsKeyScope(t *testing.T) {
	s := scopeServer(t)
	mw := scopeMiddleware(t, s)
	key := storedKey(t, s, MCPAPIKey{Name: "claude", Collections: []string{"a"}, ReadOnly: true})
	o, err := newMCPOAuth("https://mcp.example.com", strings.Repeat("s", 32), mw)
	if err != nil {
		t.Fatal(err)
	}
	var seen mcpKeyScope
	h := o.Wrap(mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = keyScopeFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})))
	redirect := "http://localhost:1/cb"
	clientID := registerClient(t, h, redirect)
	verifier, challenge := pkce()
	code := authorize(t, h, authorizeValues(clientID, redirect, challenge), key)
	access, _ := tokens(t, postForm(h, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"client_id": {clientID}, "redirect_uri": {redirect}, "code_verifier": {verifier}}))

	call := func() int {
		r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		r.Header.Set("Authorization", "Bearer "+access)
		return serve(h, r).Code
	}
	if c := call(); c != http.StatusOK || !seen.readOnly || seen.collections[0] != "a" {
		t.Fatalf("token: %d, scope %+v", c, seen)
	}
	// The token resolves its key on every request: disabling the key stops it.
	if err := s.mcpKeyStore.Disable(key); err != nil {
		t.Fatal(err)
	}
	mw.InvalidateCache()
	if c := call(); c != http.StatusUnauthorized {
		t.Errorf("token of a disabled key: %d", c)
	}
}
