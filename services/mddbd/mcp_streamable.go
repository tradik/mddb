package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	json "mddb/internal/jsonx"
	"net/http"
	"sync"
)

// MCPStreamableTransport implements the Streamable HTTP transport for both
// revisions MDDB speaks (MCP-001).
//
// 2025-11-25 (legacy, handshake-based):
//   - POST: client sends JSON-RPC, server answers with application/json
//   - GET:  client opens an SSE stream for server-initiated messages
//   - Session management via MCP-Session-Id header, terminated with DELETE
//
// 2026-07-28 (stateless):
//   - POST only; no session is minted and an Mcp-Session-Id is ignored
//   - Mcp-Method / Mcp-Name / MCP-Protocol-Version are required and are
//     checked against the body (mcp_headers.go)
//   - subscriptions/listen answers with an SSE stream instead of JSON
//   - an unknown method is 404 with a JSON-RPC body, which is what tells a
//     client this endpoint exists and the method does not
//
// Both eras share the endpoint. A request is served according to what it
// carries, never according to what an earlier request on the same connection
// carried — see mcpIsModernRequest.
type MCPStreamableTransport struct {
	handler *MCPHandler
	mu      sync.RWMutex
	// sessions tracks active session SSE channels for server-initiated messages
	sessions map[string]*streamableSession
}

type streamableSession struct {
	ch   chan []byte
	done chan struct{}
}

// NewMCPStreamableTransport creates a new Streamable HTTP transport.
func NewMCPStreamableTransport(handler *MCPHandler) *MCPStreamableTransport {
	return &MCPStreamableTransport{
		handler:  handler,
		sessions: make(map[string]*streamableSession),
	}
}

