package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// covDFailWriter is a ResponseWriter whose body writes fail, as they do when
// the client has gone away mid-response.
type covDFailWriter struct {
	header http.Header
	code   int
}

func (w *covDFailWriter) Header() http.Header       { return w.header }
func (w *covDFailWriter) WriteHeader(code int)      { w.code = code }
func (w *covDFailWriter) Write([]byte) (int, error) { return 0, errors.New("client gone") }
func covDNewFailWriter() *covDFailWriter            { return &covDFailWriter{header: http.Header{}} }
func covDHTTPServer() *MCPHTTPServer {
	return &MCPHTTPServer{client: covDHealthClient{}, globalMode: ModeRead}
}
func covDPost(path, body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
}
func covDGet(path string) *http.Request                { return httptest.NewRequest(http.MethodGet, path, nil) }
func covDDecode(t *testing.T, b []byte, v interface{}) { t.Helper(); _ = json.Unmarshal(b, v) }

func TestCovDNewMCPHTTPServer(t *testing.T) {
	s := &Server{Mode: ModeRW}
	s.Config.MCP.Mode = ModeRead
	m := s.newMCPHTTPServer()
	if m.globalMode != ModeRW || m.mode != ModeRead {
		t.Errorf("modes = %q/%q", m.globalMode, m.mode)
	}
	if dc, ok := m.client.(*DirectClient); !ok || dc.server != s {
		t.Errorf("client = %T", m.client)
	}
}

func TestCovDHTTPListings(t *testing.T) {
	m := covDHTTPServer()

	rec := httptest.NewRecorder()
	m.handleResources(rec, covDGet("/resources"))
	var res struct {
		Resources         []json.RawMessage `json:"resources"`
		ResourceTemplates []json.RawMessage `json:"resourceTemplates"`
	}
	covDDecode(t, rec.Body.Bytes(), &res)
	if rec.Header().Get("Content-Type") != "application/json" || len(res.Resources) == 0 || len(res.ResourceTemplates) == 0 {
		t.Errorf("/resources = %s", rec.Body)
	}

	rec = httptest.NewRecorder()
	m.handleTools(rec, covDGet("/tools"))
	var tools struct {
		Tools []MCPTool `json:"tools"`
	}
	covDDecode(t, rec.Body.Bytes(), &tools)
	if len(tools.Tools) == 0 {
		t.Errorf("/tools = %s", rec.Body)
	}
}

func TestCovDHTTPResourceRead(t *testing.T) {
	m := covDHTTPServer()

	rec := httptest.NewRecorder()
	m.handleResourceRead(rec, covDPost("/resources/read", `{"uri":"mddb://health"}`))
	var body struct {
		Contents []map[string]string `json:"contents"`
	}
	covDDecode(t, rec.Body.Bytes(), &body)
	if rec.Code != http.StatusOK || len(body.Contents) != 1 || body.Contents[0]["uri"] != "mddb://health" ||
		!strings.Contains(body.Contents[0]["text"], "healthy") {
		t.Errorf("read = %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	m.handleResourceRead(rec, covDPost("/resources/read", `{`))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid request") {
		t.Errorf("malformed = %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	m.handleResourceRead(rec, covDPost("/resources/read", `{"uri":"mddb://stats"}`))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "stats unavailable") {
		t.Errorf("failing resource = %d %s", rec.Code, rec.Body)
	}
}

func TestCovDHTTPToolCallErrors(t *testing.T) {
	m := covDHTTPServer()

	rec := httptest.NewRecorder()
	m.handleToolCall(rec, covDPost("/tools/call", `[`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed = %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	m.handleToolCall(rec, covDPost("/tools/call", `{"name":"no_such_tool","arguments":{}}`))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "no_such_tool") {
		t.Errorf("unknown tool = %d %s", rec.Code, rec.Body)
	}
}

// TestCovDHTTPWriteFailures checks that a client that disconnects mid-answer
// costs a log line and nothing else: each handler still sets its headers.
func TestCovDHTTPWriteFailures(t *testing.T) {
	client := &fakeFTSClient{resp: &MCPFTSSearchResponse{Results: []MCPFTSResult{{}}, Total: 1}}
	m := &MCPHTTPServer{client: client, customTools: []MCPCustomToolConfig{versionCheckTool}, globalMode: ModeRead}
	health := covDHTTPServer()

	for name, call := range map[string]func(http.ResponseWriter){
		"resources": func(w http.ResponseWriter) { health.handleResources(w, covDGet("/resources")) },
		"tools":     func(w http.ResponseWriter) { health.handleTools(w, covDGet("/tools")) },
		"read": func(w http.ResponseWriter) {
			health.handleResourceRead(w, covDPost("/resources/read", `{"uri":"mddb://health"}`))
		},
		"call": func(w http.ResponseWriter) {
			m.handleToolCall(w, covDPost("/tools/call", `{"name":"version_check","arguments":{"query":"go"}}`))
		},
	} {
		w := covDNewFailWriter()
		call(w)
		if w.header.Get("Content-Type") != "application/json" || w.code != 0 {
			t.Errorf("%s: content-type %q status %d", name, w.header.Get("Content-Type"), w.code)
		}
	}
}

func TestCovDHTTPToolCallOutsideToolset(t *testing.T) {
	t.Setenv("MDDB_MCP_TOOLSETS", "")
	m := covDHTTPServer()
	req := covDPost("/tools/call", `{"name":"get_stats","arguments":{}}`)
	req = req.WithContext(withKeyToolsets(req.Context(), map[string]bool{ToolsetSearch: true}))
	rec := httptest.NewRecorder()
	m.handleToolCall(rec, req)
	var body struct {
		Error map[string]interface{} `json:"error"`
	}
	covDDecode(t, rec.Body.Bytes(), &body)
	if rec.Code != http.StatusNotFound || body.Error["code"] != float64(mcpErrMethodNotFound) {
		t.Errorf("restricted call = %d %s", rec.Code, rec.Body)
	}
}
