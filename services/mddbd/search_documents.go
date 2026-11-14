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
	err := s.DBViewC(collection, func(tx *bolt.Tx) error {
		var err error
		docs, err = liveDocs(tx, collection, filter, time.Now().Unix())
		return err
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

// liveDocs reads the collection's documents that match filter (all of them
// when it is empty) and have not expired, as search results — without the
// semantic facts (CODE-010).
func liveDocs(tx *bolt.Tx, collection string, filter MetaFilter, now int64) ([]storage.Doc, error) {
	bDocs := tx.Bucket([]byte("docs"))
	l := liveDocList{now: now}
	if len(filter) == 0 {
		prefix := []byte("doc|" + collection + "|")
		c := bDocs.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			if err := l.add(v); err != nil {
				return nil, err
			}
		}
		return l.docs, nil
	}
	for _, id := range sortedIDs(metaFilterIDs(tx, collection, filter)) {
		if err := l.add(bDocs.Get(storage.DocKey(collection, id))); err != nil {
			return nil, err
		}
	}
	return l.docs, nil
}

// liveDocList collects the documents that have not expired.
type liveDocList struct {
	now  int64
	docs []storage.Doc
}

// add keeps one stored document unless it has expired; a missing one (nil,
// an index entry whose document is gone) is skipped.
func (l *liveDocList) add(v []byte) error {
	if v == nil {
		return nil
	}
	d, err := loadResultDoc(v)
	if err != nil {
		return err
	}
	if d.ExpiresAt == 0 || d.ExpiresAt >= l.now {
		l.docs = append(l.docs, *d)
	}
	return nil
}
