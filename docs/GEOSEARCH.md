---
title: "Geospatial Search: Radius and Bounding-Box Queries"
slug: "docs/geosearch"
description: "Geospatial search in MDDB — R-tree and geohash indexes, radius and bounding-box queries, composition with full-text and vector search."
status: publish
---

# Geosearch

MDDB 2.9.11 ships a full geospatial search subsystem: radius and
bounding-box queries, two pluggable index algorithms, composition with
full-text and vector search, and a Leaflet-backed panel UI. This
document describes the data model, the HTTP/MCP/gRPC surfaces, and the
operational trade-offs.

> **Scale envelope**: tested up to 100 000 points per collection with
> sub-millisecond R-tree queries. Fine for "venues/posts near X"
> workloads. Not a replacement for PostGIS on multi-million-point
> datasets.

## Data model

Coordinates attach to documents via reserved metadata keys. The index
extracts them in priority order at write time:

1. **Explicit lat/lng** — `geo_lat` + `geo_lng` as float64 strings
   (decimal degrees, WGS84). Canonical and fastest.
2. **Geohash** — `geo_hash` (1–12 character geohash string). MDDB
   decodes it to the centroid of the cell. Useful when your upstream
   system already has a geohash.
3. **Postcode** — `geo_postcode` + `geo_country`, resolved through an
   **opt-in** in-memory postcode → (lat, lng) lookup loaded from a CSV
   file per country. Silent no-op if the lookup is not populated.

```yaml
---
title: "Joe's Coffee"
geo_lat: "52.5200"
geo_lng: "13.4050"
---

# Joe's Coffee
Great espresso in the heart of Berlin.
```

Or, equivalently:

```yaml
---
title: "Joe's Coffee"
geo_hash: "u33d8s7"
---
```

Or, with postcode fallback (requires `geo-reindex` with `loadPostcodes`):

```yaml
---
title: "Joe's Coffee"
geo_postcode: "10115"
geo_country: "DE"
---
```

**Reserved keys**: `geo_lat`, `geo_lng`, `geo_hash`, `geo_postcode`,
`geo_country`. Do not use these for unrelated metadata or the index
will pick them up.

## Index algorithms

