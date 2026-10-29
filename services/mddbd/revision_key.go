package main

import (
	"bytes"
	"fmt"
	"strconv"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// nextRevisionKey is the key for a new revision of docID (#358).
//
// A revision key ends in the write's unix second, and that second is also
// the revision's public identifier: list_revisions returns it and restore
// takes it. Two writes within one second used to get the same key, so the
// second Put replaced the first revision. The key is now the current second,
// or one past the newest existing revision when that is not earlier. Keys,
// their format and the meaning of the timestamp are unchanged; only a burst
// of writes dates its later revisions a few seconds ahead, still in order.
//
// It must run inside the write transaction that stores the revision, so two
// writers cannot pick the same second.
func nextRevisionKey(bRev *bolt.Bucket, collection, docID string, now int64) []byte {
	prefix := storage.RevPrefix(collection, docID)
	c := bRev.Cursor()
	k, _ := c.Seek(append(append([]byte(nil), prefix...), 0xFF))
	if k == nil {
		k, _ = c.Last()
	} else {
		k, _ = c.Prev()
	}
	if k != nil && bytes.HasPrefix(k, prefix) {
		if last, err := strconv.ParseInt(string(k[len(prefix):]), 10, 64); err == nil && last >= now {
			now = last + 1
		}
	}
	return append(prefix, []byte(fmt.Sprintf("%020d", now))...)
}
