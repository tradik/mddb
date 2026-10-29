package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// A session id chosen by the caller may be shorter than the eight characters
// the summary heading shows; summarizing one used to panic the request.
func TestMemorySummarize_ShortSessionID(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	// MCP memory_add_message takes any session id, which is how such a
	// session comes to exist.
	tools := &MCPToolServer{client: NewDirectClient(s)}
	if _, err := tools.mcpCallTool(context.Background(), "memory_add_message",
		map[string]interface{}{"session_id": "abc", "role": "user", "content": "hello"}); err != nil {
		t.Fatal(err)
	}
	rec := doRequest(t, s.handleMemorySummarize, MemorySummarizeRequest{SessionID: "abc", UserID: "u1"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Session Summary: abc") {
		t.Fatalf("summarize: %d %s", rec.Code, rec.Body.String())
	}
	if shortSessionID("0123456789") != "01234567" {
		t.Fatal("long ids are cut to eight")
	}
}
