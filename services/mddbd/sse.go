package main

import (
	"context"
	"fmt"
	json "github.com/goccy/go-json"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

// SSEHub manages Server-Sent Events connections and broadcasts document change events.
// Enabled by default, configurable via MDDB_SSE_ENABLED=false.
//
// Auth behavior:
//   - No auth on server → read-only mode, all events streamed to everyone
//   - Auth enabled, no token → read-only, all events (public access)
//   - Auth enabled, with token → events filtered by PermRead; writable collections marked
type SSEHub struct {
	mu         sync.RWMutex
	clients    map[*sseClient]bool
	maxClients int
	maxPerIP   int // max concurrent SSE connections per IP (default 5)
	enabled    bool
	keepAlive  time.Duration
	ipCount    map[string]int // IP -> active connection count
}

type sseClient struct {
	ch         chan []byte
	collection string // "" = all collections, otherwise filter
	ip         string // client IP for rate limiting
	// Auth: nil = no auth / public (read-only), non-nil = authenticated
	claims *JWTClaims
	mode   string // "read" or "readwrite"
}

// SSEEvent represents a document change event sent to SSE clients.
type SSEEvent struct {
	Event      string `json:"event"` // "doc.added", "doc.updated", "doc.deleted"
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Lang       string `json:"lang"`
	Timestamp  int64  `json:"timestamp"`
	ReadOnly   bool   `json:"readOnly"` // true if client has no write permission on this collection
}

// NewSSEHub creates a new SSE hub.
// maxPerIP limits concurrent SSE connections per IP address (0 = use default 5).
func NewSSEHub(enabled bool, maxClients, maxPerIP int) *SSEHub {
	if maxClients <= 0 {
		maxClients = 1000
	}
	if maxPerIP <= 0 {
		maxPerIP = 5
	}
	return &SSEHub{
		clients:    make(map[*sseClient]bool),
		maxClients: maxClients,
		maxPerIP:   maxPerIP,
		enabled:    enabled,
		keepAlive:  30 * time.Second,
		ipCount:    make(map[string]int),
	}
}

// addClient registers a client and increments the per-IP counter. Returns false if IP limit reached.
func (h *SSEHub) addClient(client *sseClient) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.clients) >= h.maxClients {
		return false
	}
	if client.ip != "" && h.ipCount[client.ip] >= h.maxPerIP {
		return false
	}

	h.clients[client] = true
	if client.ip != "" {
		h.ipCount[client.ip]++
	}
	return true
}

// removeClient unregisters a client and decrements the per-IP counter.
func (h *SSEHub) removeClient(client *sseClient) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.clients, client)
	if client.ip != "" {
		h.ipCount[client.ip]--
		if h.ipCount[client.ip] <= 0 {
			delete(h.ipCount, client.ip)
		}
	}
	close(client.ch)
}

// clientIP extracts the client IP from the request (X-Forwarded-For, X-Real-IP, or RemoteAddr).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First IP in chain is the original client
		if idx := len(xff); idx > 0 {
			for i, c := range xff {
				if c == ',' {
					return xff[:i]
				}
				_ = i // use i
			}
			return xff
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// BroadcastWithAuth sends an event to all connected SSE clients, respecting auth permissions.
// authManager may be nil (auth disabled).
func (h *SSEHub) BroadcastWithAuth(event, collection, key, lang string, authManager *AuthManager) {
	if !h.enabled {
		return
	}

	now := time.Now().Unix()

	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		// Filter by collection if client specified one
		if client.collection != "" && client.collection != collection {
			continue
		}

		// Auth filtering: if auth is enabled and client is authenticated, check PermRead
		if authManager != nil && authManager.enabled && client.claims != nil {
			ctx := context.WithValue(context.Background(), authContextKey, client.claims)
			if err := authManager.CheckPermission(ctx, collection, PermRead); err != nil {
				continue // client has no read access to this collection
			}
		}

		// Determine if client can write to this collection
		readOnly := true
		if client.mode == "readwrite" {
			readOnly = false
		} else if authManager != nil && authManager.enabled && client.claims != nil {
			ctx := context.WithValue(context.Background(), authContextKey, client.claims)
			if err := authManager.CheckPermission(ctx, collection, PermWrite); err == nil {
				readOnly = false
			}
		} else if authManager == nil || !authManager.enabled {
			// No auth = read-only for SSE clients
			readOnly = true
		}

		evt := SSEEvent{
			Event:      event,
			Collection: collection,
			Key:        key,
			Lang:       lang,
			Timestamp:  now,
			ReadOnly:   readOnly,
		}

		data, err := json.Marshal(evt)
		if err != nil {
			continue
		}
		msg := fmt.Appendf(nil, "event: %s\ndata: %s\n\n", event, data)

		select {
		case client.ch <- msg:
		default:
			// Client buffer full, skip (non-blocking)
		}
	}
}

