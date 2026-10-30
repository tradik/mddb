---
title: "MDDB Search Algorithms"
slug: "docs/search"
description: "MDDB's four search methods - metadata, full-text, vector and hybrid - with algorithms selectable per query, retrieval modes and result diversification."
status: publish
---

# MDDB Search Algorithms

MDDB provides four search methods: **Metadata Search**, **Full-Text Search**, **Vector Search**, and **Hybrid Search**. Each method supports multiple algorithms selectable at query time via the `algorithm` parameter.

## Overview

| Method | Algorithms | Best For |
|--------|-----------|----------|
| Metadata Search | Indexed filters | Exact tag/category matching |
| Full-Text Search | TF-IDF, BM25, BM25F, PMISparse | Keyword-based document retrieval |
| Vector Search | Flat, HNSW, IVF, PQ, SQ, BQ | Semantic similarity by meaning |
| Hybrid Search | Alpha Blending, RRF | Combined keyword + semantic relevance |
| Aggregation | Facets, Histograms | Analytics and filtering UI |

## Full-Text Search

Full-text search uses an inverted index built from document content. Queries are tokenized, stop words are removed, and documents are scored by relevance.

### Multi-Language Support (v2.8.0+)

FTS supports language-aware stemming and stop word filtering for 18 languages. Each document's `lang` field determines which stemmer and stop word list is used during indexing and querying.

**Supported languages:** English, Polish, German, French, Spanish, Italian, Portuguese, Dutch, Russian, Swedish, Norwegian, Danish, Finnish, Hungarian, Romanian, Turkish, Arabic, Tamil.

Language codes are normalized: `en_US` → `en`, `pl_PL` → `pl`. Unknown languages fall back to the configured default (English by default).

```bash
# Index a Polish document
curl -X POST http://localhost:11023/v1/add \
  -d '{"collection":"articles","key":"post-pl","lang":"pl","contentMd":"Programowanie w Go jest wydajne."}'

# Search with Polish query tokenization
curl -X POST http://localhost:11023/v1/fts \
  -d '{"collection":"articles","query":"programowanie wydajne","lang":"pl","algorithm":"bm25"}'
```

Configure default language: `MDDB_FTS_DEFAULT_LANG=en` (default). Query supported languages: `GET /v1/fts-languages`. Reindex existing documents: `POST /v1/fts-reindex` with `{"collection":"X"}`.

**Protocol parity:** The `lang` parameter, FTS reindex, and FTS languages endpoints are available across all protocols: REST API, gRPC (`FTS`, `FTSReindex`, `FTSLanguages` RPCs), and MCP tools (`full_text_search` with `lang`, `fts_reindex`, `fts_languages`).

### Text Processing Pipeline

1. **Lowercasing** - All text converted to lowercase
2. **Tokenization** - Split on non-alphanumeric characters, minimum 2 characters
3. **Stop Word Removal** - Language-specific stop words filtered (e.g., ~79 English, ~297 Polish, ~232 German). Configurable via per-collection custom stop words.
4. **Stemming** (v2.6.4+) - Language-specific stemmer reduces words to their root form (e.g., English "running" → "run", Polish "domów" → "dom", German "Häuser" → "haus"). Enabled by default, configurable via `MDDB_FTS_STEMMING`.
5. **Synonym Expansion** (v2.6.4+, query-time only) - Query terms are expanded with configured synonyms. Bidirectional: if "big" has synonym "large", searching "large" also finds "big". Configurable via `MDDB_FTS_SYNONYMS`.

#### Per-Query Control

Both stemming and synonyms can be disabled per-query using request fields:
```json
{
  "collection": "docs",
  "query": "running fast",
  "algorithm": "bm25",
  "disableStem": true,
  "disableSynonyms": true
}
```

#### Synonym Management API

```bash
# Add synonyms
curl -X POST http://localhost:11023/v1/synonyms \
  -d '{"collection":"docs","term":"big","synonyms":["large","huge","enormous"]}'

# List synonyms
curl http://localhost:11023/v1/synonyms?collection=docs

# Delete synonyms
curl -X DELETE http://localhost:11023/v1/synonyms \
  -d '{"collection":"docs","term":"big"}'
```

### Stop Word Management

MDDB ships with language-specific stop words for 18 languages (e.g., ~79 English, ~297 Polish, ~232 German). You can add custom stop words per collection on top of language defaults.

```bash
# Add custom stop words
curl -X POST http://localhost:11023/v1/stopwords \
  -d '{"collection":"docs","words":["foo","bar","baz"]}'

# List stop words
curl http://localhost:11023/v1/stopwords?collection=docs

# Delete custom stop words
curl -X DELETE http://localhost:11023/v1/stopwords \
  -d '{"collection":"docs","words":["foo"]}'
```

### Search Modes

FTS supports 8 search modes. The mode can be set via the `mode` parameter, or left as `"auto"` (default) for automatic detection.

| Mode | Syntax Example | Description |
|------|---------------|-------------|
| `simple` | `markdown database` | Basic keyword search |
| `boolean` | `markdown AND database NOT sql` | Flat boolean — AND, OR, NOT, `+`, `-` (legacy; no nesting) |
| `phrase` | `"markdown database"` | Exact phrase matching (consecutive terms) |
| `proximity` | `"markdown database"~5` | Terms within N words of each other |
| `wildcard` | `mark* dat?base` | Pattern matching with `*` and `?` |
| `expression` | `(rust OR golang) AND "async runtime"~3 NOT legacy` | Full query DSL with parentheses, precedence, and mixed atoms (v2.9.13+) |
| `range` | via `rangeMeta` parameter | Numeric/date range filtering |
| `fuzzy` | `fuzzy: 1` or `fuzzy: 2` | Typo-tolerant matching (any mode) |
| `auto` | _(default)_ | Auto-detects the appropriate mode |

#### Auto Mode Detection

When `mode` is omitted or set to `"auto"`, the query parser inspects the query string:

1. Contains `"..."` only → **phrase** mode
2. Contains `"..."~N` → **proximity** mode
3. Contains `*` or `?` → **wildcard** mode
4. Contains `AND`, `OR`, `NOT`, `+`, `-` → **boolean** mode
5. Otherwise → **simple** mode

#### Expression Search (v2.9.13+)

`mode: "expression"` runs the query through a proper recursive-descent parser that understands parenthesized grouping, operator precedence (NOT > AND > OR), and mixed atom types in a single query.

Supported atoms inside an expression:

- Bare terms → `rust`
- Fuzzy terms with edit-distance modifier → `color~1`, `markdwn~2` (0–2, clamped)
- Quoted phrases → `"machine learning"`
- Proximity phrases → `"rust systems"~5`
- Wildcards → `mark*`, `te?t`
- Parenthesized groups → `(rust OR golang)`
- Negation → `NOT spam` or `-spam`
- Implicit AND between adjacent atoms → `rust async` parses as `rust AND async`

Precedence from tightest to loosest: `NOT` > `AND` > `OR`. So `a AND b OR c` parses as `(a AND b) OR c`, while `a OR b AND c` parses as `a OR (b AND c)`.

**Quotes inside a phrase** are escaped with a backslash, and a literal backslash is doubled:

| Query | Phrase searched for |
|---|---|
| `"the \"json\" field"` | `the "json" field` |
| `"a \\ b"` | `a \ b` |
| `"unterminated` | error: *unterminated phrase: expected a closing quote* |

Before v2.12.0 an embedded quote ended the phrase early and the rest of the query was reinterpreted as operators, so `"the "json" field"` silently searched for something else. A phrase with no closing quote is now an error rather than a phrase running to the end of the query.

The parser normalises what it reads, so `expr.String()` returns a query that parses back to the same expression — worth knowing when a query is logged and pasted back. Double negation collapses (`NOT NOT rust` and `NOT(NOT rust)` are both `rust`), implicit AND is printed explicitly, and grouping is printed where precedence needs it.

Examples:

```text
(rust OR golang) AND "async runtime"~3 NOT legacy
rust AND (systems OR async) AND NOT deprecated
"machine learning" OR (ml AND algorithms)
markdwn~1 OR markdown
```

Evaluation reuses the existing scorers — each leaf atom calls through to the same `SearchBM25` / `SearchPhrase` / `SearchProximity` / `SearchWildcard` / `SearchBM25Fuzzy` path that the legacy modes use. AND intersects matched doc sets and sums scores; OR unions and sums; NOT subtracts from its sibling without affecting scores. So scoring stays consistent across modes.

#### Boolean Search

Supports flat boolean logic with AND, OR, NOT operators and required/excluded term prefixes. No parenthesized grouping — use `mode: "expression"` when you need nesting.

```bash
# AND (implicit between terms)
curl -X POST http://localhost:11023/v1/fts \
  -d '{"collection":"docs","query":"markdown AND database","mode":"boolean"}'

# OR
curl -X POST http://localhost:11023/v1/fts \
  -d '{"collection":"docs","query":"markdown OR asciidoc","mode":"boolean"}'

# NOT / exclusion
curl -X POST http://localhost:11023/v1/fts \
  -d '{"collection":"docs","query":"database NOT sql","mode":"boolean"}'

# Required term (+) and excluded term (-)
curl -X POST http://localhost:11023/v1/fts \
  -d '{"collection":"docs","query":"+markdown -html database","mode":"boolean"}'
```

#### Phrase Search

Matches exact sequences of consecutive terms using the positional index.

```bash
curl -X POST http://localhost:11023/v1/fts \
  -d '{"collection":"docs","query":"\"markdown database\"","mode":"phrase"}'
```

#### Proximity Search

Finds documents where terms appear within N words of each other.

```bash
# "markdown" and "database" within 5 words of each other
curl -X POST http://localhost:11023/v1/fts \
  -d '{"collection":"docs","query":"\"markdown database\"~5","mode":"proximity"}'
```

**Scoring:** Based on the minimum span between matched terms — closer matches score higher.

#### Wildcard Search

Pattern matching against indexed terms. `*` matches any number of characters, `?` matches exactly one.

```bash
# Prefix match
curl -X POST http://localhost:11023/v1/fts \
  -d '{"collection":"docs","query":"mark*","mode":"wildcard"}'

# Single character wildcard
curl -X POST http://localhost:11023/v1/fts \
  -d '{"collection":"docs","query":"te?t","mode":"wildcard"}'
```

Matched terms are scored with BM25.

#### Range Filtering

Filter FTS results by numeric or date ranges on metadata fields and built-in fields (`addedAt`, `updatedAt`).

```bash
curl -X POST http://localhost:11023/v1/fts \
  -d '{
    "collection": "blog",
    "query": "tutorial",
    "rangeMeta": {
      "addedAt": {"gte": "2026-01-01", "lte": "2026-12-31"},
      "meta.price": {"gte": 10, "lt": 100}
    }
  }'
```

| Operator | Description |
|----------|-------------|
| `gte` | Greater than or equal |
| `gt` | Greater than |
| `lte` | Less than or equal |
| `lt` | Less than |

Supports numeric values, date strings, and string lexicographic comparison.

### Scoring Algorithms

All search modes support the `algorithm` parameter to select the scoring function.

### TF-IDF (default)

Classic Term Frequency-Inverse Document Frequency scoring.

**Formula:**
```
score = sum(TF(term, doc) * IDF(term))
where:
  TF(term, doc) = count(term in doc) / total_terms(doc)
  IDF(term)     = log(N / df(term))
```

**When to use:** General-purpose keyword search. Good for short queries and when document lengths are similar.

### BM25

Okapi BM25 is an improved ranking function that adds document length normalization. Longer documents are penalized so they don't dominate results simply because they contain more terms.

**Formula:**
```
score = sum(IDF(term) * (TF * (k1 + 1)) / (TF + k1 * (1 - b + b * dl/avgdl)))
where:
  k1    = 1.2  (term frequency saturation)
  b     = 0.75 (document length normalization)
  dl    = document length (in terms)
  avgdl = average document length across collection
  IDF   = ln((N - df + 0.5) / (df + 0.5) + 1)
```

**When to use:** When documents vary significantly in length (e.g., mix of short FAQs and long guides). BM25 prevents long documents from dominating results.

#### Tuning k1 and b (v2.18.8+)

`k1` and `b` are parameters of the scoring, not of the index, so changing them
needs no reindex.

- **Where to set them.** Pass `bm25K1` and `bm25B` on `POST /v1/fts` and
  `POST /v1/hybrid-search`. Over MCP and gRPC they are `bm25_k1` and
  `bm25_b`. To make them a collection's default, store them in its retrieval
  profile as `retrieval.bm25K1` and `retrieval.bm25B`.
- **Precedence.** The request wins over the profile, and the profile over
  1.2 / 0.75, separately for each parameter.
- **Range.** `k1` is 0–3 and `b` is 0–1. `b = 0` ignores document length.
  `k1 = 0` counts a word once however often it occurs.
