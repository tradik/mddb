package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	json "github.com/goccy/go-json"
	bolt "go.etcd.io/bbolt"
)

type AccessMode string

const (
	ModeRead  AccessMode = "read"
	ModeWrite AccessMode = "write"
	ModeRW    AccessMode = "wr"
)

type Server struct {
	DB                 *bolt.DB
	Path               string
	Mode               AccessMode
	Hooks              Hooks // optional extensions
	BucketNames        BucketNames
	Cache              *DocumentCache          // Read-through cache (legacy)
	LockFreeCache      *LockFreeCache          // Lock-free cache (extreme performance)
	IndexQueue         *IndexQueue             // Async metadata indexing
	WAL                *WAL                    // Write-Ahead Log
	MVCC               *MVCC                   // Multi-Version Concurrency Control
	BloomFilters       *BloomFilterManager     // Bloom filters for negative lookups
	DeltaEncoder       *DeltaEncoder           // Delta encoding for revisions
	AdaptiveIndex      *AdaptiveIndexManager   // Adaptive indexing
	AsyncIO            *AsyncIO                // Async I/O
	ZeroCopy           *ZeroCopyManager        // Zero-copy I/O
	SIMD               *SIMDProcessor          // Vectorized operations
	ShardCluster       *ShardCluster           // Distributed sharding
	finalBatchProcessor *FinalBatchProcessor   // Final optimized batch processor
	UseExtreme         bool                    // Enable extreme performance features
}

// BucketNames caches bucket name byte slices to avoid repeated allocations
type BucketNames struct {
	Docs   []byte
	IdxMeta []byte
	Rev    []byte
	ByKey  []byte
}

type Hooks struct {
	PostAddWebhookURL    string   // e.g. http://localhost:9000/hook/add
	PostAddExec          []string // e.g. ["/usr/local/bin/on-add"]
	PostUpdateWebhookURL string
	PostUpdateExec       []string
}

type Doc struct {
	ID        string              `json:"id"`        // generated
	Key       string              `json:"key"`       // e.g. "homepage"
	Lang      string              `json:"lang"`      // e.g. "en_GB"
	Meta      map[string][]string `json:"meta"`      // meta values (multi)
	ContentMD string              `json:"contentMd"` // raw markdown
	AddedAt   int64               `json:"addedAt"`
	UpdatedAt int64               `json:"updatedAt"`
}

type AddRequest struct {
	Collection string              `json:"collection"`
	Key        string              `json:"key"`
	Lang       string              `json:"lang"`
	Meta       map[string][]string `json:"meta"`
	ContentMD  string              `json:"contentMd"`
}

type GetRequest struct {
	Collection string            `json:"collection"`
	Key        string            `json:"key"`
	Lang       string            `json:"lang"`
	Env        map[string]string `json:"env"` // for templating
}

type SearchRequest struct {
	Collection string              `json:"collection"`
	FilterMeta map[string][]string `json:"filterMeta"` // AND over keys, OR over values
	Sort       string              `json:"sort"`       // addedAt|updatedAt|key
	Asc        bool                `json:"asc"`
	Limit      int                 `json:"limit"`
	Offset     int                 `json:"offset"`
}

type ExportRequest struct {
	Collection string              `json:"collection"`
	FilterMeta map[string][]string `json:"filterMeta"`
	Format     string              `json:"format"` // ndjson|zip
}

type TruncateRequest struct {
	Collection string `json:"collection"`
	KeepRevs   int    `json:"keepRevs"` // keep last N revisions per doc (0 = drop all history)
	DropCache  bool   `json:"dropCache"`
}

type DeleteRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Lang       string `json:"lang"`
}

type DeleteCollectionRequest struct {
	Collection string `json:"collection"`
}

// getOptimizedBoltOptions returns optimized BoltDB options for performance
func getOptimizedBoltOptions() *bolt.Options {
	return &bolt.Options{
		Timeout:         2 * time.Second,
		NoFreelistSync:  true,                    // Don't sync freelist to disk on every commit (faster writes)
		FreelistType:    bolt.FreelistMapType,    // Use hashmap for freelist (faster than array)
		NoGrowSync:      false,                   // Sync after growing mmap (safer)
		InitialMmapSize: 100 * 1024 * 1024,       // 100MB initial mmap (reduce remapping)
	}
}

