package main

import (
	"context"
	"errors"
	"net/url"
	"testing"
)

// #439: the verdicts of a scoped MCP API key, per rule class, for every
// built-in tool — so a tool added later cannot be open to scoped keys by
// accident.

// scopeServer is a server without auth holding collections "a" and "b", an
// alias "al" → "a", the scenario store and the collection configs.
func scopeServer(t *testing.T) *Server {
	t.Helper()
	s, cleanup := newTestServerWithFTS(t)
	t.Cleanup(cleanup)
	s.Aliases = NewAliasManager(s.DB)
	s.ChatScenarios = NewChatScenarioManager(s.DB)
	s.CollectionManager = NewCollectionManager(s.DB)
	for _, err := range []error{s.Aliases.LoadAll(), s.ChatScenarios.LoadAll(), s.CollectionManager.EnsureBucket()} {
		if err != nil {
			t.Fatal(err)
		}
	}
	addTestDoc(t, s, "a", "doc-a", "en", "alpha scoped content", nil)
	addTestDoc(t, s, "b", "doc-b", "en", "alpha other content", nil)
	if err := s.Aliases.set("al", "a", s.collectionExists); err != nil {
		t.Fatal(err)
	}
	return s
}

// scoped is ctx as the key middleware leaves it for a key with scope k.
func scoped(k mcpKeyScope) context.Context { return withKeyScope(context.Background(), k) }

// expectScoped is what a read-only key scoped to "a" may do with a tool
// whose rule is rule, on a call naming "a": written out per class rather
// than derived from keyScopeMayCall.
func expectScoped(tool string, rule accessRule) bool {
	if tool == "sync_refresh" {
		// The one write rule a read-only key passes: it refreshes a sync
		// rule's mirror and writes nothing of the caller's (INT-023).
		return true
	}
	switch {
	case rule.class == accessAuthenticated, rule.class == accessPublic:
		return toolIsReadOnly(tool)
	case rule.class != accessCollection, len(rule.fixed) > 0, rule.all, rule.op != PermRead:
		return false
	}
	return toolIsReadOnly(tool)
}

func TestEveryMCPToolHasAScopedKeyVerdict(t *testing.T) {
	s := scopeServer(t)
	ro := scoped(mcpKeyScope{collections: []string{"a"}, readOnly: true})
	rw := scoped(mcpKeyScope{collections: []string{"a"}})
	for _, tool := range mcpBuiltinTools() {
		rule := mcpToolAccessRules[tool.Name]
		want := expectScoped(tool.Name, rule)
		if got := s.authorizeMCP(ro, tool.Name, rule, []string{"a"}) == nil; got != want {
			t.Errorf("%s (class %d): read-only key on a allowed=%v, want %v", tool.Name, rule.class, got, want)
		}
		if got := keyScopeMayList(ro, tool.Name); got != want {
			t.Errorf("%s: tools/list shows it=%v, want %v", tool.Name, got, want)
		}
		if rule.class == accessCollection && s.authorizeMCP(ro, tool.Name, rule, []string{"b"}) == nil {
			t.Errorf("%s: a key scoped to a reached b", tool.Name)
		}
		if rule.class == accessCollection && len(rule.fallback) == 0 && s.authorizeMCP(ro, tool.Name, rule, nil) == nil {
			t.Errorf("%s: a call naming no collection was let through", tool.Name)
		}
		// Without readOnly, a write of a's own collection is allowed.
		wantRW := rule.class == accessCollection && len(rule.fixed) == 0 && !rule.all
		if rule.class == accessAuthenticated || rule.class == accessPublic {
			wantRW = true
		}
		if got := s.authorizeMCP(rw, tool.Name, rule, []string{"a"}) == nil; got != wantRW {
			t.Errorf("%s: read-write key on a allowed=%v, want %v", tool.Name, got, wantRW)
		}
	}
}

func TestKeyScopeNoScopeAndClaims(t *testing.T) {
	s := scopeServer(t)
	if err := s.authorizeMCP(context.Background(), "add_document", ruleWrite, []string{"b"}); err != nil {
		t.Errorf("stdio / unscoped key changed: %v", err)
	}
	if ctx := withKeyScope(context.Background(), mcpKeyScope{}); ctx.Value(mcpKeyScopeContextKey{}) != nil {
		t.Error("an unscoped key left a scope in the context")
	}
	// Main-auth claims are RBAC's to judge, not the key scope's.
	ctx := context.WithValue(scoped(mcpKeyScope{collections: []string{"a"}}), authContextKey, root)
	if _, ok := keyScopeFromContext(ctx); ok {
		t.Error("a scope applied over main-auth claims")
	}
	if !keyScopeMayList(context.Background(), "delete_collection") {
		t.Error("unscoped tools/list lost a tool")
	}
}

