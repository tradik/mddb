---
title: "MDDB vs Alternatives"
slug: "docs/comparison"
description: "Measured numbers and honest trade-offs: how MDDB compares with relational and document databases, search engines, vector stores and RAG wrappers — with the command that reproduces every figure."
status: publish
---

# MDDB vs Alternatives

Every number on this page was measured, not estimated, and each one names the
command that produces it again. Where a figure depends on your corpus — and
most of them do, heavily — the page says so and shows by how much.

The comparisons keep their "when to use the other thing instead" sections.
Those are the most useful part: a page that recommends itself for everything is
a page nobody believes.

## How these numbers were produced

```bash
# start a server, then:
make bench-comparison                      # MDDB's own profile
make bench-comparison BENCH_ARGS="-docs 20000 -words 160"
make bench-comparison-all                  # cross-database (needs Docker)
```

| Parameter | Value |
|---|---|
| Measured | 2026-08-23 |
| MDDB | 2.12.0, release build (`CGO_ENABLED=0`, `-ldflags="-s -w"`) |
| Host | linux/amd64, 32 CPUs, 16 GB RAM |
| Go | 1.27.0 |
| Corpus | generated prose, Zipfian term distribution over an 8 000-word vocabulary |
| Transport | HTTP/JSON, batches of 500 documents |
| Cross-database run | 2026-08-23, same host, containers from `test/docker-compose.benchmark.yml` |
| Search | 500 timed queries after 20 warm-up queries; percentiles, not averages |

The corpus generator is worth one sentence, because two earlier versions of it
produced numbers that were wrong in opposite directions. A fifteen-word
vocabulary gave almost no distinct terms, and the database came out ten times
its content. Then 250 words drawn uniformly gave the opposite pathology — every
document containing nearly every term, which is the densest an inverted index
can be — and the database came out twenty-seven times its content. Neither said
anything about the engine. Natural language is Zipfian, so the generator is
too.

## MDDB, measured

Prose documents of ~1.7 KB, full-text indexing on:

| Corpus | Ingest | Search p50 | p95 | p99 | On disk | Server RSS |
|---|---|---|---|---|---|---|
| 1 000 docs | 207 docs/s | 745 µs | 993 µs | — | 79 MiB | 223 MiB |
| 5 000 docs | 562 docs/s | 1.54 ms | 2.74 ms | 3.09 ms | 325 MiB | 274 MiB |
| 20 000 docs | 653 docs/s | 5.90 ms | 7.81 ms | 8.54 ms | 1 175 MiB | 1 030 MiB |

Two things in that table deserve explaining rather than burying.

**Ingest looks slow at 1 000 documents and faster at 20 000.** That is a fixed
start-up cost — roughly four seconds of bucket creation and file growth — being
amortised. The marginal rate is what the 20 000-document row shows.

**The database is 35× the size of its content, and RSS grows with it.** That is
almost entirely the full-text index, and you can watch it disappear. The same
5 000 documents, ingested with `skipFts`:

| Same corpus, 5 000 docs | Ingest | On disk |
|---|---|---|
| Full-text index on | 562 docs/s | 325 MiB |
| `"options": {"skipFts": true}` | **13 883 docs/s** | **16 MiB** |

**The index costs 25× the write throughput and 20× the storage.** That is the
honest price of instant full-text search over every document, and it is a
choice you get to make per ingest — `profile: "fast"` names the same trade-off.
If your workload is write-heavy and searched rarely, turn it off and reindex on
demand.

It also explains a number this page used to carry. The previous version claimed
**29 810 docs/s** with no methodology attached. Nothing in this repository
reproduces that against an indexed corpus of real prose; it is the shape of a
figure measured with tiny documents and no meaningful index work. It has been
removed rather than adjusted.

**Binary and image:** the release binary is 27.7 MB (`CGO_ENABLED=0 go build
-ldflags="-s -w"`; a plain `go build` is ~40 MB, because it keeps the symbol
table). The Docker image is ~33 MB.

## Against other databases, measured

Docker was unavailable when this page was first rewritten, so it carried no
cross-database numbers rather than invented ones. Here they are.

**The workload:** 3 000 documents, inserted **one at a time** over each
system's normal protocol, rotating three Lorem Ipsum sizes (124 B, 707 B,
1876 B). `test/compare-all-databases.sh` runs it; every client lives beside it
in `test/`.

