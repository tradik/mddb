---
title: "A Database That Asks First and Says No"
slug: "blog/a-database-that-asks-first"
status: publish
type: post
date: 2026-10-29
tags: [release, agents, mcp, operations, search]
excerpt: "An agent that can delete a collection should have to ask someone first. A tenant that fills the disk should be told no before the disk is full. A conversion that produced nothing should say so. And a benchmark that disagrees with you should win. Notes on MDDB 2.18.9."
description: "MDDB 2.18.9 asks before deleting, refuses before the disk fills, flags bad conversions, and measures search before trusting it."
---

Most of MDDB's history is about doing more: more search modes, more
protocols, more ways to put documents in. This release is mostly about
refusing. It asks before acting. It says no before running out. When a
feature cannot do something, MDDB now says so. And when a measurement says
we were wrong, we change the code.

It came out of one question we kept hearing from people who let agents and
tenants loose on a shared server: *what stops this from going wrong?* Before
2.18.9 the honest answer was "not much, so be careful".

The full list is in the
[changelog](https://github.com/tradik/mddb/blob/v2.18.9/CHANGELOG.md).

## Asking before deleting

An MCP agent with write access could call `delete_collection` on a whim.
Nobody was asked. With `MDDB_MCP_CONFIRM_DESTRUCTIVE=elicit`, MDDB uses MCP
elicitation to ask the person behind the client before any of the five tools
that destroy data runs:

- `delete_collection`
- `delete_documents_batch`
- `delete_where`
- `truncate_revisions`
- `restore_backup`

Nothing happens unless the answer is yes. For a whole collection the person
also types its name, the way a cloud console asks before dropping a database.

Each transport carries the question its own way. On stdio the server writes
`elicitation/create` and waits. Over Streamable HTTP the reply to `tools/call`
becomes a short SSE stream. Stateless 2026-07-28 clients get an
`input_required` result and retry with the answer.

`require` refuses outright when a client cannot ask. The default is `off`.

## Saying no before the disk is full

On a shared server one collection grows until bbolt cannot extend its file,
and then every collection's writes fail together.

A collection can now carry a quota: documents, bytes, vectors, and a daily
embedding budget. A write that would cross a storage limit gets
`507 Insufficient Storage`. The check runs once, inside the write
transaction, so REST, gRPC, MCP, GraphQL, batch and ingest all refuse the same
way, and a batch over the limit is refused whole.

An embedding budget never refuses a document. The document is stored, and
its vector waits until midnight UTC or until someone raises the budget.

Two server-wide limits sit on top:

- `MDDB_DISK_RESERVE_BYTES` stops writes while free disk is below a reserve.
  Reads and backups keep working.
- `MDDB_VECTOR_RAM_LIMIT_BYTES` stops vectors from outgrowing memory.

## Backups that never eat the good ones

A cron automation can now take a backup and send it to a directory or to any
S3-compatible bucket. Every file gets a `.sha256` beside it, and uploads are
read back and checked.

The order is what matters. Old backups are deleted down to `keep` only after a
run succeeds. A schedule that has failed all week still leaves you the last
seven good backups.

Building this turned up an older bug. The scheduler accepted only six-field
cron expressions, while the docs showed five. So `0 3 * * *` was stored,
listed, and never run. Both forms run now, and an expression that does not
parse is refused when you save the rule.

## Saying when a conversion failed

`mddb-cli autoindex` indexes a folder in one command, and it had two gaps:

- It skipped every PDF, DOCX and ODT file as "binary".
- It sent HTML, RTF and TeX through raw, tags and all.

Now it recognises a file by its content and hands it to the same converters
`/v1/upload` uses. Those converters gained spreadsheets (XLSX, ODS, CSV),
slides with speaker notes (PPTX, ODP), EPUB and mail (EML, MBOX, each
attachment its own document). All of it is pure Go, with no new dependency.

The bigger change is honesty. A scanned PDF used to "convert" into almost
nothing, and that nothing went into the index looking like a document. Every
converted document now carries `meta.conversionConfidence`, from 0 to 1, with
warnings. A scan scores 0. The filter `{"conversionConfidence": {"lt": 0.5}}`
finds such documents, and the search advisor tells you how many there are.

Images in Markdown and PDF files can now become documents of their own. Their
alt text and captions are searchable, and `includeImages: true` returns each
hit's pictures with its results. With Voyage's or Cohere's multimodal models,
a text query also finds a picture by what it shows, and `queryImage` searches
with a picture. Providers that cannot embed images skip them, and
`indexing-status` says so instead of failing.

## Admitting what a feature cannot do

`MDDB_EMBEDDING_PROVIDER=lexical` gives vector search with no model and no
network, for CI, air-gapped installs and demos. It matches spelling, not
meaning: it finds `replication` when you type `replicaton`, and never finds
`copy` when you ask for `backup`. On MDDB's own documentation:

| Search | nDCG@10 |
|---|---|
| keyword (bm25) | 0.719 |
| hybrid, lexical vectors | 0.726 |
| hybrid, `nomic-embed-text` | 0.782 |

The first two differ by noise. So the server reports `semantic: false`, the
panel shows "Matches by meaning: No", and the advisor recommends keyword
search. It is never chosen automatically.

## Letting the benchmark win

`algorithm: "auto"` hands the choice of vector index to a query planner, and
`explain: true` shows its reasons. Before shipping it we measured it at 2,000,
200,000 and 2,000,000 vectors. It picked flat, HNSW and IVF as designed. At 2M
it answered 17× faster than flat at the median. That part went well.

The rest of the benchmark was less flattering:

- **HNSW recall was low and unstable.** It found only 0.74 to 0.88 of the
  true top ten, and the number changed every time the same data was loaded.
  - The graph library kept each point's nearest neighbours with no selection
    heuristic. Documents stored topic by topic became islands that a search
    could not leave.
  - It also ignored one of its own parameters and drew levels from the clock.

  MDDB now builds its own graph, following the paper. Recall is 1.000, queries
  are 3–5× faster, and loading is 6× faster.
- **The exact filtered scan read every vector and then filtered.** Its cost
  grew with the collection, not with the filter. It now reads only the
  documents the filter admits. p99 at 200k fell from 31 ms to 1.8 ms.
- **Some thresholds were wrong.** A filter admitting 5% to a third of a collection
  left HNSW short of results, so `auto` now sends those to IVF: at 10%, 2.5 ms
  instead of 36.

We also tested an idea and dropped it. Late interaction (ColBERT's MaxSim)
scores a document by many query vectors instead of one. Over our existing
chunks it changed nothing on ordinary questions. On questions that join two
topics, the case it was meant for, it lost 0.12 nDCG, with the whole
confidence interval below zero. What did help those questions was a different
idea: splitting the query and keeping each document's best part. That has
its own ticket. MaxSim is not in this release, and the numbers are in
[the benchmark notes](https://github.com/tradik/mddb/blob/v2.18.9/docs/BENCHMARK.md).

Two smaller steering tools shipped next to the planner:

- **Relevance feedback** moves a query towards documents you marked right and
  away from those you marked wrong.
- **Summaries** that a client writes into `meta.summary` are indexed as a
  second representation. "What is this about" can then find a document no
  single chunk answers.

The server never writes summaries itself; there is no language model inside
MDDB.

## Seeing where the time goes

With `MDDB_OTEL_ENDPOINT` set, MDDB sends OpenTelemetry traces. A
`traceparent` from REST, gRPC or MCP puts MDDB's spans into the caller's
trace, so you can see how long a search spent embedding, scanning, fusing
and loading. The exporter is built in. It adds 45 KB to the binary where
the SDK would add 1.5 MB, and with no endpoint set it costs nothing.

## The quiet fixes

Several bugs returned correct-looking results that were wrong:

- **Key case.** `README.md` was not found as `readme.md`, although
  identifiers are case-insensitive. Every database file now records a schema
  version, and older files are migrated on first open.
- **Encrypted TTLs.** Setting a TTL rewrote an encrypted document in plain
  text.
- **Hybrid order.** When scores tied, the order changed on every call.
  Sometimes that changed which documents made the top k.
- **Ingest TTLs.** They were applied by position, so one skipped document
  moved every later TTL onto the wrong document.
- **Metadata updates.** A metadata-only update left full-text search on the
  old values.
- **Two crashes.** A model name exactly two bytes long was misread as a
  quantized record, and OPQ panicked on short vectors.

Each of these now has a test that fails without the fix.

## Upgrading

Upgrade followers before the leader. To go back to 2.18.8, restore a backup
taken before the upgrade.

Everything new is opt-in and off by default:

- confirmation of destructive tools
- quotas
- scheduled backups
- the lexical provider
- `auto`
- summaries
- images
- tracing

A server that sets none of them behaves as it did yesterday. Searches are
faster and more accurate, and there are fewer bugs.
