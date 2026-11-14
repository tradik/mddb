package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	json "mddb/internal/jsonx"
)

// mcpFixture is a foreign MCP server for the MCP-005 tests: Streamable HTTP
// over TLS, a session id, two pages of tools, and tools that answer, fail,
// stall or flood.
type mcpFixture struct {
	*httptest.Server
	sse atomic.Bool // answer as an event stream
	// token is the bearer token or header value it wants; "" for none. Tests
	// change it while the server's goroutines may still read it, so it is
	// atomic: use setToken and wantToken.
	token    atomic.Pointer[string]
	header   string       // the header carrying token; "" for Authorization: Bearer
	calls    atomic.Int32 // tools/call requests
	searchRO atomic.Bool  // "search" is read-only; false turns it into a writer
	failList atomic.Bool  // tools/list answers with an error
	// seen, when set, sees every request first (#414).
	seen atomic.Pointer[func(r *http.Request)]
}

var mcpFixtureTrue, mcpFixtureFalse = true, false

// mcpFixtureTools are its tools, two pages.
func (f *mcpFixture) tools(page string) ([]mcpRemoteTool, string) {
	ro := func(name string) mcpRemoteTool {
		return mcpRemoteTool{Name: name, Annotations: mcpRemoteAnnotations{ReadOnlyHint: &mcpFixtureTrue}}
	}
	if page == "" {
		search := ro("search")
		search.Title, search.Description = "Search", "Search the wiki"
		if !f.searchRO.Load() {
			search.Annotations.ReadOnlyHint = &mcpFixtureFalse
		}
		return []mcpRemoteTool{search, ro("slow"), ro("big"), ro("boom"), ro("mixed"), ro("structured"), ro("failing")}, "p2"
	}
	wipe := ro("wipe")
	wipe.Annotations.DestructiveHint = &mcpFixtureTrue
	return []mcpRemoteTool{wipe, {Name: "write_doc", Annotations: mcpRemoteAnnotations{ReadOnlyHint: &mcpFixtureFalse}}, {Name: "plain"}}, ""
}

// callResult answers tools/call.
func (f *mcpFixture) callResult(name string, args map[string]any) (any, *jsonRPCError) {
	f.calls.Add(1)
	text := func(s string) map[string]any {
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": s}}}
	}
	switch name {
	case "slow":
		time.Sleep(400 * time.Millisecond)
		return text("late"), nil
	case "big":
		return text(strings.Repeat("ż", 5000)), nil
	case "boom":
		return nil, &jsonRPCError{Code: -32000, Message: "exploded with " + f.wantToken()}
	case "mixed":
		return map[string]any{"content": []any{map[string]any{"type": "image", "data": "AA=="},
			map[string]any{"type": "resource", "resource": map[string]any{"uri": "wiki://a", "text": "resource text"}}}}, nil
	case "structured":
		return map[string]any{"content": []any{}, "structuredContent": map[string]any{"price": 42}}, nil
	case "failing":
		r := text("no such page")
		r["isError"] = true
		return r, nil
	}
	return text(fmt.Sprintf("found %v", args["q"])), nil
}

// setToken sets the credential the fixture demands from now on.
func (f *mcpFixture) setToken(token string) { f.token.Store(&token) }

// wantToken is the credential the fixture demands; "" for none.
func (f *mcpFixture) wantToken() string {
	if t := f.token.Load(); t != nil {
		return *t
	}
	return ""
}

func (f *mcpFixture) authorized(r *http.Request) bool {
	token := f.wantToken()
	switch {
	case token == "":
		return true
	case f.header == "":
		return r.Header.Get("Authorization") == "Bearer "+token
	}
	return r.Header.Get(f.header) == token
}

func (f *mcpFixture) serve(w http.ResponseWriter, r *http.Request) {
	if seen := f.seen.Load(); seen != nil {
		(*seen)(r)
	}
	if !f.authorized(r) {
		http.Error(w, "who are you", http.StatusUnauthorized)
		return
	}
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Cursor    string         `json:"cursor"`
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	body, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(body, &msg)
	if msg.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != "sess-1" {
		http.Error(w, "no session", http.StatusBadRequest)
		return
	}
	var result any
	var rpcErr *jsonRPCError
	switch msg.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "sess-1")
		result = map[string]any{"protocolVersion": "2025-11-25", "serverInfo": map[string]any{"name": "wiki", "version": "1.2"}}
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
		return
	case "tools/list":
		tools, next := f.tools(msg.Params.Cursor)
		result = map[string]any{"tools": tools, "nextCursor": next}
		if f.failList.Load() {
			result, rpcErr = nil, &jsonRPCError{Code: -32603, Message: "index offline"}
		}
	case "tools/call":
		result, rpcErr = f.callResult(msg.Params.Name, msg.Params.Arguments)
	}
	reply, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result, "error": rpcErr})
	if f.sse.Load() {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: message\r\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\r\n\r\ndata: %s\n\n", reply)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(reply)
}

// newMcpFixture starts the server, points the MCP client at it and lets
// outbound requests reach loopback.
func newMcpFixture(t *testing.T) *mcpFixture {
	t.Helper()
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "true")
	f := &mcpFixture{}
	f.searchRO.Store(true)
	f.Server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	prevClient := chatMcpHTTPClient
	chatMcpHTTPClient = func(timeout time.Duration) *http.Client {
		c := f.Client()
		c.Timeout = timeout
		c.CheckRedirect = httpsOnlyRedirect(func(*http.Request, []*http.Request) error { return nil })
		return c
	}
	t.Cleanup(func() { chatMcpHTTPClient = prevClient })
	return f
}

// setFeature switches a GO-049 module of s, giving s a feature store first.
func setFeature(t *testing.T, s *Server, name string, on bool) {
	t.Helper()
	if s.Features == nil {
		s.Features = NewFeatureManager(s.DB)
		if err := s.Features.LoadAll(); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Features.Set(name, on, "test"); err != nil {
		t.Fatal(err)
	}
}

// externalMcpOn turns the chatExternalMcp module of s on.
func externalMcpOn(t *testing.T, s *Server) { setFeature(t, s, FeatureChatExternalMCP, true) }
