---
title: "MDDB Architecture"
slug: "docs/architecture"
description: "How MDDB is built: the BoltDB storage layout, the HTTP/gRPC/GraphQL request path, the built-in MCP server, and the trust gates every request passes."
status: publish
---

# MDDB Architecture

> **Note**: This document describes the foundational architecture (storage layout, request flow). For an up-to-date list of every shipped subsystem (vector search, FTS, geo, MCP, GraphQL, auth, replication, TLS, UDS, etc.) see [FEATURES.md](FEATURES.md) and the canonical version table in the root [README.md](../README.md).

## Overview

MDDB is an AI-native embedded document database built on top of BoltDB. It serves a triple-protocol surface — **HTTP/JSON REST**, **gRPC/Protobuf**, and **GraphQL** — over either TCP or Unix Domain Sockets, with optional TLS / mTLS. A built-in **MCP server** (67 tools, MCP 2025-11-25) exposes the same operations to LLM agents over stdio, Streamable HTTP, and SSE transports.

Search is a layered stack: metadata indexes for filter pre-pruning, **full-text search** (TF-IDF / BM25 / BM25F / PMISparse, 7 modes, 18-language stemming, fuzzy / proximity), **vector / semantic search** (Flat / HNSW / IVF / PQ / OPQ / SQ / BQ + per-collection int8/int4 quantization, plug-in OpenAI / Ollama / Cohere / Voyage embeddings), **geospatial search** (R-tree + geohash), and **hybrid search** that combines BM25 and dense vectors via alpha blending or Reciprocal Rank Fusion.

Beyond storage and search, MDDB ships **JWT authentication + RBAC**, **mTLS client-cert auth**, **leader-follower binlog replication**, **automation** (triggers / crons / webhooks / sentiment / template variables), **document TTL**, **revisions**, **schema validation**, **aggregations**, and a **React admin panel**.

## High-Level Architecture

```mermaid
graph TB
    Client[HTTP Client]
    API[REST API Layer]
    Server[MDDB Server]
    Storage[BoltDB Storage]
    
    Client -->|HTTP Requests| API
    API -->|Route & Validate| Server
    Server -->|Read/Write| Storage
    Storage -->|Persist| DB[(mddb.db)]
    
    Server -->|Hooks| Webhooks[External Webhooks]
    Server -->|Hooks| Commands[System Commands]
```

## Components

### 1. Protocol layer (HTTP / gRPC / GraphQL / MCP)

Routes requests to handlers, applies middleware (JSON, CORS, auth, rate limit, logging), enforces access modes.

