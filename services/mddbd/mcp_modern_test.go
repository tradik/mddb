package main

import (
	"testing"
)

// MCP 2026-07-28, the stateless era (MCP-001).
//
// The pair of properties every test here circles: a client that sends its
// protocol version with each request is served by the new rules, and a client
// that does not is served by the old ones — on the same handler, with no
// connection state deciding between them.

// modernMeta is the `_meta` every stateless request carries.
func modernMeta() map[string]interface{} {
	return map[string]interface{}{
		metaProtocolVersion:    MCPModernProtocolVersion,
		metaClientCapabilities: map[string]interface{}{},
		metaClientInfo:         map[string]interface{}{"name": "test-client", "version": "1.0.0"},
	}
}

// modernRequest builds a stateless request, merging `_meta` into its params.
func modernRequest(method string, params map[string]interface{}) map[string]interface{} {
	if params == nil {
		params = map[string]interface{}{}
	}
	params["_meta"] = modernMeta()
	return map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
}

func newModernTestHandler() *MCPHandler {
	return NewMCPHandlerWithConfig(nil, nil, MCPServerInfo{Name: "mddbd"}, "How to use this server.", ModeRW, "")
}

// modernResult runs a request and insists it succeeded.
func modernResult(t *testing.T, h *MCPHandler, req map[string]interface{}) map[string]interface{} {
	t.Helper()
	resp := h.Handle(req)
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("%v returned no result: %v", req["method"], resp)
	}
	return result
}

// modernError runs a request and insists it failed, returning the error code.
func modernError(t *testing.T, h *MCPHandler, req map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	resp := h.Handle(req)
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("%v was expected to fail and did not: %v", req["method"], resp)
	}
	code, _ := errObj["code"].(int)
	return code, errObj
}

// server/discover is the one RPC the revision requires of every server, and
// the only way a client can ask what a server is without guessing a revision
// first.
func TestDiscoverNamesEveryRevisionAndTheServer(t *testing.T) {
	withSupportedVersions(t, MCPModernProtocolVersion, MCPProtocolVersion)
	h := newModernTestHandler()

	result := modernResult(t, h, modernRequest("server/discover", nil))

	versions, _ := result["supportedVersions"].([]string)
	if len(versions) != 2 || versions[0] != MCPModernProtocolVersion {
		t.Errorf("supportedVersions = %v, want both revisions, newest first", result["supportedVersions"])
	}
	if _, ok := result["capabilities"].(map[string]interface{}); !ok {
		t.Errorf("no capabilities in %v", result)
	}
	if result["instructions"] != "How to use this server." {
		t.Errorf("instructions = %v", result["instructions"])
	}
	if result["resultType"] != mcpResultTypeComplete {
		t.Errorf("resultType = %v, want %q", result["resultType"], mcpResultTypeComplete)
	}
	meta, _ := result["_meta"].(map[string]interface{})
	info, _ := meta[metaServerInfo].(map[string]interface{})
	if info["name"] != "mddbd" || info["version"] != VERSION {
		t.Errorf("serverInfo = %v, want this build's name and version", meta[metaServerInfo])
	}
}

// A pinned server must not advertise what it has been told to refuse: a client
// would select a revision from the list and be rejected by its next call. The
// pin narrows the answer and the acceptance together, or it is not a pin.
func TestDiscoverHonoursThePin(t *testing.T) {
	withSupportedVersions(t, MCPModernProtocolVersion, MCPProtocolVersion)
	t.Setenv(mcpProtocolVersionEnv, MCPModernProtocolVersion)

	result := modernResult(t, newModernTestHandler(), modernRequest("server/discover", nil))

	versions, _ := result["supportedVersions"].([]string)
	if len(versions) != 1 || versions[0] != MCPModernProtocolVersion {
		t.Errorf("supportedVersions = %v, want only the pinned revision", result["supportedVersions"])
	}
}

// The pin points the other way too: pinned to the handshake revision, a
// stateless request is refused and told what this server does serve. A client
// probing with server/discover learns the same thing from the refusal as it
// would have from a result, which is why the refusal carries the list.
func TestAPinToTheHandshakeRevisionRefusesStatelessRequests(t *testing.T) {
	withSupportedVersions(t, MCPModernProtocolVersion, MCPProtocolVersion)
	t.Setenv(mcpProtocolVersionEnv, MCPProtocolVersion)

	code, errObj := modernError(t, newModernTestHandler(), modernRequest("server/discover", nil))

	if code != mcpErrUnsupportedProtocolVersion {
		t.Fatalf("code = %d, want %d", code, mcpErrUnsupportedProtocolVersion)
	}
	data, _ := errObj["data"].(map[string]interface{})
	supported, _ := data["supported"].([]string)
	if len(supported) != 1 || supported[0] != MCPProtocolVersion {
		t.Errorf("data.supported = %v, want only the pinned revision", data["supported"])
	}
}

