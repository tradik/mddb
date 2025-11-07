# MDDB - Markdown Database

[![Go Version](https://img.shields.io/badge/Go-1.25-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-green.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/tradik/mddb)](https://github.com/tradik/mddb/releases)
[![Docker](https://img.shields.io/docker/v/tradik/mddb?label=docker)](https://hub.docker.com/r/tradik/mddb)
[![Docker Pulls](https://img.shields.io/docker/pulls/tradik/mddb)](https://hub.docker.com/r/tradik/mddb)
[![Tests](https://github.com/tradik/mddb/workflows/Tests/badge.svg)](https://github.com/tradik/mddb/actions)
[![Performance](https://img.shields.io/badge/performance-optimized-brightgreen.svg)]()
[![gRPC](https://img.shields.io/badge/gRPC-enabled-blue.svg)](https://grpc.io)
[![Protocol Buffers](https://img.shields.io/badge/protobuf-3-blue.svg)](https://protobuf.dev)

**A high-performance, version-controlled markdown database with dual protocol support (HTTP/JSON + gRPC/Protobuf)**

MDDB is a lightweight, embedded database specifically designed for storing and managing markdown documents with rich metadata. Built with Go and BoltDB, it provides blazing-fast document operations with full revision history, making it perfect for content management systems, documentation platforms, and knowledge bases.

## 🎯 What is MDDB?

MDDB (Markdown Database) is a specialized database server that treats markdown documents as first-class citizens. Unlike traditional databases that store markdown as plain text, MDDB provides:

- **Native Markdown Support** - Store, version, and query markdown documents with their metadata
- **Dual Protocol APIs** - Choose between HTTP/JSON (easy debugging) or gRPC/Protobuf (high performance)
- **Full Revision History** - Every document update creates a new revision with complete content snapshot
- **Rich Metadata Indexing** - Fast searches using multi-value metadata tags
- **Template Variables** - Dynamic content with variable substitution
- **Multi-language Support** - Store documents in multiple languages with the same key
- **Zero Configuration** - Single binary, embedded database, no external dependencies

## 🚀 Why MDDB?

MDDB is purpose-built for markdown document management. Here's how it compares to alternatives:

### vs Traditional Databases (PostgreSQL, MySQL)
- **Specialized for Markdown** - Native support with metadata indexing
- **Embedded** - No separate database server to manage
- **Built-in Versioning** - Automatic revision history
- **Simpler Deployment** - Single binary, ~15MB Docker image
- **Lower Resource Usage** - Minimal memory footprint

### vs Document Databases (MongoDB, CouchDB)
- **Markdown-First Design** - Purpose-built for markdown workflows
- **Dual Protocol** - HTTP/JSON for debugging, gRPC for performance
- **Smaller Footprint** - Embedded BoltDB, no separate server
- **Type-Safe gRPC** - Compile-time validation
- **Simpler Operations** - No sharding or replication complexity

### vs File-Based Systems (Git, Filesystem)
- **Instant Queries** - Indexed metadata searches
- **API Access** - REST + gRPC interfaces
- **Concurrent Access** - ACID transactions
- **Rich Metadata** - Structured tags and filtering
- **Better Performance** - Optimized for document operations

### vs CMS Platforms (WordPress, Strapi)
- **Lightweight** - Minimal installation size
- **API-First** - No admin UI overhead (optional web panel available)
- **Version Control** - Built-in revision tracking
- **Developer-Friendly** - Simple, well-documented API

## 💡 Use Cases

### 1. **Documentation Platforms**
```bash
# Store API documentation with versioning
mddb-cli add api-docs authentication en_US -f auth.md -m "version=2.0,status=published"
mddb-cli search api-docs -f "status=published" --sort updatedAt
```
**Perfect for**: Technical documentation, API references, knowledge bases

### 2. **Content Management Systems**
```bash
# Multi-language blog posts with metadata
mddb-cli add blog "getting-started" en_US -f post-en.md -m "author=John,tags=tutorial|beginner"
mddb-cli add blog "getting-started" pl_PL -f post-pl.md -m "author=John,tags=tutorial|beginner"
```
**Perfect for**: Blogs, news sites, multi-language content

### 3. **Configuration Management**
```bash
# Store configuration templates with variables
mddb-cli add configs nginx-prod en_US -f nginx.conf.md -m "env=production,service=web"
# Variables like {{domain}} are substituted on retrieval
```
**Perfect for**: Infrastructure configs, deployment templates

### 4. **Knowledge Bases**
```bash
# Searchable documentation with rich metadata
mddb-cli add kb troubleshooting en_US -f guide.md -m "category=support,difficulty=advanced,tags=network|vpn"
mddb-cli search kb -f "category=support,difficulty=beginner"
```
**Perfect for**: Internal wikis, support documentation, FAQs

### 5. **Microservices Communication**
```go
// High-performance gRPC for service-to-service communication
client := mddb.NewMDDBClient(conn)
doc, _ := client.Get(ctx, &mddb.GetRequest{
    Collection: "templates",
    Key: "email-welcome",
    Lang: "en_US",
})
```
**Perfect for**: Template storage, shared content, configuration distribution

### 6. **Version-Controlled Content**
```bash
# Track all changes with full history
mddb-cli add docs readme en_US -f README.md -m "version=1.0"
# Update creates new revision
mddb-cli add docs readme en_US -f README-v2.md -m "version=2.0"
# Access any revision through API
```
**Perfect for**: Legal documents, compliance, audit trails

## ⚡ Performance

MDDB is designed for high-performance document operations with multiple optimization strategies:

**Benchmark Results (3000 documents):**

| Database | Throughput | Avg Latency |
|----------|------------|-------------|
| **MDDB (Batch API)** | **29,810 docs/s** | **34µs** |
| MongoDB | 5,176 docs/s | 192µs |
| PostgreSQL | 4,324 docs/s | 231µs |
| MySQL | 1,214 docs/s | 822µs |
| CouchDB | 312 docs/s | 3,185µs |

### Key Performance Features

**Protocol & Storage:**
- Binary protocol (Protobuf) reduces payload size by ~70%
- Embedded BoltDB eliminates network overhead
- Batch operations use single transactions
- HTTP/2 multiplexing via gRPC

**Optimization Techniques:**
- Lock-free concurrent reads with sharded cache
- Optional revision history (save only when needed)
- Lazy metadata indexing with async queue
- Bloom filters for fast negative lookups
- Delta encoding for smaller revision storage
- Adaptive compression (Snappy/Zstd)

**Advanced Features (Extreme Mode):**
Enable with `MDDB_EXTREME=true` environment variable:
- Write-Ahead Log (WAL) with periodic sync
- MVCC snapshot isolation
- HTTP/3 + QUIC support
- Zero-copy I/O operations
- Vectorized operations (SIMD)

See [Performance Tests](test/README.md) for detailed benchmarks.

## 🎯 Key Features

### Core Functionality
- **Document Management** - Add, update, retrieve markdown with metadata
- **Revision History** - Every update creates a new revision with full content
- **Metadata Search** - Fast indexed search with multi-value tags
- **Multi-language** - Store same document in multiple languages
- **Template Variables** - Dynamic content with `{{variable}}` substitution
- **Collections** - Organize documents into logical groups

### APIs & Protocols
- **Dual Protocol Support** - HTTP/JSON and gRPC/Protobuf simultaneously
- **RESTful HTTP API** - Easy debugging with curl, Postman
- **High-Performance gRPC** - 16x faster, 70% smaller payload
- **gRPC Reflection** - Use grpcurl for debugging
- **CLI Client** - Full-featured command-line interface
- **Web Admin Panel** - Modern React-based UI for browsing and managing data

### Operations
- **Export** - NDJSON or ZIP formats with filtering
- **Backup/Restore** - Full database backup and restore
- **Truncate** - Remove old revisions to save space
- **Statistics** - Real-time server and database metrics
- **Access Modes** - Read-only, write-only, or read-write
- **Bulk Import** - Load entire folders of markdown files

### Developer Experience
- **Single Binary** - No external dependencies
- **Docker Support** - 15MB Alpine Linux images
- **Hot Reload** - Development mode with automatic restart
- **Monorepo Structure** - Shared protobuf definitions
- **Multi-language Clients** - Generated code for Go, Python, Node.js, PHP
- **Web Admin Panel** - Visual interface for data management
- **Comprehensive Docs** - API reference, examples, guides

## 🏗️ Architecture

```
┌─────────────────────────────────────────────────────┐
│              Client Applications                    │
├──────────────┬──────────────┬───────────────────────┤
│  HTTP/JSON   │ gRPC/Protobuf│   HTTP/3 (QUIC)      │
│  Port 11023  │  Port 11024  │   Port 11443         │
├──────────────┴──────────────┴───────────────────────┤
│              MDDB Server (Go)                       │
│  ┌─────────────────────────────────────────────┐   │
│  │ Performance Layer (Extreme Mode)            │   │
│  │ - WAL (Write-Ahead Log)                     │   │
│  │ - MVCC (Snapshot Isolation)                 │   │
│  │ - Lock-Free Cache (16 shards)               │   │
│  │ - Bloom Filters (1% FP)                     │   │
│  │ - Adaptive Compression (Snappy/Zstd)        │   │
│  │ - Delta Encoding (5-10x smaller)            │   │
│  │ - Adaptive Indexing                         │   │
│  │ - Async I/O                                 │   │
│  │ - Zero-Copy I/O                             │   │
│  │ - Vectorized Operations (SIMD)              │   │
│  │ - Distributed Sharding (4 shards)           │   │
│  └─────────────────────────────────────────────┘   │
│  ┌─────────────────────────────────────────────┐   │
│  │ Core Layer                                  │   │
│  │ - Request Handling                          │   │
│  │ - Batch Processing (parallel)               │   │
│  │ - Metadata Indexing (lazy)                  │   │
│  │ - Template Processing                       │   │
│  │ - Revision Management                       │   │
│  └─────────────────────────────────────────────┘   │
├─────────────────────────────────────────────────────┤
│           BoltDB (Embedded Storage)                 │
│  - ACID Transactions                                │
│  - B+Tree Storage                                   │
│  - Single File Database                             │
│  - Optimized: NoFreelistSync, 100MB initial mmap    │
└─────────────────────────────────────────────────────┘
```

### Extreme Performance Mode

Enable with `MDDB_EXTREME=true` environment variable to activate all 29 optimizations.

## Quick Start

### 🐳 Docker (Recommended)

```bash
# Pull and run the latest version
docker run -d \
  --name mddb \
  -p 11023:11023 \
  -p 11024:11024 \
  -v mddb-data:/data \
  -e MDDB_EXTREME=true \
  tradik/mddb:latest

# Or use Docker Compose
curl -O https://raw.githubusercontent.com/tradik/mddb/main/docker-compose.yml
docker-compose up -d
```

**Docker Hub**: https://hub.docker.com/r/tradik/mddb

### Installation

#### Ubuntu/Debian
```bash
# Server
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-linux-amd64.deb
sudo dpkg -i mddbd-latest-linux-amd64.deb
sudo systemctl start mddbd
sudo systemctl enable mddbd

# Client
wget https://github.com/tradik/mddb/releases/latest/download/mddb-cli-latest-linux-amd64.deb
sudo dpkg -i mddb-cli-latest-linux-amd64.deb
```

#### RHEL/CentOS/Fedora
```bash
# Server
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-linux-amd64.rpm
sudo rpm -i mddbd-latest-linux-amd64.rpm
sudo systemctl start mddbd
sudo systemctl enable mddbd

# Client
wget https://github.com/tradik/mddb/releases/latest/download/mddb-cli-latest-linux-amd64.rpm
sudo rpm -i mddb-cli-latest-linux-amd64.rpm
```

#### macOS (Homebrew)
```bash
# Coming soon - Homebrew tap
brew tap tradik/mddb
brew install mddbd mddb-cli

# Or download directly
# Intel Mac
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-darwin-amd64.tar.gz
tar xzf mddbd-latest-darwin-amd64.tar.gz
sudo mv mddbd-latest-darwin-amd64/mddbd /usr/local/bin/

# Apple Silicon
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-darwin-arm64.tar.gz
tar xzf mddbd-latest-darwin-arm64.tar.gz
sudo mv mddbd-latest-darwin-arm64/mddbd /usr/local/bin/
```

#### FreeBSD
```bash
wget https://github.com/tradik/mddb/releases/latest/download/mddbd-latest-freebsd-amd64.tar.gz
tar xzf mddbd-latest-freebsd-amd64.tar.gz
sudo mv mddbd-latest-freebsd-amd64/mddbd /usr/local/bin/
```

### Building from Source

#### Prerequisites
- Go 1.25 or later
- Make (optional, for using Makefile commands)

```bash
# Clone the repository
git clone https://github.com/tradik/mddb.git
cd mddb

# Build the project
make build

# Or build manually
cd services/mddbd
go build -o mddbd .
```

### Running

```bash
# Run with default settings
make run

# Run in production mode
make run-prod

# Run in development mode with hot reload (requires air)
make install-dev-tools
make dev

# Generate gRPC code (if you modify proto files)
make install-grpc-tools
make generate-proto
```

**Ports:**
- HTTP API: `localhost:11023`
- gRPC API: `localhost:11024`
- HTTP/3 (QUIC): `localhost:11443` (Extreme Mode only)

### Docker

```bash
# Production
make docker-up

# Development (with hot reload)
make docker-up-dev

# View logs
make docker-logs

# Stop
make docker-down
```

**Image size**: ~15 MB (Alpine Linux)

### CLI Client

```bash
# Build and install CLI client
make build-cli
make install-all

# Use the CLI
mddb-cli add blog hello en_US -f post.md
mddb-cli get blog hello en_US
mddb-cli search blog

# View manual
man mddb-cli
```

### Available Make Commands

Run `make help` to see all available commands:

```bash
make help          # Show all available commands
make build         # Build the Go service
make build-cli     # Build CLI client
make install-all   # Install CLI and man page
make test          # Run tests
make fmt           # Format code
make lint          # Run linter
make clean         # Clean build artifacts
make tidy          # Tidy Go modules
```

## 📚 Documentation

- **[Quick Start Guide](docs/QUICKSTART.md)** - Get started in 5 minutes
- **[Web Panel Guide](docs/PANEL.md)** - Web admin interface documentation
- **[Bulk Import Guide](docs/BULK-IMPORT.md)** - Import markdown files from folders
- **[API Documentation](docs/API.md)** - Complete HTTP/JSON API reference
- **[gRPC Documentation](docs/GRPC.md)** - High-performance gRPC API guide
- **[Docker Guide](docs/DOCKER.md)** - Docker deployment with Alpine Linux
- **[Usage Examples](docs/EXAMPLES.md)** - Code examples and patterns
- **[Architecture Guide](docs/ARCHITECTURE.md)** - System design and internals
- **[Deployment Guide](docs/DEPLOYMENT.md)** - Production deployment instructions

## 🎨 Example Workflows

### Blog Platform
```bash
# Add a blog post with tags
echo "# Getting Started with MDDB" | mddb-cli add blog intro en_US \
  -m "author=Jane,tags=tutorial|beginner,status=published"

# Search published posts
mddb-cli search blog -f "status=published" --sort updatedAt

# Export all blog posts
curl "http://localhost:11023/v1/export?collection=blog&format=zip" -o blog-backup.zip
```

### API Documentation
```bash
# Store versioned API docs
mddb-cli add api-docs auth-v2 en_US -f authentication.md \
  -m "version=2.0,endpoint=/api/auth,method=POST"

# Quick search by endpoint
mddb-cli search api-docs -f "endpoint=/api/auth"
```

### Multi-language Content
```bash
# Same key, different languages
mddb-cli add products laptop-x1 en_US -f laptop-en.md -m "category=electronics,price=999"
mddb-cli add products laptop-x1 pl_PL -f laptop-pl.md -m "category=electronics,price=999"
mddb-cli add products laptop-x1 de_DE -f laptop-de.md -m "category=electronics,price=999"

# Retrieve in user's language
mddb-cli get products laptop-x1 pl_PL
```

## 🔧 Technical Details

### Storage Engine
- **BoltDB** - Embedded key-value store (single file)
- **Prefix Indices** - Fast metadata queries using composite keys
- **ACID Transactions** - Guaranteed data consistency
- **Efficient Storage** - Optimized bucket structure for performance

### API Endpoints
- `POST /v1/add` - Add or update documents
- `POST /v1/get` - Retrieve documents with template support
- `POST /v1/search` - Search with metadata filters and sorting
- `POST /v1/export` - Export as NDJSON or ZIP
- `GET /v1/backup` - Create database backup
- `POST /v1/restore` - Restore from backup
- `POST /v1/truncate` - Clean up old revisions
- `GET /v1/stats` - Server and database statistics

### Extensions
- **Webhooks** - HTTP callbacks after add/update operations
- **System Commands** - Execute commands after operations
- **Configurable** - Environment-based configuration

### Command-Line Client
- **mddb-cli** - Full-featured CLI client similar to mysql-client
- **Man Page** - Complete Unix-style manual page
- **Bash Completion** - Tab completion support (future)
- **Piping Support** - Works seamlessly with Unix pipes

## 🚀 Quick Examples

### Add a Document
```bash
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "key": "hello-world",
    "lang": "en_US",
    "meta": {"category": ["blog"], "author": ["John Doe"]},
    "contentMd": "# Hello World\n\nWelcome to MDDB!"
  }'
```

### Get Document with Template Variables
```bash
curl -X POST http://localhost:11023/v1/get \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "key": "homepage",
    "lang": "en_GB",
    "env": {"year": "2024", "siteName": "My Blog"}
  }'
```

### Search with Filters
```bash
curl -X POST http://localhost:11023/v1/search \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "filterMeta": {"category": ["blog"]},
    "sort": "addedAt",
    "asc": false,
    "limit": 10
  }'
```

### Export as NDJSON
```bash
curl -X POST http://localhost:11023/v1/export \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "filterMeta": {"category": ["blog"]},
    "format": "ndjson"
  }' > export.ndjson
```

### Backup Database
```bash
curl "http://localhost:11023/v1/backup?to=backup-$(date +%s).db"
```

### Truncate Old Revisions
```bash
curl -X POST http://localhost:11023/v1/truncate \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "keepRevs": 3,
    "dropCache": true
  }'
```

### Using CLI Client

```bash
# Add document from file
mddb-cli add blog hello en_US -f post.md -m "category=blog,author=John"

# Get document
mddb-cli get blog hello en_US

# Search with filter
mddb-cli search blog -f "category=blog" -l 10

# Export to file
mddb-cli export blog -o backup.ndjson

# Create backup
mddb-cli backup daily-backup.db

# Show statistics
mddb-cli stats
```

### Using Web Admin Panel

```bash
# Install dependencies
make panel-install

# Run in development mode
make panel-dev

# Or use Docker Compose
docker-compose up -d

# Access panel at http://localhost:3000
```

**Features:**
- 📊 Server statistics dashboard
- 📁 Browse collections and documents
- 🔍 Advanced filtering by metadata
- 📄 View document content and metadata
- ✏️ Edit documents with live markdown preview
- ➕ Create new documents
- 📝 Split-view markdown editor (edit/preview/both)
- 🎨 Markdown toolbar with formatting buttons
- 💡 Syntax highlighting for code blocks
- 📋 Pre-built templates (blog, docs, README, API)
- 📋 Copy markdown content
- 🎨 Modern, responsive UI

### Bulk Import from Folder

```bash
# Load all .md files from a folder
./scripts/load-md-folder.sh ./docs blog

# Load recursively with custom language
./scripts/load-md-folder.sh ./content articles -r -l pl_PL

# Add custom metadata to all files
./scripts/load-md-folder.sh ./posts blog -m "author=John Doe" -m "status=published"

# Dry run to preview what would be imported
./scripts/load-md-folder.sh ./docs blog -d

# Verbose output with progress
./scripts/load-md-folder.sh ./docs blog -v
```

The folder loader script automatically:
- Generates unique keys from filenames
- Extracts frontmatter metadata (YAML-style)
- Supports recursive folder scanning
- Shows progress with statistics
- Handles errors gracefully

## 🗺️ Roadmap

### Planned Features
- **Full-Text Search** - Integration with Bleve or Meilisearch
- **Authentication** - Built-in API key and JWT support
- **Authorization** - Collection-level access control
- **Schema Validation** - JSON Schema validation for metadata
- **Streaming Export** - Memory-efficient ZIP export
- **GraphQL API** - GraphQL endpoint alongside REST
- **Replication** - Built-in replication support
- **Plugins** - Plugin system for custom extensions

## 📁 Monorepo Structure

```
mddb/
├── proto/                    # Shared Protocol Buffer definitions
│   ├── mddb.proto           # Main service definition
│   ├── generate.sh          # Code generation for all languages
│   └── README.md
├── services/
│   ├── mddbd/               # Go server
│   │   ├── main.go
│   │   ├── grpc_server.go
│   │   └── proto/           # Generated Go code
│   ├── mddb-cli/            # CLI client
│   ├── mddb-panel/          # Web admin panel (React)
│   │   ├── src/             # React components
│   │   ├── public/          # Static assets
│   │   └── Dockerfile       # Docker build
│   └── php-extension/       # PHP extension
├── clients/                 # Client libraries
│   ├── python/              # Python client
│   │   ├── mddb_client/     # Generated code
│   │   └── example.py
│   ├── nodejs/              # Node.js client
│   │   ├── proto/           # Proto files
│   │   └── example.js
│   └── go/                  # Go client library
├── scripts/                 # Utility scripts
│   └── load-md-folder.sh    # Bulk import script
├── examples/                # Example files
└── docs/                    # Documentation
```

### Shared Protobuf

All services and clients use the same Protocol Buffer definitions from `proto/`:
- **Single source of truth** for API contracts
- **Automatic code generation** for multiple languages
- **Version control** for API changes
- **Type safety** across all implementations

Generate code for all languages:
```bash
make generate-proto
```

## 🤝 Contributing

Contributions are welcome! Please:
1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. **Regenerate proto code** if you modify `proto/mddb.proto`
6. Update documentation
7. Submit a pull request

See [CHANGELOG.md](CHANGELOG.md) for version history.

## 📄 License

This project is licensed under the BSD 3-Clause License - see the [LICENSE](LICENSE) file for details.

## 🔗 Links

- [Documentation](docs/)
- [API Reference](docs/API.md)
- [Examples](docs/EXAMPLES.md)
- [Changelog](CHANGELOG.md)

## 📚 Standards & References

This project follows industry standards and best practices:

- **[RFC 2119](https://www.ietf.org/rfc/rfc2119.txt)** - Key words for use in RFCs to Indicate Requirement Levels
  - Defines the meaning of MUST, SHOULD, MAY, etc. used in our documentation
  - Ensures consistent interpretation of requirement levels across all specifications