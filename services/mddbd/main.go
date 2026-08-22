package main

import (
	"context"
	"fmt"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"log/slog"
	"mddb/internal/audit"
	"mddb/internal/automationlog"
	"mddb/internal/binlog"
	"mddb/internal/cache"
	"mddb/internal/compression"
	"mddb/internal/delta"
	"mddb/internal/embedding"
	"mddb/internal/encryption"
	"mddb/internal/envconf"
	"mddb/internal/fts"
	"mddb/internal/geo"
	"mddb/internal/indexqueue"
	"mddb/internal/logging"
	"mddb/internal/metrics"
	"mddb/internal/schema"
	"mddb/internal/spell"
	"mddb/internal/temporal"
	"mddb/internal/ttl"
	"mddb/internal/vector"
	"mddb/internal/webhooks"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// VERSION is the current release version of the MDDB server.
const VERSION = "2.12.0"

// AccessMode defines the database access mode (read, write, or both).
type AccessMode string

// Access mode constants.
const (
	ModeRead  AccessMode = "read"
	ModeWrite AccessMode = "write"
	ModeRW    AccessMode = "wr"
)

// Server is the main MDDB server instance holding the database and all subsystems.
type Server struct {
	// restoreMu guards the swappable DB handle (and the in-place reload of the
	// caches/managers) against a replication snapshot restore (GO-004). All
	// production BoltDB access goes through DBView/DBUpdate, which hold a read
	// lock; the follower's restore holds the write lock for the whole swap, so
	// in-flight reads drain before the old *bolt.DB is closed and never observe
	// a half-swapped handle.
	restoreMu           sync.RWMutex
	DB                  *bolt.DB
	Path                string
	Mode                AccessMode
	Config              ServerConfig // protocol toggles & addresses
	Hooks               Hooks        // optional extensions
	BucketNames         BucketNames
	Cache               *cache.DocumentCache   // Read-through cache (legacy)
	SearchCache         *cache.SearchCache     // Opt-in per-request search-result cache (GO-031)
	Persistence         PersistenceStatus      // What the data directory can promise (GO-032)
	SearchLimiter       *SearchLimiter         // Bounds concurrent heavy queries (GO-033)
	HTTP3               *HTTP3Server           // Held so shutdown can close the listener (GO-034)
	LockFreeCache       *cache.LockFreeCache   // Lock-free cache (extreme performance)
	IndexQueue          *indexqueue.IndexQueue // Async metadata indexing
	WAL                 *WAL                   // Write-Ahead Log
	MVCC                *MVCC                  // Multi-Version Concurrency Control
	BloomFilters        *BloomFilterManager    // Bloom filters for negative lookups
	DeltaEncoder        *delta.DeltaEncoder    // Delta encoding for revisions
	AdaptiveIndex       *AdaptiveIndexManager  // Adaptive indexing
	AsyncIO             *AsyncIO               // Async I/O
	ZeroCopy            *ZeroCopyManager       // Zero-copy I/O
	SIMD                *vector.SIMDProcessor  // Vectorized operations
	ShardCluster        *ShardCluster          // Distributed sharding
	finalBatchProcessor *FinalBatchProcessor   // Final optimized batch processor
	UseExtreme          bool                   // Enable extreme performance features
	// Vector search
	VectorStore       *vector.VectorStore              // Persistent vector storage in BoltDB
	VectorIndex       *vector.VectorIndex              // In-memory flat vector index
	VectorSearchers   map[string]vector.VectorSearcher // algorithm name -> searcher (flat, hnsw, ivf, pq, sq, bq)
	QuantizedVecIndex *vector.QuantizedVectorIndex     // In-memory quantized vector index (int8/int4)
	EmbeddingWorker   *EmbeddingWorker                 // Background embedding processor
	Embedding         embedding.Provider               // Embedding generation provider
	// Geo search
	GeoStore     *geo.GeoStore     // Persistent geo points in BoltDB ("geo" bucket)
	GeoIndex     *geo.GeoIndex     // In-memory R-tree geo index (default)
	GeoHashIndex *geo.GeoHashIndex // Alternative geohash-prefix index
	// New features
	TTLManager         *ttl.TTLManager          // Document TTL / auto-expiry
	FTSIndex           *fts.FTSIndex            // Full-text search index
	WebhookManager     *webhooks.WebhookManager // Webhook subscriptions and delivery
	SchemaManager      *schema.SchemaManager    // Per-collection metadata schema validation
	Metrics            *metrics.Metrics         // Prometheus-compatible telemetry
	AuthManager        *AuthManager             // Authentication and authorization
	AuditManager       *audit.AuditManager      // Audit log (ISO 27001 A.8.15, SOC 2 CC7.2)
	RateLimiter        *RateLimiter             // Cross-transport rate limiter (ISO 27001 A.5.30, SOC 2 CC6.6)
	Encryptor          *encryption.Encryptor    // At-rest AES-256-GCM encryption (ISO 27001 A.8.24, SOC 2 CC6.7)
	RotationManager    *RotationManager         // Background re-encryption after key rotation
	AuthFailureTracker *AuthFailureTracker      // Sliding-window auth failure counter → security.auth_failure_burst
	LagMonitor         *ReplicationLagMonitor   // Periodic replication-lag → ops.replication_lag_high
	DiskMonitor        *DiskUsageMonitor        // Periodic disk-usage → ops.disk_usage_high
	SynonymManager     *fts.SynonymManager      // Synonym dictionaries for FTS
	StopWordManager    *fts.StopWordManager     // Per-collection custom stop words for FTS
	AutomationManager  *AutomationManager       // Automation: triggers, crons, webhook targets
	AutomationLogStore *automationlog.Store     // Automation execution logs
	CronScheduler      *CronScheduler           // Cron scheduler for automation
	CollectionManager  *CollectionManager       // Per-collection attributes (type, description, icon, etc.)
	// Backends routes document payloads to a per-collection storage backend
	// (GO-021). nil until InitStorageBackends runs; a nil registry means every
	// collection uses BoltDB, which is what the server did before.
	Backends        *BackendRegistry
	CurationManager *CurationManager          // FTS/Hybrid curation rules: pinned + hidden results per query
	TemporalManager *temporal.TemporalManager // Document lifecycle event tracking (create/update/access)
	SpellManager    *spell.SpellManager       // Spell correction for FTS queries and document content
	SSEHub          *SSEHub                   // Server-Sent Events for real-time document change notifications
	BulkIngest      *BulkIngestManager        // Async bulk ingest job manager
	MCPInfo         MCPServerInfo             // Customizable MCP server profile
	MCPInstructions string                    // System prompt for LLM — how to use this server
	mcpKeyStore     *mcpAPIKeyStore           // BoltDB-backed MCP API key store
	mcpAuth         *MCPAPIKeyMiddleware      // MCP API key middleware (for cache invalidation)
	// Replication
	Binlog          *binlog.Binlog     // Binary replication log
	ReplicationRole string             // "leader", "follower", or "" (standalone)
	NodeID          string             // Unique node identifier
	replServer      *ReplicationServer // Leader-side gRPC replication service
	replClient      *ReplicationClient // Follower-side replication client
	// Readiness
	Ready bool // Set to true after full initialization; health check returns "warming_up" until then
}

// BucketNames caches bucket name byte slices to avoid repeated allocations
type BucketNames struct {
	Docs    []byte
	IdxMeta []byte
	Rev     []byte
	ByKey   []byte
}

// Hooks holds optional post-action webhook and exec hooks.
type Hooks struct {
	PostAddWebhookURL    string   // e.g. http://localhost:9000/hook/add
	PostAddExec          []string // e.g. ["/usr/local/bin/on-add"]
	PostUpdateWebhookURL string
	PostUpdateExec       []string
}

// storage.Doc represents a stored markdown document.
// AddRequest is the HTTP request body for adding or updating a document.
type AddRequest struct {
	Collection string              `json:"collection"`
	Key        string              `json:"key"`
	Lang       string              `json:"lang"`
	Meta       map[string][]string `json:"meta"`
	ContentMD  string              `json:"contentMd"`
	TTL        int64               `json:"ttl,omitempty"` // seconds; 0 = no expiry
}

// GetRequest is the HTTP request body for retrieving a document.
type GetRequest struct {
	Collection string            `json:"collection"`
	Key        string            `json:"key"`
	Lang       string            `json:"lang"`
	Env        map[string]string `json:"env"` // for templating
}

// SearchRequest is the HTTP request body for searching documents.
type SearchRequest struct {
	Collection string              `json:"collection"`
	FilterMeta map[string][]string `json:"filterMeta"` // AND over keys, OR over values
	Sort       string              `json:"sort"`       // addedAt|updatedAt|key
	Asc        bool                `json:"asc"`
	Limit      int                 `json:"limit"`
	Offset     int                 `json:"offset"`
}

// ExportRequest is the HTTP request body for exporting documents.
type ExportRequest struct {
	Collection string              `json:"collection"`
	FilterMeta map[string][]string `json:"filterMeta"`
	Format     string              `json:"format"` // ndjson|zip
}

// TruncateRequest is the HTTP request body for truncating a collection.
type TruncateRequest struct {
	Collection string `json:"collection"`
	KeepRevs   int    `json:"keepRevs"` // keep last N revisions per doc (0 = drop all history)
	DropCache  bool   `json:"dropCache"`
}

// DeleteRequest is the HTTP request body for deleting a single document.
type DeleteRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Lang       string `json:"lang"`
}