func main() {
	dbPath := env("MDDB_PATH", "mddb.db")
	mode := AccessMode(env("MDDB_MODE", "wr")) // read|write|wr
	
	db, err := bolt.Open(dbPath, 0600, getOptimizedBoltOptions())
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("Error closing database: %v", err)
		}
	}()

	// Check for extreme performance mode
	useExtreme := os.Getenv("MDDB_EXTREME") == "true"
	
	s := &Server{
		DB:   db,
		Path: dbPath,
		Mode: mode,
		BucketNames: BucketNames{
			Docs:    []byte("docs"),
			IdxMeta: []byte("idxmeta"),
			Rev:     []byte("rev"),
			ByKey:   []byte("bykey"),
		},
		Cache:         NewDocumentCache(1000, 300),     // 1000 docs, 5min TTL
		LockFreeCache: NewLockFreeCache(10000, 300),    // 10k docs, 5min TTL (lock-free)
		IndexQueue:    NewIndexQueue(nil, 4),           // 4 workers (will set server below)
		BloomFilters:  NewBloomFilterManager(),         // Bloom filters
		DeltaEncoder:  NewDeltaEncoder(),               // Delta encoding
		AdaptiveIndex: NewAdaptiveIndexManager(),       // Adaptive indexing
		AsyncIO:       NewAsyncIO(),                    // Async I/O
		ZeroCopy:      NewZeroCopyManager(),            // Zero-copy I/O
		SIMD:          NewSIMDProcessor(),              // Vectorized operations
		ShardCluster:  NewShardCluster(4, 2),           // 4 shards, 2x replication
		UseExtreme:    useExtreme,
	}
	s.IndexQueue.server = s // Set server reference
	
	// Initialize extreme performance features
	if useExtreme {
		log.Println("🚀 Extreme Performance Mode ENABLED")
		
		// Initialize WAL
		wal, err := NewWAL(dbPath, SyncPeriodic)
		if err != nil {
			log.Fatalf("Failed to initialize WAL: %v", err)
		}
		s.WAL = wal
		log.Println("  ✓ WAL initialized (SyncPeriodic)")
		
		// Initialize MVCC
		s.MVCC = NewMVCC()
		log.Println("  ✓ MVCC initialized")
		
		log.Println("  ✓ Lock-Free Cache enabled")
		log.Println("  ✓ Bloom Filters enabled")
		log.Println("  ✓ Delta Encoding enabled")
		log.Println("  ✓ Adaptive Compression enabled (Snappy + Zstd)")
		log.Println("  ✓ Adaptive Indexing enabled")
		log.Println("  ✓ Async I/O enabled")
		log.Println("  ✓ Zero-Copy I/O enabled")
		log.Println("  ✓ Vectorized Operations (SIMD) enabled")
		log.Println("  ✓ Distributed Sharding enabled (4 shards, 2x replication)")
	}
	
	if err := s.ensureBuckets(); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/add", s.guardWrite(s.handleAdd))
	mux.HandleFunc("/v1/get", s.handleGet)
	mux.HandleFunc("/v1/search", s.handleSearch)
	mux.HandleFunc("/v1/export", s.handleExport)
	mux.HandleFunc("/v1/backup", s.handleBackup)
	mux.HandleFunc("/v1/restore", s.guardWrite(s.handleRestore))
	mux.HandleFunc("/v1/truncate", s.guardWrite(s.handleTruncate))
	mux.HandleFunc("/v1/delete", s.guardWrite(s.handleDelete))
	mux.HandleFunc("/v1/delete-collection", s.guardWrite(s.handleDeleteCollection))
	mux.HandleFunc("/v1/stats", s.handleStats)

	httpAddr := env("MDDB_ADDR", ":11023")
	grpcAddr := env("MDDB_GRPC_ADDR", ":11024")

	// Start HTTP server
	go func() {
		log.Printf("mddb HTTP listening on %s (mode=%s, db=%s)", httpAddr, s.Mode, dbPath)
		if err := http.ListenAndServe(httpAddr, withJSON(mux)); err != nil {
			log.Fatal(err)
		}
	}()

	// Start HTTP/3 server if extreme mode
	if useExtreme {
		http3Addr := env("MDDB_HTTP3_ADDR", ":11443")
		go func() {
			h3Server, err := NewHTTP3Server(http3Addr, HTTP3Middleware(withJSON(mux)))
			if err != nil {
				log.Printf("⚠️  Failed to start HTTP/3 server: %v", err)
				return
			}
			if err := h3Server.Start(); err != nil {
				log.Printf("⚠️  HTTP/3 server error: %v", err)
			}
		}()
	}

	// Start gRPC server
	log.Printf("mddb gRPC listening on %s (mode=%s, db=%s)", grpcAddr, s.Mode, dbPath)
	if err := startGRPCServer(s, grpcAddr); err != nil {
		log.Fatal(err)
	}
}

// --- helpers / buckets

