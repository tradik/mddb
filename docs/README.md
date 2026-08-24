---
title: "MDDB Documentation"
slug: "docs/readme"
description: "Index of the MDDB documentation: quick start, API and protocol references, search, security, deployment guides and use-case walkthroughs."
status: publish
---

# MDDB Documentation

This directory contains the documentation for MDDB.

## Published Site

The documentation is published at: https://mddb.tradik.com/docs/readme/

Every `.md` file in this directory is rendered to a static page by the SSG
build (`make docs-build`) and deployed to Cloudflare Pages. A file's URL comes
from its front-matter `slug` — e.g. `QUICKSTART.md` (`slug: docs/quickstart`)
is served at https://mddb.tradik.com/docs/quickstart/.

Welcome to the MDDB documentation! This guide will help you understand, deploy, and use MDDB effectively.

## 📖 Documentation Index

### Documentation Index

- **[Quick Start Guide](QUICKSTART.md)** - Get up and running in 5 minutes
- **[API Documentation](API.md)** - Complete HTTP/JSON API reference
- **[OpenAPI/Swagger Specification](/docs/api/swagger/)** - Machine-readable API spec (OpenAPI 3.0)
- **[Swagger UI](/docs/api/swagger/)** - Interactive API documentation
- **[Health Check Guide](HEALTHCHECK.md)** - Health checks for Docker, Kubernetes, and load balancers
- **[gRPC Documentation](GRPC.md)** - High-performance gRPC API guide
- **[Usage Examples](EXAMPLES.md)** - Code examples and integration patterns
- **[Architecture Guide](ARCHITECTURE.md)** - System design and internals
- **[Deployment Guide](DEPLOYMENT.md)** - Production deployment instructions
  - Error handling
  - Data models
  - Best practices

### Guides
- **[Usage Examples](EXAMPLES.md)** - Practical examples and patterns
  - Basic operations
  - Advanced search queries
  - Export and backup
  - Shell scripts
  - Client libraries (Node.js, Python, Go)

- **[Architecture Guide](ARCHITECTURE.md)** - System design and internals
  - High-level architecture
  - Storage layer details
  - Data flow diagrams
  - Design decisions
  - Performance characteristics
  - Scalability considerations

- **[Deployment Guide](DEPLOYMENT.md)** - Production deployment
  - System requirements
  - Systemd service setup
  - Docker deployment
  - Reverse proxy configuration
  - Backup strategies
  - Monitoring and troubleshooting
  - Security hardening
  - Scaling strategies

## 🚀 Quick Links

### For New Users
1. Start with [Quick Start Guide](QUICKSTART.md)
2. Try the examples in [Usage Examples](EXAMPLES.md)
3. Read [API Documentation](API.md) for details

### For Developers
1. Understand the [Architecture](ARCHITECTURE.md)
2. Review [API Documentation](API.md)
3. Check [Usage Examples](EXAMPLES.md) for integration patterns

### For DevOps
1. Follow [Deployment Guide](DEPLOYMENT.md)
2. Set up monitoring and backups
3. Review security recommendations

## 📚 What is MDDB?

MDDB (Markdown Database) is an **AI-native embedded document database** for markdown content. Single ~29 MB binary, embedded BoltDB storage, zero external dependencies. Core capabilities at a glance (full list in [FEATURES.md](FEATURES.md) and the root [README.md](../README.md)):

- **Triple protocol** — HTTP/JSON REST, gRPC/Protobuf, GraphQL, all over TCP or Unix Domain Sockets
- **Built-in [MCP server](MCP.md)** — 67 tools, MCP 2025-11-25 compliant, stdio + Streamable HTTP + SSE transports for Claude / Cursor / Windsurf / ChatGPT / Ollama / DeepSeek
- **[Vector / semantic search](SEARCH.md)** — 7 index algorithms (Flat / HNSW / IVF / PQ / OPQ / SQ / BQ) with per-collection int8/int4 quantization; OpenAI / Ollama / Cohere / Voyage embeddings
- **[Full-text search](SEARCH.md)** — TF-IDF / BM25 / BM25F / PMISparse, 7 modes (simple / boolean / phrase / wildcard / proximity / range / fuzzy), 18-language stemming, typo tolerance
- **[Hybrid search](RAG-PIPELINE.md)** — sparse BM25 + dense vector via alpha blending or Reciprocal Rank Fusion
- **[Geosearch](GEOSEARCH.md)** — R-tree + geohash radius / bounding-box queries, composable with FTS and vector
- **[Memory RAG](RAG-PIPELINE.md)** — conversational memory with session management and semantic recall
- **Multi-format upload** — `.md`, `.txt`, `.html`, `.pdf`, `.docx`, `.odt`, `.rtf`, `.tex`, `.yaml` auto-converted to Markdown; URL import; Wikipedia XML dump streaming
- **[Authentication](AUTHENTICATION.md)** — JWT, API keys, per-collection RBAC, per-protocol access modes
- **[TLS / mTLS](TLS.md)** — built-in HTTPS with optional client certificate authentication
- **[Replication](REPLICATION.md)** — leader-follower binlog streaming for read scaling
- **[Automation](AUTOMATIONS.md)** — triggers, crons, webhooks, sentiment analysis, template variables
- **Document TTL** with auto-expiry, full **revision history**, **schema validation**, **aggregations** (facets + histograms)
- **[Web Admin Panel](PANEL.md)** — React UI for documents, users, search, geo, settings

