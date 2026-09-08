---
title: "What's New in MDDB 2.14"
slug: "blog/whats-new-in-mddb-2-14"
status: publish
type: post
date: 2026-09-08
tags: [release, mcp, rag, retrieval]
excerpt: "MDDB 2.14 speaks the stateless MCP revision without dropping the old one, and fixes three ways retrieval was quietly returning the wrong document first."
description: "MDDB 2.14: the stateless MCP 2026-07-28 revision served beside 2025-11-25, asymmetric embeddings, retrievalMode honoured, and readable MCP resources."
---

MDDB 2.14 is about two things: catching up with a protocol revision that removes
more than it adds, and fixing three separate ways retrieval was returning the
right answer in second place.

The full list is in the
[changelog](https://github.com/tradik/mddb/blob/2.14.0/CHANGELOG.md). This
article covers what changes for you.

## MCP 2026-07-28, without losing 2025-11-25

The new MCP revision does not add fields. It removes the handshake.

There is no `initialize`, no session, no `Mcp-Session-Id`, no standalone SSE
stream, no `ping` and no `logging/setLevel`. Instead every request carries its
own protocol version, client identity, capabilities and log level in `_meta`,
and every result carries its type, the server's identity, and how long the
client may cache it.

MDDB now serves both revisions, and **which one serves a request is decided by
the request** — not by the connection:

```json
{
  "jsonrpc": "2.0", "id": 1, "method": "tools/list",
  "params": {"_meta": {
    "io.modelcontextprotocol/protocolVersion": "2026-07-28",
    "io.modelcontextprotocol/clientCapabilities": {}
  }}
}
```

That request is served statelessly. The same request without `_meta` is served
exactly as it was in 2.13. A stateless client and a handshake client can share a
port, a connection, or one stdio process, and there is a test that proves the
older path is untouched rather than an assumption that it is.

**If your client speaks 2025-11-25, you do not have to do anything.** That is
the point of serving both: clients and servers do not upgrade on the same day.

New for stateless clients: `server/discover` (the one RPC the revision requires
of every server), `resultType` and `serverInfo` on every result, `ttlMs` and
`cacheScope` on the six list-and-read operations, `subscriptions/listen` in
place of the GET stream, per-request log levels, and the `Mcp-Method` /
`Mcp-Name` headers checked against the body — a proxy routing on a header while
the server acts on the body must not be able to authorize one call and perform
another.

To see which revisions a server answers for:

```bash
curl -s localhost:8080/v1/config | jq .protocols.mcp
```

```json
{
  "revision": "2026-07-28",
  "supportedRevisions": ["2026-07-28", "2025-11-25"],
  "revisionPinned": false,
  "handshakeRevision": "2025-11-25"
}
```

`handshakeRevision` is new, and it exists because since this revision there is
no single revision "in force": two clients on the same port can be in different
eras, and whoever is debugging one of them needs to know which.

Both revisions are documented field by field in
[Protocol Revisions](https://mddb.tradik.com/docs/mcp-revisions/).

## Your query and your documents were embedded the same way

Retrieval models are trained asymmetrically. The same sentence should produce a
different vector depending on whether it is a document in the corpus or the
question being asked of it. MDDB had one code path for both, and no way for a
provider to tell them apart.

Three of the four providers were affected. Ollama sent raw text — while
autodetection *prefers* exactly the models that need a task prefix. Cohere
hardcoded `input_type: "search_document"`, so questions were embedded as though
they were corpus entries. Voyage never sent `input_type` at all. OpenAI is
unaffected; its models are symmetric.

Measured on a six-document corpus, asking "my API key stopped working", where
the right answer is the document about rotating credentials:

| | Position of the correct answer |
|---|---|
| Before | **3rd** (0.5296) |
| After | **1st** (0.5649) |

At `topK: 1` — which is how an agent asks when it wants one answer — that is the
difference between a hit and a miss.

**Existing collections keep working and do not benefit until reindexed.** That
was measured, not assumed: documents embedded by the old code and queried by the
new one land in the same place they did before. The gain comes from re-embedding
the corpus.

Which brings us to the next one.

## MDDB now tells you what needs reindexing

Changing a collection's embedding model used to be silent. The vectors already
stored were produced by the old model, the new queries by the new one, and
nothing in the system knew the two no longer belonged in the same space.

Every collection now records which embedding produced it — provider, model and
dimensions — and MDDB names the collections that no longer match at startup and
on demand. Recording it costs a map lookup per write, not a transaction.

Reindexing remains your decision. MDDB names the collections and stops there,
rather than rewriting stored data at startup because a config line changed.

## `retrievalMode` on a collection was ignored

The documented precedence is: an explicit request parameter wins, then the
collection profile, then the default. For `topK` that held. For `retrievalMode`
it did not — both search paths read the request field directly.

So setting `{"retrieval":{"retrievalMode":"chunk"}}` on a collection returned
`{"status":"ok"}`, persisted, read back correctly, and changed nothing. Every
search kept returning whole documents. Passing the same value in the request
worked, which is the shape of bug that stays open for months: the feature works
in the way people test it and not in the way people configure it.

## Every MCP resource was advertised at an address that could not be read

This one surfaced while testing the protocol work, and it had been true for a
long time.

`resources/list` named four resources. `resources/read` refused all four.
`url.Parse` puts the first segment after `//` in a URI's *host*, not its path,
and the reader only ever looked at the path — so `mddb://health` arrived with an
empty path, and `mddb://docs/quickstart` as a single segment that failed the
collection/key split. Only the three-slash spelling `mddb:///health`, which
nothing advertised anywhere, ever worked.

Two of those four were never resources at all: `mddb://{collection}/{key}` and
`mddb-search://{collection}` are patterns, and the braces are not even valid in
a host. They now live in `resources/templates/list`, which both revisions define
for exactly this and which MDDB had left unimplemented.

If you have never used MCP resources against MDDB, this is why.

## Upgrading

```bash
docker pull tradik/mddb:2.14.0
```

Nothing in this release requires a configuration change, and no data migration
runs on startup.

Two things worth doing afterwards, in order:

1. **Check what needs reindexing.** MDDB will name any collection whose stored
   vectors were produced by a different embedding than the one configured now.
2. **Reindex those collections** if you use Ollama, Cohere or Voyage — that is
   what turns the asymmetric-embedding fix into better results rather than a
   fix that only applies to documents you add from now on.

If you pin a protocol revision with `MDDB_MCP_PROTOCOL_VERSION`, note that it
now narrows what `server/discover` advertises as well as what the server
accepts. Advertising a revision the server would refuse just sends clients
straight into the refusal.
