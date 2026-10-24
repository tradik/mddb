package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #328, #321: what a custom tool's defaults may set, and the read-only
// actions it may wrap.

func boolp(b bool) *bool { return &b }

func TestFullTextDefaultsAreAllApplied(t *testing.T) {
	ct := MCPCustomToolConfig{
		Name: "codeSearch", Action: "full_text_search", Description: "x",
		Defaults: MCPCustomToolDefs{
			Collection: "code", Limit: 7, Algorithm: "bm25", Highlight: boolp(true), FragmentSize: 80,
			DisableStem: boolp(true), Mode: "phrase", FilterMeta: map[string][]string{"module": {"net"}},
		},
		Parameters: []MCPCustomToolParam{{Name: "query", Type: "string"}, {Name: "filter_meta", Type: "object"}},
	}
	merged, err := mcpMergeCustomToolArgs(ct, map[string]interface{}{
		"query": "x", "filter_meta": map[string]interface{}{"module": []interface{}{"vm"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{"limit": float64(7), "algorithm": "bm25", "highlight": true,
		"fragment_size": float64(80), "disable_stem": true, "mode": "phrase"} {
		if merged[k] != want {
			t.Errorf("%s = %v, want %v", k, merged[k], want)
		}
	}
	// The operator's scope wins over the client's.
	fm, _ := json.Marshal(merged["filter_meta"])
	if string(fm) != `{"module":["net"]}` {
		t.Errorf("filter_meta = %s, want the pinned module net", fm)
	}
}

func TestDefaultsThatTheActionIgnoresAreRefused(t *testing.T) {
	err := validateMCPCustomTools([]MCPCustomToolConfig{{
		Name: "t", Action: "full_text_search", Description: "x", Defaults: MCPCustomToolDefs{TopK: 5},
	}})
	if err == nil || !strings.Contains(err.Error(), "topK") {
		t.Errorf("topK on full_text_search: %v", err)
	}
	err = validateMCPCustomTools([]MCPCustomToolConfig{{Name: "t", Action: "delete_collection", Description: "x"}})
	if err == nil || !strings.Contains(err.Error(), "hybrid_search") {
		t.Errorf("a write action should be refused, listing the supported ones: %v", err)
	}
	for _, action := range []string{"hybrid_search", "code_graph", "aggregate", "fts_languages"} {
		if err := validateMCPCustomTools([]MCPCustomToolConfig{{Name: "t" + action, Action: action, Description: "x"}}); err != nil {
			t.Errorf("%s: %v", action, err)
		}
		if _, err := mcpMergeCustomToolArgs(MCPCustomToolConfig{Action: action}, nil); err != nil {
			t.Errorf("merging defaults for %s: %v", action, err)
		}
	}
}

// A misspelt key under defaults used to be dropped in silence.
func TestUnknownConfigKeysFailAtStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.yaml")
	_ = os.WriteFile(path, []byte("custom_tools:\n  - name: t\n    action: full_text_search\n    description: x\n    defaults:\n      fragmnetSize: 80\n"), 0o600)
	if _, err := loadMCPConfig(path); err == nil || !strings.Contains(err.Error(), "fragmnetSize") {
		t.Errorf("unknown key: %v", err)
	}
	_ = os.WriteFile(path, []byte(""), 0o600)
	if _, err := loadMCPConfig(path); err != nil {
		t.Errorf("an empty file: %v", err)
	}
}

func TestCustomToolsWrapTheNewActions(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "kb", "a.md", "en", "# Networking\nsockets and buffers", map[string][]string{"module": {"net"}})
	addTestDoc(t, s, "kb", "b.md", "en", "# Memory\npages and buffers", map[string][]string{"module": {"vm"}})
	ts := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}

	// full_text_search scoped by the operator returns nothing outside it.
	out, err := ts.mcpCallCustomTool(context.Background(), MCPCustomToolConfig{
		Action: "full_text_search", Defaults: MCPCustomToolDefs{Collection: "kb", FilterMeta: map[string][]string{"module": {"net"}}},
		Parameters: []MCPCustomToolParam{{Name: "query", Type: "string"}},
	}, map[string]interface{}{"query": "buffers"})
	if err != nil || !strings.Contains(out, "a.md") || strings.Contains(out, "b.md") {
		t.Errorf("scoped full-text search returned %q, %v", out, err)
	}

	out, err = ts.mcpCallCustomTool(context.Background(), MCPCustomToolConfig{
		Action: "aggregate", Defaults: MCPCustomToolDefs{Collection: "kb", GroupBy: []string{"module"}},
	}, nil)
	if err != nil || !strings.Contains(out, "net") || !strings.Contains(out, "vm") {
		t.Errorf("aggregate returned %q, %v", out, err)
	}

	out, err = ts.mcpCallCustomTool(context.Background(), MCPCustomToolConfig{
		Action: "hybrid_search", Defaults: MCPCustomToolDefs{Collection: "kb"},
		Parameters: []MCPCustomToolParam{{Name: "query", Type: "string"}},
	}, map[string]interface{}{"query": "sockets"})
	if err != nil || !strings.Contains(out, "a.md") {
		t.Errorf("hybrid_search returned %q, %v", out, err)
	}

	if _, err := ts.mcpCallCustomTool(context.Background(), MCPCustomToolConfig{
		Action: "code_graph", Defaults: MCPCustomToolDefs{Collection: "kb", Depth: 1},
		Parameters: []MCPCustomToolParam{{Name: "key", Type: "string"}},
	}, map[string]interface{}{"key": "a.md"}); err != nil && !strings.Contains(err.Error(), "not") {
		t.Errorf("code_graph: %v", err)
	}
}

// The built-in full_text_search takes what POST /v1/fts takes.
func TestMCPFullTextSearchTakesFiltersModesAndFacets(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "kb", "a.md", "en", "running sockets", map[string][]string{"module": {"net"}})
	addTestDoc(t, s, "kb", "b.md", "en", "running pages", map[string][]string{"module": {"vm"}})
	ts := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}

	out, err := ts.toolFTSSearch(context.Background(), map[string]interface{}{
		"collection": "kb", "query": "running", "filter_meta": map[string]interface{}{"module": []interface{}{"vm"}},
		"disable_stem": true, "facet_by": []interface{}{"module"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var resp MCPFTSSearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 || resp.Results[0].Document.Key != "b.md" {
		t.Errorf("filter_meta not applied: %+v", resp.Results)
	}
	if len(resp.Facets) == 0 {
		t.Error("facet_by was in the schema and is still ignored")
	}

	out, err = ts.toolFTSSearch(context.Background(), map[string]interface{}{"collection": "kb", "query": `"running sockets"`, "mode": "phrase"})
	if err != nil || !strings.Contains(out, "a.md") || strings.Contains(out, "b.md") {
		t.Errorf("phrase mode over MCP: %q, %v", out, err)
	}
}
