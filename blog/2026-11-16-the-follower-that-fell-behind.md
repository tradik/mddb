---
title: "The Follower That Fell Behind"
slug: "blog/the-follower-that-fell-behind"
status: publish
type: post
date: 2026-11-16
tags: [release, operations, performance, search]
categories: [Releases]
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

Then we stopped the follower for a while and brought it back. After three
minutes it still had 5,601 documents to the leader's 6,601, and
`/v1/checksum` reported the same value on both nodes.

## Seventy-seven syncs per document

It looked stuck, but it was only slow, and only on a real disk. Our earlier
replication tests ran on tmpfs, where a sync costs nothing.

Each document turns into many binlog entries: the document itself, its key
index, a revision, the metadata entries and the full-text terms. On the
benchmark corpus that came to about 77 entries per document. The follower
applied each entry in its own transaction, and each transaction syncs, so it
did roughly 77 syncs per document and managed about 20 documents a second.
The leader writes a whole document per transaction and did around 210. With
writes coming in steadily, the follower could only lose ground.

In 2.19.8 the follower applies whatever has arrived in one go, up to 1,024
entries, with one transaction per database file and its position recorded in
the same transaction. If it crashes between two files it replays a few entries
one file already holds, which changes nothing. On the same disk the median probe
now arrives in 0.4 ms, none of the 50 is late, and the 5,000 documents the
follower missed while stopped come back in under two seconds.

## A checksum that cancelled itself out

The checksum was the more embarrassing of the two, since catching exactly this
was its whole job.

It took the CRC32 of each key and of the first 64 bytes of each value and
XORed them all together. CRC32 is linear over XOR. Feed it keys that differ
only in a counter, `late-00000000` to `late-00000999`, with values that start
the same way, and pairs of documents cancel each other out. A thousand missing
documents left the checksum exactly where it was. It also never read past the
first 64 bytes, so an edit lower down in a document went unnoticed.

The new checksum hashes the whole key and the whole stored document, mixes the
two through a non-linear function and adds the results, so the order documents
are read in still does not matter. Missing, extra and changed documents all
move it. It is 16 hex digits now instead of 8, so values from earlier releases
cannot be compared with new ones.

## The order things were written in

While we were in that code, we found an older problem. A write committed its
transaction and only then appended its entries to the binlog, outside any
lock. Two clients writing the same document at the same moment could commit A
then B, and log B then A. The follower replays the log, so it ended up with A,
the older version, and nothing reported an error.

Now the entries are appended inside the write's own transaction. The storage
engine runs one write transaction per file at a time, so the log is in commit
order by construction. A test with 16 writers on one key failed in the first
round on the old code and passes now. Ingest throughput stayed within the
noise between runs.

## Electing a leader

All of that had to be in place before the larger feature of the release. Until
now the leader was whichever node you started with
`MDDB_REPLICATION_ROLE=leader`. When it died, the followers kept serving reads
and kept dialling a server that was gone, and promoting one of them meant
restarting it by hand.

With `MDDB_CLUSTER_PEERS` set, nodes elect their leader. Every leader holds a
numbered term, and a node writes the term to disk before it votes. It votes
once per term, and only for a candidate whose log is at least as complete as
its own. A leader keeps writing only while a majority answers its heartbeats,
so on the minority side of a split network nothing accepts writes.

On three real processes with the default timings, a new leader was taking
writes 2.6 to 3.7 seconds after we sent the old one `kill -9`. When the old
leader comes back it sees the higher term and follows. Writes it took that no
other node received are saved to a `rejected-writes-*.jsonl` file for someone
to review, so they neither vanish nor overwrite what the new leader has.

Before relying on it:

- It needs three nodes. Two cannot form a majority, so automatic failover
  stays off and `POST /v1/replication/promote` (or
  `mddb-cli replication promote`) is how you move the leader.
- By default a write is acknowledged once the leader has it, as before. If the
  leader dies before a follower receives it, that write is lost even though the
  client was told it succeeded. `MDDB_REPLICATION_ACK=quorum` waits for a
  majority, at some cost in latency.
- A node that is not the leader answers a write with `421` and the leader's
  address in `X-MDDB-Leader`. The Go, Python and Node clients can follow it if
  you turn that on; it is off by default.

We wrote the election code ourselves, about 1,200 lines in `internal/cluster`,
rather than pull in a library. It does one job, and it is small enough for the
tests to cover every line, including the ones that cut the network in half.

