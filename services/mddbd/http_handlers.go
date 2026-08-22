package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	json "github.com/goccy/go-json"
	bolt "go.etcd.io/bbolt"
	"hash/crc32"
	"io"
	"log/slog"
	"mddb/internal/binlog"
	"mddb/internal/sliceutil"
	"mddb/internal/storage"
	"mddb/internal/temporal"
	"net/http"
	"sort"
	"time"
)

// --- handlers

func (s *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
	var req AddRequest
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

	if err := s.SchemaManager.Validate(req.Collection, req.Meta); err != nil {
		bad(w, err)
		return
	}

	saved, _, err := s.addDocument(req.Collection, req.Key, req.Lang, req.Meta, req.ContentMD, req.TTL, true)
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, saved)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	var req GetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		bad(w, errors.New("missing fields"))
		return
	}

	// Check read permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	var doc storage.Doc
	err := s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bByK := tx.Bucket([]byte("bykey"))
		docID := bByK.Get(storage.ByKeyKey(req.Collection, req.Key, req.Lang))
		if docID == nil {
			return errors.New("not found")
		}
		v := bDocs.Get(storage.DocKey(req.Collection, string(docID)))
		if v == nil {
			return errors.New("not found")
		}
		docPtr, unmErr := loadDoc(v)
		if unmErr != nil {
			return unmErr
		}
		doc = *docPtr
		return nil
	})
	if err != nil {
		bad(w, err)
		return
	}

	// Check TTL expiry
	if doc.ExpiresAt > 0 && doc.ExpiresAt < time.Now().Unix() {
		bad(w, errors.New("not found"))
		return
	}

	// Temporal access tracking (gated on collection config)
	if s.TemporalManager != nil && s.CollectionManager != nil {
		if cfg, cfgOk := s.CollectionManager.Get(req.Collection); cfgOk && cfg.TrackAccess {
			actor := ""
			if claims, ok := GetClaimsFromContext(r.Context()); ok {
				actor = claims.Username
			}
			s.TemporalManager.RecordAsync(req.Collection, doc.ID, temporal.EventAccess, actor)
		}
	}

	// Templating via ENV: replace %%var%%
	if len(req.Env) > 0 && doc.ContentMD != "" {
		doc.ContentMD = applyEnv(doc.ContentMD, req.Env)
	}
	ok(w, doc)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Limit <= 0 {
		req.Limit = 50
	}

	// Check read permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	type row struct{ Doc storage.Doc }
	var rows []row

	err := s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		seen := make(map[string]bool)

		// Jeśli brak filtra meta → pełny scan kolekcji (dla prostoty; można dodać bucket per collection)
		if len(req.FilterMeta) == 0 {
			c := bDocs.Cursor()
			prefix := []byte("doc|" + req.Collection + "|")
			for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
				d, err := loadDoc(v)
				if err != nil {
					return err
				}
				if d.ExpiresAt > 0 && d.ExpiresAt < time.Now().Unix() {
					continue
				}
				rows = append(rows, row{*d})
			}
		} else {
			// Intersect po meta kluczach
			var sets [][]string
			for mk, mvals := range req.FilterMeta {
				var ids []string
				for _, mv := range mvals {
					prefix := storage.MetaKeyPrefix(req.Collection, mk, mv)
					c := bIdx.Cursor()
					for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
						id := string(k[len(prefix):])
						ids = append(ids, id)
					}
				}
				ids = sliceutil.Unique(ids)
				sets = append(sets, ids)
			}
			ids := intersect(sets...)
			for _, id := range ids {
				if seen[id] {
					continue
				}
				seen[id] = true
				v := tx.Bucket([]byte("docs")).Get(storage.DocKey(req.Collection, id))
				if v == nil {
					continue
				}
				d, err := loadDoc(v)
				if err != nil {
					return err
				}
				if d.ExpiresAt > 0 && d.ExpiresAt < time.Now().Unix() {
					continue
				}
				rows = append(rows, row{*d})
			}
		}
		return nil
	})
	if err != nil {
		bad(w, err)
		return
	}

	// sort
	switch req.Sort {
	case "addedAt":
		sort.Slice(rows, func(i, j int) bool {
			if req.Asc {
				return rows[i].Doc.AddedAt < rows[j].Doc.AddedAt
			}
			return rows[i].Doc.AddedAt > rows[j].Doc.AddedAt
		})
	case "updatedAt":
		sort.Slice(rows, func(i, j int) bool {
			if req.Asc {
				return rows[i].Doc.UpdatedAt < rows[j].Doc.UpdatedAt
			}
			return rows[i].Doc.UpdatedAt > rows[j].Doc.UpdatedAt
		})
	case "key":
		sort.Slice(rows, func(i, j int) bool {
			if req.Asc {
				return rows[i].Doc.Key < rows[j].Doc.Key
			}
			return rows[i].Doc.Key > rows[j].Doc.Key
		})
	}

	// paginate
	start := req.Offset
	if start > len(rows) {
		start = len(rows)
	}
	end := start + req.Limit
	if end > len(rows) {
		end = len(rows)
	}

	out := make([]storage.Doc, 0, end-start)
	for _, r := range rows[start:end] {
		out = append(out, r.Doc)
	}
	w.Header().Set("X-Total-Count", fmt.Sprintf("%d", len(rows)))
	ok(w, out)
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	var req ExportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Format == "" {
		req.Format = "ndjson"
	}

	// Check read permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	// Reużyj /search
	sr := SearchRequest{Collection: req.Collection, FilterMeta: req.FilterMeta, Limit: 1 << 30}
	buf := new(bytes.Buffer)

	switch req.Format {
	case "ndjson":
		// stream NDJSON
		res, _ := http.Post("http://localhost"+env("MDDB_ADDR", ":11023")+"/v1/search", "application/json", bytes.NewReader(mustJSON(sr)))
		defer func() { _ = res.Body.Close() }()
		var docs []storage.Doc
		_ = json.NewDecoder(res.Body).Decode(&docs)
		for _, d := range docs {
			b, _ := json.Marshal(d)
			buf.Write(b)
			buf.WriteByte('\n')
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write(buf.Bytes())

	case "zip":
		// pack contentMd as files {key}.{lang}.md
		res, _ := http.Post("http://localhost"+env("MDDB_ADDR", ":11023")+"/v1/search", "application/json", bytes.NewReader(mustJSON(sr)))
		defer func() { _ = res.Body.Close() }()
		var docs []storage.Doc
		_ = json.NewDecoder(res.Body).Decode(&docs)
		var z bytes.Buffer
		zw := zip.NewWriter(&z)
		for _, d := range docs {
			name := fmt.Sprintf("%s.%s.md", safe(d.Key), safe(d.Lang))
			f, _ := zw.Create(name)
			_, _ = io.WriteString(f, d.ContentMD)
		}
		_ = zw.Close()
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(z.Bytes())

	default:
		http.Error(w, `{"error":"unsupported format"}`, 400)
	}
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	// Check admin permission (database-wide operation)
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermAdmin); err != nil {
			http.Error(w, `{"error":"admin access required"}`, http.StatusForbidden)
			return
		}
	}

	// snapshot = copy pliku DB (najprościej)
	dst := r.URL.Query().Get("to")
	if dst == "" {
		dst = fmt.Sprintf("backup-%d.db", time.Now().Unix())
	}
	safeDst, err := safeBackupPath(dst, false)
	if err != nil {
		bad(w, err)
		return
	}
	if err := copyFile(s.Path, safeDst); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]string{"backup": safeDst})
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From string `json:"from"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		bad(w, err)
		return
	}
	if body.From == "" {
		bad(w, errors.New("missing from"))
		return
	}

	// Check admin permission (database-wide operation)
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermAdmin); err != nil {
			http.Error(w, `{"error":"admin access required"}`, http.StatusForbidden)
			return
		}
	}

	safeFrom, err := safeBackupPath(body.From, true)
	if err != nil {
		bad(w, err)
		return
	}

	// zamknij db, podmień plik, otwórz ponownie
	_ = s.DB.Close()
	if err := copyFile(safeFrom, s.Path); err != nil {
		bad(w, err)
		return
	}

	db, err := bolt.Open(s.Path, 0600, getOptimizedBoltOptions())
	if err != nil {
		bad(w, err)
		return
	}
	s.DB = db

	// Reset binlog after restore — forces followers to re-snapshot
	if s.Binlog != nil {
		if err := s.Binlog.Rotate(0); err != nil {
			slog.Warn("failed to reset binlog after restore", "err", err)
		}
	}

	ok(w, map[string]string{"restored": body.From})
}

func (s *Server) handleTruncate(w http.ResponseWriter, r *http.Request) {
	var req TruncateRequest
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

	var bo binlog.BinlogOps
	err := s.DBUpdate(func(tx *bolt.Tx) error {
		bRev := tx.Bucket([]byte("rev"))
		bDocs := tx.Bucket([]byte("docs"))

		// Dla każdego dokumentu w kolekcji: utnij historię do N
		c := bDocs.Cursor()
		prefix := []byte("doc|" + req.Collection + "|")
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			dPtr, err := loadDoc(v)
			if err != nil {
				return err
			}
			d := *dPtr
			// Zbierz revety
			rc := bRev.Cursor()
			rp := storage.RevPrefix(req.Collection, d.ID)
			var revKeys [][]byte
			for rk, _ := rc.Seek(rp); rk != nil && bytes.HasPrefix(rk, rp); rk, _ = rc.Next() {
				cp := make([]byte, len(rk))
				copy(cp, rk)
				revKeys = append(revKeys, cp)
			}
			// jeśli trzeba ciąć
			if req.KeepRevs >= 0 && len(revKeys) > req.KeepRevs {
				// posortowane rosnąco po ts dzięki key; usuń najstarsze
				toDel := revKeys[:len(revKeys)-req.KeepRevs]
				for _, delk := range toDel {
					_ = bRev.Delete(delk)
					bo.Delete("rev", delk)
				}
			}
			// DropCache placeholder — jeśli trzymasz rendery, wyczyść je tutaj
			_ = req.DropCache
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
	ok(w, map[string]string{"status": "truncated"})
}

// --- utils

func ok(w http.ResponseWriter, v any) {
	b, _ := json.Marshal(v)
	w.WriteHeader(200)
	_, _ = w.Write(b) // #nosec G705 -- response write to http.ResponseWriter
}
func bad(w http.ResponseWriter, err error) {
	w.WriteHeader(400)
	_, _ = fmt.Fprintf(w, `{"error":%q}`, err.Error()) // #nosec G705 -- response write to http.ResponseWriter
}

// handleHealth returns a simple health check response
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Check if server has finished initialization
	if !s.Ready {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"status":"warming_up"}`))
		return
	}

	// Check if database is accessible
	err := s.DBView(func(tx *bolt.Tx) error {
		return nil
	})

	if err != nil {
		w.WriteHeader(503)
		_, _ = fmt.Fprintf(w, `{"status":"unhealthy","error":%q}`, err.Error())
		return
	}

	// GO-032: an operator checking health should see whether acknowledged
	// writes will actually survive, not only that the process is up.
	body := map[string]any{
		"status":      "healthy",
		"mode":        string(s.Mode),
		"persistence": s.Persistence,
		"durable":     s.Persistence.Durable(),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Debug("writing the health response failed", "err", err)
	}
}

