package main

import (
	"bytes"
	"log/slog"

	"mddb/internal/binlog"
	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// Deleting a collection (#331).
//
// There were three copies of this — HTTP, gRPC and MCP (GraphQL goes through
// MCP) — and each removed a different subset of what a collection owns. None
// removed its vectors: a collection deleted and written again reported 15,540
// embedded documents against 690 that existed, so a job waiting for the
// embedded count finished at once with a false success. None removed its
// full-text index, its TTL entries or, for an S3 or memory collection, the
// document bodies. MCP wrote nothing to the binlog, so followers never saw
// the delete, and kept the collection's config; gRPC left its geo points.
// Documents and revisions were deleted under the cursor iterating them,
// which bbolt does not support.
//
// Everything a single-document delete cleans up, a collection delete now
// cleans up, in one place.

// deletedDocRef is what the clean-up after a collection delete needs of each
// document.
type deletedDocRef struct {
	id, key, lang string
}

// deleteCollection removes a collection and everything derived from it, and
// returns how many documents it held.
func (s *Server) deleteCollection(collection string) (int, error) {
	var docs []deletedDocRef
	var bo binlog.BinlogOps

	err := s.DBUpdate(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(s.BucketNames.Docs)
		bIdx := tx.Bucket(s.BucketNames.IdxMeta)
		bRev := tx.Bucket(s.BucketNames.Rev)
		bByK := tx.Bucket(s.BucketNames.ByKey)

		// Collected first: bbolt does not support deleting under the cursor
		// that is iterating, and doing so skips keys.
		type found struct {
			k   []byte
			doc *storage.Doc
		}
		var all []found
		prefix := []byte("doc|" + collection + "|")
		c := bDocs.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			doc, err := loadDoc(v)
			if err != nil {
				continue
			}
			all = append(all, found{append([]byte(nil), k...), doc})
		}

		for _, f := range all {
			if err := bDocs.Delete(f.k); err != nil {
				return err
			}
			bo.Delete("docs", f.k)

			var revs [][]byte
			rp := storage.RevPrefix(collection, f.doc.ID)
			rc := bRev.Cursor()
			for rk, _ := rc.Seek(rp); rk != nil && bytes.HasPrefix(rk, rp); rk, _ = rc.Next() {
				revs = append(revs, append([]byte(nil), rk...))
			}
			for _, rk := range revs {
				if err := bRev.Delete(rk); err != nil {
					return err
				}
				bo.Delete("rev", rk)
			}

			for mk, vals := range f.doc.Meta {
				for _, mv := range vals {
					mkey := append(storage.MetaKeyPrefix(collection, mk, mv), []byte(f.doc.ID)...)
					if err := bIdx.Delete(mkey); err != nil {
						return err
					}
					bo.Delete("idxmeta", mkey)
				}
			}
			docs = append(docs, deletedDocRef{f.doc.ID, f.doc.Key, f.doc.Lang})
		}

		// DOC-016: every key-index entry for this collection, cleared by
		// prefix, so a document stored under two spellings of its key leaves
		// neither behind.
		return deleteCollectionByKeyEntries(bByK, collection, &bo)
	})
	if err != nil {
		return 0, err
	}
	bo.FlushTo(s.Binlog)

	s.cleanUpDeletedCollection(collection, docs)
	return len(docs), nil
}

// cleanUpDeletedCollection removes what lives outside the documents bucket.
// Each step is best effort: the documents are already gone, and a failure is
// logged rather than reported as a failed delete.
func (s *Server) cleanUpDeletedCollection(collection string, docs []deletedDocRef) {
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.id)
		if err := s.deleteDocPayload(collection, d.id); err != nil {
			slog.Warn("deleting a document body from the collection's backend failed",
				"collection", collection, "docID", d.id, "err", err)
		}
		if s.TTLManager != nil {
			_ = s.TTLManager.Remove(collection, d.id)
		}
		s.invalidateDocReadCaches(collection, d.key, d.lang)
	}

	if s.FTSIndex != nil {
		if err := s.FTSIndex.RemoveDocs(collection, ids); err != nil {
			slog.Warn("removing a deleted collection from the full-text index failed",
				"collection", collection, "err", err)
		}
	}
	s.dropCollectionVectors(collection)

	if s.GeoIndex != nil {
		s.GeoIndex.Clear(collection)
	}
	if s.GeoHashIndex != nil {
		s.GeoHashIndex.Clear(collection)
	}
	if s.GeoStore != nil {
		_ = s.GeoStore.DeleteCollection(collection)
	}
	if s.CollectionManager != nil {
		_ = s.CollectionManager.Delete(collection)
	}
}

// dropCollectionVectors removes a collection's vectors from storage and from
// every in-memory index (#331).
func (s *Server) dropCollectionVectors(collection string) {
	if s.VectorStore != nil {
		if _, err := s.VectorStore.DeleteCollection(collection); err != nil {
			slog.Warn("removing a deleted collection's vectors failed", "collection", collection, "err", err)
		}
	}
	for _, searcher := range s.VectorSearchers {
		searcher.DropCollection(collection)
	}
	if s.VectorIndex != nil {
		s.VectorIndex.DropCollection(collection)
	}
}
