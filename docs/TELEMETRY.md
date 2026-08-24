---
title: "MDDB Telemetry & Monitoring"
slug: "docs/telemetry"
description: "MDDB's Prometheus-compatible /metrics endpoint exposing server, database and Go runtime telemetry with no external dependencies, plus Grafana panels."
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

### Database

| Metric | Type | Labels | Description |
|---|---|---|---|
| `mddb_database_size_bytes` | gauge | -- | Database file size |
| `mddb_documents_total` | gauge | `collection` | Documents per collection |
| `mddb_revisions_total` | gauge | `collection` | Revisions per collection |
| `mddb_meta_indices_total` | gauge | `collection` | Metadata index entries per collection |

Database metrics are cached for 15 seconds to avoid excessive BoltDB scans.

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
