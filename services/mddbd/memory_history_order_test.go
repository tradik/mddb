package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	json "mddb/internal/jsonx"
	"mddb/internal/storage"
)

// /v1/memory/history is documented as chronological. Messages written within
// one second shared their only ordering key, so they came back in the order
// of their random ids — a conversation replayed out of order.

const historyBurst = 12

func writeHistoryBurst(t *testing.T, s *Server) string {
	t.Helper()
	var sess MemorySessionResponse
	_ = json.Unmarshal(doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "u"}).Body.Bytes(), &sess)
	for i := range historyBurst {
		rec := doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{SessionID: sess.SessionID, Role: "user", Content: fmt.Sprintf("m%02d", i)})
		if rec.Code != http.StatusOK {
			t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
		}
	}
	return sess.SessionID
}

func TestMemoryHistoryKeepsTheOrderOfMessagesWrittenInOneSecond(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	sid := writeHistoryBurst(t, s)

	rec := doRequest(t, s.handleMemoryHistory, MemoryHistoryRequest{SessionID: sid})
	var resp MemoryHistoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Messages) != historyBurst {
		t.Fatalf("%d messages, want %d", len(resp.Messages), historyBurst)
	}
	for i, m := range resp.Messages {
		if want := fmt.Sprintf("m%02d", i); m.ContentMD != want {
			t.Fatalf("position %d holds %s, want %s", i, m.ContentMD, want)
		}
	}
}

// The MCP tool answers in the same order as REST.
func TestMCPMemorySessionHistoryKeepsTheOrder(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	sid := writeHistoryBurst(t, s)
	tools := &MCPToolServer{client: NewDirectClient(s)}
	if _, err := tools.toolMemoryAddMessage(context.Background(), map[string]interface{}{"session_id": sid, "role": "assistant", "content": "last"}); err != nil {
		t.Fatal(err)
	}

	out, err := tools.toolMemorySessionHistory(context.Background(), map[string]interface{}{"session_id": sid, "limit": 5, "offset": 9})
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Total    int `json:"total"`
		Messages []struct {
			Content string `json:"content"`
			Role    string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got := []string{}
	for _, m := range resp.Messages {
		got = append(got, m.Content)
	}
	if resp.Total != historyBurst+1 || fmt.Sprint(got) != "[m09 m10 m11 last]" || resp.Messages[3].Role != "assistant" {
		t.Errorf("MCP history page: total %d, %v", resp.Total, got)
	}
}

// A session with no messages answers an empty list, not null.
func TestMCPMemorySessionHistoryOfAnEmptySession(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	tools := &MCPToolServer{client: NewDirectClient(s)}
	out, err := tools.toolMemorySessionHistory(context.Background(), map[string]interface{}{"session_id": "nothing-here"})
	if err != nil {
		t.Fatal(err)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if msgs, ok := resp["messages"].([]any); !ok || len(msgs) != 0 || resp["total"] != float64(0) {
		t.Errorf("empty session: %s", out)
	}
}

// Keys written before 2.19.0 carry whole seconds; they order with the
// nanosecond keys written since, and a key without a timestamp falls back to
// addedAt, then to the id.
func TestMemoryMessageOrderAcrossKeyFormats(t *testing.T) {
	msgs := []storage.Doc{
		{ID: "e", Key: "s/0000001900000000000000000-x", AddedAt: 1},     // 1.9e18 ns, oversized but numeric
		{ID: "d", Key: "s/free-form", AddedAt: 1800000001},              // no timestamp: addedAt
		{ID: "c", Key: "s/1800000000500000000-z", AddedAt: 1800000000},  // nanoseconds
		{ID: "b", Key: "s/00000000001800000000-y", AddedAt: 1800000000}, // seconds, padded (REST before 2.19.0)
		{ID: "a", Key: "s/1800000000-w", AddedAt: 1800000000},           // seconds (MCP before 2.19.0)
	}
	sortMessagesChronologically(msgs)
	got := ""
	for _, m := range msgs {
		got += m.ID
	}
	if got != "abcde" {
		t.Errorf("order %s, want abcde", got)
	}
}
