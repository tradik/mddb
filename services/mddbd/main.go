package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mddb/internal/audit"
	"mddb/internal/automationlog"
	"mddb/internal/binlog"
	"mddb/internal/boltx"
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
	"mddb/internal/migrate"
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

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// VERSION is the current release version of the MDDB server. It is a
// constant on purpose: scripts/bump-version.sh writes it and
// scripts/check-version.sh guards it, so builds take no -X flag (OPS-022);
// the release checks the artifact with `mddbd -version`.
const VERSION = "2.18.8"

// versionLine is what `mddbd -version` prints.
func versionLine() string { return "mddbd version " + VERSION }

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
	// UpdateStatus caches the startup release check (OPS-019). Nil when the
	// check is disabled or has not finished, so /health can distinguish "no
	// update" from "we have not looked".
	UpdateStatus *UpdateStatus
	// DetectedEmbedding records a provider found on this machine at startup
	// rather than configured (SRCH-001). Nil whenever the provider came from
	// the environment or from stored config, so /config can report where the
	// configuration actually came from instead of inferring it.
	DetectedEmbedding *embedding.DetectedProvider
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
	Gain               *GainRecorder            // AGT-004: tokens returned and avoided, per day
	Aliases            *AliasManager            // GO-044: alias → collection
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
	quotaOnce          sync.Once                // GO-043: builds quota on first use (see quotas())
	quota              *quotaState              // GO-043: disk reserve, vector RAM limit, usage cache, embedding budget
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
	freelistLoad    *backgroundFreelist       // #333: set while writes wait for the freelist
	collMu          sync.Mutex                // #336: guards collFiles
	collFiles       *collectionFiles          // #336: per-collection files, nil in the single-file layout
	vectorLoad      vectorLoadRef             // #346: the vector index load in progress
	summaryOnce     sync.Once                 // RAG-009: builds summaryIdx on first use
	summaryIdx      *summaryIndex             // RAG-009: the summary vectors, apart from every chunk index
	reindexJobs     reindexJobs               // #347: background vector reindexes
	walks           walkCache                 // #349: results of whole-database walks (stats, vector-stats, storage)
	dbProbe         dbProbe                   // #350: the /health transaction in flight
	// GO-021: both MCP transports count their open sessions and nothing ever
	// asked. A session that a client abandoned without closing stays until it
	// times out, so the count is how an operator sees leaks accumulating.
	// Assigned once before the listener goroutine starts, so /health can read
	// them without synchronisation.
	mcpSSE        *MCPSSETransport
	mcpStreamable *MCPStreamableTransport
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
	// ExpectedRevision makes the write conditional (INT-021): 409 unless
	// the document is at this revision. Empty = unconditional.
	ExpectedRevision string `json:"expectedRevision,omitempty"`
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
	FilterMeta map[string][]string `json:"filterMeta"`       // AND over keys, OR over values
	Filter     MetaFilter          `json:"filter,omitempty"` // operator filter (SRCH-017): prefix, contains, gt/gte/lt/lte, not, exists; ANDed with filterMeta
	Sort       string              `json:"sort"`             // addedAt|updatedAt|key
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

// freelistSyncNote: by default the freelist is not written on commit, which
// makes writes faster and makes every open rebuild it by walking every page —
// seconds on a warm SSD, reported at over 40 minutes cold on a busy spinning
// disk (#270), and the point where a damaged page tree panics.
// MDDB_FREELIST_SYNC=true writes it on every commit instead, so an open reads
// one page. The first open with it set still walks once, to write it.

// getOptimizedBoltOptions returns optimized BoltDB options for performance,
// for a database whose size is not known.
func getOptimizedBoltOptions() *bolt.Options { return boltOptionsFor("") }

// boltOptionsFor returns the options for the database at path; its size
// decides the freelist setting when MDDB_FREELIST_SYNC is not set.
func boltOptionsFor(path string) *bolt.Options {
	return &bolt.Options{
		Timeout:         2 * time.Second,
		NoFreelistSync:  !freelistSyncFor(path),                 // see freelistSyncNote
		FreelistType:    bolt.FreelistMapType,                   // Use hashmap for freelist (faster than array)
		NoGrowSync:      false,                                  // Sync after growing mmap (safer)
		InitialMmapSize: max(100*1024*1024, mmapHeadroom(path)), // 100MB, or room to grow for a large file (#350)
	}
}

// Startup seams (TEST-002). main() was one 1,040-line function that nothing
// could call, so the whole startup sat at 0.5% coverage. It is now a sequence
// of steps a test can run against a temporary database; these variables are
// the only places a test substitutes anything, and each holds the production
// behaviour by default.
var (
	// fatal logs and ends the process. A test swaps in a function that
	// records the message and stops only the calling goroutine, so it can
	// assert which startup step refused to continue.
	fatal = logging.Fatal
	// loadConfig reads CLI flags, environment and config file. Flags are
	// process-global and parse once, so a test hands serve its own config.
	loadConfig = loadServerConfig
	// shutdownSignals returns the channel serve waits on before shutting down.
	shutdownSignals = notifyShutdownSignals
)

// notifyShutdownSignals subscribes to SIGINT and SIGTERM.
func notifyShutdownSignals() chan os.Signal {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	return sigCh
}

// fatalOnErr ends the process with msg, the key/value pairs kv and the error
// when err is not nil.
func fatalOnErr(err error, msg string, kv ...any) {
	if err != nil {
		fatal(msg, append(kv, "err", err)...)
	}
}

// mustStep ends the process when the named startup step failed.
func mustStep(step string, err error) {
	fatalOnErr(err, "startup step failed", "step", step)
}

