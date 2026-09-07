package main

import (
	"context"
	"testing"
)

// The per-request metadata contract of MCP 2026-07-28 (MCP-001): caching
// hints, error renumbering, and a log level that belongs to one request
// instead of to a connection.

// Caching hints are required on the six list-and-read operations and on
// nothing else. A tool call is not cacheable — it has effects — and stamping
// it with a TTL would invite a client to skip one.
func TestCacheHintsAppearWhereTheSpecRequiresThem(t *testing.T) {
	h := newModernTestHandler()

	for _, method := range []string{"tools/list", "prompts/list", "resources/list", "server/discover"} {
		result := modernResult(t, h, modernRequest(method, nil))
		if _, ok := result["ttlMs"].(int); !ok {
			t.Errorf("%s carries no ttlMs: %v", method, result)
		}
		if result["cacheScope"] != mcpCachePublic {
			t.Errorf("%s cacheScope = %v, want %q — these lists are the same for every caller",
				method, result["cacheScope"], mcpCachePublic)
		}
	}

	// A document read is live content, scoped to whoever asked for it.
	read := mcpCacheHints["resources/read"]
	if read.TTLMs != 0 || read.Scope != mcpCachePrivate {
		t.Errorf("resources/read hint = %+v, want immediately stale and private", read)
	}

	// completion/complete is not in the cacheable set.
	completion := modernResult(t, h, modernRequest("completion/complete", nil))
	if _, present := completion["ttlMs"]; present {
		t.Errorf("completion/complete was stamped as cacheable: %v", completion)
	}
}

// -32002 was the 2025-11-25 spelling of "no such resource". The new revision
// forbids emitting it and uses plain invalid-params instead, so the same
// failure has to answer differently depending on who asked.
func TestResourceNotFoundIsRenumberedForStatelessClients(t *testing.T) {
	h := newModernTestHandler()

	code, _ := modernError(t, h, modernRequest("resources/read",
		map[string]interface{}{"uri": "nope://missing"}))
	if code != mcpErrInvalidParams {
		t.Errorf("stateless: code = %d, want %d — this revision must not emit %d",
			code, mcpErrInvalidParams, mcpErrLegacyResourceNotFound)
	}

	resp := h.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "resources/read",
		"params": map[string]interface{}{"uri": "nope://missing"},
	})
	legacyErr, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("legacy resources/read did not fail: %v", resp)
	}
	if legacyCode, _ := legacyErr["code"].(int); legacyCode != mcpErrLegacyResourceNotFound {
		t.Errorf("legacy: code = %v, want %d — the older revision keeps its own code",
			legacyErr["code"], mcpErrLegacyResourceNotFound)
	}
}

// logging/setLevel is gone. The level now arrives with each request and applies
// to that request only — and a request that named no level gets no log
// notifications at all, which the spec states as a MUST NOT rather than as a
// default to fall back on.
func TestALogLevelBelongsToOneRequest(t *testing.T) {
	var got []map[string]interface{}
	h := NewMCPHandlerWithConfig(nil, nil, MCPServerInfo{}, "", ModeRW, "")
	h.notify = func(n map[string]interface{}) { got = append(got, n) }

	silent := withMCPLogPolicy(context.Background(), mcpRequestLogPolicy(map[string]interface{}{}))
	h.logToClient(silent, MCPLogEmergency, "test", "nobody asked for this")
	if len(got) != 0 {
		t.Fatalf("a request that asked for no logs received %d: %v", len(got), got)
	}

	loud := withMCPLogPolicy(context.Background(), mcpRequestLogPolicy(map[string]interface{}{
		metaLogLevel: string(MCPLogDebug),
	}))
	h.logToClient(loud, MCPLogDebug, "test", "asked for and delivered")
	if len(got) != 1 {
		t.Fatalf("a request that asked for debug received %d messages, want 1", len(got))
	}

	// Nothing the first request asked for may survive into the next one.
	h.logToClient(silent, MCPLogDebug, "test", "the level must not have leaked")
	if len(got) != 1 {
		t.Errorf("a level set by one request leaked into another: %v", got)
	}
}

// An unknown level is not a level. It silences rather than guessing, because
// guessing would deliver messages to a client that never asked for any.
func TestAnUnknownLogLevelIsNotHonoured(t *testing.T) {
	policy := mcpRequestLogPolicy(map[string]interface{}{metaLogLevel: "superduper"})
	if policy.Emit {
		t.Errorf("policy = %+v, want silence", policy)
	}
}

