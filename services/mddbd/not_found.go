package main

import (
	"errors"
	"fmt"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// "Not found" is one error (DOC-018): a document or revision that does not
// exist. REST answers it with 404 (see bad), gRPC with NOT_FOUND; before,
// some paths answered 400 and callers had to parse the message to tell a
// missing document from a malformed request.
var errNotFound = errors.New("not found")

// errDocNotFound is a document that does not exist or has expired.
var errDocNotFound = fmt.Errorf("document %w", errNotFound)

// restoreRevision writes a stored revision back as the current document —
// the one implementation behind REST, MCP and gRPC restore.
func (s *Server) restoreRevision(collection, key, lang string, timestamp int64) (storage.Doc, error) {
	revKey := append(storage.RevPrefix(collection, genID(collection, key, lang)), []byte(fmt.Sprintf("%020d", timestamp))...)
	var revDoc *storage.Doc
	err := s.DBViewC(collection, func(tx *bolt.Tx) error {
		var v []byte
		if b := tx.Bucket([]byte("rev")); b != nil {
			v = b.Get(revKey)
		}
		if v == nil {
			return fmt.Errorf("revision %w for timestamp %d", errNotFound, timestamp)
		}
		var err error
		revDoc, err = loadDoc(v)
		return err
	})
	if err != nil {
		return storage.Doc{}, err
	}
	doc, _, err := s.addDocument(collection, key, lang, revDoc.Meta, revDoc.ContentMD, 0, true)
	return doc, err
}
