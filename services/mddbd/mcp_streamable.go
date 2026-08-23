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

// MCPStreamableTransport implements the Streamable HTTP transport (MCP 2025-11-25).
//
// Protocol:
//   - Single endpoint (e.g. /mcp) accepts POST and GET
//   - POST: client sends JSON-RPC, server responds with application/json or text/event-stream
//   - GET:  client opens SSE stream for server-initiated messages
//   - Session management via MCP-Session-Id header
//   - Protocol version via MCP-Protocol-Version header
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
	case http.MethodGet:
		t.handleGet(w, r)
	case http.MethodDelete:
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

	// Handle notifications (no id) — return 202 Accepted
	if _, hasID := req["id"]; !hasID {
		// notifications/initialized, notifications/cancelled, etc.
		w.WriteHeader(http.StatusAccepted)
		return
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

	// On initialize, assign session ID
	if method == "initialize" {
		sessionID := generateStreamableSessionID()
		w.Header().Set("MCP-Session-Id", sessionID)
	} else {
		// Echo back session ID if provided
		if sid := r.Header.Get("MCP-Session-Id"); sid != "" {
			w.Header().Set("MCP-Session-Id", sid)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	respJSON, _ := json.Marshal(resp)
	_, _ = w.Write(respJSON)
}

func (t *MCPStreamableTransport) handleGet(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
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
