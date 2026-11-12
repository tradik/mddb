package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// MCP-005: the minimal MCP client against a Streamable HTTP server.

func TestChatMcpHTTPClientRefusesPlainRedirects(t *testing.T) {
	c := chatMcpHTTPClient(3 * time.Second)
	plain, _ := http.NewRequest(http.MethodGet, "http://example.com/mcp", nil)
	if err := c.CheckRedirect(plain, nil); err == nil || c.Timeout != 3*time.Second {
		t.Errorf("a redirect to http was followed (timeout %v)", c.Timeout)
	}
}

func TestMcpRemoteClientSession(t *testing.T) {
	f := newMcpFixture(t)
	for _, sse := range []bool{false, true} {
		f.sse.Store(sse)
		c := newMcpRemoteClient(f.URL, nil, time.Second)
		ctx := context.Background()
		info, err := c.initialize(ctx)
		if err != nil || info.ServerInfo.Name != "wiki" || c.session != "sess-1" || c.protocol != "2025-11-25" {
			t.Fatalf("sse=%v initialize: %+v %v", sse, info, err)
		}
		tools, err := c.listTools(ctx)
		if err != nil || len(tools) != 10 || tools[0].Title != "Search" {
			t.Fatalf("sse=%v tools: %d %v", sse, len(tools), err)
		}
		for name, want := range map[string]string{"search": "found wiki", "mixed": "[image content omitted]\nresource text",
			"structured": `{"price":42}`, "failing": "no such page"} {
			text, isError, err := c.callTool(ctx, name, map[string]any{"q": "wiki"})
			if err != nil || text != want || isError != (name == "failing") {
				t.Errorf("sse=%v %s: %q %v %v", sse, name, text, isError, err)
			}
		}
		if text, _, err := c.callTool(ctx, "search", nil); err != nil || text != "found <nil>" {
			t.Errorf("no arguments: %q %v", text, err)
		}
	}
}

func TestMcpRemoteToolRefusal(t *testing.T) {
	yes, no := true, false
	for _, c := range []struct {
		a    mcpRemoteAnnotations
		want string
	}{
		{mcpRemoteAnnotations{ReadOnlyHint: &yes}, ""},
		{mcpRemoteAnnotations{ReadOnlyHint: &yes, DestructiveHint: &no}, ""},
		{mcpRemoteAnnotations{ReadOnlyHint: &yes, DestructiveHint: &yes}, "it is marked destructiveHint: true"},
		{mcpRemoteAnnotations{ReadOnlyHint: &no}, "it is not marked readOnlyHint: true"},
		{mcpRemoteAnnotations{}, "it is not marked readOnlyHint: true"},
	} {
		if got := (mcpRemoteTool{Annotations: c.a}).refusal(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.a, got, c.want)
		}
	}
}

func TestMcpRemoteClientCredentialAndRedaction(t *testing.T) {
	f := newMcpFixture(t)
	f.token = "s3cr3t-token"
	if _, err := newMcpRemoteClient(f.URL, nil, time.Second).initialize(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "401") {
		t.Fatalf("no credential: %v", err)
	}
	c := newMcpRemoteClient(f.URL, http.Header{"Authorization": {"Bearer s3cr3t-token"}}, time.Second, "s3cr3t-token")
	if _, err := c.initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, err := c.callTool(context.Background(), "boom", nil)
	if err == nil || strings.Contains(err.Error(), "s3cr3t") || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("the server's error quoting the token: %v", err)
	}
	if c.redact(nil) != nil || c.redact(errors.New("plain")).Error() != "plain" {
		t.Error("redact changed what holds no secret")
	}
	if (&mcpRemoteClient{}).redact(errors.New("x")).Error() != "x" {
		t.Error("redact without a secret")
	}
}

// edgeServer answers every request with handler.
func edgeServer(t *testing.T, handler http.HandlerFunc) *mcpRemoteClient {
	t.Helper()
	f := newMcpFixture(t)
	f.Config.Handler = handler
	return newMcpRemoteClient(f.URL, nil, time.Second)
}

func TestMcpRemoteClientFailures(t *testing.T) {
	ctx := context.Background()
	reply := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(readAllString(r.Body), "notifications/") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = io.WriteString(w, body)
		}
	}
	for want, body := range map[string]string{
		"not a JSON-RPC answer":         "<html>",
		"an unreadable result":          `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":7}}`,
		"refused (code -32600)":         `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"refused"}}`,
		"notifications/initialized: th": `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25"}}`,
	} {
		if _, err := edgeServer(t, reply(body)).initialize(ctx); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	stream := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"jsonrpc\":\"2.0\",\"id\":99,\"result\":{}}\n\n")
	}
	if _, err := edgeServer(t, stream).initialize(ctx); err == nil || !strings.Contains(err.Error(), "ended without an answer") {
		t.Errorf("a stream without the answer: %v", err)
	}
	huge := func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat(" ", chatMcpMaxResponseBytes+1))
	}
	if _, err := edgeServer(t, huge).initialize(ctx); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("a huge answer: %v", err)
	}
	endless := func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"tools":[],"nextCursor":"again"}}`)
	}
	if _, err := edgeServer(t, endless).listTools(ctx); err == nil || !strings.Contains(err.Error(), "more than 20 pages") {
		t.Errorf("endless pages: %v", err)
	}
	for _, method := range []string{"tools/list", "tools/call"} {
		c := edgeServer(t, reply("<html>"))
		var err error
		if method == "tools/list" {
			_, err = c.listTools(ctx)
		} else {
			_, _, err = c.callTool(ctx, "x", nil)
		}
		if err == nil || !strings.HasPrefix(err.Error(), method) {
			t.Errorf("%s: %v", method, err)
		}
	}

	// Nothing reaches a URL the guard refuses.
	c := newMcpRemoteClient("ftp://example.com/mcp", nil, time.Second)
	if _, err := c.initialize(ctx); err == nil || !strings.HasPrefix(err.Error(), "initialize: ") {
		t.Errorf("initialize: %v", err)
	}
	if err := c.notify(ctx, "notifications/initialized"); err == nil {
		t.Error("notify through the guard")
	}
}

func TestReadJSONRPCReplyBodyError(t *testing.T) {
	resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(failingReader{})}
	if _, err := readJSONRPCReply(resp, "1"); err == nil {
		t.Error("a failing body was read")
	}
}

// readAllString reads a body to a string.
func readAllString(r io.Reader) string {
	b, _ := io.ReadAll(r)
	return string(b)
}
