package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// MCP-004 over Streamable HTTP, handshake era: the destructive tools/call's
// POST turns into an SSE stream carrying elicitation/create, the client
// answers with a POST of its own, and the result ends the stream.

type streamableTestClient struct {
	t       *testing.T
	url     string
	session string
}

func startStreamableClient(t *testing.T, s *Server, elicit bool) *streamableTestClient {
	t.Helper()
	h := NewMCPHandlerWithConfig(NewDirectClient(s), nil, MCPServerInfo{}, "", ModeRW, "")
	srv := httptest.NewServer(http.HandlerFunc(NewMCPStreamableTransport(h).Handle))
	t.Cleanup(srv.Close)
	c := &streamableTestClient{t: t, url: srv.URL}
	resp := c.post(legacyInitialize(elicit))
	defer func() { _ = resp.Body.Close() }()
	c.session = resp.Header.Get("MCP-Session-Id")
	if c.session == "" {
		t.Fatal("initialize minted no session")
	}
	return c
}

func (c *streamableTestClient) post(msg map[string]interface{}) *http.Response {
	c.t.Helper()
	body, _ := json.Marshal(msg)
	req, _ := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.session != "" {
		req.Header.Set("MCP-Session-Id", c.session)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp
}

// readEvent reads the next SSE message off a stream.
func readEvent(t *testing.T, r *bufio.Reader) map[string]interface{} {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended: %v", err)
		}
		if data, ok := strings.CutPrefix(strings.TrimSpace(line), "data: "); ok {
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(data), &m); err != nil {
				t.Fatalf("event is not JSON: %q", data)
			}
			return m
		}
	}
}

// The acceptance path over Streamable HTTP: refusal keeps the collection,
// confirmation deletes it.
func TestStreamableDeleteCollectionElicitation(t *testing.T) {
	t.Setenv(mcpConfirmEnv, "require")
	s, cleanup := newTestServer(t)
	defer cleanup()
	seedDoomed(t, s)
	c := startStreamableClient(t, s, true)

	for i, answer := range []map[string]interface{}{
		{"action": "decline"},
		accept(map[string]interface{}{"confirm": true, "collection": "doomed"}),
	} {
		id := 20 + i
		resp := c.post(legacyDeleteCollection(id, "doomed"))
		if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
			t.Fatalf("content type = %q", ct)
		}
		stream := bufio.NewReader(resp.Body)
		ask := readEvent(t, stream)
		if ask["method"] != "elicitation/create" {
			t.Fatalf("expected a question, got %v", ask)
		}
		ack := c.post(map[string]interface{}{"jsonrpc": "2.0", "id": ask["id"], "result": answer})
		_ = ack.Body.Close()
		if ack.StatusCode != http.StatusAccepted {
			t.Errorf("answer POST = %d", ack.StatusCode)
		}
		res := readEvent(t, stream)
		_ = resp.Body.Close()
		deleted := i == 1
		if res["id"] != float64(id) || isErrorResult(res) == deleted {
			t.Fatalf("answer %d: result = %v", i, res)
		}
		if s.collectionExists("doomed") == deleted {
			t.Fatalf("answer %d: collection exists = %v", i, !deleted)
		}
	}
}

// A tool that is not gated never opens a stream, even for a session that can
// be asked: the response stays the JSON it always was.
func TestStreamableUngatedToolStaysJSON(t *testing.T) {
	t.Setenv(mcpConfirmEnv, "require")
	s, cleanup := newTestServer(t)
	defer cleanup()
	c := startStreamableClient(t, s, true)
	resp := c.post(map[string]interface{}{"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]interface{}{"name": "get_stats", "arguments": map[string]interface{}{}}})
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type = %q", ct)
	}
}

func TestStreamableWithoutElicitation(t *testing.T) {
	t.Setenv(mcpConfirmEnv, "require")
	s, cleanup := newTestServer(t)
	defer cleanup()
	seedDoomed(t, s)
	c := startStreamableClient(t, s, false)

	resp := c.post(legacyDeleteCollection(2, "doomed"))
	defer func() { _ = resp.Body.Close() }()
	var res map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if e, _ := res["error"].(map[string]interface{}); e == nil || e["code"] != float64(mcpErrMissingClientCapability) {
		t.Fatalf("require without elicitation = %v", res)
	}
	if !s.collectionExists("doomed") {
		t.Fatal("a refused call deleted the collection")
	}
}

// Ending a session forgets that it could be asked.
func TestStreamableDeleteForgetsElicitation(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	tr := NewMCPStreamableTransport(NewMCPHandlerWithConfig(NewDirectClient(s), nil, MCPServerInfo{}, "", ModeRW, ""))
	tr.elicit.add("sess")
	req := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	req.Header.Set("MCP-Session-Id", "sess")
	tr.Handle(httptest.NewRecorder(), req)
	if tr.elicit.has("sess") {
		t.Error("an ended session can still be asked")
	}
}
