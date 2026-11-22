package main

import (
	"encoding/json"
	"strings"
)

// applyOutputSchemas adds outputSchema to tools that have structured output definitions.
func applyOutputSchemas(tools []MCPTool) []MCPTool {
	for i := range tools {
		if schema, ok := mcpToolOutputSchemas[tools[i].Name]; ok {
			if tools[i].InputSchema == nil {
				tools[i].InputSchema = map[string]interface{}{"type": "object"}
			}
			// Store output schema in a way compatible with the tool definition.
			// The MCP spec 2025-11-25 adds outputSchema as a top-level field on Tool.
			tools[i].OutputSchema = schema
		}
	}
	return tools
}

// mcpStructuredContent is the structuredContent of a successful tool result.
//
// A tool that declares an output schema returns its JSON answer as an object
// on every call (#461): the MCP specification requires it, and a client that
// checks refuses the whole answer otherwise. Numbers stay json.Number, so an
// int64 is not rounded through float64. Any other tool returns it only when
// the server's cap lowered the limit (#368), nil otherwise.
func mcpStructuredContent(name, result string) map[string]any {
	if _, declared := mcpToolOutputSchemas[name]; !declared {
		return mcpLimitContent(result)
	}
	dec := json.NewDecoder(strings.NewReader(result))
	dec.UseNumber()
	var obj map[string]any
	if dec.Decode(&obj) == nil && obj != nil {
		return obj
	}
	// Not a JSON object: a custom tool that took a built-in tool's name and
	// wraps an action answering in prose. The schema still asks for an object.
	return map[string]any{"text": result}
}

// mcpToolResult builds the result of a successful tools/call: the answer as
// text, as every revision has had it, and as structuredContent where
// mcpStructuredContent has one. Shared by JSON-RPC and POST /tools/call.
func mcpToolResult(name, result string) map[string]any {
	res := map[string]any{
		"content": []map[string]any{{"type": "text", "text": result}},
	}
	if structured := mcpStructuredContent(name, result); structured != nil {
		res["structuredContent"] = structured
	}
	return res
}
