---
title: "What's New in MDDB 2.12"
slug: "blog/whats-new-in-mddb-2-12"
status: publish
type: post
date: 2026-08-24
tags: [release, search, rag, security, performance]
excerpt: "MDDB 2.12 adds code-aware retrieval, collection search profiles, faster bulk indexing, safer defaults and clearer production diagnostics."
description: "A practical overview of MDDB 2.12: search and RAG changes, measured performance gains, security fixes and the required upgrade steps."
---

MDDB 2.12 changes how the server handles search configuration, source code and
production workloads. It also fixes several cases where accepted configuration
was ignored or only partially exposed through one protocol.

This article covers the changes that affect deployment and day-to-day use. The
full list, including tests and internal refactors, remains in the
[changelog](https://github.com/tradik/mddb/blob/2.12.0/CHANGELOG.md).

## Search settings can live with the collection

A collection can now store its default search type, result count, retrieval
mode, hybrid strategy, alpha, oversampling and context budget. Callers may
override those values per request; otherwise HTTP, gRPC and MCP use the stored
profile.

The same configuration can include a `responsePrompt`, returned with search
results so an agent knows how answers from that collection should be formatted.
This removes the need to copy the same retrieval settings and prompt into every
client.

The new search advisor profiles a sample of the collection and recommends a
starting configuration:

```bash
curl -s "localhost:11023/v1/search-advisor?collection=runbooks"
```

It considers document length, vector coverage, vocabulary, collection size and
extracted code symbols. The response includes reasons and warnings because the
advisor measures documents, not the queries users will submit later. Applying
the result remains explicit:

```bash
curl -s "localhost:11023/v1/search-advisor?collection=runbooks&apply=true"
```

The implementation and its limits are covered in
[Choosing a Search Algorithm from the Collection](/blog/which-algorithm-should-i-use/)
and [The Collection Knows How to Be Searched](/blog/the-collection-knows-how-to-be-searched/).

## Source code is no longer treated as prose

Code documents now use a tokenizer that preserves complete identifiers and
indexes their camelCase, snake_case and kebab-case components. Language
keywords and short identifiers are retained instead of passing through prose
stemming and stop-word removal.

Chunking also follows source structure. MDDB prefers balanced braces,
parentheses and brackets, so a returned passage is more likely to contain a
complete rule or function. Full-text highlights and chunk results now carry
1-based line ranges, allowing an agent to request a small fragment and still
know where it belongs in the source file.

On write, supported source files record `defines`, `uses` and `imports` in
metadata. `retrievalMode: "graph"` can follow those relationships from a direct
search hit to the files connected to it. The graph is derived from current
metadata rather than stored as a second structure.

See [What Breaks If I Change This?](/blog/what-breaks-if-i-change-this/) for a
worked CSS and JavaScript example.

## More control over ranking and memory

The vector index gains `sq4`, a 4-bit scalar-quantized option. The measured
recall in the project benchmark is 99.5% of the int8 implementation while the
in-memory index uses half as much space. Existing int8 `sq` remains available
for workloads that prefer its higher precision.

Hybrid search adds a `weighted` strategy. It starts with alpha fusion, then can
adjust scores using:

- diversity, based on MinHash textual overlap;
- proximity between document paths;
- freshness with a configurable half-life.

All weights default to zero. Selecting `weighted` without setting a signal
therefore produces the same ranking as alpha fusion. MinHash is also exposed as
a duplicate-detection mode for copied-and-edited documents that should not be
classified solely by topic.

The measured diversity case and its computational cost are documented in
[Diversifying Near-Duplicate Search Results](/blog/your-vector-search-rewards-duplicates/).

Search requests can now set `oversample` directly. Full-text searches may also
set `cacheTtl` to reuse an identical result set; caching is opt-in and writes
invalidate the relevant collection immediately. A concurrency gate protects
FTS, vector, hybrid and aggregate searches from exhausting memory under load.
When the queue timeout is reached, the server returns `503` with `Retry-After`.

## Bulk indexing no longer commits per document

The full-text batch path previously opened three BoltDB write transactions for
each document. On the benchmark corpus, 1000 indexed documents took 4.20
seconds. Batch indexing now tokenizes before taking the write lock and commits
each index for the whole batch. The same workload takes 0.14 seconds: 238
documents per second increased to 6996, while allocations fell from 452 MB to
68 MB.

The generated index is compared byte for byte with the per-document path in the
test suite. If one document cannot be indexed in the batch, MDDB falls back to
the individual path instead of discarding the remaining documents.

Range searches now stop once they reach the requested limit instead of
materialising and sorting every match. Returning 50 results from the measured
10,000-document collection fell from 15.4 ms and 12.5 MB of allocations to 63
microseconds and 57.7 KB.

## Clients and local embeddings

When no embedding provider is configured, MDDB checks a local Ollama instance
at startup and selects a supported embedding model with known dimensions. An
explicit configuration or `MDDB_EMBEDDING_PROVIDER` still takes precedence;
`MDDB_EMBEDDING_AUTODETECT=0` disables discovery.

The new `langchain-mddb` package implements LangChain's `VectorStore` and
Retriever interfaces over the existing Python client. It uses server-side
embedding and exposes the search advisor through `recommended_settings()`.

MCP progress notifications and log messages now reach clients, while active
MCP sessions appear in `/health`. In mddb-chat,
`security.max_tokens_per_session` can cap provider-reported token use across
all tool-calling rounds in a session; zero keeps the previous unlimited
behaviour.

## Production behaviour is more explicit

Operational logs now use `log/slog`. `MDDB_LOG_FORMAT` selects text or JSON and
`MDDB_LOG_LEVEL` sets the threshold. Docker uses JSON by default. Operators who
match old message text in alerts or log queries must update those rules.

Graceful shutdown now stops accepting work, drains queues and then closes the
subsystems they use. `MDDB_SHUTDOWN_TIMEOUT_SEC` bounds the sequence, and the
compose files allow a 20-second stop grace period. Async bulk jobs also publish
`job.started`, `job.progress`, `job.completed`, `job.failed` and
`job.cancelled` over the existing SSE endpoint.

`GET /health` reports persistence conditions such as a read-only directory,
low free space or an ephemeral container filesystem. HNSW searches filter
deleted vectors, compact after substantial churn and fall back to a flat scan
if the underlying graph panics instead of taking down the server.

The CLI adds `mddb-cli self-update`. It downloads the release, verifies it
against `checksums.txt`, retains the previous binary as `.bak` and refuses to
overwrite installations owned by Snap or a container image. The daemon does
not replace itself; `mddbd --check-update` only reports whether an update is
available.

## Security defaults changed

The production compose file now requires `MDDB_AUTH_JWT_SECRET` and
`MDDB_AUTH_ADMIN_PASSWORD`, and published ports bind to `127.0.0.1` unless
`MDDB_BIND_ADDR` says otherwise. A fresh compose deployment can no longer start
as an unauthenticated, writable database on every host interface.

Collection configuration reads no longer return stored S3 or publishing
credentials. Presence flags show whether a secret exists, and an empty value on
write preserves it. MCP keys are identified by a SHA-256 fingerprint instead
of returning or logging a prefix containing part of the secret.

Embedding provider URLs retain loopback access for a local Ollama instance,
but other private and reserved addresses require the existing outbound
allow-list controls. Rate limiting can trust configured reverse proxies and
uses the rightmost untrusted address from `X-Forwarded-For`, preventing clients
from choosing their own bucket.

## Before upgrading

Review these changes before moving an existing deployment to 2.12:

1. Set `MDDB_AUTH_JWT_SECRET` and `MDDB_AUTH_ADMIN_PASSWORD` before starting
   the production compose file.
2. Set `MDDB_BIND_ADDR=0.0.0.0` only if another host must reach published ports;
   loopback is now the default.
3. Update log parsing and alerts for structured fields and new message text.
4. Replace use of MCP `keyPrefix` with the new `fingerprint` field.
5. Upgrade the Grafana datasource host to Grafana 13.
6. Re-embed code collections for the new chunk boundaries, then re-save or
   re-ingest them to populate symbol metadata for graph retrieval.
7. Check collections configured with `memory` or `s3`: `storageBackend` is now
   honoured, while existing documents remain where they were originally
   written.

The stored vector format remains backward compatible. A 2.12 server can read
older vectors, and older replicas ignore the trailing per-chunk hash added by
2.12. No database migration is required.

Installation and deployment details are in the [installation guide](/docs/installation/),
[deployment guide](/docs/deployment/) and [security reference](/docs/security/).