// The acknowledgement names the subset the server will actually deliver.
// MDDB delivers nothing — no list here changes while the process runs — and
// saying so is the difference between a client relying on a notification and a
// client relying on the ttlMs it was given.
func TestASubscriptionAcknowledgesOnlyWhatCanBeDelivered(t *testing.T) {
	req := modernRequest("subscriptions/listen", map[string]interface{}{
		"notifications": map[string]interface{}{
			"toolsListChanged":      true,
			"resourceSubscriptions": []interface{}{"mddb://health"},
		},
	})

	filter := mcpParseNotificationFilter(req)
	if !filter.ToolsListChanged || len(filter.ResourceSubscriptions) != 1 {
		t.Errorf("filter = %+v, want the client's request read back faithfully", filter)
	}

	ack := mcpSubscriptionAck(req["id"], mcpHonoredNotifications(filter))
	if ack["method"] != "notifications/subscriptions/acknowledged" {
		t.Errorf("method = %v", ack["method"])
	}
	params, _ := ack["params"].(map[string]interface{})
	meta, _ := params["_meta"].(map[string]interface{})
	if meta[metaSubscriptionID] != req["id"] {
		t.Errorf("subscriptionId = %v, want the listen request's own id", meta[metaSubscriptionID])
	}
	honored, _ := params["notifications"].(map[string]interface{})
	if len(honored) != 0 {
		t.Errorf("honored = %v, want nothing promised that cannot arrive", honored)
	}

	// The subscription ends where it began, and says so on the request's id.
	result := modernResult(t, newModernTestHandler(), req)
	resultMeta, _ := result["_meta"].(map[string]interface{})
	if resultMeta[metaSubscriptionID] != req["id"] {
		t.Errorf("closing result names subscription %v, want %v", resultMeta[metaSubscriptionID], req["id"])
	}
	if result["resultType"] != mcpResultTypeComplete {
		t.Errorf("resultType = %v", result["resultType"])
	}
}

// A filter that names nothing is legal, and so is one whose fields are the
// wrong type: both mean "subscribed to nothing", and neither may panic.
func TestASubscriptionFilterIsReadDefensively(t *testing.T) {
	for _, params := range []map[string]interface{}{
		nil,
		{"notifications": "not an object"},
		{"notifications": map[string]interface{}{"toolsListChanged": "yes", "resourceSubscriptions": []interface{}{42}}},
	} {
		req := modernRequest("subscriptions/listen", params)
		if filter := mcpParseNotificationFilter(req); filter.ToolsListChanged || len(filter.ResourceSubscriptions) != 0 {
			t.Errorf("params %v produced %+v, want an empty filter", params, filter)
		}
	}
}

// The dispatch table is the place a method goes missing without anyone
// noticing: a stateless client would be told "method not found" for something
// the server has implemented all along. Every method the revision keeps is
// exercised here, against a real database rather than a stub.
func TestEveryMethodTheRevisionKeepsIsReachable(t *testing.T) {
	srv, cleanup := newHandlerTestServer(t)
	defer cleanup()
	h := NewMCPHandlerWithConfig(NewDirectClient(srv), nil, MCPServerInfo{Name: "mddbd"}, "", ModeRW, "")

	cases := []struct {
		method string
		params map[string]interface{}
		expect string // a key the result must carry
	}{
		{"tools/list", nil, "tools"},
		{"prompts/list", nil, "prompts"},
		{"resources/list", nil, "resources"},
		{"resources/read", map[string]interface{}{"uri": "mddb://health"}, "contents"},
		{"prompts/get", map[string]interface{}{
			"name":      "analyze-collection",
			"arguments": map[string]interface{}{"collection": "docs"},
		}, "messages"},
		{"completion/complete", map[string]interface{}{
			"ref":      map[string]interface{}{"type": "ref/prompt", "name": "analyze-collection"},
			"argument": map[string]interface{}{"name": "collection", "value": ""},
		}, "completion"},
		{"tools/call", map[string]interface{}{
			"name": "mddb_health", "arguments": map[string]interface{}{},
		}, "content"},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			result := modernResult(t, h, modernRequest(tc.method, tc.params))
			if _, ok := result[tc.expect]; !ok {
				t.Errorf("result carries no %q: %v", tc.expect, result)
			}
			if result["resultType"] != mcpResultTypeComplete {
				t.Errorf("resultType = %v", result["resultType"])
			}
		})
	}
}

// A result the handler built as nil is still a result, and still has to carry
// what the revision requires of one.
func TestAnEmptyResultIsStillStamped(t *testing.T) {
	result := newModernTestHandler().mcpCompleteResult("tools/list", nil)

	if result["resultType"] != mcpResultTypeComplete {
		t.Errorf("resultType = %v", result["resultType"])
	}
	if _, ok := result["_meta"].(map[string]interface{}); !ok {
		t.Errorf("no _meta in %v", result)
	}
}
