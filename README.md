# MDDB - Markdown Database

[![Go Version](https://img.shields.io/badge/Go-1.25-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![Build Status](https://img.shields.io/badge/build-passing-brightgreen.svg)]()
[![gRPC](https://img.shields.io/badge/gRPC-enabled-blue.svg)](https://grpc.io)
[![Protocol Buffers](https://img.shields.io/badge/protobuf-3-blue.svg)](https://protobuf.dev)

## Quick Start

### Prerequisites
- Go 1.25 or later
- Make (optional, for using Makefile commands)

### Installation

```bash
# Clone the repository
git clone <repository-url>
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
- **[API Documentation](docs/API.md)** - Complete HTTP/JSON API reference
- **[gRPC Documentation](docs/GRPC.md)** - High-performance gRPC API guide
- **[Docker Guide](docs/DOCKER.md)** - Docker deployment with Alpine Linux
- **[Usage Examples](docs/EXAMPLES.md)** - Code examples and patterns
- **[Architecture Guide](docs/ARCHITECTURE.md)** - System design and internals
- **[Deployment Guide](docs/DEPLOYMENT.md)** - Production deployment instructions

## ✨ Features

### Core Functionality
- **Document Management** - Add, update, and retrieve markdown documents with metadata
- **Revision History** - Every update creates a new revision with full content snapshot
- **Metadata Search** - Fast indexed search with multi-value metadata support
- **Multi-language** - Built-in support for multiple languages per document
- **Template Engine** - Variable substitution with `%%varName%%` syntax
- **Export** - Export documents as NDJSON or ZIP files with filtering
- **Backup/Restore** - Simple file-based backup and restore operations
- **Access Modes** - Read-only, write-only, or read-write modes

### Dual Protocol Support
- **HTTP/JSON API** - RESTful API on port 11023 (easy debugging, curl-friendly)
- **gRPC API** - Binary protocol on port 11024 (70% smaller payload, faster)
- **Automatic Compression** - HTTP/2 with gzip/deflate compression
- **Streaming** - gRPC streaming for large exports

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
│   └── php-extension/       # PHP extension
├── clients/                 # Client libraries
│   ├── python/              # Python client
│   │   ├── mddb_client/     # Generated code
│   │   └── example.py
│   ├── nodejs/              # Node.js client
│   │   ├── proto/           # Proto files
│   │   └── example.js
│   └── go/                  # Go client library
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

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

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