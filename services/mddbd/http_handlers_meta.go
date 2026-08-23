package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mddb/internal/binlog"
	"mddb/internal/storage"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Parse raw JSON to detect which fields are present
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		bad(w, err)
		return
	}

	// Required fields
	var collection, key, lang string
	if v, ok := raw["collection"]; ok {
		_ = json.Unmarshal(v, &collection)
	}
	if v, ok := raw["key"]; ok {
		_ = json.Unmarshal(v, &key)
	}
	if v, ok := raw["lang"]; ok {
		_ = json.Unmarshal(v, &lang)
	}

	if collection == "" || key == "" || lang == "" {
		bad(w, errors.New("missing required fields: collection, key, lang"))
		return
	}

	// Check which optional fields are present
	_, hasMeta := raw["meta"]
	_, hasContent := raw["contentMd"]
	_, hasTTL := raw["ttl"]

	if !hasMeta && !hasContent && !hasTTL {
		bad(w, errors.New("no fields to update"))
		return
	}

	// Parse optional fields
	var newMeta map[string][]string
	if hasMeta {
		_ = json.Unmarshal(raw["meta"], &newMeta)
	}
	var newContent string
	if hasContent {
		_ = json.Unmarshal(raw["contentMd"], &newContent)
	}
	var newTTL int64
	if hasTTL {
		_ = json.Unmarshal(raw["ttl"], &newTTL)
	}

	// Auth check
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), collection, PermWrite); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	// Schema validation for meta update
	if hasMeta {
		if err := s.SchemaManager.Validate(collection, newMeta); err != nil {
			bad(w, err)
			return
		}
	}

	// Load existing doc, apply partial changes, save
	now := time.Now().Unix()
	var saved storage.Doc
	var bo binlog.BinlogOps
	var metaDidChange bool

	err := s.DBUpdate(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		bRev := tx.Bucket([]byte("rev"))
		bByK := tx.Bucket([]byte("bykey"))

		// Find existing doc
		docIDBytes := bByK.Get(storage.ByKeyKey(collection, key, lang))
		if docIDBytes == nil {
			return errors.New("not found")
		}

		v := bDocs.Get(storage.DocKey(collection, string(docIDBytes)))
		if v == nil {
			return errors.New("not found")
		}

		existing, err := loadDoc(v)
		if err != nil {
			return err
		}

		// Check TTL expiry
		if existing.ExpiresAt > 0 && existing.ExpiresAt < now {
			return errors.New("not found")
		}

		// Apply partial updates
		doc := *existing
		doc.UpdatedAt = now

		if hasMeta {
			metaDidChange = metadataChanged(doc.Meta, newMeta)
			doc.Meta = newMeta
		}
		if hasContent {
			doc.ContentMD = newContent
		}
		if hasTTL {
			if newTTL > 0 {
				doc.ExpiresAt = now + newTTL
			} else {
				doc.ExpiresAt = 0
			}
		}

		// Persist
		buf, err := marshalAndEncrypt(&doc, collection)
		if err != nil {
			return err
		}

		docKey := storage.DocKey(collection, doc.ID)
		if err := bDocs.Put(docKey, buf); err != nil {
			return err
		}
		bo.Put("docs", docKey, buf)

		// Reindex metadata if changed
		if metaDidChange {
			// Remove old meta index entries
			if existing.Meta != nil {
				for mk, vals := range existing.Meta {
					for _, mv := range vals {
						mkey := append(storage.MetaKeyPrefix(collection, mk, mv), []byte(doc.ID)...)
						_ = bIdx.Delete(mkey)
						bo.Delete("idxmeta", mkey)
					}
				}
			}
			// Add new meta index entries
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

		// Save revision
		rkey := append(storage.RevPrefix(collection, doc.ID), []byte(fmt.Sprintf("%020d", now))...)
		if err := bRev.Put(rkey, buf); err != nil {
			return err
		}
		bo.Put("rev", rkey, buf)

		if s.CollectionManager != nil {
			if cfg, found := s.CollectionManager.Get(collection); found && cfg.MaxRevisions > 0 {
				if err := trimRevisions(tx, &bo, collection, doc.ID, cfg.MaxRevisions); err != nil {
					return err
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
		if err.Error() == "not found" {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		bad(w, err)
		return
	}

	// Post-update side effects (cache invalidation, embedding, TTL, FTS, geo,
	// webhooks, triggers, metrics) — shared with gRPC UpdateDocument (GO-038).
	s.runPostUpdateHooks(collection, key, lang, saved, hasContent, hasTTL)

	ok(w, saved)
}

func (s *Server) handleDocMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	collection := r.URL.Query().Get("collection")
	key := r.URL.Query().Get("key")
	lang := r.URL.Query().Get("lang")
	if lang == "" {
		lang = "en"
	}

	if collection == "" || key == "" {
		http.Error(w, `{"error":"collection and key are required"}`, http.StatusBadRequest)
		return
	}

	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	var doc storage.Doc
	err := s.DBView(func(tx *bolt.Tx) error {
		bByK := tx.Bucket([]byte("bykey"))
		bDocs := tx.Bucket([]byte("docs"))
		docID := bByK.Get(storage.ByKeyKey(collection, key, lang))
		if docID == nil {
			return errors.New("not found")
		}
		v := bDocs.Get(storage.DocKey(collection, string(docID)))
		if v == nil {
			return errors.New("not found")
		}
		d, err := loadDoc(v)
		if err != nil {
			return err
		}
		doc = *d
		return nil
	})
	if err != nil {
		if err.Error() == "not found" {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		bad(w, err)
		return
	}

	if doc.ExpiresAt > 0 && doc.ExpiresAt < time.Now().Unix() {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	// Return metadata only (no contentMd)
	resp := map[string]interface{}{
		"key":       doc.Key,
		"lang":      doc.Lang,
		"meta":      doc.Meta,
		"addedAt":   doc.AddedAt,
		"updatedAt": doc.UpdatedAt,
	}
	if doc.ExpiresAt > 0 {
		resp["expiresAt"] = doc.ExpiresAt
	}

	if s.Metrics != nil {
		s.Metrics.IncOp("doc_meta")
	}

	ok(w, resp)
}

func (s *Server) handleMetaKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	collection := r.URL.Query().Get("collection")
	if collection == "" {
		http.Error(w, `{"error":"collection is required"}`, http.StatusBadRequest)
		return
	}

	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	meta := make(map[string][]string)

	_ = s.DBView(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))
		if bIdx == nil {
			return nil
		}

		prefix := []byte("meta|" + collection + "|")
		c := bIdx.Cursor()
		seen := make(map[string]map[string]bool)

		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			rest := string(k[len(prefix):])
			parts := strings.SplitN(rest, "|", 3)
			if len(parts) < 2 {
				continue
			}
			mk, mv := parts[0], parts[1]
			if seen[mk] == nil {
				seen[mk] = make(map[string]bool)
			}
			if !seen[mk][mv] {
				seen[mk][mv] = true
				meta[mk] = append(meta[mk], mv)
			}
		}
		return nil
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"meta": meta})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	type CollectionStats struct {
		Name           string `json:"name"`
		DocumentCount  int    `json:"documentCount"`
		RevisionCount  int    `json:"revisionCount"`
		MetaIndexCount int    `json:"metaIndexCount"`
		Checksum       string `json:"checksum"`
		Type           string `json:"type,omitempty"`
		Description    string `json:"description,omitempty"`
		Icon           string `json:"icon,omitempty"`
		Color          string `json:"color,omitempty"`
	}

	// Check read permission (database-wide stats)
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	// IndexQueueStats surfaces async meta-indexing health (GO-010): how many
	// jobs were processed, failed, or had to be indexed synchronously because
	// the queue was full (fallbacks), plus the current queue depth.
	type IndexQueueStats struct {
		Processed uint64 `json:"processed"`
		Failed    uint64 `json:"failed"`
		Fallbacks uint64 `json:"fallbacks"`
		QueueLen  int    `json:"queueLen"`
	}

	type Stats struct {
		DatabasePath     string            `json:"databasePath"`
		DatabaseSize     int64             `json:"databaseSize"`
		Mode             string            `json:"mode"`
		Collections      []CollectionStats `json:"collections"`
		TotalDocuments   int               `json:"totalDocuments"`
		TotalRevisions   int               `json:"totalRevisions"`
		TotalMetaIndices int               `json:"totalMetaIndices"`
		IndexQueue       *IndexQueueStats  `json:"indexQueue,omitempty"`
		Uptime           string            `json:"uptime"`
	}

	stats := Stats{
		DatabasePath: s.Path,
		Mode:         string(s.Mode),
		Collections:  []CollectionStats{},
	}

	// Get database file size
	if info, err := os.Stat(s.Path); err == nil {
		stats.DatabaseSize = info.Size()
	}

	// Collect statistics per collection. Tenant users only see (and count)
	// collections inside their namespace.
	tenant := TenantFromContext(r.Context())
	collectionMap := make(map[string]*CollectionStats)

	err := s.DBView(func(tx *bolt.Tx) error {
		// Count documents per collection
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs != nil {
			c := bDocs.Cursor()
			for k, _ := c.First(); k != nil; k, _ = c.Next() {
				// key format: doc|collection|id
				parts := strings.Split(string(k), "|")
				if len(parts) >= 2 && CollectionInTenant(tenant, parts[1]) {
					coll := parts[1]
					if _, ok := collectionMap[coll]; !ok {
						collectionMap[coll] = &CollectionStats{Name: coll}
					}
					collectionMap[coll].DocumentCount++
					stats.TotalDocuments++
				}
			}
		}

		// Count revisions per collection
		bRev := tx.Bucket([]byte("rev"))
		if bRev != nil {
			c := bRev.Cursor()
			for k, _ := c.First(); k != nil; k, _ = c.Next() {
				// key format: rev|collection|docID|ts
				parts := strings.Split(string(k), "|")
				if len(parts) >= 2 && CollectionInTenant(tenant, parts[1]) {
					coll := parts[1]
					if _, ok := collectionMap[coll]; !ok {
						collectionMap[coll] = &CollectionStats{Name: coll}
					}
					collectionMap[coll].RevisionCount++
					stats.TotalRevisions++
				}
			}
		}

		// Count meta indices per collection
		bIdx := tx.Bucket([]byte("idxmeta"))
		if bIdx != nil {
			c := bIdx.Cursor()
			for k, _ := c.First(); k != nil; k, _ = c.Next() {
				// key format: meta|collection|key|value|docID
				parts := strings.Split(string(k), "|")
				if len(parts) >= 2 && CollectionInTenant(tenant, parts[1]) {
					coll := parts[1]
					if _, ok := collectionMap[coll]; !ok {
						collectionMap[coll] = &CollectionStats{Name: coll}
					}
					collectionMap[coll].MetaIndexCount++
					stats.TotalMetaIndices++
				}
			}
		}

		return nil
	})

	if err != nil {
		bad(w, err)
		return
	}

	// Compute checksums per collection
	for name, cs := range collectionMap {
		cs.Checksum, _ = s.collectionChecksum(name)
	}

	// Enrich with collection config attributes
	if s.CollectionManager != nil {
		for name, cs := range collectionMap {
			if cfg, ok := s.CollectionManager.Get(name); ok {
				cs.Type = cfg.Type
				cs.Description = cfg.Description
				cs.Icon = cfg.Icon
				cs.Color = cfg.Color
			}
		}
	}

	// Convert map to slice
	for _, cs := range collectionMap {
		stats.Collections = append(stats.Collections, *cs)
	}

	// Sort collections by name
	sort.Slice(stats.Collections, func(i, j int) bool {
		return stats.Collections[i].Name < stats.Collections[j].Name
	})

	// Async meta-indexing queue health
	if s.IndexQueue != nil {
		processed, failed, fallbacks, queueLen := s.IndexQueue.Stats()
		stats.IndexQueue = &IndexQueueStats{
			Processed: processed,
			Failed:    failed,
			Fallbacks: fallbacks,
			QueueLen:  queueLen,
		}
	}

	ok(w, stats)
}