- **Where they apply.** `bm25` and `bm25f`, their fuzzy variants, and the
  full-text half of hybrid search.

Don't change them by feel. On the MDDB docs, lowering either one made
ranking worse and raising `k1` to 2 helped slightly. The numbers are in
[BENCHMARK.md](BENCHMARK.md#bm25-parameters-on-the-mddb-docs-v2188). Try
values on your own questions with `mddb-cli eval --config fts/bm25:k1=2:b=0.75`.
With an evaluation set stored, the search advisor tries three variants and
recommends one only when it scores better.

### BM25F (Field-Weighted)

BM25F extends BM25 by scoring term matches in different document fields with different weights. A match in the title can be worth more than a match in the body text.

Documents are automatically indexed per-field: `content` (body text) and each metadata key as `meta.<key>` (e.g., `meta.title`, `meta.tags`, `meta.description`).

**Formula:**
```
score = sum(IDF(term) * tf_tilde / (k1 + tf_tilde))
where:
  tf_tilde = sum_field(w_f * tf(term, doc, field) / (1 - b + b * dl_f / avgdl_f))
  w_f     = field weight (e.g., title=3.0, content=1.0)
  dl_f    = length of field f in document
  avgdl_f = average length of field f across collection
```

**Default Field Weights:**

| Field | Default Weight |
|-------|---------------|
| `content` | 1.0 |
| `meta.title` | 3.0 |
| `meta.tags` | 2.0 |
| `meta.category` | 2.0 |
| `meta.description` | 1.5 |

Custom weights can be passed per-query via `fieldWeights`. Fields not in the weights map are ignored. While the collection's `retrieval.summaryMode` is not `off`, `meta.summary` is added with `retrieval.summaryWeight` (default 2.0); see [Summaries](#summaries--a-second-representation-v2189).

**When to use:** When documents have structured metadata (title, tags, etc.) and you want title matches to rank higher than body-only matches. Best for content management, documentation, and knowledge bases.

### PMISparse (Query Expansion)

PMISparse combines BM25 scoring with automatic query expansion using Pointwise Mutual Information (PMI). It discovers related terms from the corpus and adds them to the query, improving recall without manual synonym lists.

**How it works:**

1. **Phase 1** — Standard BM25 scoring for direct query terms
2. **Phase 2** — PMI expansion: finds statistically related terms from the index and scores documents against them
3. **Combined** — Final score = BM25 score + (alpha × expansion score)

**Parameters:**

| Parameter | Default | Description |
|-----------|---------|-------------|
| `k1` | 1.5 | Term frequency saturation (tuned for expansion) |
| `b` | 0.75 | Document length normalization |
| `alpha` | 0.35 | Expansion weight multiplier |
| `expansionK` | 5 | Expansion terms per query term |
| `windowSize` | 5 | Co-occurrence sliding window |
| `minCount` | 2 | Minimum term frequency for PMI |

The PPMI (Positive PMI) matrix is trained lazily on first use and automatically invalidated when the index changes.

**When to use:** When recall matters more than precision — e.g., exploratory search, knowledge discovery, or queries where users might not know the exact terminology used in documents.

```bash
curl -X POST http://localhost:11023/v1/fts \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "kb",
    "query": "machine learning",
    "algorithm": "pmisparse",
    "limit": 10
  }'
```

See [PMISPARSE.md](PMISPARSE.md) for detailed algorithm description.

### Typo Tolerance (Fuzzy Search)

All algorithms (TF-IDF, BM25, BM25F) support typo tolerance via the `fuzzy` parameter. When enabled, the search finds indexed terms within Levenshtein edit distance of each query term.

| `fuzzy` | Tolerance | Example |
|---------|-----------|---------|
| `0` (default) | Off — exact matching only | "javascrip" → no match |
| `1` | 1 edit (insert, delete, or substitute) | "javascrip" → "javascript" |
| `2` | 2 edits | "javasript" → "javascript" |

**Scoring:** Fuzzy matches receive a 0.8x score penalty compared to exact matches, so exact results always rank higher.

**Matched terms format:** Fuzzy matches appear as `queryTerm~indexedTerm` (e.g., `javascrip~javascript`) in the `matchedTerms` array, making it easy to distinguish exact vs fuzzy matches.

### In-Graph Metadata Filtering (v2.6.5+)

FTS supports `filterMeta` to narrow results by metadata before scoring, just like vector search. This is useful for scoped keyword searches (e.g., search only within a specific category).

```bash
# BM25 search filtered to "tutorial" category
curl -X POST http://localhost:11023/v1/fts \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "blog",
    "query": "markdown database",
    "algorithm": "bm25",
    "filterMeta": {"category": ["tutorial"], "status": ["published"]}
  }'
```

**Filter logic:** AND between different metadata keys, OR between values of the same key (same as metadata search).

### Filter Operators (v2.18.8+)

`filterMeta` asks one question: is the value one of these? `filter` asks the
rest — "path starts with `internal/`", "modified after September", "smaller
than 10 kB", "not a test", "has a summary". It is accepted by `/v1/fts`,
`/v1/vector-search`, `/v1/hybrid-search`, `/v1/cross-search`, `/v1/search`
and `/v1/aggregate`, by the matching MCP tools (`filter` argument) and by the
GraphQL `search` and `vectorSearch` inputs.

```bash
curl -X POST http://localhost:11023/v1/fts \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "code",
    "query": "hnsw",
    "filter": {
      "path":     {"prefix": "internal/"},
      "modified": {"gte": "2026-09-01"},
      "size":     {"lt": 10000},
      "kind":     {"not": ["test"]},
      "summary":  {"exists": true},
      "title":    {"contains": "hnsw"}
    }
  }'
```

| Operator | Matches when | Notes |
|---|---|---|
| `in` | a value equals one of the list | one string or an array; same as `filterMeta` |
| `prefix` | a value starts with the string | exact case; seeks the index, no scan |
| `contains` | a value contains the substring | case-insensitive |
| `gt` `gte` `lt` `lte` | a value is beyond the bound | numbers compare as numbers, dates as dates (a bound may be a date or a unix timestamp), anything else lexically |
| `not` | the document has none of the values | documents without the key match |
| `exists` | the document has (`true`) or lacks (`false`) the key | |

- Keys are ANDed. Inside one key the value operators must hold for the
  **same** value; a document with several values matches when any one does.
- `filter` and `filterMeta` together are ANDed.
- Every operator is answered from the metadata index. No document body is
  read to evaluate a filter.
- An unknown operator is rejected with `400` and its name, so a typo does not
  turn into a filter that matches everything.
- `rangeMeta` on `/v1/fts` (v2.7) still works. Since v2.18.8 a range on a
  metadata key is folded into `filter` and answered from the index; ranges on
  `addedAt` / `updatedAt` are still checked on the document. A multi-valued
  key now matches when any value is in range, not only the first.

### Per-Query Boost/Demote (v2.9.12+)

Reshape ranking without reindexing by attaching a `boost` map to the request. Keys are in `"metaKey:metaValue"` form; values multiply the score of matching documents.

- Positive value — boosts (`5.0` means 5× score)
- Negative value — demotes via inverse (`-2.0` means ½× score)
- Zero — ignored
- Multiple matching entries combine multiplicatively; the combined multiplier is floored at `0.001` so a stack of demotions cannot collapse the score to zero

Works with any FTS algorithm (`tfidf`, `bm25`, `bm25f`, `pmisparse`) and with Hybrid search (applied to the fused `combinedScore`, not to the raw FTS/vector sub-scores).

```bash
# Boost "featured" docs 5×, demote archived 2×, on top of BM25
curl -X POST http://localhost:11023/v1/fts \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "blog",
    "query": "markdown database",
    "algorithm": "bm25",
    "boost": {"tag:featured": 5.0, "status:archived": -2.0}
  }'
```

### Highlighting with Fragments (v2.9.13+)

Set `highlight: true` on `/v1/fts` and each result carries a `highlights` array with text snippets taken from the document around matched terms. Works uniformly across every FTS mode — `simple`, `boolean`, `phrase`, `wildcard`, `proximity`, and `expression` — because the highlighter reuses `matchedTerms` from the existing FTS pipeline rather than adding its own index.

Each `Highlight` has:

| Field | Meaning |
|-------|---------|
| `fragment` | The snippet with matched terms wrapped in the configured tag. Ellipsis `…` markers denote truncation at the start or end. |
| `matchedTerms` | Distinct terms whose matches appear in this fragment. |
| `startOffset` / `endOffset` | Byte positions into the raw `ContentMD` — clients can re-slice the original doc if they need untagged context. |

Tunables on the request:

- `highlight` — enable (default off so legacy callers see no payload growth).
- `highlightTag` — open tag, e.g. `"<mark>"` (default), `"<strong>"`, `"**"`, `"[h]"`. Close tag is derived automatically (HTML-style `</mark>` for angle brackets, or mirror for symmetric delimiters).
- `maxHighlights` — cap fragments per result (default 3).
- `fragmentSize` — approx chars per fragment (default 150, snapped to word boundaries).

Selection strategy: the extractor clusters nearby matches into one fragment so adjacent hits don't fragment into N tiny snippets, ranks clusters by hit count, trims to `maxHighlights`, then re-sorts the final list by document offset so UIs display them in reading order. Matching is case-insensitive on ASCII word boundaries — `"rust"` matches `Rust`, `RUST`, but not the `rust` inside `rustic`.

```bash
curl -X POST http://localhost:11023/v1/fts \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "query": "markdown database",
    "highlight": true,
    "highlightTag": "<strong>",
    "maxHighlights": 2,
    "fragmentSize": 120
  }'
```

### Prefix Autocomplete (v2.9.12+)

Return up to `topN` terms starting with the given prefix, ranked by document frequency. Scans the existing FTS inverted index — no additional storage or indexing step is required.

```bash
# Global autocomplete across the whole collection
curl "http://localhost:11023/v1/autocomplete?collection=blog&q=mar&topN=5"

# Field-scoped autocomplete for type-ahead title search
curl "http://localhost:11023/v1/autocomplete?collection=blog&q=mar&field=meta.title"
```

- Prefix is lowercased and stripped of non-alphanumerics (first separator ends the prefix)
- Prefix is capped at 32 characters; longer prefixes are silently truncated
- A single call scans at most 10 000 index entries so pathological prefixes like `"a"` stay fast
- Empty `q` returns an empty result list rather than an error (saves client-side guards on cleared inputs)
- Exposed as MCP `autocomplete` tool with the same parameters

### API Examples

```bash
# TF-IDF (default)
curl -X POST http://localhost:11023/v1/fts \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "blog",
    "query": "markdown database tutorial",
    "limit": 10
  }'

# BM25
curl -X POST http://localhost:11023/v1/fts \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "blog",
    "query": "markdown database tutorial",
    "limit": 10,
    "algorithm": "bm25"
  }'

# BM25F (field-weighted, with custom weights)
curl -X POST http://localhost:11023/v1/fts \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "blog",
    "query": "markdown database tutorial",
    "limit": 10,
    "algorithm": "bm25f",
    "fieldWeights": {
      "content": 1.0,
      "meta.title": 5.0,
      "meta.tags": 2.0
    }
  }'

# With typo tolerance (works with any algorithm)
curl -X POST http://localhost:11023/v1/fts \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "blog",
    "query": "markdwn datbase tutrial",
    "limit": 10,
    "algorithm": "bm25",
    "fuzzy": 1
  }'
```

### MCP Tool

```json
{
  "tool": "full_text_search",
  "arguments": {
    "collection": "blog",
    "query": "markdown database",
    "algorithm": "bm25",
    "fuzzy": 1,
    "limit": 10
  }
}
```

## Result Caching

Agents repeat identical queries in loops, and a repeated search redoes the full
scoring pass for a result set that has not changed. Add `cacheTtl` (seconds) to
a full-text search request to reuse a recent answer:

```bash
curl -X POST http://localhost:11023/v1/fts \
  -H 'Content-Type: application/json' \
  -d '{"collection":"docs","query":"golang","cacheTtl":60}'
```

The response carries `X-MDDB-Cache: hit` or `miss`.

Caching is **opt-in per request**. A search without `cacheTtl` is neither
served from the cache nor stored in it, so the default behaviour and the
default freshness are exactly what they were — there is no silent staleness to
reason about. The caller decides how stale an answer may be, per query.

Two requests share an entry when they ask the same question of the same
collection: the key covers the whole request except `cacheTtl` itself, so a
caller willing to hold an answer for ten minutes and one willing to hold it for
ten seconds still share one.

**Writing to a collection invalidates its cached results immediately** — adds,
updates, deletes, batch operations and FTS reindexing all do. Invalidation is a
per-collection counter mixed into the key, so it costs nothing on the write
path and never serves a result set from before your write.

`cacheTtl` is capped at 3600 seconds. `MDDB_SEARCH_CACHE_SIZE=0` disables the
cache for the whole server whatever requests ask for; see
[config.md](config.md#logging).

## Vector Search

### Loading the index at startup

Vectors are stored on disk and the indexes are rebuilt in memory at every
start. Each stored vector goes into every algorithm's index (flat, HNSW, IVF,
PQ, OPQ, SQ, SQ4, BQ), and IVF, PQ and OPQ train with k-means on each
collection, so the load time grows with the number of vectors, not of
documents, and HNSW insertion and the training dominate it. Collections with
vectors enabled but none stored cost nothing. A disk-only collection loads its
quantized vectors only.

Since v2.18.4 collections load smallest first, and each one is searchable as
soon as its own vectors are in: a small collection answers while a large one
is still loading, and a collection without vectors answers at once. Until a
collection is loaded, a vector search on it answers `503` with `Retry-After`;
hybrid search answers from full text alone. The progress is on
`/v1/vector-stats` (`indexProgress`) and `/health` (`vectorIndex`), and in the
log every minute.

Vector search embeds the query text into a high-dimensional vector and finds documents with the most similar embeddings using cosine similarity. Similarity computation is hardware-accelerated on ARM64 via NEON (all ARM64) and SME (Apple M4+) SIMD instructions, with automatic runtime detection and fallback to scalar Go on other platforms. Search is parallelized across multiple goroutines for collections above 2048 vectors (~2.5x speedup on 50K collections).

### Flat (default)

Exact brute-force search. Compares the query vector against every document vector in the collection.

| Property | Value |
|----------|-------|
| Accuracy | 100% (exact) |
| Speed | O(n) - linear with collection size |
| Memory | Original vectors only |
| Build time | None |

**When to use:** Small collections (< 10K documents) or when perfect recall is required.

### HNSW (Hierarchical Navigable Small World)

Approximate nearest neighbor search using a multi-layer graph structure. Each layer connects vectors to their nearest neighbors, enabling logarithmic search time.

| Property | Value |
|----------|-------|
| Accuracy | recall@10 1.000 at 20k and 200k vectors (32 dimensions), 0.98–0.99 at 200k (128 dimensions), on the planner benchmark |
| Speed | O(log n) |
| Memory | Vectors + graph edges (~2x flat) |
| Build time | O(n log n) |
| Parameters | M=16 (32 links on layer 0), efConstruction=200, efSearch=64 (raised to the number of candidates asked for) |

**When to use:** Best general-purpose algorithm for medium to large collections (10K-1M documents). Excellent speed/accuracy trade-off.

> **The graph was replaced in v2.18.9 (SRCH-013).** Before, HNSW ran on
> `github.com/coder/hnsw`, and its recall@10 on the planner benchmark was 0.86
> at 2k vectors, 0.83 at 200k and 0.66 at 2M, varying by up to 0.13 between
> two loads of the same data. The library kept each node's M *closest*
> candidates as neighbours. Documents that arrive topic by topic then link only
> within their own topic, and a search that enters the wrong topic cannot get
> out. It also kept M links on layer 0 rather than 2M, ignored
> `efConstruction`, and drew levels from the clock. The graph is now part of
> MDDB (`internal/vector/vector_hnsw_graph.go`). It uses the paper's
> neighbour-selection heuristic, 2M links on layer 0 and a real
> `efConstruction`, and draws levels from a fixed seed. It measures recall@10
> of 1.000 at 20k and 200k and is faster per query; see
> [Vector query planner](BENCHMARK.md#vector-query-planner-v2189). The graph
> orders by cosine distance whatever `metric` the search scores with, as
> before. For normalised embeddings, cosine, dot product and euclidean rank
> alike.

### IVF (Inverted File Index)

Clusters vectors using k-means, then searches only the nearest clusters.

> **Training (IVF, PQ, OPQ, SQ, SQ4).** These five search a structure trained
> from the collection's vectors. It is trained at startup, after a
> `vector-reindex`, and — since 2.15.3 — by the index itself: the first search
> on a collection that was never trained trains it and waits for it, and a
> collection that has doubled since its last training is retrained in the
> background while searches carry on against the current structure. Before
> 2.15.3 a collection created while the server was running returned no results
> from these five until a restart or reindex.

> **Two embedding models in one collection (v2.15.4).** After a model change a
> collection holds both models' vectors until a reindex replaces the old ones.
> Every algorithm now compares a query only with vectors of its own dimension;
> before, the others came back with a score of 0, and a threshold of 0 let
> them through as matches. Trained indexes train on the dimension most vectors
> have. A quantized collection whose quantization was changed holds int8 and
> int4 vectors side by side, and each is scored as its own type — scoring them
> all as the original type panicked on every search (#269). Reindex after
> changing the model to bring every document back into one space.

| Property | Value |
|----------|-------|
| Accuracy | ~90-98% recall (depends on nProbe) |
| Speed | O(n/k) where k = number of clusters |
| Memory | Vectors + cluster assignments |
| Build time | O(n * iterations) for k-means training |
| Parameters | nClusters=sqrt(N), nProbe=10 |

**When to use:** Large collections (> 100K documents) where you need faster search than flat but HNSW memory overhead is too high.

### PQ (Product Quantization)

Compresses vectors by splitting them into subspaces and quantizing each subspace independently. Dramatically reduces memory usage at the cost of some accuracy.

| Property | Value |
|----------|-------|
| Accuracy | ~85-95% recall |
| Speed | Fast (compressed distance computation) |
| Memory | ~32x compression (8 bytes per vector vs 256 for flat) |
| Build time | O(n * iterations) for codebook training |
| Parameters | 8 subspaces, 256 codebook entries |

**When to use:** Very large collections (> 500K documents) where memory is the primary constraint. Re-ranks top candidates with exact cosine for better accuracy.

### OPQ (Optimized Product Quantization)

Extends PQ by learning an orthogonal rotation matrix that decorrelates dimensions before subspace splitting. Jointly optimizes rotation and codebooks via alternating optimization.

| Property | Value |
|----------|-------|
| Accuracy | ~88-97% recall (~1-3% better than PQ) |
| Speed | Fast (same ADC as PQ, rotated query) |
| Memory | ~32x compression (same as PQ) |
| Build time | O(n * iterations * opqIter) |
| Parameters | 8 subspaces, 256 codebook entries, 5 optimization iterations |

**When to use:** Same use case as PQ but when higher recall is needed. The rotation learning adds training time but search speed is identical to PQ.

### SQ (Scalar Quantization)

Compresses vectors by quantizing each float32 dimension to uint8 (8-bit). Simpler than PQ with better accuracy but less compression.

| Property | Value |
|----------|-------|
| Accuracy | ~92-98% recall |
| Speed | Fast (integer distance computation) |
| Memory | ~4x compression (1 byte per dimension vs 4 for flat) |
| Build time | O(n) - just min/max calibration |
| Parameters | Automatic calibration |

**When to use:** Medium to large collections where you need memory savings with better accuracy than PQ. Good middle ground between flat and PQ.

### SQ4 (4-bit Scalar Quantization)

Half of SQ's storage: four bits per dimension, two dimensions packed into each
byte. Fills the gap between `sq` (4x smaller than float32) and `bq` (32x
smaller but much coarser).

| Property | Value |
|----------|-------|
| Accuracy | **99.5% of `sq`'s recall@10** — measured, see below |
| Speed | Fast (16-entry distance table per dimension) |
| Memory | **8x compression** (0.5 bytes per dimension) |
| Build time | O(n log n) — per-dimension percentiles |
| Parameters | Automatic calibration |

Two design decisions distinguish it from a naive 4-bit quantizer:

- **Percentile boundaries, not min/max.** With only sixteen levels, one outlier
  stretching the range collapses every ordinary value into two or three codes.
  The range is clipped at the 1st and 99th percentile per dimension, which
  costs accuracy on the 2% outside and buys it back on the 98% inside.
- **Asymmetric distances.** The query stays float32; only stored vectors are
  quantized, so the query contributes no quantization error of its own.

Measured on 2 000 clustered 384-dimension vectors, 200 queries, recall@10
against exact cosine search (`go test ./internal/vector -run
TestQuantizerRecallCurve -v`):

| Quantizer | recall@10 | bytes/vector | vs float32 |
|---|---|---|---|
| `flat` (float32) | 1.000 | 1536 | 1x |
| `sq` (int8) | 1.000 | 384 | 4x |
| **`sq4` (int4)** | **0.995** | **192** | **8x** |
| `bq` (1 bit) | 0.581 | 48 | 32x |

**When to use:** a collection that no longer fits in RAM at `sq`. The recall
difference is within a rounding error of int8; the storage is half.

### BQ (Binary Quantization)

Extreme compression by converting each float32 dimension to a single bit (1 if positive, 0 if negative). Uses Hamming distance for fast comparison.

| Property | Value |
|----------|-------|
| Accuracy | ~80-90% recall |
| Speed | Very fast (bitwise operations) |
| Memory | ~128x compression (1 bit per dimension vs 32 bits for flat) |
| Build time | O(n) - just sign extraction |
| Parameters | Automatic |

**When to use:** Very large collections where speed and memory are critical and some accuracy loss is acceptable. Best for initial candidate retrieval followed by re-ranking.

### Comparison Table

| Algorithm | Accuracy | Speed | Memory | Best For |
|-----------|----------|-------|--------|----------|
| Flat | Exact | Slow | 1x | < 10K docs |
| HNSW | ~98–100% (measured, v2.18.9) | Fast | ~2x | 10K-1M docs |
| IVF | ~94% | Medium | ~1.1x | 100K+ docs |
| PQ | ~90% | Fast | ~0.03x | 500K+ docs, low memory |
| OPQ | ~93% | Fast | ~0.03x | 500K+ docs, higher recall than PQ |
| SQ | ~95% | Fast | ~0.25x | 50K+ docs, balanced |
| BQ | ~85% | Very fast | ~0.008x | 1M+ docs, speed-first |

### Algorithm Selection Guide

```
Collection size < 10,000?
  → Use Flat (exact results, fast enough)

Collection size 10,000 - 1,000,000?
  → Use HNSW (best speed/accuracy trade-off)

Collection size > 100,000 and memory constrained?
  → Use IVF (good accuracy, moderate memory)

Collection size > 500,000 and very memory constrained?
  → Use PQ (aggressive compression, acceptable accuracy)

Need guaranteed exact results?
  → Always use Flat regardless of size
```

### `auto`: the server chooses (v2.18.9+)

You don't have to pick an index. Send `"algorithm": "auto"` (hybrid search:
`"vectorAlgorithm": "auto"`) and the query planner decides for each query.
It looks at the collection size, the indexes already built, how much of the
collection the filter admits, and the request:

| Input | Decision |
|-------|----------|
| Fewer than 10,000 vectors | `flat` |
| 10,000 to 1,000,000 vectors | `hnsw` |
| More than 1,000,000 vectors | `ivf` |
| Vectors over 256 MB / 1 GB at float32 | `sq` / `sq4`, then `bq`, if built |
| A filter that admits under 5% of the collection | `flat` over the admitted documents only (`prefilter: true`) |
| A filter that admits 5% to a third, where `hnsw` would answer (v2.18.9) | a trained `ivf`, else `flat` over the admitted documents only (`prefilter: true`) |
| Collection configured for quantization or disk-only vectors | its `quantized` index |
| `latencyTargetMs` of 10 or less on 10,000+ vectors | `bq`, `sq4` or `sq`, if built |

- **Nothing is built on the query path.** IVF, PQ, OPQ, SQ and SQ4 must be
  trained first. If the index the table names is not trained yet, the planner
  takes the best index that is: `hnsw`, then a trained `ivf`, and finally
  `flat`. The reason says so.
- **The prefilter costs what the filter admits (v2.18.9).** It looks up the
  admitted documents' vectors and scores those alone. Until v2.18.9 it walked
  the whole collection and dropped what the filter refused, so a 2% filter on
  2M vectors took 79 ms. HNSW filters after its search and takes 3× the
  candidates it is asked for. A filter admitting under a third leaves it short,
  and it then scores the whole collection: 27–46 ms at 200k for filters of 2%
  to 25%. Under `auto`, such a filter goes to a trained `ivf` when `hnsw`
  would answer: 2.4 ms at 10% and 5.2 ms at 25%, where the prefilter took 4.9
  and 9.6 ms. Without a trained `ivf` it goes to the prefilter. Under 5% the
  prefilter is kept even over a trained `ivf`: at 2% `ivf` was 0.2 ms faster
  at 200k vectors, but at 20k it found 0.77 of the exact top 10.
- **Large collections with real embeddings get a quantized index, for
  memory.** At 64 dimensions or more, a few million vectors pass 256 MB of
  float32 and `auto` asks for `sq`, or `sq4` above 1 GB, if trained. On the
  planner benchmark both keep recall@10 at 1.000, at 32 and 128 dimensions.
  Neither is fast: at 200k vectors and 128 dimensions they took 71–73 ms,
  where flat took 7.8 ms and `hnsw` 0.6 ms. Choose them to fit in RAM, not to
  go faster.
- **A filtered index search does not quietly come back short.** An index
  searches first and filters after, so a filter can leave 3 results for
  `topK: 10`. When that happens under `auto`, the planner scans further. IVF
  probes more clusters; other indexes are asked for more candidates. It stops
  at 8× the default. The plan then shows `iterativeScan: true`.
- **No `algorithm` means `flat`, exactly as before.** `auto` is opt-in, on
  the request or in the collection profile (`retrieval.vectorAlgorithm:
  "auto"`). The request wins over the profile.
- **One table.** The search advisor (`GET /v1/search-advisor`) decides from
  the same thresholds. It recommends `vectorAlgorithm: "auto"` in the profile
  when the collection has more than one index built.

Add `"explain": true` to see the plan:

```bash
curl -X POST http://localhost:11023/v1/vector-search \
  -H "Content-Type: application/json" \
  -d '{"collection": "kb", "query": "rotate the signing key", "algorithm": "auto",
       "filterMeta": {"team": ["security"]}, "explain": true}'
```

```json
"plan": {
  "algorithm": "flat",
  "prefilter": true,
  "candidates": 15,
  "reasons": [
    "flat with a prefilter: the filter admits 312 documents of 48210 vectors (0.6%, under 5%), so scanning exactly what it admits beats an index search whose hits it would mostly discard"
  ]
}
```

`candidates` is `topK × oversample`. Cross-collection search plans each
target on its own: the response has `"algorithm": "auto"` and one plan per
target under `plan.collections`. Without `explain` the response has no
`plan`. Agents should send `auto` rather than guess an index.

- **Surfaces.** `algorithm`, `explain` and `latencyTargetMs` work on REST
  (`/v1/vector-search`, `/v1/hybrid-search` as `vectorAlgorithm`,
  `/v1/cross-search`), MCP (`semantic_search`, `hybrid_search`,
  `cross_search`), gRPC and GraphQL `vectorSearch`, where `plan` is a
  `SearchPlan` ([GRAPHQL.md](GRAPHQL.md#vector-search-semantic-search)).

Measured at 2k, 200k and 2M vectors, then at 20k and 200k after the HNSW,
prefilter and threshold fixes (recall@10, p50/p99, what `auto` picked, and
filters admitting 2%, 10% and 25%): see
[Vector query planner](BENCHMARK.md#vector-query-planner-v2189) in
docs/BENCHMARK.md. The bench is `tools/bench/planner`.

### API Examples

```bash
# Flat (exact, default)
curl -X POST http://localhost:11023/v1/vector-search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "kb",
    "query": "how to cancel my subscription?",
    "topK": 5,
    "algorithm": "flat"
  }'

# HNSW (fast approximate)
curl -X POST http://localhost:11023/v1/vector-search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "kb",
    "query": "how to cancel my subscription?",
    "topK": 5,
    "algorithm": "hnsw"
  }'

# IVF (clustered)
curl -X POST http://localhost:11023/v1/vector-search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "kb",
    "query": "how to cancel my subscription?",
    "topK": 5,
    "algorithm": "ivf"
  }'

# PQ (compressed)
curl -X POST http://localhost:11023/v1/vector-search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "kb",
    "query": "how to cancel my subscription?",
    "topK": 5,
    "algorithm": "pq"
  }'
```

### MCP Tool

```json
{
  "tool": "semantic_search",
  "arguments": {
    "collection": "kb",
    "query": "how to cancel subscription",
    "algorithm": "hnsw",
    "top_k": 5
  }
}
```

### Fallback Behavior

If the selected algorithm's index is not yet ready (e.g., HNSW graph still building, IVF/PQ still training), the server automatically falls back to the **flat** algorithm and includes the actual algorithm used in the response.

### Retrieval Modes — Parent, Chunk, Window (v2.11.4+)

Documents are embedded per chunk, and `retrievalMode` controls the granularity
of what a search returns:

| Mode | Returns | Use case |
|------|---------|----------|
| `parent` (default) | One result per document, scored by its best chunk | Document-level search, browsing |
| `chunk` | One result per matching chunk with `chunkIndex` + `chunkText` | Precise passages for LLM prompts (RAG) |
| `window` | Like `chunk`, but the passage includes `windowSize` neighboring chunks per side | Passage plus surrounding context |

```bash
# Get the exact matching passage for an LLM prompt
curl -X POST http://localhost:11023/v1/vector-search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "kb",
    "query": "how do I rotate the TLS certificate?",
    "topK": 3,
    "retrievalMode": "chunk"
  }'
```

Response items in `chunk`/`window` mode carry the matching passage:

```json
{
  "document": { "id": "abc123", "key": "tls-guide", "meta": {"category": ["ops"]} },
  "score": 0.91,
  "rank": 1,
  "chunkIndex": 4,
  "chunkText": "## Certificate rotation\nTo rotate the TLS certificate..."
}
```

The parent document's metadata is always included, so a RAG pipeline can cite
the source document while prompting with only the relevant passage. Passages
are derived from the parent's current content, so they never go stale. The
same options are available on the `semantic_search` MCP tool
(`retrieval_mode`, `window_size`).

### MMR Result Diversification (v2.11.4+)

When a collection contains many similar documents (or chunks), plain top-K
often fills up with near-duplicates. Maximal Marginal Relevance (MMR)
reranking selects results greedily by balancing relevance to the query
against similarity to what has already been selected:

```
mmrScore = λ·relevance − (1−λ)·max(similarity to selected)
```

```bash
curl -X POST http://localhost:11023/v1/vector-search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "kb",
    "query": "deployment strategies",
    "topK": 5,
    "mmr": true,
    "mmrLambda": 0.5
  }'
```

- `mmr: true` enables diversification; candidates are oversampled (3× topK)
  before selection, so diverse results can surface from beyond the raw top-K.
- `mmrLambda` (default `0.5`): `1.0` reproduces the pure relevance order,
  `0.0` maximizes diversity.
- Composes with `retrievalMode` — in `chunk`/`window` modes diversification
  applies across chunks, in `parent` mode across documents.
- Also available on the `semantic_search` MCP tool (`mmr`, `mmr_lambda`).

### Relevance Feedback — Positive and Negative Examples (v2.18.9+)

After a first search you know which results were right and which were not.
Send that back instead of rewriting the query. `positive` and `negative` list
examples. An example is a document key (or ID) from the earlier results, or a
vector:

```bash
curl -X POST http://localhost:11023/v1/vector-search \
  -H "Content-Type: application/json" \
  -d '{"collection": "kb", "query": "rotate the signing key", "topK": 10,
       "positive": ["key-rotation", "jwt-secrets"], "negative": ["tls-certificates"]}'
```

`feedbackMode` chooses how the examples are used:

| Mode | What it does | Cost |
|------|--------------|------|
| `query` (default) | Moves the query vector: q′ = α·q + β·mean(positive) − γ·mean(negative), with α = 1, β = 0.75, γ = 0.25 (Rocchio). Override them with `rocchio: {alpha, beta, gamma}`. | One vector operation; works with every index |
| `rescore` | Searches with the query as given, then adds `feedbackWeight` (default 0.5) × (mean similarity to the positives − mean similarity to the negatives) to each candidate's score. | Reads every candidate's vector; reorders only the `topK × oversample` candidates the index returned |

- **Every vector counts at unit length.** A document example stands for the
  mean of its chunks, so a long document does not outweigh a short one.
- **Hybrid search corrects the vector half only.** The full-text half keeps
  the query's words; words from the positives are not added to it.
- **Cross search looks keys up** in `sourceCollection` first, then in each
  target collection.
- **Refusals.** A key with no embedding, a vector of the wrong dimension, an
  unknown mode and more than 50 examples per side are refused with 422.
- **No `positive` or `negative` means today's search.** Empty lists also count
  as none.
- With `explain: true`, `plan.feedback` shows the correction. It gives the
  mode, the example counts, and either the Rocchio weights and `queryShift`
  (1 − cosine between the query and the corrected query) or the rescore
  `weight` and how many candidates were rescored.
- **Surfaces.** Vector, hybrid and cross search on REST, MCP and gRPC, and
  GraphQL `vectorSearch`, where an example is `{key: "..."}` or
  `{vector: [...]}` and `rocchio` is an input object
  ([GRAPHQL.md](GRAPHQL.md#vector-search-semantic-search)). All of them go
  through one implementation and refuse the same inputs with the same message.

Measured in [BENCHMARK.md](BENCHMARK.md#relevance-feedback-v2189): one round
of feedback raises recall@10 from 0.31 to 0.49–0.50 on a corpus built so
that the query is ambiguous.

### Query Decomposition — Questions with Several Needs (v2.18.10+)

"How do I set up a read replica and how does backup work" asks for two
pages. One query vector for the whole question lands between the two topics,
so a page vaguely near both can outrank the two pages that each answer one
half. `queryDecomposition: "auto"` searches such a question part by part:

```bash
curl -X POST http://localhost:11023/v1/vector-search \
  -H "Content-Type: application/json" \
  -d '{"collection": "kb", "query": "set up a read replica and take a backup of the database",
       "topK": 10, "queryDecomposition": "auto", "explain": true}'
```

- **The split.** The query is cut at sentence punctuation (`. , ; : ? !`)
  and at the word "and" between clauses. A one-word fragment joins the part
  before it, so "latitude and longitude" stays whole. At most 4 parts;
  clauses past the fourth join the fourth. No language model runs in the
  server.
- **The search.** The whole query and each part are searched. A document
  keeps its best score over all of them (max fusion). Ties go to the lower
  ID, and the fused list is cut back to the `topK × oversample` candidates
  one search returns.
- **A query with nothing to split** is searched as with `off`: one embedding
  call, the same results.
- **Cost.** One embedding call and one index search per part, on top of the
  whole query's. Like the whole query's, these calls are not counted against
  a collection's embedding budget, which meters document embeddings only.
- **`off` (the default)** is today's search, byte for byte.
- **Hybrid search splits the vector half only.** The full-text half already
  matches the words of every part.
- **A query sent as `queryVector`** has no text to split and is searched
  whole; the plan says so.
- With `explain: true`, `plan.decomposition` gives `parts` (1 = not split)
  and the part `texts`.
- **Relevance feedback** in `query` mode corrects every part the way it
  corrects the whole query.
- **Surfaces.** Vector and hybrid search on REST, MCP (`query_decomposition`),
  gRPC (`query_decomposition`), GraphQL `vectorSearch` and `POST /v1/eval`
  configs. All of them go through one implementation, and an unknown mode is
  refused with 422 (gRPC `InvalidArgument`).

**Why max, why no similarity gate.** Measured in
[BENCHMARK.md](BENCHMARK.md#query-decomposition-v21810) on 20 questions and
190 two-topic questions, nomic-embed-text:
- On the two-topic questions vector search gains +0.065 nDCG@10
  (CI [+0.046, +0.085]) and hybrid +0.047 (CI [+0.030, +0.066]). On ordinary
  questions the change is −0.001 (CI [−0.002, 0.000]), within noise.
- Fusing by RRF instead of max loses 0.25 nDCG on the two-topic questions.
  RRF rewards a page that is mediocre for both parts.
- Splitting only when the parts' vectors diverge was tried at cosine
  thresholds of 0.45–0.55. It removed up to two thirds of the gain and saved
  nothing on ordinary questions. Keeping the whole query as one of the
  searches is what protects them.

### Summaries — a Second Representation (v2.18.9+)

A question like "what are the main risks here" matches the language of a
summary, not of any single chunk. A document can carry a summary in its
metadata key `summary`. **The client writes the summary**: by hand, with
`PATCH /v1/update`, from an automation that calls its own LLM, or in an
ingest batch. MDDB never generates one and has no LLM in the server.

```bash
curl -X POST http://localhost:11023/v1/add \
  -H "Content-Type: application/json" \
  -d '{"collection": "kb", "key": "plant-log", "lang": "en",
       "meta": {"summary": ["Main hazards: leaks, pressure and the maintenance backlog."]},
       "contentMd": "Pump seal on line four was replaced..."}'
```

The summary is embedded as one vector of its own and indexed as the BM25F
field `meta.summary`. The collection profile decides what it does on a
search:

```bash
curl -X PUT http://localhost:11023/v1/collection-config \
  -d '{"collection": "kb", "retrieval": {"summaryMode": "blend", "summaryWeight": 2.0}}'
```

| `retrieval.summaryMode` | What a search does |
|---|---|
| `off` (default) | Ignores summaries. Results are byte-for-byte what they were before v2.18.9. |
| `blend` | A document scores the better of its best chunk and its summary. A document that only its summary matched comes back with its best chunk for the query. |
| `expand` | Like `blend`, and a summary hit also brings the document's best *n* chunks, ranked against the query, at the summary's score. With `format: "passages"` each chunk is a passage, and in `chunk` mode each chunk is a result. *n* is 3, or fewer when `contextTokenBudget` cannot hold *n* chunks for each of `topK` results (never fewer than 1). |

- **Every vector surface does the same thing.** Vector search (REST, MCP
  `semantic_search`, gRPC `VectorSearch`, GraphQL), the vector half of
  hybrid search and cross-collection search (`/v1/cross-search`, MCP
  `cross_search`, gRPC `CrossSearch`; vector and hybrid) share one
  implementation. With `explain: true`, the plan says how many documents
  their summaries matched.
- **Cross-collection search, per target.** Each target collection applies its
  own `summaryMode` and `summaryWeight` to its own hits. A target with
  summaries `off` contributes exactly what it did before, so a search over
  targets that are all `off` returns the same bytes as before v2.18.9. Under
  `blend`, a document only its summary matched joins the merged results.
  Under `expand`, with `format: "passages"`, its summary hit brings that
  document's best chunks, one passage each. With `explain: true` and a named
  algorithm, the one plan gets the summary line prefixed by the target
  (`"kb: summaryMode blend: …"`); with `algorithm: "auto"`, the line is in
  that target's plan under `collections`.
- **The keyword side.** While `summaryMode` is not `off`, BM25F (`bm25f`, in
  full-text and hybrid search) adds `meta.summary` with `summaryWeight`
  (default 2.0, range 0–10) to the field weights. A request that names
  `meta.summary` in `fieldWeights` keeps its own weight; `0` turns it off for
  that query.
- **Embedding.** The worker embeds the summary after the chunks, and only
  when its text changed. Like a chunk, the summary is reused by its hash
  (RAG-003). It spends the collection's daily embedding budget (GO-043). When
  the budget is spent, the job waits in the queue; it does not fail. Changing
  only the summary re-embeds the summary and re-indexes the document for
  full-text search. Removing the summary, deleting the document or deleting
  the collection removes the vector. `POST /v1/vector-reindex` with `force`
  re-embeds summaries too.
- **Kept apart from chunks.** Summary vectors are stored in the `vectors`
  bucket under their own key prefix (`vsum|`). They are not chunks. They do
  not count in `embedded_documents`, `total_chunks` or `quota.maxVectors`, and
  no `algorithm` searches them. A disk-only collection keeps them quantized
  in RAM and rescores the candidates from disk, as it does for its chunks.
  They replicate, back up and move to per-collection files with the rest of
  the collection.
- **No summaries cost nothing.** For a collection without them, a search adds
  one map lookup and a write adds none.

The search advisor reports `summaryDocuments` and `summaryRatio`, the share of
sampled documents that have a summary. When at least half have one, it
recommends `summaryMode: "blend"`. To measure whether `blend` or `expand`
helps on your own queries, give `run_eval` (`POST /v1/eval`) configurations
that differ only in `summaryMode`:

```bash
curl -X POST http://localhost:11023/v1/eval -d '{"collection": "kb", "configs": [
  {"searchType": "vector"},
  {"searchType": "vector", "summaryMode": "blend"},
  {"searchType": "vector", "summaryMode": "expand"}]}'
```

### Images — Alt Text, Captions and Pictures (v2.18.9+)

Diagrams, screenshots and tables saved as pictures carry content a text
index never sees. MDDB keeps a document's images as **image documents** of
their own and returns them with the hits they belong to. The server runs no
image model: a picture is found by its alt text and caption always, and by
what it shows when the embedding provider embeds images.

**Storing images.** Extraction is asked for, so a file written the way it
always was stores what it always did:

```bash
# A Markdown file and the images its links name, in one upload
curl -X POST http://localhost:11023/v1/upload \
  -F collection=docs -F lang=en -F extractImages=true \
  -F "files[]=@manual.md" -F "files[]=@img/seal.png" -F "files[]=@img/wiring.jpg"

# A PDF through ingest
curl -X POST http://localhost:11023/v1/ingest -d '{"collection": "docs",
  "options": {"extractImages": true},
  "documents": [{"key": "manual-pdf", "lang": "en", "format": "pdf", "contentBase64": "JVBERi0..."}]}'
```

- **Markdown**: every `![alt](target "title")` outside code fences. The
  target is a data URI, a relative path to a file sent with the document
  (`files[]` beside it on upload, `images: [{path, contentBase64}]` on
  ingest, `mddb-cli autoindex --images` reads them from disk), or, only with
  `fetchRemoteImages: true`, an http(s) URL fetched through the SSRF guard
  that `import-url` uses. Absolute paths, paths leaving the document's
  directory and other schemes are not read.
- **PDF**: the image XObjects. JPEG (`DCTDecode`) streams are kept as they
  are; 8-bit RGB or gray `FlateDecode` pictures (`DeviceRGB`, `DeviceGray`,
  `CalRGB`, `CalGray`, one- or three-component `ICCBased`; PNG predictors
  undone) are written as PNG. Skipped, and counted in `meta.imageWarnings`
  of the PDF: other filters (JPX, CCITT, JBIG2, filter chains), CMYK in
  Flate, Indexed and other colour spaces, bit depths other than 8, stencil
  and soft masks, inline images and pictures drawn only inside a form
  XObject (those are kept without a page).
- **Limits**: JPEG, PNG, GIF and WebP only, at most 2 MB each, at least
  16×16 pixels (smaller ones are bullets and rules), at most 64 per document.
  What was skipped and why is in the parent's `meta.imageWarnings`. The
  pictures inside other formats (DOCX, ODT, PPTX, EPUB, an email's image
  attachments) are not extracted.
- **A collection of type `images`** extracts by default, and an image file
  uploaded or ingested on its own becomes an image document, its alt text,
  caption and parent taken from the request's `meta.altText`, `meta.caption`
  and `meta.parent` (with a parent, it is returned with that document's
  hits). Elsewhere an image file is still not a document format.

Each image is stored under `KEY/images/NAME` (`seal.png`,
`page-2-image-1.png`) with `meta.kind: image`, `meta.parent` (the document's
key), `meta.altText`, `meta.caption` (the link's title, an emphasised or
"Figure …"/"Fig. …" line under the image, or a PDF page's "Figure" line, in
order), `meta.page` (PDF), `meta.position`, `meta.mimeType`, `meta.width`,
`meta.height` and `meta.size`. Its text is a Markdown image link carrying
the alt text, plus the caption, so
**alt text and captions are in full-text search** (and in text embeddings)
like any other words. The bytes are kept apart from the document, in the
collection's `images` bucket: out of full-text search, chunking, the
metadata index, revisions and every response that returns a document. They
move with the collection in the per-collection layout, are encrypted with an
encrypted collection, replicate, are in every backup, and go when the image
document or its collection goes. `GET /v1/image?collection=&key=&lang=`
serves them with the type the bytes have and `nosniff`.

**Images with search results.** `includeImages: true` on full-text, vector,
hybrid and cross-collection search (REST, MCP, gRPC, GraphQL; results and
passages alike) attaches each hit's images:

```json
{"document": {"key": "manual", ...}, "score": 0.42,
 "images": [{"key": "manual/images/seal.png", "lang": "en", "altText": "Exploded view of the pump seal",
             "caption": "Figure 1: the seal", "mimeType": "image/png", "width": 40, "height": 30,
             "parent": "manual", "url": "/v1/image?collection=docs&key=manual%2Fimages%2Fseal.png&lang=en"}]}
```

It is one metadata-index lookup for all the hits of a collection. Without
`includeImages` nothing is looked up and nothing is added: the response is
byte-for-byte what it was before v2.18.9 (a golden test holds every surface
to that).

**Searching by picture.** With a provider that embeds images (Voyage's
multimodal models, Cohere Embed v3 and newer — see
[EMBEDDING_PROVIDERS.md](EMBEDDING_PROVIDERS.md#images-v2189)), the embedding
worker embeds each image document's picture into the image space, after its
text. Then:

- `includeImages` on vector and hybrid search also embeds the query into the
  image space and returns the nearest images as `imageHits`, each with its
  `score` and `parent`. They are kept beside `results`, never mixed in: the
  two spaces' scores do not compare. This is how a text query finds a
  picture that has no alt text at all. With `explain`, the plan says how
  many image vectors were searched and with which model, or why none were.
- `queryImage` on vector search (REST, MCP `query_image`, gRPC
  `query_image`, GraphQL `queryImage`) searches by an image: base64, a data
  URI or an http(s) URL fetched through the SSRF guard. The results are the
  image documents nearest it, `algorithm: "image"`, each naming its parent.
  Without an image-capable provider it is refused with 400
  (gRPC `FAILED_PRECONDITION`); an unreadable image is 400 too.

Image embedding spends the collection's daily embedding budget (GO-043): one
call, and tokens counted as Voyage counts them (one per 560 pixels). When the
budget is spent the job waits in the queue. An unchanged image is not
embedded again: its vector record carries the hash of the bytes and the
model. Image vectors live in the `vectors` bucket under their own prefix
(`vimg|`) and in an index of their own; no chunk algorithm, count or quota
(`embedded_documents`, `quota.maxVectors`) sees them. A provider that embeds
no images (OpenAI, Ollama, lexical) skips them, and `indexing-status` says
so:

```json
"images": {"documents": 6, "embedded": 0,
           "note": "embedding provider \"lexical-ngram-v1\" does not embed images: images are skipped and found by their alt text and captions only"}
```

`POST /v1/vector-reindex` with `force` re-embeds images too.

## Passages — What an Agent Gets (v2.18.8+)

A document answers "which file"; an agent editing code needs "which lines,
inside which definition, and what they say". `format: "passages"` returns that
from `/v1/fts`, `/v1/vector-search`, `/v1/hybrid-search` and
`/v1/cross-search` (and the matching MCP tools and GraphQL queries) instead of
documents:

```bash
curl -X POST http://localhost:11023/v1/fts -H "Content-Type: application/json" \
  -d '{"collection": "theme", "query": "background", "limit": 1, "format": "passages"}'
```

```json
{"passages": [{
  "collection": "theme", "key": "css/style.css", "path": "css/style.css",
  "lines": {"start": 152, "end": 155}, "symbol": ".hero-banner",
  "content": ".hero-banner {\n  background: url(hero.png);\n  padding: 2rem;\n}",
  "score": 0.91, "source": "fts", "tokens": 16
}], "total": 1}
```

That is the whole answer to "where is the hero background set" in 292 bytes,
against 4 KB for the stylesheet it came from.

| Search | Where the passage is cut |
|---|---|
| `retrievalMode` `chunk` / `window` | the chunk or window, as those modes already cut it |
| vector, `parent` mode | the document's best-scoring chunk |
| everything else | around the first line the query's terms match, `passageLines` (default 10) on each side; a hit inside a definition no longer than that window returns the definition |

- `symbol` is the innermost definition holding the hit (code documents; see
  [Code Documents](API.md#one-symbol-everywhere-it-matters-v2188) for the
  languages).
- `tokens` uses the same estimate as the context budget, and the budget counts
  passages in this format, not the documents they came from.
- The document body is never included. Without `format`, or with
  `"results"`, responses are exactly as before.
- A collection's retrieval profile may set `format: "passages"` as its
  default; the search advisor recommends it for code collections.

## Searching What You Just Wrote (v2.18.8+)

A write returns once the document, its full text and its metadata are
committed, so a full-text search finds it at once. Its embedding is computed
afterwards by the embedding worker, so a vector search finds it only when that
job finishes — usually well under a second, longer under a bulk import or a
slow provider.

| Need | Use |
|---|---|
| Search right after a write and include the new document | `waitForIndex: 5000, waitForKeys: ["<key>"]` on `/v1/vector-search`, `/v1/hybrid-search` or `/v1/cross-search` |
| Never miss a document, even one still queued | `readLevel: "indexedPlusPending"` on `/v1/vector-search` |
| Know how far behind a collection is | `GET /v1/indexing-status?collection=<c>` |
| Be told when it has caught up | SSE event `collection.indexed` |

- `waitForIndex` waits at most 30 s. Running out of time is not an error: the
  search runs and `indexing.timedOut` says the newest documents may be
  missing.
- `readLevel: "indexedPlusPending"` appends documents whose embedding is still
  queued and whose text matches the query, after the ranked results, marked
  `pendingEmbedding: true`. They have no vector score.
- Hybrid search has always found queued documents through its full-text half;
  since v2.18.8 such results are marked `pendingEmbedding: true` too.
- `failedEmbeddings` counts documents left without vectors by a provider
  failure or a full queue. They stay unsearchable by meaning until
  `/v1/vector-reindex`.

## Grouping Results (v2.18.8+)

Parent mode already keeps one hit per document. `groupBy` is the general
form: keep the `perGroup` best results for each value of a key, then cut to
`topK`. An agent reading code wants one passage per file; a reviewer, two
per author; a cross-collection search, one per collection.

```bash
curl -X POST http://localhost:11023/v1/vector-search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "repo",
    "query": "retry with backoff",
    "retrievalMode": "chunk",
    "topK": 10,
    "groupBy": {"key": "path", "perGroup": 2}
  }'
```

| `key` | Groups by | Typical use |
|---|---|---|
| a metadata key | its first value | `path`, `author`, `language` |
| `document` | the document a chunk belongs to | several chunks of one document, with `retrievalMode: chunk` |
| `collection` | the collection | `/v1/cross-search` |

- Accepted on `/v1/fts`, `/v1/vector-search`, `/v1/hybrid-search` and
  `/v1/cross-search`, on the MCP tools as `group_by` (`{"key": "path",
  "per_group": 2}`) and on GraphQL `vectorSearch`.
- Each result carries `group` and `groupRank` (its place in the group), so
  a caller can tell what the grouping held back.
- Documents without the metadata key are never merged with each other.
- Order: candidates (`topK × oversample`) → parent dedupe → `groupBy` → MMR
  → `topK`. MMR diversifies among what grouping kept.
- `perGroup` is 1–100 and defaults to 1. A missing `key` is a 422.
- A collection's retrieval profile may set `groupBy` as its default; a
  request's own `groupBy` wins. `/v1/cross-search` has no single profile and
  uses only the request.
- Without `groupBy` responses are exactly as before.

## Oversampling (v2.12.0+)

Every search that post-processes its results — deduplicating chunks of one
document, merging two rankings, rescoring quantized candidates — asks the index
for more than it will return, then trims. That multiplier was the literal
`topK * 3` in five places, so the recall/latency trade-off was fixed at whatever
those constants said.

`oversample` exposes it: how many candidates to fetch per requested result.

```bash
# A navigational lookup: cheapest path to the obvious answer.
curl -X POST "$MDDB/v1/vector-search" -d '{"collection":"docs","query":"pricing","oversample":1}'

# A question worth getting right: cast a wider net before deduplication.
curl -X POST "$MDDB/v1/vector-search" -d '{"collection":"docs","query":"why did the migration fail","oversample":10}'
```

Range 1.0–10.0. Omitted, it uses the collection's `retrieval.oversample`
profile, then MDDB's default of 3.0 — the constant the code has always used, so
an unset parameter reproduces earlier results exactly. Out of range is a `422`
(gRPC `InvalidArgument`), never a silent clamp: quietly halving a caller's
recall setting is worse than telling them it was impossible.

### What it actually buys, measured

2,500 documents of 5 chunks each, `topK=10`, flat index:

| `oversample` | Candidates fetched | Distinct documents returned | Latency |
|---:|---:|---:|---:|
| 1× | 20 | 5 | 1.75 ms |
| 3× (default) | 30 | 9 | 1.74 ms |
| 10× | 100 | 10 | 1.77 ms |

Two things worth reading twice.

**At 1× you get half the documents you asked for.** With chunk-level indexing,
several chunks of the same document occupy the top of the ranking, and
deduplication collapses them into one result. Without spare candidates there is
nothing left to fill the gap — `topK=10` returns 5 documents. This is why the
default is 3 and not 1.

**On a flat index the latency cost is about 1%.** A flat search scans every
vector regardless; `topK` only sizes the result heap. So raising `oversample`
there is nearly free, and there is little reason to leave it low. The trade-off
becomes real on approximate indexes — HNSW, IVF, and the quantized ones — where
the candidate count drives graph traversal and rescoring work. Measure on the
index you actually run.

### Per-collection default

A collection whose documents are long and heavily chunked wants a higher factor
than one holding short notes. Set it once instead of on every query:

```bash
curl -X PUT "$MDDB/v1/collection-config" -d '{
  "collection": "handbook",
  "retrieval": { "oversample": 6 }
}'
```

Precedence is the same as every other retrieval setting: request parameter >
collection profile > default.

### What is not exposed

HNSW's `efSearch` stays a build-time parameter. It lives on the graph object
shared by every concurrent search, so setting it per query would have one
request silently change another's beam width — a data race producing wrong
results rather than a crash. Raising `oversample` moves HNSW recall through the
requested neighbour count instead, which is per-call and safe.

## Hybrid Search (v2.6.5+)

Hybrid search combines FTS (keyword) and vector (semantic) search into a single query, producing results ranked by a fused score. This gives you the best of both worlds: exact keyword matching plus semantic understanding.

### How It Works

1. **FTS search** — runs BM25 or BM25F against the inverted index
2. **Vector search** — embeds the query and searches the vector index
3. **Fusion** — merges results using the selected strategy
4. **Return** — deduplicated results with combined scores

### Alpha Blending (default)

Weighted combination of normalized FTS and vector scores.

**Formula:**
```
combined = (1 - alpha) * normalizedFTS + alpha * vectorScore
```

- `alpha = 0.0` → pure keyword (FTS only)
- `alpha = 0.5` → equal weight (default)
- `alpha = 1.0` → pure semantic (vector only)

FTS scores are min-max normalized to 0-1 range. Vector scores are already 0-1 (cosine similarity).

### RRF (Reciprocal Rank Fusion)

Rank-based fusion that doesn't depend on score magnitudes. Works well when FTS and vector scores are not directly comparable.

**Formula:**
```
score = 1/(k + rank_fts) + 1/(k + rank_vector)
```

- `k` (default 60) controls how much top ranks dominate. Higher k = more equal weighting across ranks.
- Documents appearing in both result sets get both rank contributions.
- Documents appearing in only one set get a single rank contribution.

### Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `strategy` | `"alpha"` | `"alpha"` or `"rrf"` |
| `alpha` | `0.5` | Weight for alpha blending (0-1) |
| `rrfK` | `60` | RRF k parameter |
| `algorithm` | `"bm25"` | FTS algorithm: `"bm25"`, `"bm25f"`, `"pmisparse"` |
| `vectorAlgorithm` | `"flat"` | Vector algorithm: `"flat"`, `"hnsw"`, `"ivf"`, `"pq"`, `"opq"`, `"sq"`, `"sq4"`, `"bq"` |
| `topK` | `10` | Number of results to return |
| `fuzzy` | `0` | Typo tolerance for FTS (0, 1, 2) |
| `threshold` | `0.0` | Minimum vector similarity |
| `filterMeta` | — | Metadata filters (applied to both FTS and vector) |
| `includeContent` | `false` | Include document content in results |
| `fieldWeights` | — | BM25F field weights |
| `disableStem` | `false` | Disable stemming for FTS |
| `disableSynonyms` | `false` | Disable synonym expansion for FTS |
| `boost` | — | Per-query score multiplier keyed by `"metaKey:metaValue"` |
| `geo` | — | Spatial pre-filter: `{lat, lng, radiusMeters}` |
| `sort` | `"combined"` | Result ordering: `combined` (default, by fused score) or `distance` (by `distanceMeters` ascending — requires `geo`) |

### Sort by Distance (v2.9.13+)

When the request carries a `geo` filter, `sort: "distance"` re-orders the merged result set by ascending distance from the reference point instead of by fused score. Useful for "nearest matching venue" queries where proximity matters more than keyword relevance.

```bash
curl -X POST http://localhost:11023/v1/hybrid-search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "venues",
    "query": "coffee wifi",
    "topK": 10,
    "geo": {"lat": 52.52, "lng": 13.405, "radiusMeters": 3000},
    "sort": "distance"
  }'
```

The server returns an error if `sort: "distance"` is used without a `geo` filter, because every item would otherwise carry `distanceMeters=0` and the ordering would be arbitrary. gRPC callers must stay on `combined` — the gRPC `HybridSearchRequest` has no geo field; use the HTTP endpoint for distance sorting.

### API Examples

```bash
# Alpha blending (default, equal weight)
curl -X POST http://localhost:11023/v1/hybrid-search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "kb",
    "query": "how to deploy with Docker",
    "topK": 10,
    "strategy": "alpha",
    "alpha": 0.5
  }'

# Keyword-heavy search (alpha=0.2)
curl -X POST http://localhost:11023/v1/hybrid-search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "kb",
    "query": "nginx configuration reverse proxy",
    "topK": 10,
    "strategy": "alpha",
    "alpha": 0.2,
    "algorithm": "bm25f",
    "fieldWeights": {"meta.title": 5.0, "content": 1.0}
  }'

# RRF fusion
curl -X POST http://localhost:11023/v1/hybrid-search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "kb",
    "query": "cancel subscription refund policy",
    "topK": 10,
    "strategy": "rrf",
    "rrfK": 60,
    "fuzzy": 1
  }'

# With metadata filtering
curl -X POST http://localhost:11023/v1/hybrid-search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "kb",
    "query": "kubernetes pod scaling",
    "topK": 5,
    "filterMeta": {"category": ["devops"], "status": ["published"]}
  }'
```

### MCP Tool

```json
{
  "tool": "hybrid_search",
  "arguments": {
    "collection": "kb",
    "query": "how to deploy with Docker",
    "top_k": 10,
    "strategy": "alpha",
    "alpha": 0.5,
    "algorithm": "bm25"
  }
}
```

### Response Format

```json
{
  "results": [
    {
      "document": { "id": "...", "key": "deploy-docker", "lang": "en_US", "meta": {...} },
      "combinedScore": 0.78,
      "ftsScore": 0.65,
      "vectorScore": 0.91,
      "matchedTerms": ["deploy", "docker"],
      "rank": 1
    }
  ],
  "total": 5,
  "strategy": "alpha",
  "alpha": 0.5,
  "ftsAlgorithm": "bm25",
  "vectorAlgorithm": "flat"
}
```

## Measuring Instead of Guessing (v2.18.8+)

Every recommendation in this guide is a starting point. Which configuration
is best for a collection depends on its documents and on the questions people
ask, and only those questions can settle it. An **evaluation set** holds them:
queries with the keys of the documents that should answer them. `POST
/v1/eval` scores search configurations on the set: recall@k, nDCG@k, MRR,
latency and returned tokens. No model judges anything. Relevance is what the
set says it is.

```bash
mddb-cli eval docs --queries tools/bench/evalsets/docs.json \
  --config fts/bm25 --config hybrid --config vector
```

```
20 queries, k=10

config                   recall     ndcg      mrr    p50 ms    p99 ms   tokens
fts/bm25                  0.825    0.739    0.723       0.9       1.9  1580031
vector                    0.875    0.763    0.750      20.1      44.2  1723243
hybrid                    0.875    0.783    0.765      10.7      17.3  1790501
```

That is the MDDB documentation measured with its own 20-question set (see
[BENCHMARK.md](BENCHMARK.md#retrieval-quality-on-the-mddb-docs-v2188)).

- **Store a set** on the collection with `POST /v1/eval-queries` or the MCP
  tool `add_eval_query`. Each query is
  `{id, query, expectedKeys, grades}`, where grades run from 0 to 3 and an
  ungraded expected key counts 1.
- **The advisor uses it.** With a stored set, `search_advisor` runs it and
  recommends the configuration that scored best. Its first reason gives the
  numbers, and `measured` holds the run.
- **Gate CI on it.** `mddb-cli eval <collection> --fail-below
  recall@10=0.8,ndcg=0.6` exits non-zero when any configuration scores under
  a threshold, or fails to run.

Twenty to fifty real questions are enough to tell configurations apart.
Collect them from what people actually asked, not from document titles.
Queries written from a title favour keyword search.

## Which algorithm should I use? (v2.12.0+)

MDDB offers eight vector algorithms, four ranking algorithms, three fusion
strategies and four retrieval modes. That is a lot of menu and no basis for
choosing — especially for an agent connecting over MCP, which sees the names
and nothing else.

Ask the server. It can measure the collection:

```bash
curl -s "$MDDB/v1/search-advisor?collection=theme"
```

```json
{
  "profile": {
    "documents": 4,
    "medianWords": 7,
    "termsPerDocument": 5.0,
    "codeDocuments": 4
  },
  "searchType": "fts",
  "ftsAlgorithm": "bm25",
  "retrievalMode": "graph",
  "topK": 20,
  "reasons": [
    "No embedding provider is configured and no document has a vector, so keyword search is the only search available.",
    "bm25 for ranking: this is code, where queries are identifiers and exact term matching matters more than query expansion.",
    "retrievalMode graph: this is code, so the connection graph reaches the stylesheet a matching script touches — a document no score would have found."
  ],
  "retrievalProfile": { "defaultSearchType": "fts", "topK": 20, "retrievalMode": "graph" }
}
```

Three collections on the same server, three different answers:

| Collection | Shape | Recommendation |
|---|---|---|
| A theme (CSS/JS/HTML) | 4 documents, 7 words median, all code | `bm25`, `retrievalMode: graph` |
| Manuals | 120 documents, 902 words median, 0.18 new terms each | `pmisparse`, `chunk`, topK 5 |
| Status notes | 200 documents, 8 words median | `bm25`, `parent`, topK 20 |

**What it measures, and why each thing matters:**

| Measurement | Decides |
|---|---|
| Embedded documents vs total | Whether vector search is possible at all, and whether a partial reindex is silently hiding documents |
| Median document length | `parent` vs `chunk`, and how many results fit a context window |
| Terms per document | Whether keyword ranking can tell documents apart, or needs query expansion |
| Documents carrying symbols | Whether the connection graph reaches documents a score cannot |
| Vector bytes at float32 | Whether a quantized index is worth the recall it costs |

**Terms per document, not type-token ratio.** Type-token ratio pooled over a
corpus falls towards zero as the corpus grows — vocabulary saturates while
tokens accumulate. The same text measured over 100 documents and over 100 000
gives wildly different numbers, and every large collection would look
repetitive. Vocabulary divided by document count is stable.

**Every recommendation carries its reasons.** "Use bm25" is an instruction;
"bm25, because a median document of 8 words is short and bm25's length
normalisation is what stops a one-line note outranking a real answer" is
something you can disagree with. Disagreeing with it is a legitimate outcome —
this measures the corpus, not your queries.

### Storing the answer

`retrievalProfile` is the advice as a per-collection profile (RAG-001). Store
it and every client inherits it without being told:

```bash
curl -s "$MDDB/v1/search-advisor?collection=theme&apply=true"
```

`apply` needs write permission, and merges into the existing configuration
rather than replacing it — a collection's response prompt and encryption flag
have nothing to do with retrieval and must survive.

### For agents

The same thing is the `search_advisor` MCP tool, annotated read-only. An agent
should call it once per collection before its first search, rather than
guessing from algorithm names or defaulting forever.

## Graph retrieval (v2.12.0+)

`parent`, `chunk` and `window` change the *shape* of a result. `graph` changes
which documents are *reached*.

A query about a checkout bug matches `checkout.js`. The stylesheet whose
selector it manipulates and the template that loads it match nothing — and an
agent has to notice the gap and ask again, two or three round trips to assemble
what one traversal already knows.

```bash
curl -s -X POST "$MDDB/v1/vector-search" -d '{
  "collection": "theme",
  "query": "cart total not updating",
  "retrievalMode": "graph",
  "graphExpand": { "graphDepth": 1, "graphDecay": 0.5 }
}'
```

Neighbours come back after the direct matches, each with the edge that
justifies it:

```json
"graphExpansions": [
  { "key": "css/site.css", "fromKey": "js/checkout.js",
    "symbol": ".cart-total", "kind": "uses-selector", "depth": 1, "score": 0.45 }
]
```

Without `fromKey` and `symbol` a caller sees a document that matched nothing
and cannot tell whether the search is working.

| Parameter | Default | Notes |
|---|---|---|
| `graphDepth` | 1 | Clamped to 3, for the same reason `/v1/code-graph` clamps it |
| `graphDecay` | 0.5 | Fraction of the source's score a neighbour inherits per hop |
| `graphMaxNeighbours` | 10 | Stops a hub document flooding the answer |
| `graphDirection` | `both` | `in` = what depends on this, `out` = what it depends on |
| `graphKinds` | — | (v2.18.8) Expand by symbol relation instead: `callers`, `callees`, `tests`, `siblings` (code in the same directory). Each expansion reports its `kind`. Unknown values are a 422 |

With `graphKinds` the question changes from "what is connected" to "what do I
need next to change this": `["tests", "callers"]` adds the tests that call a
matching function and the code that calls it. Without it expansion behaves as
before.

Neighbours are appended rather than merged into the ranking: a document that
matched the query and one merely adjacent to a match are different kinds of
answer, and sorting them together hides which is which.

A collection with no edges — most prose — expands to nothing, which is the
right answer rather than an error.

### Weighted (multi-signal fusion, v2.12.0+)

`alpha` and `rrf` both answer one question: how well does this document match
the query. There are cheap signals they cannot see, and the one that matters
most is duplication.

If a runbook was forked per environment, the four copies score almost
identically — and a vector score makes it worse, because near-copies have
near-identical embeddings, so similarity *rewards* the duplication. An agent
reading the top four results reads the same document four times.

`weighted` takes alpha's blend and adjusts it:

```bash
curl -s -X POST "$MDDB/v1/hybrid-search" -d '{
  "collection": "runbooks",
  "query": "restart the service",
  "strategy": "weighted",
  "alpha": 0.5,
  "signals": {
    "diversity": 0.8,
    "proximity": 0.1
  }
}'
```

| Signal | What it does | Suggested |
|---|---|---|
| `diversity` | Penalises a near-copy of a higher-ranked result, by **text overlap** (MinHash), not embedding distance | 0.5–1.0 |
| `proximity` | Rewards a result sharing a directory with a higher-ranked one | 0.05–0.15 |
| `freshness` | Rewards recently updated documents, exponential decay | 0.05–0.2 |

**A weight is a fraction of the result's own score.** `proximity: 0.1` means
"up to 10% better". That keeps the signals scale-free, and makes large weights
genuinely powerful — at 1.0 a proximity bonus can carry a result past one
scoring half again as much. Weights are clamped to [0, 1].

Measured on a corpus with one document forked into four near-copies plus three
distinct answers (`go test -run TestDiversitySignalImprovesTopKCoverage -v`):

| | Distinct documents in the top 4 |
|---|---|
| Without the signal | **1** |
| `diversity: 0.8` | **4** |

The response carries a `signalBreakdown` in the same order as the results, so
what each signal contributed is visible rather than inferred:

```json
{ "base": 0.94, "diversity": -0.71, "final": 0.23 }
```

Freshness is off by default and should stay off for reference material: an API
specification does not become less true with age.

**Nothing changes unless you ask.** `alpha` and `rrf` are untouched, and a
`weighted` request with no signals behaves exactly like `alpha`.

### Strategy Selection Guide

```
Need precise keyword matching + semantic understanding?
  → Use Alpha Blending with alpha=0.5 (balanced)

Queries are specific terms (error codes, product names)?
  → Use Alpha Blending with alpha=0.2 (keyword-heavy)

Queries are natural language questions?
  → Use Alpha Blending with alpha=0.8 (semantic-heavy)

FTS and vector score ranges differ significantly?
  → Use RRF (rank-based, ignores score magnitudes)

Top results keep repeating the same document?
  → Use Weighted with diversity=0.8 (demotes near-copies by text overlap)

Not sure?
  → Start with Alpha Blending at 0.5, adjust based on results
```

## Metadata Search

Metadata search uses BoltDB prefix indices for exact matching on document metadata tags. No algorithm selection is needed - it always uses the built-in index.

### Pagination

Use `offset` and `limit` for pagination. The response includes an `X-Total-Count` header with the total number of matching documents (before pagination).

```bash
# First page
curl -v -X POST http://localhost:11023/v1/search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "blog",
    "filterMeta": {"category": ["tutorial"], "status": ["published"]},
    "sort": "updatedAt",
    "limit": 10,
    "offset": 0
  }'
# Response header: X-Total-Count: 234

# Second page
curl -X POST http://localhost:11023/v1/search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "blog",
    "filterMeta": {"category": ["tutorial"]},
    "sort": "updatedAt",
    "limit": 10,
    "offset": 10
  }'
```

**Filter logic:** AND between different metadata keys, OR between values of the same key.

## Combining Search Methods

For best results, combine search methods:

1. **Hybrid Search** (v2.6.5+): Use `/v1/hybrid-search` for a single query that combines FTS + vector search with automatic score fusion. Best for general-purpose search where you want both keyword precision and semantic recall.
2. **Vector + Metadata**: Use `filterMeta` in vector search to narrow semantic results by category
3. **FTS + Metadata** (v2.6.5+): Use `filterMeta` in FTS to scope keyword search to specific metadata values
4. **FTS for keywords, Vector for meaning**: Use FTS when users search for specific terms, vector when queries are natural language questions
5. **BM25F for structured docs**: Use BM25F when documents have meaningful titles and tags — matches in titles will rank higher than body-only matches

## Search Stats (v2.7.0+)

All search endpoints (`/v1/fts`, `/v1/vector-search`, `/v1/hybrid-search`) return an optional `searchStats` object with performance metrics:

```json
{
  "searchStats": {
    "durationMs": 12,
    "queryTerms": ["cancel", "subscription"],
    "indexSize": 150,
    "totalTokens": 2
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `durationMs` | int | Wall-clock search time in milliseconds |
| `queryTerms` | string[] | Tokenized/stemmed query terms used |
| `indexSize` | int | Number of documents in search scope |
| `totalTokens` | int | Number of tokens in the query |

### Configuration

Search stats are **enabled by default**. To disable:

```bash
MDDB_SEARCH_STATS=false ./mddbd
```

The config endpoint (`GET /v1/config`) includes `searchStatsEnabled` field.

## Distance Metrics (v2.7.0+)

MDDB supports three distance metrics for vector and hybrid search. The metric controls how similarity between embedding vectors is computed.

| Metric | Value | Description | Score Range | Best For |
|--------|-------|-------------|-------------|----------|
| Cosine (default) | `cosine` | Measures the angle between two vectors, ignoring magnitude | -1 to 1 | Normalized text embeddings (OpenAI, Cohere, etc.) |
| Dot Product | `dot_product` | Raw dot product of two vectors. For normalized vectors, equals cosine similarity | Unbounded | Pre-normalized embeddings where speed matters |
| Euclidean | `euclidean` | Converts L2 (Euclidean) distance to similarity via `1/(1+dist)` | 0 to 1 | Non-normalized vectors where magnitude matters |

### API Example

```bash
# Vector search with dot_product metric
curl -X POST http://localhost:11023/v1/vector-search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "kb",
    "query": "how to cancel my subscription?",
    "topK": 5,
    "distanceMetric": "dot_product"
  }'
```

### Notes

- **Normalized embeddings:** For normalized embeddings (OpenAI, most providers), all three metrics produce equivalent ranking order. The default `cosine` is recommended unless you have a specific reason to change it.
- **Per-request configuration:** The distance metric is specified per-request via the `distanceMetric` parameter. No server-side configuration is needed.

## Automation Triggers (v2.6.9+)

MDDB supports **automation triggers** that fire webhooks when documents matching search criteria are added to a collection.

### Concepts

| Type | Purpose |
|------|---------|
| **Webhook** | Named HTTP endpoint target (url, method, headers) |
| **Trigger** | Rule: when a document in `{collection}` matches `{searchType}` query `{query}` above `{threshold}`, fire `{webhook}` |
| **Cron** | Scheduled execution of a trigger on a cron schedule |

All three types are stored together in the automation system with a `type` field.

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `MDDB_TRIGGERS` | `false` | Enable real-time trigger evaluation after document add |
| `MDDB_CRONS` | `false` | Enable cron scheduler for periodic trigger execution |

### Trigger Evaluation

When `MDDB_TRIGGERS=true` and a document is added:

1. All enabled triggers watching that collection are loaded
2. Each trigger's search query runs (FTS, vector, or hybrid)
3. If the new document appears in results with score >= threshold, the webhook fires
4. Webhook firing is async with retry backoff (0s, 1s, 5s, 15s)

### Threshold Behavior

| Search Type | Threshold Meaning | Example |
|-------------|-------------------|---------|
| `fts` | Raw BM25 score | threshold=5 means BM25 score >= 5 |
| `vector` | Similarity × 100 | threshold=80 means cosine similarity >= 0.8 |
| `hybrid` | Combined score × 100 | threshold=50 means combined score >= 0.5 |

### API Examples

**Create a webhook target:**
```bash
curl -X POST http://localhost:7890/v1/automation \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "webhook",
    "name": "Slack Alert",
    "url": "https://hooks.slack.com/services/...",
    "method": "POST",
    "enabled": true
  }'
```

**Create a trigger:**
```bash
curl -X POST http://localhost:7890/v1/automation \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "trigger",
    "name": "AI Article Alert",
    "collection": "articles",
    "searchType": "fts",
    "query": "artificial intelligence machine learning",
    "threshold": 5,
    "webhookId": "<webhook-id>",
    "enabled": true
  }'
```

**Create a cron:**
```bash
curl -X POST http://localhost:7890/v1/automation \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "cron",
    "name": "Daily AI Check",
    "schedule": "0 0 9 * * *",
    "triggerId": "<trigger-id>",
    "enabled": true
  }'
```

**Test a trigger (dry run):**
```bash
curl -X POST http://localhost:7890/v1/automation/<trigger-id>/test
```

### Webhook Payload

When a trigger fires, it sends a POST to the webhook URL:

```json
{
  "event": "trigger.matched",
  "trigger": {
    "id": "abc123",
    "name": "AI Article Alert"
  },
  "collection": "articles",
  "document": { "id": "...", "key": "...", "contentMd": "...", "meta": {...} },
  "score": 85.5,
  "timestamp": 1709510400
}
```

## Cross-Collection Search (v2.7.0+)

Search across multiple collections using a document's embedding or a text query.

### Use Cases
- Find images matching blog post content
- Discover related audio files for a document
- Cross-reference content between different collection types

### Document-as-Query
Use a source document's embedding vector to search target collections:

```bash
curl -X POST http://localhost:8080/v1/cross-search \
  -H "Content-Type: application/json" \
  -d '{
    "sourceCollection": "content",
    "sourceDocID": "post-123",
    "targetCollections": ["images", "audio"],
    "topK": 10,
    "threshold": 0.5,
    "distanceMetric": "cosine"
  }'
```

### Text Query Across Collections
```bash
curl -X POST http://localhost:8080/v1/cross-search \
  -H "Content-Type: application/json" \
  -d '{
    "query": "machine learning tutorial",
    "targetCollections": ["content", "images", "documents"],
    "topK": 20
  }'
```

### Response
Each result includes the collection it came from:
```json
{
  "results": [
    {
      "collection": "images",
      "document": {"id": "img-456", "meta": {"alt": ["ML diagram"]}},
      "score": 0.89,
      "rank": 1
    }
  ],
  "total": 1,
  "targetCollections": ["images", "audio"],
  "algorithm": "flat",
  "distanceMetric": "cosine"
}
```

## Collection Attributes (v2.7.0+)

Configure collection metadata: type, description, icon, color, and custom key-value pairs.

### Collection Types
- `default` — General-purpose collection
- `website` — Web content / scraped pages
- `images` — Image metadata and descriptions
- `audio` — Audio file metadata
- `documents` — Document repository

### Set Collection Config
```bash
curl -X PUT http://localhost:8080/v1/collection-config \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "blog",
    "type": "website",
    "description": "Blog posts and articles",
    "icon": "🌐",
    "color": "#3B82F6"
  }'
```

### Get Collection Config
```bash
curl http://localhost:8080/v1/collection-config?collection=blog
```

Collection attributes are returned in stats responses and visible in the panel sidebar.

## Duplicate Detection (v2.7.0+)

MDDB can detect exact and semantically similar documents within a collection. Useful for deduplication, quality control, and understanding content overlap.

### Modes

| Mode | Method | Complexity | Description |
|------|--------|-----------|-------------|
| `exact` | Content Hash (SHA256) | O(N) | Groups documents with identical content |
| `similar` | Embedding Cosine Similarity | O(N²/2) | Groups documents above a similarity threshold |
| `both` | Hash + Embeddings | O(N²/2) | Runs both detection methods (default) |

### Examples

```bash
# Find all duplicates (both exact and similar)
curl -X POST http://localhost:11023/v1/find-duplicates \
  -H "Content-Type: application/json" \
  -d '{"collection":"blog","threshold":0.9}'

# Exact duplicates only (fast, O(N))
curl -X POST http://localhost:11023/v1/find-duplicates \
  -H "Content-Type: application/json" \
  -d '{"collection":"blog","mode":"exact"}'

# Similar documents with custom threshold
curl -X POST http://localhost:11023/v1/find-duplicates \
  -H "Content-Type: application/json" \
  -d '{"collection":"images","mode":"similar","threshold":0.85,"includeContent":true}'
```

### MCP Tool

```json
{
  "name": "find_duplicates",
  "arguments": {
    "collection": "blog",
    "mode": "both",
    "threshold": 0.9
  }
}
```

### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `collection` | string | required | Collection to scan |
| `mode` | string | `both` | `exact`, `similar`, or `both` |
| `threshold` | float | `0.9` | Minimum similarity score (0-1) for similar mode |
| `maxDocs` | int | `5000` | Max documents to process (safety bound for large collections) |
| `distanceMetric` | string | `cosine` | `cosine`, `dot_product`, or `euclidean` |
| `includeContent` | bool | `false` | Include document content in results |

### Response

Results are returned as **groups** of duplicate documents. Each group contains 2+ documents.

- **Exact groups**: Documents with identical SHA256 content hashes (score = 1.0)
- **Similar groups**: Documents clustered by transitive similarity — if A is similar to B and B to C, all three are grouped together even if A and C are below threshold directly

The response includes summary counts:
- `exactDuplicates` — total documents that are exact duplicates
- `similarPairs` — total pairs of similar documents found

## Aggregation (Facets & Histograms)

MDDB supports faceted aggregation and time-based histograms for building search UIs, dashboards, and analytics.

**Endpoint:** `POST /v1/aggregate`

### Facets

Count distinct values for metadata fields. Useful for building filter sidebars (e.g., "Show 42 results in category 'tutorial'").

```bash
curl -X POST http://localhost:11023/v1/aggregate \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "blog",
    "facets": [
      {"field": "category", "orderBy": "count"},
      {"field": "author", "orderBy": "value", "maxFacetSize": 20}
    ]
  }'
```

**Response:**
```json
{
  "facets": {
    "category": [
      {"value": "tutorial", "count": 42},
      {"value": "guide", "count": 18},
      {"value": "reference", "count": 7}
    ],
    "author": [
      {"value": "alice", "count": 30},
      {"value": "bob", "count": 25}
    ]
  }
}
```

#### Facet Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `field` | required | Metadata key to aggregate |
| `orderBy` | `"count"` | Sort by `"count"` (descending) or `"value"` (alphabetical) |
| `maxFacetSize` | `50` | Maximum number of distinct values returned |

### Histograms

Group documents by time intervals on `addedAt` or `updatedAt` fields.

```bash
curl -X POST http://localhost:11023/v1/aggregate \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "blog",
    "histograms": [
      {"field": "addedAt", "interval": "month"}
    ]
  }'
```

**Response:**
```json
{
  "histograms": {
    "addedAt": [
      {"bucket": "2026-01", "count": 15},
      {"bucket": "2026-02", "count": 23},
      {"bucket": "2026-03", "count": 8}
    ]
  }
}
```

#### Histogram Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `field` | required | `"addedAt"` or `"updatedAt"` |
| `interval` | `"month"` | `"day"`, `"week"`, `"month"`, or `"year"` |

### Combined Request

Facets, histograms, and metadata filters can be combined in a single request:

```bash
curl -X POST http://localhost:11023/v1/aggregate \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "blog",
    "facets": [
      {"field": "category"},
      {"field": "tags"}
    ],
    "histograms": [
      {"field": "addedAt", "interval": "month"},
      {"field": "updatedAt", "interval": "week"}
    ],
    "filterMeta": {"status": ["published"]}
  }'
```

### MCP Tool

```json
{
  "tool": "aggregate",
  "arguments": {
    "collection": "blog",
    "facets": [{"field": "category"}],
    "histograms": [{"field": "addedAt", "interval": "month"}]
  }
}
```

## Zero-Shot Classification

MDDB supports zero-shot document classification using the same embedding infrastructure as vector search.

### How It Works

1. The document (or raw text) is embedded into a vector
2. All candidate labels are embedded in a single batch call
3. Cosine similarity is computed between the document vector and each label vector
4. Labels are ranked by similarity score

### API

```bash
# Classify raw text
curl -X POST http://localhost:11023/v1/classify \
  -d '{"text": "Introduction to machine learning algorithms", "labels": ["technology", "cooking", "sports"]}'

# Classify existing document (reuses stored embedding)
curl -X POST http://localhost:11023/v1/classify \
  -d '{"collection": "articles", "key": "ml-intro", "lang": "en", "labels": ["technology", "cooking", "sports"]}'
```

### MCP Tool

```json
{
  "name": "classify_document",
  "arguments": {
    "text": "Go is a statically typed programming language",
    "labels": ["programming", "cooking", "sports", "music"]
  }
}
```

## Inline Facets on Search (v2.9.14+)

Facets can now be computed inline with `/v1/fts` and `/v1/hybrid-search` so UIs don't need a separate `/v1/aggregate` round-trip. Pass `facetBy` with the metadata keys to aggregate; the response grows a `facets` map keyed by the same names, with per-value counts ordered by count desc, value asc.

```bash
curl -X POST http://localhost:11023/v1/fts \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "blog",
    "query": "rust",
    "limit": 20,
    "facetBy": ["category", "lang"],
    "facetMaxValues": 10
  }'
```

**Response (excerpt):**
```json
{
  "results": [ /* ... */ ],
  "facets": {
    "category": [
      {"value": "tutorial", "count": 7},
      {"value": "guide", "count": 3}
    ],
    "lang": [
      {"value": "en_US", "count": 9},
      {"value": "pl_PL", "count": 1}
    ]
  }
}
```

**Notes:**
- `facetBy` accepts any metadata key. By default counts are computed over the documents actually returned (after filtering, boosts, curation and the limit).
- `facetMaxValues` caps each key's bucket list. `0` or omitted = unlimited.
- Missing keys still appear in the map with an empty bucket list, so UIs can render a stable facet group layout across queries.
- Works with HybridSearch in the same way (parameter is also `facetBy` / `facetMaxValues`).

### Counting over every match: `facetScope` (v2.17.0+, `/v1/fts`)

A filter sidebar wants the number of matches per category, not the number on
the current page. With `"facetScope": "all"` the counts cover every document
that matched the query and `filterMeta` / `rangeMeta`, before pagination, and
`matched` gives their total for an "N found" line:

```bash
curl -X POST http://localhost:11023/v1/fts \
  -H "Content-Type: application/json" \
  -d '{"collection": "ports", "query": "library", "limit": 50,
       "filterMeta": {"system": ["freebsd"]},
       "facetBy": ["category"], "facetScope": "all"}'
```

```json
{
  "results": [ /* the 50 best */ ],
  "total": 50,
  "matched": 1525,
  "facets": { "category": [ {"value": "devel", "count": 412}, … ] }
}
```

`total` keeps its meaning — the number of results returned. Counts come from
the metadata index, not from loading each document, so they stay cheap on a
large match set (19,823 matches over 20,000 documents: 11 ms). Curation does
not change them. HybridSearch has no `all` scope: its vector half ranks every
document, so "every match" would be the whole collection.

For multi-select facets, ask each group's counts with that group's own filter
left out, as usual.

## Curation Rules — Pinned & Hidden Results (v2.9.14+)

Curation rules override organic ranking for specific queries. For each rule, editors can pin documents to fixed 1-based positions and hide other documents entirely; applied inside the FTS + Hybrid pipelines after scoring but before pagination.

### REST Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/curation?id=<id>` | Fetch one rule |
| `GET` | `/v1/curation?collection=<c>` | List rules for a collection |
| `GET` | `/v1/curation` | List all rules (admin) |
| `POST` | `/v1/curation` | Create a rule (id assigned by server) |
| `PUT` | `/v1/curation` | Replace a rule (body must include `id`) |
| `DELETE` | `/v1/curation?id=<id>` | Remove a rule |

### Rule shape

```json
{
  "id": "cur_f0a1…",
  "collection": "blog",
  "query": "rust tutorial",
  "matchMode": "exact",
  "pins": [
    {"key": "rust-getting-started", "lang": "en_US", "position": 1},
    {"key": "rust-ownership",        "position": 2}
  ],
  "hides": ["legacy-post", "wip-draft"],
  "enabled": true
}
```

- **`matchMode`** — `exact` (default) matches the full query verbatim, `contains` fires when the rule query is a substring of the incoming query. Both are case-insensitive.
- **`pins`** — Each pin places a document at the given 1-based `position`. `position <= 0` appends after organic results. When `lang` is omitted, the first matching key across any language wins.
- **`hides`** — A list of document keys dropped from results entirely.
- **`enabled`** — `false` disables a rule without deleting it.

### Response markers

Results injected by a pin carry `"pinned": true`, so clients can style them distinctly:

```json
{
  "document": { "key": "rust-getting-started", /* ... */ },
  "score": 0,
  "pinned": true
}
```

### Example

```bash
# Create
curl -X POST http://localhost:11023/v1/curation \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "blog",
    "query": "rust tutorial",
    "matchMode": "exact",
    "enabled": true,
    "pins": [{"key": "rust-getting-started", "position": 1}],
    "hides": ["legacy-post"]
  }'

# List for a collection
curl "http://localhost:11023/v1/curation?collection=blog"

# Delete
curl -X DELETE "http://localhost:11023/v1/curation?id=cur_f0a1…"
```

### MCP Tools

- `list_curation_rules` — scope by `collection` or return all.
- `create_curation_rule` — body matches the REST rule shape; `pins` accepts either full objects or a flat list of keys (which auto-assigns ascending positions).
- `update_curation_rule` — requires `id`; other fields are patched in.
- `delete_curation_rule` — by `id`.

### Precedence

Curation runs **after** filtering, range filters, and boost multipliers, but **before** the `limit`/`topK` trim and before facet counting. Facets therefore reflect what the user actually sees, including pinned docs. A pin can pull a doc from beyond the organic cutoff into the visible window.

**[← Back to README](../README.md)**
