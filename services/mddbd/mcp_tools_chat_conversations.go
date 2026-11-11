package main

import (
	"context"
	"errors"

	json "mddb/internal/jsonx"
)

// Chat conversation tools (#405): read-only, in the admin toolset, for an
// operator looking at what a user's assistant said. The host writes
// conversations over REST or gRPC; no tool creates, appends or deletes.
//
// Both tools name the user (sub), as every read of the store does.

// mcpChatConversationTools are the chat conversation tools.
func mcpChatConversationTools() []MCPTool {
	sub := map[string]interface{}{"type": "string", "description": "The user's id (the sub of the host's signed join token)"}
	return []MCPTool{
		{
			Name: "chat_conversation_list",
			Description: "List one user's persistent chat conversations (mddb-chat), newest first: id, scenario, title, " +
				"message count, times and whether it has ended. Read-only.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"sub":      sub,
				"scenario": map[string]interface{}{"type": "string", "description": "Only conversations of this scenario"},
				"limit":    map[string]interface{}{"type": "integer", "description": "Page size (default 50, max 200)"},
				"cursor":   map[string]interface{}{"type": "string", "description": "nextCursor of the previous page"},
			}, "required": []string{"sub"}},
		},
		{
			Name: "chat_conversation_get",
			Description: "Read one persistent chat conversation of a user with its messages (role, content, tool calls, " +
				"model, tokens), oldest first. A conversation of another user is not found. Read-only.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"id":    map[string]interface{}{"type": "string", "description": "The conversation id, as chat_conversation_list shows it"},
				"sub":   sub,
				"limit": map[string]interface{}{"type": "integer", "description": "Only the last N messages (default: all)"},
			}, "required": []string{"id", "sub"}},
		},
	}
}

func (s *MCPToolServer) toolChatConversationList(ctx context.Context, args map[string]interface{}) (string, error) {
	sub := mcpGetString(args, "sub")
	if sub == "" {
		return "", errors.New("sub is required")
	}
	page, err := s.client.ChatConversationList(ctx, sub, mcpGetString(args, "scenario"), mcpGetInt(args, "limit"), mcpGetString(args, "cursor"))
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(page, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolChatConversationGet(ctx context.Context, args map[string]interface{}) (string, error) {
	id, sub := mcpGetString(args, "id"), mcpGetString(args, "sub")
	if id == "" || sub == "" {
		return "", errors.New("id and sub are required")
	}
	conv, err := s.client.ChatConversationGet(ctx, id, sub, mcpGetInt(args, "limit"))
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(conv, "", "  ")
	return string(data), nil
}
