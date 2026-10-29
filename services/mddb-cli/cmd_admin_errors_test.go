package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// The database-wide commands on a server that refuses them or answers
// nonsense: each exits non-zero and prints no success line.
func TestAdminCommandsReportFailures(t *testing.T) {
	refusing := newFakeServer(t, map[string]interface{}{
		"/v1/export":   failure{http.StatusForbidden, `{"error":"no"}`},
		"/v1/backup":   failure{http.StatusInternalServerError, `{"error":"damaged"}`},
		"/v1/truncate": failure{http.StatusForbidden, `{"error":"no"}`},
	})
	garbled := newFakeServer(t, map[string]interface{}{
		"/v1/export":  "ndjson",
		"/v1/backup":  "not json",
		"/v1/restore": "not json",
		"/v1/stats":   "not json",
	})
	unwritable := filepath.Join(t.TempDir(), "absent", "out.ndjson")

	for _, tc := range []struct {
		name   string
		server string
		args   []string
	}{
		{"export refused", refusing.URL, []string{"export", "blog"}},
		{"export to an unwritable file", garbled.URL, []string{"export", "blog", "-o", unwritable}},
		{"backup refused", refusing.URL, []string{"backup", "x.db"}},
		{"backup answer unreadable", garbled.URL, []string{"backup", "x.db"}},
		{"restore answer unreadable", garbled.URL, []string{"restore", "x.db"}},
		{"truncate refused", refusing.URL, []string{"truncate", "blog"}},
		{"stats answer unreadable", garbled.URL, []string{"stats"}},
	} {
		out, err := runCLI(t, tc.server, tc.args...)
		if err == nil {
			t.Errorf("%s: exited zero", tc.name)
		}
		if strings.Contains(out, "✓") {
			t.Errorf("%s: printed a success mark:\n%s", tc.name, out)
		}
	}
}
