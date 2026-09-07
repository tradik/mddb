package main

// server/discover — the one RPC MCP 2026-07-28 requires every server to
// implement (MCP-001).
//
// With the handshake gone, this is where a client can still ask "what are
// you, and which revisions do you answer for?" in a single call. It is
// optional for the client: it may instead send any request it likes and read
// the UnsupportedProtocolVersionError if it guessed wrong. On stdio, where
// there is no HTTP status to fall back on, it is also the probe that tells a
// dual-era client which era it is talking to.

// mcpModernCapabilities describes what MDDB can do for a stateless client.
//
// listChanged is false everywhere, and that is not an oversight. The tool
// table, the prompt list and the resource descriptors are all built once at
// startup and never change while the process runs, so there is no moment at
// which MDDB could send notifications/tools/list_changed. Declaring the
// capability would promise a notification that cannot arrive and would keep a
// client from relying on the ttlMs it is given instead.
//
// The legacy `initialize` reply still declares listChanged: true, wrongly, and
// is left alone on purpose: changing what an established handshake advertises
// is a behaviour change for clients that already negotiated against it, and
// MCP-001 asks for the older revision to keep working unchanged. It is worth
// correcting on the next revision bump of that path.
func mcpModernCapabilities() map[string]interface{} {
	return map[string]interface{}{
		"tools": map[string]interface{}{
			"listChanged": false,
		},
		"resources": map[string]interface{}{
			// Resource subscriptions would be delivered through
			// subscriptions/listen, and MDDB has nothing that changes to
			// deliver. See mcp_subscriptions.go.
			"subscribe":   false,
			"listChanged": false,
		},
		"prompts": map[string]interface{}{
			"listChanged": false,
		},
		// Logging is deprecated in this revision but still functional, and
		// MDDB does emit notifications/message for a request that asks for
		// them with io.modelcontextprotocol/logLevel.
		"logging":     map[string]interface{}{},
		"completions": map[string]interface{}{},
	}
}

// handleDiscover answers server/discover.
//
// supportedVersions names every revision this server will answer for, with the
// operator's pin applied — a pinned server must not advertise revisions it has
// been told to refuse, or a client will select one and be rejected by the next
// call it makes.
func (h *MCPHandler) handleDiscover() map[string]interface{} {
	result := map[string]interface{}{
		"supportedVersions": activeMCPVersions(),
		"capabilities":      mcpModernCapabilities(),
	}
	if h.instructions != "" {
		result["instructions"] = h.instructions
	}
	return result
}