// ignoreServerClosed drops the error every listener returns on a deliberate
// close, which is not a failure.
func ignoreServerClosed(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// clientAuthMode is the mTLS client-auth policy as the startup log names it.
func clientAuthMode(cfg TLSConfig) string {
	if cfg.ClientAuth == "" {
		return "require"
	}
	return cfg.ClientAuth
}

func main() {
	os.Exit(run())
}

// run is the whole process: it returns the exit code main hands to os.Exit.
func run() int {
	// A child started by verifyDatabase: check one file and exit, before
	// anything else about this process is set up.
	if p := os.Getenv(verifyChildEnv); p != "" {
		return runVerifyChild(p)
	}

	// Install the structured logger before anything can log (GO-028).
	// MDDB_LOG_FORMAT picks text or json, MDDB_LOG_LEVEL the threshold; both
	// have defaults, so an unconfigured process still logs.
	logging.Setup()

	// Load server configuration (CLI flags > env vars > config file > defaults)
	return runServer(loadConfig())
}

// runServer starts the server described by srvCfg, blocks until a shutdown signal
// arrives, shuts down in order and returns the exit code.
func runServer(srvCfg ServerConfig) int {
	dbPath := srvCfg.Database.Path

	// GO-032: say plainly what durability this data directory can offer before
	// accepting a single write. bbolt fsyncs every commit, but that promise is
	// only worth what the storage under it is worth — a container started
	// without a volume acknowledges writes and loses them on removal.
	persistence := CheckPersistence(dbPath)
	logPersistenceStatus(persistence, slog.Warn)

	// #329: /health answers before anything slow happens, with the phase in
	// progress, and every phase logs its duration.
	clock := newStartupClock()
	var earlyServer *http.Server
	if srvCfg.HTTP.Enabled {
		earlyServer = clock.startEarlyHealth(srvCfg.HTTP.Addr)
	}
	clock.begin("opening database")
	db, freelistLoad := openServerDatabase(clock, dbPath)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("closing database", "err", err)
		}
	}()

	s := newServer(srvCfg, db, persistence, freelistLoad)
	s.checkCollectionLayout()
	defer s.closeCollectionFiles()

	clock.srv.Store(s)
	clock.begin("initialising indexes and managers")
	authEnabled := s.initSubsystems(clock)

	// Enforce ISO 27001 / SOC 2 guardrails when MDDB_PRODUCTION=true,
	// or log a one-shot warning otherwise.
	EnforceProductionGuards(slog.Warn, fatal)

	// MCP stdio mode — replaces normal HTTP/gRPC operation
	if srvCfg.MCP.Stdio {
		s.runMCPStdio()
		return 0
	}

	s.logProtocolConfig()

	mux := http.NewServeMux()
	s.registerRoutes(mux, authEnabled)
	s.registerGraphQL(mux, authEnabled)

	// Panel mode: internal (default) = CORS enabled, external = CORS disabled (panel proxies)
	panelMode := env("MDDB_PANEL_MODE", "internal")
	handler, httpReadTimeout, httpWriteTimeout := s.wrapHTTPHandler(mux, authEnabled, panelMode)

	// Shut down early health server before starting the main one
	clock.begin("starting listeners")
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
	clock.done()
	slog.Info("Server initialization complete — ready to serve")

	// OPS-019: after ready, and in the background. Someone running a release
	// with a security fix in it should not have to go looking to find that out,
	// and a GitHub request must never delay a data server's startup.
	_ = s.startUpdateCheck()

	s.startHTTPListener(handler, httpReadTimeout, httpWriteTimeout)
	s.startMCPListener(panelMode, httpReadTimeout, httpWriteTimeout)
	s.startHTTP3Listener(handler)

	// Start replication client (follower mode); stopped on the way out.
	defer s.startReplicationClient()()

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

	// Stop the disk-usage monitor; LagMonitor is stopped together with the
	// replication client above.
	if s.DiskMonitor != nil {
		defer s.DiskMonitor.Stop()
	}

	s.startGRPCListener(authEnabled)

	// Block until signal
	sigCh := shutdownSignals()
	sig := <-sigCh
	slog.Info("Received, shutting down...", "sig", sig)
	go exitOnSecondSignal(sigCh) // #350

	// GO-034: one ordered sequence rather than a handful of ad-hoc Closes.
	// Seven subsystems had a Stop or Close that nothing called, so their
	// queues were abandoned rather than drained on the way out.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout())
	defer cancel()
	s.finishShutdown(shutdownCtx)
	return 0
}

// openServerDatabase opens the main database file, ending the process when it
// cannot be opened (#333, #334).
func openServerDatabase(clock *startupClock, dbPath string) (*bolt.DB, *backgroundFreelist) {
	openStart := time.Now()
	db, walked, freelistLoad, err := openDatabase(clock, dbPath, boltOptionsFor(dbPath))
	warnSlowOpen(walked, time.Since(openStart))
	if err != nil {
		fatal("startup step failed", "step", "bolt.Open", "err", err,
			"remedy", "if the file is damaged: mddbd -verify-db FILE, then mddbd -repair-db FILE -repair-to NEWFILE (docs/BACKUP.md)")
	}
	// DOC-017: the file is brought to this build's schema before anything
	// reads or writes it.
	clock.begin("migrating storage schema")
	if err := migrate.Run(db); err != nil {
		fatal("startup step failed", "step", "schema migration", "err", err, "remedy", "docs/STORAGE-LAYOUT.md#schema-version")
	}
	return db, freelistLoad
}

// warnSlowOpen explains an open that spent d walking the file for its freelist.
func warnSlowOpen(walked bool, d time.Duration) {
	if walked && d > 30*time.Second {
		slog.Warn("opening the database walked every page to rebuild the freelist",
			"elapsed", d.Round(time.Second),
			"remedy", "a clean shutdown (SIGTERM) stores the freelist for the next start; MDDB_FREELIST_SYNC=true stores it on every commit, so a crash does not cost a walk either (docs/STORAGE-TUNING.md)")
	}
}