// handleComplianceStatus returns the ISO 27001 / SOC 2 production-guard
// state so operators (and the Panel) can see whether the server is
// running with the required security envelope.
func (s *Server) handleComplianceStatus(w http.ResponseWriter, r *http.Request) {
	missing := CheckProductionGuards()
	type missingEntry struct {
		EnvVar string `json:"envVar"`
		Want   string `json:"want"`
		Reason string `json:"reason"`
	}
	entries := make([]missingEntry, 0, len(missing))
	for _, m := range missing {
		entries = append(entries, missingEntry{EnvVar: m.EnvVar, Want: m.Want, Reason: m.Reason})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"production":   IsProduction(),
		"compliant":    len(missing) == 0,
		"missing":      entries,
		"missingCount": len(missing),
	})
}

func (s *Server) collectionChecksum(collection string) (string, int) {
	var checksum uint32
	var count int

	_ = s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		prefix := []byte("doc|" + collection + "|")
		c := bDocs.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			count++
			// Hash key + first 64 bytes of value (contains updatedAt in serialized form)
			h := crc32.ChecksumIEEE(k)
			if len(v) > 64 {
				h ^= crc32.ChecksumIEEE(v[:64])
			} else {
				h ^= crc32.ChecksumIEEE(v)
			}
			checksum ^= h
		}
		return nil
	})

	return fmt.Sprintf("%08x", checksum), count
}

func (s *Server) handleChecksum(w http.ResponseWriter, r *http.Request) {
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

	checksum, count := s.collectionChecksum(collection)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"collection":    collection,
		"checksum":      checksum,
		"documentCount": count,
	})
}
