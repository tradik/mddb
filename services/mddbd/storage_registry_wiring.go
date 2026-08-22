package main

import (
	"fmt"
	"log/slog"
)

// Storage backend wiring (GO-021).
//
// The API has always accepted `storageBackend: "memory"` and `"s3"`, validated
// the S3 credentials, and returned 200 — while nothing read the setting and
// every document went to BoltDB regardless. docs/API.md and openapi.yaml both
// documented it as working. An operator who configured S3, saw success, and
// then treated the local disk as disposable would have lost their data.
//
// This wires the registry that was written for it and never constructed,
// because the fallback it needed — a BoltDB backend — did not exist.
//
// # What a non-default backend does and does not cover
//
// The backend holds the **document payload**. Indexes, revisions, the metadata
// index and the replication binlog stay in BoltDB in every configuration, and
// that is not a shortcut: they are updated inside a single bbolt transaction
// alongside each other, and a remote object store cannot join that transaction.
// Splitting them would trade a correctness guarantee for a storage location.
//
// # Ordering
//
// A document is written to its backend **before** the transaction that records
// it commits. The two cannot be atomic, so the choice is which way a crash
// between them fails:
//
//	backend first  → an object nothing references. Wasted space, found by a
//	                 sweep, harmless to readers.
//	indexes first  → an index entry pointing at a document that is not there.
//	                 A search returns a result that cannot be fetched.
//
// Orphaned objects are recoverable; dangling indexes are what users report as
// data loss.

// InitStorageBackends builds the registry from the stored collection configs.
//
// Called once at startup, after the collection manager has loaded. A backend
// that cannot be created is logged and skipped rather than fatal: one
// misconfigured collection must not stop a server whose other collections are
// fine — but the collection is then explicitly marked as failed so writes to it
// are refused instead of silently landing on the default backend, which is the
// behaviour this whole change exists to remove.
func (s *Server) InitStorageBackends() {
	s.Backends = NewBackendRegistry(NewBoltBackend(s.DB, s.BucketNames))

	if s.CollectionManager == nil {
		return
	}

	for collection, cfg := range s.CollectionManager.ListAll() {
		if cfg == nil || cfg.StorageBackend == "" || cfg.StorageBackend == "boltdb" {
			continue
		}

		backend, err := CreateBackend(cfg.StorageBackend, cfg.StorageConfig)
		if err != nil {
			slog.Error("storage backend unavailable — writes to this collection will be refused",
				"collection", collection, "backend", cfg.StorageBackend, "err", err)
			s.Backends.MarkFailed(collection, err)
			continue
		}

		s.Backends.Register(collection, backend)
		slog.Info("collection storage backend registered",
			"collection", collection, "backend", backend.Name())
	}
}

// ApplyStorageBackend reconciles a collection's backend after its config
// changes, so a setting takes effect without a restart.
func (s *Server) ApplyStorageBackend(collection string, cfg *CollectionConfig) error {
	if s.Backends == nil || cfg == nil {
		return nil
	}

	if cfg.StorageBackend == "" || cfg.StorageBackend == "boltdb" {
		s.Backends.Deregister(collection)
		return nil
	}

	backend, err := CreateBackend(cfg.StorageBackend, cfg.StorageConfig)
	if err != nil {
		return fmt.Errorf("storage backend %q: %w", cfg.StorageBackend, err)
	}

	s.Backends.Register(collection, backend)
	slog.Info("collection storage backend changed",
		"collection", collection, "backend", backend.Name())
	return nil
}

// CloseStorageBackends releases every non-default backend.
//
// The default one holds the server's own database and closing it here would
// take the indexes down with it, which is why BoltBackend.Close is a no-op.
func (s *Server) CloseStorageBackends() {
	if s.Backends == nil {
		return
	}
	for collection, backend := range s.Backends.Registered() {
		if err := backend.Close(); err != nil {
			slog.Warn("closing a storage backend failed",
				"collection", collection, "backend", backend.Name(), "err", err)
		}
	}
}
