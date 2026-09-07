---
title: "Why Your RAG Returns the Wrong Document"
slug: "blog/why-your-rag-returns-the-wrong-document"
status: publish
type: post
date: 2026-08-29
tags: [rag, retrieval, embeddings, search]
excerpt: "A ten-document corpus, one ordinary question, and a semantic search that ranks the right answer nowhere. What actually fixed it was not a parameter."
description: "A measured walk through RAG retrieval in MDDB: where semantic search fails, why no blend weight rescues it, and the one change that fixed it."
---

Retrieval-augmented generation is usually described as a pipeline: embed the
documents, embed the question, take the nearest neighbours, hand them to a
model. Every step is true and the description is still misleading, because it
suggests the hard part is wiring. It is not. The hard part is that the nearest
neighbours are often the wrong documents, and nothing in the pipeline tells you
so.

This post is one question against one small corpus, measured at every step on a
running server. The numbers below are all from that session.

## The corpus and the question

Ten documents, an internal engineering handbook: credential rotation, on-call
escalation, deploy rollback, diagnosing a slow endpoint, data retention, access
for a new starter, incident reviews, cloud spend, release cadence, restoring
from a backup. Each is a few hundred words of ordinary prose.

Add them and MDDB embeds them for you. There is no separate indexing step:

```bash
curl -X POST "$MDDB/v1/add" -H 'Content-Type: application/json' -d '{
  "collection": "handbook",
  "key": "credential-rotation",
  "lang": "en_GB",
  "contentMd": "# Rotating service credentials\n\nEvery service credential expires..."
}'
```

```bash
curl -s "$MDDB/v1/vector-stats"
```

```json
{
  "collections": {
    "handbook": {
      "embedded_documents": 10,
      "quantization": "float32",
      "total_chunks": 10,
      "total_documents": 10
    }
  },
  "dimensions": 768,
  "enabled": true,
  "index_ready": true,
  "model": "nomic-embed-text",
  "provider": "nomic-embed-text"
}
```

Now the question, phrased the way somebody actually types it:

> my login stopped working overnight and I did not change anything

The document that answers it is `credential-rotation`. It ends with the
sentence: *"If a service starts failing with 401 and nobody changed the code, an
expired credential is the first thing to check."*

## Semantic search gets it wrong

```bash
curl -X POST "$MDDB/v1/vector-search" -H 'Content-Type: application/json' \
  -d '{"collection":"handbook","query":"my login stopped working overnight and I did not change anything","topK":5}'
```

| Rank | Document | Score |
|---|---|---|
| 1 | release-cadence | 0.6556 |
| 2 | deploy-rollback | 0.5876 |
| 3 | new-starter-access | 0.5865 |
| 4 | data-retention | 0.5764 |
| 5 | cost-controls | 0.5683 |

The right answer is not in the top five. The top result is a document about
which mornings releases go out.

It is worth being precise about why, because "the embedding is bad" is the
wrong conclusion. `release-cadence` contains "Nothing ships on a Friday unless
it fixes something that is currently broken" and "read it at three in the
morning". Against a question about something being broken overnight, that is
not a stupid answer. It is a plausible neighbour. The model is doing what it was
asked; nobody asked it the right thing.

The scores make the same point from another angle. First place to fifth spans
0.6556 to 0.5683 — under nine hundredths across five documents that have
nothing to do with each other. A similarity threshold cannot separate these,
because there is nothing to separate.

## Keyword search gets it wrong differently

```bash
curl -X POST "$MDDB/v1/fts" -H 'Content-Type: application/json' \
  -d '{"collection":"handbook","query":"my login stopped working overnight and I did not change anything","limit":5}'
```

| Rank | Document | Score |
|---|---|---|
| 1 | incident-writeups | 0.1898 |
| 2 | deploy-rollback | 0.0949 |
| 3 | new-starter-access | 0.0949 |
| 4 | credential-rotation | 0.0949 |
| 5 | cost-controls | 0.0949 |

Better in one narrow sense — the right document is on the list — and worse in
every other. Four documents tie exactly. The winner won on the stems `work` and
`chang`, from "stopped working" and "did not change", which appear in the
question by accident of English rather than by meaning.

