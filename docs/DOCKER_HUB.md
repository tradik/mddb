---
title: "MDDB - High-Performance Markdown Database"
slug: "docs/docker-hub"
description: "MDDB on Docker Hub - a markdown document database with rich metadata, full revision history and both HTTP/JSON and gRPC protocols in one small image."
status: publish
---

# MDDB - High-Performance Markdown Database

![Performance](https://img.shields.io/badge/performance-29.8k%20docs%2Fs-brightgreen.svg)
![Go Version](https://img.shields.io/badge/Go-1.25-blue.svg)
![License](https://img.shields.io/badge/license-BSD--3--Clause-green.svg)

**The fastest markdown database with gRPC and HTTP/3 support**

MDDB is a specialized, high-performance database server designed for storing and managing markdown documents with rich metadata, full revision history, and dual protocol support (HTTP/JSON + gRPC/Protobuf).

## 🚀 Performance

- **29,810 docs/sec** throughput (37.4x baseline)
- **34µs** average latency
- **5.75x faster** than MongoDB
- **6.89x faster** than PostgreSQL
- **24.54x faster** than MySQL
- **95.43x faster** than CouchDB

## ⚡ Key Features

- **Dual Protocol Support**: HTTP/JSON REST API + gRPC/Protobuf
- **HTTP/3 Support**: QUIC protocol for extreme performance
- **Full Revision History**: Track all document changes with MVCC
- **Rich Metadata**: Store and query structured metadata
- **Batch Operations**: High-throughput batch insert/update/delete
- **Compression**: Snappy and Zstd compression support
- **Lock-Free Cache**: Sharded cache with zero contention
- **Adaptive Indexing**: Smart metadata indexing
- **Built-in Backup**: Hot backup and restore functionality
- **Embedded Database**: BoltDB for zero-dependency deployment

## 🐳 Quick Start

### Production Server

```bash
docker run -d \
  --name mddb \
  -p 11023:11023 \
  -p 11024:11024 \
  -v mddb-data:/data \
  -e MDDB_PATH=/data/mddb.db \
  -e MDDB_MODE=wr \
  -e MDDB_EXTREME=true \
  tradik/mddb:latest
```

### Docker Compose

```yaml
version: '3.8'

services:
  mddb:
    image: tradik/mddb:latest
    container_name: mddb-server
    ports:
      - "11023:11023"  # HTTP
      - "11024:11024"  # gRPC
      - "11443:11443"  # HTTP/3 (extreme mode)
    volumes:
      - mddb-data:/data
    environment:
      - MDDB_PATH=/data/mddb.db
      - MDDB_MODE=wr
      - MDDB_EXTREME=true
      - MDDB_HTTP_PORT=11023
      - MDDB_GRPC_PORT=11024
      - MDDB_HTTP3_PORT=11443
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:11023/stats"]
      interval: 30s
      timeout: 10s
      retries: 3

volumes:
  mddb-data:
```

## 🔧 Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `MDDB_PATH` | `/data/mddb.db` | Database file path |
| `MDDB_MODE` | `wr` | Mode: `ro` (read-only) or `wr` (read-write) |
| `MDDB_EXTREME` | `false` | Enable extreme performance mode |
| `MDDB_HTTP_PORT` | `11023` | HTTP server port |
| `MDDB_GRPC_PORT` | `11024` | gRPC server port |
| `MDDB_HTTP3_PORT` | `11443` | HTTP/3 server port (extreme mode) |

### Extreme Performance Mode

Enable with `MDDB_EXTREME=true` to activate:
- HTTP/3 server with QUIC protocol
- All 29 performance optimizations
- Lock-free cache with sharding
- Adaptive indexing
- Compression (Snappy + Zstd)
- Optimized batch processing
- String allocation elimination
- Zero-copy operations

## 📊 Available Tags

### Production Images
- `latest` - Latest stable release
- `2.18.6`, `2.18`, `2` - Specific version tags

### Platform Support
All images support:
- `linux/amd64` (Intel/AMD x86_64)
- `linux/arm64` (ARM/Apple Silicon aarch64)

## 🔌 API Examples

### HTTP REST API

```bash
# Add a document
curl -X POST http://localhost:11023/add \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "docs",
    "key": "welcome",
    "lang": "en",
    "meta": {
      "title": {"values": ["Welcome"]},
      "author": {"values": ["MDDB Team"]}
    },
    "content_md": "# Welcome to MDDB\n\nHigh-performance markdown database."
  }'

# Get a document
curl http://localhost:11023/get/docs/welcome/en

# Search documents
curl -X POST http://localhost:11023/search \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "docs",
    "filter_meta": {
      "author": {"values": ["MDDB Team"]}
    },
    "limit": 10
  }'

# Batch insert (high performance)
curl -X POST http://localhost:11023/batch \
  -H "Content-Type: application/json" \
  -d '{
    "collection": "docs",
    "documents": [
      {
        "key": "doc1",
        "lang": "en",
        "meta": {"title": {"values": ["Document 1"]}},
        "content_md": "# Document 1"
      },
      {
        "key": "doc2",
        "lang": "en",
        "meta": {"title": {"values": ["Document 2"]}},
        "content_md": "# Document 2"
      }
    ]
  }'

# Get statistics
curl http://localhost:11023/stats
```

### gRPC API

```go
import (
    "google.golang.org/grpc"
    "mddb/proto"
)

conn, _ := grpc.Dial("localhost:11024", grpc.WithInsecure())
client := proto.NewMDDBClient(conn)

// Add document
doc, _ := client.Add(ctx, &proto.AddRequest{
    Collection: "docs",
    Key:        "welcome",
    Lang:       "en",
    Meta: map[string]*proto.MetaValues{
        "title": {Values: []string{"Welcome"}},
    },
    ContentMd: "# Welcome to MDDB",
})

// Batch insert (high performance)
resp, _ := client.AddBatch(ctx, &proto.AddBatchRequest{
    Collection: "docs",
    Documents: []*proto.BatchDocument{
        {
            Key:  "doc1",
            Lang: "en",
            Meta: map[string]*proto.MetaValues{
                "title": {Values: []string{"Document 1"}},
            },
            ContentMd: "# Document 1",
        },
        // ... more documents
    },
})
```

## 🏗️ Architecture

```
┌─────────────────────────────────────────────────────┐
│                 Performance Layer                    │
│  • HTTP/3 Server (QUIC)                             │
│  • Lock-Free Cache (Sharded)                        │
│  • Batch Processor (Parallel + Single TX)           │
│  • String Optimization (Zero-Copy)                  │
│  • Compression (Snappy + Zstd)                      │
└─────────────────────────────────────────────────────┘
                         ↓
┌─────────────────────────────────────────────────────┐
│                  Protocol Layer                      │
│  • HTTP/JSON REST API                               │
│  • gRPC/Protobuf API                                │
│  • HTTP/3 (Extreme Mode)                            │
└─────────────────────────────────────────────────────┘
                         ↓
┌─────────────────────────────────────────────────────┐
│                   Storage Layer                      │
│  • BoltDB (Embedded)                                │
│  • MVCC (Multi-Version Concurrency Control)         │
│  • Adaptive Indexing                                │
│  • Revision History                                 │
└─────────────────────────────────────────────────────┘
```

## 📈 Benchmarks

Tested with 3,000 documents on identical hardware:

| Database | Throughput | Avg Latency | vs MDDB |
|----------|------------|-------------|---------|
| **MDDB (Batch)** | **25,863 docs/s** | **39µs** | **1.00x** |
| MongoDB | 5,452 docs/s | 182µs | 4.74x slower |
| PostgreSQL | 3,360 docs/s | 297µs | 7.69x slower |
| MySQL | 1,183 docs/s | 845µs | 21.86x slower |
| CouchDB | 310 docs/s | 3,201µs | 83.39x slower |

## 🔐 Security

- No authentication by default (add reverse proxy for production)
- Runs as non-root user in container
- Read-only mode available (`MDDB_MODE=ro`)
- Volume permissions: 750 (owner: mddbd)

## 📦 Volume Management

```bash
# Create named volume
docker volume create mddb-data

# Backup database (stop the server first: a copy of an open file is not a backup;
# the image's entrypoint is mddbd, so cp needs --entrypoint)
docker run --rm --entrypoint cp \
  -v mddb-data:/data \
  -v $(pwd)/backups:/backups \
  tradik/mddb:latest \
  /data/mddb.db /backups/mddb-backup-$(date +%Y%m%d).db

# Restore database
docker run --rm --entrypoint cp \
  -v mddb-data:/data \
  -v $(pwd)/backups:/backups \
  tradik/mddb:latest \
  /backups/mddb-backup-20250107.db /data/mddb.db
```

## 🛠️ Troubleshooting

### Check logs
```bash
docker logs mddb
```

### Check health
```bash
curl http://localhost:11023/stats
```

### Performance issues
- Enable extreme mode: `MDDB_EXTREME=true`
- Use batch operations for bulk inserts
- Use gRPC for better performance than HTTP
- Increase Docker resources (CPU/Memory)

### Database locked
- Ensure only one instance is running
- Check file permissions on volume
- Use read-only mode for multiple readers

## 📚 Documentation

- **GitHub**: https://github.com/tradik/mddb
- **API Documentation**: https://mddb.tradik.com/docs/api/
- **Examples**: https://mddb.tradik.com/docs/examples/
- **Changelog**: https://github.com/tradik/mddb/blob/main/CHANGELOG.md

## 🤝 Support

- **Issues**: https://github.com/tradik/mddb/issues
- **Discussions**: https://github.com/tradik/mddb/discussions
- **Email**: team@mddb.io

## 📄 License

BSD 3-Clause License - see [LICENSE](https://github.com/tradik/mddb/blob/main/LICENSE)

## 🌟 Why MDDB?

- **Performance First**: 29 optimizations for maximum throughput
- **Developer Friendly**: Simple API, easy deployment
- **Production Ready**: Battle-tested, stable, reliable
- **Zero Dependencies**: Embedded database, no external services
- **Modern Protocols**: HTTP/3, gRPC, Protobuf
- **Rich Features**: Metadata, revisions, search, backup
- **Open Source**: BSD 3-Clause licensed, community driven

---

**Made with ❤️ by the MDDB Team**

*Star us on [GitHub](https://github.com/tradik/mddb) if you find MDDB useful!*
