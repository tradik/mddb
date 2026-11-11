package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// A session's title is part of what the listing returns: the create call
// answered it, the list dropped it, so a session picker could only show ids.

func listedTitles(t *testing.T, s *Server) map[string]string {
	t.Helper()
	rec := doRequest(t, s.handleMemorySessionsList, MemorySessionsListRequest{})
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var resp MemorySessionsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range resp.Sessions {
		out[d.SessionID] = d.Title
	}
	return out
}

func TestMemorySessionsListReturnsTheTitle(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	var named, unnamed MemorySessionResponse
	_ = json.Unmarshal(doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "u", Title: "Onboarding: week 1"}).Body.Bytes(), &named)
	_ = json.Unmarshal(doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "u"}).Body.Bytes(), &unnamed)

	titles := listedTitles(t, s)
	if titles[named.SessionID] != "Onboarding: week 1" {
		t.Errorf("named session listed with title %q", titles[named.SessionID])
	}
	if titles[unnamed.SessionID] != unnamed.Title || !strings.HasPrefix(unnamed.Title, "Session ") {
		t.Errorf("default title: created %q, listed %q", unnamed.Title, titles[unnamed.SessionID])
	}
}

// A session written before the title was stored in its metadata still lists
// with the title its body carries.
func TestMemorySessionsListReadsTheTitleOfAnOlderSession(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	meta := map[string][]string{"userId": {"u"}, "type": {"session"}, "status": {"active"}, "scenario": {""}}
	addTestDoc(t, s, memorySessionsCollection, "old-session", "en", "# Session: Legacy title\n\nUser: u\nScenario: \nCreated: 2026-01-01T00:00:00Z", meta)
	addTestDoc(t, s, memorySessionsCollection, "no-heading", "en", "free text", meta)

	titles := listedTitles(t, s)
	if titles["old-session"] != "Legacy title" {
		t.Errorf("older session listed with title %q", titles["old-session"])
	}
	if titles["no-heading"] != "" {
		t.Errorf("a body without the heading gave title %q", titles["no-heading"])
	}
}

// The MCP listing returns the same title as REST.
func TestMCPMemoryListSessionsReturnsTheTitle(t *testing.T) {
	client, _, cleanup := directClientServer(t)
	defer cleanup()
	tools := &MCPToolServer{client: client}

	if _, err := tools.toolMemoryStartSession(context.Background(), map[string]interface{}{"user_id": "u", "title": "Billing"}); err != nil {
		t.Fatal(err)
	}
	out, err := tools.toolMemoryListSessions(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(resp.Sessions) != 1 || resp.Sessions[0]["title"] != "Billing" {
		t.Errorf("MCP listing: %s", out)
	}
}
