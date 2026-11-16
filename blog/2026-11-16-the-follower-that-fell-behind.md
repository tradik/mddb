---
title: "The Follower That Fell Behind"
slug: "blog/the-follower-that-fell-behind"
status: publish
type: post
date: 2026-11-16
tags: [release, operations, performance, search]
excerpt: "A benchmark we wrote for 2.19.8 caught a replica applying 20 documents a second and a checksum that could not tell 5,601 documents from 6,601. Both are fixed, and leader election came with them."
description: "MDDB 2.19.8: replicas that keep up, a checksum that sees missing documents, leader election, vector indexes that start from disk, filtered HNSW."
---

We started 2.19.8 by writing a benchmark, mostly so the rest of the release
would have something to be measured against. It found two bugs before we had
changed a line of anything else.

The benchmark (`make bench-profile`) starts real servers on a seeded corpus and
times what users do: searches with and without filters, ingest, a restart, a
`kill -9` in the middle of writes, and a leader with one follower. The
replication part writes to the leader at a gentle 20 documents a second and
checks how long each one takes to show up on the follower.

None of the 50 probe documents arrived within 30 seconds.

Then the follower was stopped for a while and brought back. After three
minutes it still had 5,601 documents to the leader's 6,601, and
`/v1/checksum` reported the same value on both nodes.

## Seventy-seven syncs per document

The follower was not stuck. It was slow, and the reason only showed up on a
real disk. Our earlier replication tests ran on tmpfs, where a sync costs
nothing.

A document is not one entry in the binlog. It is the document itself, its key
index, a revision, the metadata entries and the full-text terms. On the
benchmark corpus that came to about 77 entries per document. The follower
applied each entry in its own transaction, and each transaction syncs. So it
did roughly 77 syncs per document and managed about 20 documents a second,
while the leader, taking whole documents per transaction, did around 210.
Under any steady load the follower fell further behind for as long as the load
lasted.

In 2.19.8 the follower applies whatever has arrived in one go, up to 1,024
entries, with one transaction per database file and its position recorded in
the same transaction. If it crashes between two files it replays a few entries
one file already holds, which changes nothing. On the same disk the median probe
now arrives in 0.4 ms, none of the 50 is late, and the 5,000 documents the
follower missed while stopped come back in under two seconds.

## A checksum that cancelled itself out

The checksum was the more embarrassing bug, because its whole job was to catch
exactly this.

It took the CRC32 of each key and of the first 64 bytes of each value and
XORed them all together. CRC32 is linear over XOR. Feed it keys that differ
only in a counter, `late-00000000` to `late-00000999`, with values that start
the same way, and pairs of documents cancel each other out. A thousand missing
documents left the checksum exactly where it was. It also never looked past the
first 64 bytes, so an edit further down a document did not register either.

The new checksum hashes the whole key and the whole stored document, mixes the
two through a non-linear function and adds the results, so the order documents
are read in still does not matter. Missing, extra and changed documents all
move it. It is 16 hex digits now instead of 8, which means values from earlier
releases cannot be compared with new ones.

## The order things were written in

While someone was in that code anyway, they found an older problem. A write
committed its transaction and only then appended its entries to the binlog,
outside any lock. Two clients writing the same document at the same moment
could commit A then B, and log B then A. The follower replays the log, so it
ended up with A, the older version, with nothing to say anything had gone
wrong.

Now the entries are appended inside the write's own transaction. The storage
engine runs one write transaction per file at a time, so the log is in commit
order by construction. A test with 16 writers on one key failed in the first
round on the old code and passes now. Ingest throughput did not move beyond the
noise between runs.

## Electing a leader

All of that had to be in place before the larger feature of the release. Until
now the leader was whoever you started with `MDDB_REPLICATION_ROLE=leader`.
When it died, the followers kept serving reads and kept dialling a server that
was not there, and promoting one of them meant restarting it by hand.

With `MDDB_CLUSTER_PEERS` set, nodes now elect their leader. Each leadership
has a term number, stored on disk before a node votes. A node votes once per
term, and only for a candidate whose log is at least as complete as its own. A
leader keeps writing only while a majority answers its heartbeats. On the
minority side of a split network nothing accepts writes.

On three real processes with the default timings, a new leader was taking
writes 2.6 to 3.7 seconds after we sent the old one `kill -9`. When the old
leader comes back it finds a higher term and follows. Any writes it took that
nobody else received go to a `rejected-writes-*.jsonl` file for a person to
look at, instead of quietly disappearing or quietly winning.

