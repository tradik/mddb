---
title: "MDDB Features"
slug: "docs/features"
description: "The complete MDDB feature list by category: storage, search, protocols, authentication, replication, automation, geo and the web admin panel."
status: publish
---

# MDDB Features

Complete list of MDDB features organized by category.

## Core Database Features

### Temporal Tracking
- **Lifecycle Events** - Track document create, update, and access events per collection
- **Activity Histogram** - Day/week/month event-frequency charts
- **Hot Documents** - Top-N most-accessed document leaderboard
- **Async writes** - Zero overhead on the read/write path; events batched with BoltDB `Batch()`
- See [TEMPORAL-TRACK.md](TEMPORAL-TRACK.md)

### Spell Correction
- **FTS Auto-correction** - Optionally correct FTS queries before execution (per-collection)
- **Spell Suggest API** - Token-level corrections with confidence scores
- **Custom Dictionaries** - Per-collection domain-term allowlists stored in BoltDB
- **Zero new dependencies** - Built on existing `levenshtein` library
- See [SYMSPELL.md](SYMSPELL.md)

### Document Management
- **Full CRUD Operations** - Add, retrieve, update, delete markdown documents
- **Rich Metadata** - Multi-value tags and structured metadata per document
- **Collections** - Organize documents into logical groups
- **Multi-language Support** - Store same document in multiple languages with same key
- **Template Variables** - Dynamic `{{variable}}` substitution on retrieval
- **Document TTL** - Auto-expiring documents with background cleanup (like Redis)
- **Schema Validation** - Optional per-collection JSON Schema validation for metadata

### Revision History
- **Complete Version Control** - Every update creates a new revision
- **Full Content Snapshots** - Each revision stores complete document content
- **Revision Queries** - Access any historical version through API
- **Truncation** - Remove old revisions to save space
- **Per-Collection Retention Cap** (v2.9.14+) - `maxRevisions` on `CollectionConfig` enforces a synchronous cap on every write: older revisions are trimmed in the same BoltDB transaction so high-churn collections can't grow history unbounded. `0` (default) = unlimited. Configurable via REST `/v1/collection-config`, gRPC `SetCollectionConfig`, MCP `set_collection_config`, and the Admin Panel.

### Search & Discovery

#### Metadata Search
- **Indexed Queries** - Fast searches using metadata tags
- **Multi-value Filters** - Query documents by multiple tag values
- **Sorting** - Sort by `addedAt`, `updatedAt`, or custom metadata
- **Pagination** - Limit and offset for large result sets

#### Vector Search (Semantic)
- **Auto-embeddings** - Documents embedded automatically in background
- **Multiple Providers** - OpenAI, Cohere, Voyage AI, Ollama
- **Similarity Search** - Find documents by meaning, not keywords
- **Multiple Algorithms** - Flat (exact), HNSW (approximate), IVF (clustered), PQ (compressed), SQ (scalar quantized), BQ (binary quantized)
- **Query-time Algorithm Selection** - Choose algorithm per request via `algorithm` parameter
- **Threshold Filtering** - Minimum similarity score
- **Metadata Filtering** - Combine semantic + metadata filters
- **Configurable Models** - Switch providers/models without code changes
- **Background Indexing** - Non-blocking embedding generation
- **Automatic Fallback** - Falls back to flat if ANN index not ready
- **Embedding Chunking** - Auto-split long documents into paragraph-based chunks before embedding with sentence and hard-split fallbacks. Multi-key chunk storage with deduplication in search results. Configurable via `MDDB_EMBEDDING_CHUNK_SIZE` and `MDDB_EMBEDDING_CHUNK_ENABLED`.
- **Embedding Cache** - Repeated queries are served from memory instead of the provider, and a reindex reuses the vectors of chunks whose text did not change — editing one paragraph of a fifty-chunk document no longer re-embeds all fifty. Hit/miss counters in `/metrics`; `MDDB_EMBEDDING_CACHE_SIZE=0` disables it.

