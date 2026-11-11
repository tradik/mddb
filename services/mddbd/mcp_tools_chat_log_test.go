package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// The read-only conversation log tools (#406).

func TestMCPChatLogTools(t *testing.T) {
	s := chatServer(t)
	ctx := context.Background()
	ts := chatTools(s, ModeRead)

	out, err := ts.mcpCallTool(ctx, "chat_gaps", map[string]interface{}{})
	if err != nil || !strings.HasPrefix(out, "No docs gaps") {
		t.Fatalf("no gaps: %s %v", out, err)
	}
	chatLogSeed(t, s)

	out, err = ts.mcpCallTool(ctx, "chat_log_list", map[string]interface{}{"scenario": "support", "rating": "down", "from": float64(0), "to": "1970-01-02"})
	var page ChatLogPage
	if err != nil || json.Unmarshal([]byte(out), &page) != nil || len(page.Turns) != 1 || page.Turns[0].Rating != "down" {
		t.Fatalf("list: %s %v", out, err)
	}
	out, err = ts.mcpCallTool(ctx, "chat_log_list", map[string]interface{}{"limit": float64(1), "outcome": "answered"})
	if err != nil || json.Unmarshal([]byte(out), &page) != nil || len(page.Turns) != 1 || page.NextCursor == "" {
		t.Fatalf("page: %s %v", out, err)
	}
	out, err = ts.mcpCallTool(ctx, "chat_gaps", map[string]interface{}{"threshold": 0.9, "minCount": float64(1), "limit": float64(5)})
	var gaps ChatGapsResponse
	if err != nil || json.Unmarshal([]byte(out), &gaps) != nil || len(gaps.Gaps) != 2 {
		t.Fatalf("gaps: %s %v", out, err)
	}
	for name, args := range map[string]map[string]interface{}{
		"chat_log_list": {"from": "soon"},
		"chat_gaps":     {"to": "later"},
	} {
		if _, err := ts.mcpCallTool(ctx, name, args); err == nil {
			t.Errorf("%s with a bad time: no error", name)
		}
	}
	if _, err := ts.mcpCallTool(ctx, "chat_log_list", map[string]interface{}{"rating": "meh"}); err == nil {
		t.Error("bad rating: no error")
	}
	if _, err := ts.mcpCallTool(ctx, "chat_gaps", map[string]interface{}{"limit": float64(-1)}); err == nil {
		t.Error("bad limit: no error")
	}
	// A client that fails: the tools pass its error on.
	failing := &MCPToolServer{client: covBNewAllErr(), globalMode: ModeRW}
	for _, name := range []string{"chat_log_list", "chat_gaps"} {
		if _, err := failing.mcpCallTool(ctx, name, map[string]interface{}{}); !errors.Is(err, errCovBClient) {
			t.Errorf("%s through a failing client: %v", name, err)
		}
	}
	// Both are listed, read-only, in the admin toolset.
	for _, name := range []string{"chat_log_list", "chat_gaps"} {
		if mcpToolGroup[name] != ToolsetAdmin {
			t.Errorf("%s toolset: %q", name, mcpToolGroup[name])
		}
	}
}

func (c *covBAllErrClient) ChatLogList(context.Context, ChatLogFilter) (*ChatLogPage, error) {
	return nil, errCovBClient
}

func (c *covBAllErrClient) ChatGaps(context.Context, ChatGapsFilter) (*ChatGapsResponse, error) {
	return nil, errCovBClient
}
