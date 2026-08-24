package main

import (
	"context"
	"fmt"
	"os"
)

// MCPCustomToolConfig defines a single custom YAML tool.
type MCPCustomToolConfig struct {
	Name        string               `yaml:"name"`
	Description string               `yaml:"description"`
	Action      string               `yaml:"action"` // semantic_search, search_documents, full_text_search
	Defaults    MCPCustomToolDefs    `yaml:"defaults"`
	Parameters  []MCPCustomToolParam `yaml:"parameters"`
}

// MCPCustomToolDefs holds pre-filled arguments for the underlying action.
type MCPCustomToolDefs struct {
	Collection     string              `yaml:"collection"`
	TopK           int                 `yaml:"topK"`
	Threshold      float64             `yaml:"threshold"`
	IncludeContent *bool               `yaml:"includeContent"`
	Sort           string              `yaml:"sort"`
	Asc            *bool               `yaml:"asc"`
	Limit          int                 `yaml:"limit"`
	Offset         int                 `yaml:"offset"`
	FilterMeta     map[string][]string `yaml:"filterMeta"`
	Query          string              `yaml:"query"`
	// Fields restricts the returned meta keys to the listed names (empty = all).
	// Applies to semantic_search, search_documents and full_text_search.
	Fields []string `yaml:"fields"`
}

// MCPCustomToolParam defines a parameter exposed to the AI.
type MCPCustomToolParam struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"` // string, integer, number, boolean, object
	Required    bool   `yaml:"required"`
	Description string `yaml:"description"`
}

// mcpBuiltinTools returns the list of hardcoded MCP tools.

// mcpCustomToolToMCPTool converts a YAML custom tool definition into an MCPTool.
func mcpCustomToolToMCPTool(ct MCPCustomToolConfig) MCPTool {
	properties := map[string]interface{}{}
	var required []string

	for _, p := range ct.Parameters {
		prop := map[string]interface{}{
			"type": p.Type,
		}
		if p.Description != "" {
			prop["description"] = p.Description
		}
		properties[p.Name] = prop
		if p.Required {
			required = append(required, p.Name)
		}
	}

	schema := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}

	return MCPTool{
		Name:        ct.Name,
		Description: ct.Description,
		InputSchema: schema,
	}
}

// mcpAllTools returns built-in tools plus custom tools from config, with annotations and output schemas.
// Set MDDB_MCP_BUILTIN_TOOLS=false to expose only custom tools (no built-in tools).
func mcpAllTools(customDefs []MCPCustomToolConfig) []MCPTool {
	var tools []MCPTool
	if os.Getenv("MDDB_MCP_BUILTIN_TOOLS") != "false" {
		tools = mcpBuiltinTools()
	}
	for _, ct := range customDefs {
		tools = append(tools, mcpCustomToolToMCPTool(ct))
	}
	tools = annotateTools(tools)
	tools = applyOutputSchemas(tools)
	return tools
}

// mcpCustomToolLockedKeys are the scope/data-minimization controls a custom
// tool exists to pin down. When the operator sets one of these in Defaults,
// client args must NOT be able to override it (SEC-010) — otherwise a tool
// pinned to `collection: public` + `include_content: false` could be called
// with `collection: secrets` + `include_content: true`.
var mcpCustomToolLockedKeys = map[string]bool{
	"collection":      true,
	"filter_meta":     true,
	"include_content": true,
	"fields":          true,
}

// mcpMergeCustomToolArgs builds the effective argument map for a custom tool:
// operator defaults first, then user args restricted to the parameters the
// tool declares, with operator-pinned scope keys locked (SEC-010).
func mcpMergeCustomToolArgs(ct MCPCustomToolConfig, userArgs map[string]interface{}) (map[string]interface{}, error) {
	merged := make(map[string]interface{})

	d := ct.Defaults
	if d.Collection != "" {
		merged["collection"] = d.Collection
	}
	if d.Query != "" {
		merged["query"] = d.Query
	}

	if err := mcpMergeActionDefaults(ct.Action, d, merged); err != nil {
		return nil, err
	}
	mcpMergeProjectionDefaults(d, merged)

	declared := make(map[string]bool, len(ct.Parameters))
	for _, p := range ct.Parameters {
		declared[p.Name] = true
	}
	for k, v := range userArgs {
		if _, pinned := merged[k]; pinned && mcpCustomToolLockedKeys[k] {
			continue // operator-pinned scope wins over the client
		}
		if !declared[k] {
			continue // only declared parameters may pass through
		}
		merged[k] = v
	}

	return merged, nil
}

