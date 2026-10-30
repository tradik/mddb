package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mddb/internal/audit"

	bolt "go.etcd.io/bbolt"
)

// fakeAuditExporter is an audit.AuditExporter that only reports fixed stats.
type fakeAuditExporter struct{ name string }

func (f fakeAuditExporter) Export(audit.AuditEvent) {}
func (f fakeAuditExporter) Close()                  {}
func (f fakeAuditExporter) Name() string            { return f.name }
func (f fakeAuditExporter) Stats() audit.ExporterStats {
	return audit.ExporterStats{Name: f.name, Delivered: 3}
}

func TestHandleAuditExporters(t *testing.T) {
	serve := func(s *Server, method string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleAuditExporters(rec, httptest.NewRequest(method, "/v1/audit/exporters", nil))
		return rec
	}
	am, cleanup := newAuditTestManager(t, 90)
	defer cleanup()

	if rec := serve(&Server{AuditManager: am}, http.MethodPost); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", rec.Code)
	}
	if rec := serve(&Server{}, http.MethodGet); rec.Code != http.StatusNotFound {
		t.Errorf("audit off = %d, want 404", rec.Code)
	}
	if rec := serve(&Server{AuditManager: am, AuthManager: &AuthManager{enabled: true}}, http.MethodGet); rec.Code != http.StatusUnauthorized {
		t.Errorf("no claims = %d, want 401", rec.Code)
	}

	am.AddExporter(fakeAuditExporter{name: "siem"})
	rec := serve(&Server{AuditManager: am}, http.MethodGet)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"siem"`) || !strings.Contains(rec.Body.String(), `"count":1`) {
		t.Errorf("GET = %d %s", rec.Code, rec.Body.String())
	}
}

// TestHandleAuditQueryError: a query that fails (closed database) is a 500.
func TestHandleAuditQueryError(t *testing.T) {
	db, err := bolt.Open(filepath.Join(t.TempDir(), "a.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	am := audit.NewAuditManager(db, true, 90)
	if err := am.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	rec := httptest.NewRecorder()
	(&Server{AuditManager: am}).handleAudit(rec, httptest.NewRequest(http.MethodGet, "/v1/audit", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("closed db = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}
