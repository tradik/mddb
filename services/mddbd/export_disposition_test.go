package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/audit"
)

// /v1/export answers a file, so it names one: without Content-Disposition a
// browser saved it as "export" with no extension, and the panel had to make
// a name up.
func TestExportNamesTheFile(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "blog", "a", "en", "x", nil)

	for _, c := range []struct{ collection, format, want string }{
		{"blog", "", `attachment; filename="blog.ndjson"`},
		{"blog", "ndjson", `attachment; filename="blog.ndjson"`},
		{"blog", "zip", `attachment; filename="blog.zip"`},
		{`we"ird/../na me`, "zip", `attachment; filename="we-ird----na-me.zip"`},
		{"", "ndjson", `attachment; filename="export.ndjson"`},
	} {
		rec := doRequest(t, s.handleExport, map[string]any{"collection": c.collection, "format": c.format})
		if rec.Code != http.StatusOK {
			t.Fatalf("%q %q: %d %s", c.collection, c.format, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Disposition"); got != c.want {
			t.Errorf("%q %q: Content-Disposition %q, want %q", c.collection, c.format, got, c.want)
		}
	}

	// A refused format names no file.
	rec := doRequest(t, s.handleExport, map[string]any{"collection": "blog", "format": "csv"})
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Disposition") != "" {
		t.Errorf("unsupported format: %d, %q", rec.Code, rec.Header().Get("Content-Disposition"))
	}
}

// A browser on another origin (the panel served apart from the server) can
// read the file name only when CORS exposes the header.
func TestCORSExposesContentDisposition(t *testing.T) {
	rec := httptest.NewRecorder()
	withCORS(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/export", nil))
	exposed := rec.Header().Get("Access-Control-Expose-Headers")
	for _, h := range []string{"X-Total-Count", "Content-Disposition"} {
		if !strings.Contains(exposed, h) {
			t.Errorf("Access-Control-Expose-Headers %q lacks %s", exposed, h)
		}
	}
}

// guardWrite records each write it lets through when auditing is on: the
// caller, the path and whether the handler failed.
func TestGuardWriteAuditsTheOutcome(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	am, done := newAuditTestManager(t, 90)
	defer done()
	s.AuditManager = am

	failing := s.guardWrite(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.WriteHeader(http.StatusOK) // only the first status counts
		_, _ = w.Write([]byte(`{"error":"x"}`))
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/add", nil)
	req = req.WithContext(context.WithValue(req.Context(), authContextKey, &JWTClaims{Username: "alice"}))
	failing(httptest.NewRecorder(), req)

	writing := s.guardWrite(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
		_, _ = w.Write([]byte(`{}`))
	})
	writing(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/update", nil))

	waitFlush(t, am, 2)
	events, err := am.Query(audit.QueryFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]audit.AuditEvent{}
	for _, e := range events {
		got[e.Action] = e
	}
	if e := got["write./v1/add"]; e.Actor != "alice" || e.Result != "fail" || e.Detail != "status=500" {
		t.Errorf("failed write: %+v", e)
	}
	if e := got["write./v1/update"]; e.Actor != "" || e.Result != "ok" || e.Detail != "status=200" {
		t.Errorf("ok write: %+v", e)
	}
}
