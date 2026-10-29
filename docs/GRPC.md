---
title: "gRPC API Documentation"
slug: "docs/grpc"
description: "MDDB's gRPC API over HTTP/2 and protocol buffers: service definitions, streaming calls, TLS and when it outperforms the HTTP/JSON surface."
status: publish
---

# gRPC API Documentation

> **Note**: The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in [RFC 2119](https://www.ietf.org/rfc/rfc2119.txt).

MDDB provides a high-performance gRPC API alongside the HTTP/JSON API. The gRPC API offers significant performance improvements through binary protocol buffers and HTTP/2.

## Table of Contents

- [Overview](#overview)
- [Why gRPC?](#why-grpc)
- [Getting Started](#getting-started)
- [Service Definition](#service-definition)
- [Client Examples](#client-examples)
- [Schema Validation RPCs](#schema-validation-rpcs)
- [Geo RPCs](#geo-rpcs)
- [Curation RPCs](#curation-rpcs)
- [FTS Maintenance RPCs](#fts-maintenance-rpcs)
- [Replication Service](#replication-service)
- [Not Yet Available over gRPC](#not-yet-available-over-grpc)
- [Performance Comparison](#performance-comparison)
- [Best Practices](#best-practices)

## Overview

**gRPC Endpoint**: `localhost:11024`  
**Protocol**: HTTP/2 + Protocol Buffers  
**Reflection**: Enabled (for grpcurl and debugging)

## Why gRPC?

### Performance Benefits

| Feature | HTTP/JSON | gRPC |
|---------|-----------|------|
| Payload Size | 100% | ~30% (70% smaller) |
| Serialization | JSON text | Binary protobuf |
| Transport | HTTP/1.1 | HTTP/2 |
| Compression | Optional gzip | Built-in |
| Streaming | Limited | Full duplex |
| Type Safety | Runtime | Compile-time |

### Use Cases

**Use gRPC when:**
- Performance is critical
- You need type safety
- Building microservices
- High-throughput operations
- Streaming large datasets

**Use HTTP when:**
- Debugging with curl
- Browser-based clients
- Simple integrations
- Human-readable logs

## Getting Started

### Prerequisites

```bash
# Install protoc (Protocol Buffer Compiler)
brew install protobuf

# Install Go gRPC tools
make install-grpc-tools
```

### Testing with grpcurl

```bash
# Install grpcurl
brew install grpcurl

# List available services
grpcurl -plaintext localhost:11024 list

# Describe a service
grpcurl -plaintext localhost:11024 describe mddb.MDDB

# Call a method
grpcurl -plaintext -d '{"collection":"blog","key":"test","lang":"en_US"}' \
  localhost:11024 mddb.MDDB/Get
```

## Service Definition

The complete service definition is in [`proto/mddb.proto`](https://github.com/tradik/mddb/blob/main/proto/mddb.proto).

### Available RPCs (81 implemented)

| Category | RPC | Request → Response |
|---|---|---|
| **Document Management** | `Add` | `AddRequest` → `Document` |
| | `AddBatch` | `AddBatchRequest` → `AddBatchResponse` |
| | `UpdateDocument` | `UpdateDocumentRequest` → `Document` |
| | `UpdateBatch` | `UpdateBatchRequest` → `UpdateBatchResponse` |
| | `DeleteDocument` | `DeleteDocumentRequest` → `DeleteDocumentResponse` |
| | `DeleteBatch` | `DeleteBatchRequest` → `DeleteBatchResponse` |
| | `DeleteWhere` | `WhereRequest` → `WhereResponse` (v2.18.8: by metadata filter, with `dry_run`) |
| | `UpdateWhere` | `WhereRequest` → `WhereResponse` (v2.18.8) |
| | `SetTTLWhere` | `WhereRequest` → `WhereResponse` (v2.18.8) |
| | `DeleteCollection` | `DeleteCollectionRequest` → `DeleteCollectionResponse` |
| | `Get` | `GetRequest` → `Document` |
| | `GetDocumentMeta` | `GetDocumentMetaRequest` → `GetDocumentMetaResponse` |
| | `Search` | `SearchRequest` → `SearchResponse` |
| | `Ingest` | `IngestRequest` → `IngestResponse` |
| | `ImportURL` | `ImportURLRequest` → `Document` |
| | `SetTTL` | `SetTTLRequest` → `Document` |
| **Full-Text Search** | `FTS` | `FTSRequest` → `FTSResponse` |
| | `FTSReindex` | `FTSReindexRequest` → `FTSReindexResponse` |
| | `FTSLanguages` | `FTSLanguagesRequest` → `FTSLanguagesResponse` |
| **Vector / Semantic** | `VectorSearch` | `VectorSearchRequest` → `VectorSearchResponse` |
| | `VectorReindex` | `VectorReindexRequest` → `VectorReindexResponse` |
| | `VectorStats` | `VectorStatsRequest` → `VectorStatsResponse` |
| | `SearchAdvisor` | `SearchAdvisorRequest` → `SearchAdvisorResponse` |
| | `GetIndexingStatus` | `IndexingStatusRequest` → `IndexingStatusResponse` (v2.18.8: queued embeddings and metadata updates — is what was just written searchable yet?) |
| | `RunEval` | `RunEvalRequest` → `RunEvalResponse` (v2.18.8: recall@k, nDCG@k, MRR, latency and tokens per search configuration) |
| | `AddEvalQuery` | `AddEvalQueryRequest` → `EvalQueryProto` (v2.18.8) |
| | `ListEvalQueries` | `ListEvalQueriesRequest` → `ListEvalQueriesResponse` (v2.18.8) |
| | `DeleteEvalQuery` | `DeleteEvalQueryRequest` → `DeleteEvalQueryResponse` (v2.18.8) |
| | `GetGain` | `GetGainRequest` → `GetGainResponse` (v2.18.8: tokens returned and avoided over a window of days) |
| **Code** | `CodeSymbol` | `CodeSymbolRequest` → `CodeSymbolResponse` (v2.18.8: definitions, references, callers, callees, tests and siblings of a symbol) |
| **Geo** | `GeoSearch` | `GeoSearchRequest` → `GeoSearchResponse` |
| | `GeoWithin` | `GeoWithinRequest` → `GeoWithinResponse` |
| | `GeoReindex` | `GeoReindexRequest` → `GeoReindexResponse` |
| | `GeoStats` | `GeoStatsRequest` → `GeoStatsResponse` |
| | `GeoEncode` | `GeoEncodeRequest` → `GeoEncodeResponse` |
| | `GeoDecode` | `GeoDecodeRequest` → `GeoDecodeResponse` |
| **Hybrid & Cross** | `HybridSearch` | `HybridSearchRequest` → `HybridSearchResponse` |
| | `CrossSearch` | `CrossSearchRequest` → `CrossSearchResponse` |
| **Analysis** | `Classify` | `ClassifyRequest` → `ClassifyResponse` |
| | `FindDuplicates` | `FindDuplicatesRequest` → `FindDuplicatesResponse` |
| | `GetChecksum` | `GetChecksumRequest` → `GetChecksumResponse` |
| | `GetMetaKeys` | `GetMetaKeysRequest` → `GetMetaKeysResponse` |
| **Revisions** | `ListRevisions` | `ListRevisionsRequest` → `ListRevisionsResponse` |
| | `RestoreRevision` | `RestoreRevisionRequest` → `Document` |
| | `Truncate` | `TruncateRequest` → `TruncateResponse` |
| **Export & Backup** | `Export` | `ExportRequest` → `stream ExportChunk` |
| | `Backup` | `BackupRequest` → `BackupResponse` |
| | `Restore` | `RestoreRequest` → `RestoreResponse` |
| **FTS Config** | `ListSynonyms` | `ListSynonymsRequest` → `ListSynonymsResponse` |
| | `AddSynonym` | `AddSynonymRequest` → `AddSynonymResponse` |
| | `DeleteSynonym` | `DeleteSynonymRequest` → `DeleteSynonymResponse` |
| | `ListStopwords` | `ListStopwordsRequest` → `ListStopwordsResponse` |
| | `AddStopwords` | `AddStopwordsRequest` → `AddStopwordsResponse` |
| | `DeleteStopwords` | `DeleteStopwordsRequest` → `DeleteStopwordsResponse` |
| **Schemas** | `SetSchema` | `SetSchemaRequest` → `SetSchemaResponse` |
| | `GetSchema` | `GetSchemaRequest` → `GetSchemaResponse` |
| | `DeleteSchema` | `DeleteSchemaRequest` → `DeleteSchemaResponse` |
| | `ListSchemas` | `ListSchemasRequest` → `ListSchemasResponse` |
| | `ValidateDocument` | `ValidateDocumentRequest` → `ValidateDocumentResponse` |
| **Webhooks** | `RegisterWebhook` | `RegisterWebhookRequest` → `WebhookProto` |
| | `ListWebhooks` | `ListWebhooksRequest` → `ListWebhooksResponse` |
| | `DeleteWebhook` | `DeleteWebhookRequest` → `DeleteWebhookResponse` |
| **Automation** | `ListAutomation` | `ListAutomationRequest` → `ListAutomationResponse` |
| | `CreateAutomation` | `CreateAutomationRequest` → `AutomationRuleProto` |
| | `GetAutomation` | `GetAutomationRequest` → `AutomationRuleProto` |
| | `UpdateAutomation` | `UpdateAutomationRequest` → `AutomationRuleProto` |
| | `DeleteAutomation` | `DeleteAutomationRequest` → `DeleteAutomationResponse` |
| | `TestAutomation` | `TestAutomationRequest` → `TestAutomationResponse` |
| | `GetAutomationLogs` | `GetAutomationLogsRequest` → `GetAutomationLogsResponse` |
| **Collection Config** | `GetCollectionConfig` | `GetCollectionConfigRequest` → `GetCollectionConfigResponse` |
| | `SetCollectionConfig` | `SetCollectionConfigRequest` → `SetCollectionConfigResponse` |
| | `ListCollectionConfigs` | `ListCollectionConfigsRequest` → `ListCollectionConfigsResponse` |
| **Curation** | `ListCurationRules` | `ListCurationRulesRequest` → `ListCurationRulesResponse` |
| | `CreateCurationRule` | `CreateCurationRuleRequest` → `CurationRuleProto` |
| | `UpdateCurationRule` | `UpdateCurationRuleRequest` → `CurationRuleProto` |
| | `DeleteCurationRule` | `DeleteCurationRuleRequest` → `DeleteCurationRuleResponse` |
| **System** | `Stats` | `StatsRequest` → `StatsResponse` |

A separate `mddb.MDDBReplication` service exposes 4 additional RPCs for leader/follower replication — see [Replication Service](#replication-service).

Full service definition: [`proto/mddb.proto`](https://github.com/tradik/mddb/blob/main/proto/mddb.proto)

### New in v2.18.8

`GetIndexingStatus` reports queued work for a collection, or for the server
when `collection` is empty (GO-042), as `GET /v1/indexing-status` does.
`RetrievalProfileProto` gained `group_by_key` (9) and `group_by_per_group`
(10), the collection's default result grouping (SRCH-016), and `format` (11,
AGT-002). `Document.revision` (9) and `UpdateDocumentRequest.expected_revision`
(10) make updates conditional (INT-021): a stale revision is `ABORTED`.
`CodeConfigProto.role` (2) marks reference collections (AGT-003). `Search` also
finds documents again — its index walk cut document IDs wrongly.

**The search RPCs run the shared search code.** `FTS`, `VectorSearch`,
`HybridSearch` and `CrossSearch` used to be separate copies of the search,
and every feature had to be added to them a third time. In 2.18.8 they call
the code that REST and MCP use. They take the same options:

| Field | On | Meaning |
|---|---|---|
| `filter` (`map<string, MetaCondition>`) | `Search`, `FTS`, `VectorSearch`, `HybridSearch`, `CrossSearch` | Operators `any_of` (REST `in`), `prefix`, `contains`, `gt`/`gte`/`lt`/`lte`, `none_of` (REST `not`), `exists`; ANDed with `filter_meta` (SRCH-017) |
| `group_by` (`GroupBy`) | `FTS`, `VectorSearch`, `HybridSearch`, `CrossSearch` | At most `per_group` results per metadata value, document or collection; results carry `group` and `group_rank` (SRCH-016) |
| `format`, `passage_lines` | the same four | `"passages"` returns `repeated Passage passages`: a path, lines, the enclosing symbol and the text (AGT-002) |
| `wait_for_index_ms`, `wait_for_keys`, `read_level` | `VectorSearch`, `HybridSearch`, `CrossSearch` | Wait for queued embeddings; the response carries `indexing` (GO-042) |
| `retrieval_mode`, `window_size` | `VectorSearch` | `chunk` and `window` modes; results carry `chunk_text`, `start_line`, `end_line` |
| `bm25_k1`, `bm25_b` | `FTS`, `HybridSearch`, `EvalConfigProto`, `RetrievalProfileProto` | BM25 parameters (SRCH-018); unset = the collection profile, then 1.2 / 0.75 |
| `search_type` | `CrossSearch` | `vector` (default), `fts` or `hybrid`; targets may be `role:reference` (AGT-003) |

`any_of` and `none_of` are named for the REST `in` and `not`, which are
keywords in Python. Behaviour now matches REST and MCP. The collection's
context budget applies, and so do curation rules on hybrid search, which MCP
did not apply before either. Errors map as follows. A vector index still
loading is `UNAVAILABLE`. No embedding provider is `FAILED_PRECONDITION`. A
cross-search source document without a vector is `NOT_FOUND`. An invalid
option is `INVALID_ARGUMENT`.

`DeleteWhere`, `UpdateWhere` and `SetTTLWhere` (GO-045) act on every
document a metadata filter matches. They share one `WhereRequest`: `filter`,
`filter_meta`, `limit`, `cursor`, `dry_run`, `meta` and `ttl`.

The new RPCs `CodeSymbol`, `RunEval`, `AddEvalQuery`, `ListEvalQueries`,
`DeleteEvalQuery` and `GetGain` match `GET /v1/symbol`, `POST /v1/eval`,
`/v1/eval-queries` and `GET /v1/gain`.

All of this is additive. Clients built against an older proto keep working.

### New in v2.9.12

`FTSRequest` and `HybridSearchRequest` each gained a `map<string, double> boost` field (field number `8` on `FTSRequest`, `15` on `HybridSearchRequest`) for per-query metadata-based score boosting/demotion. Keys are in `"metaKey:metaValue"` form; positive values multiply matching documents' scores, negative values demote. Adding fields is backwards compatible — existing clients compiled against the pre-2.9.12 proto continue to work and simply omit the new field. See [SEARCH.md — Per-Query Boost/Demote](SEARCH.md) for semantics.

### Message Types

#### Document

```protobuf
message Document {
  string id = 1;
  string key = 2;
  string lang = 3;
  map<string, MetaValues> meta = 4;
  string content_md = 5;
  int64 added_at = 6;
  int64 updated_at = 7;
}
```

#### AddRequest

```protobuf
message AddRequest {
  string collection = 1;
  string key = 2;
  string lang = 3;
  map<string, MetaValues> meta = 4;
  string content_md = 5;
}
```

## Client Examples

### Go Client

```go
package main

import (
    "context"
    "log"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"
    pb "mddb/proto"
)

func main() {
    // Connect to server
    conn, err := grpc.Dial("localhost:11024", 
        grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    client := pb.NewMDDBClient(conn)
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    // Add a document
    doc, err := client.Add(ctx, &pb.AddRequest{
        Collection: "blog",
        Key:        "hello-world",
        Lang:       "en_US",
        Meta: map[string]*pb.MetaValues{
            "category": {Values: []string{"blog", "tutorial"}},
            "author":   {Values: []string{"John Doe"}},
        },
        ContentMd: "# Hello World\n\nWelcome to MDDB!",
    })
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("Document added: %s", doc.Id)

    // Get a document
    doc, err = client.Get(ctx, &pb.GetRequest{
        Collection: "blog",
        Key:        "hello-world",
        Lang:       "en_US",
        Env: map[string]string{
            "year": "2024",
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("Content: %s", doc.ContentMd)

    // Search documents
    resp, err := client.Search(ctx, &pb.SearchRequest{
        Collection: "blog",
        FilterMeta: map[string]*pb.MetaValues{
            "category": {Values: []string{"blog"}},
        },
        Sort:  "updatedAt",
        Asc:   false,
        Limit: 10,
    })
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("Found %d documents", len(resp.Documents))

    // Get stats
    stats, err := client.Stats(ctx, &pb.StatsRequest{})
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("Total documents: %d", stats.TotalDocuments)
}
```

### Python Client

```python
import grpc
import mddb_pb2
import mddb_pb2_grpc

# Connect to server
channel = grpc.insecure_channel('localhost:11024')
client = mddb_pb2_grpc.MDD BStub(channel)

# Add a document
doc = client.Add(mddb_pb2.AddRequest(
    collection='blog',
    key='hello-world',
    lang='en_US',
    meta={
        'category': mddb_pb2.MetaValues(values=['blog', 'tutorial']),
        'author': mddb_pb2.MetaValues(values=['John Doe'])
    },
    content_md='# Hello World\n\nWelcome to MDDB!'
))
print(f'Document added: {doc.id}')

# Get a document
doc = client.Get(mddb_pb2.GetRequest(
    collection='blog',
    key='hello-world',
    lang='en_US',
    env={'year': '2024'}
))
print(f'Content: {doc.content_md}')

# Search documents
resp = client.Search(mddb_pb2.SearchRequest(
    collection='blog',
    filter_meta={
        'category': mddb_pb2.MetaValues(values=['blog'])
    },
    sort='updatedAt',
    asc=False,
    limit=10
))
print(f'Found {len(resp.documents)} documents')

# Get stats
stats = client.Stats(mddb_pb2.StatsRequest())
print(f'Total documents: {stats.total_documents}')
```

### Node.js Client

```javascript
const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');

// Load proto file
const packageDefinition = protoLoader.loadSync('mddb.proto', {
  keepCase: true,
  longs: String,
  enums: String,
  defaults: true,
  oneofs: true
});
const mddb = grpc.loadPackageDefinition(packageDefinition).mddb;

// Create client
const client = new mddb.MDDB('localhost:11024', 
  grpc.credentials.createInsecure());

// Add a document
client.Add({
  collection: 'blog',
  key: 'hello-world',
  lang: 'en_US',
  meta: {
    category: { values: ['blog', 'tutorial'] },
    author: { values: ['John Doe'] }
  },
  content_md: '# Hello World\n\nWelcome to MDDB!'
}, (err, doc) => {
  if (err) throw err;
  console.log(`Document added: ${doc.id}`);
});

// Get a document
client.Get({
  collection: 'blog',
  key: 'hello-world',
  lang: 'en_US',
  env: { year: '2024' }
}, (err, doc) => {
  if (err) throw err;
  console.log(`Content: ${doc.content_md}`);
});

// Search documents
client.Search({
  collection: 'blog',
  filter_meta: {
    category: { values: ['blog'] }
  },
  sort: 'updatedAt',
  asc: false,
  limit: 10
}, (err, resp) => {
  if (err) throw err;
  console.log(`Found ${resp.documents.length} documents`);
});

// Get stats
client.Stats({}, (err, stats) => {
  if (err) throw err;
  console.log(`Total documents: ${stats.total_documents}`);
});
```

## Schema Validation RPCs

Schema validation enforces structure on document metadata per collection. See the [Schema Validation Guide](SCHEMA-VALIDATION.md) for full details.

### Message Types

#### SchemaPropertyRule

```protobuf
message SchemaPropertyRule {
  string type = 1;
  repeated string enum_values = 2;
  string pattern = 3;
  int32 min_items = 4;
  int32 max_items = 5;
}
```

#### Schema

```protobuf
message Schema {
  repeated string required = 1;
  map<string, SchemaPropertyRule> properties = 2;
}
```

#### SetSchemaRequest / SetSchemaResponse

```protobuf
message SetSchemaRequest {
  string collection = 1;
  Schema schema = 2;
}

message SetSchemaResponse {
  string status = 1;
}
```

#### GetSchemaRequest / GetSchemaResponse

```protobuf
message GetSchemaRequest {
  string collection = 1;
}

message GetSchemaResponse {
  string collection = 1;
  Schema schema = 2;
}
```

#### DeleteSchemaRequest / DeleteSchemaResponse

```protobuf
message DeleteSchemaRequest {
  string collection = 1;
}

message DeleteSchemaResponse {
  string status = 1;
}
```

#### ListSchemasRequest / ListSchemasResponse

```protobuf
message ListSchemasRequest {}

message ListSchemasResponse {
  repeated CollectionSchema schemas = 1;
}

message CollectionSchema {
  string collection = 1;
  Schema schema = 2;
}
```

#### ValidateDocumentRequest / ValidateDocumentResponse

```protobuf
message ValidateDocumentRequest {
  string collection = 1;
  map<string, MetaValues> meta = 2;
}

message ValidateDocumentResponse {
  bool valid = 1;
  repeated string errors = 2;
}
```

### grpcurl Examples

```bash
# Set a schema for a collection
grpcurl -plaintext -d '{
  "collection": "blog",
  "schema": {
    "required": ["category", "author"],
    "properties": {
      "category": {"type": "string", "enum_values": ["blog", "tutorial", "news"]},
      "author": {"type": "string"}
    }
  }
}' localhost:11024 mddb.MDDB/SetSchema

# Get schema for a collection
grpcurl -plaintext -d '{"collection": "blog"}' \
  localhost:11024 mddb.MDDB/GetSchema

# Delete schema
grpcurl -plaintext -d '{"collection": "blog"}' \
  localhost:11024 mddb.MDDB/DeleteSchema

# List all schemas
grpcurl -plaintext -d '{}' \
  localhost:11024 mddb.MDDB/ListSchemas

# Validate metadata against schema
grpcurl -plaintext -d '{
  "collection": "blog",
  "meta": {
    "category": {"values": ["blog"]},
    "author": {"values": ["John Doe"]}
  }
}' localhost:11024 mddb.MDDB/ValidateDocument
```

### Go Client Example

```go
// Set a schema
_, err := client.SetSchema(ctx, &pb.SetSchemaRequest{
    Collection: "blog",
    Schema: &pb.Schema{
        Required: []string{"category", "author"},
        Properties: map[string]*pb.SchemaPropertyRule{
            "category": {
                Type:       "string",
                EnumValues: []string{"blog", "tutorial", "news"},
            },
            "author": {
                Type: "string",
            },
        },
    },
})

// Validate a document before adding
resp, err := client.ValidateDocument(ctx, &pb.ValidateDocumentRequest{
    Collection: "blog",
    Meta: map[string]*pb.MetaValues{
        "category": {Values: []string{"blog"}},
        "author":   {Values: []string{"Jane Doe"}},
    },
})
if resp.Valid {
    log.Println("Metadata is valid")
} else {
    log.Printf("Validation errors: %v", resp.Errors)
}
```

---

## Geo RPCs

Geospatial search over documents with `lat`/`lng` metadata, backed by an in-memory R-tree. Implemented in `services/mddbd/geo_grpc.go`.

### GeoSearch

Finds documents within `radius_meters` of a `(lat, lng)` point, ordered by ascending distance.

```protobuf
message GeoSearchRequest {
  string collection = 1;
  double lat = 2;
  double lng = 3;
  double radius_meters = 4;
  int32 top_k = 5;
  map<string, MetaValues> filter_meta = 6;
  bool include_content = 7;
}

message GeoSearchResultItem {
  Document document = 1;
  double distance_meters = 2;
  int32 rank = 3;
}

message GeoSearchResponse {
  repeated GeoSearchResultItem results = 1;
  int32 total = 2;
  double radius_meters = 3;
  string algorithm = 4;
}
```

### GeoWithin

Finds documents inside the axis-aligned bounding box `[min_lat, max_lat] × [min_lng, max_lng]`.

```protobuf
message GeoWithinRequest {
  string collection = 1;
  double min_lat = 2;
  double max_lat = 3;
  double min_lng = 4;
  double max_lng = 5;
  map<string, MetaValues> filter_meta = 6;
  bool include_content = 7;
}

message GeoWithinResponse {
  repeated GeoSearchResultItem results = 1;
  int32 total = 2;
  string algorithm = 3;
}
```

### GeoReindex

Force-rebuilds the in-memory geo R-tree from BoltDB and optionally loads postcode lookup CSVs. An empty `collection` means "rebuild all".

```protobuf
message GeoPostcodeLoadProto {
  string country = 1;
  string csv_path = 2;
}

message GeoReindexRequest {
  string collection = 1;
  repeated GeoPostcodeLoadProto load_postcodes = 2;
}

message GeoReindexResponse {
  int32 points = 1;
  string collection = 2;
  map<string, int32> postcodes_loaded = 3;
  int64 duration_ms = 4;
}
```

### GeoStats

Returns geo index statistics per collection plus loaded postcode datasets.

```protobuf
message GeoStatsRequest {}

message GeoCollectionStatProto {
  int32 points = 1;
  int64 last_rebuild_unix = 2;
}

message GeoStatsResponse {
  map<string, GeoCollectionStatProto> collections = 1;
  map<string, int32> postcode_datasets = 2;
  bool ready = 3;
}
```

### GeoEncode

Encodes a `(lat, lng)` pair into a geohash string at the requested precision.

```protobuf
message GeoEncodeRequest {
  double lat = 1;
  double lng = 2;
  int32 precision = 3;
}

message GeoEncodeResponse {
  string geohash = 1;
  int32 precision = 2;
}
```

### GeoDecode

Decodes a geohash back to its centroid `(lat, lng)` and bounding box.

```protobuf
message GeoDecodeRequest {
  string geohash = 1;
}

message GeoDecodeResponse {
  double lat = 1;
  double lng = 2;
  double min_lat = 3;
  double max_lat = 4;
  double min_lng = 5;
  double max_lng = 6;
}
```

### grpcurl Example

```bash
grpcurl -plaintext -d '{
  "collection": "places",
  "lat": 51.5074,
  "lng": -0.1278,
  "radius_meters": 5000,
  "top_k": 10
}' localhost:11024 mddb.MDDB/GeoSearch
```

---

## Curation RPCs

Curation rules (v2.9.14+) let editors pin specific documents to fixed positions and hide others for a given query, overriding organic ranking. Rules are persisted in the `curation` bucket and applied in the FTS and HybridSearch post-processing stages. Implemented in `services/mddbd/grpc_curation.go`.

### Message Types

```protobuf
message PinnedDocProto {
  string key = 1;
  string lang = 2;
  int32 position = 3;            // 1-based; <=0 means "append after organic results"
}

message CurationRuleProto {
  string id = 1;
  string collection = 2;
  string query = 3;
  string match_mode = 4;         // "exact" (default) or "contains"
  repeated PinnedDocProto pins = 5;
  repeated string hides = 6;     // document keys to drop
  bool enabled = 7;
  int64 created_at = 8;
  int64 updated_at = 9;
}
```

### ListCurationRules

Lists rules scoped to a collection, or all rules when `collection` is empty.

```protobuf
message ListCurationRulesRequest {
  string collection = 1;         // optional; "" = list all
}

message ListCurationRulesResponse {
  repeated CurationRuleProto rules = 1;
  int32 total = 2;
  string collection = 3;
}
```

### CreateCurationRule / UpdateCurationRule

Create a new rule, or replace an existing rule by `id`. Both return the stored `CurationRuleProto`.

```protobuf
message CreateCurationRuleRequest {
  CurationRuleProto rule = 1;
}

message UpdateCurationRuleRequest {
  CurationRuleProto rule = 1;
}
```

### DeleteCurationRule

Removes a rule by `id`.

```protobuf
message DeleteCurationRuleRequest {
  string id = 1;
}

message DeleteCurationRuleResponse {
  string status = 1;
  string id = 2;
}
```

### grpcurl Example

```bash
grpcurl -plaintext -d '{
  "rule": {
    "collection": "blog",
    "query": "getting started",
    "match_mode": "exact",
    "pins": [{"key": "quickstart", "lang": "en_US", "position": 1}],
    "hides": ["outdated-guide"],
    "enabled": true
  }
}' localhost:11024 mddb.MDDB/CreateCurationRule
```

---

## FTS Maintenance RPCs

### FTSReindex

Rebuilds the full-text index for a collection, re-applying language-aware stemming.

```protobuf
message FTSReindexRequest {
  string collection = 1;                         // collection to reindex
}

message FTSReindexResponse {
  string status = 1;                             // "ok"
  int32 reindexed = 2;                           // number of documents reindexed
  int32 skipped = 3;                             // number of documents skipped
}
```

### FTSLanguages

Lists the FTS languages supported by the server and its default language.

```protobuf
message FTSLanguagesRequest {}

message FTSLanguageInfo {
  string code = 1;                               // language code, e.g. "en", "pl"
  string name = 2;                               // language name, e.g. "English", "Polish"
}

message FTSLanguagesResponse {
  repeated FTSLanguageInfo languages = 1;
  string default_lang = 2;                       // server's default language
}
```

---

## Replication Service

Leader/follower replication is exposed as a separate gRPC service, `mddb.MDDBReplication`, on the same port. These RPCs are used by follower nodes and are normally not called by application clients. See [REPLICATION.md](REPLICATION.md) for setup, authentication, and operational details.

| RPC | Request → Response | Description |
|---|---|---|
| `RequestSnapshot` | `SnapshotRequest` → `stream SnapshotChunk` | Streams a full database snapshot to a follower |
| `StreamBinlog` | `StreamBinlogRequest` → `stream BinlogEntryProto` | Long-lived server stream of binlog entries from a given LSN |
| `ReplicationStatus` | `ReplicationStatusRequest` → `ReplicationStatusResponse` | Current replication state of this node (role, LSN, followers, lag) |
| `AcknowledgeLSN` | `AcknowledgeLSNRequest` → `AcknowledgeLSNResponse` | Follower confirms it has applied up to a given LSN |

```protobuf
message SnapshotRequest {
  string follower_id = 1;
  uint64 current_lsn = 2;
}

message SnapshotChunk {
  bytes data = 1;
  uint64 offset = 2;
  uint64 total_size = 3;
  bool is_last = 4;
  uint64 snapshot_lsn = 5;
}

message StreamBinlogRequest {
  string follower_id = 1;
  uint64 from_lsn = 2;
}

message BinlogEntryProto {
  uint64 lsn = 1;
  uint32 type = 2;
  int64 timestamp = 3;
  string bucket_name = 4;
  bytes key = 5;
  bytes value = 6;
  uint32 checksum = 7;
}

message ReplicationStatusRequest {}

message ReplicationStatusResponse {
  string node_id = 1;
  string role = 2;
  uint64 current_lsn = 3;
  string leader_addr = 4;
  int64 last_applied_at = 5;
  repeated FollowerInfo followers = 6;
  int64 replication_lag_ms = 7;
  uint64 binlog_oldest_lsn = 8;
  int64 binlog_size_bytes = 9;
}

message FollowerInfo {
  string follower_id = 1;
  uint64 confirmed_lsn = 2;
  int64 last_seen_at = 3;
  string address = 4;
  int64 lag_ms = 5;
}

message AcknowledgeLSNRequest {
  string follower_id = 1;
  uint64 confirmed_lsn = 2;
}

message AcknowledgeLSNResponse {
  bool ok = 1;
  uint64 leader_lsn = 2;
}
```

---

## Not Yet Available over gRPC

The following RPCs are declared in `proto/mddb.proto` but not yet implemented server-side. Calling them returns gRPC status `Unimplemented` — use the HTTP endpoints instead:

| RPC | HTTP alternative |
|---|---|
| `SpellSuggest` | `POST /v1/spell-suggest` |
| `SpellCleanup` | `POST /v1/spell-cleanup` |
| `TemporalQuery` | `POST /v1/temporal/query` |
| `TemporalHot` | `POST /v1/temporal/hot` |

---

## Performance Comparison

### Payload Size Example

**HTTP/JSON (Add Request)**:
```json
{
  "collection": "blog",
  "key": "hello-world",
  "lang": "en_US",
  "meta": {
    "category": ["blog", "tutorial"],
    "author": ["John Doe"]
  },
  "contentMd": "# Hello World\n\nWelcome to MDDB!"
}
```
Size: ~180 bytes

**gRPC/Protobuf (Same Request)**:
Binary representation: ~55 bytes (70% smaller)

### Benchmark Results

```
Operation          HTTP/JSON    gRPC      Improvement
─────────────────────────────────────────────────────
Add Document       2.5ms        0.8ms     3.1x faster
Get Document       1.8ms        0.5ms     3.6x faster
Search (10 docs)   5.2ms        1.4ms     3.7x faster
Bulk Add (100)     250ms        75ms      3.3x faster
```

## Best Practices

### Connection Management

```go
// ✅ Good: Reuse connections
var (
    conn   *grpc.ClientConn
    client pb.MDD BClient
)

func init() {
    var err error
    conn, err = grpc.Dial("localhost:11024",
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithBlock(),
    )
    if err != nil {
        log.Fatal(err)
    }
    client = pb.NewMDDBClient(conn)
}

// ❌ Bad: Create new connection for each request
func badExample() {
    conn, _ := grpc.Dial("localhost:11024", ...)
    defer conn.Close()
    client := pb.NewMDDBClient(conn)
    // Use client...
}
```

### Error Handling

```go
import "google.golang.org/grpc/status"

doc, err := client.Get(ctx, req)
if err != nil {
    st, ok := status.FromError(err)
    if ok {
        switch st.Code() {
        case codes.NotFound:
            log.Println("Document not found")
        case codes.InvalidArgument:
            log.Println("Invalid request:", st.Message())
        case codes.PermissionDenied:
            log.Println("Permission denied:", st.Message())
        default:
            log.Println("Error:", st.Message())
        }
    }
    return err
}
```

### Timeouts

```go
// Set reasonable timeouts
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

doc, err := client.Get(ctx, req)
```

### Metadata

```go
import "google.golang.org/grpc/metadata"

// Add metadata to request
md := metadata.Pairs(
    "client-id", "my-app",
    "version", "1.0.0",
)
ctx := metadata.NewOutgoingContext(context.Background(), md)

doc, err := client.Get(ctx, req)
```

## Generating Client Code

### Go

```bash
cd services/mddbd
./generate.sh
```

### Python

```bash
python -m grpc_tools.protoc \
  -I proto \
  --python_out=. \
  --grpc_python_out=. \
  proto/mddb.proto
```

### Node.js

```bash
npm install @grpc/grpc-js @grpc/proto-loader
# Use proto-loader to load .proto file at runtime
```

### Other Languages

See [gRPC documentation](https://grpc.io/docs/languages/) for language-specific guides.

## Debugging

### Enable Logging

```go
import "google.golang.org/grpc/grpclog"

grpclog.SetLoggerV2(grpclog.NewLoggerV2(os.Stdout, os.Stderr, os.Stderr))
```

### Use grpcurl

```bash
# List all methods
grpcurl -plaintext localhost:11024 list mddb.MDDB

# Get method details
grpcurl -plaintext localhost:11024 describe mddb.MDDB.Add

# Call with JSON
grpcurl -plaintext -d @ localhost:11024 mddb.MDDB/Add <<EOF
{
  "collection": "blog",
  "key": "test",
  "lang": "en_US",
  "content_md": "# Test"
}
EOF
```

### Reflection

The server has reflection enabled, allowing tools like grpcurl to discover services without .proto files.

## Migration from HTTP

### Side-by-Side

Both HTTP and gRPC APIs run simultaneously:
- HTTP: `localhost:11023`
- gRPC: `localhost:11024`

You can gradually migrate clients from HTTP to gRPC.

### API Parity

All HTTP endpoints have equivalent gRPC methods with the same functionality.

## See Also

- [API Documentation](API.md) - HTTP/JSON API reference
- [Examples](EXAMPLES.md) - More code examples
- [Protocol Buffers](https://protobuf.dev/) - Protobuf documentation
- [gRPC](https://grpc.io/) - gRPC documentation

## License

BSD 3-Clause License - see [LICENSE](https://github.com/tradik/mddb/blob/main/LICENSE) for details.
