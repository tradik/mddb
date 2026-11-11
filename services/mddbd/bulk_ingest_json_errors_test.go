package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// With no jobs the list is an empty array, not null: a client iterating it
// should not need a nil check.
func TestBulkIngestListWithoutJobsIsAnEmptyArray(t *testing.T) {
	s, cleanup := httpBulkTestServer(t)
	defer cleanup()
	for _, target := range []string{"/v1/bulk-ingest-jobs", "/v1/bulk-ingest-jobs?collection=none"} {
		rec := httptest.NewRecorder()
		s.handleBulkIngestList(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"jobs":[]`) {
			t.Errorf("%s: %d %s", target, rec.Code, rec.Body.String())
		}
	}
}

// The bulk ingest endpoints answered a missing job ("job X not found") and a
// wrong method in plain text; every API error is the server's JSON shape.
func TestBulkIngestErrorsAreJSON(t *testing.T) {
	s, cleanup := httpBulkTestServer(t)
	defer cleanup()

	for _, c := range []struct {
		name    string
		handler http.HandlerFunc
		method  string
		path    string
		status  int
		message string
	}{
		{"missing job", s.handleBulkIngestStatus, http.MethodGet, "/v1/bulk-ingest-job/nope", http.StatusNotFound, "job nope not found"},
		{"cancel a missing job", s.handleBulkIngestStatus, http.MethodDelete, "/v1/bulk-ingest-job/nope", http.StatusNotFound, "job nope not found"},
		{"status, wrong method", s.handleBulkIngestStatus, http.MethodPut, "/v1/bulk-ingest-job/nope", http.StatusMethodNotAllowed, "method not allowed"},
		{"submit, wrong method", s.handleBulkIngestSubmit, http.MethodGet, "/v1/bulk-ingest-job", http.StatusMethodNotAllowed, "method not allowed"},
		{"list, wrong method", s.handleBulkIngestList, http.MethodPost, "/v1/bulk-ingest-jobs", http.StatusMethodNotAllowed, "method not allowed"},
	} {
		rec := httptest.NewRecorder()
		withJSON(c.handler).ServeHTTP(rec, httptest.NewRequest(c.method, c.path, strings.NewReader(`{}`)))
		if rec.Code != c.status {
			t.Errorf("%s: %d, want %d", c.name, rec.Code, c.status)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] != c.message {
			t.Errorf("%s: body %q, want {\"error\":%q}", c.name, rec.Body.String(), c.message)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: content type %q", c.name, ct)
		}
	}
}
