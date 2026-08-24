package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "mddb/proto"
)

// HTTP backup must reject path-traversal attempts on the `to` query parameter
// (CVE-class: gosecurity:S2083). The handler should return 400 and not write
// anything outside MDDB_BACKUP_DIR.
func TestHandleBackup_RejectsTraversal(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	jail := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", jail)

	for _, name := range []string{"../escape.db", outsideJail(t, jail), "foo/../../escape.db"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/backup?to="+name, nil)
		s.handleBackup(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("name=%q: expected 400, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// HTTP restore must reject path traversal on the `from` field — even if the
// caller is admin. This guards against admin-credential abuse / SSRF-style
// reading of arbitrary files on the server.
func TestHandleRestore_RejectsTraversal(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	jail := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", jail)

	for _, name := range []string{"../etc/passwd", outsideJail(t, jail), "missing.db"} {
		body := map[string]string{"from": name}
		rec := doRequest(t, s.handleRestore, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("name=%q: expected 400, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// gRPC Backup must reject traversal attempts with InvalidArgument.
func TestGRPCBackup_RejectsTraversal(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()
	jail := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", jail)

	for _, name := range []string{"../escape.db", outsideJail(t, jail), "a/../../escape.db"} {
		_, err := gs.Backup(context.Background(), &pb.BackupRequest{To: name})
		if err == nil {
			t.Errorf("name=%q: expected error, got nil", name)
			continue
		}
		st, _ := status.FromError(err)
		if st.Code() != codes.InvalidArgument {
			t.Errorf("name=%q: expected InvalidArgument, got %v", name, st.Code())
		}
	}
}

// gRPC Restore must reject traversal attempts with InvalidArgument.
func TestGRPCRestore_RejectsTraversal(t *testing.T) {
	gs, _, cleanup := newTestGRPCServer(t)
	defer cleanup()
	jail := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", jail)

	for _, name := range []string{"../etc/passwd", outsideJail(t, jail), "missing.db"} {
		_, err := gs.Restore(context.Background(), &pb.RestoreRequest{From: name})
		if err == nil {
			t.Errorf("name=%q: expected error, got nil", name)
			continue
		}
		st, _ := status.FromError(err)
		if st.Code() != codes.InvalidArgument {
			t.Errorf("name=%q: expected InvalidArgument, got %v", name, st.Code())
		}
	}
}

// DirectClient Backup must reject traversal even for in-process callers.
func TestDirectClient_Backup_RejectsTraversal(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	t.Setenv("MDDB_BACKUP_DIR", t.TempDir())

	c := NewDirectClient(s)
	_, err := c.Backup(context.Background(), &MCPBackupRequest{To: "../escape.db"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "escape") && !strings.Contains(err.Error(), "backup") {
		t.Errorf("unexpected error: %v", err)
	}
}

// DirectClient Restore must reject traversal.
func TestDirectClient_Restore_RejectsTraversal(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	t.Setenv("MDDB_BACKUP_DIR", t.TempDir())

	c := NewDirectClient(s)
	_, err := c.Restore(context.Background(), &MCPRestoreRequest{From: "../etc/passwd"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// Round-trip: DirectClient Backup → Restore inside the jail succeeds.
func TestDirectClient_BackupRestore_RoundTrip(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	t.Setenv("MDDB_BACKUP_DIR", t.TempDir())

	c := NewDirectClient(s)
	addTestDoc(t, s, "blog", "k1", "en", "hello", nil)

	bresp, err := c.Backup(context.Background(), &MCPBackupRequest{To: "snap.db"})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if !strings.HasSuffix(bresp.Backup, "snap.db") {
		t.Errorf("unexpected backup path: %s", bresp.Backup)
	}

	rresp, err := c.Restore(context.Background(), &MCPRestoreRequest{From: "snap.db"})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !strings.HasSuffix(rresp.Restored, "snap.db") {
		t.Errorf("unexpected restored path: %s", rresp.Restored)
	}
}

// Empty backup name must still get a default and land inside the jail.
func TestHandleBackup_DefaultsToJail(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	bdir := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", bdir)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/backup", nil)
	s.handleBackup(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), bdir) && !strings.Contains(rec.Body.String(), "backup-") {
		t.Errorf("expected default backup path inside jail, got: %s", rec.Body.String())
	}
}

// outsideJail returns an absolute path that is not inside jail, on whatever
// platform the test runs.
//
// These cases used to be the literal "/etc/passwd". That is the right shape on
// Unix and the wrong one on Windows, where filepath.IsAbs("/etc/passwd") is
// false — an absolute path there needs a volume. The jail would treat it as a
// relative name, join it inside itself and legitimately accept it, so the test
// would stop covering the absolute-path case without ever going red for it.
func outsideJail(t *testing.T, jail string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(jail), "escape.db"))
	if err != nil {
		t.Fatalf("building a path outside %s: %v", jail, err)
	}
	if !filepath.IsAbs(abs) {
		t.Fatalf("%s is not absolute on this platform", abs)
	}
	return abs
}
