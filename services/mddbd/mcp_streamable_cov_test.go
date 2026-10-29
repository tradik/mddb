package main

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The Streamable HTTP transport's edges: refused versions, oversized bodies,
// writers that cannot stream, and the handshake era's standalone GET stream.

func newCovStreamable() *MCPStreamableTransport {
	return NewMCPStreamableTransport(NewMCPHandlerWithConfig(covDHealthClient{}, nil, MCPServerInfo{}, "", ModeRead, ""))
}

func TestStreamableRefusesUnknownVersionOnGetAndDelete(t *testing.T) {
	tr := newCovStreamable()
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		req := httptest.NewRequest(m, "/mcp", nil)
		req.Header.Set(mcpHeaderProtocolVersion, "2001-01-01")
		rec := httptest.NewRecorder()
		tr.Handle(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s with an unknown revision = %d", m, rec.Code)
		}
	}
}

func TestStreamableRejectsOversizedBody(t *testing.T) {
	rec := httptest.NewRecorder()
	newCovStreamable().Handle(rec, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(strings.Repeat("x", 5<<20))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized POST = %d", rec.Code)
	}
}

func TestStreamableWritersThatCannotStream(t *testing.T) {
	tr := newCovStreamable()

	w := &noFlushWriter{h: http.Header{}}
	tr.handleListen(w, map[string]interface{}{"id": 1})
	if !strings.Contains(w.h.Get("Content-Type"), "text/plain") {
		t.Errorf("listen without a flusher answered %q", w.h.Get("Content-Type"))
	}

	w = &noFlushWriter{h: http.Header{}}
	tr.handleGet(w, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if !strings.Contains(w.h.Get("Content-Type"), "text/plain") {
		t.Errorf("GET without a flusher answered %q", w.h.Get("Content-Type"))
	}

	// Unencodable messages write nothing.
	rec := httptest.NewRecorder()
	writeSSEMessage(rec, rec, map[string]interface{}{"bad": make(chan int)})
	writeMCPErrorResponse(rec, make(chan int), mcpError(mcpErrInvalidParams, "x"))
	if strings.Contains(rec.Body.String(), "data:") || rec.Body.Len() != 0 {
		t.Errorf("unencodable messages wrote %q", rec.Body.String())
	}
}

func TestStreamableGetRequiresSession(t *testing.T) {
	rec := httptest.NewRecorder()
	newCovStreamable().Handle(rec, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("GET without a session = %d", rec.Code)
	}
}

// The standalone stream delivers notifications, counts as a session while
// open, and ends both when the client goes away and when the session is
// deleted.
func TestStreamableGetStream(t *testing.T) {
	tr := newCovStreamable()
	srv := httptest.NewServer(http.HandlerFunc(tr.Handle))
	defer srv.Close()

	open := func(id string, ctx context.Context) *http.Response {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		req.Header.Set("MCP-Session-Id", id)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	waitSessions := func(n int) {
		deadline := time.Now().Add(5 * time.Second)
		for tr.SessionCount() != n {
			if time.Now().After(deadline) {
				t.Fatalf("sessions = %d, want %d", tr.SessionCount(), n)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	resp := open("s1", ctx)
	waitSessions(1)
	tr.SendNotification("s1", map[string]interface{}{"method": "notifications/message"})
	tr.SendNotification("s1", map[string]interface{}{"bad": make(chan int)}) // dropped
	tr.SendNotification("nobody", map[string]interface{}{})                  // dropped
	stream := bufio.NewReader(resp.Body)
	line, err := stream.ReadString('\n')
	for err == nil && !strings.HasPrefix(line, "data:") {
		line, err = stream.ReadString('\n')
	}
	if !strings.Contains(line, "notifications/message") {
		t.Errorf("stream line = %q, %v", line, err)
	}
	cancel()
	_ = resp.Body.Close()
	waitSessions(0)

	resp = open("s2", context.Background())
	waitSessions(1)
	del, _ := http.NewRequest(http.MethodDelete, srv.URL, nil)
	del.Header.Set("MCP-Session-Id", "s2")
	dr, err := http.DefaultClient.Do(del)
	if err != nil {
		t.Fatal(err)
	}
	_ = dr.Body.Close()
	waitSessions(0)
	_ = resp.Body.Close()
}

func TestStreamableSendNotificationFullOrDone(t *testing.T) {
	tr := newCovStreamable()
	full := &streamableSession{ch: make(chan []byte), done: make(chan struct{})}
	tr.sessions["full"] = full
	tr.SendNotification("full", map[string]interface{}{}) // no reader: dropped, not blocked

	done := &streamableSession{ch: make(chan []byte), done: make(chan struct{})}
	close(done.done)
	tr.sessions["done"] = done
	tr.SendNotification("done", map[string]interface{}{}) // stream ending: not blocked
}
