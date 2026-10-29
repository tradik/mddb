package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"log/slog"
	"os"
	"sync"
	"time"
)

// MCPHandler handles MCP JSON-RPC requests via stdio.
type MCPHandler struct {
	client        MCPClient
	customTools   []MCPCustomToolConfig
	customPrompts []MCPCustomPromptConfig // #323, from MDDB_MCP_CONFIG
	serverInfo    MCPServerInfo
	instructions  string     // system prompt for LLM — how to use this server
	globalMode    AccessMode // server-wide mode (from MDDB_MODE / follower)
	mode          AccessMode // per-protocol override (from MDDB_MCP_MODE, "" = inherit global)

	mu       sync.RWMutex
	logLevel MCPLogLevel // minimum log level from client
	// notify and progressToken belong to the request in flight, set by
	// HandleWithNotifier for transports that can deliver a notification
	// before the response (GO-021). Both nil for a plain POST, which has
	// nowhere to put one.
	notify        func(map[string]interface{})
	progressToken interface{}

	// observer logs and counts every tools/call (see mcp_tool_log.go).
	observer mcpToolObserver
}

// NewMCPHandlerWithConfig creates a new MCP handler with custom server info, instructions, and access mode.
func NewMCPHandlerWithConfig(client MCPClient, customTools []MCPCustomToolConfig, info MCPServerInfo, instructions string, globalMode, protocolMode AccessMode) *MCPHandler {
	if info.Name == "" {
		info.Name = "mddbd"
	}
	return &MCPHandler{
		client:        client,
		customTools:   customTools,
		customPrompts: loadMCPCustomPrompts(),
		serverInfo:    info,
		instructions:  instructions,
		globalMode:    globalMode,
		mode:          protocolMode,
		logLevel:      MCPLogWarning,
		observer:      newMCPToolObserver(nil),
	}
}

// Handle processes MCP request and returns response.
func (h *MCPHandler) Handle(req map[string]interface{}) map[string]interface{} {
	return h.HandleContext(context.Background(), req)
}

