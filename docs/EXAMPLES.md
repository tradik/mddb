---
title: "MDDB Usage Examples"
slug: "docs/examples"
description: "Practical MDDB examples: document CRUD, metadata filters, full-text and vector queries, hybrid search, exports, and client code in several languages."
status: publish
---

# MDDB Usage Examples

## Basic Operations

### Adding Documents

```bash
# Simple document
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "key": "hello",
    "lang": "en_US",
    "meta": {"category": ["blog"]},
    "contentMd": "# Hello World"
  }'

# Document with multiple metadata values
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "key": "tutorial",
    "lang": "en_US",
    "meta": {
      "category": ["tutorial", "beginner"],
      "tags": ["golang", "database", "markdown"],
      "author": ["John Doe"]
    },
    "contentMd": "# Tutorial Content"
  }'
```

### Retrieving Documents

```bash
# Basic retrieval
curl -X POST http://localhost:11023/v1/get \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "key": "hello",
    "lang": "en_US"
  }'

# With template variables
curl -X POST http://localhost:11023/v1/get \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "key": "hello",
    "lang": "en_US",
    "env": {
      "year": "2024",
      "siteName": "My Blog"
    }
  }'
```

## Search Examples

### Basic Search

```bash
# All documents in collection
curl -X POST http://localhost:11023/v1/search \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "limit": 50
  }'

# Search by category
curl -X POST http://localhost:11023/v1/search \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "filterMeta": {"category": ["tutorial"]},
    "sort": "addedAt",
    "asc": false
  }'
```

### Advanced Filtering

```bash
# Multiple categories (OR logic)
curl -X POST http://localhost:11023/v1/search \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "filterMeta": {
      "category": ["tutorial", "guide", "howto"]
    }
  }'

# Multiple criteria (AND logic)
curl -X POST http://localhost:11023/v1/search \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "filterMeta": {
      "category": ["tutorial"],
      "author": ["John Doe"],
      "status": ["published"]
    }
  }'
```

### Pagination

```bash
# Page 1 (first 10 items)
curl -X POST http://localhost:11023/v1/search \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "limit": 10,
    "offset": 0
  }'

# Page 2 (next 10 items)
curl -X POST http://localhost:11023/v1/search \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "limit": 10,
    "offset": 10
  }'
```

### Full-Text Search (v2.9.12+ and v2.9.13+ features)

```bash
# Per-query boost / demote (v2.9.12+) — rerank without reindexing
curl -X POST http://localhost:11023/v1/fts \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "query": "tutorial",
    "algorithm": "bm25",
    "boost": {"tag:featured": 5.0, "status:archived": -2.0}
  }'

# Prefix autocomplete for type-ahead UI (v2.9.12+)
curl "http://localhost:11023/v1/autocomplete?collection=blog&q=mar&topN=5"

# Expression mode — parens, precedence, mixed atoms (v2.9.13+)
curl -X POST http://localhost:11023/v1/fts \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "query": "(rust OR golang) AND \"async runtime\"~3 NOT legacy",
    "mode": "expression"
  }'

# Highlighting with fragments (v2.9.13+) — returns snippets per result
curl -X POST http://localhost:11023/v1/fts \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "query": "markdown database",
    "highlight": true,
    "highlightTag": "<mark>",
    "maxHighlights": 2,
    "fragmentSize": 120
  }'
```

### Hybrid Search with Geo Distance Sort (v2.9.13+)

```bash
# Nearest matching venue — sort by proximity instead of fused score
curl -X POST http://localhost:11023/v1/hybrid-search \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "venues",
    "query": "coffee wifi",
    "topK": 10,
    "geo": {"lat": 52.52, "lng": 13.405, "radiusMeters": 3000},
    "sort": "distance"
  }'
```

### GeoJSON Polygon / Multi-Polygon Containment (v2.9.13+)

