---
title: "MDDB Telemetry & Monitoring"
slug: "docs/telemetry"
description: "MDDB's Prometheus-compatible /metrics endpoint exposing server, database and Go runtime telemetry with no external dependencies, Grafana panels, and OpenTelemetry trace export over OTLP/HTTP."
status: publish
---

# MDDB Telemetry & Monitoring

MDDB exposes a Prometheus-compatible `/metrics` endpoint that provides real-time telemetry about the server, database, and Go runtime. No external dependencies are required -- metrics are rendered in the standard [Prometheus text exposition format](https://prometheus.io/docs/instrumenting/exposition_formats/#text-based-format) and can be scraped by any compatible tool.

## Quick Start

Metrics are **enabled by default**. Verify by hitting the endpoint:

```bash
curl http://localhost:11023/metrics
```

Output (excerpt):

```
# HELP mddb_uptime_seconds Time since server start in seconds.
# TYPE mddb_uptime_seconds gauge
mddb_uptime_seconds 3621.4

# HELP mddb_http_requests_total Total number of HTTP requests.
# TYPE mddb_http_requests_total counter
mddb_http_requests_total{method="POST",path="/v1/add",status="200"} 1523
mddb_http_requests_total{method="POST",path="/v1/search",status="200"} 892

# HELP mddb_documents_total Total documents per collection.
# TYPE mddb_documents_total gauge
mddb_documents_total{collection="blog"} 150
mddb_documents_total{collection="docs"} 42
```

## Configuration

| Environment Variable | Default | Description |
|---|---|---|
| `MDDB_METRICS` | `true` | Set to `false` to disable the `/metrics` endpoint |
| `MDDB_METRICS_PUBLIC` | `false` | When auth is enabled (`MDDB_AUTH_ENABLED=true`), expose `/metrics` **without** credentials. Default keeps it gated (SEC-009). No effect when auth is disabled. |

```bash
# Disable metrics
MDDB_METRICS=false ./mddbd

# Enable metrics (default)
MDDB_METRICS=true ./mddbd
```

When disabled, `GET /metrics` returns 404 and no request tracking middleware is active (zero overhead).

### Authenticating `/metrics` (SEC-009)

When `MDDB_AUTH_ENABLED=true`, `/metrics` is **not** public by default — an
unauthenticated scrape gets `401`, the same as every other gated endpoint. This
prevents leaking operational reconnaissance (per-endpoint operation counts,
collection labels, traffic volumes, build version) to anonymous callers.

Two supported options:

1. **Scrape with credentials** (recommended) — issue Prometheus a read-capable
   API key / JWT and pass it as a Bearer token:

   ```yaml
   # prometheus.yml
   scrape_configs:
     - job_name: mddb
       metrics_path: /metrics
       authorization:
         type: Bearer
         credentials_file: /etc/prometheus/mddb-token
       static_configs:
         - targets: ["mddb:11023"]
   ```

2. **Opt back into public metrics** — only on a trusted network where the
   scraper cannot present a token:

   ```bash
   MDDB_AUTH_ENABLED=true MDDB_METRICS_PUBLIC=true ./mddbd
   ```

| `MDDB_AUTH_ENABLED` | `MDDB_METRICS_PUBLIC` | `GET /metrics` (no creds) |
|---|---|---|
| `false` | _(any)_ | `200` — open (no auth layer at all) |
| `true`  | unset / `false` | `401` — credentials required |
| `true`  | `true` | `200` — explicitly opted in |

## Available Metrics

### Server Info

| Metric | Type | Labels | Description |
|---|---|---|---|
| `mddb_info` | gauge | `mode` | Server metadata (always 1) |
| `mddb_uptime_seconds` | gauge | -- | Seconds since server start |

### HTTP Requests

| Metric | Type | Labels | Description |
|---|---|---|---|
| `mddb_http_requests_total` | counter | `method`, `path`, `status` | Total HTTP requests |
| `mddb_http_request_duration_seconds` | histogram | `method`, `path` | Request latency distribution |

Histogram buckets: 1ms, 5ms, 10ms, 25ms, 50ms, 100ms, 250ms, 500ms, 1s, 2.5s, 5s, 10s.

### MCP tool calls

Counted on every transport (Streamable HTTP, SSE, `/mcp/tools/call`, stdio),
independent of `MDDB_MCP_LOGGING_ENABLED`. The `label` is the tool name as the
client sent it, so custom tools count under their own names.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `mddb_operations_total{operation="mcp_tool_call"}` | counter | `label` (tool) | Every `tools/call` |
| `mddb_operations_total{operation="mcp_tool_miss"}` | counter | `label` (tool) | Calls whose result carried zero documents (`total` 0 or an empty `results`/`documents` array) |
| `mddb_operations_total{operation="mcp_tool_error"}` | counter | `label` (tool) | Calls that failed |

Miss ratio per tool: `sum by (label) (rate(mddb_operations_total{operation="mcp_tool_miss"}[1h])) / sum by (label) (rate(mddb_operations_total{operation="mcp_tool_call"}[1h]))`.
The names behind the misses are in the log, not the metric — see
[MCP.md](MCP.md#request-logging).

### Scheduled backups (v2.18.9+)

Set by the scheduled backups of [BACKUP.md](BACKUP.md#scheduled-backups);
absent until a backup rule has stored a backup.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `mddb_backup_last_success_timestamp` | gauge | `rule` (automation rule ID) | Unix time of the last backup the rule stored and verified |
| `mddb_operations_total{operation="backup_failed"}` | counter | `label` (rule ID) | Scheduled backup runs that failed |

Alert when a nightly backup is late: `time() - mddb_backup_last_success_timestamp > 26 * 3600`.

### Database

| Metric | Type | Labels | Description |
|---|---|---|---|
| `mddb_database_size_bytes` | gauge | -- | Database file size |
| `mddb_db_write_transactions_total` | counter | -- | Write transactions committed to the database file; each one is an fsync |
| `mddb_documents_total` | gauge | `collection` | Documents per collection |
| `mddb_revisions_total` | gauge | `collection` | Revisions per collection |
| `mddb_meta_indices_total` | gauge | `collection` | Metadata index entries per collection |

Database metrics are cached for 15 seconds to avoid excessive BoltDB scans.

### Quotas (v2.18.9+)

| Metric | Type | Labels | Description |
|---|---|---|---|
| `mddb_collection_fullness` | gauge | `collection`, `resource` | Share of a collection quota in use, 0–1 (above 1 when a limit was lowered below current use). `resource` is `documents`, `bytes` or `vectors`; only limits that are set are reported |
| `mddb_embedding_budget_used` | gauge | `collection` | Share of today's (UTC) embedding budget spent — the larger of the call and token shares |
| `mddb_operations_total{operation="quota_exceeded"}` | counter | `label` (resource) | Writes or embeddings refused by a quota, `diskReserve` or `vectorRam` |

Both gauges are absent on a server without quotas, and share the 15-second
cache above. Alert before a tenant hits the wall:

```promql
max by (collection, resource) (mddb_collection_fullness) > 0.9
```

See [MULTI_TENANCY.md](MULTI_TENANCY.md#quotas-v2189).

### Retrieval gain (v2.18.8+)

| Metric | Type | Labels | Description |
|---|---|---|---|
| `mddb_retrieval_tokens_returned_total` | counter | -- | Estimated tokens searches returned |
| `mddb_retrieval_tokens_avoided_total` | counter | -- | Estimated tokens saved by returning passages or chunks instead of whole documents |
| `mddb_retrieval_hits_total` | counter | `shape` (`passage`, `document`) | Search hits by what was returned |

These count since the process started. The same counts are kept per day in
the database, so `GET /v1/gain`, the MCP tool `get_gain` and `mddb-cli gain`
report a window of days across restarts. Searches per algorithm are the
existing `mddb_operations_total` series.

**Definitions.** These numbers are only worth what their definitions are,
so here they are.

- **tokensReturned** is the estimated size of what a search handed back. It
  uses 4 bytes per token, the same estimate the context budget uses.
- **tokensAvoided** applies only to a hit returned as a passage, chunk or
  window. It is the hit's whole document minus what was returned. The
  baseline is reading the document the hit came from, never the whole
  collection, so the number is conservative. Several passages from one
  document count that document once.
- **fragmentHits** and **documentHits** count hits returned as a fragment
  and as a whole document.

The counters hold counts only: no query text, no keys, no collection names.
They are written once a minute and at shutdown. Cached full-text responses
and evaluation runs (`POST /v1/eval`) are not counted.

### Vector Search

| Metric | Type | Labels | Description |
|---|---|---|---|
| `mddb_vector_embeddings_total` | gauge | `collection` | Embedded documents per collection |
| `mddb_vector_index_ready` | gauge | -- | 1 if index is loaded, 0 if loading |
| `mddb_embedding_provider_configured` | gauge | -- | 1 if embedding provider is set |
| `mddb_embedding_queue_size` | gauge | -- | Pending embedding jobs in queue |
| `mddb_embedding_cache_hits_total` | counter | -- | (v2.12.0+) Embedding requests served from cache |
| `mddb_embedding_cache_misses_total` | counter | -- | (v2.12.0+) Embedding requests that reached the provider |
| `mddb_embedding_cache_size` | gauge | -- | (v2.12.0+) Embeddings currently held in cache |

### Webhooks & Schema

| Metric | Type | Labels | Description |
|---|---|---|---|
| `mddb_webhooks_total` | gauge | -- | Registered webhooks |
| `mddb_schemas_total` | gauge | -- | Registered metadata schemas |

### Go Runtime

| Metric | Type | Description |
|---|---|---|
| `go_goroutines` | gauge | Active goroutines |
| `go_memstats_alloc_bytes` | gauge | Allocated heap memory |
| `go_memstats_sys_bytes` | gauge | Total memory from OS |
| `go_memstats_heap_inuse_bytes` | gauge | In-use heap spans |
| `go_gc_completed_total` | counter | Completed GC cycles |
| `go_gc_pause_seconds_total` | counter | Total GC pause time |

## Prometheus Setup

### prometheus.yml

```yaml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'mddb'
    static_configs:
      - targets: ['localhost:11023']
    metrics_path: /metrics
    scrape_interval: 15s
```

### Docker Compose

```yaml
services:
  mddb:
    image: ghcr.io/tradik/mddbd:latest
    ports:
      - "11023:11023"

  prometheus:
    image: prom/prometheus:latest
    ports:
      - "9090:9090"
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml

  grafana:
    image: grafana/grafana:latest
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin
```

## Grafana Dashboard

### Useful PromQL Queries

**Request rate (requests/second):**
```promql
rate(mddb_http_requests_total[5m])
```

**Request rate per endpoint:**
```promql
sum by (path) (rate(mddb_http_requests_total[5m]))
```

**Error rate (4xx + 5xx):**
```promql
sum(rate(mddb_http_requests_total{status=~"4..|5.."}[5m]))
  /
sum(rate(mddb_http_requests_total[5m]))
```

**P99 latency per endpoint:**
```promql
histogram_quantile(0.99,
  sum by (path, le) (rate(mddb_http_request_duration_seconds_bucket[5m]))
)
```

**P50 (median) latency:**
```promql
histogram_quantile(0.50,
  sum by (le) (rate(mddb_http_request_duration_seconds_bucket[5m]))
)
```

**Database size over time:**
```promql
mddb_database_size_bytes
```

**Commits per second (each an fsync — the cost that matters on spinning disks):**
```promql
rate(mddb_db_write_transactions_total[1m])
```

**Documents per collection:**
```promql
mddb_documents_total
```

**Embedding coverage (% of documents embedded):**
```promql
mddb_vector_embeddings_total / mddb_documents_total * 100
```

**Embedding queue backlog:**
```promql
mddb_embedding_queue_size
```

**Embedding cache hit rate — how much of the provider bill the cache is saving:**
```promql
rate(mddb_embedding_cache_hits_total[5m])
  / (rate(mddb_embedding_cache_hits_total[5m]) + rate(mddb_embedding_cache_misses_total[5m]))
```
A rate near zero on a busy server usually means the cache is too small for the
query mix (`MDDB_EMBEDDING_CACHE_SIZE`) rather than that queries are all
distinct. The metric is absent entirely when caching is disabled.

**Memory usage:**
```promql
go_memstats_alloc_bytes / 1024 / 1024  # in MB
```

**Goroutine count:**
```promql
go_goroutines
```

### Alerting Rules

```yaml
# alerts.yml
groups:
  - name: mddb
    rules:
      # High error rate
      - alert: MDDBHighErrorRate
        expr: >
          sum(rate(mddb_http_requests_total{status=~"5.."}[5m]))
          / sum(rate(mddb_http_requests_total[5m])) > 0.05
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "MDDB error rate > 5%"

      # Slow requests (P99 > 2s)
      - alert: MDDBSlowRequests
        expr: >
          histogram_quantile(0.99,
            sum by (le) (rate(mddb_http_request_duration_seconds_bucket[5m]))
          ) > 2
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "MDDB P99 latency > 2s"

      # Vector index not ready
      - alert: MDDBVectorIndexNotReady
        expr: mddb_vector_index_ready == 0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "MDDB vector index not ready for 5+ minutes"

      # Embedding queue backlog
      - alert: MDDBEmbeddingQueueHigh
        expr: mddb_embedding_queue_size > 500
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "MDDB embedding queue backlog > 500 jobs"

      # Database size > 5GB
      - alert: MDDBDatabaseLarge
        expr: mddb_database_size_bytes > 5e9
        for: 1h
        labels:
          severity: info
        annotations:
          summary: "MDDB database size exceeds 5GB"

      # Server down
      - alert: MDDBDown
        expr: up{job="mddb"} == 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "MDDB server is down"
```

## Alternative Scrapers

### Grafana Agent

```yaml
metrics:
  configs:
    - name: mddb
      scrape_configs:
        - job_name: mddb
          static_configs:
            - targets: ['localhost:11023']
      remote_write:
        - url: https://prometheus-us-central1.grafana.net/api/prom/push
```

### Datadog Agent

```yaml
# datadog.yaml
instances:
  - openmetrics_endpoint: http://localhost:11023/metrics
    namespace: mddb
    metrics:
      - mddb_*
      - go_*
```

### Victoria Metrics

```bash
# Direct scrape
victoria-metrics -promscrape.config=prometheus.yml
```

### cURL + jq (manual check)

```bash
# Quick health dashboard in terminal
watch -n 5 'curl -s http://localhost:11023/metrics | grep -E "^mddb_(uptime|documents|database_size|http_requests)"'
```

## Kubernetes

### ServiceMonitor (Prometheus Operator)

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: mddb
  labels:
    release: prometheus
spec:
  selector:
    matchLabels:
      app: mddb
  endpoints:
    - port: http
      path: /metrics
      interval: 15s
```

### Pod annotations (auto-discovery)

```yaml
apiVersion: v1
kind: Pod
metadata:
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "11023"
    prometheus.io/path: "/metrics"
```

## OpenTelemetry Tracing (v2.18.9+)

Metrics say how many searches ran and how long they took overall. They cannot
say why one query took 900 ms: 20 ms of full-text search, 700 ms embedding the
query, 5 ms fusing, 150 ms loading documents. Traces can. MDDB exports a span for
each stage of a search or an ingest over OTLP/HTTP (JSON) to any OpenTelemetry
collector (OPS-024). If the caller sends a W3C `traceparent`, the spans join the
caller's own trace, so an agent framework's trace shows the database's work
inside it.

Prometheus remains the source of metrics. MDDB does not export OTLP metrics.

### Setup

```bash
MDDB_OTEL_ENDPOINT=http://otel-collector:4318 mddbd
```

That is the only required setting. `/v1/traces` is appended to the endpoint
unless its path already ends in it. The other settings are listed in
[config.md](config.md#opentelemetry-tracing-v2189):

| Variable | Default | Meaning |
|---|---|---|
| `MDDB_OTEL_ENDPOINT` | — | Collector URL. Unset = tracing off |
| `MDDB_OTEL_HEADERS` | — | `key=value,key2=value2`, sent with every export (API keys for hosted backends) |
| `MDDB_OTEL_SAMPLE_RATIO` | `1` | Share of new traces recorded, 0–1. A caller's `traceparent` sampling flag is respected |
| `MDDB_OTEL_SERVICE_NAME` | `mddb` | `service.name` resource attribute |
| `MDDB_OTEL_QUEUE_SIZE` | `2048` | Spans waiting for export before new ones are dropped |

To try it locally, run Jaeger, which accepts OTLP/HTTP on port 4318 and shows
traces on 16686:

```bash
docker run --rm -p 4318:4318 -p 16686:16686 jaegertracing/jaeger:2.22.0
MDDB_OTEL_ENDPOINT=http://localhost:4318 mddbd
curl -s localhost:11023/v1/hybrid-search \
  -H 'traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01' \
  -d '{"collection":"kb","query":"snapshot backup"}'
# open http://localhost:16686 and look up trace 4bf92f3577b34da6a3ce929d0e0e4736
```

The repository's compose files have no observability profile. In your own
compose file, point `MDDB_OTEL_ENDPOINT` at the collector's service name, for
example `http://otel-collector:4318`.

### Where the trace context comes from

| Transport | `traceparent` source | Server span name |
|---|---|---|
| REST (HTTP API, GraphQL) | `traceparent` request header | `POST /v1/hybrid-search` (method and path) |
| gRPC | `traceparent` metadata key | `/mddb.MDDB/HybridSearch` (full method) |
| MCP over HTTP (`/mcp`, `/sse`, `/tools/call`) | `traceparent` request header | `POST /mcp` |
| MCP over stdio | none: every request starts its own trace | `mcp.stdio` |

If a request has no `traceparent`, MDDB starts a new trace at its server span,
sampled at `MDDB_OTEL_SAMPLE_RATIO`. If the endpoint is not set, no spans are
created. A `traceparent` with the sampled flag off (`-00`) is honoured: nothing
from that trace is exported.

### Spans

Every stage span is a child of the request's server span.

| Span | Measured in | Covers | Attributes |
|---|---|---|---|
| `search.fts` | `tracedFTS` (`otel.go`), hybrid search over REST, gRPC, MCP and eval | The full-text half: filter, tokenise, BM25/BM25F | `mddb.collection`, `mddb.fts.algorithm`, `mddb.results` |
| `search.embed_query` | `embedQuery` (`otel.go`), every vector-backed search | The embedding provider call for the query text | — |
| `search.vector` | `vectorHalf` (`hybrid_search.go`), `VectorSearch` (`mcp_direct_search.go`) | The index search the planner chose, the iterative scan, feedback rescoring and summaries | `mddb.vector.algorithm`, `mddb.results` |
| `search.fuse` | `handleHybridSearch`, `hybridItems` | Merging the halves (alpha, rrf, weighted) and per-query boosts | `mddb.fusion.strategy`, `mddb.results` |
| `search.load_content` | `tracedLoadHybridDocs` (`otel.go`), `VectorSearch` | Reading the hit documents from the database | `mddb.documents` |
| `ingest.convert` | `ingestDocuments` (`ingest.go`) | Keys from URLs, frontmatter, metadata injection, deduplication | `mddb.collection`, `mddb.documents` |
| `ingest.index` | `ingestDocuments` | The batch write, TTLs, and the full-text, geo and temporal indexes | `mddb.collection`, `mddb.documents` |
| `ingest.embed` | `EmbeddingWorker.run` (`embedding_worker.go`) | One document's embedding on the background worker. It ends after the ingest response, in the same trace | `mddb.collection`, `mddb.document.id` |

Server spans carry `http.request.method`, `url.path` and
`http.response.status_code` (REST, MCP over HTTP), or `rpc.system` and
`rpc.method` (gRPC, MCP over stdio). A span is marked as an error when its stage
returned one, when an HTTP response is a 5xx, or when an RPC fails. The resource
carries `service.name` and `service.version`.

The stage names are defined in one place (`otel.go`). They follow the stages the
`explain` plan (SRCH-013) describes. The plan says which index answered and why;
the spans say how long each stage took. Neither the plan nor the evaluation run
(SRCH-019, which reports end-to-end latency per configuration) has its own stage
timers: the spans are the only per-stage measurement.

### Delivery

- **Asynchronous and batched.** Ended spans go into a bounded queue. One
  goroutine sends them in batches of up to 512, or every 5 seconds, whichever
  comes first.
- **Requests are never slowed by the collector.** When the queue is full, new
  spans are dropped and counted as
  `mddb_operations_total{operation="otel_spans_dropped"}`. A failed export (the
  collector is down, or returns an error status) is logged at debug level and
  counted as `mddb_operations_total{operation="otel_export_failed"}`.
- **Flushed on shutdown.** The ordered shutdown sends the queued spans after the
  embedding worker has drained, waiting at most 5 seconds.

### Cost

**Binary size.** The exporter is written in-house, with no OpenTelemetry SDK
dependency. It implements the subset MDDB needs: W3C trace context, span timing,
and OTLP/HTTP JSON. Measured on the stripped release build
(`CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`, linux/amd64, Go 1.27.3):

| Build | Bytes | Change |
|---|---|---|
| v2.18.9 before tracing | 30,974,112 | — |
| With the OpenTelemetry Go SDK v1.47.0 + `otlptracehttp` (exporter set up only, no instrumentation) | 32,522,400 | +1,548,288 (+5.0%) |
| With the in-house exporter and all instrumentation | 31,019,168 | +45,056 (+0.15%) |

The SDK option would also have added five modules to `go.mod`
(`otlptrace`, `otlptracehttp`, `go.opentelemetry.io/proto/otlp`,
`grpc-gateway/v2`, `cenkalti/backoff/v5`). That cost buys nothing a
default deployment uses.

**Zero cost when off.** Without `MDDB_OTEL_ENDPOINT` the tracer is nil. The
HTTP and gRPC chains are not wrapped, and each stage costs one nil check with no
allocation (`TestDisabledIsFree` in `internal/tracing` asserts zero
allocations). Hybrid search, 50 embedded documents, `go test -bench
HybridSearchTracing -benchtime=3000x -count=8`, AMD Ryzen 9 7950X (medians):

| Build | ns/op | B/op | allocs/op |
|---|---|---|---|
| Before tracing (b4f7821) | 65,522 | 63,717 | 1,052 |
| Tracing off | 66,163 | 63,683 | 1,052 |
| Tracing on, every trace sampled | 85,535 | 82,709 | 1,129 |

Off is the same as before, within run-to-run noise (the two ranges overlap:
64.5–66.8 µs before, 62.9–70.5 µs after). With tracing on, a search records six
spans and pays about 20 µs and 77 allocations for them. On a real deployment
that is small next to a provider round trip for the query embedding.

## Existing Monitoring Endpoints

In addition to `/metrics`, MDDB provides JSON endpoints useful for monitoring:

| Endpoint | Description |
|---|---|
| `GET /health` | Health check (200 = healthy, 503 = unhealthy) |
| `GET /v1/stats` | Detailed database statistics (JSON) |
| `GET /v1/vector-stats` | Vector search subsystem status (JSON) |

These can be used with simpler monitoring tools that don't support Prometheus format, or for custom health check scripts:

```bash
# Kubernetes liveness probe
livenessProbe:
  httpGet:
    path: /health
    port: 11023
  initialDelaySeconds: 5
  periodSeconds: 10

# Readiness probe (vector index loaded)
readinessProbe:
  httpGet:
    path: /health
    port: 11023
  initialDelaySeconds: 10
  periodSeconds: 5
```

## Architecture Notes

- **Zero dependencies**: Metrics are rendered using Go's `fmt` and `strings` packages. No Prometheus client library required.
- **Low overhead**: Request counting uses a mutex-protected map. The `/metrics` handler is only called during scrapes (typically every 15s).
- **DB stats caching**: Database-level metrics (document counts, file size) are cached for 15 seconds to avoid repeated BoltDB cursor scans.
- **Histogram buckets**: Fixed buckets from 1ms to 10s covering typical API latency ranges. Cumulative format compatible with `histogram_quantile()`.
- **Path normalization**: All `/v1/*` paths and `/health` are preserved as-is. Unknown paths collapse to `/other` to prevent label cardinality explosion.
