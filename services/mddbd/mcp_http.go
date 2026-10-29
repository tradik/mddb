package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// MCPHTTPServer provides MCP HTTP endpoints on the existing mux.
type MCPHTTPServer struct {
	client      MCPClient
	customTools []MCPCustomToolConfig
	globalMode  AccessMode
	mode        AccessMode
	observer    mcpToolObserver // logs and counts /mcp/tools/call (mcp_tool_log.go)
}

// newMCPHTTPServer creates an MCPHTTPServer backed by the Server.
func (s *Server) newMCPHTTPServer() *MCPHTTPServer {
	return &MCPHTTPServer{
		client:      NewDirectClient(s),
		customTools: loadMCPCustomTools(),
		globalMode:  s.Mode,
		mode:        s.Config.MCP.Mode,
		observer:    newMCPToolObserver(s.Metrics),
	}
}

func (m *MCPHTTPServer) handleResources(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"resources": mcpResourceCatalogue(),
		// Listed separately from the resources, as they are over MCP: a
		// template is a pattern to fill in, not an address to fetch.
		"resourceTemplates": mcpResourceTemplates(),
	}); err != nil {
		slog.Error("encoding resources response", "err", err)
	}
}

func (m *MCPHTTPServer) handleResourceRead(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var req MCPResourceReadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	ts := &MCPToolServer{client: m.client, customTools: m.customTools, globalMode: m.globalMode, mode: m.mode}
	content, err := ts.readResource(r.Context(), req.URI)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"uri":      req.URI,
				"mimeType": "application/json",
				"text":     content,
			},
		},
	}); err != nil {
		slog.Error("encoding resource read response", "err", err)
	}
}

func (m *MCPHTTPServer) handleTools(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"tools": mcpToolsFor(r.Context(), m.customTools),
	}); err != nil {
		slog.Error("encoding tools response", "err", err)
	}
}

func (m *MCPHTTPServer) handleToolCall(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20) // 4MB
	var req MCPToolCallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if _, builtin := mcpToolGroup[req.Name]; builtin && !builtinToolAllowed(r.Context(), req.Name) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": errToolNotInToolset(req.Name)})
		return
	}

	ts := &MCPToolServer{client: m.client, customTools: m.customTools, globalMode: m.globalMode, mode: m.mode}
	ctx := withMCPCaller(r.Context(), r)
	start := time.Now()
	result, err := ts.mcpCallTool(ctx, req.Name, req.Arguments)
	m.observer.observe(ctx, req.Name, req.Arguments, result, err, time.Since(start))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": result,
			},
		},
	}); err != nil {
		slog.Error("encoding tool call response", "err", err)
	}
}
