package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// OPS-023 against an S3-compatible bucket: an in-process fake (fakeS3), since
// MinIO is not available where the suite runs.

// A backup to an S3-compatible bucket, and a restore from what is in it: the
// objects are fetched into the backup directory and restored as any backup.
func TestScheduledBackupToS3AndRestoreFromIt(t *testing.T) {
	s := newBackupTestServer(t)
	addTestDoc(t, s, "blog", "kept", "en", "before the backup", nil)
	f := newFakeS3(t)
	rule, err := s.AutomationManager.Create(AutomationRule{Type: "cron", Name: "offsite", Enabled: true,
		Schedule: "0 3 * * *", Backup: fakeS3Action(f, "prod/mddb-", 2)})
	if err != nil {
		t.Fatal(err)
	}
	if rule.Backup.StorageConfig.SecretKey != "" || !rule.Backup.StorageConfig.SecretKeySet {
		t.Errorf("the created rule shows its secret: %+v", rule.Backup.StorageConfig)
	}

	when := time.Date(2026, 10, 30, 3, 0, 0, 0, time.UTC)
	out, err := s.runScheduledBackup(*s.AutomationManager.stored(rule.ID), when)
	if err != nil {
		t.Fatal(err)
	}
	name := "prod/mddb-20261030T030000Z.db"
	if out.where != "s3://backups/"+name {
		t.Errorf("stored at %q", out.where)
	}
	keys := f.keys("backups")
	wantKeys := 2
	if perCollectionLayout() {
		wantKeys = 3 // and prod/mddb-….db.d/blog.db
	}
	if len(keys) != wantKeys {
		t.Fatalf("objects: %v", keys)
	}
	if left, _ := os.ReadDir(filepath.Join(backupDir(), backupStagingDir)); len(left) != 0 {
		t.Errorf("staging not emptied: %v", left)
	}

	// Fetch the objects into the backup directory, as `aws s3 cp --recursive`
	// would, check them against the checksum object, and restore.
	fetched := filepath.Join(backupDir(), "fetched")
	for _, k := range keys {
		dst := filepath.Join(fetched, filepath.FromSlash(strings.TrimPrefix(k, "prod/")))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, f.object("backups", k), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	assertChecksumFile(t, fetched, "mddb-20261030T030000Z.db")
	addTestDoc(t, s, "blog", "later", "en", "after the backup", nil)
	from, err := safeBackupPath("fetched/mddb-20261030T030000Z.db", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.restoreFromBackup(from); err != nil {
		t.Fatal(err)
	}
	if got := countDocs(t, s, "blog"); got != 1 {
		t.Errorf("after the restore from S3: %d documents, want 1", got)
	}
}

// Retention in a bucket: with keep 2, the third run removes the first
// backup's every object — main, checksum and collection files — and leaves
// keys outside the prefix, and keys under it that are not backups, alone.
func TestScheduledBackupS3Retention(t *testing.T) {
	s := newBackupTestServer(t)
	addTestDoc(t, s, "blog", "a", "en", "x", nil)
	f := newFakeS3(t)
	a := fakeS3Action(f, "nightly-", 2)
	rule := AutomationRule{ID: "r1", Type: "cron", Name: "n", Backup: a}
	if err := a.normalize(); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	for i := range 3 {
		if _, err := s.runScheduledBackup(rule, day.AddDate(0, 0, i)); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			f.mu.Lock()
			f.buckets["backups"]["nightly-notes.txt"] = []byte("mine")
			f.buckets["backups"]["other/x.db"] = []byte("theirs")
			f.mu.Unlock()
		}
	}
	for _, k := range f.keys("backups") {
		if strings.HasPrefix(k, "nightly-20260101") {
			t.Errorf("the oldest backup's %s is left", k)
		}
	}
	keys := strings.Join(f.keys("backups"), " ")
	for _, k := range []string{"nightly-notes.txt", "other/x.db", "nightly-20260102T030000Z.db", "nightly-20260103T030000Z.db.sha256"} {
		if !strings.Contains(keys, k) {
			t.Errorf("%s was removed: %s", k, keys)
		}
	}
}

// The S3 failures: the bucket cannot be reached, an upload is refused, the
// object read back differs, the listing fails after the backup is stored, a
// removal fails. Each is reported; only a stored backup sets the metric.
func TestScheduledBackupS3Failures(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeS3(t)
	clock := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	run := func(a *BackupAction) (backupOutcome, error) {
		t.Helper()
		if err := a.normalize(); err != nil {
			t.Fatal(err)
		}
		clock = clock.Add(time.Hour) // every run a new name
		return s.runScheduledBackup(AutomationRule{ID: "s3f", Type: "cron", Backup: a}, clock)
	}

	f.failNext(http.MethodHead, http.StatusForbidden)
	if _, err := run(fakeS3Action(f, "", 1)); err == nil || !strings.Contains(err.Error(), "connecting to the s3 target") {
		t.Errorf("an unreachable bucket: %v", err)
	}
	f.failNext(http.MethodHead, 0)

	f.failNext(http.MethodPut, http.StatusForbidden)
	f.mu.Lock()
	f.buckets["backups"] = map[string][]byte{}
	f.mu.Unlock()
	if out, err := run(fakeS3Action(f, "", 1)); err == nil || !strings.Contains(err.Error(), "uploading") || out.where != "" {
		t.Errorf("a refused upload: %v %+v", err, out)
	}
	f.failNext(http.MethodPut, 0)

	f.corrupt = true
	if _, err := run(fakeS3Action(f, "", 1)); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("a corrupted object: %v", err)
	}
	f.corrupt = false
	if page := scrape(t, s); strings.Contains(page, backupSuccessGauge+"{") {
		t.Error("a failed backup set the success metric")
	}

	f.failNext(http.MethodGet, 0)
	f.failNext("LIST", http.StatusForbidden)
	out, err := run(fakeS3Action(f, "", 1))
	if err == nil || !strings.Contains(err.Error(), "retention failed") || out.where == "" {
		t.Errorf("a failed listing: %v %+v", err, out)
	}
	if !strings.Contains(scrape(t, s), backupSuccessGauge+`{rule="s3f"}`) {
		t.Error("a stored backup whose retention failed did not set the metric")
	}
	f.failNext("LIST", 0)

	f.failNext(http.MethodDelete, http.StatusForbidden)
	if _, err := run(fakeS3Action(f, "", 1)); err == nil || !strings.Contains(err.Error(), "retention failed") {
		t.Errorf("a failed removal: %v", err)
	}
}

