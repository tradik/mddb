package main

import (
	"strings"
	"testing"
)

// TEST-002. Custom tools let an operator expose a pre-configured search under
// their own name. The validator is what stands between a YAML file and an MCP
// agent's tool list, so its refusals matter more than its acceptances.

func validTool(name string) MCPCustomToolConfig {
	return MCPCustomToolConfig{
		Name:        name,
		Description: "a tool",
		Action:      "search_documents",
	}
}

// A custom tool shadowing a built-in is a silent capability swap: the agent
// calls what it thinks is MDDB's own tool and gets someone's configuration.
func TestCustomToolCannotShadowAnyBuiltin(t *testing.T) {
	builtins := mcpBuiltinTools()
	if len(builtins) == 0 {
		t.Fatal("no built-in tools — the test cannot mean anything")
	}

	var unguarded []string
	for _, tool := range builtins {
		if err := validateMCPCustomTools([]MCPCustomToolConfig{validTool(tool.Name)}); err == nil {
			unguarded = append(unguarded, tool.Name)
		}
	}

	if len(unguarded) > 0 {
		t.Errorf("%d built-in tools can be shadowed by a custom tool of the same name:\n  %s",
			len(unguarded), strings.Join(unguarded, "\n  "))
	}
}

// The guard list used to be a hand-written copy of all 80 names. It is derived
// now, but the property worth pinning is the behaviour: a tool added to the
// built-in table is protected without anyone editing a second list.
func TestShadowGuardCoversNewlyAddedTools(t *testing.T) {
	// code_graph was added in CODE-005, long after the copy was written.
	err := validateMCPCustomTools([]MCPCustomToolConfig{validTool("code_graph")})
	if err == nil {
		t.Error("a tool added after the guard list was written is not protected")
	}
	if err != nil && !strings.Contains(err.Error(), "built-in") {
		t.Errorf("the error does not explain the conflict: %v", err)
	}
}

func TestCustomToolValidationRejectsBadDefinitions(t *testing.T) {
	cases := map[string]MCPCustomToolConfig{
		"no name":        {Description: "d", Action: "search_documents"},
		"no description": {Name: "x", Action: "search_documents"},
		"unknown action": {Name: "x", Description: "d", Action: "telepathy"},
		"empty action":   {Name: "x", Description: "d"},
	}
	for name, tool := range cases {
		if err := validateMCPCustomTools([]MCPCustomToolConfig{tool}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// Two tools with one name means an agent's call is ambiguous; which one runs
// would depend on map iteration order.
func TestCustomToolNamesMustBeUnique(t *testing.T) {
	err := validateMCPCustomTools([]MCPCustomToolConfig{
		validTool("my-search"),
		validTool("my-search"),
	})
	if err == nil {
		t.Fatal("duplicate custom tool names were accepted")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("the error does not name the problem: %v", err)
	}
}

func TestValidCustomToolsAreAccepted(t *testing.T) {
	tools := []MCPCustomToolConfig{
		validTool("search-handbook"),
		{Name: "find-runbooks", Description: "d", Action: "semantic_search"},
		{Name: "grep-logs", Description: "d", Action: "full_text_search"},
	}
	if err := validateMCPCustomTools(tools); err != nil {
		t.Errorf("a valid set was rejected: %v", err)
	}
}

func TestEmptyCustomToolListIsValid(t *testing.T) {
	if err := validateMCPCustomTools(nil); err != nil {
		t.Errorf("no custom tools should be fine: %v", err)
	}
}

// The error must say which entry is wrong; a YAML file with twenty tools and
// "invalid action" tells the operator nothing.
func TestCustomToolErrorsNameTheOffendingEntry(t *testing.T) {
	err := validateMCPCustomTools([]MCPCustomToolConfig{
		validTool("fine-one"),
		{Name: "broken-one", Description: "d", Action: "telepathy"},
	})
	if err == nil {
		t.Fatal("an invalid action was accepted")
	}
	if !strings.Contains(err.Error(), "broken-one") {
		t.Errorf("the error does not name the offending tool: %v", err)
	}
	if !strings.Contains(err.Error(), "[1]") {
		t.Errorf("the error does not give the entry's position: %v", err)
	}
}

// Custom tools must appear alongside the built-ins, or defining one has no
// visible effect.
func TestCustomToolsJoinTheAdvertisedList(t *testing.T) {
	all := mcpAllTools([]MCPCustomToolConfig{
		{Name: "search-handbook", Description: "Search the handbook", Action: "search_documents"},
	})

	var found bool
	builtinCount := 0
	for _, tool := range all {
		if tool.Name == "search-handbook" {
			found = true
			if tool.Description != "Search the handbook" {
				t.Errorf("the custom description was lost: %q", tool.Description)
			}
		}
		builtinCount++
	}
	if !found {
		t.Error("a defined custom tool is not advertised")
	}
	if builtinCount <= len(mcpBuiltinTools()) {
		t.Error("the custom tool did not extend the list")
	}
}
