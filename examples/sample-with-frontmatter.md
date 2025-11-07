---
title: Getting Started with MDDB
author: John Doe
category: tutorial
tags: database, markdown, golang
difficulty: beginner
date: 2024-01-15
version: 1.0
---

# Getting Started with MDDB

Welcome to MDDB - the high-performance markdown database!

## What is MDDB?

MDDB is a specialized database designed specifically for storing and managing markdown documents with rich metadata. It combines the simplicity of markdown with the power of a database.

## Key Features

- **Native Markdown Support** - Store markdown as first-class citizens
- **Version Control** - Full revision history for every document
- **Fast Search** - Indexed metadata for quick queries
- **Dual Protocol** - HTTP/JSON and gRPC/Protobuf APIs
- **High Performance** - 29 optimizations for extreme speed

## Quick Start

### Installation

```bash
# Using Docker
docker run -d -p 11023:11023 -p 11024:11024 tradik/mddb:latest

# Or build from source
git clone https://github.com/tradik/mddb.git
cd mddb
make build
make run
```

### Basic Usage

```bash
# Add a document
mddb-cli add blog hello en_US -f post.md

# Get a document
mddb-cli get blog hello en_US

# Search documents
mddb-cli search blog -f "category=tutorial"
```

## Next Steps

- Read the [API Documentation](../docs/API.md)
- Try the [Examples](../docs/EXAMPLES.md)
- Learn about [Performance](../docs/PERFORMANCE.md)

## Conclusion

MDDB makes it easy to manage markdown content at scale. Start building your markdown-powered applications today!
