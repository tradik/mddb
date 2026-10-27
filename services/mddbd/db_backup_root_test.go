package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// The backup's error paths, now that its writes go through an os.Root
// (2.18.7): each one reports, and none leaves a temporary file behind.
func TestBackupThroughARootReportsEveryFailure(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "single")
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	// The directory does not exist.
	if err := s.backupTo(filepath.Join(t.TempDir(), "absent", "x.db")); err == nil {
		t.Error("a backup into a missing directory succeeded")
	}
	// The name cannot be created.
	dir := t.TempDir()
	if err := s.backupTo(filepath.Join(dir, strings.Repeat("n", 300))); err == nil {
		t.Error("a backup under an impossible name succeeded")
	}
	// The verification fails: the copy is not installed and nothing is left.
	t.Setenv("MDDB_VERIFY_TIMEOUT", "1ns")
	if err := s.backupTo(filepath.Join(dir, "v.db")); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Errorf("a failed verification: %v", err)
	}
	t.Setenv("MDDB_VERIFY_TIMEOUT", "")
	// The database is closed.
	_ = s.DB.Close()
	if err := s.backupTo(filepath.Join(dir, "closed.db")); err == nil {
		t.Error("a backup of a closed database succeeded")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*")); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
	if _, err := os.Stat(filepath.Join(dir, "v.db")); !os.IsNotExist(err) {
		t.Errorf("an unverified copy was installed: %v", err)
	}
}

// In the per-collection layout: the collection directory cannot be opened,
// one collection's copy fails, and a collection gone from the store is
// skipped.
func TestBackupOfCollectionFilesThroughARoot(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "blog", "a", "en", "x", nil)
	cf := s.collectionStore()
	if cf == nil {
		t.Fatal("not the per-collection layout")
	}

	dir := t.TempDir()
	// "<name>.d" is a file: the collection directory cannot be opened.
	if err := os.WriteFile(filepath.Join(dir, "a.db.d"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.backupTo(filepath.Join(dir, "a.db")); err == nil {
		t.Error("a backup with a file where its collection directory goes succeeded")
	}
	// A directory where the collection's copy goes: its rename fails.
	if err := os.MkdirAll(filepath.Join(dir, "b.db.d", collectionFileName("blog"), "x"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := s.backupTo(filepath.Join(dir, "b.db")); err == nil || !strings.Contains(err.Error(), `collection "blog"`) {
		t.Errorf("a failed collection copy: %v", err)
	}
	// A collection listed with no open file is skipped.
	cf.mu.Lock()
	cf.dbs["ghost"] = (*bolt.DB)(nil)
	cf.mu.Unlock()
	defer func() { cf.mu.Lock(); delete(cf.dbs, "ghost"); cf.mu.Unlock() }()
	if err := s.backupTo(filepath.Join(dir, "c.db")); err != nil {
		t.Errorf("a backup with a collection gone from the store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "c.db.d", collectionFileName("blog"))); err != nil {
		t.Errorf("the collection file was not copied: %v", err)
	}
}

// The jail directory cannot be opened as a root, or created.
func TestConfineToDirWhenTheJailCannotBeOpened(t *testing.T) {
	jail := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", jail)
	openJail = func(string) (*os.Root, error) { return nil, errors.New("boom") }
	defer func() { openJail = os.OpenRoot }()
	if _, err := safeBackupPath("x.db", false); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("an unopenable jail: %v", err)
	}
	openJail = os.OpenRoot

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MDDB_BACKUP_DIR", filepath.Join(file, "under-a-file"))
	if _, err := safeBackupPath("x.db", false); err == nil || !strings.Contains(err.Error(), "create backup dir") {
		t.Errorf("a jail that cannot be created: %v", err)
	}
}
