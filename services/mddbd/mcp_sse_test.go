package main

import (
	"bufio"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// mockMCPHandler creates an MCPHandler that responds to initialize and ping.
func mockMCPHandler() *MCPHandler {
	return &MCPHandler{
		client:      nil, // not needed for initialize/ping
		customTools: nil,
		logLevel:    MCPLogWarning,
	}
}

func TestMCPSSEEndpoint(t *testing.T) {
	handler := mockMCPHandler()
	transport := NewMCPSSETransport(handler)

	server := httptest.NewServer(http.HandlerFunc(transport.HandleSSE))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %s", resp.Header.Get("Content-Type"))
	}

	// Read first event — should be "endpoint"
	scanner := bufio.NewScanner(resp.Body)
	foundEndpoint := false
	var endpointURL string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: endpoint") {
			foundEndpoint = true
		}
		if foundEndpoint && strings.HasPrefix(line, "data: ") {
			endpointURL = line[6:]
			break
		}
	}

	if !foundEndpoint {
		t.Fatal("expected 'endpoint' event")
	}
	if !strings.Contains(endpointURL, "/message?sessionId=") {
		t.Errorf("expected /message?sessionId=..., got %s", endpointURL)
	}
}

func TestMCPSSEMessageFlow(t *testing.T) {
	handler := mockMCPHandler()
	transport := NewMCPSSETransport(handler)

	mux := http.NewServeMux()
	mux.HandleFunc("/sse", transport.HandleSSE)
	mux.HandleFunc("/message", transport.HandleMessage)

	server := httptest.NewServer(mux)
	defer server.Close()

	// 1. Connect to SSE
	resp, err := http.Get(server.URL + "/sse")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 2. Read endpoint event
	scanner := bufio.NewScanner(resp.Body)
	var endpointURL string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: /message") {
			endpointURL = server.URL + line[6:]
			break
		}
	}
	if endpointURL == "" {
		t.Fatal("did not receive endpoint URL")
	}

	// 3. POST a ping request
	pingReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "ping",
	}
	body, _ := json.Marshal(pingReq)
	postResp, err := http.Post(endpointURL, "application/json", bytes.NewReader(body)) //nolint:gosec // G107: test server URL, not user-controlled
	if err != nil {
		t.Fatal(err)
	}
	_ = postResp.Body.Close()

	if postResp.StatusCode != http.StatusAccepted {
		t.Errorf("expected 202 Accepted, got %d", postResp.StatusCode)
	}

	// 4. Read the response from SSE stream
	foundMessage := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: message") {
			foundMessage = true
		}
		if foundMessage && strings.HasPrefix(line, "data: ") {
			data := line[6:]
			var resp map[string]interface{}
			if err := json.Unmarshal([]byte(data), &resp); err != nil {
				t.Fatalf("invalid JSON response: %v", err)
			}
			if resp["jsonrpc"] != "2.0" {
				t.Errorf("expected jsonrpc 2.0, got %v", resp["jsonrpc"])
			}
			_, ok := resp["result"].(map[string]interface{})
			if !ok {
				t.Fatal("expected result object")
			}
			break
		}
	}
	if !foundMessage {
		t.Error("expected message event with pong response")
	}
}

func TestMCPSSEMessageNoSession(t *testing.T) {
	handler := mockMCPHandler()
	transport := NewMCPSSETransport(handler)

	req := httptest.NewRequest("POST", "/message?sessionId=nonexistent", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	transport.HandleMessage(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestMCPSSEMessageMissingSessionID(t *testing.T) {
	handler := mockMCPHandler()
	transport := NewMCPSSETransport(handler)

	req := httptest.NewRequest("POST", "/message", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	transport.HandleMessage(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestMCPSSEMessageWrongMethod(t *testing.T) {
	handler := mockMCPHandler()
	transport := NewMCPSSETransport(handler)

	req := httptest.NewRequest("GET", "/message?sessionId=abc", nil)
	w := httptest.NewRecorder()
	transport.HandleMessage(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestMCPSSESessionCount(t *testing.T) {
	handler := mockMCPHandler()
	transport := NewMCPSSETransport(handler)

	if transport.SessionCount() != 0 {
		t.Errorf("expected 0 sessions, got %d", transport.SessionCount())
	}
}

func TestGenerateSessionID(t *testing.T) {
	id1 := generateSessionID()
	id2 := generateSessionID()

	if len(id1) != 32 { // 16 bytes = 32 hex chars
		t.Errorf("expected 32 char session ID, got %d: %s", len(id1), id1)
	}
	if id1 == id2 {
		t.Error("session IDs should be unique")
	}
}