| System | Version | Inserts/s | Median insert |
|---|---|---|---|
| MongoDB | 8 | **2 358** | 414 µs |
| PostgreSQL | 17 | **1 368** | 712 µs |
| **MDDB**, no full-text index | 2.12.0 | **502** | 1 798 µs |
| MySQL | 9.1 | **454** | 2 131 µs |
| CouchDB | 3 | **202** | 4 880 µs |
| **MDDB**, full-text index on | 2.12.0 | **104** | 9 883 µs |

Two rows for MDDB, because the comparison is otherwise dishonest in both
directions.

**None of the other systems is building a full-text index in that table.** A
PostgreSQL row with a GIN `tsvector` index would not be at 1 368 either. MDDB
indexes every document for search on write by default, and that costs it a
factor of five: 502 inserts/s becomes 104. Compare the row that matches what
you are asking the other system to do.

**Single inserts are the wrong way to load a corpus into MDDB**, and the table
uses them only because it is the one shape every system supports identically.
Through the batch API the same engine does **13 883 docs/s** without the index
and **562** with it — the figures in the section above. If you are loading a
corpus and comparing to a bulk-load path elsewhere, those are the numbers.

Where MDDB lands honestly: an embedded single-file store doing an fsync per
write sits between PostgreSQL and MySQL on raw insert rate, and last by a wide
margin once you ask it to also make every document searchable. What it buys is
that the search index exists at all — the systems above it in the table need a
second system for that, and then a synchronisation problem between the two.

---

## vs Relational databases

### PostgreSQL / MySQL

The real difference is not performance, it is what you have to operate. MDDB is
one process with one file; the comparison is against a server, a connection
pool, a schema, and a migration story.

| | MDDB | PostgreSQL / MySQL |
|---|---|---|
| Deployment | one binary, one file | server process, users, schema, migrations |
| Markdown | native document type, front matter parsed | `TEXT` column |
| Revision history | every write, automatically | triggers and a history table you write |
| Vector search | built in | pgvector (Postgres) / none (MySQL) |
| Full-text search | built in, four algorithms | built in (Postgres), basic (MySQL) |
| Relational joins | none | the entire point |
| Multi-table transactions | no | yes |

**Choose PostgreSQL or MySQL when** you need joins, multi-table transactions,
or the ecosystem — ORMs, migration tools, managed hosting, people who already
know it. If your data is relational, a document store is the wrong shape and no
benchmark will fix that.

## vs Document databases

### MongoDB, CouchDB

Closer in shape, and the trade is scale against footprint. These are built to
shard across a cluster; MDDB is built to be one file you can copy.

| | MDDB | MongoDB | CouchDB |
|---|---|---|---|
| Horizontal sharding | consistent-hash ring, manual | automatic, mature | clustered |
| Protocols | HTTP, gRPC, GraphQL, MCP | wire protocol | HTTP |
| Change feed | webhooks, binlog replication | change streams | `_changes` |
| Markdown-aware | yes | no | no |
| Docker image | ~33 MB | ~400 MB | ~180 MB |
| Inserts/s, measured | 502 (no index) / 104 (indexed) | 2 358 | 202 |

**Choose MongoDB when** you need automatic sharding at a scale one machine
cannot hold, change streams, or its geospatial features beyond what
[GEOSEARCH](GEOSEARCH.md) covers. **Choose CouchDB when** offline-first
replication to clients is the requirement.

## vs Search engines

Heavyweight search clusters are a different class of tool. They win on scale
and on analysis depth; they cost a JVM, a cluster, and an operations budget.

| | MDDB | Heavyweight search engine |
|---|---|---|
| Deployment | one binary | cluster, JVM, coordinator nodes |
| Baseline memory | ~90 MiB with 5 000 short documents | gigabytes before any data |
| Ranking algorithms | TF-IDF, BM25, BM25F, [PMISparse](PMISPARSE.md) | BM25 plus a large analysis toolkit |
| Analyzers | stemming, stop words, synonyms, 30+ languages | far more, and pluggable |
| Vector search | built in, hybrid with keyword | built in (recent versions) |
| Distributed search | no | yes, that is the product |

The algorithm choice is measured too, on quality as well as speed:
[FTS Algorithm Benchmark](BENCHMARK.md) compares all four on the same corpus,
and [PMISparse](PMISPARSE.md) documents the sparse-retrieval algorithm that
performs best on short technical queries here.

**Choose a heavyweight search engine when** you need a distributed search
cluster, log aggregation, or analysis features — custom analyzers, complex
aggregations, percolation — beyond what a document store should be doing.

## vs Vector databases

