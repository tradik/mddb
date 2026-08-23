package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	json "mddb/internal/jsonx"
	"log/slog"
	"net/http"
	"sync"
)

// MCPSSETransport implements the MCP-over-SSE transport as defined in the MCP specification.
// https://modelcontextprotocol.io/docs/concepts/transports#server-sent-events-sse
//
// Protocol flow:
//  1. Client connects to GET /sse → receives SSE stream
//  2. Server sends "endpoint" event with a unique URL to POST messages to
//  3. Client sends JSON-RPC requests via POST to that endpoint
//  4. Server sends JSON-RPC responses via the SSE stream
//
// This allows MCP clients (Claude Desktop, Cursor, etc.) to communicate
// over HTTP instead of stdio, enabling remote MCP connections.
type MCPSSETransport struct {
	handler *MCPHandler
	mu      sync.RWMutex
	// sessions maps session ID to the SSE response channel
	sessions map[string]*mcpSSESession
}

type mcpSSESession struct {
	ch   chan []byte // outgoing JSON-RPC responses
	done chan struct{}
}

// NewMCPSSETransport creates a new MCP-over-SSE transport.
func NewMCPSSETransport(handler *MCPHandler) *MCPSSETransport {
	return &MCPSSETransport{
		handler:  handler,
		sessions: make(map[string]*mcpSSESession),
	}
}

// HandleSSE handles GET /sse — the SSE connection endpoint.
// Sends an "endpoint" event telling the client where to POST messages.
func (t *MCPSSETransport) HandleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	sessionID := generateSessionID()

	session := &mcpSSESession{
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

	// SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Send the endpoint event — tells the client where to POST JSON-RPC messages
	// The path is relative to the same host/port as the SSE connection
	endpointURL := fmt.Sprintf("/message?sessionId=%s", sessionID)
	_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpointURL)
	flusher.Flush()

	slog.Info("MCP-over-SSE client connected", "sessionID", sessionID)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			slog.Info("MCP-over-SSE client disconnected", "sessionID", sessionID)
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

// HandleMessage handles POST /message?sessionId=xxx — receives JSON-RPC requests.
func (t *MCPSSETransport) HandleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		http.Error(w, `{"error":"missing sessionId"}`, http.StatusBadRequest)
		return
	}

	t.mu.RLock()
	session, ok := t.sessions[sessionID]
	t.mu.RUnlock()

	if !ok {
		http.Error(w, `{"error":"unknown session"}`, http.StatusNotFound)
		return
	}

	// Read JSON-RPC request
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20) // 4MB
	var req map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid JSON: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	// Process through MCP handler
	resp := t.handler.Handle(req)

	// Send response via SSE stream
	respJSON, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, `{"error":"failed to marshal response"}`, http.StatusInternalServerError)
		return
	}

	select {
	case session.ch <- respJSON:
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	case <-session.done:
		http.Error(w, `{"error":"session closed"}`, http.StatusGone)
	}
}

// SessionCount returns the number of active MCP-over-SSE sessions.
func (t *MCPSSETransport) SessionCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.sessions)
}

func generateSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback — should never happen
		return "fallback-session"
	}
	return hex.EncodeToString(b)
}