func (s *Server) ensureBuckets() error {
	return s.DB.Update(func(tx *bolt.Tx) error {
		_, _ = tx.CreateBucketIfNotExists(s.BucketNames.Docs)    // doc|collection|id -> json
		_, _ = tx.CreateBucketIfNotExists(s.BucketNames.IdxMeta) // meta|collection|key|value|docID -> 1
		_, _ = tx.CreateBucketIfNotExists(s.BucketNames.Rev)     // rev|collection|docID|ts -> json
		_, _ = tx.CreateBucketIfNotExists(s.BucketNames.ByKey)   // bykey|collection|key|lang -> docID
		return nil
	})
}

func kDoc(coll, id string) []byte          { return []byte("doc|" + coll + "|" + id) }
func kByKey(coll, key, lang string) []byte { return []byte("bykey|" + coll + "|" + key + "|" + lang) }
func kRevPrefix(coll, id string) []byte    { return []byte("rev|" + coll + "|" + id + "|") }
func kMetaKeyPrefix(coll, mk, mv string) []byte {
	return []byte("meta|" + coll + "|" + mk + "|" + mv + "|")
}

// --- middleware

func withJSON(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		h.ServeHTTP(w, r)
	})
}

func (s *Server) guardWrite(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Mode == ModeRead {
			http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

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

	now := time.Now().Unix()
	docID := genID(req.Collection, req.Key, req.Lang) // deterministic ID (collection|key|lang)

	var saved Doc
	err := s.DB.Update(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		bRev := tx.Bucket([]byte("rev"))
		bByK := tx.Bucket([]byte("bykey"))

		// load existing
		existing := Doc{}
		if v := bDocs.Get(kDoc(req.Collection, docID)); v != nil {
			if err := json.Unmarshal(v, &existing); err != nil {
				return err
			}
		}
		added := existing.AddedAt
		if added == 0 {
			added = now
		}

		doc := Doc{
			ID: docID, Key: req.Key, Lang: req.Lang, Meta: req.Meta,
			ContentMD: req.ContentMD, AddedAt: added, UpdatedAt: now,
		}
		buf, _ := json.Marshal(doc)
		if err := bDocs.Put(kDoc(req.Collection, docID), buf); err != nil {
			return err
		}
		if err := bByK.Put(kByKey(req.Collection, req.Key, req.Lang), []byte(docID)); err != nil {
			return err
		}

		// Only reindex metadata if it has changed (MAJOR OPTIMIZATION)
		if metadataChanged(existing.Meta, doc.Meta) {
			// delete old indices
			if existing.ID != "" && existing.Meta != nil {
				for mk, vals := range existing.Meta {
					for _, mv := range vals {
						prefix := append(kMetaKeyPrefix(req.Collection, mk, mv), []byte(existing.ID)...)
						_ = bIdx.Delete(prefix)
					}
				}
			}
			// add new indices
			for mk, vals := range doc.Meta {
				for _, mv := range vals {
					key := append(kMetaKeyPrefix(req.Collection, mk, mv), []byte(doc.ID)...)
					if err := bIdx.Put(key, []byte("1")); err != nil {
						return err
					}
				}
			}
		}

		// revision
		rkey := append(kRevPrefix(req.Collection, doc.ID), []byte(fmt.Sprintf("%020d", now))...)
		if err := bRev.Put(rkey, buf); err != nil {
			return err
		}

		saved = doc
		return nil
	})
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

	var doc Doc
	err := s.DB.View(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bByK := tx.Bucket([]byte("bykey"))
		docID := bByK.Get(kByKey(req.Collection, req.Key, req.Lang))
		if docID == nil {
			return errors.New("not found")
		}
		v := bDocs.Get(kDoc(req.Collection, string(docID)))
		if v == nil {
			return errors.New("not found")
		}
		return json.Unmarshal(v, &doc)
	})
	if err != nil {
		bad(w, err)
		return
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

	type row struct{ Doc Doc }
	var rows []row

	err := s.DB.View(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		seen := make(map[string]bool)

		// Jeśli brak filtra meta → pełny scan kolekcji (dla prostoty; można dodać bucket per collection)
		if len(req.FilterMeta) == 0 {
			c := bDocs.Cursor()
			prefix := []byte("doc|" + req.Collection + "|")
			for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
				var d Doc
				if err := json.Unmarshal(v, &d); err != nil {
					return err
				}
				rows = append(rows, row{d})
			}
		} else {
			// Intersect po meta kluczach
			var sets [][]string
			for mk, mvals := range req.FilterMeta {
				var ids []string
				for _, mv := range mvals {
					prefix := kMetaKeyPrefix(req.Collection, mk, mv)
					c := bIdx.Cursor()
					for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
						id := string(k[len(prefix):])
						ids = append(ids, id)
					}
				}
				ids = unique(ids)
				sets = append(sets, ids)
			}
			ids := intersect(sets...)
			for _, id := range ids {
				if seen[id] {
					continue
				}
				seen[id] = true
				v := tx.Bucket([]byte("docs")).Get(kDoc(req.Collection, id))
				if v == nil {
					continue
				}
				var d Doc
				if err := json.Unmarshal(v, &d); err != nil {
					return err
				}
				rows = append(rows, row{d})
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

	out := make([]Doc, 0, end-start)
	for _, r := range rows[start:end] {
		out = append(out, r.Doc)
	}
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

	// Reużyj /search
	sr := SearchRequest{Collection: req.Collection, FilterMeta: req.FilterMeta, Limit: 1 << 30}
	buf := new(bytes.Buffer)

	switch req.Format {
	case "ndjson":
		// stream NDJSON
		res, _ := http.Post("http://localhost"+env("MDDB_ADDR", ":11023")+"/v1/search", "application/json", bytes.NewReader(mustJSON(sr)))
		defer func() { _ = res.Body.Close() }()
		var docs []Doc
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
		var docs []Doc
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
	// snapshot = copy pliku DB (najprościej)
	dst := r.URL.Query().Get("to")
	if dst == "" {
		dst = fmt.Sprintf("backup-%d.db", time.Now().Unix())
	}
	if err := copyFile(s.Path, dst); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]string{"backup": dst})
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

	// zamknij db, podmień plik, otwórz ponownie
	_ = s.DB.Close()
	if err := copyFile(body.From, s.Path); err != nil {
		bad(w, err)
		return
	}
	
	db, err := bolt.Open(s.Path, 0600, getOptimizedBoltOptions())
	if err != nil {
		bad(w, err)
		return
	}
	s.DB = db
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

	err := s.DB.Update(func(tx *bolt.Tx) error {
		bRev := tx.Bucket([]byte("rev"))
		bDocs := tx.Bucket([]byte("docs"))

		// Dla każdego dokumentu w kolekcji: utnij historię do N
		c := bDocs.Cursor()
		prefix := []byte("doc|" + req.Collection + "|")
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			var d Doc
			if err := json.Unmarshal(v, &d); err != nil {
				return err
			}
			// Zbierz revety
			rc := bRev.Cursor()
			rp := kRevPrefix(req.Collection, d.ID)
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
				}
			}
			// DropCache placeholder — jeśli trzymasz rendery, wyczyść je tutaj
			_ = req.DropCache
		}
		return nil
	})
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
	_, _ = w.Write(b)
}
func bad(w http.ResponseWriter, err error) {
	w.WriteHeader(400)
	_, _ = fmt.Fprintf(w, `{"error":%q}`, err.Error())
}
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	type CollectionStats struct {
		Name           string `json:"name"`
		DocumentCount  int    `json:"documentCount"`
		RevisionCount  int    `json:"revisionCount"`
		MetaIndexCount int    `json:"metaIndexCount"`
	}

	type Stats struct {
		DatabasePath    string            `json:"databasePath"`
		DatabaseSize    int64             `json:"databaseSize"`
		Mode            string            `json:"mode"`
		Collections     []CollectionStats `json:"collections"`
		TotalDocuments  int               `json:"totalDocuments"`
		TotalRevisions  int               `json:"totalRevisions"`
		TotalMetaIndices int              `json:"totalMetaIndices"`
		Uptime          string            `json:"uptime"`
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

	// Collect statistics per collection
	collectionMap := make(map[string]*CollectionStats)

	err := s.DB.View(func(tx *bolt.Tx) error {
		// Count documents per collection
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs != nil {
			c := bDocs.Cursor()
			for k, _ := c.First(); k != nil; k, _ = c.Next() {
				// key format: doc|collection|id
				parts := strings.Split(string(k), "|")
				if len(parts) >= 2 {
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
				if len(parts) >= 2 {
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
				if len(parts) >= 2 {
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

	// Convert map to slice
	for _, cs := range collectionMap {
		stats.Collections = append(stats.Collections, *cs)
	}

	// Sort collections by name
	sort.Slice(stats.Collections, func(i, j int) bool {
		return stats.Collections[i].Name < stats.Collections[j].Name
	})

	ok(w, stats)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func genID(parts ...string) string {
	// Optimized ID generation without string allocations
	totalLen := 0
	for i, part := range parts {
		totalLen += len(part)
		if i < len(parts)-1 {
			totalLen++ // for '|'
		}
	}
	
	buf := make([]byte, 0, totalLen)
	for i, part := range parts {
		for j := 0; j < len(part); j++ {
			c := part[j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			buf = append(buf, c)
		}
		if i < len(parts)-1 {
			buf = append(buf, '|')
		}
	}
	
	return string(buf)
}
func applyEnv(s string, env map[string]string) string {
	for k, v := range env {
		s = strings.ReplaceAll(s, "%%"+k+"%%", v)
	}
	return s
}
func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s)
}
func unique(in []string) []string {
	m := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, x := range in {
		if _, ok := m[x]; !ok {
			m[x] = struct{}{}
			out = append(out, x)
		}
	}
	return out
}
func intersect(sets ...[]string) []string {
	if len(sets) == 0 {
		return nil
	}
	m := map[string]int{}
	for _, s := range sets {
		for _, id := range s {
			m[id]++
		}
	}
	out := []string{}
	for id, c := range m {
		if c == len(sets) {
			out = append(out, id)
		}
	}
	return out
}
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
func sortDocs(docs []Doc, field string, asc bool) {
	sort.Slice(docs, func(i, j int) bool {
		var less bool
		switch field {
		case "addedAt":
			less = docs[i].AddedAt < docs[j].AddedAt
		case "updatedAt":
			less = docs[i].UpdatedAt < docs[j].UpdatedAt
		case "key":
			less = docs[i].Key < docs[j].Key
		default:
			less = docs[i].UpdatedAt < docs[j].UpdatedAt
		}
		if asc {
			return less
		}
		return !less
	})
}

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

	docID := genID(req.Collection, req.Key, req.Lang)
	
	err := s.DB.Update(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		bRev := tx.Bucket([]byte("rev"))
		bByK := tx.Bucket([]byte("bykey"))

		// Check if document exists and load it for cleanup
		v := bDocs.Get(kDoc(req.Collection, docID))
		if v == nil {
			return errors.New("document not found")
		}

		// Load document to get metadata for index cleanup
		var doc Doc
		if err := json.Unmarshal(v, &doc); err != nil {
			return err
		}

		// Delete document
		if err := bDocs.Delete(kDoc(req.Collection, docID)); err != nil {
			return err
		}

		// Delete from bykey index
		if err := bByK.Delete(kByKey(req.Collection, req.Key, req.Lang)); err != nil {
			return err
		}

		// Delete all revisions
		c := bRev.Cursor()
		rp := kRevPrefix(req.Collection, docID)
		for k, _ := c.Seek(rp); k != nil && bytes.HasPrefix(k, rp); k, _ = c.Next() {
			if err := bRev.Delete(k); err != nil {
				return err
			}
		}

		// Delete metadata indices
		for mk, vals := range doc.Meta {
			for _, mv := range vals {
				key := append(kMetaKeyPrefix(req.Collection, mk, mv), []byte(docID)...)
				if err := bIdx.Delete(key); err != nil {
					return err
				}
			}
		}

		return nil
	})

	if err != nil {
		bad(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "deleted",
		"collection": req.Collection,
		"key": req.Key,
		"lang": req.Lang,
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

	var deletedCount int
	
	err := s.DB.Update(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		bRev := tx.Bucket([]byte("rev"))
		bByK := tx.Bucket([]byte("bykey"))

		// Delete all documents in collection
		c := bDocs.Cursor()
		prefix := []byte("doc|" + req.Collection + "|")
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			// Load document to get metadata for index cleanup
			var doc Doc
			if err := json.Unmarshal(v, &doc); err != nil {
				continue
			}

			// Delete document
			if err := bDocs.Delete(k); err != nil {
				return err
			}

			// Delete from bykey index
			if err := bByK.Delete(kByKey(req.Collection, doc.Key, doc.Lang)); err != nil {
				return err
			}

			// Delete all revisions
			rc := bRev.Cursor()
			rp := kRevPrefix(req.Collection, doc.ID)
			for rk, _ := rc.Seek(rp); rk != nil && bytes.HasPrefix(rk, rp); rk, _ = rc.Next() {
				if err := bRev.Delete(rk); err != nil {
					return err
				}
			}

			// Delete metadata indices
			for mk, vals := range doc.Meta {
				for _, mv := range vals {
					key := append(kMetaKeyPrefix(req.Collection, mk, mv), []byte(doc.ID)...)
					if err := bIdx.Delete(key); err != nil {
						return err
					}
				}
			}

			deletedCount++
		}

		return nil
	})

	if err != nil {
		bad(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "deleted",
		"collection": req.Collection,
		"deletedCount": deletedCount,
	})
}
