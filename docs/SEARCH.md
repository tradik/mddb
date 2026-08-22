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

Configure default language: `MDDB_FTS_DEFAULT_LANG=en` (default). Query supported languages: `GET /v1/fts-languages`. Reindex existing documents: `POST /v1/fts-reindex?collection=X`.

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

Custom weights can be passed per-query via `fieldWeights`. Fields not in the weights map are ignored.

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
| Accuracy | ~95-99% recall |
| Speed | O(log n) |
| Memory | Vectors + graph edges (~2x flat) |
| Build time | O(n log n) |
| Parameters | M=16, efSearch=100 |

**When to use:** Best general-purpose algorithm for medium to large collections (10K-1M documents). Excellent speed/accuracy trade-off.

### IVF (Inverted File Index)

Clusters vectors using k-means, then searches only the nearest clusters. Requires training after loading vectors.

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
| HNSW | ~97% | Fast | ~2x | 10K-1M docs |
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
| `vectorAlgorithm` | `"flat"` | Vector algorithm: `"flat"`, `"hnsw"`, `"ivf"`, `"pq"`, `"opq"`, `"sq"`, `"bq"` |
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
- `facetBy` accepts any metadata key — counts are computed over the documents actually returned (after filtering, boosts, curation).
- `facetMaxValues` caps each key's bucket list. `0` or omitted = unlimited.
- Missing keys still appear in the map with an empty bucket list, so UIs can render a stable facet group layout across queries.
- Works with HybridSearch in the same way (parameter is also `facetBy` / `facetMaxValues`).

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
