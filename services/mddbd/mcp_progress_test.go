package main

import (
	"context"
	"sync"
	"testing"
)

// GO-021. mcp_progress.go held a working sender that nothing ever reached: no
// request had its progress token read, no transport supplied a delivery
// function, and no tool reported anything. A long reindex looked exactly like a
// hung one.

func TestProgressIsSilentWithoutASender(t *testing.T) {
	// A tool must be able to report unconditionally; the plumbing decides
	// whether anything is delivered.
	ReportProgress(context.Background(), 5, 10, "halfway")
}

func TestProgressReachesTheClientWhenAsked(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]interface{}

	h := NewMCPHandlerWithConfig(nil, nil, MCPServerInfo{}, "", ModeRW, "")
	sender := NewCallbackProgressSender("token-7", func(n map[string]interface{}) {
		mu.Lock()
		got = append(got, n)
		mu.Unlock()
	})
	_ = h

	ctx := withProgressSender(context.Background(), sender)
	ReportProgress(ctx, 3, 10, "three of ten")

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("got %d notifications, want 1", len(got))
	}

	n := got[0]
	if n["method"] != "notifications/progress" {
		t.Errorf("method = %v, want notifications/progress", n["method"])
	}
	params, ok := n["params"].(map[string]interface{})
	if !ok {
		t.Fatalf("notification carries no params: %v", n)
	}
	if params["progressToken"] != "token-7" {
		t.Errorf("the client's token was not echoed back: %v", params["progressToken"])
	}
	if params["message"] != "three of ten" {
		t.Errorf("message = %v", params["message"])
	}
}

// A client that did not send a progress token is not listening for progress;
// sending it anyway is noise the spec says to omit.
func TestNoTokenMeansNoProgress(t *testing.T) {
	var delivered int
	h := NewMCPHandlerWithConfig(nil, nil, MCPServerInfo{}, "", ModeRW, "")

	req := map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "ping",
		"params": map[string]interface{}{},
	}
	h.HandleWithNotifier(req, func(map[string]interface{}) { delivered++ })

	if delivered != 0 {
		t.Errorf("%d notifications were sent to a client that asked for none", delivered)
	}
	if h.progressSender() != nil {
		t.Error("a sender was left behind after the request finished")
	}
}

func TestProgressTokenIsExtractedFromMeta(t *testing.T) {
	req := map[string]interface{}{
		"params": map[string]interface{}{
			"_meta": map[string]interface{}{"progressToken": "abc"},
		},
	}
	if got := ExtractProgressToken(req); got != "abc" {
		t.Errorf("token = %v, want abc", got)
	}

	// Every shape that means "no token" must give nil rather than panic.
	for name, r := range map[string]map[string]interface{}{
		"no params":      {},
		"params not map": {"params": "nope"},
		"no meta":        {"params": map[string]interface{}{}},
		"meta not map":   {"params": map[string]interface{}{"_meta": 7}},
	} {
		if got := ExtractProgressToken(r); got != nil {
			t.Errorf("%s: token = %v, want nil", name, got)
		}
	}
}

// The sender must not outlive the request that installed it, or one client's
// notifications would follow another's call.
func TestSenderIsClearedAfterTheRequest(t *testing.T) {
	h := NewMCPHandlerWithConfig(nil, nil, MCPServerInfo{}, "", ModeRW, "")

	req := map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "ping",
		"params": map[string]interface{}{
			"_meta": map[string]interface{}{"progressToken": "t"},
		},
	}
	h.HandleWithNotifier(req, func(map[string]interface{}) {})

	if h.progressSender() != nil {
		t.Error("the request's progress sender is still installed after it returned")
	}
}

// A transport that cannot deliver mid-request passes nil, and everything must
// behave as it did before progress existed.
func TestNilNotifierBehavesLikePlainHandle(t *testing.T) {
	h := NewMCPHandlerWithConfig(nil, nil, MCPServerInfo{}, "", ModeRW, "")

	req := map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "ping"}
	withNil := h.HandleWithNotifier(req, nil)
	plain := h.Handle(req)

	if (withNil == nil) != (plain == nil) {
		t.Fatalf("nil notifier changed the response shape: %v vs %v", withNil, plain)
	}
}

// logging/setLevel used to be accepted and then ignored — the level was stored
// and never consulted, so no client ever received a log message at any level.

func TestLogMessagesRespectTheClientsLevel(t *testing.T) {
	var got []map[string]interface{}
	notify := func(n map[string]interface{}) { got = append(got, n) }

	h := NewMCPHandlerWithConfig(nil, nil, MCPServerInfo{}, "", ModeRW, "")
	h.notify = notify

	// The default threshold is warning.
	h.logToClient(MCPLogDebug, "test", "too quiet to send")
	if len(got) != 0 {
		t.Fatalf("a debug message was sent to a client asking for warnings: %v", got)
	}

	h.logToClient(MCPLogError, "test", "loud enough")
	if len(got) != 1 {
		t.Fatalf("got %d notifications, want 1", len(got))
	}
	if got[0]["method"] != "notifications/message" {
		t.Errorf("method = %v", got[0]["method"])
	}
	params := got[0]["params"].(map[string]interface{})
	if params["level"] != string(MCPLogError) || params["data"] != "loud enough" {
		t.Errorf("params = %v", params)
	}

	// Lowering the threshold must actually change what is delivered.
	h.handleSetLogLevel(map[string]interface{}{
		"params": map[string]interface{}{"level": string(MCPLogDebug)},
	})
	h.logToClient(MCPLogDebug, "test", "now audible")
	if len(got) != 2 {
		t.Errorf("lowering the level to debug delivered %d messages, want 2", len(got))
	}
}

func TestLogMessagesAreSilentWithoutATransport(t *testing.T) {
	h := NewMCPHandlerWithConfig(nil, nil, MCPServerInfo{}, "", ModeRW, "")
	h.logToClient(MCPLogEmergency, "test", "nobody to tell")
}

// Logging and progress are separate subscriptions: a client that raised its log
// level but sent no progress token must still get its logs.
func TestLogsReachAClientThatSentNoProgressToken(t *testing.T) {
	var got []map[string]interface{}

	h := NewMCPHandlerWithConfig(nil, nil, MCPServerInfo{}, "", ModeRW, "")
	req := map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": "no_such_tool"},
	}
	h.HandleWithNotifier(req, func(n map[string]interface{}) { got = append(got, n) })

	if len(got) != 1 {
		t.Fatalf("a failing tool call sent %d log messages, want 1", len(got))
	}
	if got[0]["method"] != "notifications/message" {
		t.Errorf("method = %v", got[0]["method"])
	}
}
