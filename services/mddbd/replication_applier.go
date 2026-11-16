package main

import (
	"errors"
	"fmt"
	"log/slog"
	"mddb/internal/binlog"
	"mddb/internal/cache"
	"mddb/internal/storage"
	"mddb/internal/vector"
	"strings"
	"sync/atomic"

	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

// ReplicationApplier applies binlog entries from the leader to the local BoltDB and in-memory state.
//
// GO-050: the position — the last LSN applied and the log it belongs to — is
// written by the transaction that applies the entry (replication_position.go),
// and an entry that cannot be applied is an error the caller must act on, not
// a line in the log: a follower that skipped an entry and went on was no
// longer a copy of its leader, and nothing said so.
type ReplicationApplier struct {
	server      *Server
	lastApplied atomic.Uint64
	logID       atomic.Uint64
}

// NewReplicationApplier creates a new replication applier
func NewReplicationApplier(s *Server) *ReplicationApplier {
	return &ReplicationApplier{server: s}
}

// Apply applies a single binlog entry to the local database.
func (ra *ReplicationApplier) Apply(entry *binlog.BinlogEntry) error {
	return ra.ApplyBatch([]*binlog.BinlogEntry{entry})
}

// ApplyBatch applies entries in one BoltDB transaction per database file
// they touch (replication_apply_plan.go), each carrying the position, and
// updates the in-memory state after each.
func (ra *ReplicationApplier) ApplyBatch(entries []*binlog.BinlogEntry) error {
	for _, g := range ra.plan(entries) {
		// What is already in the files stays applied when a later group
		// fails: the position follows the files, never the batch.
		if err := ra.applyGroup(g); err != nil {
			first := g.entries[0]
			return fmt.Errorf("applying LSN %d (%s %s): %w", first.LSN, first.Type, first.BucketName, err)
		}
		ra.refresh(g.entries)
		ra.lastApplied.Store(g.through)
	}
	return nil
}

// target is the collection whose file an entry belongs to ("" for the main
// file), and whether it must be applied to every collection file — a
// collection bucket deleted outright.
func (ra *ReplicationApplier) target(entry *binlog.BinlogEntry) (string, bool) {
	if ra.server.collectionStore() == nil || !isCollectionBucket([]byte(entry.BucketName)) {
		return "", false
	}
	if entry.Type == binlog.BinlogDeleteBucket {
		return "", true
	}
	c, _ := keyCollection(entry.BucketName, entry.Key)
	return c, false
}

func (ra *ReplicationApplier) applyGroup(g applyGroup) error {
	pos := replPosition{LSN: g.through, LogID: ra.logID.Load()}
	apply := func(tx *bolt.Tx) error {
		for _, entry := range g.entries {
			if err := applyEntry(tx, entry); err != nil {
				return err
			}
		}
		return nil
	}
	// The position goes into the transaction that holds the change.
	applyAndStamp := func(tx *bolt.Tx) error {
		if err := apply(tx); err != nil {
			return err
		}
		return stampApplied(tx, pos)
	}
	if g.everywhere {
		// One transaction per file cannot carry one position: a crash
		// half-way would leave it on the files already done and the entry
		// missing from the rest. It is recorded once, in the main file,
		// after the last file — and the entry, a bucket removed, is applied
		// again without harm if the crash comes before that.
		for _, db := range ra.server.collectionDBs() {
			if err := db.Update(apply); err != nil {
				return err
			}
		}
		return ra.server.saveReplicationPosition(pos)
	}
	if g.target != "" {
		return ra.server.DBUpdateC(g.target, applyAndStamp)
	}
	return ra.server.DBUpdate(applyAndStamp)
}

// applyEntry makes one entry's change in tx.
func applyEntry(tx *bolt.Tx, entry *binlog.BinlogEntry) error {
	switch entry.Type {
	case binlog.BinlogDeleteBucket:
		return ignoreMissingBucket(tx.DeleteBucket([]byte(entry.BucketName)))
	case binlog.BinlogCheckpoint:
		return nil // carries no change; it still moves the position
	case binlog.BinlogPut, binlog.BinlogDelete:
	default:
		// From a leader newer than this build. Skipping what this build
		// does not understand is how a follower drifts.
		return fmt.Errorf("unknown binlog entry type %d", entry.Type)
	}
	bucket, err := tx.CreateBucketIfNotExists([]byte(entry.BucketName))
	if err != nil {
		return err
	}
	key := entry.Key
	if entry.BucketName == "bykey" {
		// DOC-017: a leader on an older release sends key-index
		// entries as written; they land in this build's form.
		key, _ = storage.NormalizeByKeyEntry(key)
	}
	if entry.Type == binlog.BinlogPut {
		return bucket.Put(key, entry.Value)
	}
	return bucket.Delete(key)
}

// ignoreMissingBucket drops the error of removing a bucket that is not
// there: the follower is already where the entry leads.
func ignoreMissingBucket(err error) error {
	if errors.Is(err, bolterrors.ErrBucketNotFound) {
		return nil
	}
	return err
}

// LastAppliedLSN returns the last applied LSN
func (ra *ReplicationApplier) LastAppliedLSN() uint64 {
	return ra.lastApplied.Load()
}

// SetLastAppliedLSN sets the last applied LSN (used after snapshot restore).
func (ra *ReplicationApplier) SetLastAppliedLSN(lsn uint64) {
	ra.lastApplied.Store(lsn)
}

// Position is the last applied LSN and the log it is a position in.
func (ra *ReplicationApplier) Position() replPosition {
	return replPosition{LSN: ra.lastApplied.Load(), LogID: ra.logID.Load()}
}

// SetPosition moves the applier to pos without writing it.
func (ra *ReplicationApplier) SetPosition(pos replPosition) {
	ra.lastApplied.Store(pos.LSN)
	ra.logID.Store(pos.LogID)
}

// updateInMemoryState updates in-memory caches and indices for one
// replicated entry (replication_reload.go).
func (ra *ReplicationApplier) updateInMemoryState(entry *binlog.BinlogEntry) {
	ra.refresh([]*binlog.BinlogEntry{entry})
}

// applyVector updates the in-memory vector index
func (ra *ReplicationApplier) applyVector(entry *binlog.BinlogEntry) {
	if ra.server.VectorIndex == nil {
		return
	}
	// RAG-009: a summary vector goes to the summary index, never a chunk one.
	if vector.IsSummaryKey(entry.Key) {
		ra.server.applyReplicatedSummary(entry)
		return
	}
	// RAG-010: an image vector goes to the image index, never a chunk one.
	if vector.IsImageKey(entry.Key) {
		ra.server.applyReplicatedImage(entry)
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
		// A follower serves queries too, and used to hold replicated vectors in
		// its flat index alone — every other algorithm on a replica answered
		// from whatever it loaded at startup.
		if err := (serverIndexes{ra.server}).IndexChunk(collection, docID, rec.Vector); err != nil {
			// A leader running an older build can replicate a vector its
			// provider returned empty (#252). The follower refuses it the way
			// the leader now would, and says which document needs reindexing.
			slog.Warn("Replication applier refused a vector the index cannot hold",
				"collection", collection, "docID", docID, "err", err)
		}
	case binlog.BinlogDelete:
		(serverIndexes{ra.server}).Remove(collection, docID)
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
