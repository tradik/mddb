package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	json "mddb/internal/jsonx"

	"mddb/internal/httpclient"
)

// A minimal MCP client (MCP-005): JSON-RPC 2.0 over Streamable HTTP, enough
// to read a foreign server's tools and call a read-only one — initialize,
// notifications/initialized, tools/list, tools/call. Every request goes
// through the server's SSRF guard (sendOutbound) on a client whose redirects
// may not leave https. A server's answer is JSON or an event stream; both
// are read. The session id a server hands out is sent back on every request.
//
// The credential is a header of the client's; it is never logged, and an
// error that quotes it — a server echoing the header back — has it replaced.

// chatMcpProtocolVersion is the revision offered in initialize; the server's
// answer is the one used afterwards.
const chatMcpProtocolVersion = "2025-11-25"

// chatMcpMaxResponseBytes caps one answer: a tools/list of a large server
// fits, a runaway stream does not.
const chatMcpMaxResponseBytes = 4 << 20

// chatMcpHTTPClient is the pooled, SSRF-guarded client with redirects held
// to https. A test points it at its own TLS server.
var chatMcpHTTPClient = func(timeout time.Duration) *http.Client {
	c := httpclient.NewPooledClientWithTimeout(timeout)
	c.CheckRedirect = httpsOnlyRedirect(c.CheckRedirect)
	return c
}

// mcpRemoteClient talks to one server for one task.
type mcpRemoteClient struct {
	endpoint string
	auth     http.Header // the credential, when the server needs one
	secrets  []string    // its values, replaced in errors
	http     *http.Client
	session  string
	protocol string
	nextID   int
}

// newMcpRemoteClient builds a client; auth may be nil, and secrets are the
// values auth carries, replaced in errors.
func newMcpRemoteClient(endpoint string, auth http.Header, timeout time.Duration, secrets ...string) *mcpRemoteClient {
	return &mcpRemoteClient{endpoint: endpoint, auth: auth, secrets: secrets, http: chatMcpHTTPClient(timeout)}
}

// jsonRPCError is a JSON-RPC error object.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// jsonRPCReply is a JSON-RPC response; ID is raw so a number and a string
// both read.
type jsonRPCReply struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *jsonRPCError   `json:"error"`
}

// redact replaces the credentials in an error's text.
func (c *mcpRemoteClient) redact(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	for _, secret := range c.secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	if text == err.Error() {
		return err
	}
	return errors.New(text)
}

// post sends one JSON-RPC message and returns the answer, its body unread.
func (c *mcpRemoteClient) post(ctx context.Context, msg map[string]any) (*http.Response, error) {
	body, _ := json.Marshal(msg) // strings, numbers and decoded JSON: it encodes
	header := http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json, text/event-stream"},
		"User-Agent": {"mddbd/" + VERSION}}
	for k, v := range c.auth {
		header[k] = v
	}
	if c.session != "" {
		header.Set("Mcp-Session-Id", c.session)
	}
	if c.protocol != "" {
		header.Set("Mcp-Protocol-Version", c.protocol)
	}
	return sendOutbound(ctx, c.http, http.MethodPost, c.endpoint, body, header)
}

// call sends a request and decodes its result into out.
func (c *mcpRemoteClient) call(ctx context.Context, method string, params any, out any) error {
	c.nextID++
	id := c.nextID
	resp, err := c.post(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return c.redact(fmt.Errorf("%s: %w", method, err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: the server answered %s", method, resp.Status)
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.session = sid
	}
	reply, err := readJSONRPCReply(resp, fmt.Sprint(id))
	if err != nil {
		return c.redact(fmt.Errorf("%s: %w", method, err))
	}
	if reply.Error != nil {
		return c.redact(fmt.Errorf("%s: %s (code %d)", method, reply.Error.Message, reply.Error.Code))
	}
	if err := json.Unmarshal(reply.Result, out); err != nil {
		return fmt.Errorf("%s: an unreadable result: %w", method, err)
	}
	return nil
}

// readJSONRPCReply reads the response with the given id from a JSON body or
// from an event stream, where it may follow notifications.
func readJSONRPCReply(resp *http.Response, id string) (*jsonRPCReply, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, chatMcpMaxResponseBytes+1))
	switch {
	case err != nil:
		return nil, err
	case len(body) > chatMcpMaxResponseBytes:
		return nil, fmt.Errorf("the answer is larger than %d bytes", chatMcpMaxResponseBytes)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		var reply jsonRPCReply
		if err := json.Unmarshal(body, &reply); err != nil {
			return nil, fmt.Errorf("not a JSON-RPC answer: %w", err)
		}
		return &reply, nil
	}
	for _, event := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n\n") {
		var data []string
		for _, line := range strings.Split(event, "\n") {
			if rest, isData := strings.CutPrefix(line, "data:"); isData {
				data = append(data, strings.TrimPrefix(rest, " "))
			}
		}
		var reply jsonRPCReply
		if json.Unmarshal([]byte(strings.Join(data, "\n")), &reply) == nil && string(reply.ID) == id {
			return &reply, nil
		}
	}
	return nil, errors.New("the event stream ended without an answer")
}

// notify sends a notification; the server answers 202, or 200 or 204.
func (c *mcpRemoteClient) notify(ctx context.Context, method string) error {
	resp, err := c.post(ctx, map[string]any{"jsonrpc": "2.0", "method": method})
	if err != nil {
		return c.redact(fmt.Errorf("%s: %w", method, err))
	}
	_ = resp.Body.Close()
	if s := resp.StatusCode; s != http.StatusAccepted && s != http.StatusOK && s != http.StatusNoContent {
		return fmt.Errorf("%s: the server answered %s", method, resp.Status)
	}
	return nil
}