#### Full-Text Search
- **Built-in Inverted Index** - No external search-engine dependency
- **TF-IDF Scoring** - Classic term frequency-inverse document frequency ranking
- **BM25 Scoring** - Okapi BM25 with document length normalization (k1=1.2, b=0.75)
- **BM25F Scoring** - Field-weighted BM25 — weight matches in title, tags, description differently from body content
- **PMISparse Scoring** - Two-phase sparse retrieval with PPMI-based automatic query expansion (invented by Tradik Limited). Bridges vocabulary gap between short queries and documents without synonyms or external models. Fuzzy variant with typo tolerance.
- **Query-time Algorithm Selection** - Choose TF-IDF, BM25, BM25F, or PMISparse per request via `algorithm` parameter
- **Typo Tolerance** - Fuzzy matching with configurable edit distance (0-2) via `fuzzy` parameter
- **Porter Stemming** - Reduce words to root forms for better recall (configurable, per-query disable)
- **Synonym Expansion** - Bidirectional query-time synonym expansion with per-collection dictionaries
- **Synonym Management API** - CRUD endpoints for managing synonym dictionaries
- **Stop Word Filtering** - Remove common words
- **Multi-field Search** - Search in content and metadata
- **Language-aware** - Per-language stop words
- **Metadata Pre-filtering** - `filterMeta` parameter to scope FTS results by metadata before BM25 scoring (AND across keys, OR within key)
- **Per-Query Boost/Demote** (v2.9.12+) - `boost` map keyed by `"metaKey:metaValue"` multiplies scores at query time without reindexing. Positive values boost (`5.0` → 5×), negative values demote (`-2.0` → ½×); combined multiplicatively with a `0.001` floor. Works in FTS and Hybrid search.
- **Prefix Autocomplete** (v2.9.12+) - `GET /v1/autocomplete?collection=X&q=mar` returns top-N terms starting with the given prefix, ranked by document frequency. Scans the existing FTS inverted index — no additional storage. Field-scoped via `field` parameter; scan is bounded at 10k entries. Also exposed as MCP `autocomplete` tool.
- **Expression Query DSL** (v2.9.13+) - `mode: "expression"` on `/v1/fts` runs through a proper recursive-descent parser with parenthesized grouping, operator precedence (NOT > AND > OR), implicit AND between adjacent atoms, and mixed atom types (terms, fuzzy `~N`, phrases, proximity, wildcards) in one query. Example: `(rust OR golang) AND "async runtime"~3 NOT legacy`.
- **Search-Result Highlighting** (v2.9.13+) - `highlight: true` on `/v1/fts` returns context snippets around matched terms with each match wrapped in a configurable tag (default `<mark>…</mark>`). Clusters nearby hits, snaps to word boundaries, supports custom `highlightTag` / `maxHighlights` / `fragmentSize`. Works uniformly across every FTS mode.
- **Inline Facets** (v2.9.14+) - `facetBy: ["category","lang"]` on `/v1/fts` and `/v1/hybrid-search` returns a `facets` map of per-value counts aggregated over the matched documents — no separate `/v1/aggregate` round-trip. `facetMaxValues` caps per-key cardinality; counts reflect post-filter, post-boost, post-curation state so UIs stay in sync.
- **Curation Rules** (v2.9.14+) - `POST/GET/PUT/DELETE /v1/curation` pins specific documents to fixed positions and/or hides others for a given query. Rule shape: `{collection, query, matchMode: exact|contains, pins: [{key, lang, position}], hides: [...], enabled}`. Pinned results carry `pinned: true` on the wire. Applied in FTS and Hybrid pipelines after scoring, before pagination. See [SEARCH.md](SEARCH.md).

#### Hybrid Search
- **Combined Retrieval** - Merge BM25/BM25F/PMISparse keyword scores with vector semantic scores in a single query
- **Alpha Blending** - Weighted linear interpolation: `combined = (1-alpha) * FTS + alpha * vector`
- **RRF (Reciprocal Rank Fusion)** - Rank-based fusion robust to different score distributions
- **Configurable Parameters** - `strategy`, `alpha`, `rrfK`, `algorithm`, `vectorAlgorithm`
- **API Endpoint** - `POST /v1/hybrid-search` with gRPC `HybridSearch` RPC and `hybrid_search` MCP tool
- **Distance Sort** (v2.9.13+) - With a `geo` filter attached, `sort: "distance"` re-orders post-merge results by proximity ascending instead of fused score

