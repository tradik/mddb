# MDDB - Markdown Database

[![Go Version](https://img.shields.io/badge/Go-1.26-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-green.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/tradik/mddb)](https://github.com/tradik/mddb/releases)
[![Docker](https://img.shields.io/docker/v/tradik/mddb?label=docker)](https://hub.docker.com/r/tradik/mddb)
[![Docker Pulls](https://img.shields.io/docker/pulls/tradik/mddb)](https://hub.docker.com/r/tradik/mddb)
[![Tests](https://github.com/tradik/mddb/workflows/Tests/badge.svg)](https://github.com/tradik/mddb/actions)

**A high-performance, version-controlled markdown database with vector search, full-text search, webhooks, and triple protocol support (HTTP/JSON + gRPC/Protobuf + GraphQL)**

MDDB is a lightweight, embedded database specifically designed for storing and managing markdown documents with rich metadata. Built with Go and BoltDB, it provides blazing-fast document operations with full revision history, semantic vector search, and modern APIs.

## 🎯 What is MDDB?

MDDB treats markdown documents as first-class citizens, providing:

- **Native Markdown Support** - Store, version, and query markdown with metadata
- **Triple Protocol APIs** - HTTP/JSON (easy), gRPC (fast), or GraphQL (flexible)
- **Full Revision History** - Every update creates a new revision
- **Vector Search** - Semantic similarity with multiple algorithms (Flat, HNSW, IVF, PQ)
- **Full-Text Search** - Built-in inverted index with TF-IDF and BM25, typo tolerance, no external dependencies
- **Document TTL** - Auto-expiring documents like Redis
- **Webhooks** - HTTP callbacks on document events
- **Zero Configuration** - Single ~27MB binary, embedded database

**Perfect for:** Documentation platforms, content management, knowledge bases, RAG pipelines, configuration management, multi-language sites

## 🚀 Quick Start

### Docker Compose (Recommended) - Full Stack

Start all services with one command:

```bash
git clone https://github.com/tradik/mddb.git
cd mddb

# Production mode (all services)
docker compose up -d

# Development mode (with hot reload)
make dev-start

# Development + Ollama for embeddings
make dev-start-with-ollama
```

**Services started:**

| Service | Port | Image | Description |
|---------|------|-------|-------------|
| **mddbd** | 11023 (HTTP), 11024 (gRPC), 9000 (MCP) | `tradik/mddb:latest` | Database server |
| **mddb-panel** | 3000 | `tradik/mddb:panel` | React web admin UI |

### Docker - Individual Services

```bash
# MDDB Server only
docker run -d --name mddb \
  -p 11023:11023 -p 11024:11024 -p 9000:9000 \
  -v mddb-data:/data \
  tradik/mddb:latest

# Web Panel (connect to existing server)
docker run -d --name mddb-panel \
  -p 3000:3000 \
  -e VITE_MDDB_SERVER=host.docker.internal:11023 \
  tradik/mddb:panel

# MCP stdio mode (for Claude Desktop, Windsurf, etc.)
docker run -i --rm --network host \
  -v mddb-data:/app/data \
  -e MDDB_MCP_STDIO=true \
  tradik/mddb:latest

# Test it
curl http://localhost:11023/health
```

**Docker Hub:** https://hub.docker.com/r/tradik/mddb

### Install Binary

**Linux (Debian/Ubuntu):**
```bash
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-linux-amd64.deb
sudo dpkg -i mddbd-latest-linux-amd64.deb
sudo systemctl start mddbd
```

**macOS (Apple Silicon):**
```bash
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-darwin-arm64.tar.gz
tar xzf mddbd-latest-darwin-arm64.tar.gz
sudo mv mddbd-latest-darwin-arm64/mddbd /usr/local/bin/
mddbd
```

**CLI Client:**
```bash
# Linux
wget https://github.com/tradik/mddb/releases/latest/download/mddb-cli-latest-linux-amd64.deb
sudo dpkg -i mddb-cli-latest-linux-amd64.deb

# Usage
mddb-cli stats
mddb-cli add blog hello en_US -f post.md
mddb-cli search blog -f "tags=tutorial"
mddb-cli fts blog --query="getting started" --algorithm=bm25
```

**Other platforms:** See [Installation Guide](docs/INSTALLATION.md)

### Build from Source

```bash
git clone https://github.com/tradik/mddb.git
cd mddb
make build
./services/mddbd/mddbd
```

## 📦 Packages & Client Libraries

MDDB ships as a monorepo with multiple packages:

### Server & Tools

| Package | Language | Location | Description |
|---------|----------|----------|-------------|
| **mddbd** | Go | `services/mddbd/` | Database server (HTTP + gRPC + GraphQL + MCP) |
| **mddb-panel** | React/JS | `services/mddb-panel/` | Web admin panel |
| **mddb-cli** | Go | `services/mddb-cli/` | Command-line client with GraphQL support |

### Client Libraries (REST)

Zero-dependency HTTP clients - copy a single file into your project:

| Library | Language | Location | Install |
|---------|----------|----------|---------|
| **PHP Extension** | PHP 8.0+ | `services/php-extension/mddb.php` | Copy `mddb.php` into your project |
| **Python Extension** | Python 3.8+ | `services/python-extension/mddb.py` | Copy `mddb.py` into your project |

**PHP:**
```php
require_once 'mddb.php';
$db = mddb::connect('localhost:11023', 'write');
$db->collection('blog')->add('hello', 'en_US', ['author' => ['John']], '# Hello');
$results = $db->collection('blog')->vectorSearch('cancel subscription', 5, 0.7);
```

**Python:**
```python
from mddb import MDDB
db = MDDB.connect('localhost:11023', 'write').collection('blog')
db.add('hello', 'en_US', {'author': ['John']}, '# Hello')
results = db.vector_search('cancel subscription', top_k=5)
```

### Client Libraries (gRPC)

High-performance clients generated from Protocol Buffers:

| Library | Language | Location | Description |
|---------|----------|----------|-------------|
| **Go Client** | Go | `services/mddbd/proto/` | Native Go gRPC stubs |
| **Python gRPC** | Python | `clients/python/` | Generated Python gRPC client |
| **Node.js gRPC** | Node.js | `clients/nodejs/` | Uses `@grpc/grpc-js` |

Proto definitions at `proto/mddb.proto` - generate clients for any language supported by protobuf.

### Docker Images ([Docker Hub](https://hub.docker.com/r/tradik/mddb))

| Image | Size | Description |
|-------|------|-------------|
| `tradik/mddb:latest` | ~18MB | Database server with MCP built-in (Alpine) |
| `tradik/mddb:panel` | ~25MB | Web admin panel (Node Alpine) |
| `tradik/mddb:cli` | ~15MB | CLI client (Alpine) |

### System Packages

| Format | Platform | Contents |
|--------|----------|----------|
| `.deb` | Debian/Ubuntu | mddbd + systemd unit + man page |
| `.rpm` | RHEL/CentOS/Fedora | mddbd + systemd unit + man page |
| `.tar.gz` | Any (Linux, macOS, FreeBSD) | Standalone binary |

## 💡 Key Features

### Core Functionality
- ✅ **Document Management** - Full CRUD with metadata and collections
- ✅ **Revision History** - Complete version control with snapshots
- ✅ **Metadata Search** - Fast indexed queries with multi-value tags
- ✅ **Vector Search** - Semantic similarity with auto-embeddings
- ✅ **Full-Text Search** - Built-in inverted index with TF-IDF and BM25 scoring, typo tolerance
- ✅ **Document TTL** - Time-to-live with automatic cleanup
- ✅ **Webhooks** - HTTP callbacks on events with retry logic
- ✅ **Multi-language** - Same key, multiple languages
- ✅ **Schema Validation** - JSON Schema validation per collection

### APIs & Protocols
- ✅ **HTTP/JSON REST** - Easy debugging, extensive docs
- ✅ **gRPC/Protobuf** - 16x faster, 70% smaller payload
- ✅ **GraphQL** - Flexible queries, schema introspection, Playground
- ✅ **MCP Server** - Model Context Protocol for LLM integration
- ✅ **CLI Client** - Full-featured command-line with GraphQL support
- ✅ **Web Panel** - React UI with REST/GraphQL toggle

### Security & Access
- ✅ **Authentication** - JWT tokens and API keys
- ✅ **Authorization** - Collection-level RBAC (Read/Write/Admin)
- ✅ **User Management** - Multi-user with admin roles
- ✅ **Group Permissions** - Organize users into groups

### Replication & High Availability
- ✅ **Leader-Follower Replication** - Binlog streaming for read scaling
- ✅ **Automatic Catch-up** - Followers pull missing transactions
- ✅ **Zero-Downtime Snapshots** - Full sync for new followers
- ✅ **Cluster Monitoring** - Web panel with health and lag metrics

**[→ See all features](docs/FEATURES.md)** | **[→ Compare with alternatives](docs/COMPARISON.md)** | **[→ Performance benchmarks](docs/PERFORMANCE.md)**

## 🔄 Replication Architecture

MDDB supports leader-follower replication allowing you to scale read operations horizontally.

```mermaid
graph LR
    C[Clients] -->|Writes/Reads| L[Leader]
    C -->|Reads| F1[Follower 1]
    C -->|Reads| F2[Follower 2]
    L -->|gRPC StreamBinlog| F1
    L -->|gRPC StreamBinlog| F2
```

- **Leader**: Handles writes, maintains changes in a binary log, and streams them via gRPC.
- **Followers**: Read-only, pulls transactions, reconnects automatically.

**[→ Read Full Replication Guide](docs/REPLICATION.md)**

## 🎨 Web Admin Panel

Modern React-based UI for managing documents, users, and search with REST/GraphQL API toggle.

![MDDB Web Panel](docs/panel.png)

**Features:** Browse collections, view/edit documents, vector search, user management, API mode switching (REST ↔ GraphQL), live markdown preview.

**[→ Panel documentation](docs/PANEL.md)**

## 📖 Quick Examples

### Add and Retrieve Documents

```bash
# Add a document
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "key": "hello-world",
    "lang": "en_US",
    "meta": {"author": ["John"], "tags": ["tutorial"]},
    "contentMd": "# Hello World\n\nWelcome to MDDB!"
  }'

# Get document
curl -X POST http://localhost:11023/v1/get \
  -H 'Content-Type: application/json' \
  -d '{"collection": "blog", "key": "hello-world", "lang": "en_US"}'

# Search by metadata
curl -X POST http://localhost:11023/v1/search \
  -H 'Content-Type: application/json' \
  -d '{"collection": "blog", "filterMeta": {"tags": ["tutorial"]}, "limit": 10}'
```

### Vector Search (Semantic)

```bash
# Documents auto-embedded in background
# Search by meaning, not keywords
curl -X POST http://localhost:11023/v1/vector-search \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "kb",
    "query": "how do I cancel my subscription?",
    "topK": 5,
    "threshold": 0.7,
    "includeContent": true
  }'
```

### GraphQL

```bash
# Enable GraphQL
docker run -e MDDB_GRAPHQL_ENABLED=true -p 11023:11023 tradik/mddb

# Query
curl -X POST http://localhost:11023/graphql \
  -H 'Content-Type: application/json' \
  -d '{
    "query": "{ document(collection: \"blog\", key: \"hello-world\", lang: \"en\") { contentMd meta } }"
  }'

# Interactive Playground
open http://localhost:11023/playground
```

### CLI Client

```bash
# Install CLI
wget https://github.com/tradik/mddb/releases/latest/download/mddb-cli-latest-linux-amd64.deb
sudo dpkg -i mddb-cli-latest-linux-amd64.deb

# Use CLI
mddb-cli add blog hello en_US -f post.md -m "author=John,tags=tutorial"
mddb-cli get blog hello en_US
mddb-cli search blog -f "tags=tutorial"
mddb-cli fts blog --query="getting started"
mddb-cli stats
```

**[→ More examples](docs/API_QUICK_REFERENCE.md)** | **[→ Use case examples](docs/USE_CASES.md)** | **[→ Client libraries](docs/CLIENTS.md)**

## 📚 Documentation

**🌐 [Official Website](https://tradik.github.io/mddb/)** - Complete documentation, downloads, examples

### Getting Started
- **[Quick Start Guide](docs/QUICKSTART.md)** - 5-minute setup
- **[Installation Guide](docs/INSTALLATION.md)** - All platforms (Linux, macOS, FreeBSD, Windows)
- **[Use Cases](docs/USE_CASES.md)** - Real-world examples

### API Documentation
- **[HTTP/JSON API](docs/API.md)** - Complete REST API reference
- **[gRPC API](docs/GRPC.md)** - High-performance protocol guide
- **[GraphQL API](docs/GRAPHQL.md)** - Flexible query language
- **[OpenAPI/Swagger](docs/openapi.yaml)** - Machine-readable spec
- **[Swagger UI](docs/swagger.html)** - Interactive API docs

### Features & Guides
- **[Vector Search](docs/EMBEDDING_PROVIDERS.md)** - Semantic search setup (OpenAI, Cohere, Voyage, Ollama)
- **[RAG Pipeline](docs/RAG-PIPELINE.md)** - Complete RAG implementation guide
- **[Search Algorithms](docs/SEARCH.md)** - TF-IDF, BM25, Flat, HNSW, IVF, PQ
- **[Full-Text Search](docs/FTS.md)** - Built-in inverted index
- **[Webhooks](docs/WEBHOOKS.md)** - Event-driven integration
- **[Authentication](docs/AUTH.md)** - JWT & API keys, RBAC
- **[Web Panel](docs/PANEL.md)** - Admin UI guide
- **[LLM Connections](docs/LLM_CONNECTIONS.md)** - MCP for Claude, ChatGPT, Ollama, DeepSeek
- **[Bulk Import](docs/BULK-IMPORT.md)** - Load markdown folders

### Operations
- **[Docker Guide](docs/DOCKER.md)** - Container deployment
- **[Deployment](docs/DEPLOYMENT.md)** - Production setup
- **[Telemetry](docs/TELEMETRY.md)** - Prometheus metrics, Grafana
- **[Health Checks](docs/HEALTHCHECK.md)** - Docker & Kubernetes
- **[Performance](docs/PERFORMANCE.md)** - Benchmarks & tuning
- **[Architecture](docs/ARCHITECTURE.md)** - System design

### Development
- **[Client Libraries](docs/CLIENTS.md)** - PHP, Python, Go, Node.js
- **[Custom MCP Tools](docs/CUSTOM-TOOLS.md)** - YAML-defined AI tools
- **[Examples](docs/EXAMPLES.md)** - Code samples
- **[Contributing](CONTRIBUTING.md)** - Development guide
- **[Changelog](CHANGELOG.md)** - Version history

## 🏗️ Architecture

```
┌────────────────────────────────────────────────┐
│         Client Applications                    │
├──────────┬──────────┬──────────┬────────┬──────┤
│HTTP/JSON │gRPC/Proto│ GraphQL  │ HTTP/3 │  MCP  │
│  :11023  │  :11024  │ /graphql │ :11443 │ :9000 │
├──────────┴──────────┴──────────┴────────┴──────┤
│           MDDB Server (Go)                     │
│  • Vector Search (embeddings)                  │
│  • Full-Text Search (inverted index)           │
│  • Webhooks (retry logic)                      │
│  • JWT Auth + RBAC                             │
│  • Document TTL (auto-cleanup)                 │
├────────────────────────────────────────────────┤
│      BoltDB (Embedded ACID Storage)            │
│  • B+Tree index                                │
│  • Single-file database                        │
│  • MVCC transactions                           │
└────────────────────────────────────────────────┘
```

**[→ Detailed architecture](docs/ARCHITECTURE.md)**

## 🗺️ Roadmap

**[→ Full roadmap](docs/ROADMAP.md)**

## 🤝 Contributing

Contributions welcome! See **[CONTRIBUTING.md](CONTRIBUTING.md)** for guidelines.

**Security issues:** See **[SECURITY.md](SECURITY.md)**

## 📄 License

BSD 3-Clause License - see **[LICENSE](LICENSE)**

## 🔗 Quick Links

- **[GitHub](https://github.com/tradik/mddb)** - Source code
- **[Docker Hub](https://hub.docker.com/r/tradik/mddb)** - Container images
- **[Releases](https://github.com/tradik/mddb/releases)** - Download binaries
- **[Documentation](https://tradik.github.io/mddb/)** - Full docs
- **[LLM Connections](docs/LLM_CONNECTIONS.md)** - Claude, ChatGPT, Ollama, DeepSeek, Manus, Bielik.ai
- **[Issues](https://github.com/tradik/mddb/issues)** - Bug reports