// newServer builds the server around an open database, with the caches and
// performance helpers every configuration has.
func newServer(srvCfg ServerConfig, db *bolt.DB, persistence PersistenceStatus, freelistLoad *backgroundFreelist) *Server {
	s := &Server{
		DB:           db,
		Path:         srvCfg.Database.Path,
		Mode:         srvCfg.Database.Mode,
		freelistLoad: freelistLoad,
		Config:       srvCfg,
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
		// Extreme mode = HTTP/3 enabled (legacy MDDB_EXTREME mapped in config)
		UseExtreme: srvCfg.HTTP3.Enabled,
	}
	s.IndexQueue.SetStore(serverIndexStore{s: s}) // wire persistence (server now ready)
	freelistLoad.start()                          // #333: writes wait for it, reads do not
	return s
}

// checkCollectionLayout enforces the per-collection layout (#336): the main
// file must hold no collection data, and every collection file opens now, in
// parallel.
func (s *Server) checkCollectionLayout() {
	if !perCollectionLayout() {
		return
	}
	if held, err := mainFileHoldsCollections(s.DB); err != nil || held {
		fatal("startup step failed", "step", "per-collection layout",
			"err", fmt.Errorf("the main file still holds collection data (%v)", err),
			"remedy", "stop the server and run mddbd -split-collections "+s.Path+" (docs/STORAGE-LAYOUT.md)")
	}
	if s.collectionStore() == nil {
		fatal("startup step failed", "step", "opening per-collection files", "dir", collectionDir(s.Path))
	}
}

// initSubsystems builds every index and manager in dependency order and
// reports whether authentication is enabled.
func (s *Server) initSubsystems(clock *startupClock) (authEnabled bool) {
	s.initExtreme()
	s.initSearchIndexes()
	s.initEmbedding()

	// RAG-007: say so when the stored vectors were produced differently from
	// how the configured provider produces them now. Reindexing is the
	// operator's call, so this names the collections and stops there.
	if msg := reportStaleEmbeddings(s.VectorStore, s.Embedding); msg != "" {
		slog.Warn("embedding provenance changed", "detail", msg)
	}

	// Load vectors into memory asynchronously
	go s.loadVectorIndex() // in the background; logs "vector index loaded" with its duration
	clock.begin("loading geo indexes")
	go s.loadGeoIndexes()

	s.initAudit()
	s.initTTL(clock)

	// AGT-004: what retrieval saved its callers, counted per day.
	s.Gain = NewGainRecorder(func() *bolt.DB { return s.DB }) // a restore swaps s.DB
	s.Gain.Start()

	// Configure compression
	srvCfg := s.Config
	compression.ConfigureCompression(srvCfg.Compression.Enabled, srvCfg.Compression.SmallThreshold, srvCfg.Compression.MediumThreshold)
	if !srvCfg.Compression.Enabled {
		slog.Info("Document compression disabled")
	}

	s.initFTS()
	s.initWebhooks()
	automationsEnabled := s.initAutomation()
	s.initSchema()
	s.initCollections(clock)
	s.initEncryption()
	s.initCuration()
	s.initTemporal()
	s.initSpell()
	s.initEventsAndMetrics()
	s.initReplication()

	// Initialize cron scheduler (if enabled)
	if automationsEnabled && env("MDDB_CRONS", "false") == "true" {
		s.CronScheduler = NewCronScheduler(s)
		s.CronScheduler.Start()
		s.CronScheduler.Reload()
		slog.Info("Cron scheduler started")
	}

	authEnabled = s.initAuth(clock)

	// Initialize the shared HTTP+gRPC rate limiter (opt-in via
	// MDDB_RATE_LIMIT_ENABLED). Both transports consume the same
	// per-client budget. Wire the webhook publisher so rejections
	// surface as security.rate_limit_exceeded events.
	s.RateLimiter = NewRateLimiter()
	s.RateLimiter.SetWebhookManager(s.WebhookManager)
	return authEnabled
}