```bash
# Find points inside a custom polygon (Berlin Mitte bounding box)
curl -X POST http://localhost:11023/v1/geo-polygon \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "venues",
    "polygon": {
      "type": "Polygon",
      "coordinates": [
        [[13.36,52.51],[13.42,52.51],[13.42,52.53],[13.36,52.53],[13.36,52.51]]
      ]
    }
  }'

# Polygon with a hole — square minus central square
curl -X POST http://localhost:11023/v1/geo-polygon \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "venues",
    "polygon": {
      "coordinates": [
        [[0,0],[10,0],[10,10],[0,10],[0,0]],
        [[3,3],[7,3],[7,7],[3,7],[3,3]]
      ]
    }
  }'

# MultiPolygon union across two regions (Berlin + Paris)
curl -X POST http://localhost:11023/v1/geo-polygon \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "venues",
    "multiPolygon": {
      "coordinates": [
        [[[13.36,52.51],[13.42,52.51],[13.42,52.53],[13.36,52.53],[13.36,52.51]]],
        [[[2.34,48.85],[2.36,48.85],[2.36,48.87],[2.34,48.87],[2.34,48.85]]]
      ]
    }
  }'
```

### Async Bulk Ingest with Job Tracking (v2.9.12+)

```bash
# Submit a long-running import — returns 202 with a job id
JOB=$(curl -s -X POST http://localhost:11023/v1/bulk-ingest-job \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "archive",
    "documents": [ ... tens of thousands of docs ... ],
    "callbackUrl": "https://example.com/webhook"
  }' | jq -r .id)

# Poll status until completed / failed
curl "http://localhost:11023/v1/bulk-ingest-job/$JOB"

# List all jobs for a collection, newest first
curl "http://localhost:11023/v1/bulk-ingest-jobs?collection=archive"

# Cancel a pending job (already-processing jobs run to completion)
curl -X DELETE "http://localhost:11023/v1/bulk-ingest-job/$JOB"
```

## Vector Search (Semantic Search)

### Setup

First, configure an embedding provider:
```bash
# Option 1: OpenAI (cloud, best quality)
export MDDB_EMBEDDING_PROVIDER=openai
export MDDB_EMBEDDING_API_KEY=sk-your-key-here

# Option 2: Voyage AI / Anthropic (cloud)
export MDDB_EMBEDDING_PROVIDER=voyage
export MDDB_EMBEDDING_API_KEY=pa-your-key-here

# Option 3: Ollama (local, free, no API key)
export MDDB_EMBEDDING_PROVIDER=ollama
# Make sure ollama is running: ollama serve
# Pull model: ollama pull nomic-embed-text
```

### Adding Documents (auto-embedding)

When an embedding provider is configured, documents are automatically embedded when added:

```bash
# Add a document about authentication
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "docs",
    "key": "auth-guide",
    "lang": "en_US",
    "meta": {"category": ["security"], "type": ["guide"]},
    "contentMd": "# Authentication Guide\n\nThis guide covers user authentication using JWT tokens. Learn how to implement login, registration, and session management."
  }'

# Add more documents
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "docs",
    "key": "api-reference",
    "lang": "en_US",
    "meta": {"category": ["api"], "type": ["reference"]},
    "contentMd": "# API Reference\n\nComplete REST API reference with endpoints, parameters, and response formats."
  }'

curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "docs",
    "key": "deployment",
    "lang": "en_US",
    "meta": {"category": ["devops"], "type": ["guide"]},
    "contentMd": "# Deployment Guide\n\nHow to deploy the application to production using Docker, Kubernetes, and CI/CD pipelines."
  }'
```

### Semantic Search (finding documents by meaning)

```bash
# Search by meaning - finds auth-guide even though query doesn't match exact keywords
curl -X POST http://localhost:11023/v1/vector-search \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "docs",
    "query": "how to login users",
    "topK": 3,
    "includeContent": true
  }'
# Returns: auth-guide (score: ~0.85), api-reference (score: ~0.42), ...
```

Response shows which documents are most relevant and their similarity scores:
```json
{
  "results": [
    {
      "document": {
        "id": "docs|auth-guide|en_us",
        "key": "auth-guide",
        "meta": {"category": ["security"], "type": ["guide"]},
        "contentMd": "# Authentication Guide\n..."
      },
      "score": 0.85,
      "rank": 1
    },
    {
      "document": {
        "id": "docs|api-reference|en_us",
        "key": "api-reference",
        "meta": {"category": ["api"], "type": ["reference"]}
      },
      "score": 0.42,
      "rank": 2
    }
  ],
  "total": 2,
  "model": "text-embedding-3-small",
  "dimensions": 1536
}
```

### Hybrid Search (vector + metadata filter)

Combine semantic search with metadata pre-filtering:
```bash
# Find security-related docs about passwords
curl -X POST http://localhost:11023/v1/vector-search \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "docs",
    "query": "password reset flow",
    "topK": 5,
    "filterMeta": {"category": ["security"]},
    "includeContent": false
  }'
```