// mcpCallCustomTool merges user-provided args with defaults, then delegates to the built-in tool.
func (s *MCPToolServer) mcpCallCustomTool(ctx context.Context, ct MCPCustomToolConfig, userArgs map[string]interface{}) (string, error) {
	merged, err := mcpMergeCustomToolArgs(ct, userArgs)
	if err != nil {
		return "", err
	}
	return s.mcpCallTool(ctx, ct.Action, merged)
}

// mcpMergeActionDefaults fills action-specific default arguments into merged.
func mcpMergeActionDefaults(action string, d MCPCustomToolDefs, merged map[string]interface{}) error {
	switch action {
	case "semantic_search":
		mcpMergeSemanticDefaults(d, merged)
	case "search_documents":
		mcpMergeSearchDocDefaults(d, merged)
	case "full_text_search":
		if d.Limit > 0 {
			merged["limit"] = float64(d.Limit)
		}
	default:
		return fmt.Errorf("unknown custom tool action: %s", action)
	}
	return nil
}

// mcpMergeSemanticDefaults fills semantic_search default arguments into merged.
func mcpMergeSemanticDefaults(d MCPCustomToolDefs, merged map[string]interface{}) {
	if d.TopK > 0 {
		merged["top_k"] = float64(d.TopK)
	}
	if d.Threshold > 0 {
		merged["threshold"] = d.Threshold
	}
	if d.FilterMeta != nil {
		merged["filter_meta"] = mcpMetaToInterface(d.FilterMeta)
	}
}

// mcpMergeSearchDocDefaults fills search_documents default arguments into merged.
func mcpMergeSearchDocDefaults(d MCPCustomToolDefs, merged map[string]interface{}) {
	if d.Sort != "" {
		merged["sort"] = d.Sort
	}
	if d.Asc != nil {
		merged["asc"] = *d.Asc
	}
	if d.Limit > 0 {
		merged["limit"] = float64(d.Limit)
	}
	if d.Offset > 0 {
		merged["offset"] = float64(d.Offset)
	}
	if d.FilterMeta != nil {
		merged["filter_meta"] = mcpMetaToInterface(d.FilterMeta)
	}
}

// mcpMergeProjectionDefaults wires the token-saving projection controls
// (includeContent + fields) shared by every search action, so a custom tool
// can drop the document body and/or restrict returned meta keys via YAML.
func mcpMergeProjectionDefaults(d MCPCustomToolDefs, merged map[string]interface{}) {
	if d.IncludeContent != nil {
		merged["include_content"] = *d.IncludeContent
	}
	if len(d.Fields) > 0 {
		merged["fields"] = d.Fields
	}
}

func mcpMetaToInterface(meta map[string][]string) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range meta {
		items := make([]interface{}, len(v))
		for i, s := range v {
			items[i] = s
		}
		result[k] = items
	}
	return result
}

// validateMCPCustomTools validates custom tool definitions.
func validateMCPCustomTools(tools []MCPCustomToolConfig) error {
	// Derived from mcpBuiltinTools rather than restated.
	//
	// This was a hand-maintained copy of all 80 names — a second source of
	// truth that happened to be in step, and would have drifted the first
	// time someone added a tool and forgot this file. A custom tool shadowing
	// a built-in is a silent capability swap, so the list has to be the real
	// one (TEST-002).
	builtinNames := make(map[string]bool, len(mcpBuiltinTools()))
	for _, tool := range mcpBuiltinTools() {
		builtinNames[tool.Name] = true
	}
	validActions := map[string]bool{
		"semantic_search": true, "search_documents": true, "full_text_search": true, "fts_languages": true,
	}
	validTypes := map[string]bool{
		"string": true, "integer": true, "number": true, "boolean": true, "object": true,
	}
	seen := map[string]bool{}

	for i, t := range tools {
		if t.Name == "" {
			return fmt.Errorf("custom_tools[%d]: name is required", i)
		}
		if builtinNames[t.Name] {
			return fmt.Errorf("custom_tools[%d]: name %q conflicts with built-in tool", i, t.Name)
		}
		if seen[t.Name] {
			return fmt.Errorf("custom_tools[%d]: duplicate name %q", i, t.Name)
		}
		seen[t.Name] = true
		if !validActions[t.Action] {
			return fmt.Errorf("custom_tools[%d] (%s): invalid action %q (must be semantic_search, search_documents, or full_text_search)", i, t.Name, t.Action)
		}
		if t.Description == "" {
			return fmt.Errorf("custom_tools[%d] (%s): description is required", i, t.Name)
		}
		for j, p := range t.Parameters {
			if p.Name == "" {
				return fmt.Errorf("custom_tools[%d] (%s) param[%d]: name is required", i, t.Name, j)
			}
			if !validTypes[p.Type] {
				return fmt.Errorf("custom_tools[%d] (%s) param[%d] (%s): invalid type %q", i, t.Name, j, p.Name, p.Type)
			}
		}
	}
	return nil
}