// initExtreme starts the extreme performance features (HTTP/3 mode).
func (s *Server) initExtreme() {
	if !s.UseExtreme {
		return
	}
	slog.Info("Extreme Performance Mode ENABLED")

	// Initialize WAL
	wal, err := NewWAL(s.Path, SyncPeriodic)
	fatalOnErr(err, "Failed to initialize WAL")
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

// initSearchIndexes creates the document buckets and the vector and geo
// stores and indexes.
func (s *Server) initSearchIndexes() {
	mustStep("ensureBuckets", s.ensureBuckets())

	// Initialize vector search
	s.VectorStore = vector.NewVectorStore(s.DB)
	s.VectorStore.SetRouter(s.router()) // #336
	mustStep("VectorStore.EnsureBucket", s.VectorStore.EnsureBucket())
	s.VectorIndex = vector.NewVectorIndex()

	// Initialize geo search. Startup rebuild from BoltDB happens asynchronously
	// below so mddbd can start serving non-geo requests immediately.
	// Both indexes share the same "geo" bucket in BoltDB — they're two views
	// of the same underlying data, picked by the /v1/geo-search?algorithm=...
	// parameter at query time.
	s.GeoStore = geo.NewGeoStore(s.DB)
	s.GeoStore.SetRouter(s.router()) // #336
	mustStep("GeoStore.EnsureBucket", s.GeoStore.EnsureBucket())
	s.GeoIndex = geo.NewGeoIndex()
	s.GeoHashIndex = geo.NewGeoHashIndex()
	bqRerank := s.Config.Vector.BQRerankFactor
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
	s.VectorSearchers = newVectorSearchers(s.VectorIndex, s.QuantizedVecIndex, bqRerank)
}

// initEmbedding picks the embedding provider: stored config first, then the
// environment, then a model already running on this machine.
func (s *Server) initEmbedding() {
	// Try to load embedding config from database first
	defaultConfig, err := s.GetDefaultEmbeddingConfig()
	if err == nil && defaultConfig != nil {
		// Use stored configuration
		s.InitializeEmbeddingFromConfig(defaultConfig)
		slog.Info("vector search enabled", "source", "stored config",
			"provider", defaultConfig.Provider, "model", defaultConfig.Model, "dimensions", defaultConfig.Dimensions)
		return
	}
	// Fall back to environment variables
	s.Embedding = embedding.NewProvider()
	if s.Embedding != nil {
		s.startEmbeddingWorker(s.Embedding)
		slog.Info("vector search enabled", "source", "environment",
			"provider", s.Embedding.Model(), "model", s.Embedding.Model(), "dimensions", s.Embedding.Dimensions())
	} else if detected := embedding.DetectLocalProvider(context.Background()); detected != nil {
		// SRCH-001: nothing configured, but this machine is already
		// running a model. Most installs with no semantic search have one
		// sitting there — MDDB simply never looked.
		s.Embedding = detected.Provider
		s.DetectedEmbedding = detected
		s.startEmbeddingWorker(s.Embedding)
		slog.Info("vector search enabled", "source", "autodetected",
			"provider", detected.Name, "model", detected.Model,
			"dimensions", detected.Dimensions, "url", detected.APIURL,
			"note", "set MDDB_EMBEDDING_AUTODETECT=0 to disable this probe")
	} else {
		slog.Info("Vector search embedding provider not configured (set MDDB_EMBEDDING_PROVIDER or configure in panel)")
	}
}

// loadGeoIndexes loads the geo indexes from BoltDB. Both R-tree and geohash
// are populated from the same "geo" bucket so the /v1/geo-search algorithm
// switch is a pure runtime decision — no per-algorithm persistence layers.
// Handlers reject requests with 503 while !IsReady, matching the vector-index
// startup contract.
func (s *Server) loadGeoIndexes() {
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
}

// initAudit starts the audit log (ISO 27001 A.8.15 / SOC 2 CC7.2) and its
// optional external sinks.
func (s *Server) initAudit() {
	auditEnabled := env("MDDB_AUDIT_ENABLED", "false") == "true"
	auditRetention := 90
	if v := env("MDDB_AUDIT_RETENTION_DAYS", ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			auditRetention = n
		}
	}
	s.AuditManager = audit.NewAuditManager(s.DB, auditEnabled, auditRetention)
	s.AuditManager.FollowDB(func() *bolt.DB { return s.DB }) // a restore swaps s.DB
	mustStep("AuditManager.EnsureBuckets", s.AuditManager.EnsureBuckets())
	s.AuditManager.Start()
	if !auditEnabled {
		return
	}
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

// initTTL starts the document TTL manager.
func (s *Server) initTTL(clock *startupClock) {
	s.TTLManager = ttl.NewTTLManager(s.DB, serverTTLReaper{s: s})
	s.TTLManager.SetRouter(s.router()) // #336
	clock.begin("starting managers")
	mustStep("TTLManager.EnsureBuckets", s.TTLManager.EnsureBuckets())
	s.TTLManager.StartCleanup(30 * time.Second)
	slog.Info("TTL manager started (cleanup every 30s)")
}

// initFTS builds the full-text index with its languages, synonyms and stop
// words.
func (s *Server) initFTS() {
	srvCfg := s.Config
	s.FTSIndex = fts.NewFTSIndex(s.DB)
	s.FTSIndex.SetRouter(s.router()) // #336
	mustStep("FTSIndex.EnsureBuckets", s.FTSIndex.EnsureBuckets())

	// Initialize multi-language FTS support
	langReg := fts.NewLangRegistry(srvCfg.FTS.DefaultLang)
	if srvCfg.FTS.StemmingEnabled {
		fts.RegisterDefaultLanguages(langReg)
		s.FTSIndex.SetStemmer(fts.NewPorterStemmer())
		s.FTSIndex.SetLangRegistry(langReg)
		slog.Info("FTS stemming enabled", "languages", len(langReg.Languages()), "defaultLang", srvCfg.FTS.DefaultLang)
	}

	// Initialize synonym manager
	s.SynonymManager = fts.NewSynonymManager(s.DB)
	mustStep("SynonymManager.EnsureBucket", s.SynonymManager.EnsureBucket())
	mustStep("SynonymManager.LoadAll", s.SynonymManager.LoadAll())
	if srvCfg.FTS.SynonymsEnabled {
		s.FTSIndex.SetSynonymManager(s.SynonymManager)
		slog.Info("FTS synonyms enabled")
	}

	// Initialize stop word manager
	s.StopWordManager = fts.NewStopWordManager(s.DB)
	mustStep("StopWordManager.EnsureBucket", s.StopWordManager.EnsureBucket())
	mustStep("StopWordManager.LoadAll", s.StopWordManager.LoadAll())
	s.FTSIndex.SetStopWordManager(s.StopWordManager)
	s.StopWordManager.SetLangRegistry(langReg)
	slog.Info("Stop word manager initialized")

	// Initialize PMI data for PMISparse search
	s.FTSIndex.SetPMIData(fts.NewPMIData())

	slog.Info("Full-text search index initialized")
}

// initWebhooks loads the webhook subscriptions and the incident detectors
// that deliver security.* and ops.* events through them.
func (s *Server) initWebhooks() {
	s.WebhookManager = webhooks.NewWebhookManager(s.DB)
	mustStep("WebhookManager.EnsureBucket", s.WebhookManager.EnsureBucket())
	mustStep("WebhookManager.LoadAll", s.WebhookManager.LoadAll())
	slog.Info("webhook manager initialized", "hooks", len(s.WebhookManager.List()))

	// Incident detectors — reuse the WebhookManager to deliver
	// security.* and ops.* events to whichever hooks subscribed.
	s.AuthFailureTracker = NewAuthFailureTracker(s.WebhookManager)
	s.DiskMonitor = NewDiskUsageMonitor(s.WebhookManager, s.Path)
	s.DiskMonitor.Start()
}

// initAutomation loads triggers, crons and webhook targets, and their
// execution log, unless MDDB_AUTOMATIONS=disable. It reports whether
// automations are enabled.
func (s *Server) initAutomation() bool {
	if env("MDDB_AUTOMATIONS", "enable") == "disable" {
		return false
	}
	s.AutomationManager = NewAutomationManager(s.DB)
	s.AutomationManager.FollowDB(func() *bolt.DB { return s.DB }) // a restore swaps s.DB
	s.AutomationManager.SetServer(s)
	mustStep("AutomationManager.EnsureBucket", s.AutomationManager.EnsureBucket())
	mustStep("AutomationManager.LoadAll", s.AutomationManager.LoadAll())
	slog.Info("automation manager initialized", "rules", len(s.AutomationManager.List("")))

	// Initialize automation log store
	if env("MDDB_AUTOMATION_LOGS", "enable") == "disable" {
		return true
	}
	logTTLStr := env("MDDB_AUTOMATION_LOGS_TTL", "7d")
	logTTL, err := automationlog.ParseDurationString(logTTLStr)
	fatalOnErr(err, "Invalid MDDB_AUTOMATION_LOGS_TTL")
	s.AutomationLogStore = automationlog.NewStore(s.DB, logTTL)
	s.AutomationLogStore.FollowDB(func() *bolt.DB { return s.DB }) // a restore swaps s.DB
	mustStep("AutomationLogStore.EnsureBucket", s.AutomationLogStore.EnsureBucket())
	s.AutomationLogStore.StartCleanup(5 * time.Minute)
	s.AutomationManager.SetLogStore(s.AutomationLogStore)
	slog.Info("Automation logs enabled (TTL)", "logTTLStr", logTTLStr)
	return true
}

// initSchema loads the per-collection metadata schemas.
func (s *Server) initSchema() {
	s.SchemaManager = schema.NewSchemaManager(s.DB)
	mustStep("SchemaManager.EnsureBucket", s.SchemaManager.EnsureBucket())
	mustStep("SchemaManager.LoadAll", s.SchemaManager.LoadAll())
	slog.Info("Schema manager initialized (schemas loaded)", "listCount", len(s.SchemaManager.List()))
}

// initCollections loads the collection configs and the storage backends they
// ask for.
func (s *Server) initCollections(clock *startupClock) {
	s.CollectionManager = NewCollectionManager(s.DB)
	s.CollectionManager.FollowDB(func() *bolt.DB { return s.DB }) // a restore swaps s.DB
	mustStep("CollectionManager.EnsureBucket", s.CollectionManager.EnsureBucket())
	clock.begin("loading collection settings and storage backends")
	mustStep("CollectionManager.LoadAll", s.CollectionManager.LoadAll())

	// GO-021: build the per-collection storage backends the configs ask for.
	// Until this ran, `storageBackend: "s3"` was accepted, validated and
	// ignored — every document went to the local file regardless.
	s.InitStorageBackends()
	slog.Info("Collection manager initialized (collections configured)", "listAllCount", len(s.CollectionManager.ListAll()))

	// GO-044: aliases, loaded before any request can name one.
	s.Aliases = NewAliasManager(s.DB)
	s.Aliases.FollowDB(func() *bolt.DB { return s.DB }) // a restore swaps s.DB
	mustStep("AliasManager.LoadAll", s.Aliases.LoadAll())
}

// initEncryption sets up at-rest encryption (ISO 27001 A.8.24 / SOC 2
// CC6.7). The encryptor is a no-op when MDDB_ENCRYPTION_KEY is unset; a
// misconfigured key fatals to avoid silently storing plaintext for a
// collection that was explicitly opted in.
func (s *Server) initEncryption() {
	enc, err := encryption.NewEncryptor()
	fatalOnErr(err, "encryption init")
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
		return
	}
	// If any collection is flagged as encrypted but we have no key,
	// refuse to start — writing plaintext into a supposedly encrypted
	// collection is a silent compliance failure.
	for name, cfg := range s.CollectionManager.ListAll() {
		if cfg != nil && cfg.Encrypted {
			fatal("collection has encrypted=true but MDDB_ENCRYPTION_KEY is not set", "name", name)
		}
	}
}

