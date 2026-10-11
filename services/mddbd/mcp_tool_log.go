package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"

	"mddb/internal/metrics"
)

// MCP tool-call observability.
//
// The request logger (MCPRequestLogger) sees one HTTP request: method, path,
// status. Every tool call arrives as POST /mcp with status 200, so in that
// log a version_check for "golang" and a lookup for a package the database
// has never heard of are the same line. An operator asking "what do people
// query, and what do they ask for that we do not have?" had nothing to read.
//
// This file adds the per-call view. Each tools/call emits one structured
// record (behind MDDB_MCP_LOGGING_ENABLED, the same switch as the request
// log) naming the tool, the query argument, how many documents the result
// carried, and who asked. Three operation counters land in /metrics whether
// or not the log is on, because a counter per tool is cheap and a dashboard
// needs a rate:
//
//	mddb_operations_total{operation="mcp_tool_call",label="<tool>"}
//	mddb_operations_total{operation="mcp_tool_miss",label="<tool>"}   result carried 0 documents
//	mddb_operations_total{operation="mcp_tool_error",label="<tool>"}  the call failed

// mcpCaller is what the HTTP middleware already resolved about the client:
// the API key's name, the client IP and the session. It travels in the
// context so the handler can log it without holding the request.
type mcpCaller struct {
	keyName   string
	clientIP  string
	sessionID string
}

type mcpCallerContextKey struct{}

// withMCPCaller records the caller of r in ctx. A nil request (stdio, tests)
// leaves ctx as it was.
func withMCPCaller(ctx context.Context, r *http.Request) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, mcpCallerContextKey{}, mcpCaller{
		keyName:   r.Header.Get("X-MCP-Key-Name"),
		clientIP:  clientIP(r),
		sessionID: r.Header.Get("MCP-Session-Id"),
	})
}

// mcpCallerFrom returns the caller recorded by withMCPCaller, or the zero
// value when there is no request behind ctx.
func mcpCallerFrom(ctx context.Context) mcpCaller {
	c, _ := ctx.Value(mcpCallerContextKey{}).(mcpCaller)
	return c
}

// mcpToolLoggingEnabled reads the switch the request logger uses, so one
// variable turns on both views of the same traffic.
func mcpToolLoggingEnabled() bool {
	return os.Getenv("MDDB_MCP_LOGGING_ENABLED") == "true"
}

// mcpToolQueryMaxLen bounds the query copied into the log. A query is a
// package name or a short phrase; anything longer is a pasted document, and
// the log is not the place for it.
const mcpToolQueryMaxLen = 200

// mcpResultHits reads how many documents a tool result carried. Search-shaped
// results say so in "total"; a few list results carry only the array. A
// result that is neither (a backup path, a stats blob, plain text) reports
// known=false, and the caller logs no count rather than a misleading zero.
func mcpResultHits(result string) (hits int, known bool) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result), &body); err != nil {
		return 0, false
	}
	if raw, ok := body["total"]; ok {
		var n float64
		if json.Unmarshal(raw, &n) == nil {
			return int(n), true
		}
	}
	for _, key := range []string{"results", "documents"} {
		if raw, ok := body[key]; ok {
			var items []json.RawMessage
			if json.Unmarshal(raw, &items) == nil {
				return len(items), true
			}
		}
	}
	return 0, false
}

// mcpToolQuery returns the call's query argument, cut to mcpToolQueryMaxLen
// runes so a multi-byte character is never split.
func mcpToolQuery(args map[string]interface{}) string {
	q := mcpGetString(args, "query")
	runes := []rune(q)
	if len(runes) > mcpToolQueryMaxLen {
		return string(runes[:mcpToolQueryMaxLen])
	}
	return q
}

// mcpToolObserver records the outcome of tool calls. The zero value counts
// nothing and logs nothing, so a handler built without one behaves as before.
type mcpToolObserver struct {
	logEnabled bool
	metrics    *metrics.Metrics // nil is a no-op collector
}

// newMCPToolObserver builds an observer from the environment switch and the
// server's metrics collector, which may be nil.
func newMCPToolObserver(m *metrics.Metrics) mcpToolObserver {
	return mcpToolObserver{logEnabled: mcpToolLoggingEnabled(), metrics: m}
}

// WithMetrics points the handler's tool-call counters at m and returns h, so
// construction stays a single expression.
func (h *MCPHandler) WithMetrics(m *metrics.Metrics) *MCPHandler {
	h.observer.metrics = m
	return h
}

// observe counts one call and, when logging is on, writes its record.
func (o mcpToolObserver) observe(ctx context.Context, tool string, args map[string]interface{}, result string, err error, elapsed time.Duration) {
	o.metrics.IncOp("mcp_tool_call", tool)

	hits, known := 0, false
	if err != nil {
		o.metrics.IncOp("mcp_tool_error", tool)
	} else if hits, known = mcpResultHits(result); known && hits == 0 {
		o.metrics.IncOp("mcp_tool_miss", tool)
	}

	if !o.logEnabled {
		return
	}

	attrs := []any{
		"tool", tool,
		"query", mcpToolQuery(args),
		"duration_ms", elapsed.Milliseconds(),
	}
	if known {
		attrs = append(attrs, "hits", hits, "found", hits > 0)
	}
	caller := mcpCallerFrom(ctx)
	if caller.keyName != "" {
		attrs = append(attrs, "key_name", caller.keyName)
	}
	if caller.clientIP != "" {
		attrs = append(attrs, "client_ip", caller.clientIP)
	}
	if caller.sessionID != "" {
		attrs = append(attrs, "session_id", caller.sessionID)
	}
	if err != nil {
		attrs = append(attrs, "error", err.Error())
		slog.Warn("MCP tool call", attrs...) // #nosec G706 -- structured attributes, query bounded
		return
	}
	slog.Info("MCP tool call", attrs...) // #nosec G706 -- structured attributes, query bounded
}
