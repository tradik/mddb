package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// MCP-004: destructive tools ask the human behind the client before acting.
// These tests cover the gate and the 2026-07-28 round trip; mcp_elicit_test.go
// covers the handshake era over stdio and Streamable HTTP.

// confirmTestHandler is a writable handler over a real server, with one
// document in collection "doomed".
func confirmTestHandler(t *testing.T) (*Server, *MCPHandler) {
	t.Helper()
	s, cleanup := newTestServer(t)
	t.Cleanup(cleanup)
	h := NewMCPHandlerWithConfig(NewDirectClient(s), nil, MCPServerInfo{}, "", ModeRW, "")
	resp := h.Handle(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": "add_document", "arguments": map[string]interface{}{
			"collection": "doomed", "key": "k1", "lang": "en", "content_md": "# hello",
		}}})
	if r, _ := resp["result"].(map[string]interface{}); r == nil || r["isError"] == true {
		t.Fatalf("seeding the collection failed: %v", resp)
	}
	if !s.collectionExists("doomed") {
		t.Fatal("seeded collection does not exist")
	}
	return s, h
}

// modernDeleteCollection builds a stateless delete_collection call, declaring
// elicitation when elicit is set, and carrying an answer when one is given.
func modernDeleteCollection(name string, elicit bool, answer map[string]interface{}, state string) map[string]interface{} {
	params := map[string]interface{}{"name": "delete_collection", "arguments": map[string]interface{}{"collection": name}}
	req := modernRequest("tools/call", params)
	if elicit {
		mcpRequestMeta(req)[metaClientCapabilities] = map[string]interface{}{"elicitation": map[string]interface{}{}}
	}
	if answer != nil {
		params["inputResponses"] = map[string]interface{}{mcpConfirmInputKey: answer}
		params["requestState"] = state
	}
	return req
}

func accept(content map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"action": "accept", "content": content}
}

