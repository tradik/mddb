package main

import (
	"context"
	"fmt"
	"strings"

	json "mddb/internal/jsonx"
)

// The three MCP operations the chat needs from a foreign server (MCP-005),
// on mcpRemoteClient (chat_mcp_client.go).

// chatMcpMaxToolPages bounds the pages of a paginated tools/list.
const chatMcpMaxToolPages = 20

// mcpRemoteAnnotations are a tool's hints. Pointers, because absent is not
// false: a tool without readOnlyHint is not read-only.
type mcpRemoteAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool `json:"openWorldHint,omitempty"`
}

// mcpRemoteTool is one tool of tools/list.
type mcpRemoteTool struct {
	Name        string               `json:"name"`
	Title       string               `json:"title,omitempty"`
	Description string               `json:"description,omitempty"`
	Annotations mcpRemoteAnnotations `json:"annotations"`
}

// refusal says why a chat may not use the tool: "" for a tool marked
// readOnlyHint: true and not destructiveHint: true.
func (t mcpRemoteTool) refusal() string {
	a := t.Annotations
	switch {
	case a.DestructiveHint != nil && *a.DestructiveHint:
		return "it is marked destructiveHint: true"
	case a.ReadOnlyHint == nil || !*a.ReadOnlyHint:
		return "it is not marked readOnlyHint: true"
	}
	return ""
}

// mcpRemoteServerInfo is what initialize says about the server.
type mcpRemoteServerInfo struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

// initialize opens the session and acknowledges it.
func (c *mcpRemoteClient) initialize(ctx context.Context) (*mcpRemoteServerInfo, error) {
	var info mcpRemoteServerInfo
	params := map[string]any{"protocolVersion": chatMcpProtocolVersion, "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "mddbd-chat", "version": VERSION}}
	if err := c.call(ctx, "initialize", params, &info); err != nil {
		return nil, err
	}
	c.protocol = info.ProtocolVersion
	return &info, c.notify(ctx, "notifications/initialized")
}

// listTools reads every page of tools/list.
func (c *mcpRemoteClient) listTools(ctx context.Context) ([]mcpRemoteTool, error) {
	var tools []mcpRemoteTool
	cursor := ""
	for page := 0; page < chatMcpMaxToolPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res struct {
			Tools      []mcpRemoteTool `json:"tools"`
			NextCursor string          `json:"nextCursor"`
		}
		if err := c.call(ctx, "tools/list", params, &res); err != nil {
			return nil, err
		}
		tools = append(tools, res.Tools...)
		if cursor = res.NextCursor; cursor == "" {
			return tools, nil
		}
	}
	return nil, fmt.Errorf("tools/list: more than %d pages", chatMcpMaxToolPages)
}

// mcpRemoteContent is one block of a tool's result.
type mcpRemoteContent struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Resource *struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"resource"`
}

// callTool calls a tool and returns its result as text, and whether the
// tool reported an error.
func (c *mcpRemoteClient) callTool(ctx context.Context, name string, args map[string]any) (text string, isError bool, err error) {
	if args == nil {
		args = map[string]any{}
	}
	var res struct {
		Content           []mcpRemoteContent `json:"content"`
		StructuredContent json.RawMessage    `json:"structuredContent"`
		IsError           bool               `json:"isError"`
	}
	if err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &res); err != nil {
		return "", false, err
	}
	parts := make([]string, 0, len(res.Content))
	for _, block := range res.Content {
		switch {
		case block.Type == "text":
			parts = append(parts, block.Text)
		case block.Resource != nil && block.Resource.Text != "":
			parts = append(parts, block.Resource.Text)
		default:
			parts = append(parts, "["+block.Type+" content omitted]")
		}
	}
	if len(parts) == 0 && len(res.StructuredContent) > 0 {
		parts = append(parts, string(res.StructuredContent))
	}
	return strings.Join(parts, "\n"), res.IsError, nil
}
