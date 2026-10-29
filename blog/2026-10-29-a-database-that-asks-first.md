---
title: "A Database That Asks First and Says No"
slug: "blog/a-database-that-asks-first"
status: publish
type: post
date: 2026-10-29
tags: [release, agents, mcp, operations, search]
excerpt: "An agent that can delete a collection should have to ask someone first. A tenant that fills the disk should be told no before the disk is full. And a search feature that matches spelling should not call itself semantic. Notes on MDDB 2.18.9."
description: "MDDB 2.18.9: MCP tools that ask before deleting, per-collection quotas, scheduled backups that never eat old ones, honest lexical vectors, and a query planner."
---

Most of MDDB's history is about doing more: more search modes, more
protocols, more ways to put documents in. This release is mostly about
refusing. It asks before acting, it says no before running out, and it is
honest about what a feature cannot do.

It came out of one question we kept hearing from people who let agents and
tenants loose on a shared server: *what stops this from going wrong?* Before
2.18.9 the honest answer was "not much, so be careful". Here is what changed.

The full list is in the
[changelog](https://github.com/tradik/mddb/blob/v2.18.9/CHANGELOG.md).

## Asking before deleting

An MCP agent with write access could call `delete_collection` on a whim. Most
of the time it did what it was asked. The other times, nobody was asked at all.

MCP has a way for a server to put a question to the person behind the client:
elicitation. With `MDDB_MCP_CONFIRM_DESTRUCTIVE=elicit`, the five tools that
destroy data ask first:

- `delete_collection`
- `delete_documents_batch`
- `delete_where`
- `truncate_revisions`
- `restore_backup`

Nothing happens unless the answer is yes. For a whole collection the person
also has to type its name, the way a cloud console asks before dropping a
database.

It works on every transport that can carry a question. On stdio the server
writes `elicitation/create` and waits. Over Streamable HTTP the answer to the
`tools/call` becomes a short SSE stream. Stateless clients on the 2026-07-28
protocol get an `input_required` result and retry with the answer.

`require` goes further and refuses outright when a client cannot ask. The
default is `off`, so nothing changes until you opt in. See
[Confirming Destructive Tools](https://github.com/tradik/mddb/blob/v2.18.9/docs/MCP.md).

## Saying no before the disk is full

On a shared server, a disk fills up quietly. One collection grows until bbolt
cannot extend its file, and then every collection's writes fail together.

A collection can now carry a quota: documents, bytes, vectors, and a daily
embedding budget. A write that would cross a storage limit is refused with
`507 Insufficient Storage`.

The check runs inside the write transaction, in one place, so REST, gRPC,
MCP, GraphQL, batch and ingest all refuse the same way. A batch over the
limit is refused whole, and half a batch is never written.

Embedding budgets behave differently, on purpose. Going over one never
refuses a document: the document is stored, and its vector waits as pending
until midnight UTC or until someone raises the budget.

Two server-wide limits sit on top. `MDDB_DISK_RESERVE_BYTES` stops document
writes while free disk is below a reserve. Reads and backups keep working,
because those are what you need while sorting it out. `MDDB_VECTOR_RAM_LIMIT_BYTES`
stops vectors from outgrowing memory.

## Backups that never eat the good ones

A cron automation can now take a backup instead of calling a webhook. It
writes to a directory or to any S3-compatible bucket, with a `.sha256` next to
each file. Uploads are read back and checked.

The detail that matters is the order. The oldest backups are deleted down to
`keep` only after a run succeeds. A schedule that has been failing for a week
still leaves you the last seven good backups, not seven failures and nothing
to restore.

Building this turned up an older bug. The scheduler accepted only six-field
cron expressions, while the docs showed five. A rule like `0 3 * * *` was
stored, listed, and never run. Both forms run now, and an expression that does
not parse is refused when you save the rule, not discovered at 3 a.m.

## Admitting what a feature cannot do

Some people want vector search with no model at all: CI pipelines,
air-gapped installs, demos. `MDDB_EMBEDDING_PROVIDER=lexical` gives them that.
It hashes words and their character n-grams into a vector, the same vector on
every machine, with no network.

It matches spelling, not meaning. It will find `replication` when you type
`replicaton`. It will not find `copy` when you ask for `backup`.

We measured it on MDDB's own documentation:

| Search | nDCG@10 |
|---|---|
| keyword (bm25) | 0.719 |
| hybrid, lexical vectors | 0.726 |
| hybrid, `nomic-embed-text` | 0.782 |

The difference between the first two is noise. So the server says so:
`/config` and the search advisor report `semantic: false`, the panel shows
"Matches by meaning: No", and the advisor recommends plain keyword search for
such a collection. The provider is never chosen automatically. If you want
it, you name it.

## Search that explains itself

Picking a vector index used to be your job: flat, HNSW, IVF, or one of the
quantized ones. `algorithm: "auto"` hands it to a query planner. The planner
looks at:

- how many vectors there are,
- how much memory they take,
- which indexes are already built,
- how selective the filter is.

A filter that admits under 5% of documents is answered by an exact scan of
those documents. That is both faster and more accurate than asking an index
to throw away 95% of what it finds. The planner never builds an index while
you wait. `explain: true` returns its choice and its reasons.

Two more ways to steer a search arrived with it. **Relevance feedback** takes
documents you marked as right or wrong and moves the query towards the first
and away from the second.

**Summaries** let a client store a short summary of a document in
`meta.summary`. MDDB embeds and indexes the summary separately, so "what is
this document about" can find a document that no single chunk answers. The
server never writes summaries itself. There is no language model inside MDDB,
and this release does not add one.

## The quiet fixes

Several fixes in this release return correct-looking results while being wrong:

- **Key case.** A document written as `README.md` could not be found as
  `readme.md`, although its identifier was case-insensitive. The key index
  now follows the same rule. Every database file records a schema version,
  and older files are migrated on first open, in crash-safe chunks.
- **Encrypted TTL.** Setting a TTL on a document in an encrypted collection
  rewrote it unencrypted, and over gRPC the cache kept serving the old copy.
  Three copies of that code are now one.
- **Metadata search.** A metadata-only update left full-text search on the
  old values.
- **Two-byte model names.** A vector stored under a model name exactly two
  bytes long was misread as quantized.
- **Restore race.** Restoring a database in the per-collection layout raced
  the vector index reload.

Each one now has a test that fails without the fix.

## Upgrading

Upgrade followers before the leader. A follower stores keys in the new form
whatever the leader sends; a 2.18.8 follower would not.

To go back to 2.18.8, restore a backup taken before the upgrade.
Everything else is opt-in and off by default:

- destructive-tool confirmation,
- quotas,
- scheduled backups,
- the lexical provider,
- the `auto` planner,
- summaries.

A server that sets none of them behaves as it did yesterday, only with fewer
bugs.
