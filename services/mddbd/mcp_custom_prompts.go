package main

import (
	"fmt"
	"log/slog"
	"os"
)

// Custom MCP prompts (#323).
//
// An operator could add tools through MDDB_MCP_CONFIG but not prompts, so a
// ready-made instruction — "review this function: fetch it, then the similar
// changes, then…" — had to live in every client or in the server
// instructions, where an agent cannot select it. custom_prompts sit beside
// custom_tools in the same file, appear in prompts/list after the built-in
// ones, and are rendered by prompts/get with the {{name}} substitution
// responsePrompt and automations already use.
//
//	custom_prompts:
//	  - name: review_function
//	    description: "Review one function for a given category"
//	    arguments:
//	      - { name: function, required: true }
//	      - { name: category, required: true }
//	    template: |
//	      Review `{{function}}` for {{category}} ...

// MCPCustomPromptConfig is one prompt from the MCP config file.
type MCPCustomPromptConfig struct {
	Name        string                `yaml:"name"`
	Description string                `yaml:"description"`
	Arguments   []MCPCustomPromptArgV `yaml:"arguments"`
	Template    string                `yaml:"template"`
}

// MCPCustomPromptArgV is one argument a custom prompt takes.
type MCPCustomPromptArgV struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
}

// loadMCPCustomPrompts reads custom_prompts from MDDB_MCP_CONFIG. An invalid
// set is logged and none are served, as with custom tools.
func loadMCPCustomPrompts() []MCPCustomPromptConfig {
	path := os.Getenv("MDDB_MCP_CONFIG")
	if path == "" {
		return nil
	}
	cfg, err := loadMCPConfig(path)
	if err != nil {
		return nil // reported by loadMCPCustomTools, which reads the same file
	}
	if err := validateMCPCustomPrompts(cfg.CustomPrompts); err != nil {
		slog.Warn("invalid MCP custom prompts", "err", err)
		return nil
	}
	return cfg.CustomPrompts
}

func validateMCPCustomPrompts(prompts []MCPCustomPromptConfig) error {
	builtin := map[string]bool{}
	for _, p := range mcpBuiltinPrompts() {
		builtin[p.Name] = true
	}
	seen := map[string]bool{}
	for i, p := range prompts {
		switch {
		case p.Name == "":
			return fmt.Errorf("custom_prompts[%d]: name is required", i)
		case builtin[p.Name]:
			return fmt.Errorf("custom_prompts[%d]: name %q conflicts with a built-in prompt", i, p.Name)
		case seen[p.Name]:
			return fmt.Errorf("custom_prompts[%d]: duplicate name %q", i, p.Name)
		case p.Template == "":
			return fmt.Errorf("custom_prompts[%d] (%s): template is required", i, p.Name)
		}
		seen[p.Name] = true
		for j, a := range p.Arguments {
			if a.Name == "" {
				return fmt.Errorf("custom_prompts[%d] (%s) arguments[%d]: name is required", i, p.Name, j)
			}
		}
	}
	return nil
}

// asMCPPrompt is the prompts/list entry for a custom prompt.
func (p MCPCustomPromptConfig) asMCPPrompt() MCPPrompt {
	out := MCPPrompt{Name: p.Name, Description: p.Description}
	for _, a := range p.Arguments {
		out.Arguments = append(out.Arguments, MCPPromptArg{Name: a.Name, Description: a.Description, Required: a.Required})
	}
	return out
}

// render fills the template with the given arguments.
func (p MCPCustomPromptConfig) render(args map[string]interface{}) ([]MCPPromptMessage, string, error) {
	vars := make(map[string]string, len(p.Arguments))
	for _, a := range p.Arguments {
		v, ok := args[a.Name]
		if !ok || v == nil || v == "" {
			if a.Required {
				return nil, "", fmt.Errorf("%s argument required", a.Name)
			}
			vars[a.Name] = ""
			continue
		}
		vars[a.Name] = fmt.Sprint(v)
	}
	text := ExpandTemplate(p.Template, vars)
	return []MCPPromptMessage{{Role: "user", Content: map[string]interface{}{"type": "text", "text": text}}},
		p.Description, nil
}
