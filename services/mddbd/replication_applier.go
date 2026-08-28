package main

import (
	"log/slog"
	"mddb/internal/binlog"
	"mddb/internal/cache"
	"mddb/internal/vector"
	"strings"
	"sync/atomic"

	bolt "go.etcd.io/bbolt"
)

// ReplicationApplier applies binlog entries from the leader to the local BoltDB and in-memory state.
type ReplicationApplier struct {
	server      *Server
	lastApplied atomic.Uint64
}

// NewReplicationApplier creates a new replication applier
func NewReplicationApplier(s *Server) *ReplicationApplier {
	return &ReplicationApplier{server: s}
}

// Apply applies a single binlog entry to the local database.
func (ra *ReplicationApplier) Apply(entry *binlog.BinlogEntry) error {
	err := ra.server.DBUpdate(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(entry.BucketName))
		if err != nil {
			return err
		}

		switch entry.Type {
		case binlog.BinlogPut:
			return bucket.Put(entry.Key, entry.Value)
		case binlog.BinlogDelete:
			return bucket.Delete(entry.Key)
		case binlog.BinlogDeleteBucket:
			return tx.DeleteBucket([]byte(entry.BucketName))
		case binlog.BinlogCheckpoint:
			// No-op, just a marker
			return nil
		}
		return nil
	})

	if err != nil {
		return err
	}

	// Update in-memory state based on bucket type
	ra.updateInMemoryState(entry)
	ra.lastApplied.Store(entry.LSN)

	return nil
}

// ApplyBatch applies multiple binlog entries in a single BoltDB transaction for efficiency.
func (ra *ReplicationApplier) ApplyBatch(entries []*binlog.BinlogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	err := ra.server.DBUpdate(func(tx *bolt.Tx) error {
		for _, entry := range entries {
			if entry.Type == binlog.BinlogDeleteBucket {
				if err := tx.DeleteBucket([]byte(entry.BucketName)); err != nil {
					slog.Warn("Replication applier failed to delete bucket", "bucketName", entry.BucketName, "err", err)
				}
				continue
			}
			if entry.Type == binlog.BinlogCheckpoint {
				continue
			}

			bucket, err := tx.CreateBucketIfNotExists([]byte(entry.BucketName))
			if err != nil {
				return err
			}

			switch entry.Type {
			case binlog.BinlogPut:
				if err := bucket.Put(entry.Key, entry.Value); err != nil {
					return err
				}
			case binlog.BinlogDelete:
				if err := bucket.Delete(entry.Key); err != nil {
					return err
				}
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	// Update in-memory state for each entry
	for _, entry := range entries {
		ra.updateInMemoryState(entry)
	}

	ra.lastApplied.Store(entries[len(entries)-1].LSN)
	return nil
}

// LastAppliedLSN returns the last applied LSN
func (ra *ReplicationApplier) LastAppliedLSN() uint64 {
	return ra.lastApplied.Load()
}

// SetLastAppliedLSN sets the last applied LSN (used after snapshot restore).
func (ra *ReplicationApplier) SetLastAppliedLSN(lsn uint64) {
	ra.lastApplied.Store(lsn)
}

// updateInMemoryState updates in-memory caches and indices based on the replicated bucket
func (ra *ReplicationApplier) updateInMemoryState(entry *binlog.BinlogEntry) {
	switch entry.BucketName {
	case "vectors":
		ra.applyVector(entry)
	case "docs":
		ra.invalidateDocCache(entry)
	case "webhooks":
		// Reload all webhooks from DB (simple approach)
		if ra.server.WebhookManager != nil {
			_ = ra.server.WebhookManager.LoadAll()
		}
	case "schemas":
		// Reload all schemas from DB
		if ra.server.SchemaManager != nil {
			_ = ra.server.SchemaManager.LoadAll()
		}
	case "automation":
		// Reload all automation rules from DB
		if ra.server.AutomationManager != nil {
			_ = ra.server.AutomationManager.LoadAll()
		}
		if ra.server.CronScheduler != nil {
			ra.server.CronScheduler.Reload()
		}
	case "colmeta":
		// Reload all collection configs from DB
		if ra.server.CollectionManager != nil {
			_ = ra.server.CollectionManager.LoadAll()
		}
	}
}

// applyVector updates the in-memory vector index
func (ra *ReplicationApplier) applyVector(entry *binlog.BinlogEntry) {
	if ra.server.VectorIndex == nil {
		return
	}

	// Parse key: vec|collection|docID
	parts := vector.SplitKey(entry.Key)
	if len(parts) < 3 {
		return
	}
	collection := parts[1]
	docID := parts[2]

	switch entry.Type {
	case binlog.BinlogPut:
		rec, err := vector.UnmarshalEmbeddingRecord(entry.Value)
		if err != nil {
			slog.Warn("Replication applier failed to unmarshal embedding", "err", err)
			return
		}
		ra.server.VectorIndex.Add(collection, docID, rec.Vector)
	case binlog.BinlogDelete:
		ra.server.VectorIndex.Remove(collection, docID)
	}
}

// invalidateDocCache removes the replicated document from the read caches.
//
// GO-002: the cache is keyed by cache.BuildCacheKey(collection, key, lang). The doc
// key is `doc|<collection>|<docID>` where docID itself is `collection|key|lang`,
// so a naive split (vector.SplitKey) over-splits and the previous `collection|docID`
// key never matched anything. We SplitN to recover collection + the full docID,
// and for Put entries unmarshal the doc to build the exact write-path key.
func (ra *ReplicationApplier) invalidateDocCache(entry *binlog.BinlogEntry) {
	parts := strings.SplitN(string(entry.Key), "|", 3)
	if len(parts) < 3 {
		return
	}
	collection := parts[1]
	docID := parts[2] // collection|key|lang (already lowercased)

	// Default to the docID form (correct when key/lang are lowercase, the
	// common case); for Put entries derive the exact cache.BuildCacheKey from the
	// doc itself so original-case keys match too.
	cacheKey := docID
	if len(entry.Value) > 0 {
		// loadDoc auto-detects JSON / protobuf+compression / encryption.
		if doc, err := loadDoc(entry.Value); err == nil {
			cacheKey = cache.BuildCacheKey(collection, doc.Key, doc.Lang)
		}
	}

	if ra.server.Cache != nil {
		ra.server.Cache.Delete(cacheKey)
	}
	if ra.server.LockFreeCache != nil {
		ra.server.LockFreeCache.Delete(cacheKey)
	}
}