// initCuration loads all pinning/hiding rules into memory so the search hot
// path never hits disk.
func (s *Server) initCuration() {
	s.CurationManager = NewCurationManager(s.DB)
	s.CurationManager.FollowDB(func() *bolt.DB { return s.DB }) // a restore swaps s.DB
	mustStep("CurationManager.EnsureBucket", s.CurationManager.EnsureBucket())
	mustStep("CurationManager.LoadAll", s.CurationManager.LoadAll())
	slog.Info("Curation manager initialized (rules loaded)", "listAllCount", len(s.CurationManager.ListAll()))
}

// initTemporal starts temporal event tracking (disabled by default; set
// MDDB_TEMPORAL=true to enable).
func (s *Server) initTemporal() {
	if env("MDDB_TEMPORAL", "false") != "true" {
		return
	}
	s.TemporalManager = temporal.NewTemporalManager(s.DB)
	s.TemporalManager.SetRouter(s.router()) // #336
	mustStep("TemporalManager.EnsureBuckets", s.TemporalManager.EnsureBuckets())
	s.TemporalManager.Start()
	slog.Info("Temporal event tracking initialized")
}

// initSpell starts the spell checker (disabled by default; set
// MDDB_SPELL=true to enable).
func (s *Server) initSpell() {
	if env("MDDB_SPELL", "false") != "true" {
		return
	}
	s.SpellManager = spell.NewSpellManager(s.DB)
	s.SpellManager.FollowDB(func() *bolt.DB { return s.DB }) // a restore swaps s.DB
	mustStep("SpellManager.EnsureBucket", s.SpellManager.EnsureBucket())
	s.SpellManager.LoadAll() // async — sets ready flag when done
	slog.Info("Spell manager initialized (loading dictionaries in background)")
}