// The point of the revision: no handshake, no session, nothing established
// before the call the client actually wanted to make.
func TestAStatelessClientNeedsNoHandshake(t *testing.T) {
	h := newModernTestHandler()

	result := modernResult(t, h, modernRequest("tools/list", nil))

	tools, _ := result["tools"].([]MCPTool)
	if len(tools) == 0 {
		t.Fatalf("tools/list returned nothing: %v", result)
	}
	if result["resultType"] != mcpResultTypeComplete {
		t.Errorf("resultType = %v", result["resultType"])
	}
}

// The other half of MCP-001's acceptance: the older revision keeps working,
// proven rather than assumed. A legacy result carries none of the new
// furniture — a 2025-11-25 client would not know what to do with it.
func TestALegacyClientIsServedExactlyAsBefore(t *testing.T) {
	h := newModernTestHandler()

	resp := h.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]interface{}{"protocolVersion": MCPProtocolVersion},
	})
	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("initialize failed: %v", resp)
	}
	if result["protocolVersion"] != MCPProtocolVersion {
		t.Errorf("protocolVersion = %v", result["protocolVersion"])
	}

	list := modernResult(t, h, map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	})
	for _, modernOnly := range []string{"resultType", "ttlMs", "cacheScope", "_meta"} {
		if _, present := list[modernOnly]; present {
			t.Errorf("a legacy result carries %q, which its revision does not define", modernOnly)
		}
	}
}

// A client that asks for a revision this build does not serve is told which
// ones it does, so it can retry rather than give up.
func TestAnUnservedRevisionNamesTheOnesThatWork(t *testing.T) {
	withSupportedVersions(t, MCPModernProtocolVersion, MCPProtocolVersion)
	req := modernRequest("tools/list", nil)
	req["params"].(map[string]interface{})["_meta"].(map[string]interface{})[metaProtocolVersion] = "1900-01-01"

	code, errObj := modernError(t, newModernTestHandler(), req)

	if code != mcpErrUnsupportedProtocolVersion {
		t.Errorf("code = %d, want %d", code, mcpErrUnsupportedProtocolVersion)
	}
	data, _ := errObj["data"].(map[string]interface{})
	if supported, _ := data["supported"].([]string); len(supported) != 2 {
		t.Errorf("data.supported = %v, want every revision this build serves", data["supported"])
	}
	if data["requested"] != "1900-01-01" {
		t.Errorf("data.requested = %v, want the revision the client asked for", data["requested"])
	}
}

// Both fields are required on every request, and a request missing one is
// malformed rather than unsupported — which is a different error and a
// different thing for the client to do about it.
func TestAModernRequestMustCarryItsVersionAndCapabilities(t *testing.T) {
	h := newModernTestHandler()

	noCapabilities := modernRequest("tools/list", nil)
	delete(noCapabilities["params"].(map[string]interface{})["_meta"].(map[string]interface{}), metaClientCapabilities)
	if code, _ := modernError(t, h, noCapabilities); code != mcpErrInvalidParams {
		t.Errorf("missing clientCapabilities: code = %d, want %d", code, mcpErrInvalidParams)
	}

	// server/discover is modern by its own name, so a version-less one is a
	// malformed modern request rather than an unknown legacy method.
	noVersion := map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "server/discover"}
	if code, _ := modernError(t, h, noVersion); code != mcpErrInvalidParams {
		t.Errorf("missing protocolVersion: code = %d, want %d", code, mcpErrInvalidParams)
	}
}

// The methods the revision deleted are gone for the clients that speak it, and
// only for them. The error says what to do instead, because a client sending
// `initialize` with modern metadata has an era bug, not a typo.
func TestRemovedMethodsAreGoneOnlyForStatelessClients(t *testing.T) {
	h := newModernTestHandler()

	for _, method := range []string{"initialize", "ping", "logging/setLevel", "resources/subscribe"} {
		code, errObj := modernError(t, h, modernRequest(method, nil))
		if code != mcpErrMethodNotFound {
			t.Errorf("%s: code = %d, want %d", method, code, mcpErrMethodNotFound)
		}
		if message, _ := errObj["message"].(string); message == "Method not found" {
			t.Errorf("%s: the error says nothing about what replaced it", method)
		}
	}

	// The same methods, from a client that never declared a revision.
	if resp := h.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "ping",
	}); resp["error"] != nil {
		t.Errorf("ping was refused to a legacy client: %v", resp)
	}
}
