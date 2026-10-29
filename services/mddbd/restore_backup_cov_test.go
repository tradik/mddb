package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mddb/internal/fts"
	"mddb/internal/spell"
)

// covKSkipAsRoot skips tests that rely on file permissions: root opens a
// mode-0 file regardless.
func covKSkipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not stop root")
	}
	if runtime.GOOS == "windows" {
		t.Skip("mode 0 does not stop opening a file on Windows")
	}
}

// covKReopen puts a database back at s.Path after a test left the server
// without one, so the shared cleanup and the assertions have a handle.
func covKReopen(t *testing.T, s *Server) {
	t.Helper()
	db, err := openBolt(s.Path, boltOptionsFor(s.Path))
	if err != nil {
		t.Fatalf("reopen for cleanup: %v", err)
	}
	s.DB = db
}

// A restore whose binlog cannot be reset still succeeds: the database swap is
// what the caller asked for, and the binlog failure is only logged.
func TestCovKRestore_BinlogResetFailureIsNotFatal(t *testing.T) {
	if perCollectionLayout() {
		t.Skip("builds a single-file backup by hand, which the per-collection layout refuses (#336)")
	}
	s, bl, cleanup := replTestServer(t)
	defer cleanup()
	seedKeptDocument(t, s, "restored")
	source := backupNow(t, s)
	if _, _, err := s.addDocument("posts", "kept", "en", nil, "changed after backup", 0, true); err != nil {
		t.Fatal(err)
	}

	if err := bl.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.restoreFromBackup(source); err != nil {
		t.Fatalf("restore failed because of the binlog: %v", err)
	}
	assertDatabaseStillServing(t, s, "restored")
}

// The snapshot rename fails and the database cannot be reopened either: the
// error says both, so an operator knows the server is without a database.
func TestCovKSwapDatabase_SnapshotAndReopenBothFail(t *testing.T) {
	covKSkipAsRoot(t)
	s, cleanup := newTestServer(t)
	defer cleanup()
	seedKeptDocument(t, s, "still here")
	source := backupNow(t, s)

	if err := os.MkdirAll(filepath.Join(s.Path+".pre-restore", "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.Path, 0); err != nil {
		t.Fatal(err)
	}

	err := s.withRestoreLock(func() error {
		return s.swapDatabase(source, func(string) error { t.Error("install ran"); return nil })
	})
	if err == nil || !strings.Contains(err.Error(), "reopen also failed") {
		t.Fatalf("err = %v, want the snapshot and the reopen failure", err)
	}

	if err := os.Chmod(s.Path, 0o600); err != nil {
		t.Fatal(err)
	}
	covKReopen(t, s)
	assertDatabaseStillServing(t, s, "still here")
}

// The install fails and the snapshot cannot be renamed back: the error names
// where the previous database was preserved.
func TestCovKSwapDatabase_RollbackRenameFails(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	seedKeptDocument(t, s, "still here")
	source := backupNow(t, s)
	snapshot := s.Path + ".pre-restore"

	// install leaves a non-empty directory at the live path, which the
	// snapshot cannot be renamed onto.
	err := s.withRestoreLock(func() error {
		return s.swapDatabase(source, func(dst string) error {
			if err := os.MkdirAll(filepath.Join(dst, "blocker"), 0o700); err != nil {
				t.Fatal(err)
			}
			return os.ErrPermission
		})
	})
	if err == nil || !strings.Contains(err.Error(), "database preserved at "+snapshot) {
		t.Fatalf("err = %v, want the preserved snapshot path", err)
	}
	if _, statErr := os.Stat(snapshot); statErr != nil {
		t.Fatalf("snapshot gone: %v", statErr)
	}

	if err := os.RemoveAll(s.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(snapshot, s.Path); err != nil {
		t.Fatal(err)
	}
	covKReopen(t, s)
	assertDatabaseStillServing(t, s, "still here")
}

// The snapshot is renamed back but cannot be opened: the error says the
// rollback reopen failed.
func TestCovKSwapDatabase_ReopenAfterRollbackFails(t *testing.T) {
	covKSkipAsRoot(t)
	s, cleanup := newTestServer(t)
	defer cleanup()
	seedKeptDocument(t, s, "still here")
	source := backupNow(t, s)

	err := s.withRestoreLock(func() error {
		return s.swapDatabase(source, func(string) error {
			if err := os.Chmod(s.Path+".pre-restore", 0); err != nil {
				t.Fatal(err)
			}
			return os.ErrPermission
		})
	})
	if err == nil || !strings.Contains(err.Error(), "reopen after rollback failed") {
		t.Fatalf("err = %v, want the rollback reopen failure", err)
	}

	if err := os.Chmod(s.Path, 0o600); err != nil {
		t.Fatal(err)
	}
	covKReopen(t, s)
	assertDatabaseStillServing(t, s, "still here")
}

// covKRebuildServer is a handler server with every manager rebuild touches
// that the other tests leave nil.
func covKRebuildServer(t *testing.T) (*Server, func()) {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	s.SynonymManager = fts.NewSynonymManager(s.DB)
	s.StopWordManager = fts.NewStopWordManager(s.DB)
	for _, ensure := range []func() error{s.SynonymManager.EnsureBucket, s.StopWordManager.EnsureBucket} {
		if err := ensure(); err != nil {
			cleanup()
			t.Fatal(err)
		}
	}
	s.SpellManager = spell.NewSpellManager(s.DB)
	// The vector index reload runs asynchronously against s.DB; these tests
	// are about the managers.
	s.VectorIndex = nil
	return s, cleanup
}

func TestCovKRebuildInMemoryState_ReloadsSearchManagers(t *testing.T) {
	s, cleanup := covKRebuildServer(t)
	defer cleanup()
	// Written behind the server's manager, as a restored file would be.
	if err := fts.NewSynonymManager(s.DB).Set("blog", "car", []string{"automobile"}); err != nil {
		t.Fatal(err)
	}
	if got := s.SynonymManager.Get("blog", "car"); got != nil {
		t.Fatalf("synonyms known before the rebuild: %v", got)
	}

	s.rebuildInMemoryState()

	if got := s.SynonymManager.Get("blog", "car"); !strings.Contains(strings.Join(got, ","), "automobile") {
		t.Errorf("synonyms after rebuild = %v", got)
	}
}

// One manager failing to reload must not stop the others or leave stale
// cached documents behind.
func TestCovKRebuildInMemoryState_FailedReloadsAreNotFatal(t *testing.T) {
	s, cleanup := covKRebuildServer(t)
	defer cleanup()
	s.Cache.Set("posts|k|en", []byte("stale"))

	_ = s.DB.Close()
	s.rebuildInMemoryState()

	if _, found := s.Cache.Get("posts|k|en"); found {
		t.Error("cache survived a rebuild whose reloads failed")
	}
}