#### Geospatial Search
- **R-tree Index** - Radius and bounding-box queries over lat/lng points
- **Geohash Index** - Alternative geohash-prefix index with same API
- **GeoJSON Polygon Containment** (v2.9.13+) - `POST /v1/geo-polygon` accepts GeoJSON Polygon (outer ring + holes) or MultiPolygon (union); bounding-box prefilter + ray-cast. MCP tool `geo_polygon`
- **Postcode Lookup** - Optional per-country CSV lookups for postcode → lat/lng resolution
- **Composable with FTS and vector** - Spatial filter combines freely with other search modes

#### Memory RAG (Conversational Memory)
- **Session Management** — Create, list, and track conversation sessions with user/scenario metadata
- **Message Storage** — Store user/assistant/system/tool messages with auto-embedding
- **Semantic Recall** — Retrieve relevant past messages using vector similarity search
- **Keyword Recall** — FTS-based conversation search with BM25 scoring
- **Hybrid Recall** — Combined semantic + keyword search with Reciprocal Rank Fusion (RRF)
- **Session Summarization** — Generate and store conversation summaries with embeddings
- **User/Session Filtering** — Scope recall to specific users, sessions, or message roles
- **Session TTL** — Auto-expiring sessions (default: 30 days, configurable)
- **6 MCP Tools** — `memory_start_session`, `memory_add_message`, `memory_recall`, `memory_summarize`, `memory_list_sessions`, `memory_session_history`
- **API Endpoints** — `POST /v1/memory/session`, `/message`, `/recall`, `/summarize`, `/sessions`, `/history`

#### Zero-Shot Classification
- **Embedding-based** — Classify documents against candidate labels using cosine similarity
- **No Training Data** — Works out of the box with any embedding provider
- **Document or Text** — Classify by document reference (reuses existing embedding) or raw text
- **Batch Labels** — All candidate labels embedded in a single batch call
- **Configurable** — `topK`, `multi` (return all above threshold), `threshold`
- **API Endpoint** — `POST /v1/classify` with gRPC `Classify` RPC and `classify_document` MCP tool

## APIs & Protocols

### HTTP/JSON REST API
- **RESTful Design** - Standard HTTP methods (GET, POST, DELETE)
- **JSON Payloads** - Easy to debug with curl, Postman
- **OpenAPI/Swagger** - Machine-readable spec + interactive docs
- **CORS Support** - Cross-origin requests
- **Content Negotiation** - Accept headers
- **Rate Limiting** - Configurable request limits
- **`GET /v1/meta-keys`** — List unique metadata keys and values for a collection
- **`GET /v1/checksum`** — Lightweight collection checksum for cache invalidation

### gRPC/Protobuf API
- **High Performance** - 16x faster than HTTP/JSON
- **Binary Protocol** - 70% smaller payload size
- **HTTP/2 Multiplexing** - Concurrent requests on single connection
- **Streaming** - Bi-directional streaming support
- **gRPC Reflection** - Use grpcurl for debugging
- **Protocol Buffers** - Strongly typed contracts
- **Code Generation** - Auto-generate clients for Go, Python, Node.js, PHP

### GraphQL API
- **Flexible Queries** - Request exactly the fields you need
- **Schema Introspection** - Self-documenting API
- **GraphQL Playground** - Interactive development tool
- **Authentication Directives** - `@auth`, `@hasRole`, `@hasPermission`
- **Mutations** - Create, update, delete operations
- **Nested Queries** - Fetch related data in single request
- **Type Safety** - Compile-time validation
- **Query Complexity Limits** - Prevent expensive queries

