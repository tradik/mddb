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

	keys := revisionKeys(bRev, collection, docID)
	if len(keys) <= keep {
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
