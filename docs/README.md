# MDDB Documentation

Welcome to the MDDB documentation! This guide will help you understand, deploy, and use MDDB effectively.

## 📖 Documentation Index

### Documentation Index

- **[Quick Start Guide](QUICKSTART.md)** - Get up and running in 5 minutes
- **[API Documentation](API.md)** - Complete HTTP/JSON API reference
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

MDDB (Markdown Database) is a lightweight, embedded database specifically designed for storing and managing markdown documents with metadata. It provides:

- **RESTful API** - Simple HTTP/JSON interface
- **Metadata Search** - Fast filtering and sorting
- **Version History** - Complete revision tracking
- **Multi-language** - Built-in language support
- **Template Engine** - Variable substitution
- **Easy Backup** - Simple backup/restore operations
- **Single Binary** - No external dependencies

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

### Storage
- **BoltDB** - Fast, embedded key-value store
- **ACID** - Transactional guarantees
- **Single File** - Easy to backup and move
- **No Setup** - No database server required

### API
- **RESTful** - Standard HTTP methods
- **JSON** - Simple request/response format
- **Versioned** - API version in URL path
- **Documented** - Complete API reference

### Search
- **Metadata Filtering** - Fast indexed searches
- **Sorting** - By date or key
- **Pagination** - Efficient large result sets
- **Boolean Logic** - AND/OR combinations

### Operations
- **Backup/Restore** - Simple file-based backups
- **Export** - NDJSON or ZIP formats
- **Truncate** - Manage revision history
- **Access Modes** - Read-only or read-write

## 📊 Performance

### Typical Performance
- **Reads**: 1000+ ops/sec
- **Writes**: 500+ ops/sec
- **Search**: 100+ queries/sec
- **Database Size**: Up to 10 GB recommended

### Scalability
- **Documents**: 10K - 1M documents
- **Document Size**: < 1 MB each
- **Concurrent Reads**: Unlimited
- **Concurrent Writes**: Single writer (BoltDB)

## 🔒 Security

### Current State
- No built-in authentication
- No authorization
- No encryption at rest
- No TLS/HTTPS

### Recommendations
- Use reverse proxy (Nginx, Caddy)
- Implement authentication at proxy level
- Use firewall rules
- Enable TLS at proxy
- Run on private network

See [Deployment Guide](DEPLOYMENT.md) for security hardening.

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
5. Update [CHANGELOG.md](../CHANGELOG.md)

## Standards & References

This documentation follows industry standards:

- **[RFC 2119](https://www.ietf.org/rfc/rfc2119.txt)** - Key words for use in RFCs to Indicate Requirement Levels
  
  The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in our documentation are to be interpreted as described in RFC 2119.

## 📝 License

See [LICENSE](../LICENSE) file for details.

## 🔗 Links

- [GitHub Repository](https://github.com/tradik/mddb)
- [Issue Tracker](https://github.com/tradik/mddb/issues)
- [Changelog](../CHANGELOG.md)

## 💡 Support

- Check documentation first
- Search existing issues
- Open new issue with details
- Include version and OS information

## 🗺️ Roadmap

### Planned Features
- Full-text search (Bleve/Meilisearch)
- Built-in authentication
- GraphQL API
- WebSocket support
- Replication
- Plugin system
- Compression
- Metrics/monitoring

See [CHANGELOG.md](../CHANGELOG.md) for version history.

---

**Happy documenting with MDDB!** 🚀
