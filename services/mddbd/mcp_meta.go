package main

import "context"

// The per-request protocol metadata of MCP 2026-07-28 (MCP-001).
//
// Up to 2025-11-25 a connection was opened with `initialize` and everything
// learned there — the protocol revision, who the client is, what it can do,
// how much it wants logged — was remembered for as long as the connection
// lived. 2026-07-28 removes the handshake: each request carries that context
// itself, in `_meta`, and the server holds nothing between requests.
//
// This file is the whole contract in one place: the reserved key names, how to
// read them out of a request, and how to put the server's half back into a
// result. Everything else in the modern path asks these functions rather than
// indexing `_meta` for itself, so a key name appears exactly once.

const (
	// metaProtocolVersion carries the revision a request is written in. It is
	// required on every modern request and is what distinguishes an era.
	metaProtocolVersion = "io.modelcontextprotocol/protocolVersion"
	// metaClientInfo names the client. Self-reported and not verified: for
	// display and logs, never for a decision.
	metaClientInfo = "io.modelcontextprotocol/clientInfo"
	// metaClientCapabilities declares what the client can do for this request.
	// Required: a server may not assume a capability that was not declared.
	metaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	// metaLogLevel replaces logging/setLevel. Absent means the client asked
	// for no log notifications at all, not "use the default".
	metaLogLevel = "io.modelcontextprotocol/logLevel"
	// metaServerInfo is the server's half, returned in every result's _meta.
	metaServerInfo = "io.modelcontextprotocol/serverInfo"
	// metaSubscriptionID correlates a notification with the
	// subscriptions/listen request that opened its stream.
	metaSubscriptionID = "io.modelcontextprotocol/subscriptionId"
)

// mcpResultTypeComplete marks an ordinary, finished result.
//
// The other value the spec defines, "input_required", belongs to multi
// round-trip requests: a server that needs sampling, elicitation or a root
// list from the client returns one instead of issuing its own request. MDDB
// asks the client for nothing — every tool answers from the database — so
// every result it produces is complete. See docs/MCP.md.
const mcpResultTypeComplete = "complete"

// mcpModernMethods are the methods that exist only in the stateless era, so
// receiving one is itself proof of which era the client is in — a legacy
// client has no way to send them.
var mcpModernMethods = map[string]bool{
	"server/discover":      true,
	"subscriptions/listen": true,
}

// mcpParams returns a request's params object, or nil.
func mcpParams(req map[string]interface{}) map[string]interface{} {
	p, _ := req["params"].(map[string]interface{})
	return p
}

// mcpRequestMeta returns a request's `_meta` object, or nil.
func mcpRequestMeta(req map[string]interface{}) map[string]interface{} {
	m, _ := mcpParams(req)["_meta"].(map[string]interface{})
	return m
}

// mcpIsModernRequest reports whether a request is written in the stateless
// era, by the two signals that exist: a method only that era defines, or the
// per-request protocol version only that era sends.
//
// A request with neither is legacy and is served exactly as it was before this
// revision existed. That is the whole backward-compatibility rule.
func mcpIsModernRequest(method string, req map[string]interface{}) bool {
	if mcpModernMethods[method] {
		return true
	}
	_, hasVersion := mcpRequestMeta(req)[metaProtocolVersion]
	return hasVersion
}

// mcpMetaString reads a string-valued `_meta` key.
func mcpMetaString(meta map[string]interface{}, key string) (string, bool) {
	v, ok := meta[key].(string)
	return v, ok
}

// mcpCacheHint is the freshness the server offers for one method's result.
type mcpCacheHint struct {
	TTLMs int
	Scope string
}

const (
	// mcpCachePublic marks a result that carries nothing caller-specific, so a
	// shared gateway may serve it to anyone.
	mcpCachePublic = "public"
	// mcpCachePrivate marks a result that must not cross an authorization
	// context.
	mcpCachePrivate = "private"
)

// mcpCacheHints is the freshness MDDB offers per method. The spec requires
// both fields on these results and nowhere else.
//
// The values are what MDDB can honestly promise rather than what would look
// generous. The lists are built from a static tool table, the built-in prompts
// and a fixed resource catalogue, none of which change while the process runs
// — an hour is safe and identical for every caller, so they are public. A
// resource read is the opposite: it is live database content scoped to the
// caller's authorization, so it is private and immediately stale.
var mcpCacheHints = map[string]mcpCacheHint{
	"server/discover":          {TTLMs: 3600000, Scope: mcpCachePublic},
	"tools/list":               {TTLMs: 3600000, Scope: mcpCachePublic},
	"prompts/list":             {TTLMs: 3600000, Scope: mcpCachePublic},
	"resources/list":           {TTLMs: 3600000, Scope: mcpCachePublic},
	"resources/templates/list": {TTLMs: 3600000, Scope: mcpCachePublic},
	"resources/read":           {TTLMs: 0, Scope: mcpCachePrivate},
}

// mcpCompleteResult stamps a modern result with everything the revision
// requires of one: its type, the server's identity, and the caching hints for
// the methods that must carry them.
func (h *MCPHandler) mcpCompleteResult(method string, result map[string]interface{}) map[string]interface{} {
	if result == nil {
		result = map[string]interface{}{}
	}
	result["resultType"] = mcpResultTypeComplete

	meta, _ := result["_meta"].(map[string]interface{})
	if meta == nil {
		meta = map[string]interface{}{}
	}
	meta[metaServerInfo] = h.buildServerInfo()
	result["_meta"] = meta

	if hint, ok := mcpCacheHints[method]; ok {
		result["ttlMs"] = hint.TTLMs
		result["cacheScope"] = hint.Scope
	}
	return result
}

// mcpLogPolicy is what one modern request asked to be told.
//
// It exists because "how much to log" stopped being a property of the
// connection: logging/setLevel is gone, and a level set by one request must
// not leak into the next one on the same transport. A request that named no
// level gets Emit false, and the spec is explicit that such a request receives
// no notifications/message at all — not "the default level".
type mcpLogPolicy struct {
	Level MCPLogLevel
	Emit  bool
}

type mcpLogPolicyKey struct{}

// withMCPLogPolicy scopes a request's logging preference to that request.
func withMCPLogPolicy(ctx context.Context, p mcpLogPolicy) context.Context {
	return context.WithValue(ctx, mcpLogPolicyKey{}, p)
}

// mcpLogPolicyFrom returns the request's logging preference. The second value
// is false for a legacy request, which has no per-request policy and is served
// from the level its session set.
func mcpLogPolicyFrom(ctx context.Context) (mcpLogPolicy, bool) {
	p, ok := ctx.Value(mcpLogPolicyKey{}).(mcpLogPolicy)
	return p, ok
}

// mcpRequestLogPolicy reads the log level out of a request's `_meta`.
func mcpRequestLogPolicy(meta map[string]interface{}) mcpLogPolicy {
	level, ok := mcpMetaString(meta, metaLogLevel)
	if !ok {
		return mcpLogPolicy{}
	}
	if _, known := mcpLogLevelOrder[MCPLogLevel(level)]; !known {
		return mcpLogPolicy{}
	}
	return mcpLogPolicy{Level: MCPLogLevel(level), Emit: true}
}
