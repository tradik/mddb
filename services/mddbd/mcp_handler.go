package main

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"os"
	"sync"
)

// MCPProtocolVersion is the MCP spec version this server implements.
const MCPProtocolVersion = "2025-11-25"

// MCPHandler handles MCP JSON-RPC requests via stdio.
type MCPHandler struct {
	client       MCPClient
	customTools  []MCPCustomToolConfig
	serverInfo   MCPServerInfo
	instructions string     // system prompt for LLM — how to use this server
	globalMode   AccessMode // server-wide mode (from MDDB_MODE / follower)
	mode         AccessMode // per-protocol override (from MDDB_MCP_MODE, "" = inherit global)

	mu       sync.RWMutex
	logLevel MCPLogLevel // minimum log level from client
}

// NewMCPHandler creates a new MCP handler.
func NewMCPHandler(client MCPClient, customTools []MCPCustomToolConfig) *MCPHandler {
	return &MCPHandler{
		client:      client,
		customTools: customTools,
		serverInfo:  MCPServerInfo{Name: "mddbd"},
		logLevel:    MCPLogWarning,
	}
}

// NewMCPHandlerWithConfig creates a new MCP handler with custom server info, instructions, and access mode.
func NewMCPHandlerWithConfig(client MCPClient, customTools []MCPCustomToolConfig, info MCPServerInfo, instructions string, globalMode, protocolMode AccessMode) *MCPHandler {
	if info.Name == "" {
		info.Name = "mddbd"
	}
	return &MCPHandler{
		client:       client,
		customTools:  customTools,
		serverInfo:   info,
		instructions: instructions,
		globalMode:   globalMode,
		mode:         protocolMode,
		logLevel:     MCPLogWarning,
	}
}

// Handle processes MCP request and returns response.
func (h *MCPHandler) Handle(req map[string]interface{}) map[string]interface{} {
	method, _ := req["method"].(string)
	id := req["id"]
	ctx := context.Background()

	// Handle notifications (no response expected)
	if id == nil {
		switch method {
		case "notifications/initialized",
			"notifications/cancelled",
			"notifications/roots/list_changed":
			// Accept silently — no response per spec
			return nil
		}
	}

	var result map[string]interface{}
	var errObj map[string]interface{}

	switch method {
	case "initialize":
		return h.handleInitialize(req)

	// Resources
	case "resources/list":
		result = h.handleResourcesList(req)
	case "resources/read":
		result = h.handleResourcesRead(ctx, req)

	// Tools
	case "tools/list":
		result = h.handleToolsList(req)
	case "tools/call":
		result = h.handleToolsCall(ctx, req)

	// Prompts
	case "prompts/list":
		result = h.handlePromptsList(req)
	case "prompts/get":
		result = h.handlePromptsGet(ctx, req)

	// Completion
	case "completion/complete":
		result = h.handleComplete(ctx, req)

	// Logging
	case "logging/setLevel":
		result = h.handleSetLogLevel(req)

	// Ping
	case "ping":
		result = map[string]interface{}{}

	default:
		errObj = map[string]interface{}{
			"code":    -32601,
			"message": "Method not found",
		}
	}

	response := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
	}

	if errObj != nil {
		response["error"] = errObj
	} else {
		response["result"] = result
	}

	return response
}

func (h *MCPHandler) handleInitialize(req map[string]interface{}) map[string]interface{} {
	id := req["id"]

	result := map[string]interface{}{
		"protocolVersion": MCPProtocolVersion,
		"capabilities": map[string]interface{}{
			"resources": map[string]interface{}{
				"subscribe":   false,
				"listChanged": true,
			},
			"tools": map[string]interface{}{
				"listChanged": true,
			},
			"prompts": map[string]interface{}{
				"listChanged": false,
			},
			"logging":     map[string]interface{}{},
			"completions": map[string]interface{}{},
		},
		"serverInfo": h.buildServerInfo(),
	}
	if h.instructions != "" {
		result["instructions"] = h.instructions
	}

	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}
}

// buildServerInfo constructs the serverInfo object for MCP initialize response.
func (h *MCPHandler) buildServerInfo() map[string]interface{} {
	info := map[string]interface{}{
		"name":    h.serverInfo.Name,
		"version": VERSION,
	}
	if h.serverInfo.Description != "" {
		info["description"] = h.serverInfo.Description
	}
	if h.serverInfo.Vendor != "" {
		info["vendor"] = h.serverInfo.Vendor
	}
	if h.serverInfo.Homepage != "" {
		info["homepage"] = h.serverInfo.Homepage
	}
	return info
}

// ---- Resources with cursor pagination ----

func (h *MCPHandler) handleResourcesList(req map[string]interface{}) map[string]interface{} {
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

	// Cursor pagination — since we have few resources, return all in one page
	return map[string]interface{}{
		"resources": resources,
	}
}

