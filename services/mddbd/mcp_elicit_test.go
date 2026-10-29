package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// MCP-004 in the handshake era: the server sends elicitation/create mid-call
// and waits for the client's response. Driven end to end with a test client
// over stdio here, and over Streamable HTTP in mcp_streamable_elicit_test.go.

// stdioTestClient is the client end of a serveMCPStdio loop.
type stdioTestClient struct {
	t   *testing.T
	in  *io.PipeWriter
	out *bufio.Reader
}

func startStdioClient(t *testing.T, s *Server) *stdioTestClient {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan struct{})
	go func() {
		s.serveMCPStdio(inR, outW)
		_ = outW.Close()
		close(done)
	}()
	t.Cleanup(func() {
		_ = inW.Close()
		go func() { _, _ = io.Copy(io.Discard, outR) }()
		<-done
	})
	return &stdioTestClient{t: t, in: inW, out: bufio.NewReader(outR)}
}

func (c *stdioTestClient) send(msg map[string]interface{}) {
	c.t.Helper()
	data, _ := json.Marshal(msg)
	if _, err := c.in.Write(append(data, '\n')); err != nil {
		c.t.Fatal(err)
	}
}

// next reads the next message that is not a notification.
func (c *stdioTestClient) next() map[string]interface{} {
	c.t.Helper()
	for {
		line, err := c.out.ReadBytes('\n')
		if err != nil {
			c.t.Fatalf("reading stdout: %v", err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(line, &m); err != nil {
			c.t.Fatalf("stdout line is not JSON: %q", line)
		}
		if _, isNote := m["method"]; isNote && m["id"] == nil {
			continue
		}
		return m
	}
}

func legacyInitialize(elicit bool) map[string]interface{} {
	caps := map[string]interface{}{}
	if elicit {
		caps["elicitation"] = map[string]interface{}{}
	}
	return map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]interface{}{"protocolVersion": "2025-11-25", "capabilities": caps}}
}

func legacyDeleteCollection(id int, name string) map[string]interface{} {
	return map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]interface{}{"name": "delete_collection", "arguments": map[string]interface{}{"collection": name}}}
}

func seedDoomed(t *testing.T, s *Server) {
	t.Helper()
	h := NewMCPHandlerWithConfig(NewDirectClient(s), nil, MCPServerInfo{}, "", ModeRW, "")
	h.Handle(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": "add_document", "arguments": map[string]interface{}{
			"collection": "doomed", "key": "k1", "lang": "en", "content_md": "# hello",
		}}})
	if !s.collectionExists("doomed") {
		t.Fatal("seeding failed")
	}
}

func isErrorResult(m map[string]interface{}) bool {
	r, _ := m["result"].(map[string]interface{})
	return r == nil || r["isError"] == true
}

// The acceptance path over stdio: refusal keeps the collection, an error
// answer keeps it, confirmation deletes it.
func TestStdioDeleteCollectionElicitation(t *testing.T) {
	t.Setenv(mcpConfirmEnv, "elicit")
	s, cleanup := newTestServer(t)
	defer cleanup()
	seedDoomed(t, s)
	c := startStdioClient(t, s)

	c.send(legacyInitialize(true))
	if r := c.next(); r["id"] != float64(1) {
		t.Fatalf("initialize = %v", r)
	}

	answers := []struct {
		reply   map[string]interface{}
		deleted bool
	}{
		{map[string]interface{}{"result": map[string]interface{}{"action": "decline"}}, false},
		{map[string]interface{}{"error": map[string]interface{}{"code": -32603, "message": "no UI"}}, false},
		{map[string]interface{}{"result": accept(map[string]interface{}{"confirm": true, "collection": "doomed"})}, true},
	}
	for i, a := range answers {
		id := 10 + i
		c.send(legacyDeleteCollection(id, "doomed"))
		ask := c.next()
		if ask["method"] != "elicitation/create" {
			t.Fatalf("expected a question, got %v", ask)
		}
		reply := map[string]interface{}{"jsonrpc": "2.0", "id": ask["id"]}
		for k, v := range a.reply {
			reply[k] = v
		}
		// A response nobody asked for is dropped, not answered.
		c.send(map[string]interface{}{"jsonrpc": "2.0", "id": "stray", "result": map[string]interface{}{}})
		c.send(reply)
		res := c.next()
		if res["id"] != float64(id) || isErrorResult(res) == a.deleted {
			t.Fatalf("answer %d: result = %v", i, res)
		}
		if s.collectionExists("doomed") == a.deleted {
			t.Fatalf("answer %d: collection exists = %v", i, !a.deleted)
		}
	}
}

func TestStdioWithoutElicitation(t *testing.T) {
	t.Setenv(mcpConfirmEnv, "require")
	s, cleanup := newTestServer(t)
	defer cleanup()
	seedDoomed(t, s)
	c := startStdioClient(t, s)

	c.send(legacyInitialize(false))
	c.next()
	c.send(legacyDeleteCollection(2, "doomed"))
	res := c.next()
	if e, _ := res["error"].(map[string]interface{}); e == nil || e["code"] != float64(mcpErrMissingClientCapability) {
		t.Fatalf("require without elicitation = %v", res)
	}
	if !s.collectionExists("doomed") {
		t.Fatal("a refused call deleted the collection")
	}
}

