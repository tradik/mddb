package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The local target, the backup set and retention, down to their failures.

// A backup of some collections copies only their files, and needs the
// per-collection layout.
func TestScheduledBackupOfSomeCollections(t *testing.T) {
	t.Run("single layout", func(t *testing.T) {
		t.Setenv("MDDB_STORAGE_LAYOUT", "single")
		s := newBackupTestServer(t)
		a := &BackupAction{Collections: []string{"blog"}}
		_ = a.normalize()
		_, err := s.runScheduledBackup(AutomationRule{ID: "some", Type: "cron", Backup: a}, time.Now())
		if err == nil || !strings.Contains(err.Error(), "per-collection") {
			t.Errorf("a subset in the single layout: %v", err)
		}
	})
	t.Run("per-collection layout", func(t *testing.T) {
		t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
		s := newBackupTestServer(t)
		addTestDoc(t, s, "blog", "a", "en", "x", nil)
		addTestDoc(t, s, "wiki", "a", "en", "x", nil)
		a := &BackupAction{Collections: []string{"blog"}}
		_ = a.normalize()
		when := time.Date(2026, 5, 1, 3, 0, 0, 0, time.UTC)
		if _, err := s.runScheduledBackup(AutomationRule{ID: "some", Type: "cron", Backup: a}, when); err != nil {
			t.Fatal(err)
		}
		files, err := os.ReadDir(filepath.Join(backupDir(), a.name(when)+".d"))
		if err != nil || len(files) != 1 || files[0].Name() != collectionFileName("blog") {
			t.Errorf("collection files: %v %v", files, err)
		}
	})
}

