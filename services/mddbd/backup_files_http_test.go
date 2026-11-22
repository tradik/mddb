package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mddb/internal/audit"
)

// FE-024: downloading a backup, and who may list or download one.

// download sends GET /v1/backups/{name}/download straight to the handler.
func download(s *Server, method, target string, claims *JWTClaims) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if claims != nil {
		req = req.WithContext(context.WithValue(req.Context(), authContextKey, claims))
	}
	rec := httptest.NewRecorder()
	s.handleBackupItem(rec, req)
	return rec
}

func TestBackupDownloadServesTheFile(t *testing.T) {
	backupJailWith(t, "nightly.db")
	want := "content of nightly.db"
	rec := download(&Server{}, http.MethodGet, "/v1/backups/nightly.db/download", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	h := rec.Header()
	if h.Get("Content-Length") != "21" || h.Get("Content-Disposition") != `attachment; filename=nightly.db` ||
		h.Get("Content-Type") != "application/octet-stream" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers = %v", h)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/backups/nightly.db/download", nil)
	req.Header.Set("Range", "bytes=0-6")
	rec = httptest.NewRecorder()
	(&Server{}).handleBackupItem(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "content" {
		t.Errorf("range = %d %q", rec.Code, rec.Body.String())
	}

	rec = download(&Server{}, http.MethodHead, "/v1/backups/nightly.db/download", nil)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "21" {
		t.Errorf("HEAD = %d %d %v", rec.Code, rec.Body.Len(), rec.Header())
	}
}

// Every name that is not a listed backup answers the same 404, whether it
// is missing, outside the directory or not a backup.
func TestBackupDownloadRefusesWhatIsNotABackup(t *testing.T) {
	dir := backupJailWith(t, "live.db", "notes.txt", "folder.db/", "nightly/mddb-1.db", "ok.db.sha256")
	outside := filepath.Join(t.TempDir(), "secret.db")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{Path: filepath.Join(dir, "live.db")}
	names := []string{
		"missing.db", "notes.txt", "folder.db", "nightly/mddb-1.db", "ok.db.sha256", "live.db",
		"../secret.db", "..%2Fsecret.db", "%2E%2E/secret.db", "nightly%2Fmddb-1.db",
		strings.TrimPrefix(filepath.ToSlash(outside), "/"), "%00.db", "a%5Cb.db", "",
	}
	var first string
	for _, name := range names {
		req := httptest.NewRequest(http.MethodGet, "/v1/backups/x/download", nil)
		req.URL.Path = "/v1/backups/" + name + "/download"
		if decoded, err := pathUnescape(req.URL.Path); err == nil {
			req.URL.Path = decoded
		}
		rec := httptest.NewRecorder()
		s.handleBackupItem(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%q = %d, want 404", name, rec.Code)
		}
		if first == "" {
			first = rec.Body.String()
		} else if rec.Body.String() != first {
			t.Errorf("%q answered %q, not the same %q", name, rec.Body.String(), first)
		}
	}
	// An absolute path, as the decoded URL path carries it.
	req := httptest.NewRequest(http.MethodGet, "/v1/backups/x/download", nil)
	req.URL.Path = "/v1/backups/" + outside + "/download"
	rec := httptest.NewRecorder()
	s.handleBackupItem(rec, req)
	if rec.Code != http.StatusNotFound || rec.Body.String() != first {
		t.Errorf("absolute path = %d %q", rec.Code, rec.Body.String())
	}
	// Not a download, and not GET.
	if rec := download(s, http.MethodGet, "/v1/backups/live.db", nil); rec.Code != http.StatusNotFound || rec.Body.String() != first {
		t.Errorf("no /download = %d", rec.Code)
	}
	if rec := download(s, http.MethodDelete, "/v1/backups/live.db/download", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE = %d, want 405", rec.Code)
	}
}

// pathUnescape decodes a test's URL path the way the server sees r.URL.Path.
func pathUnescape(p string) (string, error) { return url.PathUnescape(p) }

func TestBackupDownloadRefusesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	outside := filepath.Join(t.TempDir(), "secret.db")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := backupJailWith(t, "real.db")
	for name, target := range map[string]string{"out.db": outside, "in.db": filepath.Join(dir, "real.db")} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
		if rec := download(&Server{}, http.MethodGet, "/v1/backups/"+name+"/download", nil); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("symlink %s = %d %q, want 404", name, rec.Code, rec.Body.String())
		}
	}
	// The backup directory itself a symlink out is fine: it is resolved
	// once, as for GET /v1/backup, and names inside it stay inside.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MDDB_BACKUP_DIR", link)
	if rec := download(&Server{}, http.MethodGet, "/v1/backups/real.db/download", nil); rec.Code != http.StatusOK {
		t.Errorf("linked directory = %d", rec.Code)
	}
}

func TestBackupDownloadUnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions: POSIX, not as root")
	}
	dir := backupJailWith(t, "locked.db")
	if err := os.Chmod(filepath.Join(dir, "locked.db"), 0); err != nil {
		t.Fatal(err)
	}
	if rec := download(&Server{}, http.MethodGet, "/v1/backups/locked.db/download", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unreadable = %d, want 404", rec.Code)
	}
}

func TestBackupDownloadIsAuditLogged(t *testing.T) {
	backupJailWith(t, "nightly.db")
	am, cleanup := newAuditTestManager(t, 90)
	defer cleanup()
	s := &Server{AuditManager: am}
	download(s, http.MethodGet, "/v1/backups/nightly.db/download", &JWTClaims{Username: "root", Admin: true})
	download(s, http.MethodGet, "/v1/backups/gone.db/download", nil)
	waitFlush(t, am, 2)
	events, err := am.Query(audit.QueryFilter{Action: "backup.download"})
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %+v %v", events, err)
	}
	got := map[string]string{}
	for _, e := range events {
		got[e.Resource] = e.Actor + "/" + e.Result
	}
	if got["nightly.db"] != "root/ok" || got["gone.db"] != "/fail" {
		t.Errorf("events = %v", got)
	}
}

// The access gate and the handlers' own check: admin on "*" only, as for
// GET /v1/backup and POST /v1/restore.
func TestBackupRoutesAreAdminOnly(t *testing.T) {
	backupJailWith(t, "nightly.db")
	s := accessServer(t)
	for _, target := range []string{"/v1/backups", "/v1/backups/nightly.db/download"} {
		for who, want := range map[*JWTClaims]int{root: http.StatusOK, alice: http.StatusForbidden, tina: http.StatusForbidden} {
			if got := restCall(t, s, who, http.MethodGet, target, ""); got != want {
				t.Errorf("%s as %s = %d, want %d", target, who.Username, got, want)
			}
		}
		routes := http.NewServeMux()
		s.registerRoutes(routes, true)
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous = %d, want 401", target, rec.Code)
		}
		// The handler refuses on its own too, as handleBackup does.
		rec = httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req = req.WithContext(context.WithValue(req.Context(), authContextKey, alice))
		mux := http.NewServeMux()
		mux.HandleFunc("/v1/backups", s.handleBackups)
		mux.HandleFunc("/v1/backups/", s.handleBackupItem)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s handler as alice = %d, want 403", target, rec.Code)
		}
	}
}

// With auth off every route is open, the backup routes as GET /v1/backup is;
// read-only mode leaves them open, as they write nothing.
func TestBackupRoutesAuthOffAndReadOnly(t *testing.T) {
	backupJailWith(t, "nightly.db")
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Mode = ModeRead
	mux := http.NewServeMux()
	s.registerRoutes(mux, false)
	for _, target := range []string{"/v1/backups", "/v1/backups/nightly.db/download"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s = %d %s", target, rec.Code, rec.Body.String())
		}
	}
}

// The MCP listener does not serve them, so no MCP key, scoped or not,
// reaches a backup file; and no MCP tool lists or hands one out.
func TestBackupRoutesNotOnTheMCPListener(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	rec := &recordingMux{patterns: map[string]http.Handler{}}
	s.registerMCPRoutes(rec, nil, nil)
	for p := range rec.patterns {
		if strings.HasPrefix(p, "/v1/backups") {
			t.Errorf("the MCP listener serves %s", p)
		}
	}
	for tool := range mcpToolAccessRules {
		if strings.Contains(tool, "backups") || strings.Contains(tool, "download") {
			t.Errorf("MCP tool %s", tool)
		}
	}
}
