package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// The read-only chat conversation tools (#405).

func chatTools(s *Server, mode AccessMode) *MCPToolServer {
	return &MCPToolServer{client: NewDirectClient(s), globalMode: mode}
}

func TestMCPChatConversationTools(t *testing.T) {
	s := chatConvServer(t)
	ctx := context.Background()
	id := chatNew(t, s, nil, "user-1", "support")
	chatNew(t, s, nil, "user-1", "sales")
	chatSay(t, s, id, "user-1", "first", chatNow()+10)
	chatSay(t, s, id, "user-1", "second", chatNow()+11)
	ts := chatTools(s, ModeRead) // read-only tools work on a read-only server

	out, err := ts.mcpCallTool(ctx, "chat_conversation_list", map[string]interface{}{"sub": "user-1", "limit": float64(1)})
	var page ChatListResponse
	if err != nil || json.Unmarshal([]byte(out), &page) != nil || len(page.Conversations) != 1 || page.Conversations[0].ID != id || page.NextCursor == "" {
		t.Fatalf("list: %s %v", out, err)
	}
	out, err = ts.mcpCallTool(ctx, "chat_conversation_list", map[string]interface{}{"sub": "user-1", "scenario": "sales", "cursor": ""})
	if err != nil || json.Unmarshal([]byte(out), &page) != nil || len(page.Conversations) != 1 || page.Conversations[0].Scenario != "sales" {
		t.Fatalf("list by scenario: %s %v", out, err)
	}
	out, err = ts.mcpCallTool(ctx, "chat_conversation_get", map[string]interface{}{"id": id, "sub": "user-1", "limit": float64(1)})
	var conv ChatConversation
	if err != nil || json.Unmarshal([]byte(out), &conv) != nil || len(conv.Messages) != 1 || conv.Messages[0].Content != "second" || conv.MessageCount != 2 {
		t.Fatalf("get: %s %v", out, err)
	}

	for name, args := range map[string]map[string]interface{}{
		"chat_conversation_list": {},
		"chat_conversation_get":  {"id": id},
	} {
		if _, err := ts.mcpCallTool(ctx, name, args); err == nil || !strings.Contains(err.Error(), "required") {
			t.Errorf("%s without its arguments: %v", name, err)
		}
	}
	if _, err := ts.mcpCallTool(ctx, "chat_conversation_get", map[string]interface{}{"id": id, "sub": "user-2"}); !errors.Is(err, errNotFound) {
		t.Errorf("another user's conversation: %v, want not found", err)
	}
	if _, err := ts.mcpCallTool(ctx, "chat_conversation_list", map[string]interface{}{"sub": "user-1", "cursor": "!!"}); !errors.Is(err, errChatInvalid) {
		t.Errorf("bad cursor: %v", err)
	}
}

// The tools are read-only members of the admin toolset, and no chat tool
// writes: the host does that over REST or gRPC.
func TestMCPChatConversationToolsAreReadOnlyAdmin(t *testing.T) {
	ts := &MCPToolServer{}
	var names []string
	for _, tool := range mcpBuiltinTools() {
		if strings.HasPrefix(tool.Name, "chat_conversation_") {
			names = append(names, tool.Name)
			if mcpToolGroup[tool.Name] != ToolsetAdmin || !ts.isToolReadOnly(tool.Name) {
				t.Errorf("%s: toolset %q, read-only %v", tool.Name, mcpToolGroup[tool.Name], ts.isToolReadOnly(tool.Name))
			}
		}
	}
	if strings.Join(names, ",") != "chat_conversation_list,chat_conversation_get" {
		t.Errorf("chat conversation tools: %v", names)
	}
}

// With the main auth's claims the tools need read on the chat collection of
// the caller's tenant; an MCP API key (no claims) is scoped by its toolsets.
func TestMCPChatConversationToolPermissions(t *testing.T) {
	s := accessServer(t)
	ts := chatTools(s, ModeRW)
	as := func(c *JWTClaims) context.Context { return context.WithValue(context.Background(), authContextKey, c) }
	conv, err := s.chatCreate(as(root), ChatCreateRequest{Sub: "user-1", Scenario: "support"})
	if err != nil {
		t.Fatal(err)
	}
	list := map[string]interface{}{"sub": "user-1"}
	get := map[string]interface{}{"id": conv.ID, "sub": "user-1"}
	for name, args := range map[string]map[string]interface{}{"chat_conversation_list": list, "chat_conversation_get": get} {
		if _, err := ts.mcpCallTool(as(alice), name, args); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s without the permission: %v, want forbidden", name, err)
		}
		if _, err := ts.mcpCallTool(as(root), name, args); err != nil {
			t.Errorf("%s as an administrator: %v", name, err)
		}
		if _, err := ts.mcpCallTool(context.Background(), name, args); err != nil {
			t.Errorf("%s on an MCP key: %v", name, err)
		}
	}
	// A tenant reads its own store, where the global conversation is not.
	if _, err := ts.mcpCallTool(as(tina), "chat_conversation_get", get); !errors.Is(err, errNotFound) {
		t.Errorf("tenant reads a global conversation: %v, want not found", err)
	}
}
