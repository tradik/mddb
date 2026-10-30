package main

import (
	"bytes"
	"errors"
	"mddb/internal/binlog"
	"mddb/internal/cache"
	"mddb/internal/storage"
	"strconv"
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
	return s.addDocumentIf(collection, key, lang, meta, contentMD, ttl, saveRevision, "")
}

// addDocumentIf is addDocument made conditional (INT-021): with
// expectedRevision set, the write succeeds only if the document is at that
// revision — and fails if it does not exist yet. "" is unconditional.
func (s *Server) addDocumentIf(collection, key, lang string, meta map[string][]string, contentMD string, ttl int64, saveRevision bool, expectedRevision string) (storage.Doc, bool, error) {
	// GO-003: validate in the single write path so EVERY transport is covered.
	// Previously only gRPC Add and HTTP handleAdd validated; MCP (DirectClient)
	// and GraphQL went straight to addDocument with no checks, and the batch
	// path skipped schema validation entirely. Schema validation is opt-in
	// (no-op unless a schema is registered for the collection), so this is safe
	// for internal callers (memory/upload/import) too.
	if collection == "" || key == "" || lang == "" {
		return storage.Doc{}, false, errors.New("missing required field: collection, key and lang are required")
	}
	if s.SchemaManager != nil {
		if err := s.SchemaManager.Validate(collection, meta); err != nil {
			return storage.Doc{}, false, err
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
	if err := ValidateDocumentText(contentMD, meta); err != nil {
		return storage.Doc{}, false, err
	}

	now := time.Now().Unix()
	docID := genID(collection, key, lang)

	var saved storage.Doc
	var isNew bool
	var bo binlog.BinlogOps
	var cachedBuf []byte // marshaled doc, reused to refresh the read cache (GO-002)
	err := s.DBUpdateC(collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		bByK := tx.Bucket([]byte("bykey"))

		existing := storage.Doc{}
		if v := bDocs.Get(storage.DocKey(collection, docID)); v != nil {
			existingPtr, err := loadDoc(v)
			if err != nil {
				return err
			}
			existing = *existingPtr
		}
		if err := checkRevision(&existing, expectedRevision); err != nil {
			return err
		}
		added := existing.AddedAt
		if added == 0 {
			added = now
			isNew = true
		}

		doc := storage.Doc{
			ID: docID, Key: key, Lang: lang, Meta: meta,
			ContentMD: contentMD, AddedAt: added, UpdatedAt: now,
		}
		// CODE-004: source symbols go into meta before the document is
		// marshalled, so the existing metadata filter can answer "which file
		// declares this" without a new query surface.
		EnrichCodeSymbolsWith(&doc, s.codeIncludePaths(collection))
		if ttl > 0 {
			doc.ExpiresAt = now + ttl
		}

		buf, err := marshalAndEncrypt(&doc, collection)
		if err != nil {
			return err
		}
		cachedBuf = buf
		docKey := storage.DocKey(collection, docID)

		// GO-043: quotas and the disk reserve, before anything is written —
		// an external backend included.
		if err := s.checkWriteQuota(tx, collection, oneQuotaWrite(docKey, buf)); err != nil {
			return err
		}

		// GO-021: an external backend receives the payload before this
		// transaction commits, so a crash in between leaves an unreferenced
		// object rather than an index entry pointing at nothing. The bucket
		// write below still happens for every collection — it is the storage
		// for BoltDB-backed ones and the local index for the rest.
		if err := s.putDocPayload(collection, docID, buf); err != nil {
			return err
		}

		// The document and its key index go in together; a key too long for
		// bbolt fails the first write and the transaction rolls back.
		if err := putLogged(&bo,
			boltWrite{bDocs, "docs", docKey, buf},
			boltWrite{bByK, "bykey", storage.ByKeyKey(collection, key, lang), []byte(docID)}); err != nil {
			return err
		}

		if metadataChanged(existing.Meta, doc.Meta) {
			if err := reindexMeta(bIdx, &bo, collection, doc.ID, existing.Meta, doc.Meta); err != nil {
				return err
			}
		}

		// The revision write and MaxRevisions trimming are shared with
		// updateDocument, so history is capped the same way on both paths.
		if saveRevision {
			if err := s.writeRevision(tx, &bo, collection, doc.ID, now, buf); err != nil {
				return err
			}
		}

		saved = doc
		return nil
	})
	if err == nil {
		bo.FlushTo(s.Binlog)
	}
	if err != nil {
		return storage.Doc{}, false, err
	}

	// Refresh the read cache so a subsequent gRPC Get (the only path that
	// consults it) sees the new value instead of a stale entry for up to the
	// 5-minute TTL (GO-002). Keyed identically to gRPC Add/Get via
	// cache.BuildCacheKey, so every transport stays coherent.
	if cachedBuf != nil {
		cacheKey := cache.BuildCacheKey(collection, key, lang)
		if s.UseExtreme && s.LockFreeCache != nil {
			s.LockFreeCache.Set(cacheKey, cachedBuf)
		} else if s.Cache != nil {
			s.Cache.Set(cacheKey, cachedBuf)
		}
	}

	// The after-write pipeline every write path shares (GO-001, GO-048).
	s.firePostBatchHooks(collection, []*ProcessedDoc{{DocID: saved.ID, Doc: saved, IsUpdate: !isNew}}, postBatchOptions{})

	return saved, isNew, nil
}

// deleteDocumentInternal deletes a document and all its associated data.
func (s *Server) deleteDocumentInternal(collection, key, lang string) error {
	docID := genID(collection, key, lang)

	var bo binlog.BinlogOps
	var deletedDoc storage.Doc // captured for trigger evaluation
	err := s.DBUpdateC(collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		bRev := tx.Bucket([]byte("rev"))
		bByK := tx.Bucket([]byte("bykey"))

		v := bDocs.Get(storage.DocKey(collection, docID))
		if v == nil {
			return errDocNotFound
		}
		docPtr, err := loadDoc(v)
		if err != nil {
			return err
		}
		doc := *docPtr
		deletedDoc = doc

		docKey := storage.DocKey(collection, docID)
		// GO-021: the payload leaves the backend as well, or a delete
		// removes the index and leaves the data the caller believes is gone.
		if err := s.deleteDocPayload(collection, docID); err != nil {
			return err
		}

		if err := bDocs.Delete(docKey); err != nil {
			return err
		}
		bo.Delete("docs", docKey)

		byKeyK := storage.ByKeyKey(collection, key, lang)
		if err := bByK.Delete(byKeyK); err != nil {
			return err
		}
		bo.Delete("bykey", byKeyK)

		c := bRev.Cursor()
		rp := storage.RevPrefix(collection, docID)
		for k, _ := c.Seek(rp); k != nil && bytes.HasPrefix(k, rp); k, _ = c.Next() {
			if err := bRev.Delete(k); err != nil {
				return err
			}
			bo.Delete("rev", k)
		}

		for mk, vals := range doc.Meta {
			for _, mv := range vals {
				mkey := append(storage.MetaKeyPrefix(collection, mk, mv), []byte(docID)...)
				if err := bIdx.Delete(mkey); err != nil {
					return err
				}
				bo.Delete("idxmeta", mkey)
			}
		}

		return nil
	})
	if err == nil {
		bo.FlushTo(s.Binlog)
	}
	if err != nil {
		return err
	}

	// Clean up vector embedding (legacy key + all chunk keys), and the
	// summary vector (RAG-009).
	s.dropDocumentSummary(collection, docID)
	s.dropDocumentImage(collection, docID) // RAG-010: its bytes and image vector
	if s.VectorStore != nil {
		_ = s.VectorStore.Delete(collection, docID)
		for _, searcher := range s.VectorSearchers {
			searcher.Remove(collection, docID)
			// Also remove chunk keys
			for i := 0; i < 100; i++ {
				chunkKey := docID + "#" + strconv.Itoa(i)
				if searcher.CollectionSize(collection) == 0 {
					break
				}
				searcher.Remove(collection, chunkKey)
			}
		}
	}

	// Clean up TTL entry
	if s.TTLManager != nil {
		_ = s.TTLManager.Remove(collection, docID)
	}

	// Automation triggers (before FTS cleanup so doc is still searchable)
	if s.AutomationManager != nil && env("MDDB_TRIGGERS", "false") == "true" {
		go s.AutomationManager.EvaluateTriggers(collection, deletedDoc, "delete")
	}

	// Invalidate the read cache so a gRPC Get can't serve the just-deleted doc
	// for up to the 5-minute TTL (GO-002). Same cache.BuildCacheKey as the write path.
	s.invalidateDocReadCaches(collection, key, lang)

	// Clean up FTS index
	if s.FTSIndex != nil {
		_ = s.FTSIndex.Remove(collection, docID)
	}

	// Clean up both geo indexes (no-op if this doc had no point).
	if s.GeoIndex != nil {
		s.GeoIndex.Remove(collection, docID)
	}
	if s.GeoHashIndex != nil {
		s.GeoHashIndex.Remove(collection, docID)
	}
	if s.GeoStore != nil {
		_ = s.GeoStore.Delete(collection, docID)
	}

	// Fire webhook + SSE
	if s.WebhookManager != nil {
		s.WebhookManager.Fire("doc.deleted", collection, key, lang, nil)
	}
	if s.SSEHub != nil {
		s.SSEHub.BroadcastWithAuth("doc.deleted", collection, key, lang, s.AuthManager)
	}

	return nil
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
