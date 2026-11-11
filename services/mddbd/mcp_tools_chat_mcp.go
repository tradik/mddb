package main

import (
	"context"

	json "mddb/internal/jsonx"
)

// Chat MCP secret tools (MCP-005), in the admin toolset and admin only: the
// credentials of the external MCP servers chat scenarios name. A listing
// gives names, never values. Inspecting a server and calling its tools are
// left to REST, gRPC and the panel.

// chatMcpServerSchema describes one entry of a scenario's mcpServers.
var chatMcpServerSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"name":           map[string]interface{}{"type": "string"},
		"url":            map[string]interface{}{"type": "string", "description": "https URL of an MCP server (Streamable HTTP)"},
		"enabled":        map[string]interface{}{"type": "boolean"},
		"secretRef":      map[string]interface{}{"type": "string", "description": "Name of a chat MCP secret"},
		"allowedTools":   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Read-only tools of the server the assistant may call"},
		"timeoutMs":      map[string]interface{}{"type": "integer", "description": "Default 8000"},
		"resultMaxChars": map[string]interface{}{"type": "integer", "description": "Default 4000"},
	},
	"required": []string{"name", "url"},
}

// mcpChatMcpTools are the chat MCP secret tools.
func mcpChatMcpTools() []MCPTool {
	name := map[string]interface{}{"type": "string", "description": "The secret's name"}
	return []MCPTool{
		{
			Name: "chat_mcp_secret_list",
			Description: "List the credentials of the external MCP servers chat scenarios use: each one's name, header, set: true " +
				"and the scenarios naming it. Values are write-only and never returned. Needs admin.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			Name: "chat_mcp_secret_put",
			Description: "Set or replace a credential for external MCP servers: a bearer token, or the value of the header named in header. " +
				"Stored sealed with the at-rest key when one is set; never returned. Needs admin.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"name":   name,
				"value":  map[string]interface{}{"type": "string", "description": "The token or header value"},
				"header": map[string]interface{}{"type": "string", "description": "A header name; Authorization: Bearer when left out"},
			}, "required": []string{"name", "value"}},
		},
		{
			Name:        "chat_mcp_secret_delete",
			Description: "Delete a credential of external MCP servers. Refused while a scenario names it; the error names them. Needs admin.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"name": name}, "required": []string{"name"}},
		},
	}
}

func (s *MCPToolServer) toolChatMcpSecretList(ctx context.Context, _ map[string]interface{}) (string, error) {
	list, err := s.client.ChatMcpSecretList(ctx)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(map[string]any{"secrets": list}, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolChatMcpSecretPut(ctx context.Context, args map[string]interface{}) (string, error) {
	name, err := chatScenarioName(args)
	if err != nil {
		return "", err
	}
	header := mcpGetString(args, "header")
	created, err := s.client.ChatMcpSecretPut(ctx, name, header, mcpGetString(args, "value"))
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal(map[string]any{"name": name, "header": header, "set": true, "created": created})
	return string(data), nil
}

func (s *MCPToolServer) toolChatMcpSecretDelete(ctx context.Context, args map[string]interface{}) (string, error) {
	name, err := chatScenarioName(args)
	if err != nil {
		return "", err
	}
	if err := s.client.ChatMcpSecretDelete(ctx, name); err != nil {
		return "", err
	}
	data, _ := json.Marshal(map[string]any{"deleted": true, "name": name})
	return string(data), nil
}
