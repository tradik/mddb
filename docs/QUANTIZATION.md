---
title: "Vector Quantization"
slug: "docs/quantization"
description: "Per-collection vector quantization in MDDB: int8 and int4 compression plus disk-only mode, cutting vector memory several-fold with near-lossless ranking."
status: publish
---

# Vector Quantization

MDDB supports **per-collection vector quantization** to reduce storage size and memory usage for embedding vectors. Quantization compresses float32 vectors into lower-precision formats while preserving search quality.

Available since **v2.9.0**.

## Overview

| Format | Bits/Dimension | Compression | Recall Drop | Use Case |
|--------|---------------|-------------|-------------|----------|
| `float32` | 32 | 1x (baseline) | 0% | Default, highest accuracy |
| `int8` | 8 | **4x** | ~1% | Recommended for most use cases |
| `int4` | 4 | **8x** | ~2-3% | Maximum compression, large collections |

### How It Works

**Scalar Quantization** maps each float32 dimension to a fixed-range integer:

- **int8**: Maps `[min, max]` → `[0, 255]` (256 levels per dimension)
- **int4**: Maps `[min, max]` → `[0, 15]` (16 levels per dimension, packed 2 per byte)

Calibration parameters (`min`, `max`) are stored per-vector, so each vector uses its full dynamic range.

### Where Quantization Applies

Quantization is applied in **two layers**:

1. **Storage (BoltDB)** — Vectors are stored in quantized binary format, reducing disk usage
2. **In-Memory Search** — Similarity is computed directly on quantized values (no dequantization at search time), reducing RAM and speeding up brute-force search

## Configuration

Quantization is configured **per collection** via the Collection Config API.

### Set Quantization via API

```bash
# Enable int8 quantization for a collection
curl -X PUT http://localhost:11023/v1/collection-config \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "archive",
    "quantization": "int8"
  }'

# Enable int4 quantization (maximum compression)
curl -X PUT http://localhost:11023/v1/collection-config \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "logs",
    "quantization": "int4"
  }'

# Disable quantization (revert to float32)
curl -X PUT http://localhost:11023/v1/collection-config \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "archive",
    "quantization": "float32"
  }'
```

### Set Quantization via Panel

In the web admin panel, open **Collection Settings** for any collection and select the desired quantization level from the **Vector Quantization** dropdown.

### Check Quantization Status

```bash
# View quantization per collection
curl http://localhost:11023/v1/vector-stats | jq '.collections'
```

Response:

```json
{
  "archive": {
    "total_documents": 5000,
    "embedded_documents": 5000,
    "total_chunks": 12500,
    "quantization": "int8"
  },
  "blog": {
    "total_documents": 200,
    "embedded_documents": 200,
    "total_chunks": 600,
    "quantization": "float32"
  }
}
```

## Reindexing After Changing Quantization

After changing a collection's quantization setting, you must **reindex** to re-encode the existing vectors:

```bash
curl -X POST http://localhost:11023/v1/vector-reindex \
  -H "Content-Type: application/json" \
  -d '{"collection": "archive", "force": true}'
```

The `force: true` flag ensures all vectors are re-embedded and stored with the new quantization format. Without it, only documents with changed content are re-processed.

## Storage Savings

For typical OpenAI `text-embedding-3-small` embeddings (1536 dimensions):

| Format | Size Per Vector | Size for 10K Docs | Size for 100K Docs |
|--------|----------------|-------------------|-------------------|
| `float32` | 6,144 bytes | ~60 MB | ~600 MB |
| `int8` | 1,549 bytes* | ~15 MB | ~150 MB |
| `int4` | 781 bytes* | ~7.5 MB | ~75 MB |

*Includes 13-byte header (type + min + max + dims).

## Search Behavior

When a collection has quantization enabled:

1. **Automatic algorithm selection** — Vector search automatically uses the `quantized` searcher for quantized collections. No need to specify `"algorithm": "quantized"` in the request.
2. **Query quantization** — The incoming query vector (float32) is quantized on-the-fly using the collection's global calibration range before similarity computation.
3. **Quantized similarity** — Cosine similarity is computed directly on int8/int4 values using integer arithmetic, which is faster than float32 operations.

### Manual Algorithm Selection

You can explicitly request the quantized searcher:

```bash
curl -X POST http://localhost:11023/v1/vector-search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "archive",
    "query": "how to configure authentication?",
    "topK": 5,
    "algorithm": "quantized"
  }'
```

Or force float32 search even on a quantized collection:

```bash
curl -X POST http://localhost:11023/v1/vector-search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "archive",
    "query": "how to configure authentication?",
    "topK": 5,
    "algorithm": "flat"
  }'
```

## Storage Quantization vs. Index Algorithms

Two independent mechanisms share the "quantization" vocabulary:

- **Storage quantization** (this document): `int8`/`int4` per collection —
  compresses what is *stored and held in memory*.
