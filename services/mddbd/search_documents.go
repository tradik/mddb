package main

import (
	"bytes"
	"time"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// searchDocuments is the metadata search behind POST /v1/search, MCP
// search_documents and gRPC Search: the collection's live documents that
// match filter (all when it is empty), sorted, then one page of them. The
// three surfaces each had a copy; gRPC's also returned expired documents.
//
// sortField is "addedAt", "updatedAt" or "key"; anything else keeps key
// order. It returns the page and how many documents matched in total.
func (s *Server) searchDocuments(collection string, filter MetaFilter, sortField string, asc bool, offset, limit int) ([]storage.Doc, int, error) {
	var docs []storage.Doc
	now := time.Now().Unix()
	keep := func(v []byte) error {
		d, err := loadDoc(v)
		if err != nil {
			return err
		}
		if d.ExpiresAt == 0 || d.ExpiresAt >= now {
			docs = append(docs, *d)
		}
		return nil
	}
	err := s.DBViewC(collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if len(filter) == 0 {
			prefix := []byte("doc|" + collection + "|")
			c := bDocs.Cursor()
			for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
				if err := keep(v); err != nil {
					return err
				}
			}
			return nil
		}
		for _, id := range sortedIDs(metaFilterIDs(tx, collection, filter)) {
			if v := bDocs.Get(storage.DocKey(collection, id)); v != nil {
				if err := keep(v); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	switch sortField {
	case "addedAt", "updatedAt", "key":
		sortDocs(docs, sortField, asc)
	}
	total := len(docs)
	start := min(max(offset, 0), total)
	end := min(start+max(limit, 0), total)
	return docs[start:end], total, nil
}
