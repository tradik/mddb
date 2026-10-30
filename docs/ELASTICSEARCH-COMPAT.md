---
title: "Elasticsearch-Compatible Subset"
slug: "docs/elasticsearch-compat"
description: "Point an Elasticsearch client at MDDB: _search with match, bool, term, range, knn, highlight and terms aggregations, _doc and _bulk — a subset translated onto MDDB's own engine, with every unsupported clause refused by name."
status: publish
---

# Elasticsearch-Compatible Subset

> v2.18.10+ (INT-019). Off by default: `MDDB_ES_COMPAT=true` turns it on.

Code written for Elasticsearch — the official SDKs, LangChain's
`ElasticsearchStore`, scripts — can index, search and delete MDDB documents
without being rewritten. This is a **subset mapped onto MDDB's own engine**,
not a second one: every request is translated into the requests `/v1` already
serves, and anything that cannot be translated faithfully is refused with a
`400` that names it. Nothing is dropped silently.

It is not Elasticsearch, and does not try to be. No mappings, no analyzers, no
cluster API, no scripts. If you need those, you need Elasticsearch.

## Turning it on

```bash
MDDB_ES_COMPAT=true mddbd
```

```python
from elasticsearch import Elasticsearch, helpers

es = Elasticsearch("http://localhost:11023/es", api_key="mddb_...")  # or bearer_auth=...
es.index(index="books", id="fox", document={"content": "The quick brown fox", "genre": "fable"})
es.search(index="books", query={"bool": {"must": {"match": {"content": "fox"}},
                                         "filter": {"term": {"genre": "fable"}}}})
```

The surface lives under **`/es`** on the HTTP listener. A prefix rather than a
separate listener: it shares the port, TLS, CORS and every middleware with
`/v1`, and it cannot collide with `/v1`, `/health`, `/metrics`, `/graphql`,
`/mcp` or `/debug` — a collection named `metrics` is `/es/metrics/_search`.
Enabling it adds routes under `/es` and changes nothing under `/v1`.

## Endpoints