Neither method knows the answer. They disagree about which wrong document to
show you.

## Chunking helps, and you can see exactly how much

The first correctable mistake is that each document was a single vector. The
default chunk size is 1500 characters, and these documents fit inside one, so
the sentence about 401 errors was averaged together with the paragraph about
24-hour overlap windows and the paragraph about expiry emails.

Drop the chunk size and reindex:

```bash
MDDB_EMBEDDING_CHUNK_SIZE=300
```

```bash
curl -X POST "$MDDB/v1/vector-reindex" -H 'Content-Type: application/json' \
  -d '{"collection":"handbook","force":true}'
```

```json
{"embedded":10,"errors":null,"failed":0,"skipped":0,"totalChunks":20}
```

Ten documents, twenty chunks. The same query:

| Rank | Document | Score | Before |
|---|---|---|---|
| 1 | release-cadence | 0.6556 | 0.6556 |
| 2 | **credential-rotation** | 0.6062 | *not in top five* |
| 3 | deploy-rollback | 0.6029 | 0.5876 |
| 4 | cost-controls | 0.5870 | 0.5683 |
| 5 | new-starter-access | 0.5718 | 0.5865 |

The right document moved from outside the top five to second place.

Look at the first row. `release-cadence` scores 0.6556 before and after, to four
decimals, because it is short enough to be one chunk either way — its vector was
not recomputed into anything different. That is the whole mechanism visible in
one number: chunking changes the documents that split, and leaves the others
exactly where they were. Anything that claims to improve retrieval should be
able to show you which rows it did not touch.

A second query behaves the same way. For "my API key stopped working",
`credential-rotation` goes from 0.5242 to 0.5453 while `release-cadence` stays
at 0.5552 — still first, still unchanged.

Second place is progress and it is not an answer. At `topK: 1`, which is how an
agent asks when it wants one answer, second place is a miss.

## No blend weight rescues it

The obvious next move is hybrid search: blend the keyword score and the vector
score, and hope the document both methods half-liked rises. MDDB fuses them
either by weighted blend or by reciprocal rank fusion, and returns both inputs
alongside the fused score so you can see what happened.

```bash
curl -X POST "$MDDB/v1/hybrid-search" -H 'Content-Type: application/json' \
  -d '{"collection":"handbook","query":"my login stopped working overnight and I did not change anything","topK":3,"alpha":0.5}'
```

`alpha` is the weight: 0 is keyword only, 1 is vector only. Sweeping it:

| alpha | 1st | 2nd | 3rd |
|---|---|---|---|
| 0.0 | incident-writeups | cost-controls | deploy-rollback |
| 0.3 | incident-writeups | cost-controls | deploy-rollback |
| 0.5 | incident-writeups | cost-controls | deploy-rollback |
| 0.7 | incident-writeups | cost-controls | deploy-rollback |
| 1.0 | release-cadence | credential-rotation | deploy-rollback |

`credential-rotation` appears once, at the extreme where the keyword half is
switched off entirely — which is just the vector search from two sections ago.

The response says why. At alpha 0.5:

```json
{
  "document": {"key": "incident-writeups"},
  "combinedScore": 0.7616,
  "ftsScore": 1,
  "vectorScore": 0.5461,
  "matchedTerms": ["work", "chang"]
}
```

A perfect keyword score, 1.000, earned by two stems that mean nothing here.
Fusion cannot repair an input that is confidently wrong; it can only average it
with something else. There is no value of alpha that fixes this query, and an
afternoon spent sweeping it would have produced five plausible-looking result
lists and no working search.

This is the part worth taking away. When retrieval is wrong, the instinct is to
reach for a parameter. Parameters trade one failure for another. What was
missing here was not a weight.

## What actually fixed it

The question says *login*. Every document that could answer it says *credential*
and *401*. The corpus and the user do not share a word, and no amount of
weighting invents one.

MDDB keeps per-collection synonyms for exactly this:

```bash
curl -X POST "$MDDB/v1/synonyms" -H 'Content-Type: application/json' \
  -d '{"collection":"handbook","term":"login","synonyms":["credential","secret","authentication"]}'
```

