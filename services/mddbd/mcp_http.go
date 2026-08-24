package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
)

// MCPHTTPServer provides MCP HTTP endpoints on the existing mux.
type MCPHTTPServer struct {
	client      MCPClient
	customTools []MCPCustomToolConfig
	globalMode  AccessMode
	mode        AccessMode
}

// newMCPHTTPServer creates an MCPHTTPServer backed by the Server.
func (s *Server) newMCPHTTPServer() *MCPHTTPServer {
	return &MCPHTTPServer{
		client:      NewDirectClient(s),
		customTools: loadMCPCustomTools(),
		globalMode:  s.Mode,
		mode:        s.Config.MCP.Mode,
	}
}

func (m *MCPHTTPServer) handleResources(w http.ResponseWriter, r *http.Request) {
	resources := []MCPResource{
		{
			URI:         "mddb://health",
			Name:        "MDDB Health",
			Description: "Health status of MDDB server",
			MimeType:    "application/json",
		},
		{
			URI:         "mddb://stats",
			Name:        "MDDB Statistics",
			Description: "Server and database statistics",
			MimeType:    "application/json",
		},
		{
			URI:         "mddb://{collection}/{key}?lang={lang}",
			Name:        "MDDB Document",
			Description: "Get a document by collection, key, and language",
			MimeType:    "text/markdown",
		},
		{
			URI:         "mddb-search://{collection}",
			Name:        "MDDB Search",
			Description: "Search documents in a collection",
			MimeType:    "application/json",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"resources": resources,
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
		"tools": mcpAllTools(m.customTools),
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

	ts := &MCPToolServer{client: m.client, customTools: m.customTools, globalMode: m.globalMode, mode: m.mode}
	result, err := ts.mcpCallTool(r.Context(), req.Name, req.Arguments)
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
