---
title: "What's New in MDDB 2.17"
slug: "blog/whats-new-in-mddb-2-17"
status: publish
type: post
date: 2026-10-22
tags: [release, search, performance]
excerpt: "MDDB 2.17 makes metadata filters search every match instead of the top few, counts facets over the whole result set, and stops the embedding worker committing three times per document."
description: "MDDB 2.17: filterMeta, rangeMeta and boost act on every match, facets over the whole result set, and one commit per embedded document."
---

MDDB 2.17 comes from two reports filed while running a real search site on it:
a ports index of 105,000 documents with filters by system, repository,
category and maintainer.

The full list is in the
[changelog](https://github.com/tradik/mddb/blob/v2.17.0/CHANGELOG.md). This
article covers what changes for you.

## Filters now see every match

If you filtered a full-text search, you may have been getting fewer results
than exist.

`/v1/fts` asked the index for the best `limit` documents of the *unfiltered*
ranking, then applied `filterMeta` to those. A filter could only keep what
happened to rank in the global top `limit`. With 100 documents matching both
query and filter and `limit: 50`, the response held 7.

The index now returns every text match when a filter is set, the filter runs
over all of them, and the limit is applied last. The same request returns 50.
`rangeMeta` and `boost` had the same order and get the same fix, and so does
the text side of `/v1/hybrid-search`.

Nothing to change on your side, and no cost to speak of: the index was already
scoring every match before truncating its sorted output. Only where the
truncation happens moved.

A side effect worth knowing: a `boost` can now lift a document from below the
limit to the top, which is what boosting was for.

## Facets over the whole result set

`facetBy` counts values over the documents a search returns, so the counts
depend on `limit`. A results page showing 50 ports could only say "among
these 50". A filter sidebar wants the number per category across everything
that matched.

That is now one parameter:

```json
{
  "collection": "ports",
  "query": "library",
  "limit": 50,
  "filterMeta": {"system": ["freebsd"]},
  "facetBy": ["category"],
  "facetScope": "all"
}
```

```json
{
  "results": [ "…the 50 best…" ],
  "total": 50,
  "matched": 1525,
  "facets": {"category": [{"value": "devel", "count": 412}]}
}
```

`facetScope: "all"` counts over every document that matched the query and its
filters, before pagination, and `matched` is the number for an "N found" line.
`total` keeps its meaning — the number of results returned — so existing
clients see no change.

The counts come from the metadata index rather than from loading each
document. Over 20,000 documents with nine metadata fields, counting two facets
across 19,823 matches takes 11 ms.

This is on the REST `/v1/fts` endpoint. Hybrid search has no `all` scope: its
vector half ranks every document, so "every match" would be the collection.

## Bulk loads on spinning disks

On SSDs nobody noticed. On a ZFS mirror of 7,200 rpm disks, a bulk load ran
at 125 documents a second, mostly waiting in `fsync`.

`add-batch` itself was fine: two commits per request, whatever the batch
size. The commits came from the embedding worker, which after storing each
document's vectors opened two more write transactions to remove stale chunks
— almost always finding none, and paying an fsync for each empty commit
anyway. A 500-document batch with embeddings cost 1,500 background commits.
It now costs 500.

To see this without dtrace, there is a new Prometheus counter:

```promql
rate(mddb_db_write_transactions_total[1m])
```

Divide it by the documents you load to get commits per document.
[DEPLOYMENT.md](https://github.com/tradik/mddb/blob/v2.17.0/docs/DEPLOYMENT.md)
has a new section on spinning disks: what each kind of write commits, and
when `sync=disabled` on the dataset is a reasonable trade — an index you can
rebuild from its sources — and when it is not.

## Also in this release

2.16.1, released earlier the same day, moved the build to Go 1.27.3 for
twelve standard-library vulnerabilities in the HTTP and TLS paths. 2.17.0
includes it.

## Upgrading

Replace the binary or image and restart. There are no schema changes, no new
configuration, and no change to existing request or response fields.