| | MDDB | Dedicated vector database |
|---|---|---|
| Vectors | one part of the store | the whole product |
| Index types | flat, IVF, quantized (int8/int4) | HNSW, IVF-PQ, DiskANN, more |
| Hybrid keyword + vector | native, one query, alpha or RRF | usually a separate keyword system |
| The documents themselves | stored here | usually somewhere else |
| Scale ceiling | one machine's disk | designed for billions |

The honest framing: if vector search *is* your application and you have more
than a few million vectors, use something built for that. MDDB's advantage is
that the vectors, the text, the metadata and the revision history are the same
records — no synchronisation between two systems, no "the vector store has a
document the database deleted".

See [Quantization](QUANTIZATION.md) for the memory trade-offs, including
disk-only vectors.

## vs RAG wrappers

A category worth naming, because it is where most "memory for my agent"
searches land: tools that wrap somebody else's retrieval engine in an MCP
server and a CLI.

They are a genuinely good way to start. They are also a coupling decision, and
the coupling is invisible until it matters.

| | MDDB | Wrapper over a third-party engine |
|---|---|---|
| Who owns the storage format | this project | the upstream engine |
| Who decides the release cadence | this project | upstream, then the wrapper |
| Deployment | one binary | Python runtime, the engine, its dependencies |
| MCP | native — 80 tools over the same core the HTTP API uses | an adapter layer over an engine that predates MCP |
| Per-collection retrieval config | [built in](RAG-PIPELINE.md) | whatever upstream exposes |
| Replication | binlog-based, built in | whatever upstream exposes |
| Upgrade risk | one project's decisions | two projects' decisions, in sequence |

The practical difference shows up in small requests. "Cap the context this
collection returns", "make search results carry the collection's answer
format", "annotate which tools are read-only so an agent cannot write" — each
is a change to one codebase here. Through a wrapper, each one is either
upstream's decision or a workaround layered on top of it.

**Choose a wrapper when** you want to try retrieval-augmented generation this
afternoon, your knowledge base is small, and one person is using it. The
setup cost is genuinely lower and you are not committing to anything.

**Choose an engine when** the knowledge base is part of your product: when you
need the storage format to be stable because you have data in it, when an
upstream release should not be able to change your retrieval behaviour, and
when "one more service to operate" is a real cost.

## vs Git and the filesystem

Storing Markdown in Git is the right answer more often than vendors like to
admit. It is free, diffable, reviewable, and every developer already knows it.

| | MDDB | Git + filesystem |
|---|---|---|
| Query by metadata | indexed, instant | `grep` and a script |
| Full-text search | indexed, ranked | `grep`, unranked |
| Vector search | built in | none |
| Concurrent writers | handled | merge conflicts |
| Serving over an API | built in | you build it |
| Human review of changes | revisions, no diff UI | pull requests, the best in the world |
| Cost | a process | nothing |

**Choose Git when** the content is authored by developers, changes go through
review, and search means `grep`. Use both when content is authored in Git and
*served* by something that can rank it — that is what
[the WordPress and SSG integrations](INTEGRATIONS.md) do.

## vs CMS platforms

### WordPress, Strapi

Different audience. A CMS is for people who need an editing interface; MDDB is
for applications that need an API. That is not a ranking, it is a different
question being answered.

| | MDDB | WordPress / Strapi |
|---|---|---|
| Editing UI | admin panel, developer-oriented | full editorial workflow |
| Non-technical authors | no | yes, that is the product |
| Plugin ecosystem | integrations, not plugins | thousands |
| API-first | yes | REST bolted on (WordPress), yes (Strapi) |
| Vector search for RAG | built in | none |
| Deployment | one binary | PHP/Node + database + web server |

**Choose a CMS when** humans who do not write code need to publish. If that is
your requirement, the [WordPress plugin](INTEGRATIONS.md) puts MDDB behind it —
authors keep their editor, and the content becomes searchable by an agent.

## When MDDB fits

- Markdown is your primary format, not a `TEXT` column.
- You want retrieval — keyword, vector or both — without operating a second
  system for it.
- You are building for agents, and want MCP that was designed in rather than
  adapted on.
- One binary and one file is a feature, not a limitation.
- Your corpus fits comfortably on one machine.

## When it does not

- Your data is relational. Use a relational database.
- You need a distributed search or vector cluster. Use one.
- Your authors are not developers and need an editorial workflow. Use a CMS —
  and put MDDB behind it if the content should also be searchable by an agent.
- You need more vectors than one machine's disk holds.

---

**[← Back to README](../README.md)** | **[See all features →](FEATURES.md)** |
**[FTS algorithm benchmark →](BENCHMARK.md)**
