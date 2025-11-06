# gRPC API Documentation

> **Note**: The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in [RFC 2119](https://www.ietf.org/rfc/rfc2119.txt).

MDDB provides a high-performance gRPC API alongside the HTTP/JSON API. The gRPC API offers significant performance improvements through binary protocol buffers and HTTP/2.

## Table of Contents

- [Overview](#overview)
- [Why gRPC?](#why-grpc)
- [Getting Started](#getting-started)
- [Service Definition](#service-definition)
- [Client Examples](#client-examples)
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

The complete service definition is in [`proto/mddb.proto`](../services/mddbd/proto/mddb.proto).

### Available RPCs

```protobuf
service MDDB {
  rpc Add(AddRequest) returns (Document);
  rpc Get(GetRequest) returns (Document);
  rpc Search(SearchRequest) returns (SearchResponse);
  rpc Export(ExportRequest) returns (stream ExportChunk);
  rpc Backup(BackupRequest) returns (BackupResponse);
  rpc Restore(RestoreRequest) returns (RestoreResponse);
  rpc Truncate(TruncateRequest) returns (TruncateResponse);
  rpc Stats(StatsRequest) returns (StatsResponse);
}
```

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

MIT License - see [LICENSE](../LICENSE) for details.
