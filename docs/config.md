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
- [OpenTelemetry Tracing](#opentelemetry-tracing-v2189)
- [HTTP Server](#http-server)
- [gRPC Server](#grpc-server)
- [MCP (Model Context Protocol)](#mcp-model-context-protocol)
- [HTTP/3 (QUIC)](#http3-quic)
- [Authentication](#authentication)
- [Quotas and Reserves](#quotas-and-reserves-v2189)
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
| `MDDB_DISK_MIN_FREE` | `104857600` (100 MB) | int (bytes) | Free space under which the persistence check reports `low_disk_space`; `0` disables the check ([DEPLOYMENT.md](DEPLOYMENT.md#persistence-guarantees)) |
| `MDDB_PANEL_MODE` | `"internal"` | string | Panel mode: `"internal"` (CORS enabled) or `"external"` (reverse proxy) |
| `MDDB_CORS_ORIGINS` | `"*"` | string | CORS allowlist (SEC-008): comma-separated exact origins, e.g. `https://app.example.com,https://admin.example.com`. Only a matching request `Origin` is echoed (with `Vary: Origin`); others get no `Access-Control-Allow-Origin`. `*` = wildcard (read-only, no credentials). Takes precedence over `MDDB_CORS_ORIGIN`. |
| `MDDB_CORS_ORIGIN` | `"*"` | string | Legacy single-origin form of `MDDB_CORS_ORIGINS` (kept for compatibility). Prefer `MDDB_CORS_ORIGINS`. |
| `MDDB_MAX_BODY_BYTES` | `33554432` (32 MB) | int | Global cap on HTTP request body size; larger requests are rejected before parsing (streaming endpoints keep their own dedicated caps) |
| `MDDB_OUTBOUND_ALLOWLIST` | `""` | string | Comma-separated hostnames the server may reach with outbound HTTP (SSRF egress control for `import-url`, webhooks, remote publishing) — see [SECURITY.md](SECURITY.md#outbound-request-egress-controls-ssrf) |
| `MDDB_OUTBOUND_ALLOW_PRIVATE` | `"false"` | bool | Allow outbound HTTP requests to private/loopback IP ranges (disabled by default to block SSRF pivoting) |
| `MDDB_WIKI_MAX_PAGES` | `500000` | int | Maximum pages processed by a single `/v1/import-wiki` run (DoS guard) |
| `MDDB_WIKI_MAX_DECOMPRESSED_BYTES` | `4294967296` (4 GiB) | int | Maximum decompressed XML volume for a wiki import (zip-bomb guard) |
| `MDDB_SHUTDOWN_TIMEOUT_SEC` | `15` | int | Deadline for the ordered shutdown, closing the database files included; a subsystem that does not finish is named in the log and the process exits anyway (v2.18.5+). A second SIGTERM or Ctrl-C exits at once |
| `MDDB_HEALTH_STALL_LIMIT` | `300` | int | Seconds a read transaction may wait to begin before `/health` answers `503` `stalled`; shorter waits answer `200` `busy` (v2.18.5+) |
| `MDDB_SEARCH_MAX_CONCURRENT` | CPU count | int | Maximum heavy queries (FTS, vector, hybrid, aggregate) running at once. Further queries queue, then receive `503` with `Retry-After`. `0` disables the limit |
| `MDDB_SEARCH_QUEUE_TIMEOUT_MS` | `2000` | int | How long a query waits for a slot before the `503` |
| `MDDB_SEARCH_MAX_LIMIT` | `1000` | int | Most results one search returns, on every surface (REST, MCP, gRPC, GraphQL) and every endpoint that takes `limit`, `topK` or `topN`: FTS, document search, autocomplete, vector, hybrid, cross-collection, code symbol, geo radius, memory recall, eval. A larger request is lowered to the cap and the response says `"limitCapped": true` with the `limit` used. A collection's `retrieval.maxTopK` can lower it further, never raise it. Also bounds FTS `facetMaxValues` (`0` = the cap). `1`–`1000000`; any other value stops startup with the value named (v2.18.10+). See [SEARCH.md](SEARCH.md#result-limits-v21810) |
| `MDDB_SEARCH_CACHE_SIZE` | `500` | int | Maximum cached search responses; `0` disables the cache regardless of what a request asks for. Caching is opt-in per request via `cacheTtl` — see [SEARCH.md](SEARCH.md#result-caching) |
| `MDDB_METRICS` | `"true"` | bool | Enable Prometheus-compatible `/metrics` endpoint |
| `MDDB_METRICS_PUBLIC` | `"false"` | bool | With `MDDB_AUTH_ENABLED=true`, serve `/metrics` without credentials (SEC-009). No effect when auth is off ([TELEMETRY.md](TELEMETRY.md)) |
| `MDDB_SEARCH_STATS` | `"true"` | bool | Include `searchStats` in search responses |

---

## OpenTelemetry Tracing (v2.18.9+)

Trace export over OTLP/HTTP JSON (OPS-024). Off unless `MDDB_OTEL_ENDPOINT` is set, and free when off. Spans, attributes and an example collector: [TELEMETRY.md](TELEMETRY.md#opentelemetry-tracing-v2189). `GET /v1/config` reports `telemetry.enabled` and the collector's scheme and host only (v2.18.10+): a path or query can carry a tenant token, and `MDDB_OTEL_HEADERS` is a secret that is never shown.

| Variable | Default | Type | Description |
|---|---|---|---|
| `MDDB_OTEL_ENDPOINT` | — | string | Collector URL, e.g. `http://otel-collector:4318`. `/v1/traces` is appended unless the path already ends in it. Unset = no tracing |
| `MDDB_OTEL_HEADERS` | — | string | Headers sent with every export, `key=value` pairs separated by commas, values percent-decoded (the `OTEL_EXPORTER_OTLP_HEADERS` format), e.g. `x-api-key=abc,authorization=Bearer%20t` |
| `MDDB_OTEL_SAMPLE_RATIO` | `1` | float | Share of new traces recorded, `0`–`1`. A request whose `traceparent` carries a sampling decision keeps it |
| `MDDB_OTEL_SERVICE_NAME` | `mddb` | string | The `service.name` resource attribute |
| `MDDB_OTEL_QUEUE_SIZE` | `2048` | int | Spans waiting for export. When full, new spans are dropped and counted in `mddb_operations_total{operation="otel_spans_dropped"}` |

---

## HTTP Server

| Env Var | Default | Type | CLI Flag | Description |
|---------|---------|------|----------|-------------|
| `MDDB_HTTP_ENABLED` | `true` | bool | `--http-enabled` | Enable/disable the HTTP API server |
| `MDDB_HTTP_ADDR` | `":11023"` | string | `--http-addr` | HTTP API listen address |
| `MDDB_API_MODE` | inherits `MDDB_MODE` | string | — | Access mode for HTTP/JSON REST and GraphQL: `read`, `write` or `wr` |
| `MDDB_HTTP_PORT` | — | string | — | Plain port number (converted to `":PORT"`) |
| `MDDB_ADDR` | `":11023"` | string | — | Legacy alias for `MDDB_HTTP_ADDR` |
| `MDDB_HTTP_READ_TIMEOUT` | `30s` | duration | — | Time allowed to read a whole request, body included. `0` = no limit. Applies to the HTTP API and the MCP HTTP listener |
| `MDDB_HTTP_WRITE_TIMEOUT` | `30s` | duration | — | Time allowed from reading the request to writing the response. `0` = no limit. A response that misses it is dropped by the connection, so it is logged as `response not delivered`. `add-batch`, `delete-batch`, `backup`, `restore`, `vector-reindex` and, since v2.18.6, `fts-reindex`, `import-url` and `import-wiki` lift it for their own response |

---

## gRPC Server

| Env Var | Default | Type | CLI Flag | Description |
|---------|---------|------|----------|-------------|
| `MDDB_GRPC_ENABLED` | `true` | bool | `--grpc-enabled` | Enable/disable the gRPC server |
| `MDDB_GRPC_ADDR` | `":11024"` | string | `--grpc-addr` | gRPC listen address |
| `MDDB_GRPC_MODE` | inherits `MDDB_MODE` | string | — | Access mode for gRPC: `read`, `write` or `wr` |
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
| `MDDB_MCP_TOOLSETS` | `all` | list | `search`, `code`, `documents`, `ingest`, `memory`, `schema`, `admin`, `geo`, `automation`, `webhooks`, `wordpress`, `all` | Built-in tool groups to expose (v2.18.8, MCP-003); tools outside them are neither listed nor callable |
| `MDDB_MCP_MODE` | `"wr"` | string | — | MCP access mode: `"read"`, `"write"`, or `"wr"` |
| `MDDB_MCP_CONFIRM_DESTRUCTIVE` | `"off"` | string | — | Ask the user through MCP elicitation before a destructive tool (`delete_collection`, `delete_documents_batch`, `delete_where`, `truncate_revisions`, `restore_backup`) runs (v2.18.9). `"off"`: never ask. `"elicit"`: ask clients that declared elicitation, serve others as before. `"require"`: also refuse clients that cannot be asked (`-32021`). An unknown value is logged and treated as `"off"`. See [MCP.md](MCP.md#confirming-destructive-tools-v2189) |
| `MDDB_MCP_PROTOCOL_VERSION` | `""` | string | — | Pin the MCP spec revision, disabling negotiation. Empty means negotiate and default to the newest revision this build implements. An unknown value stops the server at startup. |

### MCP API Key Authentication

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_MCP_API_KEY_ENABLED` | `false` | bool | Enable API key authentication for MCP endpoints |
| `MDDB_MCP_API_KEYS` | `""` | string | Static API keys: `key1:name1,key2:name2` |
| `MDDB_MCP_API_KEY_CACHE_TTL` | `"5m"` | duration | Cache TTL for dynamic API key lookups |

### MCP OAuth 2.1 (v2.18.1+)

OAuth for clients that cannot send an API key, such as claude.ai custom
connectors. Every token stands for an MCP API key; see
[MCP.md](MCP.md#oauth-21-for-claudeai-connectors-v2181).

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_MCP_OAUTH_ENABLED` | `false` | bool | Serve OAuth discovery, registration, authorization and token endpoints on the MCP listener. Requires `MDDB_MCP_API_KEY_ENABLED=true` |
| `MDDB_MCP_OAUTH_ISSUER` | `""` | string | Public base URL of the MCP listener, `https://...` (plain http only for loopback). Required |
| `MDDB_MCP_OAUTH_SECRET` | random | string | At least 32 characters; seals tokens and client IDs. Unset: random per process, so tokens stop working on restart |
| `MDDB_MCP_OAUTH_REDIRECT_URIS` | claude.ai and claude.com callbacks | string | Comma-separated redirect URIs clients may register, matched exactly. Loopback http URIs are always allowed |
| `MDDB_MCP_OAUTH_ACCESS_TTL` | `3600` | int | Access token lifetime in seconds |
| `MDDB_MCP_OAUTH_REFRESH_TTL` | `2592000` | int | Refresh token lifetime in seconds (30 days) |

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

With logging on, every `tools/call` also writes a `MCP tool call` record
(tool, `query`, `hits`/`found`, caller). Per-tool call/miss/error counters are
always exposed under `mddb_operations_total`. Details: [MCP.md](MCP.md#request-logging).

---

## HTTP/3 (QUIC)

| Env Var | Default | Type | CLI Flag | Description |
|---------|---------|------|----------|-------------|
| `MDDB_HTTP3_ENABLED` | `false` | bool | `--http3-enabled` | Enable HTTP/3 (QUIC) server |
| `MDDB_HTTP3_ADDR` | `":11443"` | string | `--http3-addr` | HTTP/3 listen address |
| `MDDB_HTTP3_MODE` | inherits `MDDB_MODE` | string | — | Access mode for HTTP/3: `read`, `write` or `wr` |
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
| `backup.failed` | A scheduled backup failed (2.18.9, [BACKUP.md](BACKUP.md#scheduled-backups)) | `{ruleId, ruleName, target, backup, error}` |
| `source.synced` | (v2.18.10+) A run of a synced source finished ([AUTOMATIONS.md](AUTOMATIONS.md#synced-sources)) | `{ruleId, ruleName, collection, source, added, updated, unchanged, deleted, failed, error?, errors?}` |
| `quota.exceeded` | (v2.18.9+) A write or embedding refused by a collection quota, the disk reserve or the vector RAM limit; at most once a minute per collection and resource. Collection-scoped webhooks receive their collection's events | `{error, code, collection, resource, limit, used, retryAfterSeconds?}` |

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
| `MDDB_BACKUP_DIR` | `./backups` | path | Directory backups are written to and restored from. Symlinks that escape the jail are rejected; absolute paths and `../` traversal are rejected; empty / NUL bytes are rejected; a target that exists and is not a regular file (the directory itself, a subdirectory) is rejected (v2.18.7+). Paths are checked and backups written through `os.Root`, so a symlink created after the check cannot lead out of the directory |
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
| `MDDB_TRUSTED_PROXIES` | the proxies' addresses — not `0.0.0.0/0` or `::/0` (unset is fine) | A.8.15 / CC7.2 — an audit IP the client cannot choose |

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
| `MDDB_TRUSTED_PROXIES` | `""` | list | Comma-separated IPs or CIDR prefixes of the reverse proxies and load balancers whose client IP header and PROXY header are believed, e.g. `127.0.0.1,10.0.0.0/8,fd00::/8`. Empty (the default) trusts nobody. An entry that is not an IP or prefix stops startup with that entry named. `0.0.0.0/0` or `::/0` trusts every client again: mddbd warns at startup and `MDDB_PRODUCTION=true` refuses it. See [Client IP and trusted proxies](#client-ip-and-trusted-proxies). |
| `MDDB_CLIENT_IP_HEADER` | `X-Forwarded-For` | string | v2.18.10+. The header a trusted proxy puts the client address in. `X-Forwarded-For` (with the `X-Real-IP` fallback) and `Forwarded` (RFC 7239) are chains; any other name (`X-Real-IP`, `CF-Connecting-IP`, `True-Client-IP`, `X-Cluster-Client-Ip`, `Client-IP`, ...) is one address. gRPC reads the same name in lower case from the metadata. A name that is not a valid HTTP header name stops startup. See [Proxies and load balancers](#proxies-and-load-balancers). |
| `MDDB_PROXY_PROTOCOL` | `off` | `off`/`on` | v2.18.10+. PROXY protocol v1 and v2 on the HTTP, gRPC and MCP listeners. When `on`, a peer in `MDDB_TRUSTED_PROXIES` **must** send a PROXY header and any other peer **must not**. Any other value stops startup. See [PROXY protocol](#proxy-protocol). |

`GET /v1/config` reports these settings under `clientAddressing` (v2.18.10+): how many trusted proxy entries there are (never their addresses), the client IP header and whether PROXY protocol is on. The admin panel shows them on the Config and System pages.

HTTP responses carry `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`; rejected requests get `429 Too Many Requests` with `Retry-After`. gRPC rejects with `codes.ResourceExhausted`. The paths `/health`, `/v1/health`, and `/metrics` are always exempt so monitoring and load-balancer probes never trip the limiter.

### Client IP and trusted proxies

One rule decides the client IP for the HTTP and gRPC rate limiters, the MCP rate limiter and access log, the HTTP access log (`client` field), the SSE per-IP connection cap, and the audit log (login attempts, writes, incident detection):

1. If the connecting peer (`RemoteAddr`, or the gRPC peer address) is **not** in `MDDB_TRUSTED_PROXIES`, the peer is the client. Every client IP header is ignored, so a client cannot pick its own rate-limit bucket or audit IP.
2. If the peer is a trusted proxy, the header named by `MDDB_CLIENT_IP_HEADER` is read (gRPC: the same name in lower case in the metadata):
   - **`X-Forwarded-For`** (the default) is read **right to left**, skipping trusted proxies; the first other address is the client. All header lines are joined in order, so a proxy that adds a line (HAProxy) and one that appends to the line (nginx) work the same. A hop that is not an IP address ends the walk. If that finds nothing, `X-Real-IP` is used when it is an IP address.
   - **`Forwarded`** (RFC 7239) is walked the same way over the `for=` parameter of each element: `for=192.0.2.60`, `for="[2001:db8:cafe::17]:4711"`, `for="198.51.100.4:_port"`. An element with `for=unknown`, an obfuscated node (`for=_hidden`) or no `for=` ends the walk. `X-Real-IP` is not consulted.
   - **Any other name** (`X-Real-IP`, `CF-Connecting-IP`, `True-Client-IP`, `X-Cluster-Client-Ip`, `Client-IP`, ...) holds one address set by the nearest proxy. The last line of that header is used; it must be one IP address (a port is dropped). A list or anything else is ignored.
3. If the header yields nothing, the peer is the client.

Addresses are canonical: the port is dropped, IPv6 is lower-case and compressed, and IPv4-mapped IPv6 (`::ffff:1.2.3.4`) becomes `1.2.3.4`. A Unix-socket peer keeps its socket name.

**Behind a reverse proxy**, list the proxy's address, or else every client looks like the proxy and shares one bucket:

```bash
export MDDB_TRUSTED_PROXIES=127.0.0.1,172.16.0.0/12   # the proxy on the host or the Docker network
export MDDB_CLIENT_IP_HEADER=X-Forwarded-For          # the default; see the table below for your proxy
```

The first time a client IP header arrives from a peer that is not trusted, mddbd logs one warning naming that peer and the header — the sign that `MDDB_TRUSTED_PROXIES` is missing. `0.0.0.0/0` or `::/0` in `MDDB_TRUSTED_PROXIES` makes every client a trusted proxy, which undoes all of the above: mddbd logs a warning at startup, and `MDDB_PRODUCTION=true` refuses to start.

### PROXY protocol

Layer-4 load balancers (HAProxy, AWS NLB, nginx `stream`, Traefik TCP routers) cannot add an HTTP header; they put the client address in a [PROXY protocol](https://www.haproxy.org/download/3.2/doc/proxy-protocol.txt) header in front of the connection instead. `MDDB_PROXY_PROTOCOL=on` reads it, v1 (text) and v2 (binary), on the HTTP API, gRPC and MCP listeners. The header is read below TLS, so it works with `MDDB_TLS_ENABLED=true` and with gRPC TLS. After the header, the connection's peer address is the client it names, and the rule above applies to it: when that address is itself a trusted proxy, the client IP header is read as well.

Who may send a header:

| Connecting peer | Sends a PROXY header | Sends none |
|-----------------|----------------------|------------|
| In `MDDB_TRUSTED_PROXIES` | Accepted. A `LOCAL` header (a load balancer's health check) keeps the peer's own address. | **Refused** — the connection is closed. A load balancer that stops sending the header is misconfigured, and guessing would put every client behind it in one bucket. |
| Not in `MDDB_TRUSTED_PROXIES` | **Refused** — a client cannot choose its own address. | Served, as itself. Direct clients and local health checks keep working. |

A peer has 10 seconds to send its header (like `ReadHeaderTimeout`). The header is read in the connection's own goroutine, never in the accept loop, so a slow peer holds only its own connection. With the switch on and `MDDB_TRUSTED_PROXIES` empty, every header is refused and mddbd warns at startup.

Not covered: HTTP/3 (QUIC is UDP; no PROXY protocol), the Unix-socket listeners (their peers have no IP, so they are never trusted), and the short-lived `/health` listener that answers while the server starts — a load balancer that sends PROXY headers sees it as unhealthy until the server is up, which is what it is.

> **Health checks.** With `MDDB_PROXY_PROTOCOL=on`, a container health check that connects from an address in `MDDB_TRUSTED_PROXIES` (for example `wget http://localhost:11023/health` with `127.0.0.1` trusted) is refused, because it sends no header. Keep the health check's source out of the trusted list: the load balancer's address or subnet, not `127.0.0.1` or the whole Docker network.

### Proxies and load balancers

What each product sends by default and what to set in mddb. Every row was checked against the vendor documentation listed under it (October 2026); what the documentation does not say is marked **not verified**.

| Product | Sends by default | Set in mddb | Proxy side (minimal) |
|---------|------------------|-------------|----------------------|
| **nginx** (HTTP)<sup>[1]</sup> | Nothing: only `Host` and `Connection` are set; a client's own `X-Forwarded-For` passes through. | `MDDB_TRUSTED_PROXIES=<nginx address>` | `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;` (appends `$remote_addr`). Or `proxy_set_header X-Real-IP $remote_addr;` with `MDDB_CLIENT_IP_HEADER=X-Real-IP`. |
| **nginx** (`stream`)<sup>[2]</sup> | No PROXY header. | `MDDB_PROXY_PROTOCOL=on`, `MDDB_TRUSTED_PROXIES=<nginx address>` | `proxy_protocol on;` sends v1; `proxy_protocol v2;` sends v2 (newer nginx, see the module docs for the version). |
| **Traefik**<sup>[3][4]</sup> | HTTP: appends the peer to `X-Forwarded-For`; incoming `X-Forwarded-*` are believed only from `entryPoints.<ep>.forwardedHeaders.trustedIPs`. | `MDDB_TRUSTED_PROXIES=<Traefik address>` | Nothing for HTTP. TCP routers: serversTransport `proxyProtocol: { version: 2 }` (version 1 or 2; 2 is the default) plus `MDDB_PROXY_PROTOCOL=on`. PROXY to backends from HTTP routers: **not verified**. |
| **Caddy**<sup>[5]</sup> | `reverse_proxy` sets or augments `X-Forwarded-For`; incoming values are ignored unless the peer is in Caddy's `trusted_proxies`. | `MDDB_TRUSTED_PROXIES=<Caddy address>` | Nothing for the header. PROXY: `reverse_proxy mddb:11023 { transport http { proxy_protocol v2 } }` plus `MDDB_PROXY_PROTOCOL=on`. |
| **HAProxy**<sup>[6]</sup> | Nothing. | `MDDB_TRUSTED_PROXIES=<HAProxy address>` | `option forwardfor` (adds an `X-Forwarded-For` line at the end; mddb joins all lines). PROXY: `server mddb 10.0.0.10:11023 send-proxy-v2` (`send-proxy` for v1) plus `MDDB_PROXY_PROTOCOL=on`. |
| **F5 BIG-IP**<sup>[7][8]</sup> | Nothing: the HTTP profile's **Insert X-Forwarded-For** defaults to Disabled. Whether it appends to a client's header: **not verified**. | `MDDB_TRUSTED_PROXIES=<self IPs / SNAT pool>` | HTTP profile: Insert X-Forwarded-For = Enabled. PROXY: no native setting found (**not verified**); the DevCentral CodeShare "Proxy Protocol Initiator" iRule (community code, v1) sends `PROXY TCP4 ...` on `SERVER_CONNECTED`, plus `MDDB_PROXY_PROTOCOL=on`. |
| **Citrix NetScaler ADC**<sup>[9][10][11]</sup> | Nothing until Client IP insertion is enabled on the service. The header name is the admin's; with none given (and no global one) it is `client-ip`. Append vs replace: **not verified**. | `MDDB_CLIENT_IP_HEADER=<the name chosen>` (e.g. `Client-IP`), `MDDB_TRUSTED_PROXIES=<SNIP>` | `set service svc_mddb -CIP ENABLED Client-IP`. PROXY: `add netprofile np_mddb -proxyProtocol ENABLED -proxyprotocoltxversion V2`, `set service svc_mddb -netProfile np_mddb`, plus `MDDB_PROXY_PROTOCOL=on`. |
| **Ivanti / Pulse / Brocade vTM**<sup>[12][13]</sup> | `X-Cluster-Client-Ip` with the client IP (virtual server `add_cluster_ip`, default Yes). `add_x_forwarded_for` defaults to No. Whether a client's own `X-Cluster-Client-Ip` is replaced: **not verified**. | `MDDB_CLIENT_IP_HEADER=X-Cluster-Client-Ip`, `MDDB_TRUSTED_PROXIES=<vTM addresses>` | Nothing (default). vTM's `proxy_protocol` setting only accepts PROXY headers from clients; sending one to a pool is left to TrafficScript, with no documented function: **not verified** — use the header. |
| **Cloudflare** (proxied DNS)<sup>[14][15][16]</sup> | `CF-Connecting-IP` (one address); `X-Forwarded-For` appended; `True-Client-IP` only with the Enterprise managed transform. | `MDDB_CLIENT_IP_HEADER=CF-Connecting-IP`, `MDDB_TRUSTED_PROXIES=<Cloudflare's published ranges>` | Nothing. |
| **cloudflared** (Cloudflare Tunnel)<sup>[14][17]</sup> | The origin's peer is the `cloudflared` process; for HTTP the client is in `CF-Connecting-IP`. Non-HTTP (TCP) tunnels carry no client IP. | `MDDB_CLIENT_IP_HEADER=CF-Connecting-IP`, `MDDB_TRUSTED_PROXIES=<cloudflared's address>`: `127.0.0.1` on the same host, or the Docker network (e.g. `172.16.0.0/12`) when cloudflared runs in a container. | Nothing. |
| **AWS ALB**<sup>[18]</sup> | `X-Forwarded-For` appended (`routing.http.xff_header_processing.mode=append`, the default; `preserve` and `remove` exist). With `routing.http.xff_client_port.enabled` the entry is `ip:port` / `[ipv6]:port`; mddb drops the port. | `MDDB_TRUSTED_PROXIES=<the ALB's subnet CIDRs>` | Nothing (default). |
| **AWS NLB**<sup>[19]</sup> | Client IP preservation (`preserve_client_ip.enabled`) is on by default for instance targets, off for IP targets with TCP/TLS; with it on, the peer already is the client. | Preservation on: nothing. Otherwise `MDDB_PROXY_PROTOCOL=on`, `MDDB_TRUSTED_PROXIES=<the NLB's subnet CIDRs>` | Target group attribute `proxy_protocol_v2.enabled=true` (v2 only). Health checks carry a header without client data too; mddb accepts it. |

Sources:
1. nginx `ngx_http_proxy_module` — https://nginx.org/en/docs/http/ngx_http_proxy_module.html
2. nginx `ngx_stream_proxy_module` — https://nginx.org/en/docs/stream/ngx_stream_proxy_module.html
3. Traefik entry points (`forwardedHeaders`) — https://doc.traefik.io/traefik/reference/install-configuration/entrypoints/
4. Traefik TCP serversTransport (`proxyProtocol`) — https://doc.traefik.io/traefik/reference/routing-configuration/tcp/serverstransport/
5. Caddy `reverse_proxy` — https://caddyserver.com/docs/caddyfile/directives/reverse_proxy
6. HAProxy 3.2 configuration manual (`option forwardfor`, `send-proxy`, `send-proxy-v2`) — https://www.haproxy.org/download/3.2/doc/configuration.txt
7. F5 BIG-IP LTM profiles reference (HTTP profile) — https://techdocs.f5.com/kb/en-us/products/big-ip_ltm/manuals/product/ltm-profiles-reference-13-0-0/2.html
8. F5 DevCentral CodeShare, Proxy Protocol Initiator (community) — https://community.f5.com/kb/codeshare/proxy-protocol-initiator/280541
9. NetScaler, insert the client IP in the request header — https://docs.netscaler.com/us/en/citrix-adc/current-release/load-balancing/load-balancing-advanced-settings/insert-cip-in-request-hdr.html
10. NetScaler command reference, `service` — https://developer-docs.netscaler.com/en-us/adc-command-reference-int/current-release/basic/service
11. NetScaler PROXY protocol — https://docs.netscaler.com/en-us/citrix-adc/current-release/system/proxy-protocol.html
12. Ivanti vTM 22.x virtual server configuration keys — https://help.ivanti.com/ps/help/en_US/VTM/22.x/config/ps-vtm-config/conf_vservers.htm
13. Ivanti vTM 22.x PROXY protocol — https://help.ivanti.com/ps/help/en_US/VTM/22.x/userguide/ps-vtm-userguide/proxy_protocol.htm
14. Cloudflare HTTP request headers — https://developers.cloudflare.com/fundamentals/reference/http-headers/
15. Cloudflare managed transforms (`True-Client-IP`) — https://developers.cloudflare.com/rules/transform/managed-transforms/reference/
16. Cloudflare IP ranges — https://www.cloudflare.com/ips/
17. Cloudflare One connectivity options (what the origin sees behind `cloudflared`) — https://developers.cloudflare.com/cloudflare-one/networks/connectivity-options/
18. AWS ALB `X-Forwarded` headers — https://docs.aws.amazon.com/elasticloadbalancing/latest/application/x-forwarded-headers.html
19. AWS NLB target group attributes — https://docs.aws.amazon.com/elasticloadbalancing/latest/network/edit-target-group-attributes.html

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
| `MDDB_EMBEDDING_PROVIDER` | `""` (disabled) | string | Provider: `"openai"`, `"ollama"`, `"voyage"`, `"cohere"`, `"lexical"` (v2.18.9+: hashed words and n-grams, no model, no network, does not match by meaning — see [EMBEDDING_PROVIDERS.md](EMBEDDING_PROVIDERS.md#5-lexical-no-model-no-network--v2189); never autodetected), or `""`. When empty and nothing is stored, MDDB probes a local Ollama once at startup — see `MDDB_EMBEDDING_AUTODETECT` |
| `MDDB_UPDATE_CHECK` | `"1"` | bool | Set to `0` to skip the startup release check. One GET to a pinned GitHub URL, carrying nothing about this installation; the answer is cached in `/health` |
| `MDDB_EMBEDDING_AUTODETECT` | `"1"` | bool | Set to `0` to skip the startup probe for a local Ollama. The probe is one `GET localhost:11434/api/tags` with a two-second budget, and only runs when no provider is configured |
| `OLLAMA_HOST` | `http://localhost:11434` | string | Where the autodetection probe looks. Accepted with or without a scheme |
| `MDDB_EMBEDDING_API_KEY` | `""` | string | API key (for openai/voyage/cohere) |
| `MDDB_EMBEDDING_API_URL` | *(see below)* | string | API base URL |
| `MDDB_EMBEDDING_MODEL` | *(see below)* | string | Embedding model name |
| `MDDB_EMBEDDING_DIMENSIONS` | *(see below)* | int | Vector dimensionality |
| `MDDB_EMBEDDING_IMAGE_MODEL` | `""` | string | (v2.18.9+, RAG-010) Voyage: the multimodal model pictures are embedded with (default `voyage-multimodal-3.5`, or the text model itself when it is multimodal). `none` embeds no pictures, with Voyage or Cohere. Other providers embed no images. See [EMBEDDING_PROVIDERS.md](EMBEDDING_PROVIDERS.md#images-v2189) |
| `MDDB_EMBEDDING_CHUNK_ENABLED` | `true` | bool | Enable text chunking before embedding |
| `MDDB_EMBEDDING_CHUNK_MODE` | `""` | string | `code` or `prose` for every document; empty decides per document. A collection's `embedding.chunkMode` takes precedence (v2.18.1+) |
| `MDDB_EMBEDDING_CHUNK_SIZE` | `1500` | int | Maximum chunk size in characters. A collection can set its own with `embedding.chunkSize` in its collection config (v2.18.1+) |
| `MDDB_EMBEDDING_QUEUE_SIZE` | `1000` | int | (v2.15.0+) Pending embedding jobs the worker will hold. Raise it for large imports; it was hardcoded before |
| `MDDB_EMBEDDING_QUEUE_WAIT` | `5s` | duration | (v2.15.0+) How long a write waits for room in a full queue before the document is stored without its vector. `0` restores the pre-2.15.0 behaviour of giving up immediately — faster ingest, and documents that no vector or hybrid search can reach until reindexed |
| `MDDB_EMBEDDING_CACHE_SIZE` | `1024` | int | (v2.12.0+) Embeddings held in the query cache; `0` disables caching and restores the pre-2.12.0 path exactly |
| `MDDB_EMBEDDING_CACHE_TTL` | `3600` | int | (v2.12.0+) Cache entry lifetime in seconds |

### Provider Defaults

| Provider | `API_URL` | `MODEL` | `DIMENSIONS` |
|----------|-----------|---------|---------------|
| `openai` | `https://api.openai.com/v1` | `text-embedding-3-small` | `1536` |
| `ollama` | `http://localhost:11434` | `nomic-embed-text` | `768` |
| `voyage` | `https://api.voyageai.com/v1` | `voyage-3` | `1024` |
| `cohere` | `https://api.cohere.ai/v1` | `embed-english-v3.0` | `1024` |
| `lexical` | — (no network) | `lexical-ngram-v1` (fixed) | `512`, clamped to `256`–`2048` |

`GET /config` reports `vectorConfig.semantic`: `true` for every model-backed
provider, `false` for `lexical`, whose vectors match shared spelling rather
than meaning. The search advisor (`/v1/search-advisor`) reports the same in
`profile.semantic` and recommends keyword search for lexical collections
unless the collection's own evaluation set measures hybrid better. The gRPC
`SearchAdvisor` carries it as `CollectionProfile.semantic` (15,
`optional bool`), unset when nothing in the collection is embedded.

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
| `MDDB_MCP_INSTRUCTIONS_FILE` | `""` | string | Path to a file with the instructions, read at startup; `MDDB_MCP_INSTRUCTIONS` wins when both are set (v2.18.1) |

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

**Not available on Windows.** The security model above rests on the `0600` mode bits, and Windows cannot express them: `os.Chmod` there toggles the read-only attribute and nothing more. Windows 10 does support `AF_UNIX`, so the socket would bind and serve — with the access restriction silently absent. MDDB refuses the listener instead and says why. Use `tcp://127.0.0.1:<port>` on Windows.

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
| `MDDB_ACCESS_LOG` | `false` | bool | One line per HTTP request: method, path, status, bytes, elapsed, remote address (the transport peer) and client (the address resolved by the trusted-proxy rule below; differs from remote behind a proxy). The query string is deliberately omitted — search endpoints carry user content there |

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
| `MDDB_CODE_MAX_SYMBOLS` | `512` | int | Most `defines`, `uses` and `imports` entries kept per code document ([API.md](API.md#symbols-which-file-declares-this)) |

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
| `MDDB_COMPRESSION_ENABLED` | `true` | bool | Enable adaptive compression of stored documents and revisions (not indexes, vectors or API traffic) |
| `MDDB_COMPRESSION_SMALL_THRESHOLD` | `1024` | int (bytes) | Below this: stored uncompressed. From here up to the medium threshold: Snappy |
| `MDDB_COMPRESSION_MEDIUM_THRESHOLD` | `10240` | int (bytes) | From this size: Zstd (default level). Either codec is kept only when the result is smaller |

---

## Replication

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_REPLICATION_ROLE` | `""` | string | Role: `"leader"`, `"follower"`, or `""` (standalone) |
| `MDDB_NODE_ID` | `""` | string | Unique node ID (required for replication) |
| `MDDB_REPLICATION_LEADER_ADDR` | `""` | string | Leader address for follower nodes |
| `MDDB_BINLOG_ENABLED` | `false` | bool | Enable binary log (auto-enabled for leaders) |
| `MDDB_BINLOG_PATH` | `""` | string | Custom binlog file path |
| `MDDB_REPLICATION_SECRET` | `""` | string | Shared secret for the snapshot and binlog streams; the same value on the leader and every follower. Without it, auth or mTLS, a leader refuses them ([REPLICATION.md](REPLICATION.md#securing-replication)) |
| `MDDB_FREELIST_SYNC` | auto | bool | Write bbolt's freelist on every commit so opening the database reads one page instead of the whole file. On a 908 MB database: 7 ms and 37 MB RSS instead of ~400 ms and 938 MB; minutes from a cold disk on large files ([DEPLOYMENT.md](DEPLOYMENT.md#large-databases-startup-time-and-memory)). Since v2.18.2 a clean shutdown stores the freelist anyway; this setting is what protects a start after a crash. Unset (or `auto`, v2.18.2+): on for a file of `MDDB_FREELIST_SYNC_THRESHOLD` or more, decided at start; `true`/`false` force it |
| `MDDB_FREELIST_SYNC_THRESHOLD` | `4294967296` | int | File size in bytes from which `MDDB_FREELIST_SYNC` defaults to on (v2.18.2+) |
| `MDDB_STATS_TTL` | `300` | int | Seconds between background recounts for `/v1/stats` on a database whose count is slow (v2.18.5+) |
| `MDDB_STATS_WAIT` | `2` | int | Seconds the first `/v1/stats` after a start waits for the count before answering `statsPending` (v2.18.5+) |
| `MDDB_STORAGE_LAYOUT` | `single` | string | `per-collection` keeps each collection in its own file under `<database>.d/`; switch with `mddbd -split-collections` / `-merge-collections` ([STORAGE-LAYOUT.md](STORAGE-LAYOUT.md), v2.18.2+) |
| `MDDB_FREELIST_BACKGROUND` | `true` | bool | When the freelist is not stored (after a crash), open at once, serve reads, and rebuild it in the background; writes get 503 until it is done. `false` walks before the server starts (v2.18.2+) |
| `MDDB_PREWARM` | `auto` | string | When the open has to walk the file: `auto` reads the file sequentially into the page cache first if it fits in half the available memory (cgroup limit included), `on` always, `off` never (v2.18.2+) |
| `MDDB_BACKUP_VERIFY` | `true` | bool | Check every backup with bbolt's full integrity check before keeping it |
| `MDDB_VERIFY_TIMEOUT` | `1h` | duration | Longest an integrity check (backup, restore, follower snapshot) may run |

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

## Elasticsearch-Compatible Subset (v2.18.10+)

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_ES_COMPAT` | `false` | bool | `true` mounts a subset of the Elasticsearch REST API under `/es` on the HTTP listener: `_search`, `_count`, `_doc`, `_create`, `_bulk`, `_refresh`, `GET /es`. Requests are translated onto `/v1` search and write paths, behind the same auth, permissions, rate limiter and body cap; `/v1` is unchanged. Unsupported clauses are a `400` naming them. See [ELASTICSEARCH-COMPAT.md](ELASTICSEARCH-COMPAT.md) |

---

## Quotas and Reserves (v2.18.9+)

Server-wide limits that protect the node, next to the per-collection `quota`
([MULTI_TENANCY.md](MULTI_TENANCY.md#quotas-v2189)). Both are off by default.

| Env Var | Default | Type | Description |
|---------|---------|------|-------------|
| `MDDB_DISK_RESERVE_BYTES` | `0` (off) | int64 | Document writes on every surface are refused with `507 Insufficient Storage` (gRPC `RESOURCE_EXHAUSTED`) while the filesystem holding the database has less than this available to the server. Reads, deletes and backups keep working. A filesystem that cannot be measured does not block writes |
| `MDDB_VECTOR_RAM_LIMIT_BYTES` | `0` (off) | int64 | Once the in-memory flat vector index holds this much (vectors x dimensions x 4 bytes), writes that would add vectors to RAM — new or growing documents in a collection that is not `diskOnlyVectors`, with an embedding provider configured — are refused with `507`, resource `vectorRam`. Disk-only collections keep accepting writes. Map overhead and the other ANN indexes' own copies are not counted, so leave headroom |

Each refusal is counted in `mddb_operations_total{operation="quota_exceeded",label="<resource>"}`,
logged, and sent as a `quota.exceeded` webhook (at most once a minute per
collection and resource).

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
| `quota` | none | object | (v2.18.9+) `{maxDocuments, maxBytes, maxVectors, embeddingCallsPerDay, embeddingTokensPerDay}`, `0` = no limit. Omitted keeps the stored quota, `{}` removes it; changing it needs a global admin when auth is on. See [MULTI_TENANCY.md](MULTI_TENANCY.md#quotas-v2189) |

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
| `--version` | | bool | Print `mddbd version X.Y.Z` and exit (v2.18.8) |
| `--check-update` | | bool | Report whether a newer release exists, then exit |

The version is the constant `VERSION` in `services/mddbd/main.go`. It is not
injected at build time. The release checks the built binary with
`mddbd -version` against the tag (OPS-022).

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
