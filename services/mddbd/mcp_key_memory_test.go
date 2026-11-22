package main

import (
	"context"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// AGT-008: a limited key's memory namespace, end to end — stored keys, the
// key middleware, the JSON-RPC handler and the memory store.

// memoryRig is a scope server with the key middleware and a handler.
type memoryRig struct {
	t  *testing.T
	s  *Server
	mw *MCPAPIKeyMiddleware
	h  *MCPHandler
}

func newMemoryRig(t *testing.T) *memoryRig {
	t.Helper()
	s := scopeServer(t)
	mw := scopeMiddleware(t, s)
	return &memoryRig{t: t, s: s, mw: mw, h: NewMCPHandlerWithConfig(NewDirectClient(s), nil, MCPServerInfo{}, "", ModeRW, "")}
}

// key creates a stored key and returns the context its requests carry.
func (r *memoryRig) key(spec MCPAPIKey) context.Context {
	r.t.Helper()
	return keyContext(r.t, r.mw, storedKey(r.t, r.s, spec))
}

// call runs a tool and returns its text, failing the test on an error.
func (r *memoryRig) call(ctx context.Context, tool string, args map[string]any) string {
	r.t.Helper()
	text, isErr := callTool(r.h, ctx, tool, args)
	if isErr {
		r.t.Fatalf("%s %v: %s", tool, args, text)
	}
	return text
}

// refused runs a tool that must fail and returns its text.
func (r *memoryRig) refused(ctx context.Context, tool string, args map[string]any) string {
	r.t.Helper()
	text, isErr := callTool(r.h, ctx, tool, args)
	if !isErr {
		r.t.Errorf("%s %v was let through: %s", tool, args, text)
	}
	return text
}

// session starts a session holding one message and returns its ID.
func (r *memoryRig) session(ctx context.Context, user, message string) string {
	r.t.Helper()
	text := r.call(ctx, "memory_start_session", map[string]any{"user_id": user})
	var out map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(text, "Memory session created:\n")), &out); err != nil {
		r.t.Fatal(err)
	}
	id, _ := out["sessionId"].(string)
	r.call(ctx, "memory_add_message", map[string]any{"session_id": id, "role": "user", "content": message})
	return id
}

