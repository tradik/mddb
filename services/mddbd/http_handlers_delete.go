package main

import (
	"bytes"
	"errors"
	"log/slog"
	"mddb/internal/binlog"
	json "mddb/internal/jsonx"
	"mddb/internal/storage"
	"net/http"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// handleDelete deletes a single document from a collection
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	var req DeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		bad(w, errors.New("missing fields"))
		return
	}

	// Check write permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermWrite); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	if err := s.deleteDocumentInternal(req.Collection, req.Key, req.Lang); err != nil {
		bad(w, err)
		return
	}

	ok(w, map[string]interface{}{
		"status":     "deleted",
		"collection": req.Collection,
		"key":        req.Key,
		"lang":       req.Lang,
	})
}

// handleDeleteBatch deletes multiple documents in a single request.
func (s *Server) handleDeleteBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Collection string `json:"collection"`
		Documents  []struct {
			Key  string `json:"key"`
			Lang string `json:"lang"`
		} `json:"documents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	if len(req.Documents) == 0 {
		bad(w, errors.New("missing documents"))
		return
	}

	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermWrite); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	var deleted, notFound, failed int
	var errs []string
	for _, d := range req.Documents {
		if d.Key == "" || d.Lang == "" {
			failed++
			errs = append(errs, "missing key or lang")
			continue
		}
		if err := s.deleteDocumentInternal(req.Collection, d.Key, d.Lang); err != nil {
			if strings.Contains(err.Error(), "not found") {
				notFound++
			} else {
				failed++
				errs = append(errs, err.Error())
			}
			continue
		}
		deleted++
	}

	ok(w, map[string]interface{}{
		"deleted":   deleted,
		"not_found": notFound,
		"failed":    failed,
		"errors":    errs,
	})
}

// handleDeleteCollection deletes all documents in a collection
func (s *Server) handleDeleteCollection(w http.ResponseWriter, r *http.Request) {
	var req DeleteCollectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}

	// Check write permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermWrite); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	var deletedCount int
	var bo binlog.BinlogOps

	err := s.DBUpdate(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		bRev := tx.Bucket([]byte("rev"))
		bByK := tx.Bucket([]byte("bykey"))

		// Delete all documents in collection
		c := bDocs.Cursor()
		prefix := []byte("doc|" + req.Collection + "|")
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			// Load document to get metadata for index cleanup
			docPtr, err := loadDoc(v)
			if err != nil {
				continue
			}
			doc := *docPtr

			// Delete document
			if err := bDocs.Delete(k); err != nil {
				return err
			}
			bo.Delete("docs", k)

			// Delete all revisions
			rc := bRev.Cursor()
			rp := storage.RevPrefix(req.Collection, doc.ID)
			for rk, _ := rc.Seek(rp); rk != nil && bytes.HasPrefix(rk, rp); rk, _ = rc.Next() {
				if err := bRev.Delete(rk); err != nil {
					return err
				}
				bo.Delete("rev", rk)
			}

			// Delete metadata indices
			for mk, vals := range doc.Meta {
				for _, mv := range vals {
					mkey := append(storage.MetaKeyPrefix(req.Collection, mk, mv), []byte(doc.ID)...)
					if err := bIdx.Delete(mkey); err != nil {
						return err
					}
					bo.Delete("idxmeta", mkey)
				}
			}

			deletedCount++
		}

		// DOC-016: every key-index entry for this collection, cleared by
		// prefix. The per-document delete above named one spelling, so a
		// document written as "README.md" and again as "readme.md" left the
		// other entry pointing at a document that no longer exists.
		if err := deleteCollectionByKeyEntries(bByK, req.Collection, &bo); err != nil {
			return err
		}

		return nil
	})
	if err == nil {
		bo.FlushTo(s.Binlog)
	}

	if err != nil {
		bad(w, err)
		return
	}

	// Clean up collection config
	if s.CollectionManager != nil {
		_ = s.CollectionManager.Delete(req.Collection)
	}

	// Clean up both geo indexes and persisted geo points for this collection.
	if s.GeoIndex != nil {
		s.GeoIndex.Clear(req.Collection)
	}
	if s.GeoHashIndex != nil {
		s.GeoHashIndex.Clear(req.Collection)
	}
	if s.GeoStore != nil {
		_ = s.GeoStore.DeleteCollection(req.Collection)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "deleted",
		"collection":   req.Collection,
		"deletedCount": deletedCount,
	}); err != nil {
		slog.Error("encoding delete collection response", "err", err)
	}
}
