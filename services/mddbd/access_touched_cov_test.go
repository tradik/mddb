package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Coverage for the files SEC-019 touched: the bulk-ingest handlers and the
// MCP resource reader, their error paths included.

func bulkCall(s *Server, method, target, body string) int {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	switch {
	case strings.HasPrefix(target, "/v1/bulk-ingest-jobs"):
		s.handleBulkIngestList(rec, req)
	case strings.HasPrefix(target, "/v1/bulk-ingest-job/"):
		s.handleBulkIngestStatus(rec, req)
	default:
		s.handleBulkIngestSubmit(rec, req)
	}
	return rec.Code
}

func TestBulkIngestHandlersWithoutAManager(t *testing.T) {
	s := &Server{}
	for _, tc := range []struct{ method, target string }{
		{http.MethodPost, "/v1/bulk-ingest-job"},
		{http.MethodGet, "/v1/bulk-ingest-job/x"},
		{http.MethodGet, "/v1/bulk-ingest-jobs"},
	} {
		if code := bulkCall(s, tc.method, tc.target, `{}`); code != http.StatusBadRequest {
			t.Errorf("%s %s without a manager: %d", tc.method, tc.target, code)
		}
	}
	if code := bulkCall(s, http.MethodPost, "/v1/bulk-ingest-jobs", ""); code != http.StatusMethodNotAllowed {
		t.Errorf("POST the job list: %d", code)
	}
}

func TestBulkIngestHandlerErrors(t *testing.T) {
	s, cleanup := newTestServerWithFTS(t)
	defer cleanup()
	s.BulkIngest = NewBulkIngestManager(s, 1) // never started: the queue holds one job
	body := `{"collection":"docs","documents":[{"key":"k","lang":"en","contentMd":"x"}]}`
	if code := bulkCall(s, http.MethodPost, "/v1/bulk-ingest-job", body); code != http.StatusAccepted {
		t.Fatalf("first job: %d", code)
	}
	if code := bulkCall(s, http.MethodPost, "/v1/bulk-ingest-job", body); code != http.StatusBadRequest {
		t.Errorf("a job over a full queue: %d", code)
	}
	closed, closeDB := newTestServerWithFTS(t)
	closed.BulkIngest = NewBulkIngestManager(closed, 1)
	closeDB()
	if code := bulkCall(closed, http.MethodGet, "/v1/bulk-ingest-jobs", ""); code != http.StatusBadRequest {
		t.Errorf("listing a closed job store: %d", code)
	}
}

func TestReadResourceEveryPath(t *testing.T) {
	s, cleanup := newTestServerWithFTS(t)
	defer cleanup()
	ok := &MCPToolServer{client: NewDirectClient(s)}
	ctx := context.Background()
	for _, uri := range []string{
		"mddb://docs/key?lang=en&env.region=eu&env.empty=",
		"mddb-search://docs?meta.kind=a&sort=key&limit=5&offset=1",
		"mddb-search://docs?limit=x&offset=y",
	} {
		_, _ = ok.readResource(ctx, uri) // a missing document is the reader's answer, not a crash
	}
	for _, uri := range []string{"%zz", "mddb-search://", "mddb://a/b/c"} {
		if _, err := ok.readResource(ctx, uri); err == nil {
			t.Errorf("%s was read", uri)
		}
	}
	failing := &MCPToolServer{client: covBNewAllErr()}
	for _, uri := range []string{"mddb://health", "mddb://stats", "mddb://docs/key", "mddb-search://docs"} {
		if _, err := failing.readResource(ctx, uri); err == nil {
			t.Errorf("%s hid the client's failure", uri)
		}
	}
}
