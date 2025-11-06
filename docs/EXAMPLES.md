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