### Search with Similarity Threshold

```bash
# Only return highly relevant results (score > 0.7)
curl -X POST http://localhost:11023/v1/vector-search \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "docs",
    "query": "kubernetes deployment",
    "topK": 10,
    "threshold": 0.7
  }'
```

### Reindex Existing Documents

If you added documents before configuring embeddings, or changed the model:
```bash
# Reindex only changed documents (smart - skips already-embedded)
curl -X POST http://localhost:11023/v1/vector-reindex \
  -H 'Content-Type: application/json' \
  -d '{"collection": "docs", "force": false}'

# Force reindex everything (after model change)
curl -X POST http://localhost:11023/v1/vector-reindex \
  -H 'Content-Type: application/json' \
  -d '{"collection": "docs", "force": true}'
```

### Check Embedding Status

```bash
# See which collections have embeddings
curl http://localhost:11023/v1/vector-stats | jq
```

Response:
```json
{
  "enabled": true,
  "provider": "text-embedding-3-small",
  "model": "text-embedding-3-small",
  "dimensions": 1536,
  "index_ready": true,
  "collections": {
    "docs": {
      "total_documents": 3,
      "embedded_documents": 3
    }
  }
}
```

## Export Examples

### NDJSON Export

```bash
# Export all documents
curl -X POST http://localhost:11023/v1/export \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "format": "ndjson"
  }' > export.ndjson

# Export filtered documents
curl -X POST http://localhost:11023/v1/export \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "filterMeta": {"status": ["published"]},
    "format": "ndjson"
  }' > published.ndjson
```

### ZIP Export

```bash
# Export as ZIP
curl -X POST http://localhost:11023/v1/export \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "format": "zip"
  }' > export.zip
```

## Backup & Restore

### Creating Backups

```bash
# Automatic timestamp
curl "http://localhost:11023/v1/backup?to=backup-$(date +%s).db"

# Custom name
curl "http://localhost:11023/v1/backup?to=my-backup.db"

# Daily backup script
#!/bin/bash
DATE=$(date +%Y-%m-%d)
curl "http://localhost:11023/v1/backup?to=backup-${DATE}.db"
```

### Restoring from Backup

```bash
curl -X POST http://localhost:11023/v1/restore \
  -H 'Content-Type: application/json' \
  -d '{"from": "backup-1699296000.db"}'
```

## Maintenance

### Truncating Revisions

```bash
# Keep last 5 revisions per document
curl -X POST http://localhost:11023/v1/truncate \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "keepRevs": 5,
    "dropCache": true
  }'

# Remove all revision history
curl -X POST http://localhost:11023/v1/truncate \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "keepRevs": 0,
    "dropCache": true
  }'
```

## Shell Scripts

### Bulk Import

```bash
#!/bin/bash
# import-posts.sh

for file in posts/*.md; do
  key=$(basename "$file" .md)
  content=$(cat "$file")
  
  curl -X POST http://localhost:11023/v1/add \
    -H 'Content-Type: application/json' \
    -d "{
      \"collection\": \"blog\",
      \"key\": \"$key\",
      \"lang\": \"en_US\",
      \"meta\": {\"category\": [\"blog\"]},
      \"contentMd\": $(echo "$content" | jq -Rs .)
    }"
done
```

### Export and Process

```bash
#!/bin/bash
# export-and-process.sh

# Export all posts
curl -X POST http://localhost:11023/v1/export \
  -H 'Content-Type: application/json' \
  -d '{"collection": "blog", "format": "ndjson"}' | \
  jq -r '.key + ": " + .meta.category[0]'
```

### Automated Backup

```bash
#!/bin/bash
# backup.sh - Add to crontab for daily backups

BACKUP_DIR="/backups/mddb"
DATE=$(date +%Y-%m-%d-%H%M%S)
KEEP_DAYS=7

# Create backup
curl "http://localhost:11023/v1/backup?to=${BACKUP_DIR}/backup-${DATE}.db"

# Remove old backups
find ${BACKUP_DIR} -name "backup-*.db" -mtime +${KEEP_DAYS} -delete
```

## Integration Examples

### Node.js Client