| Request | What it does in MDDB |
|---|---|
| `GET /es`, `HEAD /es` | info and ping (see [Client negotiation](#client-negotiation)) |
| `HEAD /es/{index}` | 200 when the collection has a configuration or a document |
| `GET\|POST /es/{index}/_search` | the search translation below |
| `GET\|POST /es/{index}/_count` | the same search, `hits.total` only; the body may hold `query` only |
| `POST /es/{index}/_refresh` | waits up to 30 s for the collection's queued embeddings |
| `PUT\|POST /es/{index}/_doc/{id}`, `POST /es/{index}/_doc` | write one document (`addDocumentIf`, the single write path) |
| `PUT\|POST /es/{index}/_create/{id}`, `?op_type=create` | write, or 409 if it exists |
| `GET\|HEAD /es/{index}/_doc/{id}` | read one document |
| `DELETE /es/{index}/_doc/{id}` | delete; 404 `not_found` when absent |
| `PUT\|POST /es/_bulk`, `/es/{index}/_bulk` | `index`, `create`, `delete`, through the batch path |

`{index}` is a collection; an [alias](API.md) resolves to its collection.
Every other path is a `400` naming the method and path.

## Documents

| `_source` field | MDDB document |
|---|---|
| `content` (string) | the Markdown body (`contentMd`) |
| `lang` (string) | the language; else `?lang=`, else `en` |
| any other field: string, number, boolean, or a list of them | a metadata key; values are stored as strings |
| a nested object | refused (`400`, naming the field) |
| `null` | skipped |

`_id` is the document key. Keys are case-insensitive in MDDB (`Fox` and `fox`
are one document). A document in another language is read and deleted with
`?lang=`. `_version` is always 1 and `_seq_no` 0: optimistic concurrency
parameters (`if_seq_no`, `if_primary_term`, `version`, `version_type`) are
refused rather than ignored, and so are `pipeline` and `routing`.

Writes go through the same code as `/v1/add` and `/v1/add-batch`, so a
[schema](SCHEMA-VALIDATION.md) violation is a 400, a quota a 507 (or 429 for
the embedding budget, with `Retry-After`), and a write to a read-only
[corpus pack](CORPUS-PACKS.md) a 403 `cluster_block_exception` — the error
Elasticsearch gives for a blocked index. `?refresh=true|wait_for` on a write
waits for the collection's embeddings, as `_refresh` does.

## Search translation

| Elasticsearch | MDDB |
|---|---|
| `match` (operator `or`), the only text clause | `/v1/fts`, mode `simple`, BM25 |
| `match` (`and`), `match_phrase`, `multi_match` (`best_fields`, `most_fields`, `cross_fields`, `phrase`), several text clauses | `/v1/fts`, mode `expression` (AND, OR, NOT, phrases) |
| `term`, `terms` (in the query, `bool.must`, `bool.filter`) | operator filter `in` |
| `term`, `terms` in `bool.must_not` | operator filter `not` |
| `range` on a metadata key | operator filter `gt`/`gte`/`lt`/`lte` |
| `range` on `addedAt` / `updatedAt` | `rangeMeta` |
| `bool.must`, `bool.filter` | AND |
| `bool.should` | a required OR group (`minimum_should_match: 1`) |
| text in `bool.must_not` | `NOT` in the expression |
| `match_all`, or no query | metadata listing (`/v1/search`), score 1.0 |
| top-level `knn` | `/v1/vector-search`, `algorithm: auto`, `retrievalMode: parent` |
| `knn` plus a single `match` | `/v1/hybrid-search`, `strategy: rrf`, `vectorAlgorithm: auto` |
| `knn.query_vector` | `queryVector` |
| `knn.query_vector_builder.text_embedding.model_text` | `query`, embedded with the collection's provider |
| `knn.filter` | ANDed into the operator filter |
| `size`, `from` (body or URL) | `limit`, then the page is cut from the result |
| `sort`: `_score`; `_id`/`_doc`; `addedAt`, `updatedAt` | score order; `sort: key`; `sort: addedAt/updatedAt` (filter-only queries) |
| `_source`: `false`, a field, a list, `{"includes": [...]}` | projection of the response |
| `highlight` on `content` with one `pre_tags`/`post_tags` pair, `fragment_size`, `number_of_fragments` | `highlight`, `highlightTag`, `fragmentSize`, `maxHighlights` |
| `aggs` / `aggregations` → `terms` (`field`, `size`) | `facetBy`, `facetScope: all`; over the knn hits for knn |
| `field.keyword` | `field` (metadata values are exact already) |

The field a text clause names does **not** narrow it: MDDB has one full-text
index per document, covering the body and the metadata values the collection
indexes. `match: {title: "x"}` searches the same index as
`match: {content: "x"}`. Use `term` to filter on one metadata key.

`hits.total.value` is exact (`relation: "eq"`): for full text it is every
match, before paging; for knn it is the hits returned (at most `k`).

### Refused, by name

Each of these is a `400` whose `error.reason` names the clause, for example
`[fuzzy] query clause is not supported by the MDDB Elasticsearch subset`:

- any query clause not listed above: `fuzzy`, `prefix`, `wildcard`, `regexp`,
  `query_string`, `simple_query_string`, `exists`, `ids`, `nested`,
  `function_score`, `script`, `knn` inside `query`, ...;
- options a clause does not translate: `match.fuzziness`, `range.format`,
  `multi_match.slop`, ...;
- `term`/`terms`/`range`/`match_all` under `bool.should` or inside a bool
  nested in `should`/`must_not` (MDDB filters are ANDed); two `term` clauses on
  one field; `range` under `must_not`;
- `bool.should` next to a text `must`/`filter` (in Elasticsearch it only ranks
  there); text only under `must_not`; `minimum_should_match` other than 1;
- `knn` with a text query that is not a single `match`, or with
  `query_vector`, or with `model_text` different from the match text (hybrid
  search embeds its one query); `knn.similarity`; more than one knn;
- `sort` on any other field, by more than one key, `_score` ascending, or a
  field sort with text or knn; `highlight` without text or with knn, on a field
  other than `content`, or with per-field options;
- aggregations other than `terms`, sub-aggregations, `terms.order` and the
  other `terms` options;
- top-level keys `post_filter`, `search_after`, `collapse`, `rescore`,
  `suggest`, `script_fields`, `docvalue_fields`, `fields`, `explain`, `pit`,
  `retriever`, `rank`, `profile`, ...; URL parameters `q`, `sort`, `_source*`,
  `typed_keys`, `rest_total_hits_as_int`, `search_type`, `scroll`, `routing`;
- `_bulk` actions other than `index`, `create`, `delete`, and action options
  other than `_index`/`_id` — the whole request is refused before anything is
  written.

Out of scope for good: mappings and index settings, analyzers, scripts,
scroll, aggregations beyond `terms`, index templates, the cluster, `_cat` and
nodes APIs, `_update`, `_update_by_query`, ILM and the security API.

## Limits

- **Page size.** `from + size` goes through the server's cap (#368):
  `MDDB_SEARCH_MAX_LIMIT`, lowered by the collection's `retrieval.maxTopK`. A
  capped page returns fewer hits and says so three ways: `X-Limit-Capped:
  true` and `X-Limit: n` headers, `"limitCapped": true, "limit": n` in the body
  (clients keep unknown top-level fields), and a `Warning: 299 MDDB "..."`
  header, which elasticsearch-py raises as an `ElasticsearchWarning` — a
  warning, not an error. Elasticsearch itself answers a page beyond
  `index.max_result_window` with a 400; MDDB caps instead, as on `/v1`.
- **Bulk.** At most 10 000 operations per request; the body is bounded by
  `MDDB_MAX_BODY_BYTES` (413 above it). Runs of writes on one collection and
  language are one batch; a second write to the same `_id`, a delete, or a
  change of collection ends a run, so operations apply in order.
- **Concurrency.** `_search` and `_count` take a slot of
  `MDDB_SEARCH_MAX_CONCURRENT` like `/v1/fts`.

## Authentication and permissions

`/es` sits behind the same middleware as `/v1`: there is no unauthenticated
door. With `MDDB_AUTH_ENABLED=true` a request needs a JWT or an API key, and
the collection permissions apply — read for `_search`, `_count`, `_doc` GET,
`HEAD` and `_refresh`; write for `_doc` writes and `_bulk` (checked per
`_index`; an item without permission is a 403 item). The rate limiter and the
body cap apply too.

| Client option | Header sent | Accepted |
|---|---|---|
| `api_key="mddb_..."` | `Authorization: ApiKey mddb_...` | on `/es` only |
| `bearer_auth="mddb_..."` or a JWT | `Authorization: Bearer ...` | everywhere |
| `headers={"X-API-Key": ...}` | `X-API-Key` | everywhere |
| `basic_auth=(user, password)` | `Authorization: Basic ...` | no — use an API key |

The `ApiKey` scheme is read only under `/es`, so `/v1` accepts exactly the
credentials it did before. An `api_key=(id, key)` tuple is base64-encoded by
the client and is not an MDDB key; pass the key string.

## Client negotiation

Official clients 8.x and 9.x check one thing about the server: every 2xx
response must carry `X-Elastic-Product: Elasticsearch`, or they raise
`UnsupportedProductError` before the caller sees the answer. They do not read
the version from `GET /` (clients 7.14–7.17 did). So every `/es` response sends
the header — it is what lets an unmodified client talk to MDDB at all. MDDB
does not otherwise claim to be Elasticsearch: `GET /es` reports
`version.number: "8.19.0"` (the API level the subset follows, which tools that
read it can reason about), `build_type: "mddb"`, the MDDB version in
`build_hash`, and a `tagline` that says it is MDDB. The compatibility media
types (`application/vnd.elasticsearch+json; compatible-with=8` or `=9`) are
accepted; responses are `application/json`, which the clients parse.

## Compatibility test

`test/es-compat/` runs the official `elasticsearch-py` (version locked in
`uv.lock`), unmodified, against a freshly built `mddbd` with the lexical
embedding provider: info and ping, index, bulk, get, search with `match`,
`bool` and `knn`, aggregations, count, a refused clause, delete. CI runs it as
the `es-compat` job; locally:

```bash
make test-es-compat
```

The Go tests (`services/mddbd/es_*_test.go`) replay the requests the client
sends — the compatibility media types, `PUT /_bulk` with NDJSON, the `ApiKey`
header — through the full middleware chain, in both storage layouts.

### opensearch-py

`opensearch-py` is not in CI. It was run by hand (3.2.0, against a v2.18.10
build with `MDDB_ES_COMPAT=true` and the lexical provider), with
`OpenSearch(hosts=[{"host": "localhost", "port": 11023, "url_prefix": "es"}])`.
`info`, `ping`, `index`, `helpers.bulk`, `indices.refresh`, `indices.exists`,
`get`, `search` with `match`, with `bool` + `term` and with a top-level `knn`
using `query_vector_builder`, a `terms` aggregation, `count` and `delete` all
worked. OpenSearch's k-NN plugin syntax, `knn` inside `query`, is refused
with a `400` naming it: write it as a top-level `knn` instead. Moving from
OpenSearch is covered in [INTEGRATIONS.md](INTEGRATIONS.md#3-opensearch--mddb).