// Input ending while a question is open refuses the call: nobody is left to
// answer it.
func TestStdioClientGoneWhileAsked(t *testing.T) {
	t.Setenv(mcpConfirmEnv, "elicit")
	s, cleanup := newTestServer(t)
	defer cleanup()
	seedDoomed(t, s)
	c := startStdioClient(t, s)

	c.send(legacyInitialize(true))
	c.next()
	c.send(legacyDeleteCollection(2, "doomed"))
	if ask := c.next(); ask["method"] != "elicitation/create" {
		t.Fatalf("expected a question, got %v", ask)
	}
	_ = c.in.Close()
	res := c.next()
	if !isErrorResult(res) || !strings.Contains(res["result"].(map[string]interface{})["content"].([]interface{})[0].(map[string]interface{})["text"].(string), "went away") {
		t.Fatalf("result = %v", res)
	}
	if !s.collectionExists("doomed") {
		t.Fatal("collection deleted without an answer")
	}
}

func TestMCPIsResponse(t *testing.T) {
	cases := map[string]bool{
		`{"id":1,"result":{}}`:              true,
		`{"id":"x","error":{}}`:             true,
		`{"id":1,"method":"ping"}`:          false,
		`{"method":"notifications/cancel"}`: false,
		`{"id":1}`:                          false,
	}
	for in, want := range cases {
		var m map[string]interface{}
		_ = json.Unmarshal([]byte(in), &m)
		if got := mcpIsResponse(m); got != want {
			t.Errorf("%s -> %v", in, got)
		}
	}
}

func TestDeclaresElicitation(t *testing.T) {
	if mcpDeclaresElicitation(nil) || mcpDeclaresElicitation(map[string]interface{}{"elicitation": true}) {
		t.Error("only an elicitation object declares it")
	}
	if !mcpDeclaresElicitation(map[string]interface{}{"elicitation": map[string]interface{}{"form": map[string]interface{}{}}}) {
		t.Error("an elicitation object declares it")
	}
}

func TestRequestConfirmerFailures(t *testing.T) {
	q := newMCPConfirmRequest("delete_where", map[string]interface{}{"collection": "c"})

	broken := &mcpRequestConfirmer{send: func(map[string]interface{}) error { return errMCPNoStream }, pending: newMCPPendingRequests()}
	if _, err := broken.confirm(context.Background(), q); err == nil || !errors.Is(err, errMCPNoStream) {
		t.Errorf("send failure = %v", err)
	}

	silent := &mcpRequestConfirmer{send: func(map[string]interface{}) error { return nil }, pending: newMCPPendingRequests()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := silent.confirm(ctx, q); err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled = %v", err)
	}

	old := mcpConfirmTimeout
	mcpConfirmTimeout = 10 * time.Millisecond
	defer func() { mcpConfirmTimeout = old }()
	if _, err := silent.confirm(context.Background(), q); err == nil || !strings.Contains(err.Error(), "within") {
		t.Errorf("timeout = %v", err)
	}
	if len(silent.pending.waiting) != 0 {
		t.Error("a finished wait was not forgotten")
	}

	closed := newMCPPendingRequests()
	closed.closeAll()
	gone := &mcpRequestConfirmer{send: func(map[string]interface{}) error { return nil }, pending: closed}
	if _, err := gone.confirm(context.Background(), q); err == nil || !strings.Contains(err.Error(), "went away") {
		t.Errorf("closed registry = %v", err)
	}
}

// failingWriter accepts nothing, so a response line cannot be written.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestStdioSender(t *testing.T) {
	if err := mcpStdioSender(failingWriter{})(map[string]interface{}{"id": 1}); err == nil {
		t.Error("a failed write must be reported")
	}
	if err := mcpStdioSender(io.Discard)(map[string]interface{}{"bad": make(chan int)}); err == nil {
		t.Error("an unencodable message must be reported")
	}
}

// noFlushWriter is a ResponseWriter that cannot stream.
type noFlushWriter struct{ h http.Header }

func (w *noFlushWriter) Header() http.Header         { return w.h }
func (w *noFlushWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *noFlushWriter) WriteHeader(int)             {}

func TestPostStreamWithoutFlusher(t *testing.T) {
	p := &mcpPostStream{w: &noFlushWriter{h: http.Header{}}}
	if err := p.send(map[string]interface{}{}); !errors.Is(err, errMCPNoStream) || p.started {
		t.Errorf("send = %v, started %v", err, p.started)
	}
}

func TestElicitSessionsBound(t *testing.T) {
	var s mcpElicitSessions
	for i := 0; i <= mcpElicitSessionsMax; i++ {
		s.add("session-" + strconv.Itoa(i))
	}
	if len(s.ids) != mcpElicitSessionsMax {
		t.Errorf("sessions = %d, want the bound %d", len(s.ids), mcpElicitSessionsMax)
	}
	s.add("kept")
	s.remove("kept")
	if s.has("kept") {
		t.Error("a removed session is still remembered")
	}
}