// Handle is the single MCP endpoint handler supporting POST and GET.
func (t *MCPStreamableTransport) Handle(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		t.handlePost(w, r)
	case http.MethodGet, http.MethodDelete:
		// GET (the standalone SSE stream) and DELETE (session termination)
		// exist only in the handshake era; 2026-07-28 removed both. Their
		// version check stays where it always was, before anything else,
		// because neither carries a body to read an era from.
		if err := checkMCPVersionHeader(r.Header.Get(mcpHeaderProtocolVersion)); err != nil {
			http.Error(w, mcpVersionErrorBody(err), http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodGet {
			t.handleGet(w, r)
			return
		}
		t.handleDelete(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (t *MCPStreamableTransport) handlePost(w http.ResponseWriter, r *http.Request) {
	// SEC-008: apply the same CORS allowlist as the REST surface instead of
	// reflecting whatever Origin the request carried (which defeated same-origin
	// protection and let any site read MCP responses). A disallowed origin gets
	// no Allow-Origin header and the browser blocks the cross-origin read.
	envCORSConfig().applyOrigin(w, r.Header.Get("Origin"))

	r.Body = http.MaxBytesReader(w, r.Body, 4<<20) // 4MB
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"request too large"}`, http.StatusRequestEntityTooLarge)
		return
	}

	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","error":{"code":-32700,"message":"Parse error"}}`)
		return
	}

	method, _ := req["method"].(string)
	modern := mcpIsModernRequest(method, req)

	// A legacy request is version-checked from its header exactly as it was
	// before the stateless era existed. A modern one is checked against its
	// own body below, where a disagreement between the two is itself an error
	// the client has to be told about.
	if !modern {
		if err := checkMCPVersionHeader(r.Header.Get(mcpHeaderProtocolVersion)); err != nil {
			http.Error(w, mcpVersionErrorBody(err), http.StatusBadRequest)
			return
		}
	}

	// Handle notifications (no id) — return 202 Accepted
	if _, hasID := req["id"]; !hasID {
		// notifications/initialized, notifications/cancelled, etc. The
		// revision leaves header requirements for notification POSTs
		// undefined, so they are not applied here.
		w.WriteHeader(http.StatusAccepted)
		return
	}

	if modern {
		if errObj := mcpValidateModernHeaders(r.Header, req); errObj != nil {
			writeMCPErrorResponse(w, req["id"], errObj)
			return
		}
		// A subscription's answer is a stream, not an object: the
		// acknowledgement has to reach the client before the response that
		// closes it.
		if method == "subscriptions/listen" {
			t.handleListen(w, req)
			return
		}
	}

	// Process request through handler.
	//
	// GO-021: this transport keeps the connection open, so a tool's progress
	// notifications can reach the client before the response does. The
	// session is known here and nowhere else, which is why the delivery
	// function is supplied by the transport rather than held by the handler.
	sessionForProgress := r.Header.Get("Mcp-Session-Id")
	resp := t.handler.HandleWithNotifier(req, func(n map[string]interface{}) {
		if sessionForProgress != "" {
			t.SendNotification(sessionForProgress, n)
		}
	})

	// Sessions belong to the handshake era. 2026-07-28 removed them outright,
	// and the rule for a server that still serves both is to neither mint nor
	// echo one for a stateless request: a client that received an
	// Mcp-Session-Id would have every reason to send it back, and the whole
	// point of the revision is that nothing is remembered between requests.
	if !modern {
		if method == "initialize" {
			sessionID := generateStreamableSessionID()
			w.Header().Set("MCP-Session-Id", sessionID)
		} else if sid := r.Header.Get("MCP-Session-Id"); sid != "" {
			w.Header().Set("MCP-Session-Id", sid)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if modern {
		// The revision maps its errors onto HTTP: an unimplemented method is
		// 404 (with a JSON-RPC body, which distinguishes it from a 404 at a
		// URL that hosts no MCP endpoint), and everything malformed or
		// unservable is 400. A client reads the body before concluding that a
		// 400 means "this server is legacy".
		if errObj, failed := resp["error"].(map[string]interface{}); failed {
			code, _ := errObj["code"].(int)
			w.WriteHeader(mcpErrorHTTPStatus(code))
		}
	}
	respJSON, _ := json.Marshal(resp)
	_, _ = w.Write(respJSON)
}

// writeMCPErrorResponse answers a request that never reached the handler.
func writeMCPErrorResponse(w http.ResponseWriter, id interface{}, errObj map[string]interface{}) {
	code, _ := errObj["code"].(int)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(mcpErrorHTTPStatus(code))
	body, err := json.Marshal(mcpErrorResponse(id, errObj))
	if err != nil {
		return
	}
	_, _ = w.Write(body)
}

// handleListen answers subscriptions/listen with an SSE stream.
//
// The stream carries the acknowledgement first — the spec requires it to
// precede every other message on the subscription — and then the result that
// ends it. MDDB honors no notification type (mcp_subscriptions.go), so there
// is nothing to wait for in between, and a stream held open for a
// notification that cannot arrive would cost a connection per client for as
// long as the client cared to keep it. The client is told the subscription
// closed cleanly rather than being left to infer it from a dropped socket.
func (t *MCPStreamableTransport) handleListen(w http.ResponseWriter, req map[string]interface{}) {
	flusher, ok := httpFlusher(w)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	id := req["id"]
	honored := mcpHonoredNotifications(mcpParseNotificationFilter(req))
	writeSSEMessage(w, flusher, mcpSubscriptionAck(id, honored))

	result := t.handler.mcpCompleteResult("subscriptions/listen", t.handler.handleSubscriptionsListen(req))
	writeSSEMessage(w, flusher, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

// writeSSEMessage writes one JSON-RPC message as an SSE event.
func writeSSEMessage(w http.ResponseWriter, flusher http.Flusher, msg map[string]interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
	flusher.Flush()
}

func (t *MCPStreamableTransport) handleGet(w http.ResponseWriter, r *http.Request) {
	flusher, ok := httpFlusher(w)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	sessionID := r.Header.Get("MCP-Session-Id")
	if sessionID == "" {
		http.Error(w, `{"error":"MCP-Session-Id required"}`, http.StatusBadRequest)
		return
	}

	session := &streamableSession{
		ch:   make(chan []byte, 64),
		done: make(chan struct{}),
	}

	t.mu.Lock()
	t.sessions[sessionID] = session
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		delete(t.sessions, sessionID)
		t.mu.Unlock()
		close(session.done)
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	slog.Info("MCP Streamable HTTP client connected", "sessionID", sessionID) // #nosec G706 -- sessionID is hex-encoded random bytes

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			slog.Info("MCP Streamable HTTP client disconnected", "sessionID", sessionID) // #nosec G706
			return
		case msg, ok := <-session.ch:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

func (t *MCPStreamableTransport) handleDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("MCP-Session-Id")
	if sessionID == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	t.mu.Lock()
	session, ok := t.sessions[sessionID]
	if ok {
		close(session.ch)
		delete(t.sessions, sessionID)
	}
	t.mu.Unlock()

	slog.Info("MCP Streamable HTTP session terminated", "sessionID", sessionID) // #nosec G706
	w.WriteHeader(http.StatusNoContent)
}

// SendNotification sends a server-initiated message to a session's SSE stream.
func (t *MCPStreamableTransport) SendNotification(sessionID string, notification map[string]interface{}) {
	t.mu.RLock()
	session, ok := t.sessions[sessionID]
	t.mu.RUnlock()
	if !ok {
		return
	}

	data, err := json.Marshal(notification)
	if err != nil {
		return
	}

	select {
	case session.ch <- data:
	case <-session.done:
	default:
		// Channel full, drop notification
	}
}

// SessionCount returns the number of active Streamable HTTP sessions.
func (t *MCPStreamableTransport) SessionCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.sessions)
}

func generateStreamableSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "fallback-session"
	}
	return hex.EncodeToString(b)
}
