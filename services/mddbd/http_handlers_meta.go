package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"mddb/internal/storage"
	"net/http"
	"os"
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
	var expectedRevision string
	if v, ok := raw["expectedRevision"]; ok {
		_ = json.Unmarshal(v, &expectedRevision)
	}

	// Auth check
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), collection, PermWrite); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	patch := DocPatch{Meta: newMeta, HasMeta: hasMeta, ExpectedRevision: expectedRevision}
	if hasContent {
		patch.Content = &newContent
	}
	if hasTTL {
		patch.TTL = &newTTL
	}
	saved, err := s.updateDocument(collection, key, lang, patch)
	if err != nil {
		writeDocWriteError(w, err)
		return
	}
	ok(w, docResponse(saved, nil))
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
	err := s.DBViewC(collection, func(tx *bolt.Tx) error {
		bByK := tx.Bucket([]byte("bykey"))
		bDocs := tx.Bucket([]byte("docs"))
		docID := bByK.Get(storage.ByKeyKey(collection, key, lang))
		if docID == nil {
			return errDocNotFound
		}
		v := bDocs.Get(storage.DocKey(collection, string(docID)))
		if v == nil {
			return errDocNotFound
		}
		d, err := loadDoc(v)
		if err != nil {
			return err
		}
		doc = *d
		return nil
	})
	if err != nil {
		bad(w, err)
		return
	}

	if doc.ExpiresAt > 0 && doc.ExpiresAt < time.Now().Unix() {
		bad(w, errDocNotFound)
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

	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
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
		// #349: the counts come from a snapshot computed in the background.
		StatsComputedAt string `json:"statsComputedAt,omitempty"`
		StatsAgeSeconds int    `json:"statsAgeSeconds"`
		StatsPending    bool   `json:"statsPending,omitempty"` // no snapshot yet; ask again shortly
		// Indexing is queued embedding and metadata work across the server
		// (GO-042). Omitted for tenant users: it counts every collection.
		Indexing *IndexingStatus `json:"indexing,omitempty"`
	}

	stats := Stats{
		DatabasePath: s.Path,
		Mode:         string(s.Mode),
		Collections:  []CollectionStats{},
	}
	if info, err := os.Stat(s.Path); err == nil {
		stats.DatabaseSize = info.Size()
	}

	// Tenant users only see (and count) collections inside their namespace.
	tenant := TenantFromContext(r.Context())
	snap := s.dbStats(statsWait())
	if snap == nil {
		stats.StatsPending = true
	} else {
		stats.StatsComputedAt = snap.ComputedAt.UTC().Format(time.RFC3339)
		stats.StatsAgeSeconds = int(time.Since(snap.ComputedAt).Seconds())
		for _, name := range snap.sortedNames() {
			if !CollectionInTenant(tenant, name) {
				continue
			}
			cc := snap.Collections[name]
			cs := CollectionStats{Name: name, DocumentCount: cc.Documents, RevisionCount: cc.Revisions,
				MetaIndexCount: cc.MetaIndices, Checksum: cc.Checksum}
			if s.CollectionManager != nil {
				if cfg, ok := s.CollectionManager.Get(name); ok {
					cs.Type, cs.Description, cs.Icon, cs.Color = cfg.Type, cfg.Description, cfg.Icon, cfg.Color
				}
			}
			stats.Collections = append(stats.Collections, cs)
			stats.TotalDocuments += cc.Documents
			stats.TotalRevisions += cc.Revisions
			stats.TotalMetaIndices += cc.MetaIndices
		}
	}

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
	if tenant == "" {
		ix := s.IndexingStatus("")
		stats.Indexing = &ix
	}

	ok(w, stats)
}
