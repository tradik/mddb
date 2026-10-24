package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #323: custom prompts from MDDB_MCP_CONFIG beside the built-in ones.
func TestCustomPromptsAreListedAndRendered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.yaml")
	if err := os.WriteFile(path, []byte(`custom_prompts:
  - name: review_function
    description: "Review one function for a given category"
    arguments:
      - { name: function, required: true }
      - { name: category, required: true }
      - { name: depth }
    template: |
      Review {{function}} for {{category}}.{{depth}}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MDDB_MCP_CONFIG", path)
	h := NewMCPHandlerWithConfig(nil, nil, MCPServerInfo{}, "", ModeRW, "")

	list := h.handlePromptsList(nil)["prompts"].([]MCPPrompt)
	if last := list[len(list)-1]; last.Name != "review_function" || len(last.Arguments) != 3 || !last.Arguments[0].Required {
		t.Errorf("custom prompt not listed after the built-ins: %+v", last)
	}
	if len(list) != len(mcpBuiltinPrompts())+1 {
		t.Errorf("built-in prompts lost: %d listed", len(list))
	}

	get := func(args map[string]interface{}) map[string]interface{} {
		return h.handlePromptsGet(nil, map[string]interface{}{"params": map[string]interface{}{"name": "review_function", "arguments": args}})
	}
	res := get(map[string]interface{}{"function": "parse_request", "category": "error handling"})
	msgs, _ := res["messages"].([]MCPPromptMessage)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Content["text"].(string), "Review parse_request for error handling.") {
		t.Errorf("rendered = %+v", res)
	}
	if res := get(map[string]interface{}{"function": "x"}); res["error"] == nil {
		t.Error("a missing required argument was accepted")
	}
}

func TestCustomPromptValidation(t *testing.T) {
	for name, prompts := range map[string][]MCPCustomPromptConfig{
		"no name":     {{Template: "x"}},
		"no template": {{Name: "p"}},
		"duplicate":   {{Name: "p", Template: "x"}, {Name: "p", Template: "y"}},
		"shadows":     {{Name: mcpBuiltinPrompts()[0].Name, Template: "x"}},
		"unnamed arg": {{Name: "p", Template: "x", Arguments: []MCPCustomPromptArgV{{}}}},
	} {
		if err := validateMCPCustomPrompts(prompts); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := validateMCPCustomPrompts([]MCPCustomPromptConfig{{Name: "p", Template: "x"}}); err != nil {
		t.Errorf("a valid prompt was refused: %v", err)
	}

	t.Setenv("MDDB_MCP_CONFIG", "")
	if loadMCPCustomPrompts() != nil {
		t.Error("prompts loaded with no config")
	}
	path := filepath.Join(t.TempDir(), "bad.yaml")
	_ = os.WriteFile(path, []byte("custom_prompts:\n  - name: p\n"), 0o600)
	t.Setenv("MDDB_MCP_CONFIG", path)
	if loadMCPCustomPrompts() != nil {
		t.Error("an invalid prompt set was served")
	}
}
