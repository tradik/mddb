package main

import (
	"bytes"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
)

// BoltBackend is the StorageBackend over the server's own BoltDB file (GO-021).
//
// The registry was written with a `fallback StorageBackend` field and nothing
// to put in it: the memory and S3 backends existed, the default one did not, so
// the whole registry could never be constructed. That is a large part of why
// `storageBackend: "s3"` was accepted by the API and then ignored — there was
// no path through the registry at all.
//
// This one is deliberately thin. It reads and writes exactly the buckets and
// key layouts the rest of the server already uses, so a collection on the
// default backend behaves identically whether it goes through the registry or
// straight to the buckets. Anything else would make the registry a second
// storage format.
type BoltBackend struct {
	db      *bolt.DB
	buckets BucketNames
}

// NewBoltBackend wraps an open database as the default storage backend.
func NewBoltBackend(db *bolt.DB, buckets BucketNames) *BoltBackend {
	return &BoltBackend{db: db, buckets: buckets}
}

// Name implements the StorageBackend interface.
func (b *BoltBackend) Name() string { return "boltdb" }

// PutDoc implements the StorageBackend interface.
func (b *BoltBackend) PutDoc(collection, docID string, data []byte) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(b.buckets.Docs)
		if bucket == nil {
			return errDocsBucketMissing
		}
		return bucket.Put(storage.DocKey(collection, docID), data)
	})
}

// GetDoc implements the StorageBackend interface. Returns nil, nil when absent,
// matching the interface's contract rather than inventing a not-found error.
func (b *BoltBackend) GetDoc(collection, docID string) ([]byte, error) {
	var out []byte
	err := b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(b.buckets.Docs)
		if bucket == nil {
			return errDocsBucketMissing
		}
		// bbolt values are only valid for the life of the transaction, so
		// the bytes are copied before it closes.
		if v := bucket.Get(storage.DocKey(collection, docID)); v != nil {
			out = bytes.Clone(v)
		}
		return nil
	})
	return out, err
}

// DeleteDoc implements the StorageBackend interface.
func (b *BoltBackend) DeleteDoc(collection, docID string) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(b.buckets.Docs)
		if bucket == nil {
			return errDocsBucketMissing
		}
		return bucket.Delete(storage.DocKey(collection, docID))
	})
}

// ListDocs implements the StorageBackend interface.
func (b *BoltBackend) ListDocs(collection string, fn func(docID string, data []byte) error) error {
	return b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(b.buckets.Docs)
		if bucket == nil {
			return errDocsBucketMissing
		}
		prefix := []byte("doc|" + collection + "|")
		c := bucket.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			if err := fn(string(k[len(prefix):]), bytes.Clone(v)); err != nil {
				return err
			}
		}
		return nil
	})
}

// PutByKey implements the StorageBackend interface.
func (b *BoltBackend) PutByKey(collection, key, lang, docID string) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(b.buckets.ByKey)
		if bucket == nil {
			return errByKeyBucketMissing
		}
		return bucket.Put(storage.ByKeyKey(collection, key, lang), []byte(docID))
	})
}

// GetByKey implements the StorageBackend interface.
func (b *BoltBackend) GetByKey(collection, key, lang string) (string, error) {
	var out string
	err := b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(b.buckets.ByKey)
		if bucket == nil {
			return errByKeyBucketMissing
		}
		if v := bucket.Get(storage.ByKeyKey(collection, key, lang)); v != nil {
			out = string(v)
		}
		return nil
	})
	return out, err
}

// DeleteByKey implements the StorageBackend interface.
func (b *BoltBackend) DeleteByKey(collection, key, lang string) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(b.buckets.ByKey)
		if bucket == nil {
			return errByKeyBucketMissing
		}
		return bucket.Delete(storage.ByKeyKey(collection, key, lang))
	})
}

// Close implements the StorageBackend interface.
//
// The database is owned by the server, not by this backend: closing it here
// would take the indexes, revisions and binlog down with it.
func (b *BoltBackend) Close() error { return nil }
