package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// AGT-007: readOnlyCollections on a key — what it may write, and the checks
// a new key passes.

func TestKeyReadOnlyCollectionsWrites(t *testing.T) {
	s := scopeServer(t)
	if err := s.Aliases.set("bl", "b", s.collectionExists); err != nil {
		t.Fatal(err)
	}
	mw := scopeMiddleware(t, s)
	ctx := keyContext(t, mw, storedKey(t, s, MCPAPIKey{Name: "p", Collections: []string{"al", "b"}, ReadOnlyCollections: []string{"bl"}}))
	h := NewMCPHandlerWithConfig(NewDirectClient(s), nil, MCPServerInfo{}, "", ModeRW, "")

	// Every write tool, by name or alias, alone or among others, is refused.
	writes := []struct {
		tool string
		args map[string]any
	}{
		{"add_document", map[string]any{"collection": "b", "key": "x", "lang": "en", "content_md": "y"}},
		{"add_document", map[string]any{"collection": "bl", "key": "x", "lang": "en", "content_md": "y"}},
		{"add_documents_batch", map[string]any{"collection": "b", "documents": []any{}}},
		{"delete_document", map[string]any{"collection": "b", "key": "doc-b", "lang": "en"}},
		{"delete_documents_batch", map[string]any{"collection": "b", "documents": []any{}}},
		{"ingest_documents", map[string]any{"collection": "b", "documents": []any{}}},
		{"update_where", map[string]any{"collection": "b"}},
		{"delete_collection", map[string]any{"collection": "b"}},
		{"set_schema", map[string]any{"collection": "b"}},
		{"wordpress_publish", map[string]any{"collection": "b", "key": "doc-b"}},
		{"add_document", map[string]any{"collection": "a", "target_collections": []any{"b"}, "key": "x", "lang": "en", "content_md": "y"}},
	}
	for _, w := range writes {
		text, _ := callTool(h, ctx, w.tool, w.args)
		if !strings.Contains(text, "forbidden") {
			t.Errorf("%s %v on a read-only collection: %s", w.tool, w.args, text)
		}
	}
	if text, isErr := callTool(h, ctx, "full_text_search", map[string]any{"collection": "bl", "query": "alpha"}); isErr || !strings.Contains(text, "doc-b") {
		t.Errorf("read of a read-only collection: %s", text)
	}
	for _, c := range []string{"a", "al"} {
		if text, isErr := callTool(h, ctx, "add_document", map[string]any{"collection": c, "key": "new", "lang": "en", "content_md": "written"}); isErr {
			t.Errorf("write to %s refused: %s", c, text)
		}
	}
	listed := featureToolNames(mcpToolsFor(ctx, nil))
	if !listed["add_document"] || !listed["memory_add_message"] || listed["create_backup"] {
		t.Errorf("tools/list for a key writing one of two collections: %v", listed)
	}
}

func TestKeyScopeSpecValidation(t *testing.T) {
	s := scopeServer(t)
	s.mcpKeyStore = newMCPAPIKeyStore(s.DB)
	long := strings.Repeat("n", maxMemoryNamespaceLen+1)
	for name, c := range map[string]struct {
		spec MCPAPIKey
		want error
		text string
	}{
		"read-only collections without collections": {MCPAPIKey{Name: "k", ReadOnlyCollections: []string{"a"}}, errReadOnlyCollectionsNeedCollections, ""},
		"read-only collection outside":              {MCPAPIKey{Name: "k", Collections: []string{"a"}, ReadOnlyCollections: []string{"b"}}, nil, `"b" is not among collections`},
		"read-only role resolving to nothing":       {MCPAPIKey{Name: "k", Collections: []string{"a"}, ReadOnlyCollections: []string{"role:reference"}}, nil, "is not among collections"},
		"namespace on a key not limited":            {MCPAPIKey{Name: "k", ReadOnly: true, MemoryNamespace: "shop"}, errMemoryNamespaceNeedsLimit, ""},
		"namespace with spaces":                     {MCPAPIKey{Name: "k", Collections: []string{"a"}, MemoryNamespace: " shop"}, errMemoryNamespaceInvalid, ""},
		"namespace too long":                        {MCPAPIKey{Name: "k", Collections: []string{"a"}, MemoryNamespace: long}, errMemoryNamespaceInvalid, ""},
	} {
		_, _, err := s.createMCPAPIKey(c.spec)
		if err == nil || (c.want != nil && !errors.Is(err, c.want)) || !strings.Contains(err.Error(), c.text) {
			t.Errorf("%s: err = %v", name, err)
		}
	}

	// An alias among readOnlyCollections may stand for a collection named
	// in collections, and the other way round.
	for _, spec := range []MCPAPIKey{
		{Name: "k", Collections: []string{"a", "b"}, ReadOnlyCollections: []string{"al"}},
		{Name: "k", Collections: []string{"al", "b"}, ReadOnlyCollections: []string{"a"}},
	} {
		got, _, err := s.createMCPAPIKey(spec)
		if err != nil || got.ReadOnly || len(got.ReadOnlyCollections) != 1 {
			t.Errorf("%v: %+v %v", spec.ReadOnlyCollections, got, err)
		}
	}

	// Every collection read-only is a read-only key.
	got, _, err := s.createMCPAPIKey(MCPAPIKey{Name: "k", Collections: []string{"a", "b"}, ReadOnlyCollections: []string{"b", "a"}, MemoryNamespace: "shop"})
	if err != nil || !got.ReadOnly || got.ReadOnlyCollections != nil || got.MemoryNamespace != "shop" {
		t.Errorf("all read-only: %+v %v", got, err)
	}

	// The listing shows both fields as stored.
	keys, err := s.mcpKeyStore.List()
	if err != nil {
		t.Fatal(err)
	}
	var shown bool
	for _, k := range keys {
		shown = shown || (len(k.ReadOnlyCollections) == 1 && k.ReadOnlyCollections[0] == "al")
	}
	if !shown {
		t.Errorf("listing lost readOnlyCollections: %+v", keys)
	}
}

func TestKeyScopeSpecOverREST(t *testing.T) {
	s := scopeServer(t)
	scopeMiddleware(t, s)
	rec := covDKeyRequest(s, s.handleMCPAPIKeys, http.MethodPost, `{"name":"p","collections":["a","b"],"readOnlyCollections":["b"],"memoryNamespace":"shop"}`, context.Background())
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"readOnlyCollections":["b"]`) || !strings.Contains(rec.Body.String(), `"memoryNamespace":"shop"`) {
		t.Errorf("create: %d %s", rec.Code, rec.Body)
	}
	if rec := covDKeyRequest(s, s.handleMCPAPIKeys, http.MethodPost, `{"name":"p","collections":["a"],"readOnlyCollections":["b"]}`, context.Background()); rec.Code != 400 {
		t.Errorf("an outside read-only collection was stored: %d %s", rec.Code, rec.Body)
	}
}
