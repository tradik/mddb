package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"mddb/internal/vector"

	bolt "go.etcd.io/bbolt"
)

// Manual backup restore (SEC-015 / SEC-016).
//
// This is the single implementation behind POST /v1/restore and the gRPC
// Restore RPC. Both transports used to hand-roll the swap: HTTP closed the
// database outside the restore lock and, when the copy or reopen failed,
// left the server with a closed — or an already overwritten — database; gRPC
// copied the backup underneath the still-open handle, so the server kept
// serving the old data, reported success, and buried every later write at
// the next process restart.
//
// Contract: validate the backup BEFORE touching the live file, keep the live
// file as a snapshot until the swap is proven, and never return with s.DB in
// a state other than "open and serving" — on any failure the previous
// database comes back.

// restoreFromBackup replaces the live database with the backup at safeFrom
// (already validated by safeBackupPath) and rebuilds derived in-memory state.
func (s *Server) restoreFromBackup(safeFrom string) error {
	// The backup must be a database bolt can open at all — checked read-only,
	// before the live file is touched, so a truncated or non-bbolt file can
	// never destroy the current database.
	check, err := bolt.Open(safeFrom, 0600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return fmt.Errorf("backup %s is not a usable database: %w", filepath.Base(safeFrom), err)
	}
	_ = check.Close()

	// The swap drains every in-flight DBView/DBUpdate (GO-004) so no handler
	// observes a closed or half-swapped *bolt.DB.
	if err := s.withRestoreLock(func() error {
		return s.swapDatabaseFile(safeFrom)
	}); err != nil {
		return err
	}

	// Reset the binlog after a restore — followers must re-snapshot from the
	// restored state instead of applying their old LSN stream onto it.
	if s.Binlog != nil {
		if err := s.Binlog.Rotate(0); err != nil {
			slog.Warn("failed to reset binlog after restore", "err", err)
		}
	}
	return nil
}

// swapDatabaseFile performs snapshot -> close -> copy -> reopen with rollback.
// MUST be called while holding the restore write lock.
func (s *Server) swapDatabaseFile(safeFrom string) error {
	reopen := func() error {
		db, err := bolt.Open(s.Path, 0600, getOptimizedBoltOptions())
		if err != nil {
			return err
		}
		s.DB = db
		s.rebuildInMemoryState()
		return nil
	}

	// Close first, then move the live file aside as the rollback snapshot.
	// Renaming (not copying) is atomic, costs no I/O on multi-gigabyte
	// databases, and only works reliably on a closed file — which is also the
	// only order Windows supports (WIN-002).
	_ = s.DB.Close()
	snapshot := s.Path + ".pre-restore"
	if err := os.Rename(s.Path, snapshot); err != nil {
		if roErr := reopen(); roErr != nil {
			return fmt.Errorf("pre-restore snapshot failed: %w (reopen also failed: %v)", err, roErr)
		}
		return fmt.Errorf("pre-restore snapshot failed (database untouched): %w", err)
	}

	rollback := func(cause error, stage string) error {
		_ = os.Remove(s.Path) // drop the failed copy, if any
		if rbErr := os.Rename(snapshot, s.Path); rbErr != nil {
			return fmt.Errorf("%s: %w (rollback rename failed: %v — database preserved at %s)", stage, cause, rbErr, snapshot)
		}
		if roErr := reopen(); roErr != nil {
			return fmt.Errorf("%s: %w (reopen after rollback failed: %v)", stage, cause, roErr)
		}
		return fmt.Errorf("%s (previous database restored): %w", stage, cause)
	}

	if err := copyFile(safeFrom, s.Path); err != nil {
		return rollback(err, "restore copy failed")
	}
	if err := reopen(); err != nil {
		return rollback(err, "restored database failed to open")
	}

	_ = os.Remove(snapshot)
	return nil
}

// rebuildInMemoryState reloads all in-memory state from the new database.
// MUST be called while holding the restore write lock: it reads the freshly
// swapped s.DB and re-points the caches/managers. The managers and caches are
// reloaded IN PLACE (same pointers) so concurrent readers of
// Server.WebhookManager / SchemaManager / Cache never see a swapped field
// (GO-004). Shared by the replication snapshot path and manual restore.
func (s *Server) rebuildInMemoryState() {
	// Reload vector index. The store wraps the new DB; the in-memory index is
	// rebuilt asynchronously (loadVectorIndex acquires the restore read lock
	// via DBView, so it waits until this restore releases the write lock).
	if s.VectorIndex != nil && s.VectorStore != nil {
		s.VectorStore = vector.NewVectorStore(s.DB)
		go s.loadVectorIndex()
	}

	// Re-point FTS and its managers at the new handle; their in-memory caches
	// are rebuilt from it. Before SEC-015/016 these kept the closed handle
	// after a snapshot restore, so every search errored until restart.
	if s.FTSIndex != nil {
		s.FTSIndex.Reload(s.DB)
	}
	if s.SynonymManager != nil {
		if err := s.SynonymManager.Reload(s.DB); err != nil {
			slog.Warn("synonym reload after restore failed", "err", err)
		}
	}
	if s.StopWordManager != nil {
		if err := s.StopWordManager.Reload(s.DB); err != nil {
			slog.Warn("stopword reload after restore failed", "err", err)
		}
	}

	// Reload webhooks in place.
	if s.WebhookManager != nil {
		if err := s.WebhookManager.Reload(s.DB); err != nil {
			slog.Warn("webhook reload after restore failed", "err", err)
		}
	}

	// Reload schemas in place.
	if s.SchemaManager != nil {
		if err := s.SchemaManager.Reload(s.DB); err != nil {
			slog.Warn("schema reload after restore failed", "err", err)
		}
	}

	// Drop every read cache in place — same objects (and their cleanup
	// goroutines), contents cleared. A cache that outlives its database is
	// how GO-002 happened.
	if s.Cache != nil {
		s.Cache.Clear()
	}
	if s.LockFreeCache != nil {
		s.LockFreeCache.Clear()
	}
	s.SearchCache.Clear()

	slog.Info("in-memory state rebuilt after database swap")
}
