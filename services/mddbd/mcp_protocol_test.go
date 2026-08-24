package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

func TestMCPHandlerInitializeVersion(t *testing.T) {
	h := &MCPHandler{logLevel: MCPLogWarning}
	resp := h.Handle(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]interface{}{},
	})

	result := resp["result"].(map[string]interface{})
	if result["protocolVersion"] != MCPProtocolVersion {
		t.Errorf("expected %s, got %v", MCPProtocolVersion, result["protocolVersion"])
	}

	caps := result["capabilities"].(map[string]interface{})
	if _, ok := caps["prompts"]; !ok {
		t.Error("expected prompts capability")
	}
	if _, ok := caps["logging"]; !ok {
		t.Error("expected logging capability")
	}
	if _, ok := caps["completions"]; !ok {
		t.Error("expected completions capability")
	}
}

func TestMCPHandlerPromptsList(t *testing.T) {
	h := &MCPHandler{logLevel: MCPLogWarning}
	resp := h.Handle(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "prompts/list",
	})

	result := resp["result"].(map[string]interface{})
	prompts := result["prompts"].([]MCPPrompt)
	if len(prompts) < 5 {
		t.Errorf("expected at least 5 prompts, got %d", len(prompts))
	}

	names := map[string]bool{}
	for _, p := range prompts {
		names[p.Name] = true
	}
	for _, expected := range []string{"analyze-collection", "search-help", "summarize-collection", "import-guide", "rag-pipeline"} {
		if !names[expected] {
			t.Errorf("missing prompt: %s", expected)
		}
	}
}

func TestMCPHandlerPromptsGet(t *testing.T) {
	h := &MCPHandler{logLevel: MCPLogWarning}
	resp := h.Handle(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "prompts/get",
		"params": map[string]interface{}{
			"name":      "search-help",
			"arguments": map[string]interface{}{"use_case": "find blog posts about Go"},
		},
	})

	result := resp["result"].(map[string]interface{})
	messages := result["messages"].([]MCPPromptMessage)
	if len(messages) == 0 {
		t.Error("expected at least one message")
	}
	if messages[0].Role != "user" {
		t.Errorf("expected user role, got %s", messages[0].Role)
	}
}

func TestMCPHandlerPromptsGetMissingArg(t *testing.T) {
	h := &MCPHandler{logLevel: MCPLogWarning}
	resp := h.Handle(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "prompts/get",
		"params": map[string]interface{}{
			"name":      "analyze-collection",
			"arguments": map[string]interface{}{},
		},
	})

	result := resp["result"].(map[string]interface{})
	if _, ok := result["error"]; !ok {
		t.Error("expected error for missing argument")
	}
}

func TestMCPHandlerSetLogLevel(t *testing.T) {
	h := &MCPHandler{logLevel: MCPLogWarning}
	resp := h.Handle(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "logging/setLevel",
		"params":  map[string]interface{}{"level": "debug"},
	})

	if resp["error"] != nil {
		t.Errorf("unexpected error: %v", resp["error"])
	}

	if h.logLevel != MCPLogDebug {
		t.Errorf("expected debug, got %s", h.logLevel)
	}
}

func TestMCPHandlerSetLogLevelInvalid(t *testing.T) {
	h := &MCPHandler{logLevel: MCPLogWarning}
	resp := h.Handle(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "logging/setLevel",
		"params":  map[string]interface{}{"level": "superduper"},
	})

	result := resp["result"].(map[string]interface{})
	if result["error"] == nil {
		t.Error("expected error for invalid log level")
	}
}

func TestMCPHandlerPing(t *testing.T) {
	h := &MCPHandler{logLevel: MCPLogWarning}
	resp := h.Handle(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "ping",
	})

	result := resp["result"].(map[string]interface{})
	if len(result) != 0 {
		t.Errorf("expected empty result for ping, got %v", result)
	}
}

func TestMCPHandlerNotification(t *testing.T) {
	h := &MCPHandler{logLevel: MCPLogWarning}
	resp := h.Handle(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})

	if resp != nil {
		t.Error("expected nil response for notification")
	}
}

func TestMCPHandlerToolsCallError(t *testing.T) {
	h := &MCPHandler{logLevel: MCPLogWarning}
	resp := h.Handle(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "nonexistent_tool",
			"arguments": map[string]interface{}{},
		},
	})

	result := resp["result"].(map[string]interface{})
	if result["isError"] != true {
		t.Error("expected isError=true for unknown tool")
	}
}

func TestMCPToolAnnotations(t *testing.T) {
	tools := mcpAllTools(nil)

	annotated := 0
	for _, tool := range tools {
		if tool.Annotations != nil {
			annotated++
		}
	}

	if annotated < 40 {
		t.Errorf("expected at least 40 annotated tools, got %d", annotated)
	}

	// Check specific annotations
	for _, tool := range tools {
		switch tool.Name {
		case "get_stats":
			if tool.Annotations == nil || tool.Annotations.ReadOnlyHint == nil || !*tool.Annotations.ReadOnlyHint {
				t.Error("get_stats should be readOnly")
			}
		case "delete_collection":
			if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
				t.Error("delete_collection should be destructive")
			}
		case "add_document":
			if tool.Annotations == nil || tool.Annotations.IdempotentHint == nil || !*tool.Annotations.IdempotentHint {
				t.Error("add_document should be idempotent")
			}
		case "semantic_search":
			if tool.Annotations == nil || tool.Annotations.OpenWorldHint == nil || !*tool.Annotations.OpenWorldHint {
				t.Error("semantic_search should be openWorld")
			}
		}
	}
}

