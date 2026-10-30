package main

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseRequest is a GET /v1/events request carrying claims (nil = anonymous).
func sseRequest(query string, claims *JWTClaims) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/v1/events"+query, nil)
	if claims != nil {
		r = r.WithContext(context.WithValue(r.Context(), authContextKey, claims))
	}
	return r
}

func TestSSEHandleRejections(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	s.SSEHub = NewSSEHub(false, 10, 1)
	w := httptest.NewRecorder()
	s.handleSSE(w, sseRequest("", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("disabled = %d, want 503", w.Code)
	}

	s.SSEHub = NewSSEHub(true, 10, 1)
	rec := httptest.NewRecorder()
	s.handleSSE(plainWriter{inner: rec}, sseRequest("", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("no flusher = %d, want 500", rec.Code)
	}

	// The per-IP cap counts the address ClientIP resolves.
	withTrustedProxies(t, "")
	if !s.SSEHub.addClient(&sseClient{ch: make(chan []byte, 1), ip: "192.0.2.1"}) {
		t.Fatal("first client refused")
	}
	w = httptest.NewRecorder()
	s.handleSSE(w, sseRequest("", nil)) // httptest's RemoteAddr is 192.0.2.1:1234
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("over the per-IP cap = %d, want 429", w.Code)
	}

	s.AuthManager = &AuthManager{enabled: true, db: s.DB}
	w = httptest.NewRecorder()
	s.handleSSE(w, sseRequest("?collection=private", &JWTClaims{Username: "nobody"}))
	if w.Code != http.StatusForbidden {
		t.Errorf("no read permission = %d, want 403", w.Code)
	}
}

// TestSSEHandleAllCollectionsWriteCheck: without ?collection= the write check
// runs against "*"; a request already gone returns at once.
func TestSSEHandleAllCollectionsWriteCheck(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	s.AuthManager = &AuthManager{enabled: true, db: s.DB}
	s.SSEHub = NewSSEHub(true, 10, 5)
	r := sseRequest("", &JWTClaims{Username: "root", Admin: true})
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	w := httptest.NewRecorder()
	s.handleSSE(w, r.WithContext(ctx))
	if !strings.Contains(w.Body.String(), `"collection":"","mode":"readwrite"`) {
		t.Errorf("connected line = %s", w.Body.String())
	}
	if s.SSEHub.ClientCount() != 0 {
		t.Error("client not removed after the request ended")
	}
}

func TestNewSSEHubDefaults(t *testing.T) {
	hub := NewSSEHub(true, 0, -1)
	if hub.maxClients != 1000 || hub.maxPerIP != 5 {
		t.Errorf("defaults = %d clients, %d per IP; want 1000 and 5", hub.maxClients, hub.maxPerIP)
	}
}

func TestSSEHubAddClientTotalCap(t *testing.T) {
	hub := NewSSEHub(true, 1, 5)
	if !hub.addClient(&sseClient{ch: make(chan []byte, 1), ip: "192.0.2.1"}) {
		t.Fatal("first client refused")
	}
	if hub.addClient(&sseClient{ch: make(chan []byte, 1), ip: "192.0.2.2"}) {
		t.Error("client over the total cap accepted")
	}
}

func TestSSEHubServeHTTPEnabled(t *testing.T) {
	w := httptest.NewRecorder()
	NewSSEHub(true, 1, 1).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/events", nil))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("enabled ServeHTTP = %d, want 500", w.Code)
	}
}

// TestSSEHubBroadcastChecksPermissions: with auth on, a client without read
// access gets nothing and one with write access sees readOnly=false.
func TestSSEHubBroadcastChecksPermissions(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	am := &AuthManager{enabled: true, db: s.DB}
	hub := NewSSEHub(true, 10, 5)
	admin := &sseClient{ch: make(chan []byte, 4), mode: "read", claims: &JWTClaims{Username: "root", Admin: true}}
	nobody := &sseClient{ch: make(chan []byte, 4), mode: "read", claims: &JWTClaims{Username: "nobody"}}
	hub.mu.Lock()
	hub.clients[admin] = true
	hub.clients[nobody] = true
	hub.mu.Unlock()

	hub.BroadcastWithAuth("doc.added", "blog", "k", "en", am)
	if len(nobody.ch) != 0 {
		t.Errorf("client without read access got %d events", len(nobody.ch))
	}
	if len(admin.ch) != 1 || !strings.Contains(string(<-admin.ch), `"readOnly":false`) {
		t.Error("admin should get one writable event")
	}
}

// TestSSEHandleStreamsEventsAndKeepalives runs a full authenticated stream:
// the connected line names the user and mode, then keepalives and broadcast
// events arrive.
func TestSSEHandleStreamsEventsAndKeepalives(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	s.AuthManager = &AuthManager{enabled: true, db: s.DB}
	s.SSEHub = NewSSEHub(true, 10, 5)
	s.SSEHub.keepAlive = 20 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), authContextKey, &JWTClaims{Username: "root", Admin: true}))
		s.handleSSE(w, r)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"?collection=blog", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	lines := bufio.NewScanner(resp.Body)
	waitFor := func(want string) string {
		t.Helper()
		for lines.Scan() {
			if strings.Contains(lines.Text(), want) {
				return lines.Text()
			}
		}
		t.Fatalf("stream ended before %q: %v", want, lines.Err())
		return ""
	}
	if got := waitFor(`"status":"connected"`); !strings.Contains(got, `"mode":"readwrite"`) || !strings.Contains(got, `"user":"root"`) {
		t.Errorf("connected line = %s", got)
	}
	waitFor(": keepalive")
	s.SSEHub.BroadcastWithAuth("doc.added", "blog", "post1", "en", s.AuthManager)
	waitFor("event: doc.added")
}
