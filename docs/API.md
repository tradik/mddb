# MDDB API Documentation

> **Note**: The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in [RFC 2119](https://www.ietf.org/rfc/rfc2119.txt).

## Table of Contents
- [Overview](#overview)
- [Configuration](#configuration)
- [Endpoints](#endpoints)
  - [POST /v1/add](#post-v1add)
  - [POST /v1/get](#post-v1get)
  - [POST /v1/search](#post-v1search)
  - [POST /v1/export](#post-v1export)
  - [GET /v1/backup](#get-v1backup)
  - [POST /v1/restore](#post-v1restore)
  - [POST /v1/truncate](#post-v1truncate)
  - [GET /v1/stats](#get-v1stats)
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
| `MDDB_MODE` | `wr` | Access mode: `read`, `write`, or `wr` (read+write) |
| `MDDB_PATH` | `mddb.db` | Path to the BoltDB database file |

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