// initEventsAndMetrics sets up the SSE hub, the MCP profile and key store,
// and the metrics collector, and wires metrics into the subsystems.
func (s *Server) initEventsAndMetrics() {
	// Initialize SSE hub (enabled by default, set MDDB_SSE_ENABLED=false to disable)
	sseEnabled := env("MDDB_SSE_ENABLED", "true") != "false"
	sseMaxClients := envconf.Int("MDDB_SSE_MAX_CLIENTS", 1000)
	sseMaxPerIP := envconf.Int("MDDB_SSE_MAX_PER_IP", 5)
	s.SSEHub = NewSSEHub(sseEnabled, sseMaxClients, sseMaxPerIP)
	if sseEnabled {
		slog.Info("SSE event stream enabled (max clients, max per IP)", "sseMaxClients", sseMaxClients, "sseMaxPerIP", sseMaxPerIP)
	}

	// Store MCP server info for handlers
	s.MCPInfo = s.Config.MCP.ServerInfo
	s.MCPInstructions = s.Config.MCP.Instructions

	// Initialize MCP API key store (BoltDB-backed, persists across restarts)
	s.mcpKeyStore = newMCPAPIKeyStore(s.DB)
	s.mcpKeyStore.FollowDB(func() *bolt.DB { return s.DB }) // a restore swaps s.DB

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
	s.WebhookManager.SetMetrics(s.Metrics)
}

// initReplication sets the node's replication role, opens the binlog and,
// on a follower, turns off the background writers.
func (s *Server) initReplication() {
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
		bl, err := binlog.NewBinlog(s.Path, binlog.BinlogConfig{
			Path:    env("MDDB_BINLOG_PATH", ""),
			MaxSize: 256 * 1024 * 1024, // 256MB
			MaxAge:  24 * time.Hour,
		})
		fatalOnErr(err, "Failed to initialize binlog")
		s.Binlog = bl
		slog.Info("binlog enabled", "currentLSN", bl.CurrentLSN())
		s.setBinlogEverywhere()
	}

	// Follower: disable background writers (data comes from binlog)
	if s.ReplicationRole == "follower" {
		s.TTLManager.Stop() // don't run TTL cleanup, leader handles it
		s.TTLManager = ttl.NewTTLManager(s.DB, serverTTLReaper{s: s})
		s.TTLManager.SetRouter(s.router()) // #336
		_ = s.TTLManager.EnsureBuckets()
		// Don't start cleanup — deletes come from binlog

		// Don't start embedding worker — embeddings come from leader
		if s.EmbeddingWorker != nil {
			s.EmbeddingWorker.Stop()
			s.EmbeddingWorker = nil
		}
		slog.Info("Replication disabled TTL cleanup and embedding worker on follower")
	}
}

// setBinlogEverywhere points every subsystem that writes at the binlog.
func (s *Server) setBinlogEverywhere() {
	s.VectorStore.SetBinlog(s.Binlog)
	s.GeoStore.SetBinlog(s.Binlog)
	s.FTSIndex.SetBinlog(s.Binlog)
	s.TTLManager.SetBinlog(s.Binlog)
	s.WebhookManager.SetBinlog(s.Binlog)
	s.SchemaManager.SetBinlog(s.Binlog)
	s.SynonymManager.SetBinlog(s.Binlog)
	s.StopWordManager.SetBinlog(s.Binlog)
	// MDDB_AUTOMATIONS=disable leaves no automation manager; setting the
	// binlog on it unguarded crashed every such start with a binlog.
	if s.AutomationManager != nil {
		s.AutomationManager.SetBinlog(s.Binlog)
	}
	s.CollectionManager.SetBinlog(s.Binlog)
	s.Aliases.SetBinlog(s.Binlog)
	if s.TemporalManager != nil {
		s.TemporalManager.SetBinlog(s.Binlog)
	}
	if s.SpellManager != nil {
		s.SpellManager.SetBinlog(s.Binlog)
	}
}

// initAuth enables authentication when MDDB_AUTH_ENABLED=true (disabled by
// default) and reports whether it did.
func (s *Server) initAuth(clock *startupClock) bool {
	authEnabled := env("MDDB_AUTH_ENABLED", "false") == "true"
	if authEnabled {
		jwtSecret := env("MDDB_AUTH_JWT_SECRET", "")
		if jwtSecret == "" {
			fatal("MDDB_AUTH_ENABLED=true requires MDDB_AUTH_JWT_SECRET to be set")
		}

		jwtExpiryStr := env("MDDB_AUTH_JWT_EXPIRY", "24h")
		jwtExpiry, err := time.ParseDuration(jwtExpiryStr)
		fatalOnErr(err, "Invalid MDDB_AUTH_JWT_EXPIRY")

		s.AuthManager = NewAuthManager(s.DB, AuthConfig{
			JWTSecret:     jwtSecret,
			JWTExpiry:     jwtExpiry,
			AdminUsername: env("MDDB_AUTH_ADMIN_USERNAME", "admin"),
			AdminPassword: env("MDDB_AUTH_ADMIN_PASSWORD", ""),
		})
		s.AuthManager.FollowDB(func() *bolt.DB { return s.DB }) // a restore swaps s.DB

		mustStep("AuthManager.EnsureBuckets", s.AuthManager.EnsureBuckets())
		clock.begin("loading users and API keys")
		mustStep("AuthManager.LoadAll", s.AuthManager.LoadAll())
		mustStep("AuthManager.BootstrapAdmin", s.AuthManager.BootstrapAdmin())

		slog.Info("Authentication enabled")
	}
	if s.AuthManager != nil {
		s.AuthManager.SetServer(s)
	}
	return authEnabled
}

// logProtocolConfig logs which protocols are on and their mode overrides.
func (s *Server) logProtocolConfig() {
	srvCfg := s.Config
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
}