// HandleContext is Handle with the caller's context. An HTTP transport passes
// a context carrying the request's caller (withMCPCaller) so a tool-call
// record can say who asked; stdio and tests pass context.Background().
func (h *MCPHandler) HandleContext(ctx context.Context, req map[string]interface{}) map[string]interface{} {
	method, _ := req["method"].(string)
	id := req["id"]

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

	// One handler, two eras (MCP-001). Which one this request belongs to is a
	// property of the request — the method it names or the protocol version it
	// carries — and never of the connection: a stateless client and a
	// handshake client may share a transport, and on stdio they may even share
	// a process. A request with neither signal is legacy and is served exactly
	// as it was before 2026-07-28 existed.
	if mcpIsModernRequest(method, req) {
		return h.handleModern(ctx, req)
	}

	var result map[string]interface{}
	var errObj map[string]interface{}

	switch method {
	case "initialize":
		return h.handleInitialize(req)

	// Resources
	case "resources/list":
		result = h.handleResourcesList(req)
	case "resources/templates/list":
		result = h.handleResourceTemplatesList(req)
	case "resources/read":
		result = h.handleResourcesRead(ctx, req)

	// Tools
	case "tools/list":
		result = h.handleToolsList(ctx, req)
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
		errObj = mcpError(mcpErrMethodNotFound, "Method not found")
	}

	// A handler that failed by returning an error object inside its result was
	// telling the client the call succeeded. Lift it out (both eras).
	if errObj == nil && result != nil {
		errObj = mcpPromoteNestedError(result, false)
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

	// A legacy client has no way to fall forward, so the refusal has to say
	// what would work — this error may be the only diagnostic its user sees.
	if !LegacyHandshakeAvailable() {
		return mcpErrorResponse(id, mcpUnsupportedVersionError(requestedMCPVersion(req["params"])))
	}

	// Answer with the revision the client and this build agree on, not with a
	// constant. A client pinned to an older revision than the newest MDDB
	// implements is a supported case, not a misconfiguration.
	result := map[string]interface{}{
		"protocolVersion": NegotiateMCPVersion(requestedMCPVersion(req["params"])),
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
	// Cursor pagination — the catalogue is small enough to fit one page.
	return map[string]interface{}{
		"resources": mcpResourceCatalogue(),
	}
}

// handleResourceTemplatesList answers resources/templates/list.
//
// The templates used to be listed as resources, which told a client it could
// read `mddb://{collection}/{key}` — a string that is not a URI. Both
// revisions define this method for patterns; only this server had nothing
// behind it.
func (h *MCPHandler) handleResourceTemplatesList(req map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"resourceTemplates": mcpResourceTemplates(),
	}
}

func (h *MCPHandler) handleResourcesRead(ctx context.Context, req map[string]interface{}) map[string]interface{} {
	params, _ := req["params"].(map[string]interface{})
	uri, _ := params["uri"].(string)

	ts := &MCPToolServer{client: h.client, customTools: h.customTools, globalMode: h.globalMode, mode: h.mode}
	content, err := ts.readResource(ctx, uri)
	if err != nil {
		return map[string]interface{}{
			"error": mcpError(mcpErrLegacyResourceNotFound, err.Error()),
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

func (h *MCPHandler) handleToolsList(ctx context.Context, req map[string]interface{}) map[string]interface{} {
	tools := mcpToolsFor(ctx, h.customTools)

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

	// MCP-003: a tool hidden from tools/list is not callable either.
	if _, builtin := mcpToolGroup[name]; builtin && !builtinToolAllowed(ctx, name) {
		return map[string]interface{}{"error": errToolNotInToolset(name)}
	}

	// GO-021: a tool long enough to be worth reporting on can now find a
	// progress sender in its context. Nil for callers that did not ask.
	ctx = withProgressSender(ctx, h.progressSender())

	ts := &MCPToolServer{client: h.client, customTools: h.customTools, globalMode: h.globalMode, mode: h.mode}
	start := time.Now()
	result, err := ts.mcpCallTool(ctx, name, args)

	// MCP-004: two answers of the confirmation gate are not tool failures. A
	// call waiting for the human's answer returns the interim result that
	// asks for it, and a call refused because the client cannot be asked is
	// a protocol error naming the capability it lacks.
	var waiting *mcpInputRequiredError
	if errors.As(err, &waiting) {
		return waiting.result()
	}
	var unavailable *mcpConfirmUnavailableError
	if errors.As(err, &unavailable) {
		return map[string]interface{}{"error": unavailable.rpcError()}
	}

	h.observer.observe(ctx, name, args, result, err, time.Since(start))
	if err != nil {
		// The client sees the failure in the result either way; this is the
		// copy that reaches its log, where a run of failing calls is visible
		// as a pattern rather than one error at a time.
		h.logToClient(ctx, MCPLogError, "tools/call", name+": "+err.Error())

		res := map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": err.Error(),
				},
			},
			"isError": true,
		}
		// GO-043: a quota refusal also says so as data, so an agent can
		// tell a full collection (507) or a spent budget (429) from a fault.
		if detail := mcpQuotaContent(err); detail != nil {
			res["structuredContent"] = detail
		}
		return res
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
	prompts := mcpBuiltinPrompts()
	for _, p := range h.customPrompts {
		prompts = append(prompts, p.asMCPPrompt())
	}
	return map[string]interface{}{
		"prompts": prompts,
	}
}

func (h *MCPHandler) handlePromptsGet(ctx context.Context, req map[string]interface{}) map[string]interface{} {
	params, _ := req["params"].(map[string]interface{})
	name, _ := params["name"].(string)
	args, _ := params["arguments"].(map[string]interface{})

	var messages []MCPPromptMessage
	var description string
	var err error
	if p, ok := h.customPrompt(name); ok {
		messages, description, err = p.render(args)
	} else {
		messages, description, err = mcpGetPrompt(ctx, h.client, name, args)
	}
	if err != nil {
		return map[string]interface{}{
			"error": mcpError(mcpErrInvalidParams, err.Error()),
		}
	}

	return map[string]interface{}{
		"description": description,
		"messages":    messages,
	}
}

// customPrompt finds a custom prompt by name.
func (h *MCPHandler) customPrompt(name string) (MCPCustomPromptConfig, bool) {
	for _, p := range h.customPrompts {
		if p.Name == name {
			return p, true
		}
	}
	return MCPCustomPromptConfig{}, false
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
			"error": mcpError(mcpErrInvalidParams, "invalid log level: "+level),
		}
	}

	h.mu.Lock()
	h.logLevel = MCPLogLevel(level)
	h.mu.Unlock()

	return map[string]interface{}{}
}

