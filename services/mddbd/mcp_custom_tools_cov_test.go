package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestCovBCustomToolToMCPToolSchema(t *testing.T) {
	tool := mcpCustomToolToMCPTool(MCPCustomToolConfig{
		Name: "find_posts", Description: "Find posts",
		Parameters: []MCPCustomToolParam{
			{Name: "query", Type: "string", Required: true, Description: "what to look for"},
			{Name: "limit", Type: "integer"},
		},
	})
	if tool.Name != "find_posts" || tool.Description != "Find posts" {
		t.Errorf("tool = %+v", tool)
	}
	schema := tool.InputSchema
	props := schema["properties"].(map[string]interface{})
	q := props["query"].(map[string]interface{})
	if q["type"] != "string" || q["description"] != "what to look for" {
		t.Errorf("query prop = %v", q)
	}
	if _, has := props["limit"].(map[string]interface{})["description"]; has {
		t.Error("empty description emitted")
	}
	if !reflect.DeepEqual(schema["required"], []string{"query"}) {
		t.Errorf("required = %v", schema["required"])
	}

	// No required parameters: no "required" key at all.
	bare := mcpCustomToolToMCPTool(MCPCustomToolConfig{Name: "x", Parameters: []MCPCustomToolParam{{Name: "a", Type: "string"}}})
	if _, has := bare.InputSchema["required"]; has {
		t.Error("required emitted with no required parameters")
	}
}

// TestCovBCustomToolPinnedScopeWins checks SEC-010: a client cannot override
// a collection the operator pinned, even when it declares the parameter.
func TestCovBCustomToolPinnedScopeWins(t *testing.T) {
	ct := MCPCustomToolConfig{
		Name: "public_search", Action: "search_documents",
		Defaults:   MCPCustomToolDefs{Collection: "public"},
		Parameters: []MCPCustomToolParam{{Name: "collection", Type: "string"}, {Name: "limit", Type: "integer"}},
	}
	merged, err := mcpMergeCustomToolArgs(ct, map[string]interface{}{"collection": "secrets", "limit": 3.0, "undeclared": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if merged["collection"] != "public" || merged["limit"] != 3.0 {
		t.Errorf("merged = %v", merged)
	}
	if _, ok := merged["undeclared"]; ok {
		t.Error("undeclared parameter passed through")
	}

	// End to end: the client request carries the pinned collection.
	fake := covBNewFake()
	ts := &MCPToolServer{client: fake, globalMode: ModeRW, customTools: []MCPCustomToolConfig{ct}}
	if _, err := ts.mcpCallTool(context.Background(), "public_search", map[string]interface{}{"collection": "secrets"}); err != nil {
		t.Fatal(err)
	}
	if r := fake.covBCall(t, "Search").(*MCPSearchRequest); r.Collection != "public" {
		t.Errorf("collection = %q, want public", r.Collection)
	}
}

// TestCovBCustomToolUnknownActionRefused checks a custom tool whose action is
// not a known one fails at call time instead of dispatching anything.
func TestCovBCustomToolUnknownActionRefused(t *testing.T) {
	fake := covBNewFake()
	ts := &MCPToolServer{client: fake, globalMode: ModeRW, customTools: []MCPCustomToolConfig{{Name: "evil", Action: "delete_collection"}}}
	_, err := ts.mcpCallTool(context.Background(), "evil", map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "unknown custom tool action: delete_collection") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.calls) != 0 {
		t.Errorf("client reached: %v", fake.calls)
	}
}

func TestCovBValidateCustomToolParameters(t *testing.T) {
	base := func(p MCPCustomToolParam) []MCPCustomToolConfig {
		return []MCPCustomToolConfig{{Name: "t1", Description: "d", Action: "search_documents", Parameters: []MCPCustomToolParam{p}}}
	}
	if err := validateMCPCustomTools(base(MCPCustomToolParam{Type: "string"})); err == nil || !strings.Contains(err.Error(), "param[0]: name is required") {
		t.Errorf("unnamed param: err = %v", err)
	}
	if err := validateMCPCustomTools(base(MCPCustomToolParam{Name: "p", Type: "array"})); err == nil || !strings.Contains(err.Error(), `invalid type "array"`) {
		t.Errorf("bad type: err = %v", err)
	}
	if err := validateMCPCustomTools(base(MCPCustomToolParam{Name: "p", Type: "object"})); err != nil {
		t.Errorf("valid tool refused: %v", err)
	}
}