Same query, same alpha, nothing else changed:

| Rank | Document | combined | fts | vector | matched |
|---|---|---|---|---|---|
| 1 | **credential-rotation** | **0.8031** | 1.000 | 0.606 | chang, secret, credenti |
| 2 | incident-writeups | 0.4936 | 0.464 | 0.523 | chang, work |
| 3 | cost-controls | 0.3557 | 0.124 | 0.587 | overnight |

First place, and by a distance: 0.8031 against 0.4936. The document that could
not be found by two methods and five blend weights is now the unambiguous
answer, because one line told the system that a person saying *login* may be
asking about a *credential*.

Notice what did not change: the vector score for `credential-rotation` is 0.606,
the same as before. The semantic half never improved. The synonym gave the
keyword half something true to find, and fusion did the rest — which is what
fusion is for, and what it could not do while both halves were guessing.

## Give the agent the passage, not the document

Ranking is half the job. An agent assembling a prompt does not want the whole
document; it wants the passage that matched, so the context window carries
answers rather than surrounding prose.

Set it once, on the collection:

```bash
curl -X PUT "$MDDB/v1/collection-config" -H 'Content-Type: application/json' -d '{
  "collection": "handbook",
  "retrieval": {"defaultSearchType": "hybrid", "topK": 5, "retrievalMode": "chunk"}
}'
```

Results then carry the passage and its index:

```json
{
  "document": {"key": "credential-rotation"},
  "score": 0.6062,
  "chunkIndex": 1,
  "chunkText": "To issue a replacement, open the service page, choose Issue new secret, and copy the value shown once..."
}
```

Writing this post is what found the bug that made that example possible.
`retrievalMode` in a collection profile was accepted, validated, stored, and
read back correctly by `GET /v1/collection-config` — and then ignored by both
search paths, which kept returning whole documents. It is fixed in 2.14.0
([#216](https://github.com/tradik/mddb/issues/216)). Passing the parameter on
each request always worked; the profile did not, which is precisely the form of
failure that is hard to notice, because everything reports success.

## Know when your vectors have gone stale

Two vectors are only comparable if they were produced the same way, and a model
name does not tell you that. A provider can begin sending a task prefix, or an
input type, while reporting the same model — as MDDB's own providers did in
2.14.0.

So each collection now records the model and the embedding variant its vectors
were made with, and MDDB compares that at startup:

```
level=WARN msg="embedding provenance changed" detail="1 collection(s) were
embedded differently than mxbai-embed-large produces now and should be
reindexed for search quality: handbook"
```

Nothing is rewritten. Reindexing costs provider calls and is your decision. What
changes is that the decision is now informed by a fact rather than by a hunch,
and a collection embedded before this record existed is reported rather than
skipped.

## What this adds up to

Every step above is measurable, and that is the actual method:

- **Ask real questions.** The failure only appeared because the question was
  phrased the way a person types it, not the way the document is written.
- **Read the inputs, not the ranking.** `ftsScore` and `vectorScore` next to the
  fused score turned "hybrid did not help" into "the keyword half is confidently
  wrong", which is a different problem with a different fix.
- **Prefer changes you can see the shape of.** Chunking left an unsplit
  document's score identical to four decimals. A change that alters everything
  by a little is a change you cannot reason about.
- **Reach for vocabulary before parameters.** One synonym beat five blend
  weights, because the gap was lexical and no weight closes a lexical gap.
- **Check what your configuration is actually doing.** A stored setting that
  returns `{"status":"ok"}` and changes nothing looks exactly like a stored
  setting that works.

MDDB gives you the pieces — automatic embedding on write, configurable
chunking, keyword and vector search with their scores exposed, fusion by blend
or by rank, per-collection synonyms, retrieval profiles, passage-level results,
and a warning when your vectors no longer match your provider. It does not give
you a correct retrieval pipeline, because nobody can: that depends on your
corpus and on how your users ask. What it can do is let you find out, in an
afternoon, which of your assumptions was wrong.

Ours was that hybrid search would fix it.
