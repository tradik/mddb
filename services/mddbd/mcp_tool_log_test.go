package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/metrics"
)

// fakeFTSClient answers every full_text_search with a fixed response, so a
// test can decide whether a query is a hit or a miss.
type fakeFTSClient struct {
	MCPClient
	resp *MCPFTSSearchResponse
	err  error
}

func (f *fakeFTSClient) FTSSearch(_ context.Context, _ *MCPFTSSearchRequest) (*MCPFTSSearchResponse, error) {
	return f.resp, f.err
}

// toolLogStubStats satisfies metrics.StatsProvider so HandleMetrics can render.
type toolLogStubStats struct{}

func (toolLogStubStats) Mode() string                                     { return "rw" }
func (toolLogStubStats) VectorIndexReady() bool                           { return false }
func (toolLogStubStats) EmbeddingConfigured() bool                        { return false }
func (toolLogStubStats) EmbeddingQueueSize() (int, bool)                  { return 0, false }
func (toolLogStubStats) EmbeddingCacheStats() (uint64, uint64, int, bool) { return 0, 0, 0, false }
func (toolLogStubStats) ReplicationRole() string                          { return "" }
func (toolLogStubStats) BinlogStats() (metrics.BinlogStatsView, bool) {
	return metrics.BinlogStatsView{}, false
}
func (toolLogStubStats) DBStats() metrics.DBStats    { return metrics.DBStats{} }
func (toolLogStubStats) DBWriteTransactions() uint64 { return 0 }

var versionCheckTool = MCPCustomToolConfig{
	Name:     "version_check",
	Action:   "full_text_search",
	Defaults: MCPCustomToolDefs{Collection: "versions", Limit: 1},
	Parameters: []MCPCustomToolParam{
		{Name: "query", Type: "string", Required: true},
	},
}

// newObservedHandler builds a handler whose only tool is version_check,
// backed by client, with tool logging on and a live metrics collector.
func newObservedHandler(client MCPClient) (*MCPHandler, *metrics.Metrics) {
	m := metrics.NewMetrics(true, toolLogStubStats{})
	h := NewMCPHandlerWithConfig(client, []MCPCustomToolConfig{versionCheckTool},
		MCPServerInfo{Name: "test"}, "", ModeRead, "").WithMetrics(m)
	h.observer.logEnabled = true
	return h, m
}

func toolsCall(name string, args map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": name, "arguments": args},
	}
}

// toolCallRecords keeps only the tool-call records out of a capture.
func toolCallRecords(recs []map[string]any) []map[string]any {
	var out []map[string]any
	for _, r := range recs {
		if r["msg"] == "MCP tool call" {
			out = append(out, r)
		}
	}
	return out
}

func renderMetrics(t *testing.T, m *metrics.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.HandleMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

func TestMCPResultHits(t *testing.T) {
	cases := []struct {
		name   string
		result string
		hits   int
		known  bool
	}{
		{"total wins", `{"results":[{}],"total":3}`, 3, true},
		{"zero total is a miss", `{"results":[],"total":0}`, 0, true},
		{"results array without total", `{"results":[{},{}]}`, 2, true},
		{"documents array", `{"documents":[{}],"other":1}`, 1, true},
		{"total that is not a number falls back", `{"total":"many","results":[{}]}`, 1, true},
		{"stats blob", `{"collections":5}`, 0, false},
		{"plain text", `Document deleted: a/b`, 0, false},
		{"array body", `[1,2,3]`, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits, known := mcpResultHits(tc.result)
			if hits != tc.hits || known != tc.known {
				t.Errorf("mcpResultHits(%q) = (%d, %v), want (%d, %v)", tc.result, hits, known, tc.hits, tc.known)
			}
		})
	}
}

func TestMCPToolQueryIsBoundedByRunes(t *testing.T) {
	long := strings.Repeat("ą", mcpToolQueryMaxLen+5)
	got := mcpToolQuery(map[string]interface{}{"query": long})
	if n := len([]rune(got)); n != mcpToolQueryMaxLen {
		t.Errorf("truncated query holds %d runes, want %d", n, mcpToolQueryMaxLen)
	}
	if got := mcpToolQuery(map[string]interface{}{"query": "golang"}); got != "golang" {
		t.Errorf("short query changed: %q", got)
	}
	if got := mcpToolQuery(map[string]interface{}{"query": 7}); got != "" {
		t.Errorf("non-string query should be empty, got %q", got)
	}
}