For the **full endpoint catalogue** see [API.md](API.md) (HTTP/REST), [GRPC.md](GRPC.md) (gRPC), [GRAPHQL.md](GRAPHQL.md) (GraphQL schema), [MCP.md](MCP.md) (MCP tools), and the live Swagger UI at `/docs/api/swagger/`. The endpoint list lives in [services/mddbd/endpoints_handlers.go](https://github.com/tradik/mddb/blob/main/services/mddbd/endpoints_handlers.go) and is queryable at `GET /v1/endpoints`.

### 2. Storage Layer (BoltDB)

**Database Structure**:

```
mddb.db
├── docs/          # Current document versions
│   └── doc|{collection}|{docID} → JSON
├── idxmeta/       # Metadata indices
│   └── meta|{collection}|{key}|{value}|{docID} → 1
├── rev/           # Revision history
│   └── rev|{collection}|{docID}|{timestamp} → JSON
└── bykey/         # Key-to-ID mapping
    └── bykey|{collection}|{key}|{lang} → docID
```

**Foundational buckets** (the four every collection always uses):

1. **`docs`** — latest document version. Key: `doc|{collection}|{docID}`. Value: protobuf-encoded `Doc` (compressed via the configured codec).
2. **`idxmeta`** — metadata index for fast filter pre-pruning. Key: `meta|{collection}|{metaKey}|{metaValue}|{docID}`. Value: existence marker. Enables prefix scans.
3. **`rev`** — revision history. Key: `rev|{collection}|{docID}|{timestamp}`. Value: encoded document snapshot, sorted by timestamp.
4. **`bykey`** — key/lang → docID lookup. Key: `bykey|{collection}|{key}|{lang}`.

Subsystems add their own buckets on top: `vectors`, `vector_meta` (vector store), `fts_*` (full-text inverted index, stop words, synonyms, BM25 stats), `geo` (R-tree + geohash), `webhooks`, `schemas`, `auth_users` / `auth_apikeys` / `auth_perms` / `auth_groups`, `automation`, `automation_log`, `binlog` (replication), `mcp_apikeys`, `memory_*` (RAG sessions), and `ttl_*`. Each is initialised by its owning module's `EnsureBucket()` call at startup.

## Data Flow

### Add/Update Document

```mermaid
sequenceDiagram
    participant C as Client
    participant S as Server
    participant DB as BoltDB
    
    C->>S: POST /v1/add
    S->>S: Validate request
    S->>DB: Begin transaction
    S->>DB: Check existing doc
    S->>DB: Update/Create doc
    S->>DB: Update metadata indices
    S->>DB: Create revision
    S->>DB: Update bykey mapping
    S->>DB: Commit transaction
    S->>C: Return document
    S->>S: Trigger hooks (if configured)
```

### Search Documents

```mermaid
sequenceDiagram
    participant C as Client
    participant S as Server
    participant DB as BoltDB
    
    C->>S: POST /v1/search
    S->>S: Parse filters
    S->>DB: Begin read transaction
    
    alt No filters
        S->>DB: Scan all docs in collection
    else With filters
        S->>DB: Scan metadata indices
        S->>S: Intersect results (AND logic)
        S->>DB: Fetch matching docs
    end
    
    S->>S: Sort results
    S->>S: Apply pagination
    S->>C: Return documents
```

### Hybrid / FTS / Vector / Geo search

These four search subsystems each have a dedicated guide — this file does not duplicate the algorithm or API details:

- **[SEARCH.md](SEARCH.md)** — full-text search modes, BM25/BM25F/PMISparse scoring, multi-language stemming, fuzzy/proximity, vector index algorithms (Flat, HNSW, IVF, PQ, OPQ, SQ, BQ), quantization
- **[PMISPARSE.md](PMISPARSE.md)** — the BM25 + PPMI two-phase ranker
- **[RAG-PIPELINE.md](RAG-PIPELINE.md)** — hybrid search (alpha blending and Reciprocal Rank Fusion), retrieval-augmented generation patterns
- **[GEOSEARCH.md](GEOSEARCH.md)** — R-tree and geohash indexes, radius / bbox queries, composition with FTS and vector
- **[EMBEDDING_PROVIDERS.md](EMBEDDING_PROVIDERS.md)** — OpenAI / Ollama / Cohere / Voyage configuration

### Get Document with Templating

```mermaid
sequenceDiagram
    participant C as Client
    participant S as Server
    participant DB as BoltDB
    
    C->>S: POST /v1/get (with env)
    S->>DB: Lookup by key+lang
    S->>DB: Fetch document
    S->>S: Apply template substitution
    Note over S: Replace %%var%% with env values
    S->>C: Return processed document
```

## Key Design Decisions

### 1. Deterministic IDs

Documents are identified by: `{collection}|{key}|{lang}`

**Benefits**:
- Predictable IDs
- Natural deduplication
- Easy to reason about
- No need for separate ID generation

### 2. Metadata as Multi-Value Maps

Metadata: `map[string][]string`

**Benefits**:
- Flexible schema
- Multiple values per key (tags, categories)
- Easy to query and filter
- Indexed for performance

### 3. Prefix-Based Indexing

Index keys: `meta|{collection}|{key}|{value}|{docID}`

**Benefits**:
- Fast prefix scans in BoltDB
- Efficient range queries
- No need for secondary indices
- Automatic sorting

### 4. Revision History

Every update creates a new revision with timestamp.

**Benefits**:
- Full audit trail
- Point-in-time recovery
- Change tracking
- Can be truncated to save space

### 5. Embedded Database (BoltDB)

**Benefits**:
- No external dependencies
- Single file storage
- ACID transactions
- Fast local access
- Easy backup/restore

**Trade-offs**:
- Single-writer (not an issue for most use cases)
- Not distributed
- Limited to single machine

## Access Modes

### Read Mode (`read`)
- Only GET operations allowed
- Write operations return 403
- Useful for read replicas

### Write Mode (`write`)
- Only write operations allowed
- Rarely used in practice

### Read-Write Mode (`wr`)
- All operations allowed
- Default and recommended mode

## Extension Points

### Webhooks, Automation, Custom MCP tools

MDDB exposes three layered extension mechanisms — each documented in its own file:

- **Webhooks** — HTTP webhooks fired on document events (`doc.added`, `doc.updated`, `doc.deleted`, batch events). Per-collection scoping, retry with backoff, payload signing. See [Automations](AUTOMATIONS.md) for configuration.
- **[AUTOMATIONS.md](AUTOMATIONS.md)** — triggers, crons, conditional rules, sentiment analysis, template variables. Configurable via REST/gRPC/MCP.
- **[CUSTOM-TOOLS.md](CUSTOM-TOOLS.md)** — YAML-defined custom MCP tools that wrap built-in actions (`semantic_search`, `search_documents`, `full_text_search`, `fts_languages`) with domain-specific defaults so AI agents see purpose-built tools instead of generic primitives.

## Performance Characteristics

### Read Performance
- **Get by key**: O(log n) - BoltDB B+tree lookup
- **Search with metadata**: O(m * log n) - where m = matching documents
- **Full collection scan**: O(n) - linear scan

### Write Performance
- **Add/Update**: O(log n + m) - where m = metadata keys
- **Index updates**: O(k) - where k = number of metadata values

### Storage
- **Document size**: Typically 1-100 KB
- **Metadata overhead**: ~100 bytes per key-value pair
- **Revision overhead**: Full document copy per revision
- **Index overhead**: ~50 bytes per indexed value

## Scalability Considerations

### Vertical Scaling
- BoltDB performs well with SSDs
- Memory-map for faster reads
- Single-writer limitation

### Horizontal Scaling
- Run multiple read-only instances
- Single write instance
- File-based replication
- Consider sharding by collection

### Database Size
- Suitable for: 10K - 1M documents
- Document size: < 1 MB each
- Total DB size: < 10 GB recommended
- Regular revision truncation important

## Security Model

This section describes the *layers* that gate every request reaching the storage engine. Per-feature usage (env vars, config files, recipes) lives in the dedicated guides linked at the bottom — and the version history for each layer lives in [CHANGELOG.md](https://github.com/tradik/mddb/blob/main/CHANGELOG.md), not here.

### Trust boundaries

A request hitting MDDB passes through up to four trust gates in order; each is independently configurable and may be disabled:

1. **Transport** — TCP+TLS, TCP plaintext, or Unix Domain Socket. TLS terminates inside MDDB (`buildServerTLSConfig` in [services/mddbd/tls_config.go](https://github.com/tradik/mddb/blob/main/services/mddbd/tls_config.go)). UDS is authenticated by filesystem ownership (`0600`) instead of TLS.
2. **Peer authentication** *(optional)* — mTLS verifies the client certificate against a configured CA bundle. The server only learns "this peer's cert chains to a trusted CA"; it does not impose identity semantics on the cert subject.
3. **Application authentication** — JWT bearer token or API key. Validated in HTTP / gRPC / GraphQL middleware ([services/mddbd/auth_middleware.go](https://github.com/tradik/mddb/blob/main/services/mddbd/auth_middleware.go), [auth_grpc.go](https://github.com/tradik/mddb/blob/main/services/mddbd/auth_grpc.go), [graphql_handler.go](https://github.com/tradik/mddb/blob/main/services/mddbd/graphql_handler.go)). On success, claims are written to the request context.
4. **Authorization** — every handler / resolver that touches a collection calls `AuthManager.CheckPermission(ctx, collection, op)`, which resolves the caller's effective permissions through both direct user grants and group membership. Operation modes are also gated *per protocol* (`MDDB_MCP_MODE`, `MDDB_API_MODE`, `MDDB_GRPC_MODE`, `MDDB_HTTP3_MODE`) — a single deployment can serve read-write to gRPC and read-only to MCP, for example.

The MCP transport adds a fifth gate above its protocol entry point — its own API-key store and per-key rate limiter ([services/mddbd/mcp_apikeys.go](https://github.com/tradik/mddb/blob/main/services/mddbd/mcp_apikeys.go)) — so an MCP client can be issued credentials independently from the main JWT/API-key store.

When `AuthManager` is `nil` (auth disabled, the default for an unconfigured deployment) gates 3 and 4 short-circuit to allow-all. mTLS (gate 2) is independent and can be enabled without enabling JWT.

### Out of scope for the engine

Three things MDDB *deliberately* does not do; they are the operator's responsibility:

- **Encryption at rest** — BoltDB writes plaintext to disk. Use filesystem-level encryption (LUKS, FileVault, dm-crypt) or full-volume encryption.
- **Backup blob encryption** — `/v1/backup` produces a plaintext copy of the database file. Encrypt the resulting blob before uploading to remote storage.
- **Public-internet hardening** — even with TLS and JWT enabled, prefer to bind to a private interface or a UDS path and front MDDB with a reverse proxy that adds WAF, rate limit, and DDoS protection.

### Reference docs

- [AUTHENTICATION.md](AUTHENTICATION.md) — JWT, API keys, RBAC, group permissions, env vars, recipes
- [TLS.md](TLS.md) — HTTPS + mTLS setup, openssl recipes, deployment patterns, troubleshooting
- [config.md](config.md#unix-domain-socket-transport) — UDS transport, env-var reference for `MDDB_HTTP_ADDR=unix:...`
- [DEPLOYMENT.md](DEPLOYMENT.md) — production hardening checklist
- [CHANGELOG.md](https://github.com/tradik/mddb/blob/main/CHANGELOG.md) — when each layer landed and how it has evolved

4. **Access Control**:
   - Implement collection-level permissions
   - Add user roles
   - Audit logging

## Monitoring & Observability

### Metrics to Track
- Request rate per endpoint
- Response times
- Database size
- Number of documents
- Number of revisions
- Error rates

### Logging
- Request/response logging
- Error logging
- Audit trail for writes
- Performance logging

### Health Checks
- Database connectivity
- Disk space
- Memory usage
- Response time

## What's next

Delivered work is recorded in the [changelog](https://github.com/tradik/mddb/blob/main/CHANGELOG.md), and requests live in [issues](https://github.com/tradik/mddb/issues). There is no separate roadmap file: the one that existed described v2.9 as current three releases later, and listed encryption at rest, the audit log and multi-tenancy as planned after all three had shipped.