// Broadcast sends an event without auth filtering (backward-compat, always readOnly=true).
func (h *SSEHub) Broadcast(event, collection, key, lang string) {
	h.BroadcastWithAuth(event, collection, key, lang, nil)
}

// handleSSE handles GET /v1/events. Registered as a Server method for auth access.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	h := s.SSEHub
	if !h.enabled {
		http.Error(w, `{"error":"SSE disabled"}`, http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	collection := r.URL.Query().Get("collection")
	ip := clientIP(r)

	// Resolve auth: extract claims from context (injected by auth middleware).
	// When auth is enabled, the middleware already rejects unauthenticated requests (401).
	// So if we get here with auth enabled, the user is always authenticated.
	var claims *JWTClaims
	mode := "read" // default: read-only (no write permission)
	if s.AuthManager != nil && s.AuthManager.enabled {
		c, ok := GetClaimsFromContext(r.Context())
		if !ok {
			// Auth enabled but no token — deny access
			http.Error(w, `{"error":"authentication required for SSE"}`, http.StatusUnauthorized)
			return
		}
		claims = c

		// Check if user has read access to the requested collection
		if collection != "" {
			ctx := context.WithValue(r.Context(), authContextKey, claims)
			if err := s.AuthManager.CheckPermission(ctx, collection, PermRead); err != nil {
				http.Error(w, `{"error":"no read permission on collection"}`, http.StatusForbidden)
				return
			}
		}

		// Check if user has write on the requested collection (or wildcard)
		checkColl := collection
		if checkColl == "" {
			checkColl = "*"
		}
		ctx := context.WithValue(r.Context(), authContextKey, claims)
		if err := s.AuthManager.CheckPermission(ctx, checkColl, PermWrite); err == nil {
			mode = "readwrite"
		}
	}

	client := &sseClient{
		ch:         make(chan []byte, 64),
		collection: collection,
		ip:         ip,
		claims:     claims,
		mode:       mode,
	}

	if !h.addClient(client) {
		http.Error(w, `{"error":"too many SSE connections from this IP"}`, http.StatusTooManyRequests)
		return
	}

	defer h.removeClient(client)

	// SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx

	// Send initial connected event with auth mode
	username := ""
	if claims != nil {
		username = claims.Username
	}
	_, _ = fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"connected\",\"collection\":%q,\"mode\":%q,\"user\":%q}\n\n", // #nosec G705 -- values quoted with %q, safe for JSON/SSE
		collection, mode, username)
	flusher.Flush()

	slog.Info("SSE client connected (,,,)", "collection", collection, "mode", mode, "username", username, "clientCount", h.ClientCount()) // #nosec G706 -- values sanitized with %q

	// Keep-alive ticker
	ticker := time.NewTicker(h.keepAlive)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			slog.Info("SSE client disconnected", "clientCount", h.ClientCount()-1)
			return
		case msg, ok := <-client.ch:
			if !ok {
				return
			}
			_, _ = w.Write(msg)
			flusher.Flush()
		case <-ticker.C:
			_, _ = fmt.Fprintf(w, ": keepalive %d\n\n", time.Now().Unix())
			flusher.Flush()
		}
	}
}

// ClientCount returns the number of connected SSE clients.
func (h *SSEHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// ServeHTTP implements http.Handler for backward compatibility (no auth).
func (h *SSEHub) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	if !h.enabled {
		http.Error(w, `{"error":"SSE disabled"}`, http.StatusServiceUnavailable)
		return
	}
	http.Error(w, `{"error":"use Server.handleSSE instead"}`, http.StatusInternalServerError)
}