### MCP Server (Model Context Protocol)
- **Dual Mode** - HTTP server + stdio mode for IDE integration
- **LLM Integration** - Windsurf, Claude Desktop, other AI tools
- **Custom Tools** - YAML-defined website-specific AI tools
- **Preconfigured Defaults** - Semantic search, document search, full-text search
- **Docker Ready** - Single image, mode selection via env var
- **gRPC Backend** - High-performance communication with MDDB
- **REST Fallback** - Automatic fallback if gRPC unavailable

## Security & Access Control

### Authentication
- **JWT Tokens** - JSON Web Tokens with expiry
- **API Keys** - Long-lived keys for scripts and automation
- **bcrypt Hashing** - Secure password storage
- **Token Refresh** - Renew tokens without re-authentication
- **Configurable Expiry** - Set token lifetime
- **Secure Headers** - `Authorization: Bearer <token>` or `X-API-Key: <key>`

### Authorization (RBAC)
- **Collection-level Permissions** - Read, Write, Admin per collection
- **User Roles** - Regular users and administrators
- **Group-based Permissions** - Organize users into groups
- **Inherited Permissions** - Groups grant permissions to all members
- **Admin Override** - Admins have full access to all collections
- **Permission Checks** - Enforced at every API call

### User Management
- **Multi-user Support** - Unlimited users
- **Admin Accounts** - Super users with full access
- **User CRUD** - Create, read, update, delete users (admin only)
- **Password Changes** - Users can change own password
- **User Listing** - Admins can list all users
- **API Key Management** - Users manage their own API keys

### Group Management
- **Group Creation** - Organize users into logical groups
- **Permission Assignment** - Grant collection permissions to groups
- **User Assignment** - Add/remove users from groups
- **Multiple Groups** - Users can belong to multiple groups
- **Cumulative Permissions** - Users get permissions from all their groups