func TestMCPCallerTravelsInContext(t *testing.T) {
	if c := mcpCallerFrom(context.Background()); c != (mcpCaller{}) {
		t.Errorf("background context should carry no caller, got %+v", c)
	}
	ctx := context.Background()
	if withMCPCaller(ctx, nil) != ctx {
		t.Error("a nil request must leave the context untouched")
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.RemoteAddr = "10.1.2.3:4444"
	req.Header.Set("X-MCP-Key-Name", "claude-prod")
	req.Header.Set("MCP-Session-Id", "sess-1")
	c := mcpCallerFrom(withMCPCaller(ctx, req))
	want := mcpCaller{keyName: "claude-prod", clientIP: "10.1.2.3", sessionID: "sess-1"}
	if c != want {
		t.Errorf("caller = %+v, want %+v", c, want)
	}
}

func TestMCPToolLoggingEnabledReadsEnv(t *testing.T) {
	t.Setenv("MDDB_MCP_LOGGING_ENABLED", "")
	if mcpToolLoggingEnabled() || newMCPToolObserver(nil).logEnabled {
		t.Error("unset variable must leave tool logging off")
	}
	t.Setenv("MDDB_MCP_LOGGING_ENABLED", "true")
	if !mcpToolLoggingEnabled() || !newMCPToolObserver(nil).logEnabled {
		t.Error("MDDB_MCP_LOGGING_ENABLED=true must turn tool logging on")
	}
}

func TestToolCallIsLoggedAndCountedAsHitOrMiss(t *testing.T) {
	client := &fakeFTSClient{resp: &MCPFTSSearchResponse{Results: nil, Total: 0}}
	h, m := newObservedHandler(client)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("X-MCP-Key-Name", "claude-prod")
	req.Header.Set("MCP-Session-Id", "sess-42")
	ctx := withMCPCaller(context.Background(), req)

	recs := toolCallRecords(captureLogs(t, func() {
		h.HandleContext(ctx, toolsCall("version_check", map[string]interface{}{"query": "alloy"}))
		client.resp = &MCPFTSSearchResponse{Results: []MCPFTSResult{{}}, Total: 1}
		h.HandleContext(ctx, toolsCall("version_check", map[string]interface{}{"query": "golang"}))
	}))
	if len(recs) != 2 {
		t.Fatalf("expected two tool-call records, got %d", len(recs))
	}

	miss := recs[0]
	if miss["level"] != "INFO" || miss["tool"] != "version_check" || miss["query"] != "alloy" {
		t.Errorf("miss record = %v", miss)
	}
	if miss["hits"] != float64(0) || miss["found"] != false {
		t.Errorf("miss should report hits=0 found=false, got %v", miss)
	}
	if miss["key_name"] != "claude-prod" || miss["client_ip"] != "203.0.113.9" || miss["session_id"] != "sess-42" {
		t.Errorf("caller fields missing: %v", miss)
	}
	if _, ok := miss["duration_ms"]; !ok {
		t.Error("duration_ms is missing")
	}

	hit := recs[1]
	if hit["query"] != "golang" || hit["hits"] != float64(1) || hit["found"] != true {
		t.Errorf("hit record = %v", hit)
	}

	out := renderMetrics(t, m)
	for _, line := range []string{
		`mddb_operations_total{operation="mcp_tool_call",label="version_check"} 2`,
		`mddb_operations_total{operation="mcp_tool_miss",label="version_check"} 1`,
	} {
		if !strings.Contains(out, line) {
			t.Errorf("metrics output lacks %q:\n%s", line, out)
		}
	}
	if strings.Contains(out, `operation="mcp_tool_error"`) {
		t.Error("no error counter expected for successful calls")
	}
}

func TestFailedToolCallIsLoggedAtWarnAndCounted(t *testing.T) {
	client := &fakeFTSClient{err: errors.New("index offline")}
	h, m := newObservedHandler(client)

	recs := toolCallRecords(captureLogs(t, func() {
		h.HandleContext(context.Background(), toolsCall("version_check", map[string]interface{}{"query": "rust"}))
		h.HandleContext(context.Background(), toolsCall("no_such_tool", nil))
	}))
	if len(recs) != 2 {
		t.Fatalf("expected two records, got %d", len(recs))
	}
	if recs[0]["level"] != "WARN" || recs[0]["error"] != "index offline" {
		t.Errorf("failed call record = %v", recs[0])
	}
	if _, ok := recs[0]["hits"]; ok {
		t.Error("a failed call has no hit count")
	}
	if recs[1]["tool"] != "no_such_tool" || recs[1]["query"] != "" {
		t.Errorf("unknown tool record = %v", recs[1])
	}
	for _, caller := range []string{"key_name", "client_ip", "session_id"} {
		if _, ok := recs[0][caller]; ok {
			t.Errorf("stdio-style call must not log %s", caller)
		}
	}

	out := renderMetrics(t, m)
	for _, line := range []string{
		`mddb_operations_total{operation="mcp_tool_error",label="version_check"} 1`,
		`mddb_operations_total{operation="mcp_tool_error",label="no_such_tool"} 1`,
	} {
		if !strings.Contains(out, line) {
			t.Errorf("metrics output lacks %q:\n%s", line, out)
		}
	}
}

func TestToolLoggingOffWritesNothingAndNilMetricsIsSafe(t *testing.T) {
	client := &fakeFTSClient{resp: &MCPFTSSearchResponse{Total: 0}}
	h := NewMCPHandlerWithConfig(client, []MCPCustomToolConfig{versionCheckTool},
		MCPServerInfo{}, "", ModeRead, "")
	h.observer.logEnabled = false // metrics stays nil

	recs := toolCallRecords(captureLogs(t, func() {
		resp := h.Handle(toolsCall("version_check", map[string]interface{}{"query": "alloy"}))
		if _, ok := resp["result"]; !ok {
			t.Errorf("call should still succeed, got %v", resp)
		}
	}))
	if len(recs) != 0 {
		t.Errorf("logging off must write no tool-call record, got %v", recs)
	}
}

func TestStreamablePostCarriesTheCallerIntoTheToolLog(t *testing.T) {
	client := &fakeFTSClient{resp: &MCPFTSSearchResponse{Total: 0}}
	h, _ := newObservedHandler(client)
	tr := NewMCPStreamableTransport(h)

	recs := toolCallRecords(captureLogs(t, func() {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"version_check","arguments":{"query":"alloy"}}}`
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-MCP-Key-Name", "ci-runner")
		req.RemoteAddr = "198.51.100.7:9000"
		rec := httptest.NewRecorder()
		tr.Handle(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /mcp = %d: %s", rec.Code, rec.Body.String())
		}
	}))
	if len(recs) != 1 {
		t.Fatalf("expected one record, got %d", len(recs))
	}
	if recs[0]["key_name"] != "ci-runner" || recs[0]["client_ip"] != "198.51.100.7" {
		t.Errorf("caller resolved by the HTTP middleware must reach the record: %v", recs[0])
	}
}

func TestRESTToolCallIsObserved(t *testing.T) {
	client := &fakeFTSClient{resp: &MCPFTSSearchResponse{Results: []MCPFTSResult{{}}, Total: 1}}
	m := metrics.NewMetrics(true, toolLogStubStats{})
	srv := &MCPHTTPServer{
		client:      client,
		customTools: []MCPCustomToolConfig{versionCheckTool},
		globalMode:  ModeRead,
		observer:    mcpToolObserver{logEnabled: true, metrics: m},
	}

	recs := toolCallRecords(captureLogs(t, func() {
		req := httptest.NewRequest(http.MethodPost, "/mcp/tools/call",
			strings.NewReader(`{"name":"version_check","arguments":{"query":"postgres"}}`))
		req.Header.Set("X-MCP-Key-Name", "rest-key")
		rec := httptest.NewRecorder()
		srv.handleToolCall(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("tools/call = %d: %s", rec.Code, rec.Body.String())
		}
	}))
	if len(recs) != 1 || recs[0]["query"] != "postgres" || recs[0]["found"] != true || recs[0]["key_name"] != "rest-key" {
		t.Errorf("REST tool call record = %v", recs)
	}
	if !strings.Contains(renderMetrics(t, m), `mddb_operations_total{operation="mcp_tool_call",label="version_check"} 1`) {
		t.Error("REST tool call was not counted")
	}
}