// A listing that fails part-way — the bucket's listing errors — is reported
// by keys, and an object that cannot be read back is reported by upload.
func TestS3BackupTargetReadBackFailure(t *testing.T) {
	f := newFakeS3(t)
	a := fakeS3Action(f, "", 0)
	if err := a.normalize(); err != nil {
		t.Fatal(err)
	}
	target, err := newS3BackupTarget(a)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.db"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := hashBackupSet(dir, "x.db")
	if err != nil {
		t.Fatal(err)
	}
	f.failNext(http.MethodGet, http.StatusForbidden)
	if _, err := target.store(set, "x.db"); err == nil {
		t.Error("a failed read-back was not reported")
	}
	f.failNext(http.MethodGet, 0)
	// The set's file is gone before the upload.
	_ = os.Remove(filepath.Join(dir, "x.db"))
	if _, err := target.store(set, "x.db"); err == nil {
		t.Error("uploading a missing file succeeded")
	}
	// A removal whose listing of collection objects fails.
	f.failNext("LIST", http.StatusForbidden)
	if err := target.remove("x.db"); err == nil {
		t.Error("a removal with a failed listing succeeded")
	}
	f.failNext("LIST", 0)
	// The staging directory itself is gone.
	set.dir = filepath.Join(dir, "absent")
	if _, err := target.store(set, "x.db"); err == nil {
		t.Error("storing from a missing directory succeeded")
	}
}
