package main

import "context"

// The stateless request path of MCP 2026-07-28 (MCP-001).
//
// One handler serves both eras. Which one a request belongs to is decided per
// request, by mcpIsModernRequest, and never by the connection it arrived on —
// that is the point of the revision. A legacy request goes down the path it
// always did; a modern one comes here, is validated against its own declared
// revision, and leaves with the type, identity and caching hints this revision
// requires of every result.

// mcpRemovedMethods are the methods 2026-07-28 deletes, mapped to what a
// client should do instead.
//
// They are named rather than falling through to "Method not found" because
// the difference matters to whoever reads the error: a client sending
// `initialize` with modern metadata has a bug in its era handling, not a typo,
// and "Method not found" would send its author looking for a missing feature.
var mcpRemovedMethods = map[string]string{
	"initialize":            "the handshake is gone; send the request you want, carrying io.modelcontextprotocol/protocolVersion in _meta",
	"ping":                  "removed with no replacement; a request either answers or it does not",
	"logging/setLevel":      "set io.modelcontextprotocol/logLevel in each request's _meta instead",
	"resources/subscribe":   "use subscriptions/listen",
	"resources/unsubscribe": "close the subscriptions/listen stream instead",
	"tasks/result":          "tasks moved to the io.modelcontextprotocol/tasks extension, which MDDB does not implement",
}

// handleModern serves one request in the stateless era and returns a complete
// JSON-RPC response.
func (h *MCPHandler) handleModern(ctx context.Context, req map[string]interface{}) map[string]interface{} {
	id := req["id"]
	method, _ := req["method"].(string)
	meta := mcpRequestMeta(req)

	if errObj := mcpValidateModernMeta(meta); errObj != nil {
		return mcpErrorResponse(id, errObj)
	}

	// The level is scoped to this request and to nothing else. A request that
	// named none receives no notifications/message, which is the spec's rule
	// and not a default we chose.
	ctx = withMCPLogPolicy(ctx, mcpRequestLogPolicy(meta))

	result, errObj := h.dispatchModern(ctx, method, req)
	if errObj != nil {
		return mcpErrorResponse(id, errObj)
	}
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  h.mcpCompleteResult(method, result),
	}
}

// mcpValidateModernMeta checks the fields every modern request must carry.
//
// A missing required field is malformed rather than unsupported, so it is
// -32602 — the same answer a request with a bad parameter gets, because that
// is what it is. An unservable revision is the separate, richer error that
// names what would work.
func mcpValidateModernMeta(meta map[string]interface{}) map[string]interface{} {
	version, ok := mcpMetaString(meta, metaProtocolVersion)
	if !ok || version == "" {
		return mcpError(mcpErrInvalidParams,
			"Invalid params: _meta is missing "+metaProtocolVersion)
	}
	if !mcpVersionServed(version) {
		return mcpUnsupportedVersionError(version)
	}
	if _, ok := meta[metaClientCapabilities]; !ok {
		return mcpError(mcpErrInvalidParams,
			"Invalid params: _meta is missing "+metaClientCapabilities)
	}
	return nil
}

// dispatchModern routes a validated modern request to the shared tool layer.
//
// The handlers below are the same ones the legacy era calls: a revision
// changes how a request is framed and what a result must carry, not what a
// search returns. Only the framing lives in this file.
func (h *MCPHandler) dispatchModern(ctx context.Context, method string,
	req map[string]interface{}) (map[string]interface{}, map[string]interface{}) {

	var result map[string]interface{}

	switch method {
	case "server/discover":
		result = h.handleDiscover()
	case "subscriptions/listen":
		result = h.handleSubscriptionsListen(req)
	case "resources/list":
		result = h.handleResourcesList(req)
	case "resources/templates/list":
		result = h.handleResourceTemplatesList(req)
	case "resources/read":
		result = h.handleResourcesRead(ctx, req)
	case "tools/list":
		result = h.handleToolsList(req)
	case "tools/call":
		result = h.handleToolsCall(ctx, req)
	case "prompts/list":
		result = h.handlePromptsList(req)
	case "prompts/get":
		result = h.handlePromptsGet(ctx, req)
	case "completion/complete":
		result = h.handleComplete(ctx, req)
	default:
		if replacement, removed := mcpRemovedMethods[method]; removed {
			return nil, mcpError(mcpErrMethodNotFound,
				"Method not found: "+method+" was removed in MCP "+MCPModernProtocolVersion+" — "+replacement)
		}
		return nil, mcpError(mcpErrMethodNotFound, "Method not found: "+method)
	}

	// Several handlers report failure by putting an error object inside the
	// result. Promote it: an error belongs beside `result`, not inside it, and
	// -32002 must not be emitted by this revision at all.
	if errObj := mcpPromoteNestedError(result, true); errObj != nil {
		return nil, errObj
	}
	return result, nil
}

// mcpPromoteNestedError lifts an error a handler returned inside its result
// map into a real JSON-RPC error object, or returns nil when the result is
// what it claims to be.
//
// The nesting is older than this revision: handlers for resources/read and
// prompts/get have always answered a failure with {"error": {...}} as their
// result, which every client reads as a successful call whose payload happens
// to contain the word "error". Both eras are corrected here, because a result
// that carries an error object was never valid JSON-RPC in either.
//
// modern also renumbers the resource-not-found code: -32002 was the 2025-11-25
// spelling and 2026-07-28 forbids emitting it, replacing it with the ordinary
// invalid-params code.
func mcpPromoteNestedError(result map[string]interface{}, modern bool) map[string]interface{} {
	nested, ok := result["error"].(map[string]interface{})
	if !ok {
		return nil
	}
	code, _ := nested["code"].(int)
	if modern && code == mcpErrLegacyResourceNotFound {
		nested["code"] = mcpErrInvalidParams
	}
	return nested
}
