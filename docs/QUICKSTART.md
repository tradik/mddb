# MDDB Quick Start Guide

## Installation

### Prerequisites
- Go 1.25 or later
- Make (optional)

### Build from Source

```bash
# Clone the repository
git clone <repository-url>
cd mddb

# Build using Make
make build

# Or build manually
cd services/mddbd
go build -o mddbd .
```

## Running the Server

### Using Make

```bash
# Run with default settings
make run

# Run in development mode with hot reload
make install-dev-tools
make dev

# Run in production mode
make run-prod
```

### Manual Start

```bash
cd services/mddbd

# Default settings (read-write mode, port 11023)
./mddbd

# Custom configuration
MDDB_ADDR=":8080" MDDB_MODE="wr" MDDB_PATH="data.db" ./mddbd
```

## First Steps

### 1. Add Your First Document

```bash
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "key": "hello-world",
    "lang": "en_US",
    "meta": {
      "category": ["blog"],
      "author": ["Your Name"]
    },
    "contentMd": "# Hello World\n\nThis is my first document in MDDB!"
  }'
```

### 2. Retrieve the Document

```bash
curl -X POST http://localhost:11023/v1/get \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "key": "hello-world",
    "lang": "en_US"
  }'
```

### 3. Search Documents

```bash
curl -X POST http://localhost:11023/v1/search \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "limit": 10
  }'
```

### 4. Create a Backup

```bash
curl "http://localhost:11023/v1/backup?to=my-backup.db"
```

## Common Use Cases

### Blog/CMS Backend

```bash
# Add a blog post
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "posts",
    "key": "my-first-post",
    "lang": "en_US",
    "meta": {
      "category": ["tech"],
      "tags": ["golang", "database"],
      "status": ["published"],
      "publishDate": ["2024-01-15"]
    },
    "contentMd": "# My First Post\n\nContent here..."
  }'

# Get all published posts
curl -X POST http://localhost:11023/v1/search \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "posts",
    "filterMeta": {"status": ["published"]},
    "sort": "updatedAt",
    "asc": false
  }'
```

### Documentation System

```bash
# Add documentation page
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "docs",
    "key": "getting-started",
    "lang": "en_US",
    "meta": {
      "section": ["introduction"],
      "version": ["1.0"]
    },
    "contentMd": "# Getting Started\n\nWelcome to our documentation..."
  }'
```

### Multi-language Content

```bash
# English version
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "pages",
    "key": "about",
    "lang": "en_US",
    "meta": {"type": ["page"]},
    "contentMd": "# About Us\n\nWe are..."
  }'

# Polish version
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "pages",
    "key": "about",
    "lang": "pl_PL",
    "meta": {"type": ["page"]},
    "contentMd": "# O Nas\n\nJesteśmy..."
  }'
```

## Configuration Examples

### Read-Only Mode (for read replicas)

```bash
MDDB_MODE="read" MDDB_ADDR=":11024" ./mddbd
```

### Custom Port and Database Path

```bash
MDDB_ADDR=":8080" MDDB_PATH="/data/mddb.db" ./mddbd
```

## Testing the Installation

```bash
# Run tests
make test

# Run with coverage
make test-coverage

# Format code
make fmt

# Run linter
make lint
```

## Next Steps

- Read the [API Documentation](API.md) for detailed endpoint information
- Check [Examples](EXAMPLES.md) for more usage patterns
- Review [Architecture](ARCHITECTURE.md) to understand the internals
- See [Deployment Guide](DEPLOYMENT.md) for production setup

## Troubleshooting

### Port Already in Use

```bash
# Check what's using the port
lsof -i :11023

# Use a different port
MDDB_ADDR=":11024" ./mddbd
```

### Database Locked

```bash
# Make sure no other instance is running
ps aux | grep mddbd

# Remove lock if necessary (only if no other instance is running)
rm mddb.db-lock
```

### Permission Denied

```bash
# Make binary executable
chmod +x mddbd

# Check database file permissions
ls -la mddb.db
```

## Getting Help

- Check the [API Documentation](API.md)
- Review [Examples](EXAMPLES.md)
- Read the [Architecture Guide](ARCHITECTURE.md)
- Open an issue on GitHub
