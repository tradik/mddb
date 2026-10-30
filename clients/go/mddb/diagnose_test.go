package mddb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// OPS-025: Diagnose asks for the opt-in parts by query parameter and returns
// the archive's bytes untouched.
func TestDiagnoseSendsOptionsAndReturnsTheArchive(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		if r.URL.Query().Get("keepNames") == "true" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write([]byte{0x1f, 0x8b, 0})
	}))
	defer srv.Close()
	c := New(srv.URL)

	b, err := c.Diagnose(context.Background(), DiagnoseOptions{})
	if err != nil || len(b) != 3 || b[0] != 0x1f || gotPath != "/v1/admin/diagnose" || gotQuery != "" {
		t.Fatalf("default: %v %v %q %q", b, err, gotPath, gotQuery)
	}
	if _, err := c.Diagnose(context.Background(), DiagnoseOptions{Profiles: true, Integrity: true}); err != nil ||
		gotQuery != "integrity=true&profiles=true" {
		t.Fatalf("opt-in parts: %v %q", err, gotQuery)
	}
	if _, err := c.Diagnose(context.Background(), DiagnoseOptions{KeepNames: true}); err == nil {
		t.Fatal("a 403 was not an error")
	}
}
