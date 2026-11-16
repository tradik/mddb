package main

import (
	"errors"
	"mddb/internal/binlog"
	"mddb/internal/cache"
	"mddb/internal/storage"
	"time"

	bolt "go.etcd.io/bbolt"
)

// addDocument is the shared internal method for adding/updating a document.
// Returns the saved document, whether it was newly created, and any error.
// addDocument is the single write path for one document, shared by every
// transport (HTTP, gRPC, MCP, GraphQL). It performs the in-transaction insert,
// metadata index, and — when saveRevision is true — the revision write plus
// MaxRevisions trimming, then runs the shared post-write side-effect pipeline.
//
// saveRevision lets the transport opt out of revision history (gRPC exposes a
// per-request SaveRevision flag); all other callers pass true to preserve the
// always-record-a-revision behaviour they have always had.
func (s *Server) addDocument(collection, key, lang string, meta map[string][]string, contentMD string, ttl int64, saveRevision bool) (storage.Doc, bool, error) {
	return s.addDocumentIf(docWrite{collection: collection, key: key, lang: lang, meta: meta, contentMD: contentMD, ttl: ttl, saveRevision: saveRevision, cond: writeCondition{}})
}

// addDocumentIf is addDocument made conditional: with cond.ExpectedRevision
// set, the write succeeds only if the document is at that revision — and
// fails if it does not exist yet (INT-021); with cond.IfAbsent, only if it
// does not exist (#423). The zero condition is unconditional.
func (s *Server) addDocumentIf(w docWrite) (storage.Doc, bool, error) {
	return s.addDocumentWith(w, postBatchOptions{})
}

// docWrite is one document write through addDocumentIf / addDocumentWith;
// now is set by addDocumentWith.
type docWrite struct {
	collection, key, lang string
	meta                  map[string][]string
	contentMD             string
	ttl                   int64
	saveRevision          bool
	cond                  writeCondition
	now                   int64
}

// docWriteResult is what a write transaction stored.
type docWriteResult struct {
	doc   storage.Doc
	isNew bool
	buf   []byte // the marshaled document, reused to refresh the read cache (GO-002)
}

// addDocumentWith is addDocumentIf with the after-write pipeline narrowed by
// opts: a system document that must not be embedded, indexed for full text
// or sent to webhooks (a chat conversation, #405) says so here and still
// takes the one write path.
func (s *Server) addDocumentWith(w docWrite, opts postBatchOptions) (storage.Doc, bool, error) {
	if err := s.validateDocWrite(w.collection, w.key, w.lang, w.meta, w.contentMD, w.cond); err != nil {
		return storage.Doc{}, false, err
	}
	w.now = time.Now().Unix()
	collection, key, lang := w.collection, w.key, w.lang
	var res docWriteResult
	var bo binlog.BinlogOps
	err := s.DBUpdateC(collection, bo.InTx(s.Binlog, func(tx *bolt.Tx) error {
		var err error
		res, err = s.putDocTx(tx, &bo, w)
		return err
	}))
	err = bo.Settle(s.Binlog, err)
	if err != nil {
		return storage.Doc{}, false, err
	}
	s.refreshDocReadCache(collection, key, lang, res.buf)
	// The after-write pipeline every write path shares (GO-001, GO-048).
	s.firePostBatchHooks(collection, []*ProcessedDoc{{DocID: res.doc.ID, Doc: res.doc, IsUpdate: !res.isNew}}, opts)
	return res.doc, res.isNew, nil
}

// validateDocWrite refuses a write before any transaction is opened.
//
// GO-003: validation sits in the single write path so EVERY transport is
// covered. Previously only gRPC Add and HTTP handleAdd validated; MCP
// (DirectClient) and GraphQL went straight to addDocument with no checks, and
// the batch path skipped schema validation entirely. Schema validation is
// opt-in (no-op unless a schema is registered for the collection), so this is
// safe for internal callers (memory/upload/import) too.
func (s *Server) validateDocWrite(collection, key, lang string, meta map[string][]string, contentMD string, cond writeCondition) error {
	if collection == "" || key == "" || lang == "" {
		return errors.New("missing required field: collection, key and lang are required")
	}
	if err := cond.validate(); err != nil {
		return err
	}
	if s.SchemaManager != nil {
		if err := s.SchemaManager.Validate(collection, meta); err != nil {
			return err
		}
	}
	// GO-036: refuse text protobuf cannot store, here for the same reason as
	// the schema check above — every transport goes through this function, and
	// a check in a handler is a check the other five handlers do not have.
	//
	// Refusing rather than sanitising: a person picked this one file, and
	// silently dropping the bytes that would not decode turns `café` into
	// `caf` and tells nobody. Bulk paths sanitise and count instead; see
	// text_encoding.go.
	return ValidateDocumentText(contentMD, meta)
}