func TestConfirmModeFromEnv(t *testing.T) {
	for in, want := range map[string]mcpConfirmMode{
		"": mcpConfirmOff, "off": mcpConfirmOff, "elicit": mcpConfirmElicit,
		" REQUIRE ": mcpConfirmRequire, "sometimes": mcpConfirmOff,
	} {
		t.Setenv(mcpConfirmEnv, in)
		if got := mcpConfirmModeFromEnv(); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestNeedsConfirmationIsTheDestructiveSet(t *testing.T) {
	for _, name := range []string{"delete_collection", "truncate_revisions", "restore_backup", "delete_documents_batch", "delete_where"} {
		if !mcpNeedsConfirmation(name) {
			t.Errorf("%s is destructive() and must be gated", name)
		}
	}
	for _, name := range []string{"delete_document", "get_stats", "add_document", "no_such_tool"} {
		if mcpNeedsConfirmation(name) {
			t.Errorf("%s must not be gated", name)
		}
	}
}

// askNever fails the test if the gate asks when it should not.
type askNever struct{ t *testing.T }

func (a askNever) confirm(context.Context, mcpConfirmRequest) (map[string]interface{}, error) {
	a.t.Error("the client was asked")
	return nil, nil
}

func TestConfirmGatePolicy(t *testing.T) {
	args := map[string]interface{}{"collection": "c"}
	asker := withMCPConfirmer(context.Background(), askNever{t})

	t.Setenv(mcpConfirmEnv, "off")
	if err := confirmDestructive(asker, "delete_collection", args); err != nil {
		t.Errorf("off: %v", err)
	}
	t.Setenv(mcpConfirmEnv, "require")
	if err := confirmDestructive(asker, "get_stats", nil); err != nil {
		t.Errorf("a read-only tool was gated: %v", err)
	}
	var unavailable *mcpConfirmUnavailableError
	if err := confirmDestructive(context.Background(), "delete_collection", args); !errors.As(err, &unavailable) {
		t.Errorf("require without a way to ask = %v", err)
	}
	t.Setenv(mcpConfirmEnv, "elicit")
	if err := confirmDestructive(context.Background(), "delete_collection", args); err != nil {
		t.Errorf("elicit without a way to ask must behave as before: %v", err)
	}
}

func TestCheckConfirmation(t *testing.T) {
	args := map[string]interface{}{"collection": "c"}
	cases := []struct {
		name   string
		tool   string
		answer map[string]interface{}
		ok     bool
	}{
		{"declined", "delete_where", map[string]interface{}{"action": "decline"}, false},
		{"cancelled", "delete_where", map[string]interface{}{"action": "cancel"}, false},
		{"accepted without yes", "delete_where", accept(map[string]interface{}{"confirm": false}), false},
		{"accepted, no content", "delete_where", map[string]interface{}{"action": "accept"}, false},
		{"yes", "delete_where", accept(map[string]interface{}{"confirm": true}), true},
		{"collection, wrong name", "delete_collection", accept(map[string]interface{}{"confirm": true, "collection": "d"}), false},
		{"collection, no name", "delete_collection", accept(map[string]interface{}{"confirm": true}), false},
		{"collection, name", "delete_collection", accept(map[string]interface{}{"confirm": true, "collection": " c "}), true},
	}
	for _, c := range cases {
		err := checkMCPConfirmation(c.tool, args, c.answer)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v", c.name, err)
		}
		if err != nil && !strings.Contains(err.Error(), "nothing was changed") {
			t.Errorf("%s: refusal does not say nothing changed: %v", c.name, err)
		}
	}
}

func TestConfirmRequestShape(t *testing.T) {
	q := newMCPConfirmRequest("delete_collection", map[string]interface{}{"collection": "c"})
	schema := q.Params["requestedSchema"].(map[string]interface{})
	props := schema["properties"].(map[string]interface{})
	if props["confirm"] == nil || props["collection"] == nil {
		t.Errorf("delete_collection schema = %v", schema)
	}
	if req := schema["required"].([]string); len(req) != 2 {
		t.Errorf("required = %v", req)
	}

	q2 := newMCPConfirmRequest("delete_where", map[string]interface{}{"collection": "c"})
	if props := q2.Params["requestedSchema"].(map[string]interface{})["properties"].(map[string]interface{}); props["collection"] != nil {
		t.Errorf("only delete_collection asks for a typed name: %v", props)
	}
	if q.State == q2.State || q.State == newMCPConfirmRequest("delete_collection", map[string]interface{}{"collection": "d"}).State {
		t.Error("state must differ per tool and per arguments")
	}

	long := newMCPConfirmRequest("delete_documents_batch", map[string]interface{}{"keys": strings.Repeat("k", 5000)})
	if msg := long.Params["message"].(string); len(msg) > mcpConfirmArgsPreview+200 || !strings.Contains(msg, "…") {
		t.Errorf("long arguments were not cut: %d bytes", len(msg))
	}
}

func TestCompleteResultKeepsInputRequired(t *testing.T) {
	h := newModernTestHandler()
	if r := h.mcpCompleteResult("tools/call", map[string]interface{}{"resultType": mcpResultTypeInputRequired}); r["resultType"] != mcpResultTypeInputRequired {
		t.Errorf("resultType = %v", r["resultType"])
	}
}

// The acceptance path for 2026-07-28: the call answers input_required, a
// refusal leaves the collection, a confirmation deletes it.
func TestModernDeleteCollectionRoundTrip(t *testing.T) {
	t.Setenv(mcpConfirmEnv, "elicit")
	s, h := confirmTestHandler(t)

	first := h.Handle(modernDeleteCollection("doomed", true, nil, ""))
	res := first["result"].(map[string]interface{})
	if res["resultType"] != mcpResultTypeInputRequired {
		t.Fatalf("first call = %v", first)
	}
	ask := res["inputRequests"].(map[string]interface{})[mcpConfirmInputKey].(map[string]interface{})
	if ask["method"] != "elicitation/create" {
		t.Errorf("input request = %v", ask)
	}
	state, _ := res["requestState"].(string)
	if state == "" || !s.collectionExists("doomed") {
		t.Fatalf("state %q, collection exists %v", state, s.collectionExists("doomed"))
	}

	declined := h.Handle(modernDeleteCollection("doomed", true, map[string]interface{}{"action": "decline"}, state))
	if r := declined["result"].(map[string]interface{}); r["isError"] != true || r["resultType"] != mcpResultTypeComplete {
		t.Errorf("declined = %v", declined)
	}
	if !s.collectionExists("doomed") {
		t.Fatal("a declined deletion deleted the collection")
	}

	// An answer carrying another call's state is not an answer to this one.
	stale := h.Handle(modernDeleteCollection("doomed", true, accept(map[string]interface{}{"confirm": true, "collection": "doomed"}), "other"))
	if stale["result"].(map[string]interface{})["resultType"] != mcpResultTypeInputRequired || !s.collectionExists("doomed") {
		t.Fatalf("stale state = %v", stale)
	}

	ok := h.Handle(modernDeleteCollection("doomed", true, accept(map[string]interface{}{"confirm": true, "collection": "doomed"}), state))
	if r := ok["result"].(map[string]interface{}); r["isError"] == true {
		t.Fatalf("confirmed = %v", ok)
	}
	if s.collectionExists("doomed") {
		t.Error("a confirmed deletion left the collection")
	}
}

func TestModernClientWithoutElicitation(t *testing.T) {
	t.Setenv(mcpConfirmEnv, "require")
	s, h := confirmTestHandler(t)

	refused := h.Handle(modernDeleteCollection("doomed", false, nil, ""))
	e, _ := refused["error"].(map[string]interface{})
	if e == nil || e["code"] != mcpErrMissingClientCapability || !strings.Contains(e["message"].(string), "elicitation") {
		t.Fatalf("require without elicitation = %v", refused)
	}
	if !s.collectionExists("doomed") {
		t.Fatal("a refused call deleted the collection")
	}

	t.Setenv(mcpConfirmEnv, "elicit")
	done := h.Handle(modernDeleteCollection("doomed", false, nil, ""))
	if r, _ := done["result"].(map[string]interface{}); r == nil || r["isError"] == true || s.collectionExists("doomed") {
		t.Errorf("elicit without elicitation must delete as before: %v", done)
	}
}

// A stateless request never uses a confirmer installed further out — the
// one a stdio handshake set up for its own era.
func TestModernRequestIgnoresHandshakeConfirmer(t *testing.T) {
	t.Setenv(mcpConfirmEnv, "require")
	s, h := confirmTestHandler(t)
	ctx := withMCPConfirmer(context.Background(), askNever{t})
	resp := h.HandleContext(ctx, modernDeleteCollection("doomed", false, nil, ""))
	if e, _ := resp["error"].(map[string]interface{}); e == nil || !s.collectionExists("doomed") {
		t.Errorf("resp = %v", resp)
	}
}

func TestInputRequiredErrorNamesTheTool(t *testing.T) {
	e := &mcpInputRequiredError{q: newMCPConfirmRequest("delete_where", nil)}
	if !strings.Contains(e.Error(), "delete_where") || !strings.Contains(e.Error(), "confirmation") {
		t.Errorf("Error() = %q", e.Error())
	}
}
