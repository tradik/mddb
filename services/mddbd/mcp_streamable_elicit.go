package main

import (
	"net/http"
	"sync"
)

// Asking a handshake-era Streamable HTTP client for confirmation (MCP-004).
//
// 2025-11-25 lets a server answer a POST with an SSE stream instead of a JSON
// body, and send its own requests on that stream before the response; the
// client answers each with a POST of its own. That is the channel
// elicitation/create travels on here: the POST of a destructive tools/call
// becomes a stream at the moment the server has something to ask, and stays a
// plain JSON answer otherwise — clients that never declared elicitation see
// exactly the responses they always did.

// mcpElicitSessionsMax bounds the sessions remembered as able to elicit. A
// client that never ends its session with DELETE would otherwise leave an
// entry behind for the life of the process; when the bound is reached an
// arbitrary entry is dropped, and that session is served as one that cannot
// be asked.
const mcpElicitSessionsMax = 10000

// mcpElicitSessions remembers which handshake sessions declared elicitation
// in their initialize. The capability is a property of the session, and the
// session id is the only thing a later POST carries to name it.
type mcpElicitSessions struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

// add records a session that declared elicitation.
func (s *mcpElicitSessions) add(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ids == nil {
		s.ids = map[string]struct{}{}
	}
	if len(s.ids) >= mcpElicitSessionsMax {
		for old := range s.ids {
			delete(s.ids, old)
			break
		}
	}
	s.ids[id] = struct{}{}
}

// has reports whether a session declared elicitation.
func (s *mcpElicitSessions) has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.ids[id]
	return ok
}

// remove forgets a session that ended.
func (s *mcpElicitSessions) remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.ids, id)
}

// mcpPostStream turns a POST's response into an SSE stream the first time the
// server sends something on it. Used from the request's own goroutine only.
type mcpPostStream struct {
	w       http.ResponseWriter
	started bool
}

// send writes one server message to the stream, opening it if need be.
func (p *mcpPostStream) send(msg map[string]interface{}) error {
	flusher, ok := httpFlusher(p.w)
	if !ok {
		return errMCPNoStream
	}
	if !p.started {
		p.w.Header().Set("Content-Type", "text/event-stream")
		p.w.Header().Set("Cache-Control", "no-cache")
		p.w.Header().Set("X-Accel-Buffering", "no")
		p.w.WriteHeader(http.StatusOK)
		p.started = true
	}
	writeSSEMessage(p.w, flusher, msg)
	return nil
}

// legacyConfirmer returns how a handshake-era request on this transport asks
// for confirmation: on its own response stream, when its session declared
// elicitation and the request is a tool call. nil otherwise.
func (t *MCPStreamableTransport) legacyConfirmer(w http.ResponseWriter, method, sessionID string) (*mcpPostStream, mcpConfirmer) {
	if method != "tools/call" || sessionID == "" || !t.elicit.has(sessionID) {
		return nil, nil
	}
	stream := &mcpPostStream{w: w}
	return stream, &mcpRequestConfirmer{send: stream.send, pending: t.pending}
}