- **Index algorithms** (`algorithm` on `/v1/vector-search`): `flat`, `hnsw`,
  `ivf`, `pq`, `opq`, `sq`, `sq4`, `bq` — change *how candidates are found*. PQ/OPQ/
  SQ/BQ are themselves compression-based indexes; HNSW and IVF are
  graph/cluster-based approximate indexes. See
  [SEARCH.md](SEARCH.md#vector-search) for per-algorithm characteristics.

Index tuning parameters are currently fixed at sensible defaults (HNSW
`M=16`, `efConstruction=200`, `efSearch=100`; IVF `nProbe=10`; PQ
`nSubspaces=8`, `codebookSize=256`; OPQ `opqIter=5`). The one runtime knob is
`MDDB_VECTOR_BQ_RERANK_FACTOR` for the binary-quantized index — see
[config.md](config.md).

## Disk-Only Vectors — Low-Memory Mode (v2.11.4+)

By default, quantized collections still keep full-precision vectors in the
float32 index alongside the quantized copies. `diskOnlyVectors` removes that
duplication entirely: **RAM holds only the quantized representation**, while
full-precision vectors live exclusively on disk (the BoltDB `vectors` bucket).

```bash
curl -X PUT http://localhost:11023/v1/collection-config \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "archive",
    "quantization": "int8",
    "diskOnlyVectors": true
  }'

# Re-embed so full-precision vectors are stored on disk
curl -X POST http://localhost:11023/v1/vector-reindex \
  -d '{"collection": "archive", "force": true}'
```

Searches then run in two phases:

1. **Phase 1 (RAM):** the quantized index produces an oversampled candidate
   set (3× topK) using fast integer arithmetic.
2. **Phase 2 (disk):** the candidates' full-precision vectors are batch-read
   from disk and rescored exactly, restoring the precision lost to
   quantization before final ranking.

Memory footprint per vector drops ~4× with `int8` and ~8× with `int4`
relative to keeping float32 vectors resident — for 100K docs at 1536
dimensions that is ~600 MB → ~150 MB (int8). The cost is one extra BoltDB
read per query for the candidate set, typically well under a millisecond.

Notes:

- Requires `quantization` (`int8` or `int4`); the server rejects
  `diskOnlyVectors` without it.
- Vectors are stored on disk in **full precision** for this mode, so the
  exact rescoring phase has lossless data to work with.
- `vector-stats` reports `diskOnlyVectors: true` for such collections.
- Ideal for edge deployments and small VPS instances hosting large archives.

## Backward Compatibility

- **Existing vectors** stored before v2.9.0 use float32 format (v1 binary encoding). They continue to work without changes.
- The storage layer auto-detects the format (v1 float32 vs v2 quantized) on read.
- Changing quantization only affects newly written vectors. Use `vector-reindex --force` to convert all existing vectors.
- Collections without a `quantization` setting default to `float32`.

## Comparison with Other Index Algorithms

MDDB offers multiple approaches to reduce vector search cost:

| Approach | Compression | Speed | Accuracy | Configurable Per-Collection |
|----------|-------------|-------|----------|----------------------------|
| **Scalar Quantization (this)** | 4-8x storage + RAM | Faster | ~98-99% | Yes |
| SQ4 index (`algorithm: "sq4"`) | 8x RAM only | Fast | 99.5% of int8 (measured) | Yes |
| PQ (Product Quantization) | 8-32x RAM only | Much faster | ~95% | No (global) |
| SQ (Index-level SQ) | 4x RAM only | Faster | ~98% | No (global) |
| BQ (Binary Quantization) | 32x RAM only | Fastest | ~90% | No (global) |
| HNSW | No compression | Faster | ~99% | No (global) |

The key advantage of per-collection quantization is **storage compression** (BoltDB on disk) combined with in-memory search on quantized data, and the ability to choose different precision levels for different collections.

## Technical Details

### Binary Storage Format (v2)

Quantized records use a v2 binary format with a version byte prefix:

```
[1B version=2][1B quantType][4B model_len][model][4B qvec_len][quantized_vector][8B created_at][4B hash_len][hash][4B docid_len][docid]
```

The quantized vector block:

```
[1B type][4B min][4B max][4B dims][data...]
```

- **int8**: `data` = 1 byte per dimension
- **int4**: `data` = 1 byte per 2 dimensions (high nibble first)

### Similarity Functions

- `CosineSimInt8` — Integer dot product and norms on uint8 values
- `CosineSimInt4` — Nibble extraction + integer arithmetic

Both return values in the same range as float32 cosine similarity, so thresholds work identically.

### Hardware Acceleration (v2.9.9+)

Float32 vector math (cosine similarity, dot product, Euclidean distance) is hardware-accelerated on ARM64 platforms using a 3-tier dispatch:

| Tier | Hardware | SIMD Width | Speedup |
|------|----------|-----------|---------|
| **SME** | Apple M4+, Cortex-X925+ | Scalable (128-2048 bit) | ~7x |
| **NEON** | All ARM64 (M1+, Graviton, etc.) | 128 bit (4x float32) | ~3-4x |
| **Scalar** | x86, other architectures | N/A | Baseline |

Detection is automatic at runtime. No configuration required.

Build with `-tags nosme` to force pure Go scalar on ARM64 (useful for debugging or CI).

Check active tier via server logs at startup or `vectorMathTier()` in code.