func TestKeyScopeReadOnlyOnly(t *testing.T) {
	s := scopeServer(t)
	ctx := scoped(mcpKeyScope{readOnly: true})
	if err := s.authorizeMCP(ctx, "get_stats", ruleAllRead, nil); err != nil {
		t.Errorf("a read-only key unlimited by collection reads every collection: %v", err)
	}
	if err := s.authorizeMCP(ctx, "full_text_search", ruleRead, []string{"b"}); err != nil {
		t.Errorf("read-only key on b: %v", err)
	}
	for tool, rule := range map[string]accessRule{"add_document": ruleWrite, "bulk_ingest_cancel": ruleAllWrite,
		"create_backup": ruleAdmin, "memory_recall": mcpToolAccessRules["memory_recall"], "wordpress_set_status": ruleAuthenticated} {
		if err := s.authorizeMCP(ctx, tool, rule, []string{"b"}); !errors.Is(err, ErrForbidden) {
			t.Errorf("read-only key ran %s: %v", tool, err)
		}
	}
}

func TestKeyScopeAliasesAndRoles(t *testing.T) {
	s := scopeServer(t)
	byAlias := scoped(mcpKeyScope{collections: []string{"al"}})
	for _, name := range []string{"a", "al"} {
		if err := s.authorizeMCP(byAlias, "full_text_search", ruleRead, []string{name}); err != nil {
			t.Errorf("alias scope, call on %s: %v", name, err)
		}
	}
	if err := s.authorizeMCP(byAlias, "full_text_search", ruleRead, []string{"b"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("alias scope reached b: %v", err)
	}
	role := scoped(mcpKeyScope{collections: []string{rolePrefix + CodeRoleReference}})
	if err := s.authorizeTool(role, "reference_search", map[string]any{}); !errors.Is(err, ErrForbidden) {
		t.Errorf("no reference collection yet: the fallback is every collection, refused: %v", err)
	}
	if err := s.CollectionManager.Set("refs", &CollectionConfig{Code: &CodeConfigDef{Role: CodeRoleReference}}); err != nil {
		t.Fatal(err)
	}
	if err := s.authorizeTool(role, "reference_search", map[string]any{}); err != nil {
		t.Errorf("role:reference scope, the reference fallback: %v", err)
	}
	if err := s.authorizeTool(role, "cross_search", map[string]any{"collections": []any{"refs", "a"}}); !errors.Is(err, ErrForbidden) {
		t.Errorf("one collection outside among several: %v", err)
	}
	if err := s.authorizeTool(scoped(mcpKeyScope{collections: []string{"a"}}), "reference_search", map[string]any{}); !errors.Is(err, ErrForbidden) {
		t.Errorf("key on a used the reference fallback: %v", err)
	}
}

func TestKeyScopeResources(t *testing.T) {
	s := scopeServer(t)
	ctx := scoped(mcpKeyScope{collections: []string{"a"}, readOnly: true})
	for raw, allowed := range map[string]bool{"mddb://health": true, "mddb://stats": false,
		"mddb://a/doc-a": true, "mddb://al/doc-a": true, "mddb://b/doc-b": false, "mddb-search://a?q=x": true, "mddb-search://b?q=x": false} {
		u, _ := url.Parse(raw)
		if got := s.authorizeResource(ctx, u) == nil; got != allowed {
			t.Errorf("%s allowed=%v, want %v", raw, got, allowed)
		}
	}
}

func TestKeyScopeReachIntersection(t *testing.T) {
	s := scopeServer(t)
	putScenario(t, s, &ChatScenario{Name: "s", Enabled: true, AllowedCollections: []string{"a", "b"}})
	both := scoped(mcpKeyScope{scenario: "s", collections: []string{"b"}})
	if err := s.authorizeMCP(both, "full_text_search", ruleRead, []string{"a"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("intersection let a through: %v", err)
	}
	if err := s.authorizeMCP(both, "full_text_search", ruleRead, []string{"b"}); err != nil {
		t.Errorf("intersection refused b: %v", err)
	}
}

// putScenario stores a scenario as the REST handler would.
func putScenario(t *testing.T, s *Server, sc *ChatScenario) {
	t.Helper()
	if _, err := s.PutChatScenario(context.Background(), sc); err != nil {
		t.Fatal(err)
	}
}
