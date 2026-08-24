package main

import (
	"fmt"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
)

// Routing document payloads to a collection's storage backend (GO-021).
//
// These are the choke points. Every path that stores or fetches a document body
// goes through one of them, so "which backend holds this collection" is decided
// in one place rather than at each of the ninety-odd call sites that touch the
// docs bucket.

// usesExternalBackend reports whether a collection stores its documents
// somewhere other than this server's BoltDB file.
func (s *Server) usesExternalBackend(collection string) bool {
	if s == nil || s.Backends == nil {
		return false
	}
	b := s.Backends.Get(collection)
	return b != nil && b.Name() != "boltdb"
}

// checkBackendAvailable refuses a write to a collection whose configured
// backend could not be created.
//
// Falling through to the default backend would put documents on local disk
// while the operator believes they are in object storage — the exact failure
// this work exists to remove, and one nobody would notice until they needed
// the data.
func (s *Server) checkBackendAvailable(collection string) error {
	if s == nil || s.Backends == nil {
		return nil
	}
	if err := s.Backends.Failed(collection); err != nil {
		return fmt.Errorf("collection %q has an unavailable storage backend: %w", collection, err)
	}
	return nil
}

// putDocPayload writes a document body to the collection's backend before the
// transaction recording it commits.
//
// Order is deliberate and cannot be atomic: a remote object store cannot join a
// bbolt transaction. Writing the payload first means a crash in between leaves
// an object nothing references — wasted space, harmless to readers — instead of
// an index entry pointing at a document that is not there, which is what users
// experience as data loss.
//
// For a BoltDB-backed collection this writes nothing here: the caller's own
// bucket write inside the transaction is the storage, and duplicating it would
// double every write.
func (s *Server) putDocPayload(collection, docID string, data []byte) error {
	if !s.usesExternalBackend(collection) {
		return nil
	}
	if err := s.checkBackendAvailable(collection); err != nil {
		return err
	}
	return s.Backends.Get(collection).PutDoc(collection, docID, data)
}

// deleteDocPayload removes a document body from an external backend.
//
// Failure is reported rather than swallowed: a delete that removed the index
// but left the object would leave data the caller believes is gone.
func (s *Server) deleteDocPayload(collection, docID string) error {
	if !s.usesExternalBackend(collection) {
		return nil
	}
	return s.Backends.Get(collection).DeleteDoc(collection, docID)
}

// docPayload fetches a document body, from the collection's backend when it has
// one and from the bucket otherwise.
//
// tx may be nil, in which case the bucket read opens its own view. It is never
// used for an external backend: a network round trip inside an open bbolt
// transaction holds the freelist and grows the file for as long as the remote
// call takes.
func (s *Server) docPayload(tx *bolt.Tx, collection, docID string) ([]byte, error) {
	if s.usesExternalBackend(collection) {
		return s.Backends.Get(collection).GetDoc(collection, docID)
	}

	if tx != nil {
		b := tx.Bucket(s.BucketNames.Docs)
		if b == nil {
			return nil, errDocsBucketMissing
		}
		return b.Get(storage.DocKey(collection, docID)), nil
	}

	var out []byte
	err := s.DBView(func(tx *bolt.Tx) error {
		b := tx.Bucket(s.BucketNames.Docs)
		if b == nil {
			return errDocsBucketMissing
		}
		if v := b.Get(storage.DocKey(collection, docID)); v != nil {
			out = append([]byte(nil), v...)
		}
		return nil
	})
	return out, err
}

// LoadDocFromBackend reads and decodes one document, wherever it lives.
//
// This is what a caller outside a transaction should use: it picks the right
// storage, copies the bytes out, and decrypts and decodes them the same way for
// every backend, so a document written to S3 comes back byte-identical to one
// written locally.
func (s *Server) LoadDocFromBackend(collection, docID string) (*storage.Doc, error) {
	data, err := s.docPayload(nil, collection, docID)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	return loadDoc(data)
}

// pushBatchPayloads sends every document body to the collection's backend
// before the transaction that indexes them opens (GO-021).
//
// Outside the transaction on purpose: a remote put per document inside an open
// bbolt Update would hold the write lock for the length of the network round
// trips, stalling every other writer for the whole batch.
//
// A document whose payload fails to store is marked failed so the transaction
// skips it — indexing a document the backend does not hold would produce a
// search result that cannot be fetched.
func pushBatchPayloads(s *Server, collection string, processed []*ProcessedDoc) {
	if !s.usesExternalBackend(collection) {
		return
	}
	for _, p := range processed {
		if p.Error != nil {
			continue
		}
		if err := s.putDocPayload(collection, p.DocID, p.Buf); err != nil {
			p.Error = fmt.Errorf("storage backend: %w", err)
		}
	}
}