// putDocTx writes one document in tx: the condition is checked against the
// stored document inside the transaction, so no other write can land between
// the check and this one.
func (s *Server) putDocTx(tx *bolt.Tx, bo *binlog.BinlogOps, w docWrite) (docWriteResult, error) {
	docID := genID(w.collection, w.key, w.lang)
	existing := storage.Doc{}
	if v := tx.Bucket([]byte("docs")).Get(storage.DocKey(w.collection, docID)); v != nil {
		existingPtr, err := loadDoc(v)
		if err != nil {
			return docWriteResult{}, err
		}
		existing = *existingPtr
	}
	if err := w.cond.check(&existing, w.now); err != nil {
		return docWriteResult{}, err
	}
	res := docWriteResult{doc: s.newDocVersion(w, docID, existing.AddedAt), isNew: existing.AddedAt == 0}
	buf, err := marshalAndEncrypt(&res.doc, w.collection)
	if err != nil {
		return docWriteResult{}, err
	}
	res.buf = buf
	return res, s.storeDocTx(tx, bo, w, &res.doc, buf, existing.Meta)
}

// newDocVersion is the document a write stores; addedAt is the stored
// version's, 0 for a new document.
func (s *Server) newDocVersion(w docWrite, docID string, addedAt int64) storage.Doc {
	if addedAt == 0 {
		addedAt = w.now
	}
	doc := storage.Doc{
		ID: docID, Key: w.key, Lang: w.lang, Meta: w.meta,
		ContentMD: w.contentMD, AddedAt: addedAt, UpdatedAt: w.now,
	}
	// CODE-004: source symbols go into meta before the document is
	// marshalled, so the existing metadata filter can answer "which file
	// declares this" without a new query surface.
	EnrichCodeSymbolsWith(&doc, s.codeIncludePaths(w.collection))
	if w.ttl > 0 {
		doc.ExpiresAt = w.now + w.ttl
	}
	return doc
}

// storeDocTx stores a marshaled document, its key and metadata index and,
// when asked, a revision, in tx; oldMeta is the stored version's meta.
func (s *Server) storeDocTx(tx *bolt.Tx, bo *binlog.BinlogOps, w docWrite, doc *storage.Doc, buf []byte, oldMeta map[string][]string) error {
	docKey := storage.DocKey(w.collection, doc.ID)
	// GO-043: quotas and the disk reserve, before anything is written —
	// an external backend included.
	if err := s.checkWriteQuota(tx, w.collection, oneQuotaWrite(docKey, buf)); err != nil {
		return err
	}
	// GO-021: an external backend receives the payload before this
	// transaction commits, so a crash in between leaves an unreferenced
	// object rather than an index entry pointing at nothing. The bucket
	// write below still happens for every collection — it is the storage
	// for BoltDB-backed ones and the local index for the rest.
	if err := s.putDocPayload(w.collection, doc.ID, buf); err != nil {
		return err
	}
	// The document and its key index go in together; a key too long for
	// bbolt fails the first write and the transaction rolls back.
	if err := putLogged(bo,
		boltWrite{tx.Bucket([]byte("docs")), "docs", docKey, buf},
		boltWrite{tx.Bucket([]byte("bykey")), "bykey", storage.ByKeyKey(w.collection, w.key, w.lang), []byte(doc.ID)}); err != nil {
		return err
	}
	if metadataChanged(oldMeta, doc.Meta) {
		if err := reindexMeta(tx.Bucket([]byte("idxmeta")), bo, w.collection, doc.ID, oldMeta, doc.Meta); err != nil {
			return err
		}
	}
	// The revision write and MaxRevisions trimming are shared with
	// updateDocument, so history is capped the same way on both paths.
	if w.saveRevision {
		return s.writeRevision(tx, bo, w.collection, doc.ID, w.now, buf)
	}
	return nil
}

// refreshDocReadCache stores a written document in the read cache, so a
// subsequent gRPC Get (the only path that consults it) sees the new value
// instead of a stale entry for up to the 5-minute TTL (GO-002). Keyed
// identically to gRPC Add/Get via cache.BuildCacheKey, so every transport
// stays coherent.
func (s *Server) refreshDocReadCache(collection, key, lang string, buf []byte) {
	cacheKey := cache.BuildCacheKey(collection, key, lang)
	if s.UseExtreme && s.LockFreeCache != nil {
		s.LockFreeCache.Set(cacheKey, buf)
	} else if s.Cache != nil {
		s.Cache.Set(cacheKey, buf)
	}
}

// invalidateDocReadCaches evicts every read cache that can serve a stale copy
// of one document: the gRPC Get document caches (regular and lock-free) and
// the collection's search-result cache. GO-002 wired this into delete; GO-038
// makes it the single helper every mutating path calls.
func (s *Server) invalidateDocReadCaches(collection, key, lang string) {
	cacheKey := cache.BuildCacheKey(collection, key, lang)
	if s.Cache != nil {
		s.Cache.Delete(cacheKey)
	}
	if s.LockFreeCache != nil {
		s.LockFreeCache.Delete(cacheKey)
	}
	s.invalidateSearchCache(collection)
}