// sessions lists the session IDs ctx sees.
func (r *memoryRig) sessions(ctx context.Context) map[string]bool {
	r.t.Helper()
	var out struct {
		Sessions []struct {
			SessionID string `json:"sessionId"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(r.call(ctx, "memory_list_sessions", map[string]any{})), &out); err != nil {
		r.t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, s := range out.Sessions {
		ids[s.SessionID] = true
	}
	return ids
}

// recalled lists the session IDs a keyword recall of query finds for ctx.
func (r *memoryRig) recalled(ctx context.Context, args map[string]any) map[string]bool {
	r.t.Helper()
	args["strategy"] = "keyword"
	var out struct {
		Results []struct {
			SessionID string `json:"sessionId"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(r.call(ctx, "memory_recall", args)), &out); err != nil {
		r.t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, res := range out.Results {
		ids[res.SessionID] = true
	}
	return ids
}

func notFound(text string) bool { return strings.Contains(text, errMemorySessionNotFound.Error()) }

func TestKeyMemoryNamespaceIsolation(t *testing.T) {
	r := newMemoryRig(t)
	unscoped := context.Background()
	global := r.session(unscoped, "g", "global zebra secret")
	a := r.key(MCPAPIKey{Name: "a", Collections: []string{"a"}})
	b := r.key(MCPAPIKey{Name: "b", Collections: []string{"b"}})
	sidA := r.session(a, "agent", "alpha zebra note")
	sidB := r.session(b, "agent", "beta zebra note")

	if got := r.sessions(a); !got[sidA] || got[sidB] || got[global] || len(got) != 1 {
		t.Errorf("key A lists %v, want only %s", got, sidA)
	}
	if got := r.sessions(b); !got[sidB] || len(got) != 1 {
		t.Errorf("key B lists %v, want only %s", got, sidB)
	}
	if got := r.recalled(a, map[string]any{"query": "zebra"}); !got[sidA] || len(got) != 1 {
		t.Errorf("key A recalls %v, want only %s", got, sidA)
	}
	for _, sid := range []string{sidB, global} {
		if got := r.recalled(a, map[string]any{"query": "zebra", "session_id": sid}); len(got) != 0 {
			t.Errorf("key A recalled %v from %s", got, sid)
		}
		for tool, args := range map[string]map[string]any{
			"memory_session_history": {"session_id": sid},
			"memory_add_message":     {"session_id": sid, "role": "user", "content": "planted"},
			"memory_summarize":       {"session_id": sid},
		} {
			if text := r.refused(a, tool, args); !notFound(text) {
				t.Errorf("key A %s on %s: %s", tool, sid, text)
			}
		}
	}
	if text := r.refused(a, "memory_session_history", map[string]any{"session_id": "no-such-session"}); !notFound(text) {
		t.Errorf("a missing session answers differently: %s", text)
	}
	if text := r.call(a, "memory_session_history", map[string]any{"session_id": sidA}); !strings.Contains(text, "alpha zebra note") {
		t.Errorf("key A history of its own session: %s", text)
	}
	if text := r.call(a, "memory_summarize", map[string]any{"session_id": sidA}); !strings.Contains(text, "Session summarized") {
		t.Errorf("key A summary of its own session: %s", text)
	}
	if text := r.call(unscoped, "memory_session_history", map[string]any{"session_id": sidB}); strings.Contains(text, "planted") {
		t.Errorf("key A wrote into B's session: %s", text)
	}

	// The caller's meta cannot move a session into another namespace.
	bNamespace := keyMemoryNamespace("", "x") // any value other than A's own
	planted := r.call(a, "memory_start_session", map[string]any{"user_id": "agent", "meta": map[string]any{memoryNamespaceMeta: bNamespace}})
	if got := r.sessions(b); len(got) != 1 {
		t.Errorf("a session planted through meta reached B: %v (%s)", got, planted)
	}

	// An unscoped key, and main auth, see every session as before.
	if got := r.sessions(unscoped); !got[global] || !got[sidA] || !got[sidB] {
		t.Errorf("unscoped key lists %v", got)
	}
	claims := context.WithValue(a, authContextKey, root)
	if got := r.sessions(claims); !got[global] || !got[sidB] {
		t.Errorf("main auth over a key lists %v", got)
	}
}

func TestKeyMemorySharedReadOnlyAndDeletedKeys(t *testing.T) {
	r := newMemoryRig(t)
	one := r.key(MCPAPIKey{Name: "one", Collections: []string{"a"}, MemoryNamespace: "shop"})
	two := r.key(MCPAPIKey{Name: "two", Collections: []string{"b"}, MemoryNamespace: "shop"})
	other := r.key(MCPAPIKey{Name: "other", Collections: []string{"a"}, MemoryNamespace: "blog"})
	sid := r.session(one, "agent", "handoff: tests left")
	if got := r.sessions(two); !got[sid] {
		t.Errorf("a key of the same namespace does not see the session: %v", got)
	}
	r.call(two, "memory_add_message", map[string]any{"session_id": sid, "role": "assistant", "content": "picked up"})
	if got := r.sessions(other); len(got) != 0 {
		t.Errorf("another namespace sees %v", got)
	}

	// A read-only key recalls, lists and reads history, and writes nothing.
	ro := r.key(MCPAPIKey{Name: "ro", Collections: []string{"a"}, ReadOnly: true, MemoryNamespace: "shop"})
	if got := r.sessions(ro); !got[sid] {
		t.Errorf("read-only key lists %v", got)
	}
	r.call(ro, "memory_session_history", map[string]any{"session_id": sid})
	r.recalled(ro, map[string]any{"query": "handoff"})
	for tool, args := range map[string]map[string]any{
		"memory_start_session": {"user_id": "agent"},
		"memory_add_message":   {"session_id": sid, "role": "user", "content": "x"},
		"memory_summarize":     {"session_id": sid},
	} {
		if text := r.refused(ro, tool, args); !forbidden(text, tool) {
			t.Errorf("read-only key %s: %s", tool, text)
		}
	}

	// Deleting a key leaves its memory; a new key with the same spec gets a
	// namespace of its own.
	spec := MCPAPIKey{Name: "own", Collections: []string{"a"}}
	key := storedKey(t, r.s, spec)
	own := r.session(keyContext(t, r.mw, key), "agent", "kept after delete")
	if err := r.s.mcpKeyStore.Delete(key); err != nil {
		t.Fatal(err)
	}
	r.mw.InvalidateCache()
	if got := r.sessions(r.key(spec)); got[own] {
		t.Error("a new key inherited a deleted key's namespace")
	}
	if got := r.sessions(context.Background()); !got[own] {
		t.Error("deleting the key deleted its memory")
	}
}

func TestKeyMemoryRefusedWithoutLimit(t *testing.T) {
	s := scopeServer(t)
	rule := mcpToolAccessRules["memory_list_sessions"]
	for name, k := range map[string]mcpKeyScope{
		"read-only, every collection": {readOnly: true, memoryNamespace: "key:x"},
		"limited, no namespace":       {collections: []string{"a"}},
	} {
		if err := s.authorizeMCP(scoped(k), "memory_list_sessions", rule, nil); err == nil {
			t.Errorf("%s: memory let through", name)
		}
	}
}

func TestKeyMemoryNamespaceDerivation(t *testing.T) {
	if keyMemoryNamespace("mcp_a", "") == keyMemoryNamespace("mcp_b", "") {
		t.Error("two keys share a derived namespace")
	}
	if got := keyMemoryNamespace("mcp_a", "shop"); got != "ns:shop" {
		t.Errorf("explicit namespace = %q", got)
	}
	derived := keyMemoryNamespace("mcp_a", "")
	if !strings.HasPrefix(derived, memoryNamespaceKeyPrefix) || strings.Contains(derived, "mcp_a") {
		t.Errorf("derived namespace = %q", derived)
	}
	_, g := parseStaticMCPKey("sk1:name::a")
	if g.scope.memoryNamespace != keyMemoryNamespace("sk1", "") {
		t.Errorf("static key namespace = %q", g.scope.memoryNamespace)
	}
	if _, ok := memoryNamespaceFromContext(scoped(mcpKeyScope{readOnly: true, memoryNamespace: derived})); ok {
		t.Error("a key not limited to collections got a namespace")
	}
}
