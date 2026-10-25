---
title: "What's New in MDDB 2.18"
slug: "blog/whats-new-in-mddb-2-18"
status: publish
type: post
date: 2026-10-25
tags: [release, storage, mcp, search]
excerpt: "MDDB 2.18 reads C code, then spent two patch releases on what large databases taught it: restarts in milliseconds, reads during crash recovery, one file per collection, and OAuth for claude.ai connectors."
description: "MDDB 2.18.0 to 2.18.3: C, C++ and assembly in code_graph; vector search for keys with '#'; collection deletes that remove vectors; OAuth 2.1 on the MCP listener; stored freelists, background recovery, per-collection storage files, storage breakdown and an importable Go client."
---

MDDB 2.18.0 taught the server to read C, C++ and assembly. The two patch
releases after it came from the people who then loaded large source trees
into it: a database of about a million documents and 101 GB on disk, and the
problems that only show up at that size.

The full list is in the
[changelog](https://github.com/tradik/mddb/blob/2.18.3/CHANGELOG.md). This
article covers what changes for you.

## 2.18.0: C, C++ and assembly

`code_graph` now knows which file defines a function, what calls it and
which files include a header, for C, C++ and assembly as well as the
languages it already read.
[MDDB reads C, C++ and assembly](/blog/mddb-reads-c-code/) explains how it
was checked against `grep`.

## A restart no longer reads the whole file

bbolt, the storage engine under MDDB, keeps a list of free pages. By default
MDDB did not write that list to disk, because writing it costs every commit
a little. The price was paid at every start instead: the list was rebuilt by
reading every page of the file. On the 101 GB database that took 101
minutes, one random read at a time, and the server answered nothing until it
was done.

Three changes in 2.18.2 remove most of that:

- **A clean shutdown stores the list.** Stopping the server with SIGTERM
  writes it once, so the next start reads one page. Any write after that
  start clears it again, so a crash still costs a rebuild, which is what
  keeps the shortcut safe.
- **After a crash, reads do not wait.** The database opens as soon as its
  header is read, the server starts and answers searches and reads, and the
  list is rebuilt in the background. Writes get `503` with `Retry-After` and
  the progress until it is done, and `/health` reports `degraded` with
  `reads: true`, `writes: false`, a percentage and an ETA. This needed a
  change to bbolt itself, which MDDB now carries as a small patch
  ([MDDB-PATCH.md](https://github.com/tradik/mddb/blob/2.18.3/services/mddbd/third_party/bbolt/MDDB-PATCH.md)).
- **Files of 4 GiB or more store the list on every commit**, unless
  `MDDB_FREELIST_SYNC` says otherwise. Past that size a rebuild costs too
  much to risk; below it, commits stay as fast as they were.

Measured on a 2.5 GB database with the page cache dropped before each start:

| start | database open |
|---|---|
| rebuild, 2.18.1 | 56–60 s |
| after a clean shutdown | 0.014 s |
| after a crash | reads after 0.06 s; writes after the rebuild |

When the file fits in memory, the rebuild first reads it front to back. That
turned a 57-second rebuild into 1.5 s of reading and 0.4 s of rebuilding. It
reads the file twice: when memory is already full of other files, pages read
once are the first to be evicted, and after one pass only 52 to 89 percent
of them were still cached.

## One file per collection

With `MDDB_STORAGE_LAYOUT=per-collection`, each collection lives in a file of
its own under `<database>.d/`, and the main file keeps what belongs to the
server: users, keys, configuration, automations. Deleting a collection
removes its file, so the disk space comes back at once instead of staying
inside a file that never shrinks. After a crash each file is rebuilt on its
own, so a large collection does not hold up writes to a small one.

`mddbd -split-collections` moves an existing database to the new layout and
`-merge-collections` moves it back. Both copy every key, check it arrived,
and only then delete it from where it was. The default layout has not
changed. Replication snapshots are not available in this layout yet; the
binlog is. [STORAGE-LAYOUT.md](https://github.com/tradik/mddb/blob/2.18.3/docs/STORAGE-LAYOUT.md)
covers backups and the limits.

## Where the bytes are

The 101 GB database held 3 GB of content. `GET /v1/storage` now says where
the rest is: bytes per bucket, and for each collection its content, metadata
indexes, full-text postings and vectors, plus the free space a compaction
would give back. Postings are usually the largest part, so a collection
searched only by metadata or vectors can turn them off with
`"fts": {"enabled": false}` in its configuration. `mddbd -compact-db` copies
a database into a new file without its free space.

[STORAGE-TUNING.md](https://github.com/tradik/mddb/blob/2.18.3/docs/STORAGE-TUNING.md)
collects what matters for disks and filesystems: cloud volumes and their
IOPS limits, ZFS record sizes, and settings for WSL2, macOS and Windows.

## Fixes

- **After a restore, writes to users, keys, configurations and automations
  failed until a restart** (2.18.3): those managers kept the database handle
  the restore had closed.

- **Vector search found nothing for keys containing `#`** (2.18.1). Code collections
  key functions as `file#function`, and the chunk suffix was cut at the first
  `#`. Every vector hit for those documents was dropped while `vector-stats`
  counted them as embedded.
- **Deleting a collection left its vectors behind**, and, depending on the
  API used, its full-text index, TTL entries and configuration. A collection
  deleted and written again reported more embedded documents than it held.
- **Identifiers in code were not found by full-text search**, and
  `disableStem` turned stemming off for every concurrent search.
- **MCP `full_text_search` ignored most of its arguments.** It now runs the
  same search as `/v1/fts`, and a misspelt key in the MCP configuration stops
  the server at startup instead of being ignored.

## MCP for claude.ai

claude.ai custom connectors connect only with OAuth. With
`MDDB_MCP_OAUTH_ENABLED=true` the MCP listener is a small OAuth 2.1
authorization server: discovery, client registration, authorization codes
with PKCE and refresh tokens. The consent page asks for an MCP API key, and
every token stands for that key: it can do what the key can, and deleting
the key revokes it.

Also for MCP: custom prompts next to custom tools, custom tools that wrap
hybrid search, `code_graph` and aggregation, and
`MDDB_MCP_INSTRUCTIONS_FILE` for server instructions too long for an
environment variable.

## Also in this release

- Chunk size and mode per collection (`embedding: {chunkSize, chunkMode}`),
  so long functions and short articles can share a server.
- The Go client can be imported:
  `go get github.com/tradik/mddb/clients/go/mddb/v2@latest`, with typed
  methods for ingest, aggregation, collection configuration, hybrid search
  and `code_graph`, and a `Status` call that reports a starting server as
  starting rather than as down.
- Startup logs each phase with its duration.

## Upgrading

Nothing to do for most installations. A database of 4 GiB or more starts
storing its freelist on every commit; set `MDDB_FREELIST_SYNC=false` to keep
the old behaviour. Give the container time to stop: the server needs up to
15 seconds to shut down and store the freelist, and Docker's default is 10.
The bundled compose files set `stop_grace_period: 20s`.
