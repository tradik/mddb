package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Streamable HTTP transport under MCP 2026-07-28 (MCP-001): mirrored
// headers that must agree with the body, no sessions, and HTTP statuses that
// let a dual-era client tell a modern refusal from a legacy endpoint.

// postModern sends a stateless request, filling in the mirrored headers unless
// a test overrides them.
func postModern(t *testing.T, tr *MCPStreamableTransport, body string,
	headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set(mcpHeaderProtocolVersion, MCPModernProtocolVersion)
	for name, value := range headers {
		if value == "" {
			req.Header.Del(name)
			continue
		}
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	tr.Handle(rec, req)
	return rec
}

// modernBody is a stateless request as JSON, with `_meta` already in place.
func modernBody(t *testing.T, method string, params map[string]interface{}) string {
	t.Helper()
	body, err := json.Marshal(modernRequest(method, params))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(body)
}

// decodeRPC reads a JSON-RPC response out of a recorder.
func decodeRPC(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	return resp
}

// rpcErrorCode returns the error code of a response, or 0 when it succeeded.
// JSON numbers decode as float64, which is why this is not a type assertion.
func rpcErrorCode(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	errObj, ok := decodeRPC(t, rec)["error"].(map[string]interface{})
	if !ok {
		return 0
	}
	code, _ := errObj["code"].(float64)
	return int(code)
}

func newModernTransport() *MCPStreamableTransport {
	return NewMCPStreamableTransport(newModernTestHandler())
}

// The headers exist so an intermediary can route without parsing the body.
// That is only safe while the two agree, so a server that reads the body has
// to check — otherwise a proxy can authorize one call and the server perform
// another.
func TestAStatelessPostMustMirrorItsHeaders(t *testing.T) {
	tr := newModernTransport()
	body := modernBody(t, "tools/call", map[string]interface{}{"name": "mddb_health"})

	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"no Mcp-Method", map[string]string{mcpHeaderName: "mddb_health"}},
		{"Mcp-Method disagrees", map[string]string{mcpHeaderMethod: "tools/list", mcpHeaderName: "mddb_health"}},
		{"no Mcp-Name on a call that names something", map[string]string{mcpHeaderMethod: "tools/call"}},
		{"Mcp-Name disagrees", map[string]string{mcpHeaderMethod: "tools/call", mcpHeaderName: "mddb_delete"}},
		{"no protocol version", map[string]string{mcpHeaderProtocolVersion: "", mcpHeaderMethod: "tools/call", mcpHeaderName: "mddb_health"}},
		{"protocol version disagrees", map[string]string{mcpHeaderProtocolVersion: "2025-11-25", mcpHeaderMethod: "tools/call", mcpHeaderName: "mddb_health"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postModern(t, tr, body, tc.headers)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
			if code := rpcErrorCode(t, rec); code != mcpErrHeaderMismatch {
				t.Errorf("code = %d, want %d (HeaderMismatch)", code, mcpErrHeaderMismatch)
			}
		})
	}
}

// A tool named outside plain ASCII travels Base64-encoded between sentinels,
// and the comparison happens after decoding — otherwise every client with a
// non-English tool name would be rejected for a mismatch it did not make.
func TestABase64NameHeaderIsDecodedBeforeComparing(t *testing.T) {
	tr := newModernTransport()
	name := "wyszukaj_dokumenty_ąćę"
	body := modernBody(t, "tools/call", map[string]interface{}{"name": name})
	encoded := mcpHeaderB64Prefix + base64.StdEncoding.EncodeToString([]byte(name)) + mcpHeaderB64Suffix

	rec := postModern(t, tr, body, map[string]string{
		mcpHeaderMethod: "tools/call",
		mcpHeaderName:   encoded,
	})

	if code := rpcErrorCode(t, rec); code == mcpErrHeaderMismatch {
		t.Fatalf("an encoded name was read as a mismatch: %s", rec.Body.String())
	}

	// Broken Base64 is a mismatch rather than a panic or a pass.
	rec = postModern(t, tr, body, map[string]string{
		mcpHeaderMethod: "tools/call",
		mcpHeaderName:   mcpHeaderB64Prefix + "!!!not base64!!!" + mcpHeaderB64Suffix,
	})
	if code := rpcErrorCode(t, rec); code != mcpErrHeaderMismatch {
		t.Errorf("code = %d, want %d", code, mcpErrHeaderMismatch)
	}
}

