---
title: "MDDB API Documentation"
slug: "docs/api"
description: "REST API reference for MDDB: document CRUD, collections, metadata queries, search, revision history and bulk operations over HTTP/JSON."
status: publish
---

# MDDB API Documentation

> **Note**: The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in [RFC 2119](https://www.ietf.org/rfc/rfc2119.txt).

## Table of Contents
- [Overview](#overview)
- [Configuration](#configuration)
- [Endpoints](#endpoints)
  - [POST /v1/add](#post-v1add)
  - [POST /v1/add-batch](#post-v1add-batch)
  - [POST /v1/bulk-ingest-job](#post-v1bulk-ingest-job)
  - [POST /v1/ingest](#post-v1ingest)
  - [POST /v1/upload](#post-v1upload)
  - [POST /v1/get](#post-v1get)
  - [POST /v1/search](#post-v1search)
  - [POST /v1/vector-search](#post-v1vector-search)
  - [POST /v1/vector-reindex](#post-v1vector-reindex)
  - [GET /v1/vector-stats](#get-v1vector-stats)
  - [POST /v1/classify](#post-v1classify)
  - [PATCH /v1/update](#patch-v1update)
  - [GET /v1/doc-meta](#get-v1doc-meta)
  - [POST /v1/fts](#post-v1fts)
  - [POST /v1/fts-reindex](#post-v1fts-reindex)
  - [GET /v1/fts-languages](#get-v1fts-languages)
  - [GET /v1/autocomplete](#get-v1autocomplete)
  - [POST /v1/synonyms](#post-v1synonyms)
  - [GET /v1/synonyms](#get-v1synonyms)
  - [DELETE /v1/synonyms](#delete-v1synonyms)
  - [POST /v1/export](#post-v1export)
  - [GET /v1/backup](#get-v1backup)
  - [POST /v1/restore](#post-v1restore)
  - [POST /v1/truncate](#post-v1truncate)
  - [GET /v1/stats](#get-v1stats)
  - [POST /v1/schema/set](#post-v1schemaset)
  - [POST /v1/schema/get](#post-v1schemaget)
  - [POST /v1/schema/delete](#post-v1schemadelete)
  - [POST /v1/schema/list](#post-v1schemalist)
  - [POST /v1/validate](#post-v1validate)
  - [POST /v1/auth/login](#post-v1authlogin)
  - [POST /v1/auth/api-key](#post-v1authapi-key)
  - [GET /v1/auth/api-keys](#get-v1authapi-keys)
  - [DELETE /v1/auth/api-keys/:keyHash](#delete-v1authapi-keyskeyhash)
  - [POST /v1/delete](#post-v1delete)
  - [POST /v1/delete-batch](#post-v1delete-batch)
  - [POST /v1/delete-collection](#post-v1delete-collection)
  - [POST /v1/hybrid-search](#post-v1hybrid-search)
  - [POST /v1/cross-search](#post-v1cross-search)
  - [POST /v1/find-duplicates](#post-v1find-duplicates)
  - [POST /v1/aggregate](#post-v1aggregate)
  - [GET /v1/collection-config](#get-v1collection-config)
  - [GET /v1/collection-configs](#get-v1collection-configs)
  - [GET /v1/embedding-configs](#get-v1embedding-configs)
  - [GET/PUT/DELETE /v1/embedding-configs/:id](#getputdelete-v1embedding-configsid)
  - [POST /v1/embedding-configs/set-default](#post-v1embedding-configsset-default)
  - [GET/POST/DELETE /v1/stopwords](#getpostdelete-v1stopwords)
  - [GET/POST /v1/webhooks](#getpost-v1webhooks)
  - [POST /v1/webhooks/delete](#post-v1webhooksdelete)
  - [POST /v1/revisions](#post-v1revisions)
  - [POST /v1/revisions/restore](#post-v1revisionsrestore)
  - [GET/POST /v1/automation](#getpost-v1automation)
  - [GET/PUT/DELETE /v1/automation/:id](#getputdelete-v1automationid)
  - [GET /v1/automation-logs](#get-v1automation-logs)
  - [POST /v1/import-url](#post-v1import-url)
  - [POST /v1/import-wiki](#post-v1import-wiki)
  - [POST /v1/set-ttl](#post-v1set-ttl)
  - [GET /v1/meta-keys](#get-v1meta-keys)
  - [GET /v1/checksum](#get-v1checksum)
  - [GET /v1/system/info](#get-v1systeminfo)
  - [GET /v1/config](#get-v1config)
  - [GET /v1/endpoints](#get-v1endpoints)
  - [POST /v1/memory/session](#post-v1memorysession)
  - [POST /v1/memory/message](#post-v1memorymessage)
  - [POST /v1/memory/recall](#post-v1memoryrecall)
  - [POST /v1/memory/summarize](#post-v1memorysummarize)
  - [POST /v1/memory/sessions](#post-v1memorysessions)
  - [POST /v1/memory/history](#post-v1memoryhistory)
  - [GET /health](#get-health)
  - [POST /v1/auth/register](#post-v1authregister)
  - [GET /v1/auth/me](#get-v1authme)
  - [GET/POST /v1/auth/permissions](#getpost-v1authpermissions)
  - [GET /v1/auth/users](#get-v1authusers)
  - [DELETE /v1/auth/users/:username](#delete-v1authusersusername)
  - [GET/POST /v1/auth/groups](#getpost-v1authgroups)
  - [GET/PUT/DELETE /v1/auth/groups/:name](#getputdelete-v1authgroupsname)
  - [GET/POST /v1/auth/group-permissions](#getpost-v1authgroup-permissions)
- [Data Models](#data-models)
- [Error Handling](#error-handling)

## Overview

MDDB is a lightweight markdown database server built with Go and BoltDB. It provides a RESTful API for storing, retrieving, and managing markdown documents with metadata.

**Base URL**: `http://localhost:11023`

**API Version**: `v1`

## Configuration

The server can be configured using environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `MDDB_ADDR` | `:11023` | Server address and port |
| `MDDB_MODE` | `wr` | Access mode: `read`, `write`, or `wr` (read+write). Also: `--mode` flag, `database.mode` in YAML |
| `MDDB_PATH` | `mddb.db` | Path to the BoltDB database file. Also: `--db` flag, `database.path` in YAML |
| `MDDB_EMBEDDING_PROVIDER` | `none` | Embedding provider: `openai`, `ollama`, `voyage`, or `none` |
| `MDDB_EMBEDDING_API_KEY` | | API key for OpenAI or Voyage AI |
| `MDDB_EMBEDDING_API_URL` | *(per provider)* | API base URL (see [Vector Search](#vector-search-configuration)) |
| `MDDB_EMBEDDING_MODEL` | *(per provider)* | Embedding model name |
| `MDDB_EMBEDDING_DIMENSIONS` | *(per provider)* | Vector dimensions |
| `MDDB_FTS_STEMMING` | `true` | Enable stemming for FTS |
| `MDDB_FTS_DEFAULT_LANG` | `en` | Default language for FTS stemming and stop words (18 languages supported) |
| `MDDB_FTS_SYNONYMS` | `true` | Enable synonym expansion for FTS |
| `MDDB_COMPRESSION_ENABLED` | `true` | Enable adaptive compression (Snappy/Zstd) |
| `MDDB_COMPRESSION_SMALL_THRESHOLD` | `1024` | Snappy compression threshold (bytes) |
| `MDDB_COMPRESSION_MEDIUM_THRESHOLD` | `10240` | Zstd compression threshold (bytes) |

### Access Modes

- **`read`**: Read-only mode. Write operations will return `403 Forbidden`
- **`write`**: Write-only mode (not commonly used)
- **`wr`**: Read and write mode (recommended for most use cases)

## Endpoints

### POST /v1/add

Add or update a markdown document in a collection.

**Request Body**:
```json
{
  "collection": "blog",
  "key": "homepage",
  "lang": "en_GB",
  "meta": {
    "category": ["blog", "featured"],
    "author": ["John Doe"],
    "tags": ["golang", "database"]
  },
  "contentMd": "# Welcome\n\nThis is the homepage content."
}
```

**Response**:
```json
{
  "id": "blog|homepage|en_gb",
  "key": "homepage",
  "lang": "en_GB",
  "meta": {
    "category": ["blog", "featured"],
    "author": ["John Doe"],
    "tags": ["golang", "database"]
  },
  "contentMd": "# Welcome\n\nThis is the homepage content.",
  "addedAt": 1699296000,
  "updatedAt": 1699296000
}
```

**Features**:
- Creates a new document or updates an existing one
- Automatically generates a deterministic ID based on collection, key, and lang
- Maintains revision history
- Updates metadata indices
- Tracks `addedAt` (first creation) and `updatedAt` (last modification) timestamps

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "key": "homepage",
    "lang": "en_GB",
    "meta": {
      "category": ["blog"]
    },
    "contentMd": "# Welcome to my blog"
  }'
```

---

### POST /v1/add-batch

Add multiple documents to a collection in a single request. Uses the optimized batch processor for high throughput. Fires all post-commit hooks (embedding, FTS indexing, webhooks, TTL, automation triggers).

**Request Body**:
```json
{
  "collection": "blog",
  "documents": [
    {
      "key": "post1",
      "lang": "en",
      "contentMd": "# Post 1\n\nFirst post content.",
      "meta": {
        "category": ["blog"],
        "author": ["John Doe"]
      },
      "saveRevision": true
    },
    {
      "key": "post2",
      "lang": "en",
      "contentMd": "# Post 2\n\nSecond post content.",
      "meta": {
        "category": ["tutorial"]
      }
    }
  ]
}
```

**Parameters**:
- `collection` (required): Collection name
- `documents` (required): Array of documents to add
  - `key` (required): Document key
  - `lang` (required): Language code
  - `contentMd` (required): Markdown content
  - `meta` (optional): Metadata key-value pairs
  - `saveRevision` (optional): Whether to save a revision for this document

**Response**:
```json
{
  "added": 1,
  "updated": 1,
  "failed": 0,
  "errors": []
}
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/add-batch \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "documents": [
      {"key": "p1", "lang": "en", "contentMd": "# Hello"},
      {"key": "p2", "lang": "en", "contentMd": "# World"}
    ]
  }'
```

---

### POST /v1/bulk-ingest-job

Queue a long-running bulk ingest job and return immediately with a job ID. Poll `/v1/bulk-ingest-job/{id}` or supply `callbackUrl` for a webhook on completion. Jobs are processed by a single FIFO worker in chunks of 500 documents.

**Request Body**: same shape as `/v1/add-batch`, plus optional `callbackUrl`.

**Response**: HTTP 202 with `{id, collection, status: "pending", total, submittedAt}`.

**Companion endpoints**:
- `GET /v1/bulk-ingest-job/{id}` — status poll
- `DELETE /v1/bulk-ingest-job/{id}` — cancel (pending jobs only)
- `GET /v1/bulk-ingest-jobs?collection=X` — list all jobs, newest first

See [BULK-IMPORT.md](BULK-IMPORT.md#async-bulk-ingest-with-job-tracking) for full usage and semantics.

---

### POST /v1/ingest

Bulk ingest endpoint with advanced features for scraping pipelines and data import workflows. Supports URL key derivation, YAML frontmatter extraction, content deduplication, auto-metadata injection, and collection auto-configuration.

**Request Body**:
```json
{
  "collection": "imported",
  "documents": [
    {
      "url": "https://example.com/page1",
      "lang": "en",
      "contentMd": "# Page 1\n\nContent here.",
      "scraper": "my-crawler",
      "scrapedAt": 1709500000,
      "ttl": 86400
    },
    {
      "url": "https://example.com/page2",
      "lang": "en",
      "contentMd": "---\ntitle: Page 2\ncategory: tutorial\n---\n# Page 2\n\nMore content.",
      "extractFrontmatter": true
    }
  ],
  "options": {
    "skipDuplicates": true,
    "autoConfigureCollection": true
  }
}
```

**Parameters**:
- `collection` (required): Collection name
- `documents` (required): Array of documents to ingest
  - `url` (optional): Source URL — used for key derivation and auto-injected as `source_url` metadata
  - `key` (optional): Document key — if empty, derived from URL
  - `lang` (required): Language code
  - `contentMd` (required): Markdown content
  - `meta` (optional): Metadata key-value pairs
  - `extractFrontmatter` (optional): Parse YAML frontmatter from content and merge into metadata
  - `scrapedAt` (optional): Unix timestamp of when the content was scraped — auto-injected as `scraped_at` metadata
  - `scraper` (optional): Scraper identifier — auto-injected as `scraper` metadata
  - `ttl` (optional): Time-to-live in seconds
- `options` (optional): Ingest options
  - `skipDuplicates` (optional): Skip documents whose content hasn't changed (CRC32 hash comparison)
  - `skipEmbeddings` (optional): Skip embedding generation for this batch
  - `skipFts` (optional): Skip FTS indexing for this batch
  - `skipWebhooks` (optional): Skip webhook firing for this batch
  - `autoConfigureCollection` (optional): Auto-configure collection as "scraping" type if it doesn't exist
  - `saveRevision` (optional): Save revision history for all documents in this batch

**Response**:
```json
{
  "added": 2,
  "updated": 0,
  "skipped": 0,
  "failed": 0,
  "errors": [],
  "collection": "imported",
  "durationMs": 45
}
```

**Features**:
- **URL key derivation**: If `key` is empty, a deterministic key is derived from the URL path
- **Frontmatter extraction**: When `extractFrontmatter` is true, YAML frontmatter is parsed from content and merged into metadata (request metadata takes priority over frontmatter)
- **Auto-metadata injection**: `source_url`, `scraped_at`, and `scraper` fields are auto-injected into document metadata
- **Content deduplication**: With `skipDuplicates`, existing documents with identical content (CRC32 hash) are skipped
- **Collection auto-configuration**: With `autoConfigureCollection`, the collection is created with type "scraping" if it doesn't exist
- **Selective hook control**: Skip embeddings, FTS, or webhooks per batch via options

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/ingest \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "imported",
    "documents": [
      {"url": "https://example.com/page1", "lang": "en", "contentMd": "# Hello", "scraper": "my-crawler"},
      {"url": "https://example.com/page2", "lang": "en", "contentMd": "# World", "extractFrontmatter": true}
    ],
    "options": {"autoConfigureCollection": true, "skipDuplicates": true}
  }'
```

---

### POST /v1/upload

Upload files via multipart/form-data. Files are auto-converted to Markdown and stored as documents. Supports single and batch upload.

**Content-Type**: `multipart/form-data`

**Form Fields**:
- `file` or `files[]` (required): One or more files to upload. Supported formats: `.md`, `.txt`, `.html`, `.htm`, `.pdf`, `.docx`, `.odt`, `.rtf`, `.yaml`, `.yml`, `.log`, `.lex`, `.tex`, `.latex`
- `collection` (required): Target collection name
- `lang` (required): Document language code (e.g. `en_US`, `pl_PL`)
- `key` (optional): Document key — if empty, derived from filename (lowercase, spaces→hyphens, extension stripped)
- `meta` (optional): JSON-encoded metadata map, e.g. `{"category":["docs"]}`
- `ttl` (optional): Time-to-live in seconds (0 = no expiry)
- `maxSize` (optional): Per-file size limit in bytes (default: 10MB, max: 100MB)

**Format Conversion**:
| Format | Extension | Conversion |
|--------|-----------|------------|
| Markdown | `.md` | Stored as-is, frontmatter extracted |
| Plain text | `.txt` | Stored as-is, frontmatter extracted |
| HTML | `.html`, `.htm` | Converted to Markdown (headings, links, lists, bold/italic preserved) |
| PDF | `.pdf` | Text extracted (text-based PDFs only; scanned/image PDFs not supported — use Docling) |
| DOCX | `.docx` | Text extracted with headings and list structure preserved |
| ODT | `.odt` | OpenDocument text extracted with headings preserved |
| RTF | `.rtf` | Rich Text Format — text extracted, formatting stripped |
| LaTeX | `.tex`, `.latex` | Converted to Markdown (sections, formatting, environments, math preserved) |
| YAML | `.yaml`, `.yml` | Wrapped in code block for structured data |
| Log | `.log` | Wrapped in code block |
| LEX | `.lex` | Wrapped in code block |

**Auto-injected Metadata**:
- `upload_format`: Original file format (e.g. `pdf`, `html`, `docx`)
- `upload_filename`: Original filename
- `upload_converted`: `"true"` if file was converted from non-markdown format

**Single File Response**:
```json
{
  "key": "report-2026-q1",
  "format": "pdf",
  "converted": true,
  "document": {
    "id": "doc|docs|report-2026-q1",
    "key": "report-2026-q1",
    "lang": "en_US",
    "meta": {
      "upload_format": ["pdf"],
      "upload_filename": ["report-2026-q1.pdf"],
      "upload_converted": ["true"]
    },
    "contentMd": "# Q1 2026 Report\n\nExtracted text content...",
    "addedAt": 1710000000,
    "updatedAt": 1710000000
  }
}
```

**Batch Response** (multiple files):
```json
{
  "added": 3,
  "updated": 0,
  "failed": 0,
  "errors": [],
  "results": [
    {"key": "doc1", "format": "pdf", "converted": true, "document": {...}},
    {"key": "doc2", "format": "html", "converted": true, "document": {...}},
    {"key": "doc3", "format": "txt", "converted": false, "document": {...}}
  ]
}
```

**cURL Examples**:
```bash
# Single file upload
curl -X POST http://localhost:11023/v1/upload \
  -F "file=@report.pdf" \
  -F "collection=docs" \
  -F "lang=en_US"

# With custom key and metadata
curl -X POST http://localhost:11023/v1/upload \
  -F "file=@manual.docx" \
  -F "collection=docs" \
  -F "key=user-manual" \
  -F "lang=en_US" \
  -F 'meta={"category":["documentation"],"type":["manual"]}'

# Batch upload
curl -X POST http://localhost:11023/v1/upload \
  -F "files[]=@doc1.pdf" \
  -F "files[]=@doc2.html" \
  -F "files[]=@doc3.txt" \
  -F "collection=docs" \
  -F "lang=en_US"

# With custom size limit (50MB)
curl -X POST http://localhost:11023/v1/upload \
  -F "file=@large-manual.pdf" \
  -F "collection=docs" \
  -F "lang=en_US" \
  -F "maxSize=52428800"
```

**MCP Tool**: `upload_file` — accepts base64-encoded file content with filename for format detection.

---

### POST /v1/get

Retrieve a specific document by collection, key, and language.

**Request Body**:
```json
{
  "collection": "blog",
  "key": "homepage",
  "lang": "en_GB",
  "env": {
    "year": "2024",
    "siteName": "My Blog"
  }
}
```

**Response**:
```json
{
  "id": "blog|homepage|en_gb",
  "key": "homepage",
  "lang": "en_GB",
  "meta": {
    "category": ["blog"]
  },
  "contentMd": "# Welcome to My Blog in 2024",
  "addedAt": 1699296000,
  "updatedAt": 1699296000
}
```

**Features**:
- Retrieves the latest version of a document
- Supports templating via `env` parameter
- Template variables in content are replaced: `%%varName%%` → value from `env`

**Template Example**:

If your content contains:
```markdown
# Welcome to %%siteName%% in %%year%%
```

And you provide:
```json
{
  "env": {
    "year": "2024",
    "siteName": "My Blog"
  }
}
```

The response will contain:
```markdown
# Welcome to My Blog in 2024
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/get \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "key": "homepage",
    "lang": "en_GB",
    "env": {"year": "2024"}
  }'
```

---

### POST /v1/search

Search for documents in a collection with optional metadata filtering and sorting.

**Request Body**:
```json
{
  "collection": "blog",
  "filterMeta": {
    "category": ["blog", "tutorial"],
    "author": ["John Doe"]
  },
  "sort": "updatedAt",
  "asc": false,
  "limit": 10,
  "offset": 0
}
```

**Parameters**:
- `collection` (required): Collection name
- `filterMeta` (optional): Metadata filters (AND between keys, OR between values)
- `sort` (optional): Sort field - `addedAt`, `updatedAt`, or `key`
- `asc` (optional): Sort order - `true` for ascending, `false` for descending
- `limit` (optional): Maximum number of results (default: 50)
- `offset` (optional): Number of results to skip (default: 0)

**Response**:
```json
[
  {
    "id": "blog|post1|en_gb",
    "key": "post1",
    "lang": "en_GB",
    "meta": {
      "category": ["blog"],
      "author": ["John Doe"]
    },
    "contentMd": "# Post 1",
    "addedAt": 1699296000,
    "updatedAt": 1699296100
  },
  {
    "id": "blog|post2|en_gb",
    "key": "post2",
    "lang": "en_GB",
    "meta": {
      "category": ["tutorial"],
      "author": ["John Doe"]
    },
    "contentMd": "# Post 2",
    "addedAt": 1699295000,
    "updatedAt": 1699296200
  }
]
```

**Filtering Logic**:
- Multiple values for the same key are combined with OR
- Multiple keys are combined with AND
- Example: `{"category": ["blog", "tutorial"], "author": ["John"]}` means:
  - (category = "blog" OR category = "tutorial") AND (author = "John")

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/search \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "filterMeta": {"category": ["blog"]},
    "sort": "addedAt",
    "asc": true,
    "limit": 10
  }'
```

---

### POST /v1/vector-search

Perform semantic (vector) search using natural language queries. Documents are automatically embedded when added (if an embedding provider is configured). The search finds documents by meaning, not just exact metadata matches.

**Request Body**:
```json
{
  "collection": "docs",
  "query": "how to authenticate users",
  "topK": 5,
  "threshold": 0.3,
  "filterMeta": {
    "category": ["tutorial"]
  },
  "includeContent": true
}
```

**Parameters**:
- `collection` (required): Collection name
- `query` (required*): Natural language search query (will be embedded server-side)
- `queryVector` (optional*): Pre-computed embedding vector (use instead of `query`)
- `topK` (optional): Maximum results to return (default: 5)
- `threshold` (optional): Minimum similarity score 0.0-1.0 (default: 0.0)
- `filterMeta` (optional): Metadata pre-filter (same logic as `/v1/search`)
- `includeContent` (optional): Include `contentMd` in results (default: false)

\* Either `query` or `queryVector` is required.

**Response**:
```json
{
  "results": [
    {
      "document": {
        "id": "docs|auth-guide|en_us",
        "key": "auth-guide",
        "lang": "en_US",
        "meta": {"category": ["tutorial"]},
        "contentMd": "# Authentication Guide\n...",
        "addedAt": 1709136000,
        "updatedAt": 1709136000
      },
      "score": 0.89,
      "rank": 1
    },
    {
      "document": {
        "id": "docs|login-flow|en_us",
        "key": "login-flow",
        "lang": "en_US",
        "meta": {"category": ["tutorial"]},
        "contentMd": "# Login Flow\n...",
        "addedAt": 1709135000,
        "updatedAt": 1709135000
      },
      "score": 0.74,
      "rank": 2
    }
  ],
  "total": 2,
  "model": "text-embedding-3-small",
  "dimensions": 1536
}
```

**Response Fields**:
- `results`: Array of matched documents with similarity scores
  - `document`: Full document object
  - `score`: Cosine similarity score (0.0-1.0, higher = more similar)
  - `rank`: Position in results (1-based)
- `total`: Number of results returned
- `model`: Embedding model used
- `dimensions`: Vector dimensionality

**How It Works**:
1. When a document is added via `/v1/add`, its content is automatically embedded in the background
2. The query text is embedded using the same model
3. Cosine similarity is computed between the query vector and all document vectors
4. Results are ranked by similarity score
5. If `filterMeta` is provided, only documents matching the metadata filter are searched (hybrid search)

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/vector-search \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "docs",
    "query": "how to authenticate users",
    "topK": 5,
    "includeContent": true
  }'
```

---

### POST /v1/vector-reindex

Re-embed all documents in a collection. Useful after changing the embedding provider/model, or for initial indexing of existing documents.

**Request Body**:
```json
{
  "collection": "docs",
  "force": false
}
```

**Parameters**:
- `collection` (required): Collection name
- `force` (optional): If `true`, re-embed all documents regardless of content changes. If `false`, skip documents whose content hasn't changed (default: false)

**Response**:
```json
{
  "embedded": 42,
  "skipped": 8,
  "failed": 0,
  "errors": []
}
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/vector-reindex \
  -H 'Content-Type: application/json' \
  -d '{"collection": "docs", "force": false}'
```

---

### GET /v1/vector-stats

Get embedding/vector search statistics.

**Response**:
```json
{
  "enabled": true,
  "provider": "text-embedding-3-small",
  "model": "text-embedding-3-small",
  "dimensions": 1536,
  "index_ready": true,
  "collections": {
    "docs": {
      "total_documents": 50,
      "embedded_documents": 48
    },
    "blog": {
      "total_documents": 120,
      "embedded_documents": 120
    }
  }
}
```

**cURL Example**:
```bash
curl http://localhost:11023/v1/vector-stats
```

---

### Vector Search Configuration

#### Embedding Providers

| Provider | `MDDB_EMBEDDING_PROVIDER` | Default Model | Default Dimensions | API Key Required |
|----------|--------------------------|---------------|-------------------|-----------------|
| OpenAI | `openai` | `text-embedding-3-small` | 1536 | Yes |
| Voyage AI (Anthropic) | `voyage` | `voyage-3` | 1024 | Yes |
| Ollama (local) | `ollama` | `nomic-embed-text` | 768 | No |
| Disabled | `none` or empty | - | - | - |

#### Provider-Specific Configuration

**OpenAI**:
```bash
MDDB_EMBEDDING_PROVIDER=openai
MDDB_EMBEDDING_API_KEY=sk-...
MDDB_EMBEDDING_API_URL=https://api.openai.com/v1    # default
MDDB_EMBEDDING_MODEL=text-embedding-3-small          # default
MDDB_EMBEDDING_DIMENSIONS=1536                        # default
```

**Voyage AI (Anthropic)**:
```bash
MDDB_EMBEDDING_PROVIDER=voyage
MDDB_EMBEDDING_API_KEY=pa-...
MDDB_EMBEDDING_API_URL=https://api.voyageai.com/v1   # default
MDDB_EMBEDDING_MODEL=voyage-3                         # default
MDDB_EMBEDDING_DIMENSIONS=1024                        # default
```

**Ollama (local, no API key needed)**:
```bash
MDDB_EMBEDDING_PROVIDER=ollama
MDDB_EMBEDDING_API_URL=http://localhost:11434          # default
MDDB_EMBEDDING_MODEL=nomic-embed-text                  # default
MDDB_EMBEDDING_DIMENSIONS=768                          # default
```

#### Performance Benchmarks (Apple M2)

| Documents | Dimensions | Search Latency | Throughput |
|-----------|-----------|---------------|------------|
| 1,000 | 768 | ~0.9 ms | ~1,064 qps |
| 1,000 | 1,536 | ~1.8 ms | ~544 qps |
| 5,000 | 768 | ~4.8 ms | ~210 qps |
| 10,000 | 768 | ~9.7 ms | ~104 qps |
| 10,000 | 1,536 | ~19 ms | ~52 qps |
| 50,000 | 768 | ~50 ms | ~20 qps |
| 50,000 | 1,536 | ~96 ms | ~10 qps |

Metadata pre-filtering significantly reduces search time (e.g., filtering to 10% of 10K docs: ~1.1 ms vs ~9.7 ms).

---

### POST /v1/fts

Perform full-text search across document content. Supports multiple search modes: simple, boolean, phrase, wildcard, proximity, and range filtering. Uses TF-IDF, BM25, BM25F, or PMISparse scoring with optional stemming, synonyms, and typo tolerance.

**Request Body**:
```json
{
  "collection": "blog",
  "query": "markdown database tutorial",
  "limit": 10,
  "algorithm": "bm25f",
  "fuzzy": 1,
  "mode": "auto",
  "disableStem": false,
  "disableSynonyms": false,
  "fieldWeights": {
    "content": 1.0,
    "meta.title": 3.0,
    "meta.tags": 2.0
  },
  "rangeMeta": [
    {"field": "addedAt", "gte": "2024-01-01", "lte": "2024-12-31"}
  ]
}
```

**Parameters**:
- `collection` (required): Collection name
- `query` (required): Search query text
- `limit` (optional): Maximum results (default: 50)
- `algorithm` (optional): `"tfidf"` (default), `"bm25"`, `"bm25f"`, or `"pmisparse"` — used for simple mode
- `mode` (optional): Search mode — `"auto"` (default), `"simple"`, `"boolean"`, `"phrase"`, `"wildcard"`, `"proximity"`, `"expression"` (v2.9.13+ — full query DSL with nested parens and precedence)
- `distance` (optional): Proximity distance in words (default: 5) — only used with mode=proximity
- `fuzzy` (optional): Typo tolerance — `0` (off, default), `1` (1 edit), `2` (2 edits) — used for simple mode
- `lang` (optional): Language code for query tokenization (e.g., `"pl"`, `"de"`, `"fr"`). Uses language-specific stemmer and stop words. Falls back to server default if omitted (default: `"en"`, configurable via `MDDB_FTS_DEFAULT_LANG`)
- `disableStem` (optional): Disable stemming for this query (default: false)
- `disableSynonyms` (optional): Disable synonym expansion for this query (default: false)
- `fieldWeights` (optional, BM25F only): Map of field name to weight. Defaults: content=1.0, meta.title=3.0, meta.tags=2.0, meta.category=2.0, meta.description=1.5
- `filterMeta` (optional): Metadata pre-filter — `{"key": ["value1", "value2"]}`
- `rangeMeta` (optional): Array of range filters on metadata or timestamps
- `boost` (optional): Per-query score multipliers keyed by `"metaKey:metaValue"`. Positive values boost (`5.0` → 5×), negative values demote (`-2.0` → ½×). Multiple matching entries combine multiplicatively; floor is `0.001`. No reindex required.
- `highlight` (optional, v2.9.13+): When `true`, each result gains a `highlights[]` array with snippet fragments around matched terms. Works uniformly across every mode.
- `highlightTag` (optional, v2.9.13+): Wrap tag for matched terms — defaults to `"<mark>"` which produces `<mark>term</mark>`. Common alternatives: `"<strong>"`, `"**"` (for markdown), `"[h]"`. The close tag is derived automatically from the open tag.
- `maxHighlights` (optional, v2.9.13+): Max fragments returned per result (default `3`).
- `fragmentSize` (optional, v2.9.13+): Approximate chars per fragment (default `150`). Fragments snap to word boundaries so the value is a target, not a hard cap.
- `facetBy` (optional, v2.9.14+): Array of metadata keys. When non-empty the response gains a `facets` map keyed by the same names with per-value counts (ordered by count desc, value asc) aggregated over the matched documents. Counts reflect the post-filter, post-boost, post-curation result set.
- `facetMaxValues` (optional, v2.9.14+): Cap per-key bucket count. `0` / omitted = unlimited.

**Search Modes**:
- **simple**: Standard full-text search with TF-IDF/BM25/BM25F/PMISparse scoring
- **boolean**: Boolean operators — `rust AND performance`, `rust OR golang`, `NOT java`, `+required -excluded`
- **phrase**: Exact phrase matching — `"machine learning algorithms"` (consecutive terms)
- **wildcard**: Pattern matching — `prog*` (any suffix), `te?t` (single char)
- **proximity**: Terms within N words — `"rust systems"` with `distance: 5`
- **auto**: Auto-detects mode from query syntax (default)

**Range Filter Object**:
- `field` (required): Metadata key name, or `"addedAt"` / `"updatedAt"` for timestamps
- `gte` (optional): Greater than or equal (supports unix timestamps, ISO dates, numeric strings)
- `lte` (optional): Less than or equal
- `gt` (optional): Greater than (strict)
- `lt` (optional): Less than (strict)

**Response**:
```json
{
  "results": [
    {
      "document": {
        "id": "blog|post1|en_gb",
        "key": "post1",
        "lang": "en_GB",
        "meta": {"category": ["tutorial"]},
        "contentMd": "# Markdown Database Tutorial..."
      },
      "score": 2.3456,
      "matchedTerms": ["markdown", "databas", "tutori"]
    }
  ],
  "total": 1,
  "algorithm": "bm25",
  "mode": "simple",
  "lang": "en",
  "stemmingActive": true,
  "synonymsActive": true
}
```

**cURL Examples**:
```bash
# Simple search
curl -X POST http://localhost:11023/v1/fts \
  -H 'Content-Type: application/json' \
  -d '{"collection":"blog","query":"markdown database","algorithm":"bm25","limit":10}'

# Boolean search
curl -X POST http://localhost:11023/v1/fts \
  -H 'Content-Type: application/json' \
  -d '{"collection":"blog","query":"rust AND performance NOT java","mode":"boolean"}'

# Phrase search
curl -X POST http://localhost:11023/v1/fts \
  -H 'Content-Type: application/json' \
  -d '{"collection":"blog","query":"\"machine learning\"","mode":"phrase"}'

# Wildcard search
curl -X POST http://localhost:11023/v1/fts \
  -H 'Content-Type: application/json' \
  -d '{"collection":"blog","query":"prog*","mode":"wildcard"}'

# Proximity search (terms within 5 words)
curl -X POST http://localhost:11023/v1/fts \
  -H 'Content-Type: application/json' \
  -d '{"collection":"blog","query":"rust systems","mode":"proximity","distance":5}'

# Range filter (price between 10 and 100)
curl -X POST http://localhost:11023/v1/fts \
  -H 'Content-Type: application/json' \
  -d '{"collection":"shop","query":"widget","rangeMeta":[{"field":"price","gte":"10","lte":"100"}]}'

# Multi-language search (Polish)
curl -X POST http://localhost:11023/v1/fts \
  -H 'Content-Type: application/json' \
  -d '{"collection":"articles","query":"programowanie wydajne","lang":"pl","algorithm":"bm25"}'

# Per-query boost: 5× featured, ½× archived
curl -X POST http://localhost:11023/v1/fts \
  -H 'Content-Type: application/json' \
  -d '{"collection":"blog","query":"tutorial","boost":{"tag:featured":5.0,"status:archived":-2.0}}'

# Highlighting with fragments
curl -X POST http://localhost:11023/v1/fts \
  -H 'Content-Type: application/json' \
  -d '{"collection":"blog","query":"markdown database","highlight":true,"maxHighlights":2}'
```

When `highlight: true`, each result gains a `highlights` array:

```json
{
  "results": [
    {
      "document": { "id": "blog|post1|en", "key": "post1", ... },
      "score": 2.34,
      "matchedTerms": ["markdown", "database"],
      "highlights": [
        {
          "fragment": "…using <mark>Markdown</mark> as a lightweight <mark>database</mark> format…",
          "matchedTerms": ["markdown", "database"],
          "startOffset": 312,
          "endOffset": 461
        }
      ]
    }
  ]
}
```

---

### POST /v1/fts-reindex

Reindex all documents in a collection using their stored `lang` field for language-aware FTS processing.

**Query Parameters**:
- `collection` (required): Collection name to reindex

**cURL Example**:
```bash
curl -X POST "http://localhost:11023/v1/fts-reindex?collection=articles"
```

**Response**:
```json
{
  "reindexed": 150,
  "collection": "articles"
}
```

---

### GET /v1/fts-languages

Returns all supported languages for multi-language FTS.

**cURL Example**:
```bash
curl http://localhost:11023/v1/fts-languages
```

**Response**:
```json
{
  "languages": [
    {"code": "ar", "name": "Arabic"},
    {"code": "da", "name": "Danish"},
    {"code": "de", "name": "German"},
    {"code": "en", "name": "English"}
  ],
  "defaultLang": "en"
}
```

---

### GET /v1/autocomplete

Return up to `topN` terms starting with the given prefix, ranked by document frequency. Scans the existing FTS inverted index — no additional indexing is required.

**Query Parameters**:
- `collection` (required): Collection name
- `q` (required): Prefix query — lowercased and stripped of non-alphanumerics
- `field` (optional): Limit to one indexed field (e.g. `title`, `content`, `meta.tags`); empty means global
- `topN` (optional, default 10): Maximum suggestions

**cURL Examples**:
```bash
# Global autocomplete across all indexed content
curl "http://localhost:11023/v1/autocomplete?collection=blog&q=mar&topN=5"

# Title-only autocomplete for type-ahead UI
curl "http://localhost:11023/v1/autocomplete?collection=blog&q=mar&field=meta.title"
```

**Response**:
```json
{
  "items": [
    {"term": "market", "field": "", "docCount": 42},
    {"term": "marker", "field": "", "docCount": 17},
    {"term": "marathon", "field": "", "docCount": 3}
  ],
  "total": 3,
  "query": "mar",
  "field": ""
}
```

**Notes**:
- A single call scans at most 10000 index entries so pathological prefixes like `a` stay fast.
- Prefix is capped at 32 characters; longer prefixes are truncated silently.
- Empty `q` returns an empty result list rather than an error — safer for client-side type-ahead handlers.

---

### POST /v1/synonyms

Add or update synonyms for a term in a collection.

**Request Body**:
```json
{
  "collection": "docs",
  "term": "big",
  "synonyms": ["large", "huge", "enormous"]
}
```

**Response**:
```json
{
  "status": "ok"
}
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/synonyms \
  -H 'Content-Type: application/json' \
  -d '{"collection":"docs","term":"big","synonyms":["large","huge","enormous"]}'
```

---

### GET /v1/synonyms

List all synonyms for a collection.

**Query Parameters**:
- `collection` (required): Collection name

**Response**:
```json
{
  "collection": "docs",
  "synonyms": {
    "big": ["large", "huge", "enormous"],
    "fast": ["quick", "rapid", "swift"]
  }
}
```

**cURL Example**:
```bash
curl "http://localhost:11023/v1/synonyms?collection=docs"
```

---

### DELETE /v1/synonyms

Delete all synonyms for a term in a collection.

**Request Body**:
```json
{
  "collection": "docs",
  "term": "big"
}
```

**Response**:
```json
{
  "status": "ok"
}
```

**cURL Example**:
```bash
curl -X DELETE http://localhost:11023/v1/synonyms \
  -H 'Content-Type: application/json' \
  -d '{"collection":"docs","term":"big"}'
```

---

### POST /v1/export

Export documents from a collection in NDJSON or ZIP format.

**Request Body**:
```json
{
  "collection": "blog",
  "filterMeta": {
    "category": ["blog"]
  },
  "format": "ndjson"
}
```

**Parameters**:
- `collection` (required): Collection name
- `filterMeta` (optional): Metadata filters (same as search)
- `format` (required): Export format - `ndjson` or `zip`

**Response (NDJSON)**:
```
{"id":"blog|post1|en_gb","key":"post1","lang":"en_GB","meta":{"category":["blog"]},"contentMd":"# Post 1","addedAt":1699296000,"updatedAt":1699296100}
{"id":"blog|post2|en_gb","key":"post2","lang":"en_GB","meta":{"category":["blog"]},"contentMd":"# Post 2","addedAt":1699295000,"updatedAt":1699296200}
```

**Response (ZIP)**:
Binary ZIP file containing markdown files named as `{key}.{lang}.md`

**cURL Examples**:

NDJSON export:
```bash
curl -X POST http://localhost:11023/v1/export \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "filterMeta": {"category": ["blog"]},
    "format": "ndjson"
  }' > export.ndjson
```

ZIP export:
```bash
curl -X POST http://localhost:11023/v1/export \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "format": "zip"
  }' > export.zip
```

---

### GET /v1/backup

Create a backup of the database file.

**Query Parameters**:
- `to` (optional): Backup file name (default: `backup-{timestamp}.db`)

**Response**:
```json
{
  "backup": "backup-1699296000.db"
}
```

**cURL Example**:
```bash
curl "http://localhost:11023/v1/backup?to=backup-$(date +%s).db"
```

**Notes**:
- Creates a copy of the entire BoltDB database file
- Backup is created in the same directory as the database
- Does not interrupt server operations

---

### POST /v1/restore

Restore the database from a backup file.

**Request Body**:
```json
{
  "from": "backup-1699296000.db"
}
```

**Response**:
```json
{
  "restored": "backup-1699296000.db"
}
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/restore \
  -H 'Content-Type: application/json' \
  -d '{"from": "backup-1699296000.db"}'
```

**⚠️ Warning**:
- This operation replaces the current database
- The server briefly closes and reopens the database connection
- All current data will be replaced with the backup

---

### POST /v1/truncate

Truncate revision history and optionally clear cache.

**Request Body**:
```json
{
  "collection": "blog",
  "keepRevs": 3,
  "dropCache": true
}
```

**Parameters**:
- `collection` (required): Collection name
- `keepRevs` (required): Number of recent revisions to keep per document (0 = delete all history)
- `dropCache` (optional): Whether to drop cache (placeholder for future use)

**Response**:
```json
{
  "status": "truncated"
}
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/truncate \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "keepRevs": 3,
    "dropCache": true
  }'
```

**Use Cases**:
- Reduce database size by removing old revisions
- Keep only recent history for auditing
- Clean up after bulk imports

---

### GET /v1/stats

Get server and database statistics.

**Request**: No body required (GET request)

**Response**:
```json
{
  "databasePath": "mddb.db",
  "databaseSize": 16384,
  "mode": "wr",
  "collections": [
    {
      "name": "blog",
      "documentCount": 42,
      "revisionCount": 156,
      "metaIndexCount": 84
    }
  ],
  "totalDocuments": 42,
  "totalRevisions": 156,
  "totalMetaIndices": 84,
  "uptime": ""
}
```

**Response Fields**:
- `databasePath`: Path to the database file
- `databaseSize`: Database file size in bytes
- `mode`: Access mode (read, write, wr)
- `collections`: Array of collection statistics
  - `name`: Collection name
  - `documentCount`: Number of documents in collection
  - `revisionCount`: Number of revisions in collection
  - `metaIndexCount`: Number of metadata indices in collection
- `totalDocuments`: Total documents across all collections
- `totalRevisions`: Total revisions across all collections
- `totalMetaIndices`: Total metadata indices across all collections

**cURL Example**:
```bash
curl http://localhost:11023/v1/stats
```

**CLI Example**:
```bash
mddb-cli stats
```

**Use Cases**:
- Monitor database growth
- Check collection sizes before operations
- Verify indexing status
- Performance monitoring and capacity planning

---

### POST /v1/schema/set

Set or update the validation schema for a collection. Schema validation is opt-in per collection. See the [Schema Validation Guide](SCHEMA-VALIDATION.md) for full details on supported rules.

**Request Body**:
```json
{
  "collection": "blog",
  "schema": {
    "required": ["category", "author"],
    "properties": {
      "category": { "type": "string", "enum": ["blog", "tutorial", "news"] },
      "author":   { "type": "string" },
      "tags":     { "type": "string", "minItems": 1, "maxItems": 5 }
    }
  }
}
```

**Response**:
```json
{
  "status": "ok"
}
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/schema/set \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "schema": {
      "required": ["category"],
      "properties": {
        "category": { "type": "string", "enum": ["blog", "tutorial"] }
      }
    }
  }'
```

---

### POST /v1/schema/get

Retrieve the current validation schema for a collection.

**Request Body**:
```json
{
  "collection": "blog"
}
```

**Response** (schema exists):
```json
{
  "collection": "blog",
  "schema": {
    "required": ["category", "author"],
    "properties": {
      "category": { "type": "string", "enum": ["blog", "tutorial", "news"] },
      "author":   { "type": "string" },
      "tags":     { "type": "string", "minItems": 1, "maxItems": 5 }
    }
  }
}
```

**Response** (no schema):
```json
{
  "collection": "blog",
  "schema": null
}
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/schema/get \
  -H 'Content-Type: application/json' \
  -d '{"collection": "blog"}'
```

---

### POST /v1/schema/delete

Delete the validation schema for a collection, disabling validation. Existing documents are not affected.

**Request Body**:
```json
{
  "collection": "blog"
}
```

**Response**:
```json
{
  "status": "ok"
}
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/schema/delete \
  -H 'Content-Type: application/json' \
  -d '{"collection": "blog"}'
```

---

### POST /v1/schema/list

List all collections that have a validation schema defined.

**Request Body**: Empty or `{}`.

**Response**:
```json
{
  "schemas": [
    {
      "collection": "blog",
      "schema": {
        "required": ["category", "author"],
        "properties": {
          "category": { "type": "string", "enum": ["blog", "tutorial", "news"] },
          "author":   { "type": "string" }
        }
      }
    },
    {
      "collection": "products",
      "schema": {
        "required": ["price", "sku"],
        "properties": {
          "price": { "type": "number" },
          "sku":   { "type": "string", "pattern": "^SKU-[0-9]+$" }
        }
      }
    }
  ]
}
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/schema/list \
  -H 'Content-Type: application/json' \
  -d '{}'
```

---

### POST /v1/validate

Validate a document's metadata against the collection schema without persisting anything. Useful for dry-run checks.

**Request Body**:
```json
{
  "collection": "blog",
  "meta": {
    "category": ["blog"],
    "author": ["Jane Doe"],
    "tags": ["golang", "tutorial"]
  }
}
```

**Response** (valid):
```json
{
  "valid": true,
  "errors": []
}
```

**Response** (invalid):
```json
{
  "valid": false,
  "errors": [
    "value \"pending\" for key \"status\" is not in allowed enum values [draft, published, archived]",
    "key \"tags\" has 6 values, exceeds maxItems 5"
  ]
}
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/validate \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "meta": {
      "category": ["blog"],
      "author": ["Jane Doe"]
    }
  }'
```

---

### POST /v1/auth/login

Authenticate with username and password to receive a JWT token. The token must be included in the `Authorization` header for subsequent authenticated requests.

**Request Body**:
```json
{
  "username": "admin",
  "password": "secret"
}
```

**Response**:
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expiresAt": 1709481200
}
```

**cURL Example**:
```bash
curl -X POST http://localhost:11023/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"secret"}'
```

**Error Responses**:
- `401 Unauthorized` - Invalid credentials
- `400 Bad Request` - Invalid request format

---

### POST /v1/auth/api-key

Create a new API key for programmatic access. Requires JWT authentication via `Authorization` header.

**Authentication**: JWT token required

**Request Body**:
```json
{
  "description": "CI/CD pipeline",
  "expiresAt": 0
}
```

**Parameters**:
- `description` (string, optional): Human-readable label for the API key
- `expiresAt` (int64, optional): Unix timestamp when key expires (0 = never expires)

**Response**:
```json
{
  "key": "mddb_live_abc123def456...",
  "description": "CI/CD pipeline",
  "createdAt": 1709394600,
  "expiresAt": 0
}
```

**cURL Example**:
```bash
# First, login to get JWT token
TOKEN=$(curl -s -X POST http://localhost:11023/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"secret"}' | jq -r .token)

# Create API key
curl -X POST http://localhost:11023/v1/auth/api-key \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"description":"Production deployment","expiresAt":0}'
```

**Important Notes**:
- The full API key is **only shown once** in the response
- Save the key securely - it cannot be retrieved again
- API keys are hashed with SHA256 before storage
- Use the key in subsequent requests via the `X-API-Key` header

**Error Responses**:
- `401 Unauthorized` - Missing or invalid JWT token
- `400 Bad Request` - Invalid request format
- `500 Internal Server Error` - Failed to create API key

---

### GET /v1/auth/api-keys

List all API keys for the authenticated user. Returns metadata about each key (not the actual key values).

**Authentication**: JWT token required

**Response**:
```json
{
  "keys": [
    {
      "keyHash": "abc123def456...",
      "description": "Production deployment",
      "createdAt": 1709394600,
      "expiresAt": 0
    },
    {
      "keyHash": "xyz789ghi012...",
      "description": "Development testing",
      "createdAt": 1709395200,
      "expiresAt": 1740931200
    }
  ]
}
```

**Response Fields**:
- `keyHash` (string): SHA256 hash of the API key (use this to delete the key)
- `description` (string): Key description
- `createdAt` (int64): Unix timestamp of creation
- `expiresAt` (int64): Unix timestamp of expiry (0 = never expires)

**cURL Example**:
```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:11023/v1/auth/api-keys
```

**Error Responses**:
- `401 Unauthorized` - Missing or invalid JWT token
- `500 Internal Server Error` - Failed to retrieve API keys

---

### DELETE /v1/auth/api-keys/:keyHash

Delete an API key by its hash. Users can only delete their own API keys.

**Authentication**: JWT token required

**URL Parameters**:
- `keyHash` (string, required): The SHA256 hash of the API key (from GET /v1/auth/api-keys)

**Response**:
```json
{
  "status": "deleted"
}
```

**cURL Example**:
```bash
# Get list of keys to find the keyHash
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:11023/v1/auth/api-keys

# Delete specific key
curl -X DELETE -H "Authorization: Bearer $TOKEN" \
  http://localhost:11023/v1/auth/api-keys/abc123def456...
```

**Error Responses**:
- `401 Unauthorized` - Missing or invalid JWT token
- `403 Forbidden` - Attempting to delete another user's API key
- `404 Not Found` - API key not found
- `400 Bad Request` - Missing keyHash parameter

---

### Using API Keys

Once you have an API key, use it to authenticate requests instead of JWT tokens:

**With HTTP Header**:
```bash
curl -H "X-API-Key: mddb_live_abc123def456..." \
  http://localhost:11023/v1/search \
  -H 'Content-Type: application/json' \
  -d '{"collection":"blog","filterMeta":{"status":["published"]}}'
```

**With CLI**:
```bash
mddb-cli --api-key mddb_live_abc123def456... search blog -f "status=published"
```

**API Key vs JWT Token**:
- **JWT Tokens**: Short-lived (default 24h), obtained via login, ideal for interactive sessions
- **API Keys**: Long-lived or permanent, ideal for automation, CI/CD, and third-party integrations

---

### POST /v1/classify

Zero-shot document classification using embedding similarity. Ranks candidate labels by their semantic similarity to a document or text.

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `collection` | string | No* | Collection name (for doc reference) |
| `key` | string | No* | Document key (for doc reference) |
| `lang` | string | No | Language code (default: "en") |
| `text` | string | No* | Raw text to classify |
| `labels` | string[] | Yes | Candidate labels (max 100) |
| `topK` | int | No | Return top K labels (0 = all) |
| `multi` | bool | No | Return all labels above threshold |
| `threshold` | float | No | Minimum similarity score (default: 0.0) |

*Provide either `text` OR `collection`+`key` (with optional `lang`).

**Example Request:**
```bash
curl -X POST http://localhost:11023/v1/classify \
  -d '{
    "text": "Go is a statically typed, compiled language designed at Google",
    "labels": ["programming", "cooking", "sports", "music"]
  }'
```

**Example Response:**
```json
{
  "results": [
    {"label": "programming", "score": 0.87},
    {"label": "music", "score": 0.21},
    {"label": "sports", "score": 0.18},
    {"label": "cooking", "score": 0.12}
  ],
  "model": "text-embedding-3-small",
  "dimensions": 1536
}
```

**Notes:**
- Requires an embedding provider to be configured
- For document references, reuses existing embedding from vector store if available
- Labels are embedded in a single batch API call for efficiency

---

### PATCH /v1/update

Partially update a document's metadata and/or content independently without re-sending the entire document.

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `collection` | string | Yes | Collection name |
| `key` | string | Yes | Document key |
| `lang` | string | Yes | Language code |
| `meta` | object | No | New metadata (replaces all). Use `{}` to clear |
| `contentMd` | string | No | New content (replaces existing) |
| `ttl` | int | No | New TTL in seconds (0 = remove) |

**Example:**
```bash
# Update only metadata
curl -X PATCH http://localhost:11023/v1/update \
  -d '{"collection":"blog","key":"p1","lang":"en","meta":{"tag":["go","updated"]}}'

# Update only content
curl -X PATCH http://localhost:11023/v1/update \
  -d '{"collection":"blog","key":"p1","lang":"en","contentMd":"# Updated content"}'
```

---

### GET /v1/doc-meta

Get document metadata without content. Lightweight read.

**Query Parameters:**
| Parameter | Required | Description |
|-----------|----------|-------------|
| `collection` | Yes | Collection name |
| `key` | Yes | Document key |
| `lang` | No | Language code (default: "en") |

**Example:**
```bash
curl "http://localhost:11023/v1/doc-meta?collection=blog&key=p1&lang=en"
```

---

### POST /v1/delete

Delete a document from a collection.

**Request Body**:
```json
{
  "collection": "blog",
  "key": "homepage",
  "lang": "en"
}
```

**Response**:
```json
{
  "status": "deleted",
  "collection": "blog",
  "key": "homepage",
  "lang": "en"
}
```

---

### POST /v1/delete-batch

Delete multiple documents in a single request.

**Request Body**:
```json
{
  "collection": "blog",
  "documents": [
    { "key": "post-1", "lang": "en" },
    { "key": "post-2", "lang": "en" }
  ]
}
```

**Response**:
```json
{
  "deleted": 2,
  "not_found": 0,
  "failed": 0,
  "errors": null
}
```

---

### POST /v1/delete-collection

Delete all documents in a collection.

**Request Body**:
```json
{
  "collection": "blog"
}
```

**Response**:
```json
{
  "status": "ok",
  "collection": "blog"
}
```

---

### POST /v1/hybrid-search

Hybrid search combining full-text (sparse) and vector (dense) results using alpha blending or reciprocal rank fusion (RRF).

**Request Body**:
```json
{
  "collection": "blog",
  "query": "how to deploy",
  "topK": 10,
  "algorithm": "bm25",
  "vectorAlgorithm": "flat",
  "alpha": 0.5,
  "strategy": "alpha",
  "rrfK": 60,
  "fuzzy": 0,
  "threshold": 0.0,
  "distanceMetric": "cosine",
  "filterMeta": { "category": ["tutorial"] },
  "includeContent": false,
  "disableStem": false,
  "disableSynonyms": false
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `collection` | string | | **Required.** Collection name |
| `query` | string | | **Required.** Search query |
| `topK` | integer | `10` | Max results |
| `algorithm` | string | `"bm25"` | FTS algorithm: `bm25`, `bm25f` |
| `vectorAlgorithm` | string | `"flat"` | Vector algorithm: `flat`, `hnsw`, `ivf`, `pq`, `sq` |
| `alpha` | number | `0.5` | Weight blending (0=FTS only, 1=vector only) |
| `strategy` | string | `"alpha"` | Fusion strategy: `alpha` or `rrf` |
| `rrfK` | integer | `60` | RRF parameter k |
| `fuzzy` | integer | `0` | Typo tolerance: 0, 1, or 2 |
| `threshold` | number | `0.0` | Min vector similarity 0–1 |
| `distanceMetric` | string | `"cosine"` | `cosine`, `dot_product`, `euclidean` |
| `filterMeta` | object | | Metadata key-value filter |
| `includeContent` | boolean | `false` | Include full content |
| `disableStem` | boolean | `false` | Disable stemming |
| `disableSynonyms` | boolean | `false` | Disable synonym expansion |
| `boost` | object | | Per-query score multiplier keyed by `"metaKey:metaValue"` |
| `geo` | object | | Spatial pre-filter: `{lat, lng, radiusMeters}` |
| `sort` | string | `"combined"` | Result ordering: `combined` (default, by fused score) or `distance` (by `distanceMeters` ascending — requires `geo`) |
| `facetBy` | array | | (v2.9.14+) Metadata keys to aggregate into `facets` map on the response |
| `facetMaxValues` | integer | `0` | (v2.9.14+) Cap per-key bucket count; `0` = unlimited |

**Response**:
```json
{
  "results": [
    {
      "document": { "id": "...", "key": "...", "lang": "...", "meta": {} },
      "combinedScore": 0.85,
      "ftsScore": 0.7,
      "vectorScore": 0.95,
      "matchedTerms": ["deploy"],
      "rank": 1
    }
  ],
  "total": 1,
  "strategy": "alpha",
  "alpha": 0.5,
  "ftsAlgorithm": "bm25",
  "vectorAlgorithm": "flat",
  "distanceMetric": "cosine",
  "searchStats": { "durationMs": 12 }
}
```

---

### POST /v1/cross-search

Vector search across multiple collections using a text query, pre-computed vector, or another document's embedding.

**Request Body**:
```json
{
  "query": "machine learning basics",
  "targetCollections": ["articles", "tutorials"],
  "topK": 10,
  "threshold": 0.5,
  "algorithm": "flat",
  "distanceMetric": "cosine",
  "includeContent": false
}
```

Alternative source modes (use one):
- `query` (string) — text to embed
- `sourceCollection` + `sourceDocID` — use an existing document's embedding
- `queryVector` (array of numbers) — pre-computed vector

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `targetCollections` | string[] | all | Collections to search |
| `topK` | integer | `10` | Max results |
| `threshold` | number | `0.0` | Min similarity |
| `algorithm` | string | `"flat"` | Vector algorithm |
| `distanceMetric` | string | `"cosine"` | Distance metric |
| `filterMeta` | object | | Metadata filter |
| `includeContent` | boolean | `false` | Include content |

**Response**:
```json
{
  "results": [
    {
      "collection": "tutorials",
      "document": { "key": "ml-intro", "lang": "en", "meta": {} },
      "score": 0.92,
      "rank": 1
    }
  ],
  "total": 1,
  "targetCollections": ["articles", "tutorials"],
  "algorithm": "flat",
  "distanceMetric": "cosine",
  "searchStats": { "durationMs": 8, "collectionsSearched": 2 }
}
```

---

### POST /v1/find-duplicates

Detect exact and similar documents in a collection using content hashing and vector embeddings.

**Request Body**:
```json
{
  "collection": "blog",
  "mode": "both",
  "threshold": 0.9,
  "maxDocs": 5000,
  "distanceMetric": "cosine",
  "includeContent": false
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `collection` | string | | **Required.** Collection name |
| `mode` | string | `"both"` | `exact`, `similar`, or `both` |
| `threshold` | number | `0.9` | Similarity threshold 0–1 |
| `maxDocs` | integer | `5000` | Max documents to process |
| `distanceMetric` | string | `"cosine"` | Distance metric |
| `includeContent` | boolean | `false` | Include document content |

**Response**:
```json
{
  "collection": "blog",
  "mode": "both",
  "threshold": 0.9,
  "distanceMetric": "cosine",
  "totalDocuments": 150,
  "totalEmbedded": 148,
  "exactGroups": [
    {
      "groupId": 1,
      "type": "exact",
      "documents": [
        { "docId": "blog|p1|en", "key": "p1", "contentHash": "abc123" },
        { "docId": "blog|p2|en", "key": "p2", "contentHash": "abc123" }
      ]
    }
  ],
  "similarGroups": [],
  "exactDuplicates": 2,
  "similarPairs": 0
}
```

---

### POST /v1/aggregate

Compute metadata facets and date histograms for a collection. Supports optional metadata pre-filtering.

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `collection` | string | Yes | Collection name |
| `filterMeta` | object | No | Metadata pre-filter (same as `/v1/search`) |
| `facets` | array | No | Facet aggregation requests |
| `facets[].field` | string | Yes | Metadata key to aggregate (e.g. `"category"`) |
| `facets[].orderBy` | string | No | `"count"` (default, descending) or `"value"` (alphabetical) |
| `histograms` | array | No | Date histogram requests |
| `histograms[].field` | string | Yes | `"addedAt"` or `"updatedAt"` |
| `histograms[].interval` | string | No | `"day"`, `"week"`, `"month"` (default), `"year"` |
| `maxFacetSize` | int | No | Max values per facet (default: 50) |

**Example:**
```bash
curl -X POST http://localhost:11023/v1/aggregate \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "facets": [
      {"field": "category"},
      {"field": "author", "orderBy": "value"}
    ],
    "histograms": [
      {"field": "addedAt", "interval": "month"}
    ]
  }'
```

**Example with metadata pre-filter:**
```bash
curl -X POST http://localhost:11023/v1/aggregate \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "filterMeta": {"status": ["published"]},
    "facets": [{"field": "tags"}]
  }'
```

**Response:**
```json
{
  "collection": "blog",
  "totalDocs": 42,
  "facets": {
    "category": [
      {"value": "tutorial", "count": 15},
      {"value": "news", "count": 12},
      {"value": "release", "count": 8}
    ],
    "author": [
      {"value": "Alice", "count": 20},
      {"value": "Bob", "count": 22}
    ]
  },
  "histograms": {
    "addedAt": [
      {"key": "2026-01", "from": 1767225600, "to": 1769904000, "count": 10},
      {"key": "2026-02", "from": 1769904000, "to": 1772323200, "count": 18},
      {"key": "2026-03", "from": 1772323200, "to": 1775001600, "count": 14}
    ]
  },
  "durationMs": 3
}
```

---

### GET /v1/collection-config

Get configuration for a specific collection.

**Query Parameters:**
| Parameter | Required | Description |
|-----------|----------|-------------|
| `collection` | Yes | Collection name |

**Example:**
```bash
curl "http://localhost:11023/v1/collection-config?collection=blog"
```

**Response**:
```json
{
  "collection": "blog",
  "config": {
    "type": "default",
    "description": "Blog posts",
    "icon": "",
    "color": "",
    "customMeta": {}
  },
  "configured": true
}
```

Also supports **PUT** to set config and **DELETE** to remove config for a collection.

#### PUT /v1/collection-config

Set or update collection configuration including storage backend.

**Request Body:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `collection` | string | Yes | Collection name |
| `type` | string | No | Collection type (`default`, `website`, `images`, `audio`, `documents`) |
| `description` | string | No | Collection description |
| `icon` | string | No | Emoji icon |
| `color` | string | No | Hex color code |
| `customMeta` | object | No | Custom key-value metadata |
| `storageBackend` | string | No | Storage backend: `boltdb` (default), `memory`, `s3` |
| `storageConfig` | object | No | Backend-specific settings (required for `s3`) |
| `maxRevisions` | integer | No | (v2.9.14+) Revision retention cap. `0` (default) = unlimited; `N > 0` keeps only the newest N revisions per document. Older entries are trimmed inside the same transaction as every write. |

**storageConfig fields (for S3):**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `endpoint` | string | Yes | S3 endpoint (e.g. `s3.amazonaws.com`, `minio:9000`) |
| `bucket` | string | Yes | S3 bucket name |
| `region` | string | No | AWS region (e.g. `us-east-1`) |
| `accessKey` | string | No | Access key |
| `secretKey` | string | No | Secret key |
| `prefix` | string | No | Key prefix within bucket (e.g. `mddb/`) |
| `useTLS` | bool | No | Use HTTPS (default: false) |

**Example — In-Memory backend:**
```bash
curl -X PUT http://localhost:11023/v1/collection-config \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "scratch",
    "type": "default",
    "storageBackend": "memory"
  }'
```

**Example — S3 backend:**
```bash
curl -X PUT http://localhost:11023/v1/collection-config \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "archive",
    "type": "documents",
    "storageBackend": "s3",
    "storageConfig": {
      "endpoint": "s3.amazonaws.com",
      "bucket": "my-mddb-archive",
      "region": "us-east-1",
      "accessKey": "AKIAIOSFODNN7EXAMPLE",
      "secretKey": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
      "prefix": "mddb/",
      "useTLS": true
    }
  }'
```

**Response:**
```json
{
  "status": "ok",
  "collection": "archive"
}
```

> **Note:** The `memory` backend is ephemeral — all data is lost on server restart. The `s3` backend requires `endpoint` and `bucket` in `storageConfig`. The default `boltdb` backend uses the embedded database.

---

### GET /v1/collection-configs

List all collection configurations.

**Example:**
```bash
curl http://localhost:11023/v1/collection-configs
```

**Response**:
```json
{
  "configs": [
    {
      "collection": "blog",
      "config": { "type": "default", "description": "Blog posts" }
    }
  ],
  "total": 1
}
```

---

### GET /v1/embedding-configs

List all configured embedding models.

**Example:**
```bash
curl http://localhost:11023/v1/embedding-configs
```

**Response**:
```json
{
  "configs": [
    {
      "id": "cfg_abc123",
      "name": "OpenAI Ada",
      "provider": "openai",
      "model": "text-embedding-3-small",
      "dimensions": 1536,
      "apiKey": "sk-...",
      "apiUrl": "",
      "isDefault": true,
      "createdAt": 1709100000
    }
  ]
}
```

Also supports **POST** to create a new embedding config.

---

### GET/PUT/DELETE /v1/embedding-configs/:id

Manage a specific embedding configuration.

- **GET** returns the config object
- **PUT** updates the config (fields: `name`, `provider`, `model`, `dimensions`, `apiKey`, `apiUrl`, `isDefault`)
- **DELETE** removes the config (returns 204 No Content)

**Example:**
```bash
curl http://localhost:11023/v1/embedding-configs/cfg_abc123
```

---

### POST /v1/embedding-configs/set-default

Set a specific embedding configuration as default.

**Request Body**:
```json
{
  "id": "cfg_abc123"
}
```

**Response**:
```json
{
  "message": "default config updated"
}
```

---

### GET/POST/DELETE /v1/stopwords

Manage FTS stop words for a collection.

**GET** — List stop words:
```bash
curl "http://localhost:11023/v1/stopwords?collection=blog"
```

**Response**:
```json
{
  "collection": "blog",
  "entries": [
    { "word": "the", "isDefault": true },
    { "word": "myword", "isDefault": false }
  ],
  "total": 2,
  "defaults": 1,
  "custom": 1
}
```

**POST** — Add stop words:
```json
{
  "collection": "blog",
  "words": ["myword", "another"]
}
```

**DELETE** — Remove stop words:
```json
{
  "collection": "blog",
  "words": ["myword"]
}
```

---

### GET/POST /v1/webhooks

List or register webhooks.

**GET** — List all webhooks:
```bash
curl http://localhost:11023/v1/webhooks
```

**POST** — Register a webhook:
```json
{
  "url": "https://example.com/hook",
  "events": ["doc.added", "doc.updated", "doc.deleted"],
  "collection": "blog"
}
```

**Response** (webhook object):
```json
{
  "id": "wh_abc123",
  "url": "https://example.com/hook",
  "events": ["doc.added", "doc.updated", "doc.deleted"],
  "collection": "blog",
  "createdAt": 1709100000
}
```

---

### GET /v1/audit

List audit-log events. **Admin-only.** Returns 404 when `MDDB_AUDIT_ENABLED` is not set.

**Query parameters**:

| Name | Type | Description |
|------|------|-------------|
| `from` | RFC3339 timestamp | Lower bound (inclusive). Converted to nanoseconds internally. |
| `to` | RFC3339 timestamp | Upper bound (inclusive). |
| `fromNanos` | int64 | Nanosecond-precision lower bound (takes precedence over `from`). |
| `toNanos` | int64 | Nanosecond-precision upper bound. |
| `actor` | string | Filter by authenticated username. |
| `action` | string | e.g. `auth.login`, `auth.jwt`, `auth.apikey`, `auth.missing`, `write./v1/add`. |
| `result` | string | `ok` or `fail`. |
| `limit` | int | Max events returned (default 100). |

**Example**:
```bash
curl -H "Authorization: Bearer $ADMIN_JWT" \
  "http://localhost:11023/v1/audit?actor=alice&result=fail&limit=50"
```

**Response**:
```json
{
  "events": [
    {
      "ts": 1740000000000000000,
      "actor": "alice",
      "action": "auth.login",
      "resource": "/v1/auth/login",
      "result": "fail",
      "ip": "203.0.113.5",
      "userAgent": "curl/8.4.0"
    }
  ],
  "count": 1,
  "dropped": 0
}
```

Events are returned newest-first. `dropped` reports the running total of events that could not fit in the in-memory ring buffer (operational-health signal — a non-zero value means the audit subsystem was under pressure).

---

### GET /v1/audit/exporters

> Added in 2.9.16.

Per-sink delivery counters for the audit log export subsystem. Returns a snapshot of every configured exporter (webhook + syslog) with delivered / failed / dropped counts and the last error.

**Authentication**: admin JWT or API key required.

**Response**:

```json
{
  "exporters": [
    {
      "name": "webhook",
      "target": "https://splunk.example/services/collector/raw",
      "queued": 1250,
      "delivered": 1248,
      "failed": 2,
      "dropped": 0,
      "lastError": "attempt 1: HTTP 503",
      "bufferSize": 1024
    },
    {
      "name": "syslog",
      "target": "tcp://logs.example:6514",
      "queued": 1250,
      "delivered": 1250,
      "failed": 0,
      "dropped": 0,
      "lastError": "",
      "bufferSize": 1024
    }
  ],
  "count": 2
}
```

Exporters are configured via `MDDB_AUDIT_EXPORT_WEBHOOK_URL`, `MDDB_AUDIT_EXPORT_SYSLOG_ADDR`, etc. — see [config.md](config.md#audit-log-export-iso-27001--soc-2).

---

### GET /v1/encryption/status

> Added in 2.9.16.

At-rest encryption posture: primary keyID, configured previous keyIDs, and per-collection counts of how each document is sealed.

**Authentication**: admin JWT or API key required.

**Response**:

```json
{
  "enabled": true,
  "primaryKeyID": 2,
  "previousKeyIDs": [1],
  "collections": [
    {
      "collection": "secrets",
      "encrypted": true,
      "total": 1500,
      "withPrimary": 800,
      "withLegacy": 700,
      "plaintext": 0,
      "unknownKey": 0
    }
  ]
}
```

`withLegacy` counts entries sealed under a previous key (V1 ciphertexts or V2 with a non-primary keyID). Run `POST /v1/encryption/rotate` to converge them on the current primary.

---

### POST /v1/encryption/rotate

> Added in 2.9.16.

Start a re-encryption job that walks every encrypted entry under non-primary keys and re-seals it with the current `MDDB_ENCRYPTION_KEY`. Plaintext entries and entries already sealed under the primary are skipped.

**Authentication**: admin JWT or API key required. Refused in read-only mode.

**Body** (optional):

```json
{ "collection": "secrets" }
```

Empty `collection` (or omit the body) scopes the job to every collection.

**Response** (job is started in the background; poll for progress):

```json
{
  "id": "rot-a1b2c3d4e5f6a7b8",
  "status": "queued",
  "primaryKeyID": 2,
  "startedAt": 1714560000000000000,
  "scanned": 0,
  "reencrypted": 0,
  "skipped": 0,
  "errors": 0
}
```

Calling rotate while a job is already running returns the running job's ID instead of queueing a second one — the operation is single-flight.

---

### GET /v1/encryption/jobs[/:id]

> Added in 2.9.16.

Without an ID — list every rotation job ever queued in this process (newest first):

```json
{ "jobs": [ { "id": "rot-...", "status": "completed", ... } ] }
```

With an ID — single job snapshot:

```json
{
  "id": "rot-a1b2c3d4e5f6a7b8",
  "status": "completed",
  "primaryKeyID": 2,
  "startedAt": 1714560000000000000,
  "finishedAt": 1714560015000000000,
  "scanned": 1500,
  "reencrypted": 700,
  "skipped": 800,
  "errors": 0
}
```

`status` is one of `queued`, `running`, `completed`, `failed`. A failed job carries `lastError` with the most recent failure message.

---

### GET /v1/compliance-status

Report the live state of the production-hardening guard. **No authentication required** — this endpoint is designed to be called by operator-facing liveness / readiness probes and by external monitoring that must detect a drifted configuration before production traffic hits a non-compliant server.

**Query parameters**: none.

**Response**:

```json
{
  "production": true,
  "compliant": true,
  "missing": [],
  "missingCount": 0
}
```

When the server was started with `MDDB_PRODUCTION=true` but a guardrail is not satisfied the server would have refused to boot, so `compliant=true` is implied. When `MDDB_PRODUCTION` is unset the server runs with the existing defaults and this endpoint reports what is and is not wired up:

```json
{
  "production": false,
  "compliant": false,
  "missing": [
    { "envVar": "MDDB_AUTH_ENABLED",     "want": "true",              "reason": "A.5.15 / CC6.1 — access control" },
    { "envVar": "MDDB_TLS_ENABLED",      "want": "true",              "reason": "A.8.24 / CC6.7 — encryption in transit" },
    { "envVar": "MDDB_CORS_ORIGINS",     "want": "explicit allowlist",   "reason": "A.8.23 / CC6.6 — web-origin segmentation" },
    { "envVar": "MDDB_AUDIT_ENABLED",    "want": "true",              "reason": "A.8.15 / CC7.2 — audit trail" },
    { "envVar": "MDDB_RATE_LIMIT_ENABLED","want": "true",             "reason": "A.5.30 / CC6.6 — resource-exhaustion protection" }
  ],
  "missingCount": 5
}
```

Wire a liveness probe to alert when `compliant=false` so configuration drift is caught immediately.

---

### POST /v1/webhooks/delete

Delete a webhook by ID.

**Request Body**:
```json
{
  "id": "wh_abc123"
}
```

**Response**:
```json
{
  "status": "deleted",
  "id": "wh_abc123"
}
```

---

### POST /v1/revisions

List document revision history.

**Request Body**:
```json
{
  "collection": "blog",
  "key": "homepage",
  "lang": "en"
}
```

**Response**:
```json
{
  "collection": "blog",
  "key": "homepage",
  "lang": "en",
  "revisions": [
    {
      "timestamp": 1709200000,
      "updatedAt": 1709200000,
      "contentMd": "# Old content",
      "meta": { "author": ["Jane"] }
    }
  ],
  "total": 1
}
```

---

### POST /v1/revisions/restore

Restore a document to a previous revision.

**Request Body**:
```json
{
  "collection": "blog",
  "key": "homepage",
  "lang": "en",
  "timestamp": 1709200000
}
```

**Response**: Restored document object.

---

### GET/POST /v1/automation

List or create automation rules (triggers, crons, webhooks).

**GET** — List rules:
```bash
curl http://localhost:11023/v1/automation
```

**Response**:
```json
{
  "rules": [
    {
      "id": "auto_abc",
      "name": "Alert on new docs",
      "type": "trigger",
      "searchType": "fts",
      "query": "urgent",
      "threshold": 0.8,
      "webhookUrl": "https://example.com/alert"
    }
  ],
  "total": 1
}
```

**POST** — Create a rule:
```json
{
  "name": "Daily report",
  "type": "cron",
  "schedule": "0 9 * * *",
  "searchType": "vector",
  "query": "status report",
  "webhookUrl": "https://example.com/report"
}
```

**Response**: Created rule object (201 Created).

---

### GET/PUT/DELETE /v1/automation/:id

Manage a specific automation rule.

- **GET** returns the rule object
- **PUT** updates the rule
- **DELETE** removes the rule

**POST /v1/automation/:id/test** — Test a trigger rule:
```json
{
  "trigger": { "id": "auto_abc", "name": "...", "searchType": "fts", "query": "urgent" },
  "matches": [...],
  "total": 3
}
```

---

### GET /v1/automation-logs

Get automation execution logs with pagination.

**Query Parameters:**
| Parameter | Required | Default | Description |
|-----------|----------|---------|-------------|
| `limit` | No | `50` | Max results per page |
| `cursor` | No | | Pagination cursor |
| `ruleId` | No | | Filter by rule ID |
| `status` | No | | Filter by status |

**Example:**
```bash
curl "http://localhost:11023/v1/automation-logs?limit=10&ruleId=auto_abc"
```

**Response**:
```json
{
  "logs": [...],
  "total": 25,
  "nextCursor": "...",
  "hasMore": true
}
```

---

### POST /v1/import-url

Import a markdown document from a URL. Automatically extracts YAML frontmatter.

**Request Body**:
```json
{
  "collection": "articles",
  "url": "https://example.com/post.md",
  "key": "imported-post",
  "lang": "en",
  "meta": { "source": ["web"] },
  "ttl": 86400
}
```

| Field | Type | Description |
|-------|------|-------------|
| `collection` | string | **Required.** Target collection |
| `url` | string | **Required.** URL to fetch |
| `key` | string | Document key (derived from URL path if empty) |
| `lang` | string | **Required.** Language code |
| `meta` | object | Metadata (merged with frontmatter) |
| `ttl` | integer | Time-to-live in seconds |

**Response**: Saved document object.

---

### POST /v1/import-wiki

Import Wikipedia (MediaWiki) XML dumps. Supports `.xml` and `.xml.bz2` compressed files. Streams the XML — does not load the entire file into memory.

**Multipart Form Upload:**
```bash
curl -X POST http://localhost:11023/v1/import-wiki \
  -F "file=@enwiki-20260101-pages-articles.xml.bz2" \
  -F "collection=wikipedia" \
  -F "lang=en" \
  -F "skipRedirects=true" \
  -F "skipFts=true"
```

**Raw Stream (octet-stream):**
```bash
curl -X POST "http://localhost:11023/v1/import-wiki?collection=wikipedia&lang=en&skipRedirects=true&skipFts=true" \
  -H "Content-Type: application/x-bzip2" \
  --data-binary @enwiki-20260101-pages-articles.xml.bz2
```

| Field | Type | Description |
|-------|------|-------------|
| `collection` | string | **Required.** Target collection |
| `lang` | string | **Required.** Language code (e.g. `en`, `de`, `pl`) |
| `namespaces` | string | Comma-separated namespace IDs to import (default: `0` = articles only) |
| `skipRedirects` | bool | Skip redirect pages (default: `false`) |
| `skipFts` | bool | Skip FTS indexing during import for speed (default: `false`). Run `/v1/fts-reindex` after. |
| `maxPages` | int | Maximum pages to import (default: unlimited) |
| `batchSize` | int | Pages per batch commit (default: `500`) |

**Response:**
```json
{
  "imported": 1234567,
  "skipped": 456789,
  "failed": 0,
  "collection": "wikipedia",
  "durationMs": 3600000
}
```

**Metadata stored per document:** `source=wikipedia`, `wiki_id`, `wiki_title`, `wiki_ns`, `wiki_rev_id`, `wiki_timestamp`, `wiki_contributor`, `wiki_redirect` (if applicable).

---

### POST /v1/set-ttl

Set or remove document time-to-live.

**Request Body**:
```json
{
  "collection": "blog",
  "key": "temp-post",
  "lang": "en",
  "ttl": 3600
}
```

| Field | Type | Description |
|-------|------|-------------|
| `collection` | string | **Required.** Collection name |
| `key` | string | **Required.** Document key |
| `lang` | string | **Required.** Language code |
| `ttl` | integer | **Required.** Seconds until expiry; `0` to remove TTL |

**Response**: Updated document object with `expiresAt` field.

---

### GET /v1/meta-keys

List all unique metadata keys and their distinct values for a collection.

**Query Parameters:**
| Parameter | Required | Description |
|-----------|----------|-------------|
| `collection` | Yes | Collection name |

**Example:**
```bash
curl "http://localhost:11023/v1/meta-keys?collection=blog"
```

**Response**:
```json
{
  "meta": {
    "author": ["John", "Jane"],
    "category": ["blog", "tutorial"],
    "tags": ["golang", "database"]
  }
}
```

---

### GET /v1/checksum

Get CRC32 checksum of a collection for integrity verification.

**Query Parameters:**
| Parameter | Required | Description |
|-----------|----------|-------------|
| `collection` | Yes | Collection name |

**Example:**
```bash
curl "http://localhost:11023/v1/checksum?collection=blog"
```

**Response**:
```json
{
  "collection": "blog",
  "checksum": "a1b2c3d4",
  "documentCount": 42
}
```

---

### GET /v1/system/info

Returns system information including OS, memory, CPU, and network details.

**Example:**
```bash
curl http://localhost:11023/v1/system/info
```

**Response**:
```json
{
  "hostname": "server-1",
  "os": "linux",
  "arch": "amd64",
  "numCPU": 4,
  "goVersion": "go1.27.0",
  "version": "2.11.4",
  "uptimeSeconds": 3600,
  "memoryTotal": 134217728,
  "memoryUsed": 67108864,
  "numGoroutines": 12,
  "cpuUsagePercent": 15.3
}
```

---

### GET /v1/config

Returns server configuration overview.

**Example:**
```bash
curl http://localhost:11023/v1/config
```

**Response**:
```json
{
  "version": "2.11.4",
  "databasePath": "mddb.db",
  "mode": "wr",
  "protocols": {
    "http": { "enabled": true, "addr": ":11023" },
    "grpc": { "enabled": true, "addr": ":11024" },
    "mcp": { "enabled": true, "addr": ":11025" }
  },
  "authEnabled": false,
  "metricsEnabled": true,
  "vectorConfig": {
    "enabled": true,
    "provider": "openai",
    "model": "text-embedding-3-small",
    "dimensions": 1536
  },
  "automationsEnabled": true,
  "searchStatsEnabled": true
}
```

---

### GET /v1/endpoints

Returns list of all available endpoints across HTTP, gRPC, and MCP protocols.

**Example:**
```bash
curl http://localhost:11023/v1/endpoints
```

**Response**:
```json
{
  "http": [
    { "method": "POST", "path": "/v1/add", "description": "Add document", "requiresAuth": true }
  ],
  "grpc": [
    { "name": "AddDocument", "description": "Add a document" }
  ],
  "mcp": [
    { "name": "add_document", "description": "Add a document" }
  ]
}
```

---

### GET /health

Health check endpoint. Also available at `/v1/health`.

**Response**:
```json
{
  "status": "healthy",
  "mode": "wr"
}
```

Returns `503` with `"status": "unhealthy"` if the database is not accessible.

---

### POST /v1/auth/register

Register a new user. Requires admin privileges.

**Request Body**:
```json
{
  "username": "newuser",
  "password": "secret123"
}
```

**Response**:
```json
{
  "username": "newuser",
  "createdAt": 1709100000
}
```

---

### GET /v1/auth/me

Get current authenticated user information.

**Response**:
```json
{
  "username": "admin",
  "admin": true,
  "createdAt": 1709000000
}
```

---

### GET/POST /v1/auth/permissions

Get or set user permissions on collections.

**GET** — Query parameter `username`:
```bash
curl "http://localhost:11023/v1/auth/permissions?username=john"
```

**POST** — Set permission:
```json
{
  "username": "john",
  "collection": "blog",
  "read": true,
  "write": true,
  "admin": false
}
```

**Response** (POST): `{ "status": "ok" }`

---

### GET /v1/auth/users

List all users. Requires admin privileges.

**Response**:
```json
{
  "users": [
    {
      "username": "admin",
      "createdAt": 1709000000,
      "disabled": false,
      "admin": true,
      "groups": ["admins"]
    }
  ]
}
```

---

### DELETE /v1/auth/users/:username

Delete a user account. Requires admin privileges.

**Example:**
```bash
curl -X DELETE http://localhost:11023/v1/auth/users/john
```

**Response**: `{ "status": "deleted" }`

---

### GET/POST /v1/auth/groups

List all groups (GET) or create a new group (POST). Requires admin privileges.

**POST** — Create group:
```json
{
  "name": "editors",
  "description": "Content editors",
  "members": ["john", "jane"]
}
```

**Response** (POST): Created group object (201 Created).

---

### GET/PUT/DELETE /v1/auth/groups/:name

Manage a specific group. Requires admin privileges.

- **GET** returns the group object
- **PUT** updates description and members
- **DELETE** removes the group

---

### GET/POST /v1/auth/group-permissions

Get or set permissions for a group on collections.

**GET** — Query parameter `group`:
```bash
curl "http://localhost:11023/v1/auth/group-permissions?group=editors"
```

**POST** — Set group permission:
```json
{
  "group": "editors",
  "collection": "blog",
  "read": true,
  "write": true,
  "admin": false
}
```

**Response** (POST): `{ "status": "permission set" }`

---

## Data Models

### Document

```go
{
  "id": string,              // Auto-generated: "collection|key|lang"
  "key": string,             // Document key (e.g., "homepage")
  "lang": string,            // Language code (e.g., "en_GB")
  "meta": {                  // Metadata (multi-value)
    "key1": ["value1", "value2"],
    "key2": ["value3"]
  },
  "contentMd": string,       // Markdown content
  "addedAt": int64,          // Unix timestamp (first creation)
  "updatedAt": int64         // Unix timestamp (last update)
}
```

### Metadata

- Metadata is stored as `map[string][]string` (key → array of values)
- Each metadata key can have multiple values
- Metadata is automatically indexed for fast searching
- Common metadata keys: `category`, `author`, `tags`, `status`, etc.

---

## Code Documents

MDDB stores source the same way it stores prose: one document per file, with no
new type, table or endpoint. What changes is how the text is tokenised, and
that is decided by a convention on the ordinary flat `meta` map.

| Meta key | Value | Effect |
|---|---|---|
| `kind` | `code` | Index with the source-aware tokeniser |
| `language` | `css`, `html`, `javascript`, … | The source language; inferred from the extension when absent |
| `path` | `css/style.css` | The original path, when the document key is a slug |

```json
{
  "collection": "theme",
  "key": "css/style.css",
  "lang": "en",
  "contentMd": ".hero-banner { background: url(hero.png); }",
  "meta": { "kind": ["code"], "language": ["css"] }
}
```

**The convention is optional.** A document whose key or `path` ends in a known
source extension (`.css`, `.html`, `.js`, `.ts`, `.go`, `.py`, …) is treated as
code without any meta at all, so a theme ingested by an existing tool indexes
usefully as it stands. An explicit `kind` always wins, in both directions: set
`kind: ["prose"]` on a `.css` document and it is tokenised as prose.

### Finding the place, not just the file

A search result that names the document answers "which file". The question an
agent has is "where" — and answering it by reading the file is what makes a
one-line change cost thousands of tokens (issue #192).

Ask for `highlight: true` and every fragment carries the lines it occupies:

```json
{
  "results": [{
    "document": { "key": "css/style.css" },
    "highlights": [{
      "fragment": "…}\n\n.hero-banner {\n  <mark>background</mark>: url(hero.png);",
      "startLine": 158,
      "endLine": 163,
      "startOffset": 4021,
      "endOffset": 4104
    }]
  }]
}
```

`startLine` and `endLine` are 1-based and inclusive. Byte offsets are still
there for callers doing their own markup on the same bytes.

**Combine it with a projection.** `fields` drops the document body, `highlight`
says where to look; together they answer without carrying the file. The whole
response above is under 800 bytes for a 164-line stylesheet. The two are
deliberately compatible — a projection keeps the fragments.

`fragmentSize` is a byte budget tuned for prose. Source lines are short, so 150
bytes covers roughly fifteen lines of CSS against two or three of prose; lower
it when searching code.

Vector and hybrid hits carry the same information: in `chunk` and `window`
retrieval modes each result reports `startLine`/`endLine` for the passage it
matched, widened along with the passage when a window is requested.

### Why the tokeniser differs

The prose tokeniser stems (`classes` → `class`), drops stop words (`for`, `if`,
`class` — the keywords of the language being searched) and splits on every
punctuation mark, which leaves `.hero-banner` findable only as two unrelated
words. The code tokeniser keeps the whole identifier **and** emits its parts,
across camelCase, snake_case and kebab-case:

| In the source | Indexed as |
|---|---|
| `.hero-banner` | `hero-banner`, `hero`, `banner` |
| `checkoutHandler` | `checkouthandler`, `checkout`, `handler` |
| `XMLHttpRequest` | `xmlhttprequest`, `xml`, `http`, `request` |
| `MAX_RETRY_COUNT` | `max_retry_count`, `max`, `retry`, `count` |

Because both the whole name and its parts are indexed, a code collection is
searchable with ordinary queries — nothing in the search path needs to know the
collection holds source. The one asymmetry is stemming on the query side: a
search for `classes` stems to `class` and will not match an unstemmed `classes`
in a code index. Searching source for an English plural is rare enough to
accept; searching it for `class` finds every declaration.

Single characters and digits are kept, unlike in prose: `a` is a real selector
and `h2` and `utf8` are real names.

## Error Handling

### Error Response Format

```json
{
  "error": "error message description"
}
```

### HTTP Status Codes

| Code | Description |
|------|-------------|
| `200` | Success |
| `400` | Bad Request - Invalid JSON or missing required fields |
| `403` | Forbidden - Write operation in read-only mode |
| `404` | Not Found - Document doesn't exist |
| `500` | Internal Server Error |

### Common Errors

**Missing required fields**:
```json
{
  "error": "missing fields"
}
```

**Document not found**:
```json
{
  "error": "not found"
}
```

**Read-only mode**:
```json
{
  "error": "read-only mode"
}
```

---

## Best Practices

### 1. Document Keys
- Use descriptive, URL-friendly keys
- Keep keys consistent within a collection
- Example: `homepage`, `about-us`, `blog-post-1`

### 2. Language Codes
- Use standard language codes (ISO 639-1 + ISO 3166-1)
- Examples: `en_US`, `en_GB`, `pl_PL`, `de_DE`

### 3. Metadata
- Keep metadata keys consistent across documents
- Use arrays even for single values (for consistency)
- Index frequently queried fields

### 4. Collections
- Group related documents in collections
- Use collections like database tables
- Examples: `blog`, `pages`, `products`, `docs`

### 5. Revisions
- Regularly truncate old revisions to save space
- Keep enough history for your audit requirements
- Consider keeping 5-10 recent revisions

### 6. Backups
- Schedule regular backups
- Store backups in a different location
- Test restore procedures periodically

---

## Performance Tips

1. **Indexing**: Metadata is automatically indexed - use it for filtering
2. **Pagination**: Always use `limit` and `offset` for large result sets
3. **Batch Operations**: Use export/import for bulk operations
4. **Revisions**: Truncate old revisions regularly to keep database size manageable
5. **Read Mode**: Use read-only mode for read-heavy workloads with separate write instances

---

## Memory RAG Endpoints

Conversational memory system for RAG applications. Store, search, and recall conversation history with semantic search.

### POST /v1/memory/session

Create a new memory/conversation session.

**Request:**
```json
{
  "userId": "user-1",
  "scenario": "customer_support",
  "title": "Session about search API",
  "meta": {"department": "engineering"},
  "ttl": 2592000
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `userId` | string | Yes | User identifier |
| `scenario` | string | No | Session context/scenario |
| `title` | string | No | Human-readable title (auto-generated if empty) |
| `meta` | object | No | Additional metadata key-value pairs |
| `ttl` | int | No | TTL in seconds (default: 30 days) |

**Response:**
```json
{
  "sessionId": "a1b2c3d4e5f6...",
  "userId": "user-1",
  "scenario": "customer_support",
  "title": "Session about search API",
  "createdAt": 1743400000,
  "expiresAt": 1745992000
}
```

### POST /v1/memory/message

Add a message to an existing session. Messages are automatically embedded for semantic recall.

**Request:**
```json
{
  "sessionId": "a1b2c3d4e5f6...",
  "role": "user",
  "content": "How does vector search work?",
  "meta": {"topic": "search", "source": "docs"}
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `sessionId` | string | Yes | Session ID from `/v1/memory/session` |
| `role` | string | Yes | `user`, `assistant`, `system`, or `tool` |
| `content` | string | Yes | Message content (markdown supported) |
| `meta` | object | No | Extra metadata (topic, source, tool_call, etc.) |

**Response:**
```json
{
  "messageId": "memory_messages|...",
  "sessionId": "a1b2c3d4e5f6...",
  "role": "user",
  "createdAt": 1743400100,
  "embedded": true
}
```

### POST /v1/memory/recall

Semantically recall relevant messages from past conversations using hybrid search (vector + keyword).

**Request:**
```json
{
  "query": "How does vector search work?",
  "userId": "user-1",
  "sessionId": "",
  "role": "assistant",
  "topK": 10,
  "threshold": 0.5,
  "strategy": "hybrid",
  "alpha": 0.5,
  "includeContent": true,
  "filterMeta": {}
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `query` | string | Yes | Natural language recall query |
| `userId` | string | No | Filter to sessions belonging to this user |
| `sessionId` | string | No | Filter to a specific session |
| `role` | string | No | Filter by message role |
| `topK` | int | No | Number of results (default: 10) |
| `threshold` | float | No | Min similarity score 0-1 |
| `strategy` | string | No | `hybrid` (default), `semantic`, `keyword` |
| `alpha` | float | No | Weight 0-1 (0=keyword, 1=semantic) |
| `includeContent` | bool | No | Include full message content |
| `filterMeta` | object | No | Additional metadata filters |

**Response:**
```json
{
  "results": [
    {
      "document": {"id": "...", "key": "...", "meta": {...}, "contentMd": "..."},
      "score": 0.87,
      "rank": 1,
      "sessionId": "a1b2c3d4e5f6...",
      "role": "assistant",
      "matchStrategy": "hybrid"
    }
  ],
  "total": 5,
  "strategy": "hybrid",
  "query": "How does vector search work?"
}
```

### POST /v1/memory/summarize

Generate and store a summary of a session's conversation.

**Request:**
```json
{
  "sessionId": "a1b2c3d4e5f6...",
  "userId": "user-1"
}
```

**Response:**
```json
{
  "summaryId": "memory_summaries|...",
  "sessionId": "a1b2c3d4e5f6...",
  "summary": "# Session Summary: a1b2c3d4\n\nMessages: 5\n\n## Conversation\n\n...",
  "createdAt": 1743401000,
  "messages": 5
}
```

### POST /v1/memory/sessions

List memory sessions with optional filtering.

**Request:**
```json
{
  "userId": "user-1",
  "scenario": "customer_support",
  "limit": 50,
  "offset": 0,
  "sort": "createdAt",
  "asc": false
}
```

**Response:**
```json
{
  "sessions": [
    {
      "sessionId": "a1b2c3d4e5f6...",
      "userId": "user-1",
      "scenario": "customer_support",
      "title": "Session about search API",
      "createdAt": 1743400000,
      "updatedAt": 1743401000,
      "expiresAt": 1745992000,
      "messageCount": 12
    }
  ],
  "total": 3
}
```

### POST /v1/memory/history

Get the full message history for a session, ordered chronologically.

**Request:**
```json
{
  "sessionId": "a1b2c3d4e5f6...",
  "limit": 100,
  "offset": 0
}
```

**Response:**
```json
{
  "messages": [
    {"id": "...", "key": "...", "meta": {"role": ["user"], "sessionId": ["..."]}, "contentMd": "How does vector search work?", "addedAt": 1743400100},
    {"id": "...", "key": "...", "meta": {"role": ["assistant"], "sessionId": ["..."]}, "contentMd": "Vector search uses embeddings...", "addedAt": 1743400110}
  ],
  "total": 2
}
```

---

## Curation Rules (v2.9.14+)

Editorial overrides for search ranking: pin documents to fixed positions and/or hide others for specific queries. Applied in FTS + Hybrid pipelines after scoring, before pagination.

### GET /v1/curation

List rules. Pass `id=<id>` for a single rule, `collection=<c>` to scope by collection, or omit both to list all.

```bash
curl "http://localhost:11023/v1/curation?collection=blog"
```

### POST /v1/curation

Create a new rule. Server assigns the `id`.

```bash
curl -X POST http://localhost:11023/v1/curation \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "query": "rust tutorial",
    "matchMode": "exact",
    "enabled": true,
    "pins": [
      {"key": "rust-getting-started", "position": 1},
      {"key": "rust-ownership", "position": 2}
    ],
    "hides": ["legacy-post"]
  }'
```

**Fields:**
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `collection` | string | Yes | Collection scope |
| `query` | string | Yes | Trigger query text |
| `matchMode` | string | No | `exact` (default) or `contains`; case-insensitive |
| `pins` | array | No | `[{key, lang?, position}]` — `position` is 1-based; `<=0` appends after organic results |
| `hides` | array | No | Document keys to drop from results |
| `enabled` | bool | No | Default `false` via REST — pass `true` to activate immediately |

### PUT /v1/curation

Replace an existing rule. Body must include `id`. `createdAt` is preserved server-side.

### DELETE /v1/curation?id=<id>

Remove a rule by id.

### Response markers

Results injected by a pin carry `"pinned": true` on `FTSResultWithDoc` and `HybridSearchResultItem`, so clients can style them.
