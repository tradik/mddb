package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The bulk-ingest and autocomplete MCP tools, end to end through the tool
// dispatcher: every refusal, then a submit, status, list and cancel.

func TestMCPBulkIngestTools(t *testing.T) {
	s, cleanup := newBulkTestServer(t)
	defer cleanup()
	ctx := context.Background()
	tools := &MCPToolServer{client: NewDirectClient(s)}
	call := func(tool string, args map[string]any) (string, error) {
		return tools.mcpCallTool(ctx, tool, args)
	}

	// Not in-process, and in-process without a manager.
	assertBulkToolsNeedDirectManager(ctx, t, call)

	// A queue of one and no worker: the first job waits, pending, and the
	// second finds the queue full.
	s.BulkIngest = NewBulkIngestManager(s, 1)
	assertBulkToolRefusals(t, call)
	assertBulkJobLifecycle(t, call)

	// A closed database: listing fails and says so.
	_ = s.DB.Close()
	if _, err := call("bulk_ingest_list", map[string]any{}); err == nil {
		t.Fatal("list over a closed database succeeded")
	}
}

// mcpToolCall calls one MCP tool by name.
type mcpToolCall func(tool string, args map[string]any) (string, error)

// wantErrContaining reports whether err is set and its text holds want.
func wantErrContaining(err error, want string) bool {
	return err != nil && strings.Contains(err.Error(), want)
}

// assertBulkToolsNeedDirectManager checks every bulk tool refuses over a
// remote client, and in-process before a manager exists.
func assertBulkToolsNeedDirectManager(ctx context.Context, t *testing.T, call mcpToolCall) {
	t.Helper()
	remote := &MCPToolServer{client: covDHealthClient{}}
	for _, tool := range []string{"bulk_ingest_submit", "bulk_ingest_status", "bulk_ingest_list", "bulk_ingest_cancel"} {
		if _, err := remote.mcpCallTool(ctx, tool, map[string]any{}); !wantErrContaining(err, "direct") {
			t.Fatalf("%s over a remote client: %v", tool, err)
		}
		if _, err := call(tool, map[string]any{}); !wantErrContaining(err, "not initialized") {
			t.Fatalf("%s without a manager: %v", tool, err)
		}
	}
}

// assertBulkToolRefusals checks submit refuses each malformed request, and
// status and cancel refuse a missing or unknown id.
func assertBulkToolRefusals(t *testing.T, call mcpToolCall) {
	t.Helper()
	for args, want := range map[*map[string]any]string{
		{}:                  "missing collection",
		{"collection": "c"}: "non-empty array",
		{"collection": "c", "documents": []any{}}:    "non-empty array",
		{"collection": "c", "documents": []any{"x"}}: "no documents",
	} {
		if _, err := call("bulk_ingest_submit", *args); !wantErrContaining(err, want) {
			t.Fatalf("submit %v: %v, want %q", *args, err, want)
		}
	}
	for _, tool := range []string{"bulk_ingest_status", "bulk_ingest_cancel"} {
		if _, err := call(tool, map[string]any{}); !wantErrContaining(err, "missing id") {
			t.Fatalf("%s without id: %v", tool, err)
		}
		if _, err := call(tool, map[string]any{"id": "nope"}); !wantErrContaining(err, "not found") {
			t.Fatalf("%s of an unknown job: %v", tool, err)
		}
	}
}

// assertBulkJobLifecycle submits one job into a queue of one (pending), finds
// the queue full for a second, then reads its status, lists and cancels it.
func assertBulkJobLifecycle(t *testing.T, call mcpToolCall) {
	t.Helper()
	doc := map[string]any{"collection": "c", "documents": []any{map[string]any{"key": "a", "lang": "en", "contentMd": "# A",
		"saveRevision": true, "meta": map[string]any{"k": []any{"v"}}}}}
	out, err := call("bulk_ingest_submit", doc)
	if err != nil || !strings.Contains(out, `"pending"`) {
		t.Fatalf("submit: %v %s", err, out)
	}
	if _, err := call("bulk_ingest_submit", doc); !wantErrContaining(err, "queue is full") {
		t.Fatalf("second submit: %v", err)
	}
	id := strings.Split(strings.Split(out, `"id": "`)[1], `"`)[0]
	if out, err := call("bulk_ingest_status", map[string]any{"id": id}); err != nil || !strings.Contains(out, id) {
		t.Fatalf("status: %v %s", err, out)
	}
	if out, err := call("bulk_ingest_list", map[string]any{"collection": "c"}); err != nil || !strings.Contains(out, id) {
		t.Fatalf("list: %v %s", err, out)
	}
	if out, err := call("bulk_ingest_cancel", map[string]any{"id": id}); err != nil || out != `{"status":"cancelled","id":"`+id+`"}` {
		t.Fatalf("cancel: %v %s", err, out)
	}
}

func TestAutocomplete_RefusalsAndFailures(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	ctx := context.Background()
	addTestDoc(t, s, "c", "a", "en", "market marker", nil)

	if _, err := (&MCPToolServer{client: covDHealthClient{}}).mcpCallTool(ctx, "autocomplete", map[string]any{"collection": "c", "q": "m"}); err == nil {
		t.Fatal("autocomplete over a remote client")
	}
	tools := &MCPToolServer{client: NewDirectClient(s)}
	if _, err := tools.mcpCallTool(ctx, "autocomplete", map[string]any{"q": "m"}); err == nil || !strings.Contains(err.Error(), "missing collection") {
		t.Fatalf("autocomplete without a collection: %v", err)
	}
	out, err := tools.mcpCallTool(ctx, "autocomplete", map[string]any{"collection": "c", "q": "mar"})
	if err != nil || !strings.Contains(out, `"market"`) || strings.Contains(out, "limitCapped") {
		t.Fatalf("autocomplete: %v %s", err, out)
	}

	get := func(srv *Server, query string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.handleAutocomplete(rec, httptest.NewRequest(http.MethodGet, "/v1/autocomplete?"+query, nil))
		return rec
	}
	if rec := get(s, "collection=c&q=mar&topN=abc"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "market") {
		t.Fatalf("a topN that is not a number falls back to the default: %d %s", rec.Code, rec.Body.String())
	}
	if rec := get(&Server{}, "collection=c&q=mar"); rec.Code != http.StatusBadRequest {
		t.Fatalf("without full-text search: %d", rec.Code)
	}

	_ = s.collectionDB("c").Close() // the file holding the collection, in either layout
	if rec := get(s, "collection=c&q=mar"); rec.Code != http.StatusBadRequest {
		t.Fatalf("over a closed database: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := tools.mcpCallTool(ctx, "autocomplete", map[string]any{"collection": "c", "q": "mar"}); err == nil {
		t.Fatal("MCP autocomplete over a closed database succeeded")
	}
}
