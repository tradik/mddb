package migrate

import (
	"bytes"
	"errors"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
)

// Step 1 (DOC-017): the key index stores the key as the identifier does.
//
// Before 2.18.9 a document's identifier was built from its key in lower case
// while its key-index entry kept the key as written, so a document written as
// "README.md" was not found as "readme.md", and writing both left two
// entries. This step rewrites every entry to storage.ByKeyKey's form and
// merges the entries that then share a key.

var (
	byKeyBucket = []byte("bykey")
	docsBucket  = []byte("docs")
)

// chunkSize is how many entries one transaction rewrites, so a large index
// is not rewritten in a single transaction holding every change in memory.
var chunkSize = 10000

// normalizeByKey rewrites the key index of db in chunks. Each chunk commits
// on its own; a chunk that did not commit is redone by the next run, and an
// entry already in normal form is left alone, so the step is safe to repeat.
func normalizeByKey(db *bolt.DB) error {
	var from []byte
	for {
		next, err := normalizeByKeyChunk(db, from, chunkSize)
		if err != nil || next == nil {
			return err
		}
		from = next
	}
}

// byKeyRewrite is one entry to move to its normal form.
type byKeyRewrite struct {
	raw, normal, docID []byte
}

// normalizeByKeyChunk rewrites up to limit entries from from (the start when
// nil) and returns where the next chunk starts, or nil at the end.
func normalizeByKeyChunk(db *bolt.DB, from []byte, limit int) (next []byte, err error) {
	err = db.Update(func(tx *bolt.Tx) error {
		index := tx.Bucket(byKeyBucket)
		if index == nil {
			return nil
		}
		var todo []byKeyRewrite
		c := index.Cursor()
		k, v := c.First()
		if from != nil {
			k, v = c.Seek(from)
		}
		for ; k != nil; k, v = c.Next() {
			if len(todo) == limit {
				next = bytes.Clone(k)
				break
			}
			if normal, changed := storage.NormalizeByKeyEntry(k); changed {
				todo = append(todo, byKeyRewrite{raw: bytes.Clone(k), normal: normal, docID: bytes.Clone(v)})
			}
		}
		// Applied after the scan: bbolt does not promise a cursor survives
		// writes to its bucket.
		docs := tx.Bucket(docsBucket)
		var errs []error
		for _, r := range todo {
			if kept := index.Get(r.normal); kept == nil || replaces(docs, r, kept) {
				errs = append(errs, index.Put(r.normal, r.docID))
			}
			errs = append(errs, index.Delete(r.raw))
		}
		// Any failure rolls the whole chunk back.
		return errors.Join(errs...)
	})
	return next, err
}

// replaces decides a collision between an entry being rewritten and the
// entry already at its normal form: the one pointing at a document that
// exists wins, and when both or neither do, the one already there stays.
func replaces(docs *bolt.Bucket, r byKeyRewrite, kept []byte) bool {
	coll := collectionOf(r.raw)
	return !docExists(docs, coll, kept) && docExists(docs, coll, r.docID)
}

// collectionOf is the collection of a key-index entry: the part between the
// tag and the next '|'. Only called on entries NormalizeByKeyEntry changed,
// which have that shape.
func collectionOf(entry []byte) string {
	rest := entry[bytes.IndexByte(entry, '|')+1:]
	return string(rest[:bytes.IndexByte(rest, '|')])
}

func docExists(docs *bolt.Bucket, coll string, docID []byte) bool {
	return docs != nil && docs.Get(storage.DocKey(coll, string(docID))) != nil
}