// runMCPStdio runs the MCP stdio loop on the Server.
func (s *Server) runMCPStdio() {
	log.SetOutput(os.Stderr) // MCP uses stdout for protocol
	s.serveMCPStdio(os.Stdin, os.Stdout)
}

// serveMCPStdio is the stdio loop on an arbitrary reader and writer: one
// JSON-RPC message per line in, one response per line out. runMCPStdio binds
// it to the process's stdin and stdout; tests drive it with buffers.
//
// Lines are read on their own goroutine and served one at a time on this one.
// The split exists for MCP-004: a destructive tool may wait, mid-call, for the
// client's response to an elicitation/create, and that response arrives on
// stdin like everything else — the reader routes it to the waiting call
// instead of queueing it behind the call that is waiting for it.
func (s *Server) serveMCPStdio(in io.Reader, out io.Writer) {
	customTools := loadMCPCustomTools()
	client := NewDirectClient(s)
	handler := NewMCPHandlerWithConfig(client, customTools, s.MCPInfo, s.MCPInstructions, s.Mode, s.Config.MCP.Mode).WithMetrics(s.Metrics)

	pending := newMCPPendingRequests()
	requests := make(chan map[string]interface{}, 64)
	go readMCPStdio(in, requests, pending)

	// GO-021: stdio can deliver notifications mid-request because requests
	// are handled one at a time on this goroutine — anything written during
	// a request is already ordered before the response that follows it.
	notify := mcpStdioNotifier(out)

	// One process serves one client, so what its handshake declared holds
	// for every handshake-era request after it.
	var confirmer mcpConfirmer
	for req := range requests {
		if method, _ := req["method"].(string); method == "initialize" && !mcpIsModernRequest(method, req) {
			confirmer = nil
			if mcpDeclaresElicitation(mcpParams(req)["capabilities"]) {
				confirmer = &mcpRequestConfirmer{send: mcpStdioSender(out), pending: pending}
			}
		}

		// OPS-024: stdio carries no traceparent, so each request is a trace
		// of its own when tracing is on.
		method, _ := req["method"].(string)
		ctx, sp := s.Tracer.StartServer(withMCPConfirmer(context.Background(), confirmer), "mcp.stdio", "")
		sp.SetString("rpc.system", "jsonrpc")
		sp.SetString("rpc.method", method)
		resp := handler.HandleWithNotifierContext(ctx, req, notify)
		sp.End()
		if resp == nil {
			// Notification — no response to send
			continue
		}
		_ = mcpStdioSender(out)(resp)
	}
}

// readMCPStdio reads one JSON-RPC message per line. A response to a request
// the server sent goes to whoever is waiting for it; everything else is queued
// for the serving loop. When input ends, every wait ends with it and the queue
// is closed, which ends the loop.
func readMCPStdio(in io.Reader, requests chan<- map[string]interface{}, pending *mcpPendingRequests) {
	defer close(requests)
	defer pending.closeAll()

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 4*1024*1024), 4*1024*1024) // 4MB buffer
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err != nil {
			slog.Warn("MCP handler error", "err", err)
			continue
		}
		if mcpIsResponse(msg) {
			pending.deliver(msg)
			continue
		}
		requests <- msg
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("MCP stdio scanner error", "err", err)
	}
}

// mcpStdioSender writes one message to stdout as a line of JSON: responses,
// and the server's own requests. Called only from the serving loop, like
// every other write to out. A message that cannot be encoded is reported to
// the caller and nothing is written.
func mcpStdioSender(out io.Writer) func(map[string]interface{}) error {
	return func(msg map[string]interface{}) error {
		data, err := json.Marshal(msg)
		if err != nil {
			slog.Warn("MCP message could not be encoded", "err", err)
			return err
		}
		_, err = out.Write(append(data, '\n'))
		return err
	}
}

// mcpStdioNotifier returns the notify callback for the stdio transport: each
// notification is written to out as one line of JSON. A notification that
// cannot be encoded is logged and dropped rather than breaking the stream.
func mcpStdioNotifier(out io.Writer) func(map[string]interface{}) {
	return func(n map[string]interface{}) {
		data, err := json.Marshal(n)
		if err != nil {
			slog.Warn("MCP notification could not be encoded", "err", err)
			return
		}
		_, _ = out.Write(data)
		_, _ = out.Write([]byte("\n"))
	}
}