```javascript
// mddb-client.js
const axios = require('axios');

class MDDBClient {
  constructor(baseURL = 'http://localhost:11023') {
    this.client = axios.create({ baseURL });
  }

  async add(collection, key, lang, meta, contentMd) {
    const response = await this.client.post('/v1/add', {
      collection, key, lang, meta, contentMd
    });
    return response.data;
  }

  async get(collection, key, lang, env = {}) {
    const response = await this.client.post('/v1/get', {
      collection, key, lang, env
    });
    return response.data;
  }

  async search(collection, filterMeta = {}, options = {}) {
    const response = await this.client.post('/v1/search', {
      collection,
      filterMeta,
      ...options
    });
    return response.data;
  }
}

// Usage
const mddb = new MDDBClient();

await mddb.add('blog', 'hello', 'en_US', 
  { category: ['blog'] }, 
  '# Hello World'
);

const doc = await mddb.get('blog', 'hello', 'en_US');
console.log(doc);
```

### Python Client

```python
# mddb_client.py
import requests

class MDDBClient:
    def __init__(self, base_url='http://localhost:11023'):
        self.base_url = base_url
    
    def add(self, collection, key, lang, meta, content_md):
        response = requests.post(f'{self.base_url}/v1/add', json={
            'collection': collection,
            'key': key,
            'lang': lang,
            'meta': meta,
            'contentMd': content_md
        })
        return response.json()
    
    def get(self, collection, key, lang, env=None):
        response = requests.post(f'{self.base_url}/v1/get', json={
            'collection': collection,
            'key': key,
            'lang': lang,
            'env': env or {}
        })
        return response.json()
    
    def search(self, collection, filter_meta=None, **options):
        response = requests.post(f'{self.base_url}/v1/search', json={
            'collection': collection,
            'filterMeta': filter_meta or {},
            **options
        })
        return response.json()

# Usage
mddb = MDDBClient()

mddb.add('blog', 'hello', 'en_US', 
         {'category': ['blog']}, 
         '# Hello World')

doc = mddb.get('blog', 'hello', 'en_US')
print(doc)
```

### Go Client

```go
// mddb_client.go
package main

import (
    "bytes"
    "encoding/json"
    "net/http"
)

type MDDBClient struct {
    BaseURL string
}

func (c *MDDBClient) Add(collection, key, lang string, meta map[string][]string, contentMd string) (map[string]interface{}, error) {
    data := map[string]interface{}{
        "collection": collection,
        "key": key,
        "lang": lang,
        "meta": meta,
        "contentMd": contentMd,
    }
    
    body, _ := json.Marshal(data)
    resp, err := http.Post(c.BaseURL+"/v1/add", "application/json", bytes.NewBuffer(body))
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var result map[string]interface{}
    json.NewDecoder(resp.Body).Decode(&result)
    return result, nil
}

// Usage
func main() {
    client := &MDDBClient{BaseURL: "http://localhost:11023"}

    doc, _ := client.Add("blog", "hello", "en_US",
        map[string][]string{"category": {"blog"}},
        "# Hello World")

    fmt.Println(doc)
}
```

## Schema Validation

Schema validation enforces structure on document metadata per collection. It is opt-in -- collections without a schema accept any metadata.

### Setting Up a Schema

```bash
# Define a schema for the "products" collection
curl -X POST http://localhost:11023/v1/schema/set \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "products",
    "schema": {
      "required": ["sku", "price", "category"],
      "properties": {
        "sku":      { "type": "string", "pattern": "^SKU-[0-9]+$" },
        "price":    { "type": "number" },
        "category": { "type": "string", "enum": ["electronics", "clothing", "books"] },
        "tags":     { "type": "string", "minItems": 1, "maxItems": 10 }
      }
    }
  }'
```

### Adding Documents with Validation

```bash
# This succeeds -- all required fields present with valid values
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "products",
    "key": "laptop-1",
    "lang": "en_US",
    "meta": {
      "sku": ["SKU-10042"],
      "price": ["999.99"],
      "category": ["electronics"],
      "tags": ["laptop", "computer", "portable"]
    },
    "contentMd": "# Laptop Pro 15\n\nHigh-performance laptop."
  }'

# This fails -- missing "price", invalid sku format, bad category
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "products",
    "key": "bad-product",
    "lang": "en_US",
    "meta": {
      "sku": ["INVALID"],
      "category": ["furniture"]
    },
    "contentMd": "# Bad Product"
  }'
# Error: schema validation failed: missing required metadata key "price";
#   value "INVALID" for key "sku" does not match pattern "^SKU-[0-9]+$";
#   value "furniture" for key "category" is not in allowed enum values [electronics, clothing, books]
```

