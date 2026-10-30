---
title: "Who Is Asking?"
slug: "blog/who-is-asking"
status: publish
type: post
date: 2026-10-30
tags: [release, security, operations, search, interoperability]
excerpt: "A database should know who is talking to it, how much it is willing to hand over, and what it is willing to say about itself. MDDB 2.18.10 got all three wrong in places, and this release is mostly about getting them right."
description: "MDDB 2.18.10 fixes who a request comes from, what a credential may touch and how much one search may return, and adds an Elasticsearch-compatible subset, corpus packs and a diagnose bundle."
---

Every request to a database carries three questions:

- Who is asking?
- What are they allowed to touch?
- How much should they get?

For most of MDDB's history the answers came from whichever handler happened
to receive the request. This release moves them to one place each. It also
found, on the way, that some handlers never asked at all.

The full list is in the
[changelog](https://github.com/tradik/mddb/blob/v2.18.10/CHANGELOG.md).

## Who is asking

MDDB rate-limits clients by IP address and records failed logins under it.
Until this release it took that address from the first entry of
`X-Forwarded-For`, from anyone. A client that sent a different header on each
request was never limited. A burst of failed logins landed under addresses the
attacker chose, so the detector that counts them never fired.

Behind a proxy it was no better. nginx appends the real client to whatever
`X-Forwarded-For` the client sent, so the first entry is still the client's
own text.

Now forwarding headers count only from proxies listed in
`MDDB_TRUSTED_PROXIES`, and the chain is read from the right, skipping those
proxies. The default list is empty: MDDB trusts nobody until told otherwise.

Not every proxy speaks `X-Forwarded-For`:

| In front of MDDB | Sends the client as |
|---|---|
| Cloudflare and cloudflared | `CF-Connecting-IP` |
| Ivanti (Brocade) vTM | `X-Cluster-Client-Ip` |
| NetScaler | a header the admin names |
| HAProxy in TCP mode, AWS NLB, F5, nginx `stream` | no header; PROXY protocol |

So `MDDB_CLIENT_IP_HEADER` picks the header, `Forwarded` (RFC 7239) is
understood, and `MDDB_PROXY_PROTOCOL=on` reads PROXY v1/v2 on the HTTP, gRPC
and MCP listeners. The configuration docs carry one row per product, each
checked against the vendor's own documentation. Where a vendor's behaviour
could not be confirmed, the row says so.

## What may they touch

MDDB has per-collection permissions and tenants. A key can be limited to one
collection, and a tenant user sees only its own namespace. One function,
`CheckPermission`, enforces both. The trouble was that each handler had to
remember to call it.

While building the Elasticsearch subset (below) we noticed that `/v1/fts`
never did. So we checked every entry point:

| Surface | What did not check |
|---|---|
| REST | about thirty-five routes, among them `vector-search`, `hybrid-search`, `add-batch`, `ingest`, `schema/*`, `webhooks` and `memory/*` |
| MCP | every tool and every resource |
| gRPC | synonyms, stopwords and `GeoStats` |
| GraphQL | `schema`, `schemas`, `stats` and `vectorStats` |

With authentication on, a key limited to one collection could read, and in
places write, any other. A tenant could reach another tenant's documents.

There is now one rule per entry point, in a table, applied before the
handler runs. A request needs the permission on every collection it names,
including each target of a cross-collection search. An entry point with no
rule is refused. The tests list every route, RPC, MCP tool and GraphQL field
the server has and fail if one is missing from the table, so a new endpoint
cannot quietly skip the check again.

The review found one more thing. Admin rights on a single collection set the
global admin flag in the user's token, and that flag passes every check. An
admin of `docs` was an admin of the whole database. Now only admin on `"*"`
is global.

If you run with authentication enabled, read the upgrade notes. Tenant users
now get `403` on listings that are not filtered by tenant, and login tokens
issued before the upgrade keep their old admin flag until they expire.

## How much should they get

A public search page asks for 50 results. Nothing stopped a script from
asking for 100,000: one request to `/v1/fts` on daemonports.org returned
21 MB in 1.4 seconds. A rate limit counts requests, not bytes.

`MDDB_SEARCH_MAX_LIMIT` (1000 by default) now caps every search on every
surface, through the one function that already resolved the result count.
A collection can lower the cap but never raise it, because the server value
is the operator's limit and anyone with config access to one collection
should not be able to lift it. A capped response says `limitCapped: true`, so
a client knows why it got fewer results than it asked for.

Building the cap turned up a smaller version of the same problem. Facet
values were sorted with an insertion sort. On a field with 200,000 distinct
values that took eleven seconds.

## Writing less

The same site reported that adding one metadata key to each of its 111,000
documents would take three to four days on spinning disks. Every update,
even one that changed only metadata, deleted and rewrote every full-text
posting of the document.

The index now keeps a signature of each document:

- A resent document writes nothing.
- A metadata change rewrites only the fields that changed.
- New content still re-indexes the whole document.

Adding one key to a document of about 300 words used to rewrite 306 keys
across 576 pages. Now it writes 5 keys and 15 pages.

A shortcut like this is only safe if it gives the same answer as rebuilding
from scratch. A property test applies thousands of random edits and, after
each one, compares the index byte for byte with a fresh rebuild, including
the statistics BM25 scores with.

That work also exposed three paths that left the index lying:

- Batch updates over gRPC and MCP never re-indexed full text at all.
- They also never queued new embeddings, so vectors kept describing the old
  text.
- Emptying a document's content left it findable by the words it no longer
  had.

All three are fixed, and one test now sends the same update through fourteen
write paths and checks that each has the same effects.

## Ready sooner

After a restart, a collection of 1,000 vectors waited 1 minute 39 seconds
before answering anything. The server trained its IVF and product-quantisation
indexes first and only then marked the collection searchable, so flat and
HNSW search, which need no training, waited too.

Training now runs in the background. The collection answers after 0.12
seconds. `algorithm: "auto"` skips an index that is still training and says
so in `explain`.

## Speaking other dialects

- **Elasticsearch.** Some tools only know how to talk to Elasticsearch.
  `MDDB_ES_COMPAT=true` mounts a subset of its API under `/es`: `_search`,
  `_doc`, `_bulk` and `_count`, with `match`, `bool`, `term`, `range`, `knn`,
  highlights and `terms` aggregations. Each clause is translated onto MDDB's
  own full-text, vector and hybrid search. There is no second engine. Anything
  outside the subset is refused with its name, never ignored. The official
  `elasticsearch-py` client runs unmodified against it in CI. `GET /es` says
  plainly that this is a subset, not Elasticsearch.
- **Corpus packs.** `mddb-cli corpus build` packs a collection, with its
  full-text index and vectors, into one file with a licence in its manifest.
  `corpus install` mounts it read-only on another server, without
  re-embedding. If the other server uses a different embedding model, the pack
  still installs, answers from full text, and says why.
- **Synced sources.** A scheduled rule can keep a collection in step with a
  sitemap, a list of URLs or an S3 prefix. It fetches only what changed, and
  deletes a document only when its source is gone and the listing was
  complete. An S3 source is only ever read: a mistyped bucket fails the run
  instead of creating an empty bucket.

## Asking for help without handing over data

When something goes wrong on someone else's server, we used to ask for half a
dozen endpoints, one at a time. `mddb-cli diagnose` collects them into one
bundle: build, configuration, statistics, metrics, recent error classes. It
leaves out document content, metadata values, query text, keys, addresses and
paths. Collection names are hashed unless you ask otherwise.

A test plants unique strings in every place user data could hide. It then
checks that none of them reaches any file in the bundle. With each
redaction removed in turn, all eleven variants of that test fail.

The bundle also carries counts of how queries are shaped. Two planned
features, graph queries and community detection, have been waiting for
evidence that anyone needs them, and these counts are how that evidence can
arrive without anyone sending us their questions.

## Upgrading

Without authentication, almost nothing changes. Two defaults are new:

- Searches return at most 1,000 results; raise `MDDB_SEARCH_MAX_LIMIT` if you
  need more in one call.
- Behind a proxy, set `MDDB_TRUSTED_PROXIES`. Until you do, the rate limiter
  and the audit log see every client as the proxy.

With authentication, permissions are now enforced where they were not, and
some clients that worked by accident will get `403`. The upgrade section of
the changelog lists every case.