// registerGraphQL serves /graphql and the playground unless
// MDDB_GRAPHQL_ENABLED=false.
func (s *Server) registerGraphQL(mux *http.ServeMux, authEnabled bool) {
	if env("MDDB_GRAPHQL_ENABLED", "true") == "false" {
		return
	}
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

// wrapHTTPHandler wraps the routes in the middleware chain:
// CORS → panic-recover → rate limit → auth → metrics → max-body → JSON → routes.
// It returns the handler and the listener's read and write timeouts.
func (s *Server) wrapHTTPHandler(mux *http.ServeMux, authEnabled bool, panelMode string) (handler http.Handler, readTimeout, writeTimeout time.Duration) {
	handler = withJSON(mux)
	// GO-044: an alias becomes its collection before any handler reads it.
	handler = s.withAliases(handler)
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
	// #315: a response that misses the write deadline is dropped by net/http
	// without a trace; say so.
	readTimeout, writeTimeout = httpTimeouts()
	handler = withDeadlineWarning(handler, writeTimeout)
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
	return handler, readTimeout, writeTimeout
}

// startHTTPListener serves the HTTP API (with optional TLS) in the
// background. The address may be a TCP host:port or a Unix Domain Socket
// (unix:/path/to/sock) — see listen_addr.go.
func (s *Server) startHTTPListener(handler http.Handler, readTimeout, writeTimeout time.Duration) {
	if !s.Config.HTTP.Enabled {
		slog.Info("HTTP server disabled")
		return
	}
	go s.serveHTTP(handler, readTimeout, writeTimeout)
}

// serveHTTP runs the HTTP API listener until it fails.
func (s *Server) serveHTTP(handler http.Handler, readTimeout, writeTimeout time.Duration) {
	srvCfg := s.Config
	httpAddr := srvCfg.HTTP.Addr
	server := &http.Server{
		Addr:              httpAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       120 * time.Second,
	}
	lis, err := openListener(httpAddr)
	mustStep("openListener", err)
	defer func() { _ = closeListener(lis, httpAddr) }()
	tlsOn := srvCfg.TLS.Enabled && srvCfg.TLS.CertFile != "" && srvCfg.TLS.KeyFile != ""
	if tlsOn && isUnixAddr(httpAddr) {
		slog.Info("mddb TLS ignored on UDS listener (filesystem perms authenticate the peer)", "httpAddr", httpAddr)
		tlsOn = false
	}
	if !tlsOn {
		slog.Info("mddb HTTP listening", "httpAddr", httpAddr, "mode", s.Mode, "dbPath", s.Path)
		mustStep("server.Serve", ignoreServerClosed(server.Serve(lis)))
		return
	}
	tlsCfg, terr := buildServerTLSConfig(srvCfg.TLS)
	if terr != nil {
		fatal("TLS config", "terr", terr)
	}
	server.TLSConfig = tlsCfg
	mtls := ""
	if srvCfg.TLS.ClientCAFile != "" {
		mtls = fmt.Sprintf(", mtls=on (clientAuth=%s)", clientAuthMode(srvCfg.TLS))
	}
	slog.Info("mddb HTTPS listening (,, tls=on)", "httpAddr", httpAddr, "mode", s.Mode, "dbPath", s.Path, "mtls", mtls)
	mustStep("server.ServeTLS", ignoreServerClosed(server.ServeTLS(lis, "", "")))
}

// startMCPListener serves MCP over HTTP on its own port, in the background.
func (s *Server) startMCPListener(panelMode string, readTimeout, writeTimeout time.Duration) {
	srvCfg := s.Config
	if !srvCfg.MCP.Enabled {
		slog.Info("MCP server disabled")
		return
	}
	// A pin naming a revision this build cannot speak is refused here
	// rather than at the first handshake: the operator set it deliberately,
	// so silently ignoring it would leave them believing a compatibility
	// problem was solved.
	mustStep("MCP protocol version", ValidateMCPProtocolVersion())
	if pinned := PinnedMCPVersion(); pinned != "" {
		slog.Warn("MCP protocol revision is pinned; version negotiation is disabled",
			"revision", pinned, "supported", SupportedMCPVersions())
	}

	// Built before the goroutine so /health can read their session counts
	// without racing the listener's startup.
	mcpSSE := NewMCPSSETransport(NewMCPHandlerWithConfig(NewDirectClient(s), loadMCPCustomTools(),
		srvCfg.MCP.ServerInfo, srvCfg.MCP.Instructions, s.Mode, srvCfg.MCP.Mode).WithMetrics(s.Metrics))
	mcpStreamable := NewMCPStreamableTransport(NewMCPHandlerWithConfig(NewDirectClient(s), loadMCPCustomTools(),
		srvCfg.MCP.ServerInfo, srvCfg.MCP.Instructions, s.Mode, srvCfg.MCP.Mode).WithMetrics(s.Metrics))
	s.mcpSSE, s.mcpStreamable = mcpSSE, mcpStreamable

	go s.serveMCP(mcpSSE, mcpStreamable, panelMode, readTimeout, writeTimeout)
}

// serveMCP runs the MCP HTTP listener until it fails.
func (s *Server) serveMCP(mcpSSE *MCPSSETransport, mcpStreamable *MCPStreamableTransport, panelMode string, readTimeout, writeTimeout time.Duration) {
	srvCfg := s.Config
	mcpMux := http.NewServeMux()
	mcpSrv := s.newMCPHTTPServer()
	mcpMux.HandleFunc("/resources", mcpSrv.handleResources)
	mcpMux.HandleFunc("/resources/read", mcpSrv.handleResourceRead)
	mcpMux.HandleFunc("/tools", mcpSrv.handleTools)
	mcpMux.HandleFunc("/tools/call", s.guardWrite(mcpSrv.handleToolCall))
	mcpMux.HandleFunc("/events", s.handleSSE)

	// MCP-over-SSE transport (legacy, 2024-11-05 spec)
	mcpMux.HandleFunc("/sse", mcpSSE.HandleSSE)
	mcpMux.HandleFunc("/message", mcpSSE.HandleMessage)
	slog.Info("MCP-over-SSE transport enabled at /sse + /message (legacy)")

	// Streamable HTTP transport (2025-11-25 spec)
	mcpMux.HandleFunc("/mcp", mcpStreamable.Handle)
	slog.Info("MCP Streamable HTTP transport enabled at /mcp")

	// MCP middleware chain: CORS → OAuth (#322) → API Key Auth → Rate Limit → Request Logging → JSON → Routes
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

	// #322: OAuth 2.1 for clients that cannot send an API key
	// (claude.ai connectors). Off unless MDDB_MCP_OAUTH_ENABLED=true.
	mcpHandler = newMCPOAuthFromEnv(mcpAuth).Wrap(mcpHandler)

	if panelMode != "external" {
		mcpHandler = withCORS(mcpHandler)
	}

	slog.Info("mddb MCP HTTP listening", "addr", srvCfg.MCP.Addr)
	server := &http.Server{
		Addr:              srvCfg.MCP.Addr,
		Handler:           mcpHandler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       120 * time.Second,
	}
	mustStep("server.ListenAndServe", ignoreServerClosed(server.ListenAndServe()))
}

// startHTTP3Listener serves the API over HTTP/3 in the background when
// enabled.
func (s *Server) startHTTP3Listener(handler http.Handler) {
	if !s.Config.HTTP3.Enabled {
		return
	}
	slog.Info("mddb HTTP/3 listening", "addr", s.Config.HTTP3.Addr)
	h3Server, err := NewHTTP3Server(s.Config.HTTP3.Addr, HTTP3Middleware(handler))
	if err != nil {
		slog.Warn("Failed to start HTTP/3 server", "err", err)
		return
	}
	// Kept on the server so shutdown can close the listener (GO-034), and
	// assigned before the listener goroutine starts: assigned inside it, the
	// shutdown sequence read the field with no synchronisation.
	s.HTTP3 = h3Server
	go func() {
		if err := h3Server.Start(); err != nil {
			slog.Warn("HTTP/3 server error", "err", err)
		}
	}()
}

// startReplicationClient starts the follower's replication client and lag
// monitor, returning what stops them. Other roles get a no-op.
func (s *Server) startReplicationClient() (stop func()) {
	if s.ReplicationRole != "follower" {
		return func() {}
	}
	leaderAddr := env("MDDB_REPLICATION_LEADER_ADDR", "")
	if leaderAddr == "" {
		fatal("MDDB_REPLICATION_ROLE=follower requires MDDB_REPLICATION_LEADER_ADDR")
	}
	replClient := NewReplicationClient(s, ReplicationClientConfig{
		LeaderAddr: leaderAddr,
		FollowerID: s.NodeID,
	})
	s.replClient = replClient
	replClient.Start()
	slog.Info("Replication client started", "leaderAddr", leaderAddr, "nodeID", s.NodeID)

	// Monitor replication lag and fire ops.replication_lag_high
	// when it crosses the threshold.
	s.LagMonitor = NewReplicationLagMonitor(s.WebhookManager, replClient)
	s.LagMonitor.Start()
	return func() {
		s.LagMonitor.Stop()
		replClient.Stop()
	}
}

// startGRPCListener serves gRPC in the background when enabled.
func (s *Server) startGRPCListener(authEnabled bool) {
	if !s.Config.GRPC.Enabled {
		slog.Info("gRPC server disabled")
		return
	}
	grpcAddr := s.Config.GRPC.Addr
	grpcOpts, grpcTLSLog := s.grpcServerOptions(authEnabled)
	go func() {
		slog.Info("mddb gRPC listening", "grpcAddr", grpcAddr, "mode", s.Mode, "dbPath", s.Path, "grpcTLSLog", grpcTLSLog)
		mustStep("startGRPCServer", startGRPCServer(s, grpcAddr, grpcOpts...))
	}()
}

// grpcServerOptions builds the interceptor chains and TLS credentials for the
// gRPC listener, and the TLS note for its startup log.
func (s *Server) grpcServerOptions(authEnabled bool) ([]grpc.ServerOption, string) {
	var grpcOpts []grpc.ServerOption
	// GO-044: aliases resolve before auth, so permissions are checked on the
	// collection an alias points at.
	unaryChain := []grpc.UnaryServerInterceptor{s.aliasUnaryInterceptor()}
	streamChain := []grpc.StreamServerInterceptor{s.aliasStreamInterceptor()}
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
	if isUnixAddr(s.Config.GRPC.Addr) {
		return grpcOpts, grpcTLSLog
	}
	tlsCfg, terr := buildServerTLSConfig(s.Config.TLS)
	if terr != nil {
		fatal("gRPC TLS config", "terr", terr)
	}
	if tlsCfg != nil {
		grpcOpts = append(grpcOpts, grpc.Creds(credentials.NewTLS(tlsCfg)))
		grpcTLSLog = ", tls=on"
		if s.Config.TLS.ClientCAFile != "" {
			grpcTLSLog = fmt.Sprintf(", tls=on, mtls=on (clientAuth=%s)", clientAuthMode(s.Config.TLS))
		}
	}
	return grpcOpts, grpcTLSLog
}

// --- helpers / buckets

func (s *Server) ensureBuckets() error {
	// #333: written only when one is missing; see boltx.EnsureBuckets.
	return boltx.EnsureBuckets(s.DB,
		s.BucketNames.Docs,          // doc|collection|id -> json
		s.BucketNames.IdxMeta,       // meta|collection|key|value|docID -> 1
		s.BucketNames.Rev,           // rev|collection|docID|ts -> json
		s.BucketNames.ByKey,         // bykey|collection|key|lang -> docID
		[]byte("embedding_configs"), // embedding model configurations
		bucketBulkJobs,              // bulk ingest job tracking
	)
}