func TestMCPToolOutputSchemas(t *testing.T) {
	tools := mcpAllTools(nil)

	withOutput := 0
	for _, tool := range tools {
		if tool.OutputSchema != nil {
			withOutput++
		}
	}

	if withOutput < 5 {
		t.Errorf("expected at least 5 tools with output schemas, got %d", withOutput)
	}
}

func TestMCPLogShouldLog(t *testing.T) {
	if !mcpShouldLog(MCPLogError, MCPLogWarning) {
		t.Error("error should pass warning threshold")
	}
	if mcpShouldLog(MCPLogDebug, MCPLogWarning) {
		t.Error("debug should not pass warning threshold")
	}
	if !mcpShouldLog(MCPLogWarning, MCPLogWarning) {
		t.Error("warning should pass warning threshold")
	}
}

func TestMCPLogMessage(t *testing.T) {
	msg := MCPLogMessage(MCPLogInfo, "mddbd", "server started")
	if msg["method"] != "notifications/message" {
		t.Error("expected notifications/message method")
	}
	params := msg["params"].(map[string]interface{})
	if params["level"] != "info" {
		t.Error("expected info level")
	}
}

func TestMCPCompletePromptArg(t *testing.T) {
	values, total, _ := mcpCompletePromptArg("source", "w")
	if total != 5 {
		t.Errorf("expected 5 total, got %d", total)
	}
	if len(values) != 1 || values[0] != "wordpress" {
		t.Errorf("expected [wordpress], got %v", values)
	}
}

func TestMCPCompletePromptArgEmpty(t *testing.T) {
	values, _, _ := mcpCompletePromptArg("source", "")
	if len(values) != 5 {
		t.Errorf("expected 5 values for empty prefix, got %d", len(values))
	}
}

func TestFilterPrefix(t *testing.T) {
	items := []string{"apple", "avocado", "banana", "cherry"}
	result := filterPrefix(items, "a")
	if len(result) != 2 {
		t.Errorf("expected 2, got %d: %v", len(result), result)
	}
	result = filterPrefix(items, "")
	if len(result) != 4 {
		t.Errorf("expected 4 for empty prefix, got %d", len(result))
	}
}

func TestMCPStreamableTransportPost(t *testing.T) {
	h := &MCPHandler{logLevel: MCPLogWarning, serverInfo: MCPServerInfo{Name: "test"}}
	transport := NewMCPStreamableTransport(h)

	body := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	transport.Handle(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["jsonrpc"] != "2.0" {
		t.Error("expected jsonrpc 2.0")
	}
}

func TestMCPStreamableTransportPostInitialize(t *testing.T) {
	h := &MCPHandler{logLevel: MCPLogWarning, serverInfo: MCPServerInfo{Name: "test"}}
	transport := NewMCPStreamableTransport(h)

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	transport.Handle(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	sessionID := w.Header().Get("MCP-Session-Id")
	if sessionID == "" {
		t.Error("expected MCP-Session-Id header on initialize")
	}
}

func TestMCPStreamableTransportNotification(t *testing.T) {
	h := &MCPHandler{logLevel: MCPLogWarning}
	transport := NewMCPStreamableTransport(h)

	body := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	transport.Handle(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202 for notification, got %d", w.Code)
	}
}

func TestMCPStreamableTransportDelete(t *testing.T) {
	h := &MCPHandler{logLevel: MCPLogWarning}
	transport := NewMCPStreamableTransport(h)

	req := httptest.NewRequest("DELETE", "/mcp", nil)
	req.Header.Set("MCP-Session-Id", "test-session")
	w := httptest.NewRecorder()

	transport.Handle(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

func TestMCPStreamableTransportMethodNotAllowed(t *testing.T) {
	h := &MCPHandler{logLevel: MCPLogWarning}
	transport := NewMCPStreamableTransport(h)

	req := httptest.NewRequest("PUT", "/mcp", nil)
	w := httptest.NewRecorder()

	transport.Handle(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestMCPBuiltinPrompts(t *testing.T) {
	prompts := mcpBuiltinPrompts()
	if len(prompts) != 5 {
		t.Errorf("expected 5 prompts, got %d", len(prompts))
	}
	for _, p := range prompts {
		if p.Name == "" {
			t.Error("prompt name should not be empty")
		}
		if p.Description == "" {
			t.Error("prompt description should not be empty")
		}
	}
}

func TestMCPGetPromptUnknown(t *testing.T) {
	_, _, err := mcpGetPrompt(context.Background(), nil, "nonexistent", nil)
	if err == nil {
		t.Error("expected error for unknown prompt")
	}
}

func TestMCPHandlerCompletion(t *testing.T) {
	h := &MCPHandler{logLevel: MCPLogWarning}
	resp := h.Handle(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "completion/complete",
		"params": map[string]interface{}{
			"ref":      map[string]interface{}{"type": "ref/prompt"},
			"argument": map[string]interface{}{"name": "source", "value": "w"},
		},
	})

	result := resp["result"].(map[string]interface{})
	completion := result["completion"].(map[string]interface{})
	values := completion["values"].([]string)
	if len(values) != 1 || values[0] != "wordpress" {
		t.Errorf("expected [wordpress], got %v", values)
	}
}
