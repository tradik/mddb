---
title: "The Duplicate Pages Your Crawler Cannot See"
slug: "blog/duplicate-pages-your-crawler-cannot-see"
status: publish
type: post
date: 2026-09-08
tags: [search, retrieval, embeddings]
excerpt: "A site audit finds copied pages by comparing words. The pages that actually compete with each other share an intent and almost no vocabulary, and word comparison is blind to them."
description: "Three duplicate detectors — content hash, text overlap and embeddings — on one client site, with the thresholds measured rather than guessed."
---

Every site audit tool will tell you about duplicate content. What it means is
pages whose *words* overlap: the same paragraph on two URLs, a location page
templated across forty towns, a product description pasted from the
manufacturer.

That is one kind of duplicate. It is not the kind that costs a client money.

The expensive kind is two pages written months apart, by different people,
that answer the same question in completely different words. Nothing about
their text overlaps. They compete with each other in the index, split their
own links, and no word-comparison tool will ever put them side by side.

MDDB ships three detectors, and the interesting part is where they disagree.

## A client site, nine pages

Here is a corpus small enough to check by hand. A running shop's blog:

| Page | |
|---|---|
| `how-to-choose-running-shoes` | buying guide: gait, cushioning, drop, sizing |
| `picking-the-right-trainers` | **the same advice, rewritten from scratch** |
| `running-shoes-guide` | a longer guide |
| `running-shoes-guide-copy` | **byte-identical to the one above** |
| `store-krakow` | location page |
| `store-warsaw` | **the same location page, city name swapped** |
| `clean-running-shoes` | care guide |
| `marathon-training-plan` | training |
| `trail-vs-road` | comparison |

Three planted problems, each a different shape. Load it and ask:

```bash
curl -s -X POST localhost:11023/v1/find-duplicates \
  -H 'Content-Type: application/json' \
  -d '{"collection":"client-site","mode":"both","threshold":0.9}'
```

```json
{
  "totalDocuments": 9,
  "totalEmbedded": 9,
  "exactGroups": [
    {"groupId": 1, "type": "exact", "score": 1,
     "documents": [
       {"key": "running-shoes-guide-copy", "contentHash": "064fe82d79d08ae4"},
       {"key": "running-shoes-guide",      "contentHash": "064fe82d79d08ae4"}
     ]}
  ],
  "similarGroups": [
    {"groupId": 1, "score": 1,
     "documents": [{"key": "running-shoes-guide-copy"}, {"key": "running-shoes-guide"}]},
    {"groupId": 2, "score": 0.91372794,
     "documents": [{"key": "store-krakow"}, {"key": "store-warsaw"}]}
  ],
  "exactDuplicates": 2,
  "similarPairs": 2,
  "searchStats": {"durationMs": 0.71, "indexSize": 9}
}
```

Two of the three planted problems, in 0.71 ms. The identical guide is caught by
its content hash — free, exact, no embeddings involved. The two location pages
come in at **0.9137** similarity.

The rewritten buying guide is not there. It is the one that matters, and at the
default threshold it does not appear.

## The threshold is the whole story

Drop it and watch:

| Threshold | What comes back |
|---|---|
| **0.90** | the identical pair (1.0000), the two location pages (0.9137) |
| **0.85** | the above, **plus `how-to-choose-running-shoes` + `picking-the-right-trainers` at 0.8611** |
| **0.80** | everything collapses: the guide and both how-to pages become one group of four at 0.8504 — 7 pairs instead of 3 |

At 0.85 the detector names the pair a crawler cannot: two pages, no shared
sentences, one intent. At 0.80 it stops distinguishing "the same page twice"
from "two pages about running shoes", and you get a blob you have to re-read
by hand.

The useful window on this corpus is **0.85 to 0.90**. On yours it will be
somewhere else, which is the point of being able to move it: run it at three
thresholds on a client site you already know well, see where the results stop
being obvious and start being wrong, and use that number for the rest of the
estate.

## What word overlap can and cannot do

The third detector, `minhash`, compares the words themselves rather than the
meaning. Same corpus:

| Threshold | Found |
|---|---|
| 0.7 | the byte-identical pair |
| 0.6 | the identical pair **and** the two location pages |
| 0.4 | the same two. Nothing more. |

It never finds the rewritten guide. Not at 0.4, not at any threshold, because
those two pages genuinely do not share their words — that is what "rewritten"
means. Meanwhile it finds the templated location pages instantly and cheaply,
without needing a single embedding.

So the two detectors are not ranked. They see different things:

- **`minhash`** — copy-paste, templating, syndicated manufacturer text,
  a page forked per city or per environment. No embeddings needed at all.
- **`similar`** — the same intent in different words. Needs embeddings, needs
  a threshold you have calibrated, finds the thing nothing else finds.
- **`exact`** — free, instant, and worth running on every collection you own.

Run `minhash` first: it costs nothing to configure and it clears out the
mechanical duplication. What is left after that is the editorial problem.

## Does it scale to a real estate?

An all-pairs comparison is quadratic, so the honest question is where that
starts to hurt. Measured on the same instance, local Ollama embeddings
(`nomic-embed-text`, 768 dimensions):

| Collection | Documents | Mode | Time |
|---|---|---|---|
| one client blog | 300 | `exact` | 0.56 ms |
| one client blog | 300 | `similar` | 5.2 ms |
| an estate | 4,000 | `similar` | ~900 ms |
| an estate | 4,000 | `minhash` | ~750 ms |

Four thousand pages compared against each other, under a second. For agency
work that means the scan is not the constraint — reading the output is.

One caveat on those larger numbers: the 4,000-page corpus was generated, and
generated pages resemble each other far more than real ones do, so the *pair
counts* from that run say nothing about a real site. The timings are real; the
duplicate counts are an artefact of the generator, and I would rather say so
than quote a number that flatters the tool.

## Getting a client site in

For WordPress, the path already exists:

```
WordPress → wpexportjson → MDDB → find_duplicates
```

The [WordPress Website Analyzer](https://mddb.tradik.com/docs/uses-wordpress-analyzer/)
guide covers the export end to end.

**Check your embeddings before you trust the `similar` results.** A large
import can outrun the embedding queue, and today the ingest API will tell you
`"failed": 0` while a portion of the collection has no vectors at all
([issue #232](https://github.com/tradik/mddb/issues/232) — a bulk load of 4,000
documents left 1,002 embedded and reported success). One request tells you
where you stand:

```bash
curl -s localhost:11023/v1/vector-stats | jq '.collections'
# {"client-site": {"embedded_documents": 9, "total_documents": 9}}
```

If those two numbers disagree, `POST /v1/vector-reindex` with
`{"force": true}` fills the gaps. A `similar` scan over a half-embedded
collection is not wrong so much as quiet — it compares the documents it has.

## Asking it from an agent

`find_duplicates` is also one of the 81 tools on MDDB's built-in MCP server, so
the whole thing is available to Claude, Cursor or your own agent without an
integration to write:

> Scan the `client-site` collection for duplicates at 0.85 and group them by
> what kind of problem each one is.

Since 2.14 the MCP server speaks the stateless `2026-07-28` revision alongside
`2025-11-25`, so an agent can ask this without a handshake and without a
session — useful when the thing asking is a scheduled job rather than a chat
window.

## What this does not do

It does not tell you which page to keep. That is the part of the job that is
still yours: consolidate, redirect, canonicalise or leave alone, depending on
links, traffic and what the client actually sells.

What it does is turn "we think there is some duplication on this site" into a
list of page pairs with a number against each one — in under a second, for a
site large enough that nobody was going to read it by hand.