func (h *MCPHandler) handleResourcesRead(ctx context.Context, req map[string]interface{}) map[string]interface{} {
	params, _ := req["params"].(map[string]interface{})
	uri, _ := params["uri"].(string)

	ts := &MCPToolServer{client: h.client, customTools: h.customTools, globalMode: h.globalMode, mode: h.mode}
	content, err := ts.readResource(ctx, uri)
	if err != nil {
		return map[string]interface{}{
			"error": map[string]interface{}{
				"code":    -32002,
				"message": err.Error(),
			},
		}
	}

	return map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"uri":      uri,
				"mimeType": "application/json",
				"text":     content,
			},
		},
	}
}

// ---- Tools with cursor pagination ----

func (h *MCPHandler) handleToolsList(req map[string]interface{}) map[string]interface{} {
	tools := mcpAllTools(h.customTools)

	// Cursor-based pagination
	params, _ := req["params"].(map[string]interface{})
	cursor, _ := params["cursor"].(string)

	if cursor != "" {
		// Find cursor position and return remaining tools
		for i, t := range tools {
			if t.Name == cursor {
				tools = tools[i+1:]
				break
			}
		}
	}

	result := map[string]interface{}{
		"tools": tools,
	}

	// All tools fit in one page — no nextCursor needed
	return result
}

func (h *MCPHandler) handleToolsCall(ctx context.Context, req map[string]interface{}) map[string]interface{} {
	params, _ := req["params"].(map[string]interface{})
	name, _ := params["name"].(string)
	args, _ := params["arguments"].(map[string]interface{})

	ts := &MCPToolServer{client: h.client, customTools: h.customTools, globalMode: h.globalMode, mode: h.mode}
	result, err := ts.mcpCallTool(ctx, name, args)
	if err != nil {
		return map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": err.Error(),
				},
			},
			"isError": true,
		}
	}

	return map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": result,
			},
		},
	}
}

// ---- Prompts ----

func (h *MCPHandler) handlePromptsList(req map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"prompts": mcpBuiltinPrompts(),
	}
}

func (h *MCPHandler) handlePromptsGet(ctx context.Context, req map[string]interface{}) map[string]interface{} {
	params, _ := req["params"].(map[string]interface{})
	name, _ := params["name"].(string)
	args, _ := params["arguments"].(map[string]interface{})

	messages, description, err := mcpGetPrompt(ctx, h.client, name, args)
	if err != nil {
		return map[string]interface{}{
			"error": map[string]interface{}{
				"code":    -32602,
				"message": err.Error(),
			},
		}
	}

	return map[string]interface{}{
		"description": description,
		"messages":    messages,
	}
}

// ---- Completion ----

func (h *MCPHandler) handleComplete(ctx context.Context, req map[string]interface{}) map[string]interface{} {
	params, _ := req["params"].(map[string]interface{})
	ref, _ := params["ref"].(map[string]interface{})
	argument, _ := params["argument"].(map[string]interface{})

	values, total, hasMore := mcpComplete(ctx, h.client, ref, argument)

	return map[string]interface{}{
		"completion": map[string]interface{}{
			"values":  values,
			"total":   total,
			"hasMore": hasMore,
		},
	}
}

// ---- Logging ----

func (h *MCPHandler) handleSetLogLevel(req map[string]interface{}) map[string]interface{} {
	params, _ := req["params"].(map[string]interface{})
	level, _ := params["level"].(string)

	if _, ok := mcpLogLevelOrder[MCPLogLevel(level)]; !ok {
		return map[string]interface{}{
			"error": map[string]interface{}{
				"code":    -32602,
				"message": "invalid log level: " + level,
			},
		}
	}

	h.mu.Lock()
	h.logLevel = MCPLogLevel(level)
	h.mu.Unlock()

	return map[string]interface{}{}
}

// GetLogLevel returns the current minimum log level.
func (h *MCPHandler) GetLogLevel() MCPLogLevel {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.logLevel
}

// HandleJSON processes JSON request and returns JSON response.
func (h *MCPHandler) HandleJSON(reqJSON []byte) ([]byte, error) {
	var req map[string]interface{}
	if err := json.Unmarshal(reqJSON, &req); err != nil {
		return nil, err
	}

	resp := h.Handle(req)
	if resp == nil {
		// Notification — no response
		return nil, nil
	}
	return json.Marshal(resp)
}

// runMCPStdio runs the MCP stdio loop on the Server.
func (s *Server) runMCPStdio() {
	log.SetOutput(os.Stderr) // MCP uses stdout for protocol

	customTools := loadMCPCustomTools()
	client := NewDirectClient(s)
	handler := NewMCPHandlerWithConfig(client, customTools, s.MCPInfo, s.MCPInstructions, s.Mode, s.Config.MCP.Mode)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 4*1024*1024), 4*1024*1024) // 4MB buffer

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		resp, err := handler.HandleJSON(line)
		if err != nil {
			slog.Warn("MCP handler error", "err", err)
			continue
		}

		if resp == nil {
			// Notification — no response to send
			continue
		}

		_, _ = os.Stdout.Write(resp)
		_, _ = os.Stdout.Write([]byte("\n"))
	}

	if err := scanner.Err(); err != nil {
		slog.Warn("MCP stdio scanner error", "err", err)
	}
}
