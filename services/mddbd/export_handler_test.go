package main

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"

	"mddb/proto"
)

// GO-021. /v1/export used to answer by POSTing to its own /v1/search over
// localhost:MDDB_ADDR. These tests run the handler with nothing listening on
// that address: before, both formats came back empty or panicked on the nil
// response; now the handler reads the database it already has open.

func exportRequest(t *testing.T, s *Server, req ExportRequest) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.handleExport(rec, httptest.NewRequest(http.MethodPost, "/v1/export", bytes.NewReader(b)))
	return rec
}

func TestExportNDJSONWithoutALoopbackServer(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "alpha", "beta")

	rec := exportRequest(t, srv, ExportRequest{Collection: "docs", Format: "ndjson"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("Content-Type = %q", ct)
	}

	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), rec.Body.String())
	}
	// Every line must stand alone as JSON — that is what NDJSON promises a
	// consumer that reads it a line at a time.
	seen := map[string]bool{}
	for i, line := range lines {
		var d map[string]interface{}
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			t.Fatalf("line %d is not valid JSON: %v\n%s", i, err, line)
		}
		seen[d["key"].(string)] = true
	}
	for _, key := range []string{"alpha", "beta"} {
		if !seen[key] {
			t.Errorf("document %q is missing from the export", key)
		}
	}
}

func TestExportZipWithoutALoopbackServer(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "alpha", "beta")

	rec := exportRequest(t, srv, ExportRequest{Collection: "docs", Format: "zip"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q", ct)
	}

	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("the response is not a readable archive: %v", err)
	}
	if len(zr.File) != 2 {
		t.Fatalf("archive holds %d entries, want 2", len(zr.File))
	}

	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".md") {
			t.Errorf("entry %q is not named as markdown", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, _ := io.ReadAll(rc)
		_ = rc.Close()
		if len(content) == 0 {
			t.Errorf("entry %q is empty", f.Name)
		}
	}
}

// The filter is the same query for both formats, because both now go through
// collectExportDocs.
func TestExportAppliesTheMetadataFilterToBothFormats(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "alpha", "beta")

	filter := map[string][]string{"kind": {"prose"}}

	direct, err := client.collectExportDocs(&MCPExportRequest{Collection: "docs", FilterMeta: filter})
	if err != nil {
		t.Fatal(err)
	}
	if len(direct) != 2 {
		t.Fatalf("the filter selected %d documents, want 2", len(direct))
	}

	miss, err := client.collectExportDocs(&MCPExportRequest{
		Collection: "docs",
		FilterMeta: map[string][]string{"kind": {"poetry"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(miss) != 0 {
		t.Errorf("a filter matching nothing selected %d documents", len(miss))
	}
}

func TestExportOfAnUnknownCollectionIsEmptyNotAnError(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	rec := exportRequest(t, srv, ExportRequest{Collection: "nothing-here", Format: "ndjson"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "" {
		t.Errorf("an empty collection exported %q", body)
	}
}

// The pooled writer flushes on Close; a document larger than the buffer must
// not come back truncated.
func TestExportDoesNotTruncateAtTheBufferBoundary(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()

	// Content several buffers long, so the tail lands in a partially-filled
	// buffer that only Close flushes.
	large := strings.Repeat("paragraph of text. ", exportBufferSize/10)
	resp, err := NewBatchProcessor(srv, 1).ProcessBatch(context.Background(), "docs",
		[]*proto.BatchDocument{makeBatchDoc("alpha", "en", large, nil, false)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Failed > 0 {
		t.Fatalf("seeding failed: %v", resp.Errors)
	}

	stream, err := client.Export(context.Background(), &MCPExportRequest{Collection: "docs"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()

	data, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(data), &d); err != nil {
		t.Fatalf("a document spanning several buffers came back truncated: %v", err)
	}
}

// GO-021: both MCP transports counted their sessions and nothing ever read the
// count, so a session a client abandoned without closing was invisible.

func TestHealthOmitsSessionCountsWhenMCPIsOff(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	srv.Ready = true

	if counts := srv.mcpSessionCounts(); counts != nil {
		t.Errorf("MCP is disabled but health reports %v — a zero would read as idle", counts)
	}

	rec := httptest.NewRecorder()
	srv.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("health response is not JSON: %v", err)
	}
	if _, present := body["mcpSessions"]; present {
		t.Error("mcpSessions is present with MCP disabled")
	}
}

func TestHealthReportsSessionsPerTransport(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	srv.Ready = true

	handler := NewMCPHandlerWithConfig(NewDirectClient(srv), nil, MCPServerInfo{}, "", ModeRW, "")
	srv.mcpSSE = NewMCPSSETransport(handler)
	srv.mcpStreamable = NewMCPStreamableTransport(handler)

	counts := srv.mcpSessionCounts()
	if counts == nil {
		t.Fatal("MCP is enabled and health reports nothing")
	}
	for _, transport := range []string{"sse", "streamable"} {
		if got, present := counts[transport]; !present || got != 0 {
			t.Errorf("%s = %d (present=%v), want 0 on a fresh server", transport, got, present)
		}
	}

	rec := httptest.NewRecorder()
	srv.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	sessions, ok := body["mcpSessions"].(map[string]interface{})
	if !ok {
		t.Fatalf("mcpSessions missing from the health response: %s", rec.Body.String())
	}
	if len(sessions) != 2 {
		t.Errorf("mcpSessions = %v, want both transports", sessions)
	}
}

// Half-configured is a real state during startup: one transport built, the
// other not yet.
func TestHealthReportsOnlyTheTransportsThatExist(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	srv.mcpStreamable = NewMCPStreamableTransport(
		NewMCPHandlerWithConfig(NewDirectClient(srv), nil, MCPServerInfo{}, "", ModeRW, ""))

	counts := srv.mcpSessionCounts()
	if _, present := counts["sse"]; present {
		t.Error("a transport that was never built is reported as having sessions")
	}
	if _, present := counts["streamable"]; !present {
		t.Error("the transport that exists is missing")
	}
}

func TestSessionCountsOnANilServer(t *testing.T) {
	var s *Server
	if counts := s.mcpSessionCounts(); counts != nil {
		t.Errorf("a nil server reported %v", counts)
	}
}