Two algorithms share the same `geo` BoltDB bucket. They are two
*in-memory views* of the same persisted data, rebuilt independently at
startup and kept in sync by the write hooks in [main.go](https://github.com/tradik/mddb/blob/main/services/mddbd/main.go).
Pick between them at query time via the `algorithm` field on
`/v1/geo-search`.

### `rtree` (default)

Implementation: [tidwall/rtree](https://github.com/tidwall/rtree), a
pure-Go R-tree with `[2]float64` bounding boxes. Each point is stored
as a zero-area bbox keyed by docID. The in-memory structure mirrors
the vector index pattern: an `RWMutex`-protected per-collection tree
plus a secondary `map[docID]geoPoint` so we can delete by docID
without scanning.

**Strong for**: radius queries, bounding-box queries, moderate update
frequency. Handles poles and the anti-meridian cleanly (the index
does not, but haversine scoring does).

### `geohash`

Implementation: `geo_hash.go` + `geohash_index.go`. Points are encoded
at a fixed precision (`geohashIndexPrecision = 8`, ~40 m cell) and
kept in a sorted slice per collection. Queries walk the precision
down until the cell is larger than the search radius, binary-search
the slice for that prefix range, and haversine-filter the candidates.

**Strong for**: BoltDB-native workloads that want to compose with
prefix scans on the same hash. Slightly slower than the R-tree for
bbox queries (falls back to a linear scan). Useful as a sanity check
against the R-tree results on the same data.

The encoding is the canonical 32-char alphabet
`0123456789bcdefghjkmnpqrstuvwxyz`, compatible with
[geohash.org](http://geohash.org) and most client libraries.

## Endpoints

All endpoints accept JSON and return JSON. Write endpoints are gated
by the usual `read-only` mode middleware.

### POST `/v1/geo-search`

Radius search. Returns results sorted by ascending distance.

```json
{
  "collection": "venues",
  "lat": 52.52,
  "lng": 13.405,
  "radiusMeters": 5000,
  "topK": 10,
  "algorithm": "rtree",
  "filterMeta": {"category": ["coffee"]}
}
```

Response:

```json
{
  "results": [
    {
      "document": {"id": "...", "key": "joes-coffee", "meta": {...}},
      "distanceMeters": 342.7,
      "rank": 1
    }
  ],
  "total": 1,
  "radiusMeters": 5000,
  "algorithm": "rtree"
}
```

### POST `/v1/geo-within`

Axis-aligned bbox search. No ordering is applied.

```json
{
  "collection": "venues",
  "minLat": 52.5, "maxLat": 52.6,
  "minLng": 13.3, "maxLng": 13.5
}
```

### POST `/v1/geo-polygon` (v2.9.13+)

GeoJSON Polygon or MultiPolygon containment. Returns every indexed point
whose coordinates fall inside the shape. Exactly one of `polygon` or
`multiPolygon` must be set — supplying both is rejected with 400.

Polygons follow RFC 7946: the outer coordinate array holds one or more
linear rings, where the first ring is the outer boundary and any
subsequent rings are holes. Coordinates are `[lng, lat]` pairs. Rings
may be open (first ≠ last) or closed — both forms are accepted.

```json
{
  "collection": "venues",
  "polygon": {
    "type": "Polygon",
    "coordinates": [
      [[13.36, 52.51], [13.42, 52.51], [13.42, 52.53],
       [13.36, 52.53], [13.36, 52.51]]
    ]
  }
}
```

With holes (square with a central 4×4 hole):

```json
{
  "collection": "venues",
  "polygon": {
    "coordinates": [
      [[0,0],[10,0],[10,10],[0,10],[0,0]],
      [[3,3],[7,3],[7,7],[3,7],[3,3]]
    ]
  }
}
```

MultiPolygon (union semantics — matches any member):

```json
{
  "collection": "venues",
  "multiPolygon": {
    "type": "MultiPolygon",
    "coordinates": [
      [[[13.36,52.51],[13.42,52.51],[13.42,52.53],[13.36,52.53],[13.36,52.51]]],
      [[[2.34,48.85],[2.36,48.85],[2.36,48.87],[2.34,48.87],[2.34,48.85]]]
    ]
  }
}
```

**Implementation**: the R-tree does a bounding-box query on the polygon's
bbox to prefilter candidates, then ray-casts each survivor against the
actual ring geometry. Response time tracks the shape's bbox size rather
than the whole collection size. The endpoint does not cross the
anti-meridian — split queries that span ±180° into east and west halves.

Also exposed as MCP tool `geo_polygon`.

### POST `/v1/geo-reindex`

Force-rebuild **both** in-memory indexes from the persisted `geo`
bucket, optionally loading one or more postcode CSVs first.
Write-gated.

```json
{
  "collection": "venues",
  "loadPostcodes": [
    {"country": "PL", "csvPath": "/var/lib/mddb/postcodes/pl.csv"},
    {"country": "GB", "csvPath": "/var/lib/mddb/postcodes/gb.csv"}
  ]
}
```

CSV format: `postcode,lat,lng` (three columns, no header, UTF-8).
MDDB never ships postcode datasets — operators provide their own.

### GET `/v1/geo-stats`

Per-collection point counts + loaded postcode dataset sizes.

### POST `/v1/geo-encode` · POST `/v1/geo-decode`

Ad-hoc conversion helpers. Useful for building UIs or debugging.

```json
// geo-encode
{"lat": 52.52, "lng": 13.405, "precision": 8}
// → {"geohash": "u33dc1j2", "precision": 8}

// geo-decode
{"geohash": "u33dc1j2"}
// → {"lat": 52.5199..., "lng": 13.4049..., "minLat": ..., "maxLat": ..., "minLng": ..., "maxLng": ...}
```

## Composition with FTS and vector search

`POST /v1/hybrid-search` grows an optional `geo` field that spatially
pre-filters the FTS + vector candidate set before rank fusion. This is
the easiest way to write a query like "coffee shops within 5 km of me,
ranked by semantic relevance".

```json
{
  "collection": "venues",
  "query": "coffee",
  "geo": {"lat": 52.52, "lng": 13.405, "radiusMeters": 5000},
  "strategy": "alpha",
  "alpha": 0.6
}
```

Each result item gains a `distanceMeters` field in the composed
response.

## GraphQL

GraphQL is **not** a supported protocol for geosearch in 2.9.10. The
GraphQL subsystem in the project is currently a pre-existing stub —
every query resolver panics with `not implemented` — and wiring it
up is tracked separately. Until that follow-up PR lands, use REST,
gRPC, or MCP for geo queries.

## MCP tools

All geo endpoints are exposed to LLM clients via MCP. Tool names:
`geo_search`, `geo_within`, `geo_stats`, `geo_encode`, `geo_decode`.
All are annotated `readOnlyHint: true` and work in read-only mode.

## Panel UI

The panel ships a "Geo Search" tab with a Leaflet + OpenStreetMap map.
Click the map to set the query center, drag the slider to change the
radius, pick the algorithm and hit Search. Results are drawn as pins
and listed to the right; clicking a pin opens the document in the
shared viewer.

No map-provider key is needed — OpenStreetMap tiles are used directly
with their public attribution. If you need a different tile source
(Mapbox, Stamen, Carto), edit the `tileLayer` URL in
[services/mddb-panel/src/components/GeoPanel.jsx](https://github.com/tradik/mddb/blob/main/services/mddb-panel/src/components/GeoPanel.jsx).

## Operational notes

- **Startup latency**: both indexes load asynchronously from the `geo`
  bucket. Queries return HTTP 503 until `IsReady()` flips. Startup
  time is roughly linear in the number of points; 100 000 points take
  ~250 ms on a modern laptop.
- **Replication**: the `geo` bucket participates in the standard
  Binlog replication stream. Follower nodes receive geo upserts and
  deletes automatically; no extra wiring needed.
- **Memory**: each point costs ~80 bytes in the R-tree plus ~40 bytes
  in the geohash slice. 100 k points ≈ 12 MB RSS for both indexes
  combined.
- **Benchmark**: `go test ./internal/geo/ -bench BenchmarkGeoIndex -benchmem`. See
  [services/mddbd/internal/geo/geo_index_test.go](https://github.com/tradik/mddb/blob/main/services/mddbd/internal/geo/geo_index_test.go)
  for the harness.

## Limitations

- **Anti-meridian crossing** is not supported. Queries that would
  cross ±180° longitude should be split into two halves by the caller.
- **3D / altitude** is not supported. MDDB is strictly 2D.
- **Automatic postcode downloads** — MDDB does not ship or fetch any
  postcode datasets. Bring your own CSV.
- **Scale ceiling** — beyond ~500 000 points per collection the
  in-memory R-tree starts to dominate process RSS. For bigger
  datasets, use PostGIS or a dedicated spatial DB.
