package main

import (
	"bytes"
	"strconv"

	"mddb/internal/binlog"
	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// deleteDocumentInternal deletes a document and all its associated data.
func (s *Server) deleteDocumentInternal(collection, key, lang string) error {
	return s.deleteDocumentIf(collection, key, lang, "")
}

// deleteDocumentIf is deleteDocumentInternal made conditional: with
// expectedRevision set, the document goes only if it is still at that
// revision (#424). "" is unconditional.
func (s *Server) deleteDocumentIf(collection, key, lang, expectedRevision string) error {
	docID := genID(collection, key, lang)
	var bo binlog.BinlogOps
	var deleted storage.Doc // captured for trigger evaluation
	err := s.DBUpdateC(collection, bo.InTx(s.Binlog, func(tx *bolt.Tx) error {
		var err error
		deleted, err = s.deleteDocTx(tx, &bo, docRef{collection, key, lang, docID}, expectedRevision)
		return err
	}))
	err = bo.Settle(s.Binlog, err)
	if err != nil {
		return err
	}
	s.dropDocumentVectors(collection, docID)
	s.afterDelete(docRef{collection, key, lang, docID}, deleted)
	return nil
}

// docRef names one stored document.
type docRef struct {
	collection, key, lang, docID string
}

// deleteTarget loads the document a delete removes and checks the delete's
// condition against it, inside the delete's transaction (#424). A
// conditional delete of a missing document is a lost race, as a conditional
// update of one is: a conflict with an empty current revision. An
// unconditional one keeps the not-found it has always had.
func deleteTarget(bDocs *bolt.Bucket, collection, docID, expectedRevision string) (*storage.Doc, error) {
	v := bDocs.Get(storage.DocKey(collection, docID))
	if v == nil {
		if expectedRevision != "" {
			return nil, checkRevision(nil, expectedRevision)
		}
		return nil, errDocNotFound
	}
	doc, err := loadDoc(v)
	if err != nil {
		return nil, err
	}
	return doc, checkRevision(doc, expectedRevision)
}

// deleteDocTx removes a document, its key index, its revisions and its
// metadata index entries in tx, and returns the document it removed.
func (s *Server) deleteDocTx(tx *bolt.Tx, bo *binlog.BinlogOps, ref docRef, expectedRevision string) (storage.Doc, error) {
	bDocs := tx.Bucket([]byte("docs"))
	doc, err := deleteTarget(bDocs, ref.collection, ref.docID, expectedRevision)
	if err != nil {
		return storage.Doc{}, err
	}
	// GO-021: the payload leaves the backend as well, or a delete removes
	// the index and leaves the data the caller believes is gone.
	if err := s.deleteDocPayload(ref.collection, ref.docID); err != nil {
		return storage.Doc{}, err
	}
	docKey := storage.DocKey(ref.collection, ref.docID)
	if err := bDocs.Delete(docKey); err != nil {
		return storage.Doc{}, err
	}
	bo.Delete("docs", docKey)
	byKeyK := storage.ByKeyKey(ref.collection, ref.key, ref.lang)
	if err := tx.Bucket([]byte("bykey")).Delete(byKeyK); err != nil {
		return storage.Doc{}, err
	}
	bo.Delete("bykey", byKeyK)
	if err := deleteRevisions(tx.Bucket([]byte("rev")), bo, ref.collection, ref.docID); err != nil {
		return storage.Doc{}, err
	}
	return *doc, deleteMetaIndex(tx.Bucket([]byte("idxmeta")), bo, ref.collection, ref.docID, doc.Meta)
}

// deleteRevisions removes a document's revision history.
func deleteRevisions(bRev *bolt.Bucket, bo *binlog.BinlogOps, collection, docID string) error {
	c := bRev.Cursor()
	rp := storage.RevPrefix(collection, docID)
	for k, _ := c.Seek(rp); k != nil && bytes.HasPrefix(k, rp); k, _ = c.Next() {
		if err := bRev.Delete(k); err != nil {
			return err
		}
		bo.Delete("rev", k)
	}
	return nil
}

// deleteMetaIndex removes a document's metadata index entries.
func deleteMetaIndex(bIdx *bolt.Bucket, bo *binlog.BinlogOps, collection, docID string, meta map[string][]string) error {
	for mk, vals := range meta {
		for _, mv := range vals {
			mkey := append(storage.MetaKeyPrefix(collection, mk, mv), []byte(docID)...)
			if err := bIdx.Delete(mkey); err != nil {
				return err
			}
			bo.Delete("idxmeta", mkey)
		}
	}
	return nil
}

// dropDocumentVectors removes a deleted document's vectors: the legacy key,
// every chunk key, the summary vector (RAG-009) and the image (RAG-010).
func (s *Server) dropDocumentVectors(collection, docID string) {
	s.dropDocumentSummary(collection, docID)
	s.dropDocumentImage(collection, docID) // RAG-010: its bytes and image vector
	if s.VectorStore == nil {
		return
	}
	_ = s.VectorStore.Delete(collection, docID)
	for _, searcher := range s.VectorSearchers {
		searcher.Remove(collection, docID)
		for i := 0; i < 100; i++ {
			if searcher.CollectionSize(collection) == 0 {
				break
			}
			searcher.Remove(collection, docID+"#"+strconv.Itoa(i))
		}
	}
}

// afterDelete runs what follows a committed delete: the TTL entry, the
// automation triggers, the read caches, full text, geo, webhooks and SSE.
func (s *Server) afterDelete(ref docRef, deleted storage.Doc) {
	if s.TTLManager != nil {
		_ = s.TTLManager.Remove(ref.collection, ref.docID)
	}
	// Automation triggers (before FTS cleanup so doc is still searchable)
	if s.AutomationManager != nil && env("MDDB_TRIGGERS", "false") == "true" {
		go s.AutomationManager.EvaluateTriggers(ref.collection, deleted, "delete")
	}
	// Invalidate the read cache so a gRPC Get can't serve the just-deleted doc
	// for up to the 5-minute TTL (GO-002). Same cache.BuildCacheKey as the write path.
	s.invalidateDocReadCaches(ref.collection, ref.key, ref.lang)
	if s.FTSIndex != nil {
		_ = s.FTSIndex.Remove(ref.collection, ref.docID)
	}
	s.dropDocumentGeo(ref.collection, ref.docID)
	if s.WebhookManager != nil {
		s.WebhookManager.Fire("doc.deleted", ref.collection, ref.key, ref.lang, nil)
	}
	if s.SSEHub != nil {
		s.SSEHub.BroadcastWithAuth("doc.deleted", ref.collection, ref.key, ref.lang, s.AuthManager)
	}
}

// dropDocumentGeo removes a deleted document from both geo indexes and the
// geo store (a no-op if it had no point).
func (s *Server) dropDocumentGeo(collection, docID string) {
	if s.GeoIndex != nil {
		s.GeoIndex.Remove(collection, docID)
	}
	if s.GeoHashIndex != nil {
		s.GeoHashIndex.Remove(collection, docID)
	}
	if s.GeoStore != nil {
		_ = s.GeoStore.Delete(collection, docID)
	}
}
