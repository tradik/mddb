package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	proto "mddb/proto"

	json "mddb/internal/jsonx"
)

// submitOneDoc wires the single-document flow used by the HTTP handler tests.
// Returns the job ID after submission; callers wait on it as needed.
func submitOneDoc(t *testing.T, s *Server, collection, key string) string {
	t.Helper()
	doc := &proto.BatchDocument{Key: key, Lang: "en", ContentMd: "# " + key}
	job, err := s.BulkIngest.Submit(collection, []*proto.BatchDocument{doc}, "")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	return job.ID
}

func TestHandleBulkIngestSubmit_Accepts(t *testing.T) {
	s, cleanup := httpBulkTestServer(t)
	defer cleanup()

	body := BulkIngestSubmitRequest{
		Collection: "blog",
		Documents: []AddBatchDocument{
			{Key: "a", Lang: "en", ContentMD: "# A"},
			{Key: "b", Lang: "en", ContentMD: "# B"},
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/bulk-ingest-job", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	s.handleBulkIngestSubmit(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var job BulkIngestJob
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if job.ID == "" || job.Total != 2 {
		t.Errorf("unexpected job record: %+v", job)
	}
}

func TestHandleBulkIngestSubmit_Validation(t *testing.T) {
	s, cleanup := httpBulkTestServer(t)
	defer cleanup()

	cases := []struct {
		name string
		body string
	}{
		{"missing collection", `{"documents":[{"key":"a","lang":"en","contentMd":"x"}]}`},
		{"empty documents", `{"collection":"c","documents":[]}`},
		{"malformed json", `not json`},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/v1/bulk-ingest-job", strings.NewReader(c.body))
		rec := httptest.NewRecorder()
		s.handleBulkIngestSubmit(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", c.name, rec.Code)
		}
	}
}

func TestHandleBulkIngestSubmit_WrongMethod(t *testing.T) {
	s, cleanup := httpBulkTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/v1/bulk-ingest-job", nil)
	rec := httptest.NewRecorder()
	s.handleBulkIngestSubmit(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestHandleBulkIngestStatus_HTTPFlow(t *testing.T) {
	s, cleanup := httpBulkTestServer(t)
	defer cleanup()

	id := submitOneDoc(t, s, "c", "a")
	waitForStatus(t, s.BulkIngest, id, 2*time.Second, BulkJobCompleted, BulkJobFailed)

	req := httptest.NewRequest(http.MethodGet, "/v1/bulk-ingest-job/"+id, nil)
	rec := httptest.NewRecorder()
	s.handleBulkIngestStatus(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got BulkIngestJob
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.ID != id {
		t.Errorf("expected id=%s, got %s", id, got.ID)
	}

	// GET on missing id → 404.
	req404 := httptest.NewRequest(http.MethodGet, "/v1/bulk-ingest-job/does-not-exist", nil)
	rec404 := httptest.NewRecorder()
	s.handleBulkIngestStatus(rec404, req404)
	if rec404.Code != http.StatusNotFound {
		t.Errorf("missing id: expected 404, got %d", rec404.Code)
	}

	// Path without an id after trailing slash → 400.
	reqBad := httptest.NewRequest(http.MethodGet, "/v1/bulk-ingest-job/", nil)
	recBad := httptest.NewRecorder()
	s.handleBulkIngestStatus(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Errorf("empty id: expected 400, got %d", recBad.Code)
	}

	// Unsupported method → 405.
	reqPut := httptest.NewRequest(http.MethodPut, "/v1/bulk-ingest-job/"+id, nil)
	recPut := httptest.NewRecorder()
	s.handleBulkIngestStatus(recPut, reqPut)
	if recPut.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT: expected 405, got %d", recPut.Code)
	}
}

func TestHandleBulkIngestStatus_CancelAlreadyCompleted(t *testing.T) {
	s, cleanup := httpBulkTestServer(t)
	defer cleanup()

	id := submitOneDoc(t, s, "c", "a")
	waitForStatus(t, s.BulkIngest, id, 2*time.Second, BulkJobCompleted, BulkJobFailed)

	req := httptest.NewRequest(http.MethodDelete, "/v1/bulk-ingest-job/"+id, nil)
	rec := httptest.NewRecorder()
	s.handleBulkIngestStatus(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("cancel completed: expected 400, got %d", rec.Code)
	}
}

func TestHandleBulkIngestList_HTTP(t *testing.T) {
	s, cleanup := httpBulkTestServer(t)
	defer cleanup()

	id := submitOneDoc(t, s, "c", "a")
	waitForStatus(t, s.BulkIngest, id, 2*time.Second, BulkJobCompleted, BulkJobFailed)

	req := httptest.NewRequest(http.MethodGet, "/v1/bulk-ingest-jobs", nil)
	rec := httptest.NewRecorder()
	s.handleBulkIngestList(rec, req)
	if rec.Code != 200 {
		t.Fatalf("list: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	reqPost := httptest.NewRequest(http.MethodPost, "/v1/bulk-ingest-jobs", nil)
	recPost := httptest.NewRecorder()
	s.handleBulkIngestList(recPost, reqPost)
	if recPost.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: expected 405, got %d", recPost.Code)
	}
}
