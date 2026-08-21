// Package main — MCP listener authentication reconciliation (SEC-002).
//
// The standalone MCP HTTP listener (default :9000) historically had only one
// guard: MCPAPIKeyMiddleware, which is a no-op unless MDDB_MCP_API_KEY_ENABLED
// =true. As a result, even with MDDB_AUTH_ENABLED=true the MCP endpoints
// (/mcp, /tools/call, /sse, /message, /resources) accepted anonymous, full
// read/write traffic — a complete bypass of the main API's AuthManager.
//
// decideMCPAuth + applyMCPAuth close that gap by tying MCP exposure to the
// main auth configuration in one auditable place.
package main

import (
	"log/slog"
	"net/http"
	"os"
)

// mcpAuthDecision captures how the MCP listener should be protected relative to
// the main API authentication.
type mcpAuthDecision struct {
	// wrapMainAuth gates the MCP handler with the main AuthManager so the same
	// Bearer / X-API-Key credentials that protect the HTTP API also protect MCP.
	wrapMainAuth bool
	// warnInsecure marks a listener with NO authentication that is reachable
	// beyond loopback — the caller logs a prominent startup warning.
	warnInsecure bool
	// reason is a human-readable explanation used in the emitted log line.
	reason string
}

// decideMCPAuth derives the MCP protection strategy from the relevant flags:
//
//   - main auth on, MCP key off  → wrap MCP with the main AuthManager (closes
//     the unauthenticated-bypass hole; MCP now demands credentials).
//   - main auth on, MCP key on   → MCP is already protected by its own keys.
//   - main auth off, MCP key off → no authentication at all; warn unless the
//     bind is loopback (otherwise anyone on the network gets full R/W).
//   - main auth off, MCP key on  → MCP protected by its own keys.
func decideMCPAuth(authEnabled, mcpKeyEnabled, loopbackBind bool) mcpAuthDecision {
	switch {
	case authEnabled && !mcpKeyEnabled:
		return mcpAuthDecision{
			wrapMainAuth: true,
			reason:       "MDDB_AUTH_ENABLED=true but MCP API-key auth is off — gating MCP with the main AuthManager",
		}
	case !authEnabled && !mcpKeyEnabled && !loopbackBind:
		return mcpAuthDecision{
			warnInsecure: true,
			reason:       "MCP listener has NO authentication and is bound to a non-loopback address",
		}
	default:
		return mcpAuthDecision{}
	}
}

// applyMCPAuth wraps the MCP handler chain according to decideMCPAuth, reading
// the live environment and the listener address. It is the single place that
// reconciles MCP exposure with the main auth configuration.
func (s *Server) applyMCPAuth(next http.Handler, addr string) http.Handler {
	authEnabled := env("MDDB_AUTH_ENABLED", "false") == "true"
	mcpKeyEnabled := os.Getenv("MDDB_MCP_API_KEY_ENABLED") == "true"
	d := decideMCPAuth(authEnabled, mcpKeyEnabled, isLoopbackListenAddr(addr))

	if d.warnInsecure {
		slog.Warn("MCP listener is unauthenticated — anyone who can reach it has full read/write access to the database. "+
			"Set MDDB_MCP_API_KEY_ENABLED=true, enable MDDB_AUTH_ENABLED=true, bind MCP to loopback, "+
			"or disable MCP (MDDB_MCP_ENABLED=false).",
			"reason", d.reason, "addr", addr)
	}

	if d.wrapMainAuth {
		if s.AuthManager == nil {
			slog.Warn("MCP listener stays unauthenticated: AuthManager is nil", "reason", d.reason, "addr", addr)
			return next
		}
		slog.Info("MCP (listener)", "reason", d.reason, "addr", addr)
		return s.AuthManager.HTTPMiddleware(next)
	}
	return next
}
