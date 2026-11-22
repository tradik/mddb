package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	json "mddb/internal/jsonx"
)

// FE-024: the backup list, read from MDDB_BACKUP_DIR.

// backupJailWith makes MDDB_BACKUP_DIR a fresh directory holding files, each
// name → its content, modified a minute apart in the order given (the last
// newest). A name ending in "/" is a directory.
func backupJailWith(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", dir)
	base := time.Now().Add(-time.Hour)
	for i, name := range names {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if name[len(name)-1] == '/' {
			if err := os.MkdirAll(p, 0o750); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("content of "+name), 0o600); err != nil {
			t.Fatal(err)
		}
		at := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestIsBackupFileName(t *testing.T) {
	for name, want := range map[string]bool{
		"backup-1.db": true, "mddb-20261122T030000Z.db": true, "..x.db": true,
		"": false, ".db": false, "notes.txt": false, "x.db.sha256": false, "x.db.tmp-AB": false,
		"../x.db": false, "a/x.db": false, `a\x.db`: false, "/x.db": false, "x\x00.db": false, "..": false,
	} {
		if got := isBackupFileName(name); got != want {
			t.Errorf("isBackupFileName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestListBackupFilesNewestFirstWithState(t *testing.T) {
	dir := backupJailWith(t,
		"old.db", "notes.txt", "mid.db", "mid.db.sha256", "mid.db.d/blog.db", "mid.db.d/docs.db", "mid.db.d/sub/",
		"live.db", "folder.db/", "nightly/mddb-1.db", "new.db", "new.db.tmp-XYZ")
	rows, total, err := listBackupFiles(filepath.Join(dir, "live.db"), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(rows) != 3 {
		t.Fatalf("total %d rows %+v, want new, mid, old", total, rows)
	}
	for i, want := range []string{"new.db", "mid.db", "old.db"} {
		if rows[i].Name != want {
			t.Errorf("row %d = %s, want %s", i, rows[i].Name, want)
		}
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	mid := rows[1]
	if mid.State != backupStateChecksummed || mid.CollectionFiles != 2 || mid.Bytes != int64(len("content of mid.db")) ||
		mid.Path != filepath.Join(resolved, "mid.db") || mid.ModifiedAt.Location() != time.UTC {
		t.Errorf("mid = %+v", mid)
	}
	if rows[0].State != backupStateUnknown || rows[0].CollectionFiles != 0 {
		t.Errorf("new = %+v", rows[0])
	}
}

func TestListBackupFilesPaging(t *testing.T) {
	backupJailWith(t, "a.db", "b.db", "c.db")
	rows, total, err := listBackupFiles("", 1, 1)
	if err != nil || total != 3 || len(rows) != 1 || rows[0].Name != "b.db" {
		t.Fatalf("page = %+v %d %v", rows, total, err)
	}
	rows, total, _ = listBackupFiles("", 7, 100)
	if total != 3 || len(rows) != 0 {
		t.Errorf("past the end = %+v %d", rows, total)
	}
}

func TestListBackupFilesSameStamp(t *testing.T) {
	dir := backupJailWith(t, "a.db", "b.db")
	at := time.Now().Add(-time.Minute)
	for _, n := range []string{"a.db", "b.db"} {
		_ = os.Chtimes(filepath.Join(dir, n), at, at)
	}
	rows, _, _ := listBackupFiles("", 0, 10)
	if len(rows) != 2 || rows[0].Name != "b.db" {
		t.Errorf("same time: %+v, want b.db first", rows)
	}
}

func TestListBackupFilesMissingDirectory(t *testing.T) {
	t.Setenv("MDDB_BACKUP_DIR", filepath.Join(t.TempDir(), "none"))
	rows, total, err := listBackupFiles("", 0, 10)
	if err != nil || total != 0 || rows == nil || len(rows) != 0 {
		t.Errorf("missing dir = %v %d %v, want an empty list", rows, total, err)
	}
}

func TestListBackupFilesUnreadable(t *testing.T) {
	backupJailWith(t, "a.db")
	orig := openJail
	t.Cleanup(func() { openJail = orig })
	openJail = func(string) (*os.Root, error) { return nil, errors.New("refused") }
	if _, _, err := listBackupFiles("", 0, 10); err == nil {
		t.Error("an unopenable directory listed")
	}
	rec := httptest.NewRecorder()
	(&Server{}).handleBackups(rec, httptest.NewRequest(http.MethodGet, "/v1/backups", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handler = %d, want 500", rec.Code)
	}
	if _, _, err := openBackupFile("a.db", ""); !errors.Is(err, errBackupNotFound) {
		t.Errorf("open = %v, want not found", err)
	}
}

func TestListBackupFilesReadDirFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions: POSIX, not as root")
	}
	dir := backupJailWith(t, "a.db")
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // #nosec G302 -- a directory needs x to be removed by t.TempDir
	// Opened, then made unreadable: the root's fd is good, a read of the
	// directory through it is refused.
	orig := openJail
	t.Cleanup(func() { openJail = orig })
	openJail = func(p string) (*os.Root, error) {
		r, err := os.OpenRoot(p)
		_ = os.Chmod(p, 0o100) // #nosec G302 -- x only: no read, which is what the test needs
		return r, err
	}
	if _, _, err := listBackupFiles("", 0, 10); err == nil {
		t.Error("an unreadable directory listed")
	}
}

func TestListBackupFilesSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	outside := filepath.Join(t.TempDir(), "secret.db")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := backupJailWith(t, "real.db")
	if err := os.Symlink(outside, filepath.Join(dir, "out.db")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real.db"), filepath.Join(dir, "in.db")); err != nil {
		t.Fatal(err)
	}
	rows, total, err := listBackupFiles("", 0, 10)
	if err != nil || total != 1 || rows[0].Name != "real.db" {
		t.Errorf("rows = %+v %v, want real.db only", rows, err)
	}
}

func TestHandleBackupsAnswer(t *testing.T) {
	backupJailWith(t, "a.db", "b.db")
	rec := httptest.NewRecorder()
	(&Server{}).handleBackups(rec, httptest.NewRequest(http.MethodGet, "/v1/backups?limit=5000&offset=-3", nil))
	var body struct {
		Backups []backupFileRow `json:"backups"`
		Total   int             `json:"total"`
		Limit   int             `json:"limit"`
		Offset  int             `json:"offset"`
		Note    string          `json:"note"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if body.Total != 2 || len(body.Backups) != 2 || body.Limit != backupListMaxLimit || body.Offset != 0 || body.Note != backupListNote {
		t.Errorf("body = %+v", body)
	}

	rec = httptest.NewRecorder()
	(&Server{}).handleBackups(rec, httptest.NewRequest(http.MethodPost, "/v1/backups", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", rec.Code)
	}
}
