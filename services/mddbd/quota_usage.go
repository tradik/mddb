package main

import (
	"bytes"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// What a collection holds, as the write gate counts it (GO-043): the usage
// cache, the scan behind it, and the gate's view of each kind of write.

// writeDelta is how many documents and bytes the writes add to what the
// transaction sees. A key written twice in one batch counts once, at its last
// size.
func writeDelta(tx *bolt.Tx, writes []quotaWrite) quotaUsage {
	sizes := make(map[string]int, len(writes))
	for _, w := range writes {
		sizes[string(w.key)] = w.size
	}
	b := tx.Bucket([]byte("docs"))
	var d quotaUsage
	for key, size := range sizes {
		old := b.Get([]byte(key))
		if old == nil {
			d.docs++
		}
		d.bytes += int64(size - len(old))
	}
	return d
}

// currentUsage is the collection's usage as the transaction sees it, from the
// cache when no other write has committed since it was recorded.
func (qs *quotaState) currentUsage(tx *bolt.Tx, collection string) quotaUsage {
	// A write transaction's ID is one past the commit it starts from.
	base := tx.ID()
	if tx.Writable() {
		base--
	}
	qs.mu.Lock()
	e, ok := qs.usage[collection]
	qs.mu.Unlock()
	if ok && e.db == tx.DB() && e.txid == base {
		return e.usage
	}
	u := scanUsage(tx, collection)
	if !tx.Writable() {
		qs.remember(tx.DB(), base, collection, u)
	}
	return u
}

// remember records a collection's usage as of a commit, unless a later one is
// already recorded for the same file.
func (qs *quotaState) remember(db *bolt.DB, txid int, collection string, u quotaUsage) {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	if e, ok := qs.usage[collection]; ok && e.db == db && e.txid > txid {
		return
	}
	qs.usage[collection] = usageEntry{db: db, txid: txid, usage: u}
}

// scanUsage counts a collection's documents and their stored bytes.
func scanUsage(tx *bolt.Tx, collection string) quotaUsage {
	var u quotaUsage
	prefix := storage.DocKey(collection, "")
	c := tx.Bucket([]byte("docs")).Cursor()
	for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
		u.docs++
		u.bytes += int64(len(v))
	}
	return u
}

// collectionUsage reads a collection's usage outside a write, for metrics.
func (s *Server) collectionUsage(collection string) quotaUsage {
	var u quotaUsage
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		u = s.quotas().currentUsage(tx, collection)
		return nil
	})
	return u
}

// collectionVectorCount is the number of chunk vectors a collection has in
// the largest of the server's indexes (a disk-only collection is only in the
// quantized one).
func (s *Server) collectionVectorCount(collection string) int64 {
	n := 0
	if s.VectorIndex != nil {
		n = s.VectorIndex.CollectionSize(collection)
	}
	for _, vs := range s.VectorSearchers {
		n = max(n, vs.CollectionSize(collection))
	}
	return int64(n)
}

// docQuotaWrites is the gate's view of a batch of prepared documents.
func docQuotaWrites(collection string, processed []*ProcessedDoc) func() []quotaWrite {
	return func() []quotaWrite {
		out := make([]quotaWrite, 0, len(processed))
		for _, p := range processed {
			if p.Error == nil {
				out = append(out, quotaWrite{key: storage.DocKey(collection, p.DocID), size: len(p.Buf)})
			}
		}
		return out
	}
}

// updateQuotaWrites is the gate's view of a batch update.
func updateQuotaWrites(collection string, updated []*UpdatedDoc) func() []quotaWrite {
	return func() []quotaWrite {
		out := make([]quotaWrite, 0, len(updated))
		for _, u := range updated {
			if u.Error == nil {
				out = append(out, quotaWrite{key: storage.DocKey(collection, u.DocID), size: len(u.Buf)})
			}
		}
		return out
	}
}

// oneQuotaWrite is the gate's view of a single document write.
func oneQuotaWrite(key []byte, buf []byte) func() []quotaWrite {
	return func() []quotaWrite { return []quotaWrite{{key: key, size: len(buf)}} }
}