// Sessions were removed. Minting one for a stateless client would invite it to
// send the id back, which is exactly the connection state the revision exists
// to remove — while a handshake client must still get one.
func TestSessionsBelongOnlyToTheHandshakeEra(t *testing.T) {
	tr := newModernTransport()

	modern := postModern(t, tr, modernBody(t, "tools/list", nil), map[string]string{
		mcpHeaderMethod: "tools/list",
	})
	if got := modern.Header().Get("MCP-Session-Id"); got != "" {
		t.Errorf("a stateless request was given session %q", got)
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	legacy := httptest.NewRecorder()
	tr.Handle(legacy, req)
	if legacy.Header().Get("MCP-Session-Id") == "" {
		t.Error("a handshake client was not given a session")
	}
}

// A 404 with a JSON-RPC body says "this endpoint is here, that method is not".
// A 404 without one is what a client gets from a URL that hosts no MCP
// endpoint at all, and the two must not look alike — a dual-era client decides
// whether to fall back on exactly this difference.
func TestAnUnknownStatelessMethodIs404WithABody(t *testing.T) {
	rec := postModern(t, newModernTransport(), modernBody(t, "tools/teleport", nil),
		map[string]string{mcpHeaderMethod: "tools/teleport"})

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if code := rpcErrorCode(t, rec); code != mcpErrMethodNotFound {
		t.Errorf("code = %d, want %d", code, mcpErrMethodNotFound)
	}
}

// A subscription's answer is a stream: the acknowledgement has to arrive
// before the response, and a client that received only the response would not
// know which of its subscriptions had been agreed to.
func TestListenAnswersWithAStream(t *testing.T) {
	rec := postModern(t, newModernTransport(),
		modernBody(t, "subscriptions/listen", map[string]interface{}{
			"notifications": map[string]interface{}{"toolsListChanged": true},
		}),
		map[string]string{mcpHeaderMethod: "subscriptions/listen"})

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want an SSE stream", ct)
	}
	body := rec.Body.String()
	ack := strings.Index(body, "notifications/subscriptions/acknowledged")
	result := strings.Index(body, `"result"`)
	if ack < 0 || result < 0 {
		t.Fatalf("stream carried %q, want an acknowledgement and a closing result", body)
	}
	if ack > result {
		t.Error("the acknowledgement arrived after the result it must precede")
	}
}

// The removed transport mechanics stay available to the clients that still
// need them, and a stateless client's stray session header is ignored rather
// than echoed.
func TestTheHandshakeTransportIsUntouched(t *testing.T) {
	tr := newModernTransport()

	get := httptest.NewRecorder()
	tr.Handle(get, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if get.Code != http.StatusBadRequest {
		t.Errorf("GET without a session: status = %d, want 400 as before", get.Code)
	}

	del := httptest.NewRecorder()
	tr.Handle(del, httptest.NewRequest(http.MethodDelete, "/mcp", nil))
	if del.Code != http.StatusNoContent {
		t.Errorf("DELETE: status = %d, want 204 as before", del.Code)
	}

	stray := postModern(t, tr, modernBody(t, "tools/list", nil), map[string]string{
		mcpHeaderMethod:  "tools/list",
		"Mcp-Session-Id": "left-over-from-an-older-client",
	})
	if got := stray.Header().Get("MCP-Session-Id"); got != "" {
		t.Errorf("a stateless request had session %q echoed back at it", got)
	}
}

// Only the methods that address something by name mirror one. Demanding
// Mcp-Name from tools/list would reject every conforming client; not demanding
// it from resources/read would leave a proxy routing on a value nobody checked.
func TestOnlyMethodsThatNameSomethingMirrorAName(t *testing.T) {
	cases := []struct {
		method   string
		params   map[string]interface{}
		want     string
		required bool
	}{
		{"tools/call", map[string]interface{}{"name": "mddb_health"}, "mddb_health", true},
		{"prompts/get", map[string]interface{}{"name": "analyze-collection"}, "analyze-collection", true},
		{"resources/read", map[string]interface{}{"uri": "mddb://health"}, "mddb://health", true},
		{"tools/list", nil, "", false},
		{"server/discover", nil, "", false},
	}

	for _, tc := range cases {
		got, required := mcpHeaderNameSource(tc.method, modernRequest(tc.method, tc.params))
		if required != tc.required || got != tc.want {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", tc.method, got, required, tc.want, tc.required)
		}
	}
}

// A JSON-RPC error is not automatically an HTTP failure: the call was
// delivered and answered. Only the codes this revision gives a status to get
// one, and everything else stays 200 with the error in the body.
func TestOnlySpecifiedErrorsChangeTheHTTPStatus(t *testing.T) {
	cases := map[int]int{
		mcpErrMethodNotFound:             http.StatusNotFound,
		mcpErrInvalidParams:              http.StatusBadRequest,
		mcpErrHeaderMismatch:             http.StatusBadRequest,
		mcpErrMissingClientCapability:    http.StatusBadRequest,
		mcpErrUnsupportedProtocolVersion: http.StatusBadRequest,
		mcpErrParse:                      http.StatusBadRequest,
		mcpErrInternal:                   http.StatusOK,
		mcpErrLegacyResourceNotFound:     http.StatusOK,
	}

	for code, want := range cases {
		if got := mcpErrorHTTPStatus(code); got != want {
			t.Errorf("code %d mapped to %d, want %d", code, got, want)
		}
	}
}

// A body that is not JSON cannot be placed in an era, so it is answered the
// way JSON-RPC answers anything unreadable.
func TestAnUnparseableBodyIsAParseError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":`))
	rec := httptest.NewRecorder()
	newModernTransport().Handle(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if code := rpcErrorCode(t, rec); code != mcpErrParse {
		t.Errorf("code = %d, want %d", code, mcpErrParse)
	}
}

// A notification carries no id and gets no answer — in either era, and
// whatever the revision says about headers on requests.
func TestANotificationIsAcceptedAndNotAnswered(t *testing.T) {
	rec := postModern(t, newModernTransport(),
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{}}`, nil)

	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("body = %q, want nothing", body)
	}
}