A few things a reader of this should know before relying on it:

- It needs three nodes. With two there is no majority to elect anybody, so
  automatic failover stays off and `POST /v1/replication/promote` (or
  `mddb-cli replication promote`) is how you move the leader.
- By default a write is acknowledged once the leader has it, as before. If the
  leader dies before a follower receives it, that write is lost even though the
  client was told it succeeded. `MDDB_REPLICATION_ACK=quorum` waits for a
  majority instead, at the cost of latency.
- A node that is not the leader answers a write with `421` and the leader's
  address in `X-MDDB-Leader`. The Go, Python and Node clients can follow it if
  you switch that on; it is off by default.

The election code is ours, about 1,200 lines in `internal/cluster`, rather
than a library. It does one job, and it is small enough that every line of it
is covered by tests, including the ones that cut the network in half.

## Vector indexes that start from disk

Until 2.19.8 MDDB stored vectors on disk and rebuilt every index from them at
start. For HNSW on 200,000 vectors of 128 dimensions that took about two
minutes. The indexes that need training, IVF, PQ, OPQ and the scalar
quantizers, took 56 minutes, and while they trained the server answered
searches on them with 503.

Each collection's indexes are now written to a checkpoint file in the
background and on a clean shutdown. At start the server reads the file and
checks it against the stored vectors one by one, then adds whatever was written
since. The same restart is ready in 4 to 10 seconds for every algorithm. If the
file is damaged, or was written for another embedding model or another
dimension, the index is rebuilt the old way and the server starts normally. We
tested that by flipping one byte in the file, and by killing the process while
it was writing one.

## Filters that do not fight the graph

HNSW used to handle a metadata filter by taking three times as many neighbours
as asked for, throwing away the ones the filter did not allow, and scoring the
whole collection if too few were left. At 200,000 vectors, a filter that let
through 10% of documents took 30 ms at p95, and one that let through 25% took
44 ms.

The filter is now applied during the walk through the graph. Nodes it excludes
are still used to get from one part of the graph to another, but never
returned. When a filter admits so little that walking does not pay, MDDB
scores just the admitted documents, exactly. The same two cases now take 1.3 ms
and 0.64 ms, with recall@10 between 0.98 and 1.0. `explain` tells you which
strategy ran.

IVF had a related problem: behind a filter admitting 2% of documents it found
only 77% of the true nearest neighbours. It now keeps probing clusters until it
expects to have seen enough admitted vectors, and finds all of them.

## Also in 2.19.8

- **Reranking.** A search can pass its best candidates to a reranking model
  you configure (`MDDB_RERANK_*`). It is off unless you ask for it. If the model
  times out or fails, the search returns its own order with `rerankSkipped`
  instead of an error, and `/v1/eval` will tell you whether the model actually
  improved results on your own questions.
- **Tenants.** Quotas can now cover a whole tenant, and request limits apply per
  tenant and per API key. Background embedding and indexing serve tenants in
  turn instead of first come, first served. In our test, one tenant's job
  queued behind another tenant's 300-job backlog ran second. It does not make
  tenants fully independent: a quiet tenant's p95 still rose 1.5 to 3.4 times
  while a neighbour bulk-loaded, because they share one process.
- **One search limit.** `MDDB_SEARCH_MAX_CONCURRENT` used to cover some REST
  routes and nothing else. It now covers every search on REST, gRPC, GraphQL
  and MCP.
- **Docs that match the code.** Our comparison page listed horizontal sharding,
  and one feature page promised sub-100 ms replication lag. The sharding code
  was never called by anything, and nobody had measured the lag. Both claims are
  gone, and a test now fails if a sharding claim comes back without code
  behind it.

## Before you upgrade

Upgrade followers before the leader. Leader and followers must use the same
`MDDB_STORAGE_LAYOUT`. Checksums from older releases are not comparable with new
ones. When the embedding queue is full, a document now waits for a slot
instead of being dropped; `MDDB_EMBEDDING_PARK_MAX=0` restores the old
behaviour. None of the cluster features change anything until you set
`MDDB_CLUSTER_PEERS`.

The benchmark that started all this runs in CI on pull requests that touch
search, vector indexes or replication. Its numbers live in
`docs/benchmarks/`, so the next time a follower falls behind, we should see it
before you do.
