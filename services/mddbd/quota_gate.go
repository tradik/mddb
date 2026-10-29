package main

import (
	"path/filepath"
	"sync"

	"mddb/internal/envconf"

	bolt "go.etcd.io/bbolt"
)

// The write gate (GO-043).
//
// checkWriteQuota runs inside the write transaction of every path that stores
// documents — addDocumentIf, updateDocument, both batch commits and the batch
// update — so REST, gRPC, MCP, GraphQL, batch and ingest are refused by the
// same code with the same error. Running inside the transaction makes the
// check exact: BoltDB has one writer per file, so two writers cannot both see
// room for the last document.

// quotaState is the server's quota bookkeeping, built on first use.
type quotaState struct {
	// diskReserve is MDDB_DISK_RESERVE_BYTES: document writes are refused
	// while the filesystem has less than this available.
	diskReserve int64
	// vectorRAMLimit is MDDB_VECTOR_RAM_LIMIT_BYTES: writes that would add
	// vectors to RAM are refused once the in-memory vectors reach it.
	vectorRAMLimit int64

	budget *embeddingBudget

	mu       sync.Mutex
	usage    map[string]usageEntry
	reported map[string]int64 // collection|resource → unix time of the last report
}

// quotaUsage is what a collection holds in its docs bucket.
type quotaUsage struct {
	docs  int64
	bytes int64
}

// usageEntry is a collection's usage as of a committed transaction. It is
// valid for a transaction that starts from that commit and for no other: a
// write to the file by anyone — a delete, a replication apply, a restore —
// moves the transaction ID on and forces a rescan.
type usageEntry struct {
	db    *bolt.DB
	txid  int
	usage quotaUsage
}

// quotaWrite is one document a write is about to store.
type quotaWrite struct {
	key  []byte // its key in the docs bucket
	size int    // its stored size
}

// quotaDiskSpace is diskSpace, a variable so tests can stand in a nearly full
// filesystem.
var quotaDiskSpace = diskSpace

// quotas returns the server's quota state, reading the limits from the
// environment the first time.
func (s *Server) quotas() *quotaState {
	s.quotaOnce.Do(func() {
		qs := &quotaState{
			diskReserve:    envconf.Int64("MDDB_DISK_RESERVE_BYTES", 0),
			vectorRAMLimit: envconf.Int64("MDDB_VECTOR_RAM_LIMIT_BYTES", 0),
			usage:          make(map[string]usageEntry),
			reported:       make(map[string]int64),
		}
		qs.budget = newEmbeddingBudget(s.collectionQuota, s.reportQuota)
		s.quota = qs
	})
	return s.quota
}

// checkWriteQuota refuses a write that would take the collection past its
// quota, or the server past its disk reserve or vector RAM limit. writes is
// called only when a limit applies, so a collection without a quota pays for
// one map lookup. A write that does not grow the collection — an update to a
// smaller document — is never refused by a quota, so a collection over a
// lowered limit can still be cut down to size.
func (s *Server) checkWriteQuota(tx *bolt.Tx, collection string, writes func() []quotaWrite) error {
	qs := s.quotas()
	if qe := qs.checkDiskReserve(s.Path); qe != nil {
		return s.reportQuota(qe)
	}
	q := s.collectionQuota(collection)
	ramCheck := qs.vectorRAMLimit > 0 && s.Embedding != nil && !s.collectionDiskOnly(collection)
	if !q.limitsStorage() && !ramCheck {
		return nil
	}
	delta := writeDelta(tx, writes())
	if delta.docs <= 0 && delta.bytes <= 0 {
		return nil
	}
	if ramCheck && s.VectorIndex != nil {
		if used := s.VectorIndex.MemoryBytes(); used >= qs.vectorRAMLimit {
			return s.reportQuota(&QuotaError{Resource: quotaVectorRAM, Limit: qs.vectorRAMLimit, Used: used})
		}
	}
	if !q.limitsStorage() {
		return nil
	}
	base := qs.currentUsage(tx, collection)
	next := quotaUsage{docs: base.docs + delta.docs, bytes: base.bytes + delta.bytes}
	if qe := s.storageQuotaExceeded(collection, q, base, next, delta); qe != nil {
		return s.reportQuota(qe)
	}
	// Captured now: a closed transaction no longer knows its database.
	db, txid := tx.DB(), tx.ID()
	tx.OnCommit(func() { qs.remember(db, txid, collection, next) })
	return nil
}

// storageQuotaExceeded compares a write's outcome with the collection's limits.
func (s *Server) storageQuotaExceeded(collection string, q *CollectionQuota, base, next, delta quotaUsage) *QuotaError {
	switch {
	case q.MaxDocuments > 0 && delta.docs > 0 && next.docs > q.MaxDocuments:
		return &QuotaError{Collection: collection, Resource: quotaDocuments, Limit: q.MaxDocuments, Used: base.docs}
	case q.MaxBytes > 0 && delta.bytes > 0 && next.bytes > q.MaxBytes:
		return &QuotaError{Collection: collection, Resource: quotaBytes, Limit: q.MaxBytes, Used: base.bytes}
	}
	if q.MaxVectors > 0 {
		if n := s.collectionVectorCount(collection); n >= q.MaxVectors {
			return &QuotaError{Collection: collection, Resource: quotaVectors, Limit: q.MaxVectors, Used: n}
		}
	}
	return nil
}

// checkDiskReserve refuses writes while the filesystem holding the database
// has less than the reserve available. A filesystem that cannot be asked is
// not known to be full, so an error lets the write through.
func (qs *quotaState) checkDiskReserve(path string) *QuotaError {
	if qs.diskReserve <= 0 || path == "" {
		return nil
	}
	avail, _, err := quotaDiskSpace(filepath.Dir(path))
	if err != nil || avail >= uint64(qs.diskReserve) {
		return nil
	}
	return &QuotaError{Resource: quotaDiskReserve, Limit: qs.diskReserve, Used: int64(avail)} // #nosec G115 -- avail is below a positive int64 here
}