### Compliance & Hardening (ISO 27001 / SOC 2)
- **Audit Log** (v2.9.15+) - Structured JSON events (auth attempts, writes, deletes) persisted to a dedicated BoltDB bucket; admin-only `GET /v1/audit` with actor/action/result/time filters; retention configurable via `MDDB_AUDIT_RETENTION_DAYS` (default 90). ISO A.8.15 / SOC 2 CC7.2. See [config.md#audit-log-iso-27001--soc-2](config.md#audit-log-iso-27001--soc-2) and [SECURITY.md](SECURITY.md).
- **Production Hardening Switch** (v2.9.15+) - `MDDB_PRODUCTION=true` refuses startup unless every guardrail is satisfied (auth on, JWT secret ≥32 B, TLS on, CORS explicit, audit + rate limit enabled). Unauth `GET /v1/compliance-status` publishes live state for operator probes. ISO A.8.9 / SOC 2 CC6.1. See [config.md#production-hardening-iso-27001--soc-2](config.md#production-hardening-iso-27001--soc-2) and [SECURITY.md](SECURITY.md).
- **HTTP + gRPC Rate Limiting** (v2.9.15+) - Single sliding-window limiter shared across both transports; per-IP or per-user keying; `X-RateLimit-*` headers, `429 Retry-After`, gRPC `ResourceExhausted`. `/health`, `/v1/health`, `/metrics` exempt. Independent from MCP limiter. ISO A.5.30 / SOC 2 CC6.6. See [config.md#rate-limiting-http--grpc](config.md#rate-limiting-http--grpc) and [SECURITY.md](SECURITY.md).
- **At-Rest Encryption** (v2.9.15+) - Opt-in per-collection AES-256-GCM on `docs` and `rev` buckets. Enable globally with `MDDB_ENCRYPTION_KEY` (32 B base64) and flip `CollectionConfig.encrypted=true` per collection. Transparent read path, legacy plaintext stays readable, key loss is terminal. ISO A.8.24 / SOC 2 CC6.7. See [config.md#at-rest-encryption-iso-27001--soc-2](config.md#at-rest-encryption-iso-27001--soc-2) and [SECURITY.md](SECURITY.md).
- **Incident Webhook Events** (v2.9.15+) - Five named events on existing `/v1/webhooks`: `security.auth_failure_burst`, `security.rate_limit_exceeded`, `ops.replication_lag_high`, `ops.panic_recovered`, `ops.disk_usage_high`. Panic-recovery middleware turns handler crashes into 500 + event. Structured `detail` payload. ISO A.8.16 / SOC 2 CC7.3–7.4. See [config.md#incident-webhook-events](config.md) and [SECURITY.md](SECURITY.md).

## Integration & Automation

### Automation System
- **Triggers** - Fire webhooks when new/updated/deleted documents match search criteria (FTS/vector/hybrid) above configurable threshold
- **Crons** - Schedule periodic trigger execution using cron expressions (6-field format with seconds)
- **Webhook Targets** - Named HTTP endpoints with custom method (POST/GET/PUT), custom headers, and template variable substitution (`{{doc.id}}`, `{{collection}}`, etc.)
- **Sentiment Analysis** - Optional keyword-based sentiment condition (-1.0 to +1.0) on triggers with AND/OR logic for combining with search
- **Execution Logs** - `GET /v1/automation-logs` with cursor-based pagination, status filtering, TTL-based cleanup
- **Template Variables** - Dynamic `{{variable}}` substitution in webhook URLs and headers with document fields, meta, scores, and context
- **Retry Logic** - Exponential backoff (0s, 1s, 5s, 15s) with custom X-MDDB headers
- **Unified Storage** - All rules (webhooks, triggers, crons) in single `automation` BoltDB bucket
- **HTTP API** - `GET/POST /v1/automation`, `GET/PUT/DELETE /v1/automation/{id}`, `POST /v1/automation/{id}/test`
- **MCP Tools** - `list_automation`, `create_automation`, `get_automation`, `update_automation`, `delete_automation`, `test_automation`, `get_automation_logs`
- **Configurable** - `MDDB_AUTOMATIONS`, `MDDB_AUTOMATION_LOGS`, `MDDB_AUTOMATION_LOGS_TTL`, `MDDB_TRIGGERS`, `MDDB_CRONS`, `MDDB_WEBHOOKS`

### Import from URL
- **Fetch Markdown** - Download from any HTTP/HTTPS URL
- **Frontmatter Parsing** - Automatic YAML frontmatter extraction
- **Key Derivation** - Auto-generate key from filename if not provided
- **Metadata Extraction** - Frontmatter becomes document metadata
- **Content Cleaning** - Strip frontmatter from content
- **Error Handling** - Graceful handling of HTTP errors, invalid markdown

### Telemetry (Prometheus)
- **Metrics Endpoint** - `GET /metrics` (Prometheus-compatible)
- **Request Counters** - Total requests by method, path, status
- **Latency Histograms** - Request duration distribution
- **Database Stats** - Document count, revision count, DB size
- **Vector Stats** - Embedding count, queue size
- **Go Runtime** - Goroutines, memory, GC stats
- **Configurable** - Disable with `MDDB_METRICS=false`

### Bulk Import
- **Folder Scanning** - Load all `.md` files from directory
- **Recursive** - Optional recursive subdirectory scanning
- **Frontmatter Support** - Extract metadata from YAML frontmatter
- **Custom Metadata** - Add metadata to all imported files
- **Progress Tracking** - Show import progress
- **Dry Run** - Preview what would be imported
- **Error Handling** - Continue on errors, report failures
- **Async Bulk Ingest Jobs** (v2.9.12+) - `/v1/bulk-ingest-job*` endpoints for long-running imports. Queue jobs that return HTTP 202 immediately, poll status, cancel pending, list all jobs newest-first. Single FIFO worker processes 500-doc chunks; orphan jobs from crashed runs are marked `failed` on startup. Optional `callbackUrl` fires a webhook on completion. 4 MCP tools (`bulk_ingest_submit/status/list/cancel`) mirror the HTTP API.

## Developer Tools

### CLI Client
- **Full-featured** - All server operations available
- **File Input** - Read markdown from files
- **Piping Support** - Works with Unix pipes
- **Man Page** - Complete Unix-style manual
- **GraphQL Support** - Use GraphQL instead of REST with `--graphql`
- **Interactive Playground** - `mddb-cli playground` opens browser
- **Authentication** - JWT and API key support
- **Output Formatting** - JSON, pretty-print, or raw

### Web Admin Panel
- **Modern React UI** - Fast, responsive interface
- **Server Statistics** - Real-time metrics dashboard
- **Collection Browser** - Browse all collections and documents
- **Document Viewer** - View markdown with syntax highlighting
- **Document Editor** - Edit with live preview
- **Split-view** - Edit/Preview/Both modes
- **Markdown Toolbar** - Formatting buttons
- **Templates** - Pre-built templates (blog, docs, README, API)
- **Advanced Filtering** - Filter by metadata
- **Pagination** - Offset-based pagination with total count for metadata search
- **Result Counts** - Shows number of results for FTS and vector search
- **Resizable Sidebar** - Drag to resize, collapsible with persist to localStorage
- **API Mode Toggle** - Switch between REST and GraphQL
- **Authentication** - Login with username/password
- **User Management** - Manage users and groups (admin only)
- **Conditional UI** - Users & Groups hidden when auth is disabled
- **Vector Search** - Semantic search interface with auto-retry on index loading
- **LLM Connections** - Config templates for Claude, ChatGPT, Ollama, DeepSeek, Manus, Bielik.ai
- **CPU & Memory Monitoring** - Live CPU usage bar, heap memory bar with color coding
- **Version Display** - Server and panel version always visible in sidebar
- **Automation Tab** - Full automation management UI with type filter tabs, dynamic forms, enable/disable toggle, test button
- **Automation Logs Tab** - Execution log viewer with status filter, auto-refresh, cursor-based pagination
- **Hybrid Search Mode** - Combined FTS + vector search with strategy/alpha/algorithm controls
- **Command Modal** - Copy-ready API examples in curl, PHP, Python, and JavaScript for all search operations
- **Webhook Template Variables Help** - Collapsible reference for available `{{variable}}` names in webhook forms
- **Metadata Tag Filter** — Filter search results by metadata tags across FTS, vector, and hybrid modes. Dynamically loads available tags from collection. Multi-select with AND/OR semantics.
- **API Endpoints Browser** — Tabbed view of all HTTP endpoints, gRPC methods, and MCP tools with auth status indicators. Includes link to versioned OpenAPI spec on GitHub.

### Docker Support
- **Multi-arch Images** - amd64, arm64, armv7
- **Alpine Linux** - Minimal image size (~29MB)
- **Health Checks** - Built-in health check endpoint
- **Volume Persistence** - Mount `/data` for database
- **Environment Config** - All settings via env vars
- **Docker Compose** - Development and production configs
- **Auto-restart** - Crash recovery

### Development Mode
- **Hot Reload** - Auto-restart on code changes (with air)
- **Development Logging** - Verbose debug output
- **CORS** - Enabled for local development
- **Source Maps** - JavaScript debugging
- **Fast Builds** - Optimized for iteration

## Horizontal Scaling

### Leader-Follower Replication
- **Single-leader Replication** - One write node, multiple read-only followers
- **Binary Replication Log** - Compact binlog with LSN-based change tracking
- **Real-time Streaming** - gRPC-based binlog streaming with sub-100ms lag
- **Full Snapshot Sync** - Automatic snapshot transfer for new followers
- **Automatic Reconnection** - Followers reconnect and catch up after disconnects
- **Subsystem Replication** - Documents, vectors, FTS, webhooks, schemas, auth all replicated
- **Cluster Dashboard** - Web panel shows node status, lag, and follower health
- **Prometheus Metrics** - `mddb_replication_lsn`, `mddb_binlog_size_bytes`, follower count
- **Zero Config Standalone** - Replication disabled by default, opt-in via `MDDB_REPLICATION_ROLE`

See [Replication Guide](REPLICATION.md) for setup instructions and examples.

## Operations & Management

### Export & Backup
- **NDJSON Export** - Newline-delimited JSON format
- **ZIP Export** - Compressed archive with metadata
- **Filtered Export** - Export specific collections or metadata filters
- **Full Backup** - Complete database backup (BoltDB file)
- **Streaming** - Memory-efficient for large datasets
- **Restore** - Restore from backup file

### Database Maintenance
- **Truncate Revisions** - Remove old revisions to save space
- **Keep N Revisions** - Configurable retention policy
- **Cache Invalidation** - Drop cache after truncate
- **No Scheduled Vacuum Needed** - The embedded storage engine reuses freed
  pages automatically, so there is no routine vacuum/compaction job to run or
  schedule. Reclaiming disk space after mass deletions is an optional one-off
  (restore from a fresh backup); day-to-day operation is maintenance-free.
- **Statistics** - Real-time server and DB metrics

### Access Modes
- **Read-only** - Prevent all writes
- **Write-only** - Prevent reads (rare use case)
- **Read-write** - Default mode
- **Environment Config** - `MDDB_ACCESS_MODE=read-only`

### Monitoring
- **Health Check** - `GET /health` endpoint
- **Statistics** - `GET /v1/stats` for server info
- **Prometheus Metrics** - Full observability
- **Grafana Dashboards** - Pre-built visualizations
- **Alerting** - Prometheus alertmanager rules
- **Logging** - Structured JSON logs

## Performance Features

### Storage Optimizations
- **BoltDB** - Embedded ACID key-value store
- **Single File** - Entire database in one file
- **B+Tree Index** - Fast lookups and range scans
- **MVCC** - Multi-version concurrency control
- **Prefix Indices** - Composite keys for fast metadata queries
- **NoFreelistSync** - Faster writes (configurable)
- **Initial mmap** - Pre-allocate 100MB for performance

### Caching
- **Lock-free Cache** - 16 shards for concurrent reads
- **LRU Eviction** - Configurable cache size
- **Metadata Cache** - Index metadata in memory
- **Query Cache** - Cache search results
- **TTL Support** - Auto-expire cached entries

### Batch Operations
- **Batch Add** - Add multiple documents in single transaction
- **Parallel Processing** - Concurrent batch processing
- **Single Transaction** - ACID guarantees for batch
- **Error Handling** - Partial success reporting

### Async Processing
- **Background Embedding** - Non-blocking vector indexing
- **TTL Cleanup** - Async expired document removal
- **Webhook Delivery** - Async HTTP callbacks
- **Queue Management** - Configurable queue sizes

### Advanced (Extreme Mode)
Enable with `MDDB_EXTREME=true`:
- **Write-Ahead Log** - WAL with periodic sync
- **Adaptive Compression** - Snappy/Zstd based on size (configurable thresholds)
- **Delta Encoding** - 5-10x smaller revision storage
- **Bloom Filters** - Fast negative lookups (1% FP)
- **Zero-Copy I/O** - Direct memory access
- **Vectorized Operations** - SIMD instructions
- **HTTP/3 + QUIC** - Modern transport protocol

## Extensibility

### Protocol Buffers
- **Shared Definitions** - Single source of truth
- **Code Generation** - Auto-generate clients
- **Version Control** - API contract versioning
- **Type Safety** - Compile-time validation
- **Multi-language** - Go, Python, Node.js, PHP, Java, C++

### Client Libraries
- **Go** - Native client library
- **Python** - Zero-dependency single file
- **PHP** - Zero-dependency single file (PHP 8.0+)
- **Node.js** - npm package
- **Auto-generated** - From protobuf definitions

### Custom MCP Tools
- **YAML Definitions** - Define website-specific tools
- **Preconfigured Defaults** - Semantic search, document search, FTS
- **Prompt Templates** - Customize AI tool descriptions
- **Parameter Validation** - JSON schema validation
- **Dynamic Loading** - Hot reload tool definitions

## Platform Support

### Operating Systems
- **Linux** - Ubuntu, Debian, RHEL, CentOS, Fedora, Arch
- **macOS** - Intel and Apple Silicon
- **FreeBSD** - amd64
- **Windows** - WSL2 recommended
- **Docker** - Any platform with Docker

### Architectures
- **amd64** - Intel/AMD 64-bit
- **arm64** - ARM 64-bit (Apple Silicon, Raspberry Pi 4)
- **armv7** - ARM 32-bit (Raspberry Pi 3)

### Package Formats
- **DEB** - Debian/Ubuntu packages
- **RPM** - RHEL/CentOS/Fedora packages
- **Tarball** - Standalone binaries
- **Docker** - Container images
- **Homebrew** - macOS (coming soon)

## Configuration

### Environment Variables
- `MDDB_DB_PATH` - Database file path
- `MDDB_HTTP_PORT` - HTTP API port (default: 11023)
- `MDDB_GRPC_PORT` - gRPC API port (default: 11024)
- `MDDB_ACCESS_MODE` - read-only, write-only, read-write
- `MDDB_EXTREME` - Enable extreme performance mode
- `MDDB_GRAPHQL_ENABLED` - Enable GraphQL endpoint
- `MDDB_GRAPHQL_PLAYGROUND` - Enable GraphQL Playground
- `MDDB_METRICS` - Enable Prometheus metrics
- `MDDB_EMBEDDING_PROVIDER` - openai, cohere, voyage, ollama
- `MDDB_EMBEDDING_API_KEY` - API key for embedding provider
- `MDDB_EMBEDDING_MODEL` - Model name
- `MDDB_EMBEDDING_DIMENSIONS` - Vector dimensions
- `MDDB_FTS_STEMMING` - Enable/disable Porter stemming (default: true)
- `MDDB_FTS_SYNONYMS` - Enable/disable synonym expansion (default: true)
- `MDDB_COMPRESSION_ENABLED` - Enable/disable adaptive compression (default: true)
- `MDDB_COMPRESSION_SMALL_THRESHOLD` - Snappy threshold in bytes (default: 1024)
- `MDDB_COMPRESSION_MEDIUM_THRESHOLD` - Zstd threshold in bytes (default: 10240)
- `MDDB_AUTH_ENABLED` - Enable authentication
- `MDDB_JWT_SECRET` - JWT signing secret
- `MDDB_REPLICATION_ROLE` - leader, follower, or empty (standalone)
- `MDDB_REPLICATION_LEADER_ADDR` - Follower: gRPC address of the leader
- `MDDB_BINLOG_ENABLED` - Enable binlog (auto-enabled for leader)
- `MDDB_NODE_ID` - Unique node identifier
- `MDDB_AUTOMATIONS` - Enable/disable entire automation system (default: enabled)
- `MDDB_AUTOMATION_LOGS` - Enable/disable automation execution logging (default: enabled)
- `MDDB_AUTOMATION_LOGS_TTL` - Automation log retention period (default: 7d)
- `MDDB_TRIGGERS` - Enable/disable triggers (default: false)
- `MDDB_CRONS` - Enable/disable crons (default: false)
- `MDDB_WEBHOOKS` - Enable/disable webhooks (default: false)
- `MDDB_EMBEDDING_CHUNK_ENABLED` - Enable/disable embedding chunking (default: true)
- `MDDB_EMBEDDING_CHUNK_SIZE` - Maximum chunk size in characters (default: 1500)
- `MDDB_EMBEDDING_CACHE_SIZE` - Embeddings held in the query cache (default: 1024; `0` disables)
- `MDDB_EMBEDDING_CACHE_TTL` - Cache entry lifetime in seconds (default: 3600)
- `MDDB_PANEL_MODE` - Panel mode: internal (CORS) or external (reverse proxy)

### CLI Flags
- `--db` - Database file path
- `--http-port` - HTTP API port
- `--grpc-port` - gRPC API port
- `--access-mode` - Access mode
- `--graphql` - Enable GraphQL
- `--extreme` - Enable extreme mode
- `--help` - Show help

**[← Back to README](../README.md)**