## Collections on more than one group

Once a group of nodes could pick its own leader, we could run several groups.
With `MDDB_PLACEMENT_GROUPS`, each collection lives, whole, in one group. A
new collection goes where a consistent hash puts it and stays there. Any node
takes any request and passes it to the group that holds the collection, or
answers `421` with the address if you would rather the client went there
itself. One group, the meta group, keeps the map of which collection lives
where, along with the users, keys and other settings every group needs.

`POST /v1/placement/move` moves a collection while it is being read and
written. The target copies the collection's file, then catches up from the
source's log. When it is nearly level, the source holds new writes for a
moment, the target applies the last of them, and the map changes. In our test,
20,000 documents with their vectors moved in 8 to 9 seconds while about 13,000
writes and 22,000 reads went through. None failed and no acknowledged write was
lost.

A collection is never split across groups, and nothing rebalances on its own,
so a collection that outgrows one group still has to fit in one group.
Placement also needs the per-collection storage layout, because a collection is
copied and dropped as its own file.

## Vector indexes that start from disk

Until 2.19.8 MDDB kept vectors on disk and rebuilt every index from them at
start. For HNSW on 200,000 vectors of 128 dimensions that took about two
minutes. The indexes that need training, IVF, PQ, OPQ and the scalar
quantizers, took 56 minutes, and while they trained the server answered
searches on them with 503.

Each collection's indexes are now written to a checkpoint file in the
background and on a clean shutdown. At start the server reads the file, checks
it against the stored vectors one by one, and adds whatever was written since.
The same restart is ready in 4 to 10 seconds for every algorithm. If the file
is damaged, or belongs to another embedding model or another dimension, the
server rebuilds the index the old way and starts normally. We tested that by
flipping one byte in the file, and by killing the process while it was writing
one.

## Filtered search in HNSW

HNSW used to handle a metadata filter by taking three times as many neighbours
as asked for, throwing away the ones the filter did not allow, and scoring the
whole collection if too few were left. At 200,000 vectors, a filter that let
through 10% of documents took 30 ms at p95, and one that let through 25% took
44 ms.

Now the filter is applied while the search walks the graph. Excluded nodes
still serve as stepping stones from one part of the graph to another, but they
never come back as results. When a filter admits so little that walking does
not pay, MDDB scores only the admitted documents, exactly. The same two cases
now take 1.3 ms and 0.64 ms, with recall@10 between 0.98 and 1.0, and
`explain` tells you which strategy ran.

IVF had a related problem: behind a filter admitting 2% of documents it found
only 77% of the true nearest neighbours. It now keeps probing clusters until it
expects to have seen enough admitted vectors, and finds all of them.

## Also in 2.19.8

A search can pass its best candidates to a reranking model you configure
(`MDDB_RERANK_*`). It stays off unless you ask for it. If the model times out
or fails, the search returns its own order with `rerankSkipped` rather than an
error, and `/v1/eval` will tell you whether the model improved results on your
own questions.

Quotas can now cover a whole tenant, and request limits apply per tenant and
per API key. Background embedding and indexing take tenants in turn rather than
in arrival order: in our test, one tenant's job queued behind another tenant's
300-job backlog ran second. Tenants still share one process, though, and a
quiet tenant's p95 rose 1.5 to 3.4 times while a neighbour bulk-loaded.

`MDDB_SEARCH_MAX_CONCURRENT` used to cover some REST routes and nothing else.
It now covers every search on REST, gRPC, GraphQL and MCP.

We also corrected two claims in our own docs. The comparison page said MDDB
could spread one database across nodes by hashing, and a feature page promised
sub-100 ms replication lag. No code path used that hashing, and nobody had
measured the lag. Both claims are gone, and a doc test now fails if the first
one comes back without code behind it.

## Before you upgrade

Upgrade followers before the leader. Leader and followers must use the same
`MDDB_STORAGE_LAYOUT`. Checksums from older releases are not comparable with new
ones. When the embedding queue is full, a document now waits for a slot
instead of being dropped; `MDDB_EMBEDDING_PARK_MAX=0` restores the old
behaviour. None of the cluster features change anything until you set
`MDDB_CLUSTER_PEERS`.

The benchmark that started all this now runs in CI on pull requests that touch
search, vector indexes or replication, and its numbers are kept in
`docs/benchmarks/`. A follower that falls behind again should fail a pull
request before it reaches a release.