## 🎯 Common Use Cases

### Content Management
- Blog posts and articles
- Documentation systems
- Knowledge bases
- Static site generators

### Multi-language Content
- Internationalized websites
- Multi-region documentation
- Localized marketing content

### Version Control
- Content approval workflows
- Change tracking
- Audit trails
- Point-in-time recovery

## 🔧 Key Features

The full feature matrix is maintained in [FEATURES.md](FEATURES.md). Storage internals (buckets, data flow, design decisions) are in [ARCHITECTURE.md](ARCHITECTURE.md). Don't duplicate them here.

## 📊 Performance

Numbers, methodology and benchmarks live in their own document — see **[BENCHMARK.md](BENCHMARK.md)** or run the bench suite under `tools/bench/`. This index intentionally does not pin throughput targets that would drift from reality.

## 🔒 Security

For the security model (the layers a request passes through, trust boundaries, what's deliberately out of scope) see the [Security Model](ARCHITECTURE.md#security-model) section in ARCHITECTURE.md. For practical setup:

- **[AUTHENTICATION.md](AUTHENTICATION.md)** — JWT, API keys, RBAC, group permissions
- **[TLS.md](TLS.md)** — HTTPS + mTLS setup, openssl recipes, deployment patterns
- **[config.md](config.md#unix-domain-socket-transport)** — Unix Domain Socket transport
- **[DEPLOYMENT.md](DEPLOYMENT.md)** — production hardening checklist

Version-by-version changes for any of these layers live in [CHANGELOG.md](https://github.com/tradik/mddb/blob/main/CHANGELOG.md), not here.

## 🛠️ Development

### Building from Source
```bash
git clone <repository-url>
cd mddb
make build
```

### Running Tests
```bash
make test
make test-coverage
```

### Development Mode
```bash
make install-dev-tools
make dev
```

### Code Quality
```bash
make fmt    # Format code
make lint   # Run linter
make tidy   # Tidy modules
```

## 📦 Installation Methods

### Binary Release
Download from releases page and run:
```bash
./mddbd
```

### Build from Source
```bash
make build
make run
```

### Docker
```bash
docker run -p 11023:11023 -v mddb-data:/data mddb:latest
```

### Docker Compose
```bash
docker-compose up -d
```

## 🤝 Contributing

Contributions are welcome! Please:
1. Read the [Architecture Guide](ARCHITECTURE.md)
2. Follow Go best practices
3. Add tests for new features
4. Update documentation
5. Update [CHANGELOG.md](https://github.com/tradik/mddb/blob/main/CHANGELOG.md)

## Standards & References

This documentation follows industry standards:

- **[RFC 2119](https://www.ietf.org/rfc/rfc2119.txt)** - Key words for use in RFCs to Indicate Requirement Levels
  
  The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in our documentation are to be interpreted as described in RFC 2119.

## 📝 License

See [LICENSE](https://github.com/tradik/mddb/blob/main/LICENSE) file for details.

## 🔗 Links

- [GitHub Repository](https://github.com/tradik/mddb)
- [Issue Tracker](https://github.com/tradik/mddb/issues)
- [Changelog](https://github.com/tradik/mddb/blob/main/CHANGELOG.md)

## 💡 Support

- Check documentation first
- Search existing issues
- Open new issue with details
- Include version and OS information

## 🗺️ What's next

Past releases live in [CHANGELOG.md](https://github.com/tradik/mddb/blob/main/CHANGELOG.md). Requests and proposals live in [issues](https://github.com/tradik/mddb/issues) and [discussions](https://github.com/tradik/mddb/discussions/categories/ideas).


---

**Happy documenting with MDDB!** 🚀