// Staging and writing a local backup can fail: the directory cannot be made,
// the backup's name is taken by a directory, its checksum file's name is, the
// copy fails its check. Nothing is left of the failed backup.
func TestLocalBackupFailures(t *testing.T) {
	s := newBackupTestServer(t)
	when := time.Date(2026, 5, 1, 3, 0, 0, 0, time.UTC)
	run := func(a *BackupAction) error {
		t.Helper()
		if err := a.normalize(); err != nil {
			t.Fatal(err)
		}
		_, err := s.runScheduledBackup(AutomationRule{ID: "lf", Type: "cron", Backup: a}, when)
		return err
	}
	name := (&BackupAction{Prefix: defaultBackupPrefix}).name(when)

	// The path is a file.
	if err := os.WriteFile(filepath.Join(backupDir(), "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(&BackupAction{Path: "file"}); err == nil {
		t.Error("a backup into a file succeeded")
	}
	// MDDB_BACKUP_DIR cannot be created.
	t.Setenv("MDDB_BACKUP_DIR", filepath.Join(backupDir(), "file", "sub"))
	if err := run(&BackupAction{}); err == nil {
		t.Error("a backup with no backup directory succeeded")
	}
	t.Setenv("MDDB_BACKUP_DIR", t.TempDir())

	// The name is a directory.
	if err := os.MkdirAll(filepath.Join(backupDir(), "d", name), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := run(&BackupAction{Path: "d"}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a backup onto a directory: %v", err)
	}
	// The checksum file's name is a directory: the backup is removed again.
	if err := os.MkdirAll(filepath.Join(backupDir(), "c", sumFileName(name), "x"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := run(&BackupAction{Path: "c"}); err == nil {
		t.Error("a backup whose checksum file cannot be written succeeded")
	}
	if _, err := os.Stat(filepath.Join(backupDir(), "c", name)); !os.IsNotExist(err) {
		t.Errorf("the unhashed backup was left: %v", err)
	}
}

// A second run of a rule while the first is going is skipped and logged.
func TestScheduledBackupSkipsAnOverlappingRun(t *testing.T) {
	s := newBackupTestServer(t)
	backupRuns.Store("busy", true)
	defer backupRuns.Delete("busy")
	a := &BackupAction{}
	_ = a.normalize()
	if _, err := s.runScheduledBackup(AutomationRule{ID: "busy", Type: "cron", Backup: a}, time.Now()); !errors.Is(err, errBackupRunning) {
		t.Fatalf("an overlapping run: %v", err)
	}
	logs, _, _ := s.AutomationLogStore.List(10, "", "busy", "skipped")
	if len(logs) != 1 {
		t.Errorf("the skipped run was not logged: %v", logs)
	}

	// A server without the log, the SSE hub or webhooks still runs backups.
	s.AutomationLogStore, s.SSEHub, s.WebhookManager = nil, nil, nil
	t.Setenv("MDDB_VERIFY_TIMEOUT", "1ns")
	if _, err := s.runScheduledBackup(AutomationRule{ID: "bare", Type: "cron", Backup: a}, time.Now()); err == nil {
		t.Error("the failing backup succeeded")
	}
	t.Setenv("MDDB_VERIFY_TIMEOUT", "")
	if _, err := s.runScheduledBackup(AutomationRule{ID: "bare", Type: "cron", Backup: a}, time.Now()); err != nil {
		t.Error(err)
	}
}

// fakeTarget is a backupTarget whose listing and removals a test controls.
type fakeTarget struct {
	names     []string
	listErr   error
	removeErr error
	removed   []string
}

func (f *fakeTarget) stage(string) (string, string, error)     { return "", "", nil }
func (f *fakeTarget) store(*backupSet, string) (string, error) { return "", nil }
func (f *fakeTarget) discard(string, string, bool)             {}
func (f *fakeTarget) list() ([]string, error)                  { return f.names, f.listErr }
func (f *fakeTarget) remove(n string) error                    { f.removed = append(f.removed, n); return f.removeErr }

func TestPruneBackups(t *testing.T) {
	a := &BackupAction{Prefix: "p-", Keep: 2}
	names := []string{"p-20260103T000000Z.db", "p-20260101T000000Z.db", "other.db", "p-20260102T000000Z.db"}

	// Oldest first, never the one just stored, nothing that is not a backup.
	f := &fakeTarget{names: append([]string(nil), names...)}
	removed, err := pruneBackups(f, a, "p-20260101T000000Z.db")
	if err != nil || len(removed) != 0 {
		t.Errorf("the current backup (oldest by its clock) was removed: %v %v", removed, err)
	}
	f = &fakeTarget{names: append([]string(nil), names...)}
	if removed, err = pruneBackups(f, a, "p-20260103T000000Z.db"); err != nil || len(removed) != 1 || removed[0] != "p-20260101T000000Z.db" {
		t.Errorf("removed %v, %v", removed, err)
	}
	// keep 0 keeps everything without listing.
	if removed, err = pruneBackups(&fakeTarget{listErr: errors.New("no")}, &BackupAction{}, ""); err != nil || removed != nil {
		t.Errorf("keep 0: %v %v", removed, err)
	}
	if _, err = pruneBackups(&fakeTarget{listErr: errors.New("no")}, a, ""); err == nil {
		t.Error("a failed listing was not reported")
	}
	f = &fakeTarget{names: append([]string(nil), names...), removeErr: errors.New("no")}
	if _, err = pruneBackups(f, a, "x"); err == nil {
		t.Error("a failed removal was not reported")
	}
}

func TestLocalBackupTargetListAndRemove(t *testing.T) {
	dir := t.TempDir()
	lt := &localBackupTarget{dir: dir}
	for _, p := range []string{"p-20260101T000000Z.db", "p-20260101T000000Z.db.sha256", "p-20260101T000000Z.db.d/blog.db"} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	names, err := lt.list()
	if err != nil || len(names) != 2 {
		t.Errorf("list: %v %v", names, err)
	}
	if err := lt.remove("p-20260101T000000Z.db"); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("left after remove: %v", left)
	}
	// Removing again: every part is already gone.
	if err := lt.remove("p-20260101T000000Z.db"); err != nil {
		t.Errorf("removing a removed backup: %v", err)
	}
	lt.dir = filepath.Join(dir, "absent")
	if _, err := lt.list(); err == nil {
		t.Error("listing a missing directory succeeded")
	}
	if err := lt.remove("x.db"); err == nil {
		t.Error("removing from a missing directory succeeded")
	}
}

func TestBackupSetFailures(t *testing.T) {
	dir := t.TempDir()
	if _, err := hashBackupSet(filepath.Join(dir, "absent"), "x.db"); err == nil {
		t.Error("hashing in a missing directory succeeded")
	}
	if _, err := hashBackupSet(dir, "x.db"); err == nil {
		t.Error("hashing a missing file succeeded")
	}
	// "<name>.d" is a file, not the collection directory.
	if err := os.WriteFile(filepath.Join(dir, "y.db"), []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "y.db.d"), []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := hashBackupSet(dir, "y.db"); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("hashing with a file for the collection directory: %v", err)
	}
	// A part that cannot be removed: the main file's name is a non-empty
	// directory.
	if err := os.MkdirAll(filepath.Join(dir, "z.db", "inner"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := removeBackupSet(dir, "z.db"); err == nil {
		t.Error("a failed removal was not reported")
	}
	if _, err := sha256Hex(failingReader{}); err == nil {
		t.Error("a failed read was not reported")
	}
}

// failingReader fails every read.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// The cron job of a backup rule reads the rule when it fires: a rule deleted
// or disabled since the reload takes no backup.
func TestBackupJobRereadsTheRule(t *testing.T) {
	s := newBackupTestServer(t)
	cs := NewCronScheduler(s)
	rule, err := s.AutomationManager.Create(AutomationRule{Type: "cron", Name: "n", Enabled: true,
		Schedule: "0 3 * * *", Backup: &BackupAction{}})
	if err != nil {
		t.Fatal(err)
	}
	job := cs.actionJob(rule.ID)
	upd := *s.AutomationManager.Get(rule.ID)
	upd.Enabled = false
	if _, err := s.AutomationManager.Update(rule.ID, upd); err != nil {
		t.Fatal(err)
	}
	job()
	if err := s.AutomationManager.Delete(rule.ID); err != nil {
		t.Fatal(err)
	}
	job()
	if got := backupsIn(t, backupDir()); len(got) != 0 {
		t.Errorf("a disabled or deleted rule took backups: %v", got)
	}
}

// A collection directory that cannot be listed fails the hash rather than
// passing for a set without collection files.
func TestBackupSetUnreadableCollectionDirectory(t *testing.T) {
	covKSkipAsRoot(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "u.db"), []byte("u"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "u.db.d")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) }) // #nosec G302 -- a directory needs x to be removed by t.TempDir
	if _, err := hashBackupSet(dir, "u.db"); err == nil {
		t.Error("hashing with an unreadable collection directory succeeded")
	}
}
