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
// Each result carries its revision, the one a read of the document returns
// (#425), so a list view can make a conditional write without reading every
// document it lists first.
//
// sortField is "addedAt", "updatedAt" or "key"; anything else keeps key
// order. It returns the page and how many documents matched in total.
func (s *Server) searchDocuments(collection string, filter MetaFilter, sortField string, asc bool, offset, limit int) ([]DocResponse, int, error) {
	var l *liveDocList
	err := s.DBViewC(collection, func(tx *bolt.Tx) error {
		var err error
		l, err = liveDocs(tx, collection, filter, time.Now().Unix())
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	docs := l.docs
	switch sortField {
	case "addedAt", "updatedAt", "key":
		sortDocs(docs, sortField, asc)
	}
	total := len(docs)
	start := min(max(offset, 0), total)
	end := min(start+max(limit, 0), total)
	page := make([]DocResponse, 0, end-start)
	for i := start; i < end; i++ {
		page = append(page, DocResponse{Doc: docs[i], Revision: l.revision(&docs[i])})
	}
	return page, total, nil
}

// liveDocs reads the collection's documents that match filter (all of them
// when it is empty) and have not expired, as search results — without the
// semantic facts (CODE-010).
func liveDocs(tx *bolt.Tx, collection string, filter MetaFilter, now int64) (*liveDocList, error) {
	bDocs := tx.Bucket([]byte("docs"))
	l := &liveDocList{now: now}
	if len(filter) == 0 {
		prefix := []byte("doc|" + collection + "|")
		c := bDocs.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			if err := l.add(v); err != nil {
				return nil, err
			}
		}
		return l, nil
	}
	for _, id := range sortedIDs(metaFilterIDs(tx, collection, filter)) {
		if err := l.add(bDocs.Get(storage.DocKey(collection, id))); err != nil {
			return nil, err
		}
	}
	return l, nil
}

// liveDocList collects the documents that have not expired.
type liveDocList struct {
	now  int64
	docs []storage.Doc
	// factRevisions holds, by document ID, the revision of each document
	// whose semantic facts were hidden: a revision covers the whole meta, so
	// it is taken before they go. The other documents are hashed as they
	// are, and only those that make the page.
	factRevisions map[string]string
}

// add keeps one stored document unless it has expired; a missing one (nil,
// an index entry whose document is gone) is skipped.
func (l *liveDocList) add(v []byte) error {
	if v == nil {
		return nil
	}
	d, err := loadDoc(v)
	if err != nil {
		return err
	}
	if d.ExpiresAt != 0 && d.ExpiresAt < l.now {
		return nil
	}
	if hasSemanticFacts(d.Meta) {
		if l.factRevisions == nil {
			l.factRevisions = map[string]string{}
		}
		l.factRevisions[d.ID] = docRevision(d)
		hideSemanticFacts(d) // what loadResultDoc does to every other result
	}
	l.docs = append(l.docs, *d)
	return nil
}

// revision is d's revision as a read of the whole document reports it.
func (l *liveDocList) revision(d *storage.Doc) string {
	if rev, ok := l.factRevisions[d.ID]; ok {
		return rev
	}
	return docRevision(d)
}
