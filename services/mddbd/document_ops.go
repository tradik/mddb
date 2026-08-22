package main

import (
	"bytes"
	"errors"
	"fmt"
	"mddb/internal/binlog"
	"mddb/internal/cache"
	"mddb/internal/storage"
	"mddb/internal/temporal"
	"strconv"
	"strings"
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

	now := time.Now().Unix()
	docID := genID(collection, key, lang)

	var saved storage.Doc
	var isNew bool
	var bo binlog.BinlogOps
	var cachedBuf []byte // marshaled doc, reused to refresh the read cache (GO-002)
	err := s.DBUpdate(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		bRev := tx.Bucket([]byte("rev"))
		bByK := tx.Bucket([]byte("bykey"))

		existing := storage.Doc{}
		if v := bDocs.Get(storage.DocKey(collection, docID)); v != nil {
			existingPtr, err := loadDoc(v)
			if err != nil {
				return err
			}
			existing = *existingPtr
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
		if ttl > 0 {
			doc.ExpiresAt = now + ttl
		}

		buf, err := marshalAndEncrypt(&doc, collection)
		if err != nil {
			return err
		}
		cachedBuf = buf
		docKey := storage.DocKey(collection, docID)
		if err := bDocs.Put(docKey, buf); err != nil {
			return err
		}
		bo.Put("docs", docKey, buf)

		byKeyK := storage.ByKeyKey(collection, key, lang)
		if err := bByK.Put(byKeyK, []byte(docID)); err != nil {
			return err
		}
		bo.Put("bykey", byKeyK, []byte(docID))

		if metadataChanged(existing.Meta, doc.Meta) {
			if existing.ID != "" && existing.Meta != nil {
				for mk, vals := range existing.Meta {
					for _, mv := range vals {
						prefix := append(storage.MetaKeyPrefix(collection, mk, mv), []byte(existing.ID)...)
						_ = bIdx.Delete(prefix)
						bo.Delete("idxmeta", prefix)
					}
				}
			}
			for mk, vals := range doc.Meta {
				for _, mv := range vals {
					mkey := append(storage.MetaKeyPrefix(collection, mk, mv), []byte(doc.ID)...)
					if err := bIdx.Put(mkey, []byte("1")); err != nil {
						return err
					}
					bo.Put("idxmeta", mkey, []byte("1"))
				}
			}
		}

		if saveRevision {
			rkey := append(storage.RevPrefix(collection, doc.ID), []byte(fmt.Sprintf("%020d", now))...)
			if err := bRev.Put(rkey, buf); err != nil {
				return err
			}
			bo.Put("rev", rkey, buf)

			// Enforce per-collection MaxRevisions: drop oldest revs over the cap so
			// history never grows unbounded on high-churn collections.
			if s.CollectionManager != nil {
				if cfg, found := s.CollectionManager.Get(collection); found && cfg.MaxRevisions > 0 {
					if err := trimRevisions(tx, &bo, collection, doc.ID, cfg.MaxRevisions); err != nil {
						return err
					}
				}
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

	// Side-effect pipeline shared by every write transport (GO-001).
	s.runPostWriteHooks(collection, saved, isNew)

	return saved, isNew, nil
}

// runPostWriteHooks runs the side-effect pipeline that must fire after a
// document write commits to BoltDB, regardless of the transport that produced
// it — HTTP, gRPC (single Add or AddBatch), MCP, or GraphQL. Centralising it
// here is what guarantees identical behaviour across transports (GO-001):
// async embedding, TTL bucket registration, FTS (content + positional +
// field/BM25F), geo (R-tree + geohash + GeoStore), temporal tracking,
// webhooks, SSE broadcast, and automation triggers. Every dependency is
// nil-guarded so partially-configured servers (and tests) are safe.
//
// Revision writing and MaxRevisions trimming are intentionally NOT here: they
// must happen inside the write transaction (see addDocument / the batch
// commits) so a crash can never leave a doc without its revision.
func (s *Server) runPostWriteHooks(collection string, saved storage.Doc, isNew bool) {
	// A write changes what a search returns, so cached result sets for this
	// collection must stop being reachable (GO-031). This is the shared
	// pipeline for every write transport, so one call covers them all.
	s.invalidateSearchCache(collection)

	// Trigger async embedding
	if s.EmbeddingWorker != nil && saved.ContentMD != "" {
		s.EmbeddingWorker.Enqueue(EmbeddingJob{
			Collection: collection,
			DocID:      saved.ID,
			ContentMD:  saved.ContentMD,
			ChunkMode:  ChunkModeFor(&saved),
		})
	}

	// TTL bucket entry
	if s.TTLManager != nil && saved.ExpiresAt > 0 {
		_ = s.TTLManager.Set(collection, saved.ID, saved.ExpiresAt)
	}

	// FTS indexing (language-aware)
	if s.FTSIndex != nil && saved.ContentMD != "" {
		_ = s.FTSIndex.IndexWithLang(collection, saved.ID, saved.ContentMD, saved.Lang)
		// Positional index for phrase/proximity search
		_ = s.FTSIndex.IndexPositionsWithLang(collection, saved.ID, saved.ContentMD, saved.Lang)
		// Field-level indexing for BM25F
		fields := map[string]string{"content": saved.ContentMD}
		for k, vals := range saved.Meta {
			if len(vals) > 0 {
				fields["meta."+k] = strings.Join(vals, " ")
			}
		}
		_ = s.FTSIndex.IndexFieldsWithLang(collection, saved.ID, fields, saved.Lang)
	}

	// Geo indexing: persist resolved lat/lng to BoltDB and mirror into
	// BOTH in-memory indexes (R-tree + geohash). Coordinates are extracted
	// from meta via AddFromMeta, which tries explicit geo_lat/geo_lng,
	// then geo_hash, then the optional postcode lookup — silent no-op if
	// the doc has none of those.
	if s.GeoIndex != nil && s.GeoStore != nil {
		if lat, lng, ok := s.GeoIndex.AddFromMeta(collection, saved.ID, saved.Meta); ok {
			_ = s.GeoStore.Put(collection, saved.ID, lat, lng)
			if s.GeoHashIndex != nil {
				s.GeoHashIndex.Add(collection, saved.ID, lat, lng)
			}
		} else if !isNew {
			// Update may have dropped the geo fields — remove any stale entry.
			s.GeoIndex.Remove(collection, saved.ID)
			if s.GeoHashIndex != nil {
				s.GeoHashIndex.Remove(collection, saved.ID)
			}
			_ = s.GeoStore.Delete(collection, saved.ID)
		}
	}

	// Temporal tracking
	if s.TemporalManager != nil {
		et := temporal.EventUpdate
		if isNew {
			et = temporal.EventCreate
		}
		s.TemporalManager.RecordAsync(collection, saved.ID, et, "")
	}

	// Webhooks + SSE
	event := "doc.updated"
	if isNew {
		event = "doc.added"
	}
	if s.WebhookManager != nil {
		s.WebhookManager.Fire(event, collection, saved.Key, saved.Lang, &saved)
	}
	if s.SSEHub != nil {
		s.SSEHub.BroadcastWithAuth(event, collection, saved.Key, saved.Lang, s.AuthManager)
	}

	// Automation triggers
	if s.AutomationManager != nil && env("MDDB_TRIGGERS", "false") == "true" {
		triggerEvent := "insert"
		if !isNew {
			triggerEvent = "update"
		}
		go s.AutomationManager.EvaluateTriggers(collection, saved, triggerEvent)
	}
}

// deleteDocumentInternal deletes a document and all its associated data.
func (s *Server) deleteDocumentInternal(collection, key, lang string) error {
	docID := genID(collection, key, lang)

	var bo binlog.BinlogOps
	var deletedDoc storage.Doc // captured for trigger evaluation
	err := s.DBUpdate(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		bRev := tx.Bucket([]byte("rev"))
		bByK := tx.Bucket([]byte("bykey"))

		v := bDocs.Get(storage.DocKey(collection, docID))
		if v == nil {
			return errors.New("document not found")
		}
		docPtr, err := loadDoc(v)
		if err != nil {
			return err
		}
		doc := *docPtr
		deletedDoc = doc

		docKey := storage.DocKey(collection, docID)
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

	// Clean up vector embedding (legacy key + all chunk keys)
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
	cacheKey := cache.BuildCacheKey(collection, key, lang)
	if s.Cache != nil {
		s.Cache.Delete(cacheKey)
	}
	s.invalidateSearchCache(collection)
	if s.LockFreeCache != nil {
		s.LockFreeCache.Delete(cacheKey)
	}

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