### Dry-Run Validation

```bash
# Check metadata before adding a document
curl -X POST http://localhost:11023/v1/validate \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "products",
    "meta": {
      "sku": ["SKU-20001"],
      "price": ["49.99"],
      "category": ["books"]
    }
  }'
# Response: {"valid": true, "errors": []}
```

### Listing and Managing Schemas

```bash
# List all schemas
curl -X POST http://localhost:11023/v1/schema/list \
  -H 'Content-Type: application/json' \
  -d '{}'

# Get schema for a specific collection
curl -X POST http://localhost:11023/v1/schema/get \
  -H 'Content-Type: application/json' \
  -d '{"collection": "products"}'

# Delete schema to disable validation
curl -X POST http://localhost:11023/v1/schema/delete \
  -H 'Content-Type: application/json' \
  -d '{"collection": "products"}'
```

### Blog Schema Example

```bash
# Enforce consistent blog metadata
curl -X POST http://localhost:11023/v1/schema/set \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "schema": {
      "required": ["category", "author", "status"],
      "properties": {
        "category": { "type": "string", "enum": ["blog", "tutorial", "news", "changelog"] },
        "author":   { "type": "string" },
        "status":   { "type": "string", "enum": ["draft", "published", "archived"] },
        "tags":     { "type": "string", "maxItems": 5 },
        "featured": { "type": "boolean", "maxItems": 1 }
      }
    }
  }'

# Add a valid blog post
curl -X POST http://localhost:11023/v1/add \
  -H 'Content-Type: application/json' \
  -d '{
    "collection": "blog",
    "key": "schema-validation-guide",
    "lang": "en_US",
    "meta": {
      "category": ["tutorial"],
      "author": ["Jane Doe"],
      "status": ["published"],
      "tags": ["mddb", "validation", "schema"],
      "featured": ["true"]
    },
    "contentMd": "# Schema Validation Guide\n\nLearn how to validate document metadata."
  }'
```

## Security & Compliance (v2.9.15+)

Worked examples for the ISO 27001 / SOC 2 hardening features. See [SECURITY.md](SECURITY.md) for the full compliance map and [config.md](config.md) for every environment variable.

### Enable the audit log and query events by actor

```bash
# 1) Start the server with the audit log on
export MDDB_AUTH_ENABLED=true
export MDDB_AUTH_JWT_SECRET=$(openssl rand -hex 32)
export MDDB_AUTH_ADMIN_USERNAME=admin
export MDDB_AUTH_ADMIN_PASSWORD=changeme
export MDDB_AUDIT_ENABLED=true
export MDDB_AUDIT_RETENTION_DAYS=365
./mddbd &

# 2) Log in as admin to get a JWT
ADMIN_JWT=$(curl -s -X POST http://localhost:11023/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"changeme"}' | jq -r .token)

# 3) Generate some audited activity — a failed login
curl -s -X POST http://localhost:11023/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"wrong"}'

# 4) Query every failed auth attempt by actor=alice
curl -s -H "Authorization: Bearer $ADMIN_JWT" \
  "http://localhost:11023/v1/audit?actor=alice&result=fail&limit=50" | jq .

# Response shape:
# { "events": [ { "ts": 1740..., "actor":"alice","action":"auth.login",
#                 "result":"fail","ip":"...","userAgent":"..." } ],
#   "count": 1, "dropped": 0 }
```

### Flip `MDDB_PRODUCTION=true` with all guardrails set

```bash
# Every variable below is required when MDDB_PRODUCTION=true.
# Missing any one aborts startup with a per-variable checklist.

export MDDB_PRODUCTION=true
export MDDB_AUTH_ENABLED=true
export MDDB_AUTH_JWT_SECRET=$(openssl rand -hex 32)      # >= 32 bytes
export MDDB_TLS_ENABLED=true
export MDDB_TLS_CERT=/etc/mddb/server.crt
export MDDB_TLS_KEY=/etc/mddb/server.key
export MDDB_CORS_ORIGIN=https://app.example.com          # not "*"
export MDDB_AUDIT_ENABLED=true
export MDDB_RATE_LIMIT_ENABLED=true

./mddbd
# On success:  ✓ Production guards satisfied (ISO 27001 / SOC 2)

# Operator-facing health probe — no auth required
curl -s http://localhost:11023/v1/compliance-status | jq .
# { "production": true, "compliant": true, "missing": [], "missingCount": 0 }
```

