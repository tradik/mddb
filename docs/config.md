---
title: "MDDB Configuration Reference"
slug: "docs/config"
description: "Complete MDDB configuration reference: every environment variable and config key for storage, search, auth, TLS, replication and telemetry."
status: publish
---

# MDDB Configuration Reference

Complete reference for all MDDB configuration parameters.

**Precedence order:** CLI flags > environment variables > YAML config file > defaults

## Table of Contents

- [General / Core](#general--core)
- [HTTP Server](#http-server)
- [gRPC Server](#grpc-server)
- [MCP (Model Context Protocol)](#mcp-model-context-protocol)
- [HTTP/3 (QUIC)](#http3-quic)
- [Authentication](#authentication)
- [Embedding / Vector Search](#embedding--vector-search)
- [Vector Index](#vector-index)
- [Full-Text Search (FTS)](#full-text-search-fts)
- [Temporal Tracking](#temporal-tracking)
- [Spell Correction](#spell-correction)
- [Compression](#compression)
- [Replication](#replication)
- [Automation & Triggers](#automation--triggers)
- [GraphQL](#graphql)
- [CLI Flags](#cli-flags)
- [YAML Config File](#yaml-config-file)

---

## General / Core

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_CONFIG` | `""` | string | Path to YAML config file (also `-config` / `-c` CLI flag) |
| `MDDB_PATH` | `"mddb.db"` | string | Path to the BoltDB database file (also `--db` CLI flag, `database.path` in YAML) |
| `MDDB_MODE` | `"wr"` | string | Access mode: `"read"`, `"write"`, or `"wr"` (read+write) (also `--mode` CLI flag, `database.mode` in YAML) |
| `MDDB_PANEL_MODE` | `"internal"` | string | Panel mode: `"internal"` (CORS enabled) or `"external"` (reverse proxy) |
| `MDDB_CORS_ORIGINS` | `"*"` | string | CORS allowlist (SEC-008): comma-separated exact origins, e.g. `https://app.example.com,https://admin.example.com`. Only a matching request `Origin` is echoed (with `Vary: Origin`); others get no `Access-Control-Allow-Origin`. `*` = wildcard (read-only, no credentials). Takes precedence over `MDDB_CORS_ORIGIN`. |
| `MDDB_CORS_ORIGIN` | `"*"` | string | Legacy single-origin form of `MDDB_CORS_ORIGINS` (kept for compatibility). Prefer `MDDB_CORS_ORIGINS`. |
| `MDDB_MAX_BODY_BYTES` | `33554432` (32 MB) | int | Global cap on HTTP request body size; larger requests are rejected before parsing (streaming endpoints keep their own dedicated caps) |
| `MDDB_OUTBOUND_ALLOWLIST` | `""` | string | Comma-separated hostnames the server may reach with outbound HTTP (SSRF egress control for `import-url`, webhooks, remote publishing) — see [SECURITY.md](SECURITY.md#outbound-request-egress-controls-ssrf) |
| `MDDB_OUTBOUND_ALLOW_PRIVATE` | `"false"` | bool | Allow outbound HTTP requests to private/loopback IP ranges (disabled by default to block SSRF pivoting) |
| `MDDB_WIKI_MAX_PAGES` | `500000` | int | Maximum pages processed by a single `/v1/import-wiki` run (DoS guard) |
| `MDDB_WIKI_MAX_DECOMPRESSED_BYTES` | `4294967296` (4 GiB) | int | Maximum decompressed XML volume for a wiki import (zip-bomb guard) |
| `MDDB_SHUTDOWN_TIMEOUT_SEC` | `15` | int | Deadline for the ordered shutdown; a subsystem that does not finish is named in the log and left behind |
| `MDDB_SEARCH_MAX_CONCURRENT` | CPU count | int | Maximum heavy queries (FTS, vector, hybrid, aggregate) running at once. Further queries queue, then receive `503` with `Retry-After`. `0` disables the limit |
| `MDDB_SEARCH_QUEUE_TIMEOUT_MS` | `2000` | int | How long a query waits for a slot before the `503` |
| `MDDB_SEARCH_CACHE_SIZE` | `500` | int | Maximum cached search responses; `0` disables the cache regardless of what a request asks for. Caching is opt-in per request via `cacheTtl` — see [SEARCH.md](SEARCH.md#result-caching) |
| `MDDB_METRICS` | `"true"` | bool | Enable Prometheus-compatible `/metrics` endpoint |
| `MDDB_SEARCH_STATS` | `"true"` | bool | Include `searchStats` in search responses |

---

## HTTP Server

| Env Var | Default | Type | CLI Flag | Description |
|---------|---------|------|----------|-------------|
| `MDDB_HTTP_ENABLED` | `true` | bool | `--http-enabled` | Enable/disable the HTTP API server |
| `MDDB_HTTP_ADDR` | `":11023"` | string | `--http-addr` | HTTP API listen address |
| `MDDB_HTTP_PORT` | — | string | — | Plain port number (converted to `":PORT"`) |
| `MDDB_ADDR` | `":11023"` | string | — | Legacy alias for `MDDB_HTTP_ADDR` |

---

## gRPC Server

| Env Var | Default | Type | CLI Flag | Description |
|---------|---------|------|----------|-------------|
| `MDDB_GRPC_ENABLED` | `true` | bool | `--grpc-enabled` | Enable/disable the gRPC server |
| `MDDB_GRPC_ADDR` | `":11024"` | string | `--grpc-addr` | gRPC listen address |
| `MDDB_GRPC_PORT` | — | string | — | Plain port number (converted to `":PORT"`) |

---

## MCP (Model Context Protocol)

| Env Var | Default | Type | CLI Flag | Description |
|---------|---------|------|----------|-------------|
| `MDDB_MCP_ENABLED` | `true` | bool | `--mcp-enabled` | Enable/disable the MCP server |
| `MDDB_MCP_ADDR` | `":9000"` | string | `--mcp-addr` | MCP HTTP listen address |
| `MDDB_MCP_PORT` | — | string | — | Plain port number (converted to `":PORT"`) |
| `MDDB_MCP_STDIO` | `false` | bool | `--mcp-stdio` | Run MCP in stdio mode (for Claude Desktop) |
| `MDDB_MCP_DOMAIN` | `""` | string | — | MCP server domain |
| `MDDB_MCP_CONFIG` | `""` | string | — | Path to YAML with custom MCP tool definitions |
| `MDDB_MCP_BUILTIN_TOOLS` | `true` | bool | — | Set to `false` to expose only custom YAML tools |
| `MDDB_MCP_MODE` | `"wr"` | string | — | MCP access mode: `"read"`, `"write"`, or `"wr"` |

### MCP API Key Authentication

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_MCP_API_KEY_ENABLED` | `false` | bool | Enable API key authentication for MCP endpoints |
| `MDDB_MCP_API_KEYS` | `""` | string | Static API keys: `key1:name1,key2:name2` |
| `MDDB_MCP_API_KEY_CACHE_TTL` | `"5m"` | duration | Cache TTL for dynamic API key lookups |

### MCP Rate Limiting

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_MCP_RATE_LIMIT_ENABLED` | `false` | bool | Enable per-client rate limiting for MCP |
| `MDDB_MCP_RATE_LIMIT_REQUESTS` | `100` | int | Maximum requests per window |
| `MDDB_MCP_RATE_LIMIT_WINDOW` | `"60s"` | duration | Rate limit time window |
| `MDDB_MCP_RATE_LIMIT_BURST` | `20` | int | Maximum burst size |
| `MDDB_MCP_RATE_LIMIT_BY` | `"ip"` | string | Rate limit key: `"ip"`, `"api_key"`, or `"session"` |

### MCP Logging

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_MCP_LOGGING_ENABLED` | `false` | bool | Enable structured JSON audit logs for MCP requests |
| `MDDB_MCP_LOGGING_LEVEL` | `"info"` | string | Minimum log level: `"debug"`, `"info"`, `"warn"`, `"error"` |

---

## HTTP/3 (QUIC)

| Env Var | Default | Type | CLI Flag | Description |
|---------|---------|------|----------|-------------|
| `MDDB_HTTP3_ENABLED` | `false` | bool | `--http3-enabled` | Enable HTTP/3 (QUIC) server |
| `MDDB_HTTP3_ADDR` | `":11443"` | string | `--http3-addr` | HTTP/3 listen address |
| `MDDB_HTTP3_PORT` | — | string | — | Plain port number (converted to `":PORT"`) |
| `MDDB_EXTREME` | — | bool | — | Legacy alias for `MDDB_HTTP3_ENABLED` |

---

## Authentication

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_AUTH_ENABLED` | `false` | bool | Enable JWT-based authentication |
| `MDDB_AUTH_JWT_SECRET` | `""` | string | JWT signing secret (**required** when auth is enabled) |
| `MDDB_AUTH_JWT_EXPIRY` | `"24h"` | duration | JWT token expiry duration |
| `MDDB_AUTH_ADMIN_USERNAME` | `"admin"` | string | Default admin username |
| `MDDB_AUTH_ADMIN_PASSWORD` | `""` | string | Default admin password |

---

## Incident Events (ISO 27001 / SOC 2)

Security and operational incidents are delivered through the existing `/v1/webhooks` subscription system. A webhook that registers for one of the incident event names receives the same JSON envelope as document-lifecycle events, with the event-specific payload in `detail`.

| Event | Fired when | `detail` payload |
|-------|------------|------------------|
| `security.auth_failure_burst` | N auth failures from the same `actor@ip` inside the window | `{actor, ip, count, windowSec}` |
| `security.rate_limit_exceeded` | A request is rejected by the HTTP/gRPC rate limiter | `{clientId, transport}` |
| `ops.replication_lag_high` | Follower lag exceeds threshold on poll | `{lagMs, thresholdMs}` |
| `ops.panic_recovered` | An HTTP handler panicked and was recovered by the middleware | `{method, path, panic, ip}` |
| `ops.disk_usage_high` | DB filesystem used-% ≥ threshold | `{path, usedBytes, totalBytes, usedPct, thresholdPct}` |

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_INCIDENT_AUTH_THRESHOLD` | `10` | int | Failures per window before firing. |
| `MDDB_INCIDENT_AUTH_WINDOW_SEC` | `60` | int | Sliding-window length. |
| `MDDB_INCIDENT_AUTH_COOLDOWN_SEC` | `300` | int | Quiet period after a burst before the same `actor@ip` can refire. |
| `MDDB_INCIDENT_LAG_THRESHOLD_MS` | `5000` | int | Replication-lag threshold. |
| `MDDB_INCIDENT_LAG_INTERVAL_SEC` | `30` | int | Poll interval. |
| `MDDB_INCIDENT_LAG_COOLDOWN_SEC` | `300` | int | Cool-down after a lag event. |
| `MDDB_INCIDENT_DISK_THRESHOLD_PCT` | `85` | int (1–100) | Disk-usage threshold. |
| `MDDB_INCIDENT_DISK_INTERVAL_SEC` | `300` | int | Poll interval. |
| `MDDB_INCIDENT_DISK_COOLDOWN_SEC` | `3600` | int | Cool-down after a disk event. |

Registering a webhook for incident events:

```bash
curl -X POST localhost:11023/v1/webhooks \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://ops.example.com/mddb-incidents",
    "events": ["security.auth_failure_burst","ops.panic_recovered","ops.disk_usage_high"]
  }'
```

Retries, backoff (0s / 1s / 5s / 15s) and `X-MDDB-Event` / `X-MDDB-Webhook-ID` headers are shared with the existing document-lifecycle delivery path.

---

## At-Rest Encryption (ISO 27001 / SOC 2)

Opt-in per-collection AES-256-GCM encryption for documents and revisions. Activation requires **both** a process-wide key and a per-collection flag — an operator who does neither pays zero runtime cost and stores plaintext like today.

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_ENCRYPTION_KEY` | *(unset)* | base64 string | 32 bytes of random key material, base64-encoded. Unset = encryption disabled globally. Invalid base64 or wrong length aborts startup. |
| `MDDB_ENCRYPTION_KEY_ID` | `1` | integer 1..255 | Identifier stamped on every new ciphertext (V2 wire format). Pick a fresh value when you rotate so the new entries are distinguishable from legacy ones. |
| `MDDB_ENCRYPTION_KEYS_PREVIOUS` | *(unset)* | JSON array | Read-only previous keys for rotation: `[{"id":1,"key":"<base64>"}, ...]`. KeyID 0 is reserved (legacy V1 marker); collisions with the primary keyID abort startup. |

Enabling encryption for a collection:

```bash
curl -X PUT localhost:11023/v1/collection-config \
  -H "Authorization: Bearer $ADMIN_JWT" \
  -d '{"collection":"secrets","encrypted":true}'
```

Details:

- **Wire format** per stored value: `MDDB_ENC_V1\x00` (12 B magic) + 12 B nonce + AES-256-GCM ciphertext & auth tag.
- **Backward compat**: legacy plaintext documents remain readable even after a collection is flipped to `encrypted=true`. New writes use ciphertext, old reads transparently passthrough because the magic prefix is absent.
- **Scope**: only the `docs` and `rev` buckets carry ciphertext. FTS inverted indexes and vector embeddings remain plaintext because they are queryable structures — encrypting them would break search. Document this in your threat model.
- **Key loss is terminal**: losing `MDDB_ENCRYPTION_KEY` makes the corresponding collections unrecoverable. Store the key in an HSM / secret manager and keep an offline escrow.
- **Startup safety**: if a collection has `encrypted=true` but `MDDB_ENCRYPTION_KEY` is missing, the server refuses to start — writing plaintext into a collection that claims to be encrypted is treated as a compliance failure, not a warning.
- **Bootstrap key**: `openssl rand -base64 32`.

Generate a fresh key:

```bash
export MDDB_ENCRYPTION_KEY="$(openssl rand -base64 32)"
```

### Key Rotation (2.9.16+)

The 2.9.16 wire format **V2** prefixes every ciphertext with a 1-byte `keyID` so the encryptor can hold a primary plus any number of read-only previous keys. V1 (2.9.15) ciphertexts continue to decrypt — non-breaking upgrade.

Rotation procedure:

```bash
# 1. Capture the current key.
old_key="$MDDB_ENCRYPTION_KEY"
old_id="${MDDB_ENCRYPTION_KEY_ID:-1}"

# 2. Generate the new key.
export MDDB_ENCRYPTION_KEY="$(openssl rand -base64 32)"
export MDDB_ENCRYPTION_KEY_ID=2

# 3. Keep the old key around so historical ciphertexts decrypt.
export MDDB_ENCRYPTION_KEYS_PREVIOUS="[{\"id\":$old_id,\"key\":\"$old_key\"}]"

# 4. Restart the server. Old documents continue to read; new writes
#    are sealed under keyID=2.

# 5. Run a re-encryption pass to migrate historical entries.
curl -X POST localhost:11023/v1/encryption/rotate \
  -H "Authorization: Bearer $ADMIN_JWT" \
  -d '{"collection":""}'  # empty = all collections

# 6. Watch progress.
curl localhost:11023/v1/encryption/status -H "Authorization: Bearer $ADMIN_JWT"

# 7. Once status shows withLegacy=0 across collections, drop the
#    previous key from the env (and the secret store).
unset MDDB_ENCRYPTION_KEYS_PREVIOUS
```

The admin panel exposes the same workflow under **Sidebar → Encryption**: keyID, per-collection coverage, "Start rotation" button, and a job table.

---

## Audit Log Export (ISO 27001 / SOC 2)

The audit log persists locally to BoltDB by default. For tamper-evident, off-host retention (the auditor expectation), mirror events to an external SIEM webhook or a syslog collector. Local BoltDB remains the source of truth — exporters are best-effort.

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_AUDIT_EXPORT_WEBHOOK_URL` | *(unset)* | URL | Each audit event is POSTed as JSON to this URL with `_mddb_event_type:audit` decoration. Empty = exporter disabled. |
| `MDDB_AUDIT_EXPORT_WEBHOOK_HEADER` | *(unset)* | comma-separated list | Headers added to every request: `Authorization: Splunk xxx,X-Source: prod`. |
| `MDDB_AUDIT_EXPORT_WEBHOOK_INSECURE_TLS` | `false` | bool | Skip TLS cert verification — only for self-signed development collectors. |
| `MDDB_AUDIT_EXPORT_SYSLOG_ADDR` | *(unset)* | host:port or proto://host:port | Syslog target. UDP by default; prefix `tcp://` for TCP. |
| `MDDB_AUDIT_EXPORT_SYSLOG_FACILITY` | `local0` | facility name | RFC 5424 facility (`local0`–`local7`, `daemon`, `auth`, `authpriv`, ...). |
| `MDDB_AUDIT_EXPORT_BUFFER` | `1024` | integer | Bounded channel size per exporter. When full, oldest events are dropped (counted as `dropped`). |

Both sinks can run together; per-sink delivery counters are exposed at `GET /v1/audit/exporters` and rendered in the Security panel.

Quick recipe — Splunk HEC + papertrail-style syslog:

```bash
export MDDB_AUDIT_ENABLED=true
export MDDB_AUDIT_EXPORT_WEBHOOK_URL="https://splunk.example/services/collector/raw"
export MDDB_AUDIT_EXPORT_WEBHOOK_HEADER="Authorization: Splunk $HEC_TOKEN"
export MDDB_AUDIT_EXPORT_SYSLOG_ADDR="tcp://logs.papertrailapp.com:12345"
```

---

## Backup Path Jail (2.9.16+)

`/v1/backup` and `/v1/restore` accept a user-supplied path. Without bounds an admin (or an attacker who steals admin creds) could read or overwrite arbitrary files. The 2.9.16 jail confines every backup to a single directory.

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_BACKUP_DIR` | `./backups` | path | Directory backups are written to and restored from. Symlinks that escape the jail are rejected; absolute paths and `../` traversal are rejected; empty / NUL bytes are rejected. |
| `MDDB_GEO_DATA_DIR` | `"geodata"` | path | Directory postcode CSVs must live in. `/v1/geo-reindex` and its gRPC twin accept a name relative to it and refuse anything that resolves outside, symlinks included — the path used to be taken from the request and opened as given |

No further configuration required — the jail is always on.

---

## Production Hardening (ISO 27001 / SOC 2)

`MDDB_PRODUCTION=true` is a single switch that fails the server start unless every ISO 27001 / SOC 2 guardrail is satisfied. When unset, the guard logs a one-line warning at boot and continues with the same defaults as before — so existing deployments are unaffected.

| Env Var | Required when `MDDB_PRODUCTION=true` | Reason |
|---------|--------------------------------------|--------|
| `MDDB_AUTH_ENABLED` | `true` | A.5.15 / CC6.1 — access control |
| `MDDB_AUTH_JWT_SECRET` | ≥32 bytes | A.8.24 / CC6.7 — key strength |
| `MDDB_TLS_ENABLED` | `true` (or `MDDB_TLS_INSECURE_OK=true` as an explicit opt-out for dev) | A.8.24 / CC6.7 — encryption in transit |
| `MDDB_CORS_ORIGIN` | explicit origin list (not `*`) | A.8.23 / CC6.6 — web-origin segmentation |
| `MDDB_AUDIT_ENABLED` | `true` | A.8.15 / CC7.2 — audit trail |
| `MDDB_RATE_LIMIT_ENABLED` | `true` | A.5.30 / CC6.6 — resource-exhaustion protection |

On a successful production start the server logs:

```
✓ Production guards satisfied (ISO 27001 / SOC 2)
```

When a requirement is missing, startup is aborted with a line-by-line breakdown pointing at each offending env var.

---

## Rate Limiting (HTTP + gRPC)

Shared sliding-window limiter covering both HTTP and gRPC. Separate from the pre-existing `MDDB_MCP_RATE_LIMIT_*` budget, which continues to apply to the MCP endpoints only.

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_RATE_LIMIT_ENABLED` | `false` | bool | Enable the limiter. When off, both HTTP and gRPC are passthrough. |
| `MDDB_RATE_LIMIT_REQUESTS` | `100` | int | Sustained requests per window. |
| `MDDB_RATE_LIMIT_WINDOW` | `60` | int seconds | Window length. |
| `MDDB_RATE_LIMIT_BURST` | `50` | int | Additional allowance before a client is blocked. Effective ceiling = `REQUESTS + BURST`. |
| `MDDB_RATE_LIMIT_BY` | `"ip"` | string | `"ip"` (default) or `"user"`. `user` keys on the authenticated username and falls back to IP for anonymous traffic. |

HTTP responses carry `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`; rejected requests get `429 Too Many Requests` with `Retry-After`. gRPC rejects with `codes.ResourceExhausted`. The paths `/health`, `/v1/health`, and `/metrics` are always exempt so monitoring and load-balancer probes never trip the limiter.

---

## Audit Log (ISO 27001 / SOC 2)

Structured authentication and mutation trail persisted to a dedicated `audit` BoltDB bucket. Events are buffered and flushed asynchronously so hot-path handlers never block on disk I/O. Queryable via admin-only `GET /v1/audit`.

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_AUDIT_ENABLED` | `false` | bool | Enable the audit log. When disabled, `AuditManager` is a no-op and `/v1/audit` returns 404. |
| `MDDB_AUDIT_RETENTION_DAYS` | `90` | int | Retention window. A background trimmer runs every hour and deletes events older than the cutoff. |

Query parameters on `GET /v1/audit`: `from` / `to` (RFC3339) or `fromNanos` / `toNanos`, `actor`, `action`, `result` (`ok`/`fail`), `limit` (default 100). Response shape: `{events: [...], count, dropped}` — `dropped` counts events lost when the in-memory buffer was full.

---

## Embedding / Vector Search

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_EMBEDDING_PROVIDER` | `""` (disabled) | string | Provider: `"openai"`, `"ollama"`, `"voyage"`, `"cohere"`, or `""`. When empty and nothing is stored, MDDB probes a local Ollama once at startup — see `MDDB_EMBEDDING_AUTODETECT` |
| `MDDB_UPDATE_CHECK` | `"1"` | bool | Set to `0` to skip the startup release check. One GET to a pinned GitHub URL, carrying nothing about this installation; the answer is cached in `/health` |
| `MDDB_EMBEDDING_AUTODETECT` | `"1"` | bool | Set to `0` to skip the startup probe for a local Ollama. The probe is one `GET localhost:11434/api/tags` with a two-second budget, and only runs when no provider is configured |
| `OLLAMA_HOST` | `http://localhost:11434` | string | Where the autodetection probe looks. Accepted with or without a scheme |
| `MDDB_EMBEDDING_API_KEY` | `""` | string | API key (for openai/voyage/cohere) |
| `MDDB_EMBEDDING_API_URL` | *(see below)* | string | API base URL |
| `MDDB_EMBEDDING_MODEL` | *(see below)* | string | Embedding model name |
| `MDDB_EMBEDDING_DIMENSIONS` | *(see below)* | int | Vector dimensionality |
| `MDDB_EMBEDDING_CHUNK_ENABLED` | `true` | bool | Enable text chunking before embedding |
| `MDDB_EMBEDDING_CHUNK_SIZE` | `1500` | int | Maximum chunk size in characters |
| `MDDB_EMBEDDING_CACHE_SIZE` | `1024` | int | (v2.12.0+) Embeddings held in the query cache; `0` disables caching and restores the pre-2.12.0 path exactly |
| `MDDB_EMBEDDING_CACHE_TTL` | `3600` | int | (v2.12.0+) Cache entry lifetime in seconds |

### Provider Defaults

| Provider | `API_URL` | `MODEL` | `DIMENSIONS` |
|----------|-----------|---------|---------------|
| `openai` | `https://api.openai.com/v1` | `text-embedding-3-small` | `1536` |
| `ollama` | `http://localhost:11434` | `nomic-embed-text` | `768` |
| `voyage` | `https://api.voyageai.com/v1` | `voyage-3` | `1024` |
| `cohere` | `https://api.cohere.ai/v1` | `embed-english-v3.0` | `1024` |

---

## Vector Index

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_VECTOR_DEFAULT_ALGORITHM` | `"flat"` | string | Default algorithm: `"flat"`, `"hnsw"`, `"ivf"`, `"pq"`, `"opq"`, `"sq"`, `"bq"` |
| `MDDB_VECTOR_BQ_RERANK_FACTOR` | `10` | int | Binary quantization rerank factor |
| `MDDB_VECTOR_PARALLEL_WORKERS` | `NumCPU` (max 16) | int | Number of goroutines for parallel vector scoring |
| `MDDB_VECTOR_PARALLEL_MIN_SIZE` | `2048` | int | Minimum collection size to enable parallel search |

---

## MCP Server Info

Customize the MCP server profile returned in the `initialize` response. Useful for identifying your server to LLM clients.

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_MCP_SERVER_NAME` | `"mddbd"` | string | Server name shown to MCP clients |
| `MDDB_MCP_SERVER_DESCRIPTION` | `""` | string | Human-readable server description |
| `MDDB_MCP_SERVER_VENDOR` | `""` | string | Organization / vendor name |
| `MDDB_MCP_SERVER_HOMEPAGE` | `""` | string | URL to server documentation or homepage |
| `MDDB_MCP_INSTRUCTIONS` | `""` | string | System prompt for LLM — tells the AI how to use this server |

Or via YAML config:

```yaml
mcp:
  serverInfo:
    name: "my-knowledge-base"
    description: "Company internal documentation"
    vendor: "Acme Corp"
    homepage: "https://docs.acme.com"
  instructions: |
    This is the company knowledge base. Use search_documents to find
    relevant articles before answering questions. Always cite document
    keys in your responses. Prefer the 'docs' collection for technical
    questions and 'blog' for product updates.
```

---

## Server-Sent Events (SSE)

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_SSE_ENABLED` | `true` | bool | Enable SSE event stream at `/v1/events` |
| `MDDB_SSE_MAX_CLIENTS` | `1000` | int | Maximum total concurrent SSE connections |
| `MDDB_SSE_MAX_PER_IP` | `5` | int | Maximum concurrent SSE connections per IP address |

---

## TLS / HTTPS / mTLS

See [TLS.md](TLS.md) for the full setup guide (cert generation, recipes, troubleshooting).

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_TLS_ENABLED` | `false` | bool | Enable built-in TLS (HTTPS) on the HTTP listener |
| `MDDB_TLS_CERT` | `""` | string | Path to server TLS certificate (PEM) |
| `MDDB_TLS_KEY` | `""` | string | Path to server TLS private key (PEM) |
| `MDDB_TLS_CLIENT_CA` | `""` | string | Path to PEM bundle of trusted client CAs — enables **mTLS** when set |
| `MDDB_TLS_CLIENT_AUTH` | `"require"` | string | mTLS mode when `MDDB_TLS_CLIENT_CA` is set: `require` (reject anonymous clients) or `request` (verify only if cert presented) |

`MinVersion` is pinned to TLS 1.2. mTLS is automatically skipped on UDS listeners (filesystem permissions already authenticate the local peer).

---

## Unix Domain Socket transport

`MDDB_HTTP_ADDR` and `MDDB_GRPC_ADDR` accept either a TCP `host:port` (default) or a Unix Domain Socket address of the form `unix:/absolute/path.sock`. The server creates the socket with owner-only `0600` permissions, removes any stale socket file from a previous run, and unlinks the socket on graceful shutdown.

```bash
# HTTP API on a UDS, gRPC on TCP, no public HTTP port
MDDB_HTTP_ADDR=unix:/var/run/mddb/http.sock \
MDDB_GRPC_ADDR=:11024 \
./mddbd
```

TLS is automatically disabled on UDS listeners (peer is authenticated by filesystem permissions; API keys / JWT still apply on top). Per-IP rate limits in SSE collapse to a single bucket on UDS — apply application-level rate limiting if you need to differentiate clients.

Clients with native UDS support:

| Client | Address form |
|--------|--------------|
| Python (`services/python-extension/mddb.py`) | `MDDB.connect('unix:/var/run/mddb/http.sock')` |
| PHP (`services/php-extension/mddb.php`) | `mddb::connect('unix:/var/run/mddb/http.sock')` |
| Python gRPC (`clients/python/`) | `grpc.insecure_channel('unix:/var/run/mddb/grpc.sock')` |
| Node gRPC (`clients/nodejs/`) | `new MDDBClient('unix:/var/run/mddb/grpc.sock', creds)` |
| `curl` | `curl --unix-socket /var/run/mddb/http.sock http://localhost/v1/healthz` |

---

## Logging

The operational log is structured. `text` is the readable default for a
terminal; `json` emits one object per line — `time` (RFC 3339), `level`, `msg`
and the event's own fields — which is what a collector wants, so the container
image sets it. Severity lives in the `level` field, never in the message, so
`level=error` is a filter rather than a regular expression over prose.

This is separate from the **audit trail**, which has its own retention,
RFC 5424 syslog export and SIEM webhooks — see
[SECURITY.md](SECURITY.md) and the `MDDB_AUDIT_*` variables.

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_LOG_FORMAT` | `text` (`json` in the Docker image) | string | Output encoding: `text` or `json` |
| `MDDB_LOG_LEVEL` | `info` | string | Minimum level emitted: `debug`, `info`, `warn`, `error` |
| `MDDB_ACCESS_LOG` | `false` | bool | One line per HTTP request: method, path, status, bytes, elapsed, remote address. The query string is deliberately omitted — search endpoints carry user content there |

Access log lines take their level from the status: 5xx is `error`, 4xx is
`warn`, everything else `info`.

```json
{"time":"2026-08-21T14:03:11.412Z","level":"ERROR","msg":"embedding failed after all attempts","collection":"docs","docID":"intro","chunk":2,"err":"context deadline exceeded"}
```

---

## Profiling

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_PPROF_ENABLED` | `false` | bool | Enable pprof profiling endpoints at `/debug/pprof/` |

---

## HTTP Connection Pool

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_HTTP_POOL_MAX_IDLE` | `100` | int | Max idle connections in shared HTTP pool |
| `MDDB_HTTP_POOL_MAX_PER_HOST` | `10` | int | Max idle connections per target host |
| `MDDB_HTTP_POOL_IDLE_TIMEOUT` | `90` | int | Idle connection timeout in seconds |

---

## Full-Text Search (FTS)

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_FTS_STEMMING` | `true` | bool | Enable Porter stemming for FTS indexing and queries |
| `MDDB_FTS_SYNONYMS` | `true` | bool | Enable synonym expansion in FTS queries |
| `MDDB_FTS_DEFAULT_LANG` | `"en"` | string | Default language for stemming and stop words |

---

## Temporal Tracking

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_TEMPORAL` | `false` | bool | Enable document lifecycle event tracking (create/update/access) |

When enabled, provides endpoints: `POST /v1/temporal/query`, `POST /v1/temporal/hot`, `POST /v1/temporal/histogram`. Per-collection opt-in via Collection Settings (`trackAccess`, `trackHot`).

---

## Spell Correction

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_SPELL` | `false` | bool | Enable SymSpell-style spell checker for FTS queries |

When enabled, provides endpoints: `POST /v1/spell-suggest`, `POST /v1/spell-cleanup`, `GET/PUT/DELETE /v1/spell-dictionary`. Enable `spellCorrect: true` on a collection for auto-correction.

---

## Compression

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_COMPRESSION_ENABLED` | `true` | bool | Enable adaptive document compression |
| `MDDB_COMPRESSION_SMALL_THRESHOLD` | `1024` | int (bytes) | Below this: Snappy compression |
| `MDDB_COMPRESSION_MEDIUM_THRESHOLD` | `10240` | int (bytes) | Above this: Zstd compression |

---

## Replication

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_REPLICATION_ROLE` | `""` | string | Role: `"leader"`, `"follower"`, or `""` (standalone) |
| `MDDB_NODE_ID` | `""` | string | Unique node ID (required for replication) |
| `MDDB_REPLICATION_LEADER_ADDR` | `""` | string | Leader address for follower nodes |
| `MDDB_BINLOG_ENABLED` | `false` | bool | Enable binary log (auto-enabled for leaders) |
| `MDDB_BINLOG_PATH` | `""` | string | Custom binlog file path |

---

## Automation & Triggers

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_AUTOMATIONS` | `"enable"` | string | Set to `"disable"` to disable automation manager |
| `MDDB_AUTOMATION_LOGS` | `"enable"` | string | Set to `"disable"` to disable log storage |
| `MDDB_AUTOMATION_LOGS_TTL` | `"7d"` | duration | TTL for automation log entries |
| `MDDB_TRIGGERS` | `false` | bool | Enable automation triggers on document changes |
| `MDDB_CRONS` | `false` | bool | Enable cron scheduler for automations |

---

## GraphQL

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_GRAPHQL_ENABLED` | `false` | bool | Enable GraphQL endpoint at `/graphql` |
| `MDDB_GRAPHQL_PLAYGROUND` | `true` | bool | Enable GraphQL Playground at `/playground` |

---

## Per-Collection Configuration (v2.9.14+ additions)

Per-collection attributes are persisted via `PUT /v1/collection-config` (REST), `SetCollectionConfig` (gRPC), the `set_collection_config` MCP tool, or the Admin Panel — **not** via env vars. The settings below extend what has been configurable since earlier versions.

| Field | Default | Type | Description |
|-------|---------|------|-------------|
| `maxRevisions` | `0` | integer | (v2.9.14+) Revision retention cap per document. `0` = unlimited. When `> 0`, each `add`/`update` trims older revisions in the same BoltDB transaction so history stays capped even under high write-churn. |
| `trackAccess` | `false` | bool | Record per-read access events (needs `MDDB_TEMPORAL=true`) |
| `trackHot` | `false` | bool | Maintain a hot-docs leaderboard |
| `spellCorrect` | `false` | bool | Auto-correct FTS queries (needs `MDDB_SPELL=true`) |
| `spellLang` | `""` | string | Override spell-correction language for this collection |
| `quantization` | `"float32"` | string | Vector quantization level: `float32`, `int8`, or `int4` |
| `storageBackend` | `"boltdb"` | string | `boltdb`, `memory`, or `s3` |

**Example — set a 20-revision cap:**
```bash
curl -X PUT http://localhost:11023/v1/collection-config \
  -H 'Content-Type: application/json' \
  -d '{"collection":"blog","type":"default","maxRevisions":20}'
```

---

## Curation Rules (v2.9.14+)

Curation is data, not configuration — rules live in a dedicated bolt bucket and are managed at runtime via `/v1/curation` (see [API.md](API.md) and [SEARCH.md](SEARCH.md)). No server-level flag controls the subsystem; it's always on and has zero overhead when no rules match an incoming query.

---

## CLI Flags

| Flag | Short | Type | Description |
|------|-------|------|-------------|
| `--config` | `-c` | string | Path to YAML config file |
| `--http-enabled` | | string | Enable HTTP API (true/false) |
| `--http-addr` | | string | HTTP listen address |
| `--grpc-enabled` | | string | Enable gRPC server (true/false) |
| `--grpc-addr` | | string | gRPC listen address |
| `--mcp-enabled` | | string | Enable MCP server (true/false) |
| `--mcp-addr` | | string | MCP listen address |
| `--mcp-stdio` | | string | MCP stdio mode (true/false) |
| `--http3-enabled` | | string | Enable HTTP/3 server (true/false) |
| `--http3-addr` | | string | HTTP/3 listen address |

---

## YAML Config File

Pass via `--config config.yaml` or `MDDB_CONFIG=config.yaml`.

```yaml
# Database
path: "mddb.db"
mode: "wr"                    # read, write, wr

# HTTP Server
http:
  enabled: true
  addr: ":11023"

# gRPC Server
grpc:
  enabled: true
  addr: ":11024"

# MCP Server
mcp:
  enabled: true
  addr: ":9000"
  stdio: false
  domain: ""

# HTTP/3 (QUIC)
http3:
  enabled: false
  addr: ":11443"

# Authentication
auth:
  enabled: false
  jwtSecret: ""
  jwtExpiry: "24h"
  adminUsername: "admin"
  adminPassword: ""

# Full-Text Search
fts:
  stemmingEnabled: true
  synonymsEnabled: true

# Compression
compression:
  enabled: true
  smallThreshold: 1024
  mediumThreshold: 10240

# Vector Search
vector:
  defaultAlgorithm: "flat"
  bqRerankFactor: 10
  parallelWorkers: 0              # 0 = auto (NumCPU, max 16)
  parallelMinSize: 2048           # min collection size for parallel search

# Temporal Tracking
temporal: false

# Spell Correction
spell: false

# MCP Security
mcp:
  apiKeyEnabled: false
  apiKeys: ""
  rateLimitEnabled: false
  rateLimitRequests: 100
  rateLimitWindow: "60s"
  rateLimitBurst: 20
  rateLimitBy: "ip"
  loggingEnabled: false
```

---

**Total: 65+ environment variables** across 17 categories, 10 CLI flags, full YAML config file support.
