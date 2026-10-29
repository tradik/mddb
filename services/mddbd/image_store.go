package main

import (
	"bytes"

	"mddb/internal/binlog"
	"mddb/internal/boltx"

	bolt "go.etcd.io/bbolt"
)

// Image bytes (RAG-010).
//
// An image document is an ordinary document — its text is its alt text and
// caption, which is what full-text search and text embeddings see — and its
// bytes live apart, in the "images" bucket under img|collection|docID. Kept
// out of the document they stay out of everything that reads documents: the
// full-text index, text chunking and embedding, the metadata index, revisions,
// and every response that returns a document. Base64 in the content would
// have put two megabytes of noise into each of those.
//
// The bucket is a collection bucket, so the bytes move with the collection
// in the per-collection layout, are in every backup, replicate through the
// binlog and go when the collection or the document goes. A collection with
// encryption on has them encrypted, as its documents are.

// bucketImages holds image bytes.
var bucketImages = []byte("images")

// imageBytesKey is the key of an image document's bytes.
func imageBytesKey(collection, docID string) []byte {
	return []byte("img|" + collection + "|" + docID)
}

// putImageBytes stores an image document's bytes.
func (s *Server) putImageBytes(collection, docID string, data []byte) error {
	var err error
	if globalEncryptor != nil {
		data, err = globalEncryptor.Encrypt(data, collection)
	}
	key := imageBytesKey(collection, docID)
	if err == nil {
		err = s.DBUpdateC(collection, func(tx *bolt.Tx) error {
			// Created at startup in both layouts; created here too, so a
			// file from before the bucket existed takes images at once.
			b, err := tx.CreateBucketIfNotExists(bucketImages)
			if err == nil {
				err = b.Put(key, data)
			}
			return err
		})
	}
	if err == nil && s.Binlog != nil {
		_ = s.Binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: string(bucketImages), Key: key, Value: bytes.Clone(data)})
	}
	return err
}

// imageBytes returns an image document's bytes, nil when it has none.
func (s *Server) imageBytes(collection, docID string) []byte {
	var out []byte
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		if b := tx.Bucket(bucketImages); b != nil {
			out = bytes.Clone(b.Get(imageBytesKey(collection, docID)))
		}
		return nil
	})
	if out == nil {
		return nil
	}
	plain, err := maybeDecrypt(out)
	if err != nil {
		return nil
	}
	return plain
}

// deleteImageBytes removes an image document's bytes, if it has any.
func (s *Server) deleteImageBytes(collection, docID string) {
	key := imageBytesKey(collection, docID)
	removed := false
	_ = s.DBUpdateC(collection, func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketImages)
		if b == nil || b.Get(key) == nil {
			return nil
		}
		removed = true
		return b.Delete(key)
	})
	if removed && s.Binlog != nil {
		_ = s.Binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogDelete, BucketName: string(bucketImages), Key: key})
	}
}

// deleteCollectionImageBytes removes a collection's image bytes inside the
// collection delete's transaction, recording each deletion for followers.
func deleteCollectionImageBytes(tx *bolt.Tx, collection string, bo *binlog.BinlogOps) error {
	b := tx.Bucket(bucketImages)
	if b == nil {
		return nil
	}
	prefix := imageBytesKey(collection, "")
	var keys [][]byte
	c := b.Cursor()
	for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
		keys = append(keys, bytes.Clone(k))
	}
	for _, k := range keys {
		if err := b.Delete(k); err != nil {
			return err
		}
		bo.Delete(string(bucketImages), k)
	}
	return nil
}

// countImages is how many image documents a collection holds bytes for, or
// the whole server for "".
func (s *Server) countImages(collection string) int {
	n := 0
	visit := func(_, _ []byte) bool { n++; return true }
	if collection != "" {
		_ = s.DBScanC(collection, bucketImages, imageBytesKey(collection, ""), visit)
		return n
	}
	for _, db := range s.collectionDBs() {
		_ = boltx.Scan(db, bucketImages, []byte("img|"), visit)
	}
	return n
}
