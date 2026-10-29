---
title: "What's New in MDDB 2.18.8"
slug: "blog/whats-new-in-mddb-2-18-8"
status: publish
type: post
date: 2026-10-29
tags: [release, agents, search, mcp, retrieval]
excerpt: "MDDB 2.18.8 is about agents that work on code: passages instead of files, a repository indexed in one command, reference code to learn from, edits that cannot overwrite each other, and numbers for what search is worth."
description: "MDDB 2.18.8: passages instead of files, a repository indexed in one command, safe concurrent edits, aliases, and measured search quality."
---

MDDB 2.18.8 is for agents that work on code. An agent that edits a project
spends most of its context finding things. It greps, opens a file, opens
another, and reads hundreds of lines to change three. This release makes
finding cheaper and easier to measure. It also stops two agents that share a
document from losing each other's edits.

The full list is in the
[changelog](https://github.com/tradik/mddb/blob/2.18.8/CHANGELOG.md). This
article covers what changes for you.

## A repository in one command

```bash
mddb-cli autoindex .
```

This indexes every text file under the directory into one collection. It
respects `.gitignore` and records each file's path, kind and language, and
the server extracts symbols from code. A second run sends only what changed.
On the MDDB repository, 1,455 files and 14 MB, the first run took 41 seconds
and the second took half a second.

## Passages instead of documents

Any search can now answer with `format: "passages"`. Each result is then a
place: a file path, a line range, the function or type that holds those
lines, and the lines themselves, with an estimate of their size in tokens.
An agent asking "where is the revision computed" gets the dozen lines that
do it, not the 400-line file around them.

For code collections the search advisor recommends passages. A collection
can make them its default.

## Symbols, not just text

`GET /v1/symbol` and the MCP tool `code_symbol` answer everything about one
name. They return where it is defined, with the span from start line to end
line, and every place that uses it. They also return its callers and
callees, the tests that touch it, and other definitions in the same
directory. Before an agent changes a function, one call tells it what else
will notice.

## Code to learn from

Before implementing something, it helps to see how a project that already
did it went about it:

```bash
mddb-cli reference add github.com/etcd-io/bbolt --ref v1.4.0
mddb-cli reference search "how are freed pages released"
```

The CLI makes a shallow clone into your cache directory and indexes it as a
**reference** collection. It records the repository, the commit and the
licence. MCP agents search every reference collection at once with
`reference_search`, `reference_symbol`, `reference_examples` and
`reference_dependencies`. The server never clones anything. That stays on
your machine.
[Reference coding](/docs/reference-coding/) has the details.

## Narrower searches

- **Filter operators.** `filter` accepts `prefix`, `contains`, ranges (`gt`,
  `gte`, `lt`, `lte`), `not` and `exists`, alongside exact matches. Dates and
  numbers compare as dates and numbers. "Go files under `services/`, changed
  this week" is one search.
- **Grouping.** `groupBy` returns at most k results per file, author or
  collection. A search no longer hands back seven chunks of the same long
  document.

## Is it searchable yet?

A write returns once its full text and metadata are indexed. Its embedding
is computed in the background. Until now, an agent that wrote a note and
searched for it by meaning a second later found nothing, and could not tell
why. Searches now take `waitForIndex` and `waitForKeys` to wait for named
documents. `GET /v1/indexing-status` and the MCP tool `get_indexing_status`
report the queue. The SSE event `collection.indexed` fires when a
collection's queue empties.

## Fewer tools, chosen deliberately

MDDB has 90 MCP tools, and every schema sits in the model's context on every
turn. Toolsets let a server or a single API key expose only what an agent
needs. `MDDB_MCP_TOOLSETS=search,code` cuts `tools/list` from about 19,600
tokens to about 8,900.

## Edits that cannot overwrite each other

Every document now carries a `revision`. Pass it back as `expectedRevision`,
or `expected_revision` over MCP and gRPC, and the write fails with `409`
when someone else wrote in between. The second agent then reads again
instead of silently overwriting the first. Building this showed that REST,
gRPC and MCP had three separate update implementations that had drifted
apart. MCP updates, for example, skipped the metadata index. They now share
one.

`langchain-mddb` uses this for `MddbBackend`, a filesystem backend for
LangChain Deep Agents. Files live in MDDB with revision history, they can be
searched by text and by meaning, and concurrent edits are safe.

## Numbers instead of guesses

Two features let you measure what search does instead of trusting it.

**Evaluation sets.** Store the questions people actually ask, each with the
documents that should answer it. Then compare configurations:

```bash
mddb-cli eval docs --config fts/bm25 --config hybrid --config vector
```

The output gives recall@k, nDCG@k, MRR, latency and tokens for each
configuration. With a set stored, the search advisor recommends from the
measurement instead of from the collection's shape. `--fail-below
recall@10=0.8` turns it into a CI gate. On MDDB's own documentation, with 20
questions, hybrid search ranked best: nDCG@10 0.783, against 0.739 for BM25.

**Retrieval gain.** The server now counts the tokens its searches return.
It also counts the tokens that passages and chunks save compared with the
whole documents they came from. `mddb-cli gain` reports both over a window
of days, and so do `GET /v1/gain`, the panel's System page and Prometheus.
The counters keep counts only, never queries.

We also measured an agent finding code in this repository, on 15 tasks.
Grep followed by reading the right file reached 13 answers with about
140,000 tokens. One vector search returning passages reached the same 13
with about 85,000 tokens, in half the calls. When the agent reads the files
with the most grep matches first, as agents usually do, grep needed 2.8
million tokens. Full-text passages reached only 6 of the 15. These questions
are phrased the way people ask, and the code uses other words. That is the
kind of thing an evaluation set on your own questions will show you.
[The benchmark](/docs/benchmark/) has the method and every number.

## Aliases and bulk changes

An alias is a stable name for a collection. Build `docs-v2` with a new
embedding model, then run `mddb-cli reindex-swap docs --into docs-v2`. It
copies the documents, waits until they are indexed, and moves `docs` to the
new collection in one step. The old collection stays, so a rollback is one
command.

`delete_where`, `update_where` and `set_ttl_where` act on every document a
metadata filter matches. Each document goes through the normal write path,
and a dry run shows first what would change.

## gRPC catches up

The gRPC search calls were separate copies of the search code, and they had
fallen behind REST and MCP. They now call the same code, so gRPC gets
filters, grouping, passages, read-your-writes, symbol lookup, evaluation and
gain as well.

## Fixes worth knowing about

- **Two writes in the same second no longer lose a revision**
  ([#358](https://github.com/tradik/mddb/issues/358)).
- **Raising test coverage to 97% for every file this release touched found
  several crashes.** A cancelled gRPC batch, a short memory session ID, a
  missing MCP-keys bucket, and startup with automations disabled could all
  crash the server.

## Upgrading

Nothing to migrate. All new fields are optional, and a write without
`expectedRevision` behaves as before.

One status code changes. A request for a document or revision that doesn't
exist now answers **404 instead of 400**, with the same JSON body. A client
that checks for 400 to detect a missing document has to check for 404.

Download 2.18.8 from
[GitHub releases](https://github.com/tradik/mddb/releases), or run
`mddb-cli self-update`.