### Trigger the rate limiter and read `429` + `Retry-After`

```bash
export MDDB_RATE_LIMIT_ENABLED=true
export MDDB_RATE_LIMIT_REQUESTS=10
export MDDB_RATE_LIMIT_WINDOW=60
export MDDB_RATE_LIMIT_BURST=5
export MDDB_RATE_LIMIT_BY=ip
# Behind a reverse proxy only: whose X-Forwarded-For to believe.
# export MDDB_TRUSTED_PROXIES=127.0.0.1,10.0.0.0/8
./mddbd &

# Fire 20 requests back-to-back. The first 10 succeed (+5 burst);
# the rest return 429 with a Retry-After header.
for i in $(seq 1 20); do
  printf "req %02d -> " "$i"
  curl -s -o /dev/null -w "HTTP %{http_code}  X-RateLimit-Remaining=%header{x-ratelimit-remaining}  Retry-After=%header{retry-after}\n" \
    http://localhost:11023/v1/stats
done

# Last few lines look like:
# req 16 -> HTTP 429  X-RateLimit-Remaining=0  Retry-After=42
# req 17 -> HTTP 429  X-RateLimit-Remaining=0  Retry-After=42
```

### Encrypt a collection end-to-end and verify ciphertext on disk

```bash
# 1) Generate a 32-byte encryption key and start the server
export MDDB_ENCRYPTION_KEY="$(openssl rand -base64 32)"
./mddbd &

# 2) Flip the target collection to encrypted=true (admin only)
curl -s -X PUT http://localhost:11023/v1/collection-config \
  -H "Authorization: Bearer $ADMIN_JWT" \
  -H "Content-Type: application/json" \
  -d '{"collection":"secrets","encrypted":true}'

# 3) Write a document — stored ciphertext
curl -s -X POST http://localhost:11023/v1/add \
  -H "Authorization: Bearer $ADMIN_JWT" \
  -H "Content-Type: application/json" \
  -d '{"collection":"secrets","key":"api-key-1","lang":"en_US",
       "contentMd":"# Production API key\nsk-live-XXXXXXXXXXXX"}'

# 4) Read back — transparent decryption
curl -s -X POST http://localhost:11023/v1/get \
  -H "Authorization: Bearer $ADMIN_JWT" \
  -H "Content-Type: application/json" \
  -d '{"collection":"secrets","key":"api-key-1","lang":"en_US"}' | jq -r .contentMd

# 5) Verify the on-disk value is ciphertext by inspecting the BoltDB file.
#    The wire format is:  "MDDB_ENC_V1\x00" (12 B magic) + 12 B nonce + AES-GCM ct + tag.
#    Any hex dumper works; the magic prefix is grep-able:
strings mddb.db | grep -a MDDB_ENC_V1 | head -3
# Output (three-ish matches per encrypted value, binary-obscured after the magic):
# MDDB_ENC_V1
# MDDB_ENC_V1
# MDDB_ENC_V1

# NB: losing $MDDB_ENCRYPTION_KEY is terminal — escrow it offline.
```

### Subscribe to `security.auth_failure_burst` and trigger it

```bash
# 1) Tune the detector — 5 failures in 30s fires one event, then 60s cooldown.
export MDDB_INCIDENT_AUTH_THRESHOLD=5
export MDDB_INCIDENT_AUTH_WINDOW_SEC=30
export MDDB_INCIDENT_AUTH_COOLDOWN_SEC=60
./mddbd &

# 2) Register a webhook for the incident
curl -s -X POST http://localhost:11023/v1/webhooks \
  -H "Authorization: Bearer $ADMIN_JWT" \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://webhook.site/your-unique-id",
    "events": ["security.auth_failure_burst"]
  }'

# 3) Produce 10 failed logins from the same IP
for i in $(seq 1 10); do
  curl -s -o /dev/null -X POST http://localhost:11023/v1/auth/login \
    -H "Content-Type: application/json" \
    -d '{"username":"alice","password":"wrong"}'
done

# 4) The webhook receiver gets a POST with headers and body like:
#    X-MDDB-Event: security.auth_failure_burst
#    X-MDDB-Webhook-ID: wh_abc123
#
#    { "event": "security.auth_failure_burst",
#      "ts": 1740000000000000000,
#      "detail": {
#        "actor": "alice",
#        "ip": "203.0.113.5",
#        "count": 10,
#        "windowSec": 30
#      } }
```