// DeleteCollectionRequest is the HTTP request body for deleting an entire collection.
type DeleteCollectionRequest struct {
	Collection string `json:"collection"`
}

// getOptimizedBoltOptions returns optimized BoltDB options for performance
func getOptimizedBoltOptions() *bolt.Options {
	return &bolt.Options{
		Timeout:         2 * time.Second,
		NoFreelistSync:  true,                 // Don't sync freelist to disk on every commit (faster writes)
		FreelistType:    bolt.FreelistMapType, // Use hashmap for freelist (faster than array)
		NoGrowSync:      false,                // Sync after growing mmap (safer)
		InitialMmapSize: 100 * 1024 * 1024,    // 100MB initial mmap (reduce remapping)
	}
}

func main() {
	// Install the structured logger before anything can log (GO-028).
	// MDDB_LOG_FORMAT picks text or json, MDDB_LOG_LEVEL the threshold; both
	// have defaults, so an unconfigured process still logs.
	logging.Setup()

	// Load server configuration (CLI flags > env vars > config file > defaults)
	srvCfg := loadServerConfig()

	dbPath := srvCfg.Database.Path
	mode := srvCfg.Database.Mode

	// GO-032: say plainly what durability this data directory can offer before
	// accepting a single write. bbolt fsyncs every commit, but that promise is
	// only worth what the storage under it is worth — a container started
	// without a volume acknowledges writes and loses them on removal.
	persistence := CheckPersistence(dbPath)
	logPersistenceStatus(persistence, slog.Warn)

	db, err := bolt.Open(dbPath, 0600, getOptimizedBoltOptions())
	if err != nil {
		logging.Fatal("startup step failed", "step", "bolt.Open", "err", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("closing database", "err", err)
		}
	}()

	// Extreme mode = HTTP/3 enabled (legacy MDDB_EXTREME mapped in config)
	useExtreme := srvCfg.HTTP3.Enabled

	s := &Server{
		DB:     db,
		Path:   dbPath,
		Mode:   mode,
		Config: srvCfg,
		BucketNames: BucketNames{
			Docs:    []byte("docs"),
			IdxMeta: []byte("idxmeta"),
			Rev:     []byte("rev"),
			ByKey:   []byte("bykey"),
		},
		Cache:         cache.NewDocumentCache(1000, 300),  // 1000 docs, 5min TTL
		SearchCache:   newSearchCache(),                   // opt-in per request (GO-031)
		Persistence:   persistence,                        // GO-032
		SearchLimiter: newSearchLimiter(),                 // GO-033
		LockFreeCache: cache.NewLockFreeCache(10000, 300), // 10k docs, 5min TTL (lock-free)
		IndexQueue:    indexqueue.NewIndexQueue(nil, 4),   // 4 workers (store wired below)
		BloomFilters:  NewBloomFilterManager(),            // Bloom filters
		DeltaEncoder:  delta.NewDeltaEncoder(),            // Delta encoding
		AdaptiveIndex: NewAdaptiveIndexManager(),          // Adaptive indexing
		AsyncIO:       NewAsyncIO(),                       // Async I/O
		ZeroCopy:      NewZeroCopyManager(),               // Zero-copy I/O
		SIMD:          vector.NewSIMDProcessor(),          // Vectorized operations
		ShardCluster:  NewShardCluster(4, 2),              // 4 shards, 2x replication
		UseExtreme:    useExtreme,
	}
	s.IndexQueue.SetStore(serverIndexStore{s: s}) // wire persistence (server now ready)

	// Start early health-only HTTP server so clients can poll readiness during init.
	// This server is shut down gracefully before the main HTTP server starts.
	var earlyServer *http.Server
	if srvCfg.HTTP.Enabled {
		earlyMux := http.NewServeMux()
		earlyMux.HandleFunc("/health", s.handleHealth)
		earlyMux.HandleFunc("/v1/health", s.handleHealth)
		earlyServer = &http.Server{
			Addr:              srvCfg.HTTP.Addr,
			Handler:           earlyMux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			if err := earlyServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Warn("early health server stopped", "err", err)
			}
		}()
		slog.Info("health endpoint listening while the server warms up", "addr", srvCfg.HTTP.Addr)
	}

	// Initialize extreme performance features
	if useExtreme {
		slog.Info("Extreme Performance Mode ENABLED")

		// Initialize WAL
		wal, err := NewWAL(dbPath, SyncPeriodic)
		if err != nil {
			logging.Fatal("Failed to initialize WAL", "err", err)
		}
		s.WAL = wal
		// Honest wording: this WAL is allocated but nothing writes to it, and
		// bbolt already fsyncs every commit, so durability comes from bbolt —
		// not from here. Saying "WAL initialized" implied a guarantee this
		// subsystem does not provide (GO-032).
		slog.Info("WAL file allocated (not in the write path; durability comes from bbolt's per-commit fsync)")

		// Initialize MVCC
		s.MVCC = NewMVCC()
		slog.Info("MVCC initialized")

		slog.Info("Lock-Free Cache enabled")
		slog.Info("Bloom Filters enabled")
		slog.Info("Delta Encoding enabled")
		slog.Info("Adaptive Compression enabled (Snappy + Zstd)")
		slog.Info("Adaptive Indexing enabled")
		slog.Info("Async I/O enabled")
		slog.Info("Zero-Copy I/O enabled")
		slog.Info("Vectorized Operations (SIMD) enabled")
		slog.Info("Distributed Sharding enabled (4 shards, 2x replication)")
	}

	if err := s.ensureBuckets(); err != nil {
		logging.Fatal("startup step failed", "step", "ensureBuckets", "err", err)
	}

	// Initialize vector search
	s.VectorStore = vector.NewVectorStore(db)
	if err := s.VectorStore.EnsureBucket(); err != nil {
		logging.Fatal("startup step failed", "step", "VectorStore.EnsureBucket", "err", err)
	}
	s.VectorIndex = vector.NewVectorIndex()

	// Initialize geo search. Startup rebuild from BoltDB happens asynchronously
	// below so mddbd can start serving non-geo requests immediately.
	// Both indexes share the same "geo" bucket in BoltDB — they're two views
	// of the same underlying data, picked by the /v1/geo-search?algorithm=...
	// parameter at query time.
	s.GeoStore = geo.NewGeoStore(db)
	if err := s.GeoStore.EnsureBucket(); err != nil {
		logging.Fatal("startup step failed", "step", "GeoStore.EnsureBucket", "err", err)
	}
	s.GeoIndex = geo.NewGeoIndex()
	s.GeoHashIndex = geo.NewGeoHashIndex()
	bqRerank := srvCfg.Vector.BQRerankFactor
	if bqRerank <= 0 {
		bqRerank = 10
	}
	s.QuantizedVecIndex = vector.NewQuantizedVectorIndex(func(collection string) vector.QuantizationType {
		if s.CollectionManager == nil {
			return vector.QuantNone
		}
		cfg, ok := s.CollectionManager.Get(collection)
		if !ok || cfg.Quantization == "" {
			return vector.QuantNone
		}
		return vector.ParseQuantization(cfg.Quantization)
	})
	s.VectorSearchers = map[string]vector.VectorSearcher{
		"flat":      s.VectorIndex,
		"hnsw":      vector.NewHNSWIndex(16, 200, 100),
		"ivf":       vector.NewIVFIndex(10, 20),
		"pq":        vector.NewPQIndex(8, 256, 20),
		"opq":       vector.NewOPQIndex(8, 256, 20, 5),
		"sq":        vector.NewSQIndex(),
		"bq":        vector.NewBQIndex(bqRerank),
		"quantized": s.QuantizedVecIndex,
	}

	// Try to load embedding config from database first
	defaultConfig, err := s.GetDefaultEmbeddingConfig()
	if err == nil && defaultConfig != nil {
		// Use stored configuration
		s.InitializeEmbeddingFromConfig(defaultConfig)
		slog.Info("vector search enabled", "source", "stored config",
			"provider", defaultConfig.Provider, "model", defaultConfig.Model, "dimensions", defaultConfig.Dimensions)
	} else {
		// Fall back to environment variables
		s.Embedding = embedding.NewProvider()
		if s.Embedding != nil {
			s.EmbeddingWorker = NewEmbeddingWorker(s.Embedding, s.VectorStore, s.VectorIndex, 1000)
			s.EmbeddingWorker.SetDiskOnly(s.QuantizedVecIndex, s.collectionDiskOnly)
			s.EmbeddingWorker.Start(2)
			slog.Info("vector search enabled", "source", "environment",
				"provider", s.Embedding.Model(), "model", s.Embedding.Model(), "dimensions", s.Embedding.Dimensions())
		} else {
			slog.Info("Vector search embedding provider not configured (set MDDB_EMBEDDING_PROVIDER or configure in panel)")
		}
	}

	// Load vectors into memory asynchronously
	go s.loadVectorIndex()

	// Load geo indexes from BoltDB asynchronously. Both R-tree and geohash
	// are populated from the same "geo" bucket so the /v1/geo-search
	// algorithm switch is a pure runtime decision — no per-algorithm
	// persistence layers. Handlers reject requests with 503 while !IsReady,
	// matching the vector-index startup contract.
	go func() {
		start := time.Now()
		n, err := s.GeoStore.Rebuild(s.GeoIndex, "")
		if err != nil {
			slog.Warn("Geo index rebuild failed", "err", err)
		}
		s.GeoIndex.SetReady()
		slog.Info("geo R-tree loaded",
			"points", n, "collections", len(s.GeoIndex.Collections()), "elapsed", time.Since(start))

		start = time.Now()
		nh, err := s.GeoStore.RebuildHash(s.GeoHashIndex, "")
		if err != nil {
			slog.Warn("Geohash index rebuild failed", "err", err)
		}
		s.GeoHashIndex.SetReady()
		slog.Info("geohash index loaded", "points", nh, "elapsed", time.Since(start))
	}()

	// Initialize audit log (ISO 27001 A.8.15 / SOC 2 CC7.2)
	auditEnabled := env("MDDB_AUDIT_ENABLED", "false") == "true"
	auditRetention := 90
	if v := env("MDDB_AUDIT_RETENTION_DAYS", ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			auditRetention = n
		}
	}
	s.AuditManager = audit.NewAuditManager(db, auditEnabled, auditRetention)
	if err := s.AuditManager.EnsureBuckets(); err != nil {
		logging.Fatal("startup step failed", "step", "AuditManager.EnsureBuckets", "err", err)
	}
	s.AuditManager.Start()
	if auditEnabled {
		slog.Info("Audit log enabled (retention days)", "auditRetention", auditRetention)

		// Wire optional external sinks (ISO 27001 A.8.15 / SOC 2 CC7.2 —
		// audit trail must be tamper-evident; pushing to an off-host SIEM
		// or syslog collector covers the case where the local DB is
		// compromised).
		exportBuf := 1024
		if v := env("MDDB_AUDIT_EXPORT_BUFFER", ""); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				exportBuf = n
			}
		}
		if u := env("MDDB_AUDIT_EXPORT_WEBHOOK_URL", ""); u != "" {
			insecure := env("MDDB_AUDIT_EXPORT_WEBHOOK_INSECURE_TLS", "") == "true"
			we, err := audit.NewWebhookExporter(u, env("MDDB_AUDIT_EXPORT_WEBHOOK_HEADER", ""), exportBuf, insecure)
			if err != nil {
				slog.Warn("audit webhook exporter", "err", err)
			} else {
				s.AuditManager.AddExporter(we)
				slog.Info("Audit webhook exporter active", "u", u)
			}
		}
		if a := env("MDDB_AUDIT_EXPORT_SYSLOG_ADDR", ""); a != "" {
			fac := env("MDDB_AUDIT_EXPORT_SYSLOG_FACILITY", "local0")
			se, err := audit.NewSyslogExporter(a, fac, exportBuf)
			if err != nil {
				slog.Warn("audit syslog exporter", "err", err)
			} else {
				s.AuditManager.AddExporter(se)
				slog.Info("Audit syslog exporter active (facility)", "a", a, "fac", fac)
			}
		}
	}

	// Initialize TTL manager
	s.TTLManager = ttl.NewTTLManager(db, serverTTLReaper{s: s})
	if err := s.TTLManager.EnsureBuckets(); err != nil {
		logging.Fatal("startup step failed", "step", "TTLManager.EnsureBuckets", "err", err)
	}
	s.TTLManager.StartCleanup(30 * time.Second)
	slog.Info("TTL manager started (cleanup every 30s)")

	// Configure compression
	compression.ConfigureCompression(srvCfg.Compression.Enabled, srvCfg.Compression.SmallThreshold, srvCfg.Compression.MediumThreshold)
	if !srvCfg.Compression.Enabled {
		slog.Info("Document compression disabled")
	}

	// Initialize FTS index
	s.FTSIndex = fts.NewFTSIndex(db)
	if err := s.FTSIndex.EnsureBuckets(); err != nil {
		logging.Fatal("startup step failed", "step", "FTSIndex.EnsureBuckets", "err", err)
	}

	// Initialize multi-language FTS support
	langReg := fts.NewLangRegistry(srvCfg.FTS.DefaultLang)
	if srvCfg.FTS.StemmingEnabled {
		fts.RegisterDefaultLanguages(langReg)
		s.FTSIndex.SetStemmer(fts.NewPorterStemmer())
		s.FTSIndex.SetLangRegistry(langReg)
		slog.Info("FTS stemming enabled", "languages", len(langReg.Languages()), "defaultLang", srvCfg.FTS.DefaultLang)
	}

	// Initialize synonym manager
	s.SynonymManager = fts.NewSynonymManager(db)
	if err := s.SynonymManager.EnsureBucket(); err != nil {
		logging.Fatal("startup step failed", "step", "SynonymManager.EnsureBucket", "err", err)
	}
	if err := s.SynonymManager.LoadAll(); err != nil {
		logging.Fatal("startup step failed", "step", "SynonymManager.LoadAll", "err", err)
	}
	if srvCfg.FTS.SynonymsEnabled {
		s.FTSIndex.SetSynonymManager(s.SynonymManager)
		slog.Info("FTS synonyms enabled")
	}

	// Initialize stop word manager
	s.StopWordManager = fts.NewStopWordManager(db)
	if err := s.StopWordManager.EnsureBucket(); err != nil {
		logging.Fatal("startup step failed", "step", "StopWordManager.EnsureBucket", "err", err)
	}
	if err := s.StopWordManager.LoadAll(); err != nil {
		logging.Fatal("startup step failed", "step", "StopWordManager.LoadAll", "err", err)
	}
	s.FTSIndex.SetStopWordManager(s.StopWordManager)
	s.StopWordManager.SetLangRegistry(langReg)
	slog.Info("Stop word manager initialized")

	// Initialize PMI data for PMISparse search
	s.FTSIndex.SetPMIData(fts.NewPMIData())

	slog.Info("Full-text search index initialized")

	// Initialize webhook manager
	s.WebhookManager = webhooks.NewWebhookManager(db)
	if err := s.WebhookManager.EnsureBucket(); err != nil {
		logging.Fatal("startup step failed", "step", "WebhookManager.EnsureBucket", "err", err)
	}
	if err := s.WebhookManager.LoadAll(); err != nil {
		logging.Fatal("startup step failed", "step", "WebhookManager.LoadAll", "err", err)
	}
	slog.Info("webhook manager initialized", "hooks", len(s.WebhookManager.List()))

	// Incident detectors — reuse the WebhookManager to deliver
	// security.* and ops.* events to whichever hooks subscribed.
	s.AuthFailureTracker = NewAuthFailureTracker(s.WebhookManager)
	s.DiskMonitor = NewDiskUsageMonitor(s.WebhookManager, dbPath)
	s.DiskMonitor.Start()

	// Initialize automation manager (triggers, crons, webhook targets)
	automationsEnabled := env("MDDB_AUTOMATIONS", "enable") != "disable"
	if automationsEnabled {
		s.AutomationManager = NewAutomationManager(db)
		s.AutomationManager.SetServer(s)
		if err := s.AutomationManager.EnsureBucket(); err != nil {
			logging.Fatal("startup step failed", "step", "AutomationManager.EnsureBucket", "err", err)
		}
		if err := s.AutomationManager.LoadAll(); err != nil {
			logging.Fatal("startup step failed", "step", "AutomationManager.LoadAll", "err", err)
		}
		slog.Info("automation manager initialized", "rules", len(s.AutomationManager.List("")))

		// Initialize automation log store
		if env("MDDB_AUTOMATION_LOGS", "enable") != "disable" {
			logTTLStr := env("MDDB_AUTOMATION_LOGS_TTL", "7d")
			logTTL, err := automationlog.ParseDurationString(logTTLStr)
			if err != nil {
				logging.Fatal("Invalid MDDB_AUTOMATION_LOGS_TTL", "err", err)
			}
			s.AutomationLogStore = automationlog.NewStore(db, logTTL)
			if err := s.AutomationLogStore.EnsureBucket(); err != nil {
				logging.Fatal("startup step failed", "step", "AutomationLogStore.EnsureBucket", "err", err)
			}
			s.AutomationLogStore.StartCleanup(5 * time.Minute)
			s.AutomationManager.SetLogStore(s.AutomationLogStore)
			slog.Info("Automation logs enabled (TTL)", "logTTLStr", logTTLStr)
		}
	}

	// Initialize schema manager
	s.SchemaManager = schema.NewSchemaManager(db)
	if err := s.SchemaManager.EnsureBucket(); err != nil {
		logging.Fatal("startup step failed", "step", "SchemaManager.EnsureBucket", "err", err)
	}
	if err := s.SchemaManager.LoadAll(); err != nil {
		logging.Fatal("startup step failed", "step", "SchemaManager.LoadAll", "err", err)
	}
	slog.Info("Schema manager initialized (schemas loaded)", "listCount", len(s.SchemaManager.List()))

	// Initialize collection config manager
	s.CollectionManager = NewCollectionManager(db)
	if err := s.CollectionManager.EnsureBucket(); err != nil {
		logging.Fatal("startup step failed", "step", "CollectionManager.EnsureBucket", "err", err)
	}
	if err := s.CollectionManager.LoadAll(); err != nil {
		logging.Fatal("startup step failed", "step", "CollectionManager.LoadAll", "err", err)
	}

	// GO-021: build the per-collection storage backends the configs ask for.
	// Until this ran, `storageBackend: "s3"` was accepted, validated and
	// ignored — every document went to the local file regardless.
	s.InitStorageBackends()
	slog.Info("Collection manager initialized (collections configured)", "listAllCount", len(s.CollectionManager.ListAll()))

	// At-rest encryption (ISO 27001 A.8.24 / SOC 2 CC6.7). The encryptor
	// is a no-op when MDDB_ENCRYPTION_KEY is unset; a misconfigured key
	// fatals to avoid silently storing plaintext for a collection that
	// was explicitly opted in.
	enc, err := encryption.NewEncryptor()
	if err != nil {
		logging.Fatal("encryption init", "err", err)
	}
	s.Encryptor = enc
	SetGlobalEncryptor(enc)
	s.CollectionManager.SetEncryptor(enc)
	if enc.Enabled() {
		encCount := 0
		for _, cfg := range s.CollectionManager.ListAll() {
			if cfg != nil && cfg.Encrypted {
				encCount++
			}
		}
		slog.Info("At-rest encryption enabled (collection(s) opted in)", "encCount", encCount)
		slog.Info("Encryption primary", "primaryKeyID", enc.PrimaryKeyID(), "previousKeyIDsCount", len(enc.PreviousKeyIDs()))
		s.RotationManager = NewRotationManager(s, enc)
	} else {
		// If any collection is flagged as encrypted but we have no key,
		// refuse to start — writing plaintext into a supposedly encrypted
		// collection is a silent compliance failure.
		for name, cfg := range s.CollectionManager.ListAll() {
			if cfg != nil && cfg.Encrypted {
				logging.Fatal("collection has encrypted=true but MDDB_ENCRYPTION_KEY is not set", "name", name)
			}
		}
	}

	// Curation manager: loads all pinning/hiding rules into memory so the
	// search hot path never hits disk.
	s.CurationManager = NewCurationManager(db)
	if err := s.CurationManager.EnsureBucket(); err != nil {
		logging.Fatal("startup step failed", "step", "CurationManager.EnsureBucket", "err", err)
	}
	if err := s.CurationManager.LoadAll(); err != nil {
		logging.Fatal("startup step failed", "step", "CurationManager.LoadAll", "err", err)
	}
	slog.Info("Curation manager initialized (rules loaded)", "listAllCount", len(s.CurationManager.ListAll()))

	// Initialize temporal event tracking (disabled by default; set MDDB_TEMPORAL=true to enable)
	if env("MDDB_TEMPORAL", "false") == "true" {
		s.TemporalManager = temporal.NewTemporalManager(db)
		if err := s.TemporalManager.EnsureBuckets(); err != nil {
			logging.Fatal("startup step failed", "step", "TemporalManager.EnsureBuckets", "err", err)
		}
		s.TemporalManager.Start()
		slog.Info("Temporal event tracking initialized")
	}

	// Initialize spell checker (disabled by default; set MDDB_SPELL=true to enable)
	if env("MDDB_SPELL", "false") == "true" {
		s.SpellManager = spell.NewSpellManager(db)
		if err := s.SpellManager.EnsureBucket(); err != nil {
			logging.Fatal("startup step failed", "step", "SpellManager.EnsureBucket", "err", err)
		}
		s.SpellManager.LoadAll() // async — sets ready flag when done
		slog.Info("Spell manager initialized (loading dictionaries in background)")
	}

	// Initialize SSE hub (enabled by default, set MDDB_SSE_ENABLED=false to disable)
	sseEnabled := env("MDDB_SSE_ENABLED", "true") != "false"
	sseMaxClients := envconf.Int("MDDB_SSE_MAX_CLIENTS", 1000)
	sseMaxPerIP := envconf.Int("MDDB_SSE_MAX_PER_IP", 5)
	s.SSEHub = NewSSEHub(sseEnabled, sseMaxClients, sseMaxPerIP)
	if sseEnabled {
		slog.Info("SSE event stream enabled (max clients, max per IP)", "sseMaxClients", sseMaxClients, "sseMaxPerIP", sseMaxPerIP)
	}

	// Store MCP server info for handlers
	s.MCPInfo = srvCfg.MCP.ServerInfo
	s.MCPInstructions = srvCfg.MCP.Instructions

	// Initialize MCP API key store (BoltDB-backed, persists across restarts)
	s.mcpKeyStore = newMCPAPIKeyStore(s.DB)

	// Initialize metrics (enabled by default, set MDDB_METRICS=false to disable)
	metricsEnabled := env("MDDB_METRICS", "true") != "false"
	s.Metrics = metrics.NewMetrics(metricsEnabled, &serverMetricsStats{s: s})
	if metricsEnabled {
		slog.Info("Prometheus metrics enabled (GET /metrics)")
	}

	// Wire metrics into subsystems
	if s.EmbeddingWorker != nil {
		s.EmbeddingWorker.metrics = s.Metrics
	}
	if s.WebhookManager != nil {
		s.WebhookManager.SetMetrics(s.Metrics)
	}

	// Initialize replication
	s.ReplicationRole = env("MDDB_REPLICATION_ROLE", "") // "leader", "follower", ""
	s.NodeID = env("MDDB_NODE_ID", "")
	if s.NodeID == "" && s.ReplicationRole != "" {
		s.NodeID = fmt.Sprintf("node-%d", time.Now().UnixNano()%100000)
	}

	// Force follower to read-only mode
	if s.ReplicationRole == "follower" {
		s.Mode = ModeRead
		slog.Info("Replication follower mode — forced read-only")
	}

	// Initialize binlog (auto-enabled for leader, opt-in for standalone)
	binlogEnabled := env("MDDB_BINLOG_ENABLED", "false") == "true" || s.ReplicationRole == "leader"
	if binlogEnabled {
		bl, err := binlog.NewBinlog(dbPath, binlog.BinlogConfig{
			Path:    env("MDDB_BINLOG_PATH", ""),
			MaxSize: 256 * 1024 * 1024, // 256MB
			MaxAge:  24 * time.Hour,
		})
		if err != nil {
			logging.Fatal("Failed to initialize binlog", "err", err)
		}
		s.Binlog = bl
		slog.Info("binlog enabled", "currentLSN", bl.CurrentLSN())
	}

	// Set binlog on all subsystems
	if s.Binlog != nil {
		s.VectorStore.SetBinlog(s.Binlog)
		s.GeoStore.SetBinlog(s.Binlog)
		s.FTSIndex.SetBinlog(s.Binlog)
		s.TTLManager.SetBinlog(s.Binlog)
		s.WebhookManager.SetBinlog(s.Binlog)
		s.SchemaManager.SetBinlog(s.Binlog)
		s.SynonymManager.SetBinlog(s.Binlog)
		s.StopWordManager.SetBinlog(s.Binlog)
		s.AutomationManager.SetBinlog(s.Binlog)
		s.CollectionManager.SetBinlog(s.Binlog)
		if s.TemporalManager != nil {
			s.TemporalManager.SetBinlog(s.Binlog)
		}
		if s.SpellManager != nil {
			s.SpellManager.SetBinlog(s.Binlog)
		}
	}

	// Follower: disable background writers (data comes from binlog)
	if s.ReplicationRole == "follower" {
		s.TTLManager.Stop() // don't run TTL cleanup, leader handles it
		s.TTLManager = ttl.NewTTLManager(db, serverTTLReaper{s: s})
		_ = s.TTLManager.EnsureBuckets()
		// Don't start cleanup — deletes come from binlog

		// Don't start embedding worker — embeddings come from leader
		if s.EmbeddingWorker != nil {
			s.EmbeddingWorker.Stop()
			s.EmbeddingWorker = nil
		}
		slog.Info("Replication disabled TTL cleanup and embedding worker on follower")
	}

	// Initialize cron scheduler (if enabled)
	if automationsEnabled && env("MDDB_CRONS", "false") == "true" {
		s.CronScheduler = NewCronScheduler(s)
		s.CronScheduler.Start()
		s.CronScheduler.Reload()
		slog.Info("Cron scheduler started")
	}

	// Initialize authentication (disabled by default)
	authEnabled := env("MDDB_AUTH_ENABLED", "false") == "true"
	if authEnabled {
		jwtSecret := env("MDDB_AUTH_JWT_SECRET", "")
		if jwtSecret == "" {
			logging.Fatal("MDDB_AUTH_ENABLED=true requires MDDB_AUTH_JWT_SECRET to be set")
		}

		jwtExpiryStr := env("MDDB_AUTH_JWT_EXPIRY", "24h")
		jwtExpiry, err := time.ParseDuration(jwtExpiryStr)
		if err != nil {
			logging.Fatal("Invalid MDDB_AUTH_JWT_EXPIRY", "err", err)
		}

		s.AuthManager = NewAuthManager(db, AuthConfig{
			JWTSecret:     jwtSecret,
			JWTExpiry:     jwtExpiry,
			AdminUsername: env("MDDB_AUTH_ADMIN_USERNAME", "admin"),
			AdminPassword: env("MDDB_AUTH_ADMIN_PASSWORD", ""),
		})

		if err := s.AuthManager.EnsureBuckets(); err != nil {
			logging.Fatal("startup step failed", "step", "AuthManager.EnsureBuckets", "err", err)
		}
		if err := s.AuthManager.LoadAll(); err != nil {
			logging.Fatal("startup step failed", "step", "AuthManager.LoadAll", "err", err)
		}
		if err := s.AuthManager.BootstrapAdmin(); err != nil {
			logging.Fatal("startup step failed", "step", "AuthManager.BootstrapAdmin", "err", err)
		}

		slog.Info("Authentication enabled")
	}
	if s.AuthManager != nil {
		s.AuthManager.SetServer(s)
	}

	// Initialize the shared HTTP+gRPC rate limiter (opt-in via
	// MDDB_RATE_LIMIT_ENABLED). Both transports consume the same
	// per-client budget. Wire the webhook publisher so rejections
	// surface as security.rate_limit_exceeded events.
	s.RateLimiter = NewRateLimiter()
	s.RateLimiter.SetWebhookManager(s.WebhookManager)

	// Enforce ISO 27001 / SOC 2 guardrails when MDDB_PRODUCTION=true,
	// or log a one-shot warning otherwise.
	EnforceProductionGuards(slog.Warn, logging.Fatal)

	// MCP stdio mode — replaces normal HTTP/gRPC operation
	if srvCfg.MCP.Stdio {
		s.runMCPStdio()
		return
	}

	// Log protocol configuration
	slog.Info("Protocol config HTTP MCP HTTP3", "enabled", srvCfg.HTTP.Enabled, "enabled2", srvCfg.GRPC.Enabled, "enabled3", srvCfg.MCP.Enabled, "enabled4", srvCfg.HTTP3.Enabled)
	// Log per-protocol mode overrides
	if srvCfg.HTTP.Mode != "" {
		slog.Info("Per-protocol mode API (MDDB_API_MODE)", "mode", srvCfg.HTTP.Mode)
	}
	if srvCfg.GRPC.Mode != "" {
		slog.Info("Per-protocol mode (MDDB_GRPC_MODE)", "mode", srvCfg.GRPC.Mode)
	}
	if srvCfg.MCP.Mode != "" {
		slog.Info("Per-protocol mode MCP (MDDB_MCP_MODE)", "mode", srvCfg.MCP.Mode)
	}
	if srvCfg.HTTP3.Mode != "" {
		slog.Info("Per-protocol mode HTTP3 (MDDB_HTTP3_MODE)", "mode", srvCfg.HTTP3.Mode)
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux, authEnabled)

	// GraphQL endpoint (disabled by default)
	graphqlEnabled := env("MDDB_GRAPHQL_ENABLED", "true") != "false"
	if graphqlEnabled {
		graphqlHandler := s.newGraphQLHandler()

		// Wrap with auth middleware if enabled
		if authEnabled && s.AuthManager != nil {
			graphqlHandler = s.GraphQLAuthMiddleware(graphqlHandler)
		}

		mux.Handle("/graphql", graphqlHandler)
		slog.Info("GraphQL endpoint enabled at /graphql")

		// GraphQL Playground (development tool)
		if env("MDDB_GRAPHQL_PLAYGROUND", "true") == "true" {
			mux.Handle("/playground", newGraphQLPlaygroundHandler("/graphql"))
			slog.Info("GraphQL Playground enabled at /playground")
		}
	}

	httpAddr := srvCfg.HTTP.Addr
	grpcAddr := srvCfg.GRPC.Addr

	// Panel mode: internal (default) = CORS enabled, external = CORS disabled (panel proxies)
	panelMode := env("MDDB_PANEL_MODE", "internal")

	// Wrap mux: CORS → panic-recover → rate limit → auth → metrics → max-body → JSON → routes
	handler := withJSON(mux)
	// SEC-005: cap request body size globally so a single oversized JSON body
	// can't OOM the process. Upload/import endpoints legitimately stream large
	// files and manage their own limits, so they are exempt.
	handler = withMaxBody(
		int64(envconf.Int("MDDB_MAX_BODY_BYTES", 32<<20)), // 32MB default
		map[string]bool{"/v1/upload": true, "/v1/import-wiki": true},
		handler,
	)
	handler = s.Metrics.Middleware(handler)
	if authEnabled && s.AuthManager != nil {
		handler = s.AuthManager.HTTPMiddleware(handler)
	}
	if s.RateLimiter.Enabled() {
		handler = s.RateLimiter.HTTPMiddleware(handler)
	}
	// Panic recovery is the outermost concern so every other
	// middleware is shielded — a crash in auth/rate-limit/etc.
	// becomes an ops.panic_recovered event, not a process kill.
	handler = PanicRecoveryMiddleware(s.WebhookManager, handler)
	// GO-028: one line per request, off unless asked for. It wraps outside
	// panic recovery so a recovered panic still reports its 500.
	if envconf.String("MDDB_ACCESS_LOG", "false") == "true" {
		handler = withAccessLog(handler)
		slog.Info("access logging enabled")
	}
	if panelMode != "external" {
		handler = withCORS(handler)
		// SEC-008: a wildcard CORS policy lets any website read responses from a
		// user's browser. Warn so operators set MDDB_CORS_ORIGINS for non-public
		// deployments.
		if envCORSConfig().wildcard {
			if s.AuthManager != nil && s.AuthManager.enabled {
				slog.Warn("CORS is wildcard (*) with auth enabled — any origin can attempt "+
					"credentialed cross-origin reads. Set MDDB_CORS_ORIGINS to an allowlist.",
					"finding", "SEC-008")
			} else {
				slog.Warn("CORS is wildcard (*) — any website can read this instance from a user's "+
					"browser. Set MDDB_CORS_ORIGINS to an allowlist for non-public deployments.",
					"finding", "SEC-008")
			}
		}
	}
	if panelMode == "external" {
		slog.Info("Panel mode external (CORS disabled, panel proxies requests)")
	}

	// Shut down early health server before starting the main one
	if earlyServer != nil {
		_ = earlyServer.Close()
		time.Sleep(50 * time.Millisecond) // brief pause to release port
	}

	// Start async bulk ingest manager — recovers orphan jobs from previous run
	// and spins up the single worker that drains the in-memory queue.
	s.BulkIngest = NewBulkIngestManager(s, 64)
	s.BulkIngest.Start()

	// Mark server as ready — health check will now return "healthy" instead of "warming_up"
	s.Ready = true
	slog.Info("Server initialization complete — ready to serve")

	// Start HTTP server (with optional TLS). httpAddr may be a TCP host:port
	// or a Unix Domain Socket (unix:/path/to/sock) — see listen_addr.go.
	if srvCfg.HTTP.Enabled {
		go func() {
			server := &http.Server{
				Addr:              httpAddr,
				Handler:           handler,
				ReadHeaderTimeout: 10 * time.Second,
				ReadTimeout:       30 * time.Second,
				WriteTimeout:      30 * time.Second,
				IdleTimeout:       120 * time.Second,
			}
			lis, err := openListener(httpAddr)
			if err != nil {
				logging.Fatal("startup step failed", "step", "openListener", "err", err)
			}
			defer func() { _ = closeListener(lis, httpAddr) }()
			tlsOn := srvCfg.TLS.Enabled && srvCfg.TLS.CertFile != "" && srvCfg.TLS.KeyFile != ""
			if tlsOn && isUnixAddr(httpAddr) {
				slog.Info("mddb TLS ignored on UDS listener (filesystem perms authenticate the peer)", "httpAddr", httpAddr)
				tlsOn = false
			}
			if tlsOn {
				tlsCfg, terr := buildServerTLSConfig(srvCfg.TLS)
				if terr != nil {
					logging.Fatal("TLS config", "terr", terr)
				}
				server.TLSConfig = tlsCfg
				mtls := ""
				if srvCfg.TLS.ClientCAFile != "" {
					mode := srvCfg.TLS.ClientAuth
					if mode == "" {
						mode = "require"
					}
					mtls = fmt.Sprintf(", mtls=on (clientAuth=%s)", mode)
				}
				slog.Info("mddb HTTPS listening (,, tls=on)", "httpAddr", httpAddr, "mode", s.Mode, "dbPath", dbPath, "mtls", mtls)
				if err := server.ServeTLS(lis, "", ""); err != nil && err != http.ErrServerClosed {
					logging.Fatal("startup step failed", "step", "server.ServeTLS", "err", err)
				}
			} else {
				slog.Info("mddb HTTP listening", "httpAddr", httpAddr, "mode", s.Mode, "dbPath", dbPath)
				if err := server.Serve(lis); err != nil && err != http.ErrServerClosed {
					logging.Fatal("startup step failed", "step", "server.Serve", "err", err)
				}
			}
		}()
	} else {
		slog.Info("HTTP server disabled")
	}

	// Start MCP HTTP server on its own port
	if srvCfg.MCP.Enabled {
		go func() {
			mcpMux := http.NewServeMux()
			mcpSrv := s.newMCPHTTPServer()
			mcpMux.HandleFunc("/resources", mcpSrv.handleResources)
			mcpMux.HandleFunc("/resources/read", mcpSrv.handleResourceRead)
			mcpMux.HandleFunc("/tools", mcpSrv.handleTools)
			mcpMux.HandleFunc("/tools/call", s.guardWrite(mcpSrv.handleToolCall))
			mcpMux.HandleFunc("/events", s.handleSSE)

			// MCP-over-SSE transport (legacy, 2024-11-05 spec)
			mcpSSEHandler := NewMCPHandlerWithConfig(NewDirectClient(s), loadMCPCustomTools(), srvCfg.MCP.ServerInfo, srvCfg.MCP.Instructions, s.Mode, srvCfg.MCP.Mode)
			mcpSSE := NewMCPSSETransport(mcpSSEHandler)
			mcpMux.HandleFunc("/sse", mcpSSE.HandleSSE)
			mcpMux.HandleFunc("/message", mcpSSE.HandleMessage)
			slog.Info("MCP-over-SSE transport enabled at /sse + /message (legacy)")

			// Streamable HTTP transport (2025-11-25 spec)
			mcpStreamableHandler := NewMCPHandlerWithConfig(NewDirectClient(s), loadMCPCustomTools(), srvCfg.MCP.ServerInfo, srvCfg.MCP.Instructions, s.Mode, srvCfg.MCP.Mode)
			mcpStreamable := NewMCPStreamableTransport(mcpStreamableHandler)
			mcpMux.HandleFunc("/mcp", mcpStreamable.Handle)
			slog.Info("MCP Streamable HTTP transport enabled at /mcp")

			// MCP middleware chain: CORS → API Key Auth → Rate Limit → Request Logging → JSON → Routes
			var mcpHandler http.Handler = mcpMux
			mcpHandler = withJSON(mcpHandler)

			mcpLogger := NewMCPRequestLogger()
			mcpHandler = mcpLogger.Wrap(mcpHandler)

			mcpRateLimiter := NewMCPRateLimiter()
			mcpHandler = mcpRateLimiter.Wrap(mcpHandler)

			mcpAuth := NewMCPAPIKeyMiddleware()
			mcpAuth.SetKeyStore(s.mcpKeyStore)
			s.mcpAuth = mcpAuth
			mcpHandler = mcpAuth.Wrap(mcpHandler)

			// SEC-002: tie MCP exposure to the main auth config. When
			// MDDB_AUTH_ENABLED=true and MCP has no key auth of its own, gate
			// the listener with the main AuthManager so it can't be an
			// anonymous full-R/W bypass; warn loudly if MCP is unauthenticated
			// and bound beyond loopback.
			mcpHandler = s.applyMCPAuth(mcpHandler, srvCfg.MCP.Addr)

			if panelMode != "external" {
				mcpHandler = withCORS(mcpHandler)
			}

			slog.Info("mddb MCP HTTP listening", "addr", srvCfg.MCP.Addr)
			server := &http.Server{
				Addr:              srvCfg.MCP.Addr,
				Handler:           mcpHandler,
				ReadHeaderTimeout: 10 * time.Second,
				ReadTimeout:       30 * time.Second,
				WriteTimeout:      30 * time.Second,
				IdleTimeout:       120 * time.Second,
			}
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logging.Fatal("startup step failed", "step", "server.ListenAndServe", "err", err)
			}
		}()
	} else {
		slog.Info("MCP server disabled")
	}

	// Start HTTP/3 server
	if srvCfg.HTTP3.Enabled {
		go func() {
			slog.Info("mddb HTTP/3 listening", "addr", srvCfg.HTTP3.Addr)
			h3Server, err := NewHTTP3Server(srvCfg.HTTP3.Addr, HTTP3Middleware(handler))
			if err != nil {
				slog.Warn("Failed to start HTTP/3 server", "err", err)
				return
			}
			// Kept on the server so shutdown can close the listener; it was
			// a local until now, which made the UDP socket unclosable (GO-034).
			s.HTTP3 = h3Server
			if err := h3Server.Start(); err != nil {
				slog.Warn("HTTP/3 server error", "err", err)
			}
		}()
	}

	// Start replication client (follower mode)
	var replClient *ReplicationClient
	if s.ReplicationRole == "follower" {
		leaderAddr := env("MDDB_REPLICATION_LEADER_ADDR", "")
		if leaderAddr == "" {
			logging.Fatal("MDDB_REPLICATION_ROLE=follower requires MDDB_REPLICATION_LEADER_ADDR")
		}
		replClient = NewReplicationClient(s, ReplicationClientConfig{
			LeaderAddr: leaderAddr,
			FollowerID: s.NodeID,
		})
		s.replClient = replClient
		replClient.Start()
		defer replClient.Stop()
		slog.Info("Replication client started", "leaderAddr", leaderAddr, "nodeID", s.NodeID)

		// Monitor replication lag and fire ops.replication_lag_high
		// when it crosses the threshold.
		s.LagMonitor = NewReplicationLagMonitor(s.WebhookManager, replClient)
		s.LagMonitor.Start()
		defer s.LagMonitor.Stop()
	}

	if s.ReplicationRole == "leader" {
		slog.Info("replication leader started", "nodeID", s.NodeID, "currentLSN", s.Binlog.CurrentLSN())
	}

	// Close binlog on shutdown
	if s.Binlog != nil {
		defer func() { _ = s.Binlog.Close() }()
	}

	// Stop automation log cleanup on shutdown
	if s.AutomationLogStore != nil {
		defer s.AutomationLogStore.Stop()
	}

	// Flush pending audit events on shutdown
	if s.AuditManager != nil {
		defer s.AuditManager.Stop()
	}

	// Stop the disk-usage monitor; LagMonitor is stopped adjacent to
	// the replication client in the follower branch above.
	if s.DiskMonitor != nil {
		defer s.DiskMonitor.Stop()
	}

	// Start gRPC server
	if srvCfg.GRPC.Enabled {
		var grpcOpts []grpc.ServerOption
		var unaryChain []grpc.UnaryServerInterceptor
		var streamChain []grpc.StreamServerInterceptor
		if s.RateLimiter.Enabled() {
			unaryChain = append(unaryChain, s.RateLimiter.UnaryInterceptor())
			streamChain = append(streamChain, s.RateLimiter.StreamInterceptor())
		}
		if authEnabled && s.AuthManager != nil {
			unaryChain = append(unaryChain, s.AuthManager.GRPCUnaryInterceptor())
			// SEC-003: streaming RPCs (Export, replication) must be
			// authenticated too — and claims injected so stream handlers'
			// CheckPermission sees them.
			streamChain = append(streamChain, s.AuthManager.GRPCStreamInterceptor())
		}
		if len(unaryChain) > 0 {
			grpcOpts = append(grpcOpts, grpc.ChainUnaryInterceptor(unaryChain...))
		}
		if len(streamChain) > 0 {
			grpcOpts = append(grpcOpts, grpc.ChainStreamInterceptor(streamChain...))
		}
		// TLS / mTLS — same buildServerTLSConfig as the HTTP listener.
		// Skipped on UDS (filesystem perms authenticate the local peer).
		grpcTLSLog := ""
		if !isUnixAddr(grpcAddr) {
			tlsCfg, terr := buildServerTLSConfig(srvCfg.TLS)
			if terr != nil {
				logging.Fatal("gRPC TLS config", "terr", terr)
			}
			if tlsCfg != nil {
				grpcOpts = append(grpcOpts, grpc.Creds(credentials.NewTLS(tlsCfg)))
				grpcTLSLog = ", tls=on"
				if srvCfg.TLS.ClientCAFile != "" {
					mode := srvCfg.TLS.ClientAuth
					if mode == "" {
						mode = "require"
					}
					grpcTLSLog = fmt.Sprintf(", tls=on, mtls=on (clientAuth=%s)", mode)
				}
			}
		}
		go func() {
			slog.Info("mddb gRPC listening", "grpcAddr", grpcAddr, "mode", s.Mode, "dbPath", dbPath, "grpcTLSLog", grpcTLSLog)
			if err := startGRPCServer(s, grpcAddr, grpcOpts...); err != nil {
				logging.Fatal("startup step failed", "step", "startGRPCServer", "err", err)
			}
		}()
	} else {
		slog.Info("gRPC server disabled")
	}

	// Block until signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	slog.Info("Received, shutting down...", "sig", sig)

	// GO-034: one ordered sequence rather than a handful of ad-hoc Closes.
	// Seven subsystems had a Stop or Close that nothing called, so their
	// queues were abandoned rather than drained on the way out.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout())
	defer cancel()
	s.Shutdown(shutdownCtx)
}

// --- helpers / buckets

func (s *Server) ensureBuckets() error {
	return s.DBUpdate(func(tx *bolt.Tx) error {
		_, _ = tx.CreateBucketIfNotExists(s.BucketNames.Docs)          // doc|collection|id -> json
		_, _ = tx.CreateBucketIfNotExists(s.BucketNames.IdxMeta)       // meta|collection|key|value|docID -> 1
		_, _ = tx.CreateBucketIfNotExists(s.BucketNames.Rev)           // rev|collection|docID|ts -> json
		_, _ = tx.CreateBucketIfNotExists(s.BucketNames.ByKey)         // bykey|collection|key|lang -> docID
		_, _ = tx.CreateBucketIfNotExists([]byte("embedding_configs")) // embedding model configurations
		_, _ = tx.CreateBucketIfNotExists(bucketBulkJobs)              // bulk ingest job tracking
		return nil
	})
}
