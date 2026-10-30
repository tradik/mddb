package main

import (
	"bytes"
	"mddb/internal/binlog"
	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// trimRevisions deletes the oldest revisions of a document until at most keep
// remain. Intended to be called from within an active bolt.Tx right after a
// new revision was written, so the cap is enforced synchronously and history
// cannot grow past the configured limit.
//
// Revision keys use the format `rev|collection|docID|<20-digit-timestamp>`,
// so iterating in bolt's natural byte order yields them oldest-first. We
// collect all keys under the prefix, then delete the oldest (total-keep) of
// them; cursor mutation during iteration is avoided intentionally, since
// bucket.Delete mid-iteration can skip entries.
func trimRevisions(tx *bolt.Tx, bo *binlog.BinlogOps, collection, docID string, keep int) error {
	if keep <= 0 {
		return nil
	}
	bRev := tx.Bucket([]byte("rev"))
	if bRev == nil {
		return nil
	}

	return dropOldestRevisions(bRev, bo, collection, docID, keep)
}

// truncateCollectionRevisions keeps the newest keep revisions of every
// document in a collection. It is the Truncate operation, shared by the REST
// handler, the gRPC RPC and the MCP direct client so the three cannot drift:
// keep 0 drops the whole history, a negative keep deletes nothing (unlike
// trimRevisions, the write-time cap, where 0 means "no cap"). Every deletion
// is recorded in bo, when given, for the caller to flush to the binlog after
// the transaction commits. A document that cannot be decoded fails the whole
// truncate, so the transaction rolls back rather than half-applying.
func truncateCollectionRevisions(tx *bolt.Tx, bo *binlog.BinlogOps, collection string, keep int) error {
	bRev := tx.Bucket([]byte("rev"))
	prefix := []byte("doc|" + collection + "|")
	c := tx.Bucket([]byte("docs")).Cursor()
	for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
		d, err := loadDoc(v)
		if err != nil {
			return err
		}
		if err := dropOldestRevisions(bRev, bo, collection, d.ID, keep); err != nil {
			return err
		}
	}
	return nil
}

// dropOldestRevisions deletes the oldest revisions of one document until at
// most keep remain; a negative keep deletes nothing. Each deletion is
// recorded in bo when bo is not nil.
func dropOldestRevisions(bRev *bolt.Bucket, bo *binlog.BinlogOps, collection, docID string, keep int) error {
	keys := revisionKeys(bRev, collection, docID)
	if keep < 0 || len(keys) <= keep {
		return nil
	}
	for _, delk := range keys[:len(keys)-keep] {
		if err := bRev.Delete(delk); err != nil {
			return err
		}
		if bo != nil {
			bo.Delete("rev", delk)
		}
	}
	return nil
}

// revisionKeys copies the revision keys of a document, oldest first. Copies,
// because the caller deletes them: a key slice from a cursor is only valid
// until the bucket changes.
func revisionKeys(bRev *bolt.Bucket, collection, docID string) [][]byte {
	prefix := storage.RevPrefix(collection, docID)
	c := bRev.Cursor()
	var keys [][]byte
	for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
		keys = append(keys, bytes.Clone(k))
	}
	return keys
}
