---
title: "Qdrant-Compatible Point Query"
slug: "docs/qdrant-compat"
description: "Search an MDDB collection with qdrant-client's query_points — the call Open WebUI's External Knowledge Source with the Qdrant provider makes — translated onto MDDB's vector search, with every unsupported field refused by name."
status: publish
---

# Qdrant-Compatible Point Query

> v2.19.10+ ([#447](https://github.com/tradik/mddb/issues/447)). Off by default.
> It is the `qdrantCompat` module: switch it on under *System → Features* in
> the panel or with `PUT /v1/features/qdrantCompat`, no restart.
> `MDDB_QDRANT_COMPAT=true` turns it on by default; a switch set on the
> Features page wins. Switched off, `/qdrant` answers `404`.

Open WebUI's **External Knowledge Source** form has a **Qdrant** provider. It
embeds the user's question with Open WebUI's own embedding model and calls
`qdrant-client`'s `query_points` over REST. MDDB answers that call, so an MDDB
collection can be selected there directly.

This is the few requests `query_points` makes, **mapped onto MDDB's own vector
search** — not a Qdrant server. The query runs through `/v1/vector-search`
(algorithm `auto`, chunk retrieval), so permissions, the result cap and the
search concurrency limit apply as on `/v1`. Anything that cannot be translated
faithfully is refused with a `400` that names it. There are no writes, no
collection management, no filters, no named vectors and no gRPC.

## Turning it on

```bash
MDDB_QDRANT_COMPAT=true mddbd
```

The surface lives under **`/qdrant`** on the HTTP listener, beside `/v1`, and
shares its port, TLS, CORS and middleware. `GET /v1/config` reports
`qdrantCompat: {"enabled": true, "prefix": "/qdrant"}`.

`qdrant-client` uses the path of its `url` as the prefix of every request, so
the prefix goes into the URL:

```python
from qdrant_client import QdrantClient

client = QdrantClient(url="http://mddb:11023/qdrant", api_key="mddb_...")
hits = client.query_points(collection_name="docs", query=vector, limit=5)  # vector: list[float]
```

**Always write the port.** `qdrant-client` adds port `6333` to a URL without
one: `https://mddb.example.com/qdrant` is sent to
`https://mddb.example.com:6333/qdrant`. Behind a TLS proxy on 443 write
`https://mddb.example.com:443/qdrant`.

## Open WebUI

Admin Settings → Documents → External Knowledge Source, provider **Qdrant**:

| Field | Value |
|---|---|
| Endpoint | `http://mddb:11023/qdrant` (MDDB's HTTP address, port included, plus `/qdrant`) |
| API Key / Token | an MDDB API key (`mddb_...`) with read on the collection; empty when auth is off |
| Timeout | `30` (seconds, as the client's default) |
| Collection | the MDDB collection name (an alias works) |
| Content Field | `payload.text` (the default) |
| Vector Field | empty — an MDDB collection has one unnamed vector |
| Metadata Field | `payload.metadata` (the default) |
| Document ID Field | `id` (the default) |

**Both sides must embed with the same model.** Open WebUI embeds the question
with its own embedding model; MDDB compares that vector with the vectors its
own provider (`MDDB_EMBEDDING_PROVIDER`, `MDDB_EMBEDDING_MODEL`) wrote. A vector
of another length is refused with a `400` naming both dimensions; a vector of
the same length from a different model is not detectable and returns poor
matches. `GET /v1/vector-stats` shows MDDB's model and dimensions.

## Endpoints

| Request | What it does in MDDB |
|---|---|
| `GET /qdrant`, `HEAD /qdrant` | the version probe `qdrant-client` sends at start-up: `{"title", "version": "1.20.0", "commit"}` |
| `POST /qdrant/collections/{name}/points/query` | a nearest-neighbour query on the collection's stored vectors |

`{name}` is a collection; an alias resolves to its collection. Every other
path or method is a `404` naming the method and path. Errors from the surface
use Qdrant's envelope, `{"status": {"error": "..."}, "time": ...}`; a `401` from
the shared authentication middleware is `{"error": "..."}`. `qdrant-client`
raises both as `UnexpectedResponse` with the status code.

`version` is the Qdrant API level the subset follows, not a claim to be
Qdrant (the `title` says MDDB). The client compares it with its own version
and only warns — major must match, minor within one — so it never blocks a
query.

### Request

| Field | Accepted |
|---|---|
| `query` | a dense vector (list of numbers) or `{"nearest": vector}`; required |
| `using` | absent, `null` or `""`; any name is a `400` |
| `limit` | 1 or more, default 10; capped by `MDDB_SEARCH_MAX_LIMIT` and the collection's `retrieval.maxTopK` (a capped page carries `X-Limit-Capped` and `X-Limit`) |
| `offset` | 0 or more, default 0 |
| `score_threshold` | a number; points scoring below it are dropped |
| `with_payload` | `true` (default) or `false`; selectors are a `400` |
| `with_vector` | `false` only; MDDB does not return stored vectors |
| `params` | accepted and ignored: MDDB's planner picks the index |

Any other field with a value — `filter`, `prefetch`, `lookup_from` and the
rest, and other query kinds such as `recommend`, `fusion` or a point id —
is a `400` naming it. The `consistency` and `timeout` query parameters are
ignored.

### Response

```json
{"result": {"points": [
  {"id": "36e29559-973e-8c8d-8159-8fe8e8ddc1e0", "version": 0, "score": 0.522,
   "payload": {"text": "The owl hunts mice at night in the forest",
               "metadata": {"collection": "kb", "key": "owl", "lang": "en", "chunk": 0,
                            "source": "owl", "name": "Owl", "title": "Owl"}}}
]}, "status": "ok", "time": 0.0004}
```

- **One point per passage.** Documents longer than the chunk size
  (`MDDB_EMBEDDING_CHUNK_SIZE`) are embedded per chunk; each matching chunk is
  a point, `payload.text` is that passage and `metadata.chunk` its index.
- **`metadata`** is the document's metadata — one value as a string, several
  as a list — plus `collection`, `key`, `lang` and `chunk`, which always
  describe the hit. `source` (what Open WebUI shows as the citation) defaults
  to the document key and `name` to its `title` metadata or the key, unless
  the document has its own.
- **`id`** is a UUID derived from the collection, document and chunk: the
  same passage always has the same id. Qdrant point ids are unsigned integers
  or UUIDs.
- **`score`** is MDDB's similarity, higher is closer (cosine by default, so
  at most 1). `version` is always 0.

## Authentication and permissions

`/qdrant` sits behind the same middleware as `/v1`. With
`MDDB_AUTH_ENABLED=true` a request needs an API key or a JWT, and the query
needs **read** on the collection. A caller without it gets `403` whether or
not the collection exists; with it, a missing collection is a `404`. Tenant
confinement and per-key limits apply as on `/v1`.

| Client option | Header sent | Accepted |
|---|---|---|
| `api_key="mddb_..."` | `api-key: mddb_...` | on `/qdrant` only |
| a JWT or key as `Authorization: Bearer ...` | `Authorization: Bearer ...` | everywhere |

The `api-key` header is read only under `/qdrant`, so `/v1` accepts exactly
the credentials it did before. `qdrant-client` warns when an API key is sent
over plain `http`; the warning does not stop the request.

## Compatibility test

`test/qdrant-compat/` runs `qdrant-client` (version locked in `uv.lock`),
unmodified, against a freshly built `mddbd` with auth on and an
OpenAI-shaped embedding endpoint served by the test, so the test and MDDB
embed with the same model as Open WebUI and MDDB must: the version probe,
`query_points` exactly as Open WebUI calls it read with Open WebUI's default
field mapping, a dimension mismatch, a named vector, a missing collection and
a wrong key. CI runs it in the `es-compat` job; locally:

```bash
make test-qdrant-compat
```

The Go tests (`services/mddbd/qdrant_*_test.go`) replay the client's requests
through the full middleware chain, in both storage layouts.
