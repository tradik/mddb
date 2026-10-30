---
title: "MDDB Use Cases"
slug: "docs/use-cases"
description: "Real-world MDDB use cases: documentation, RAG knowledge bases, CMS search, migrations from Elasticsearch and several tenants on one server."
status: publish
---

# MDDB Use Cases

Real-world examples and use cases for MDDB.

The examples assume a server on `localhost:11023` with authentication off, as
`docker run tradik/mddb` starts it. With `MDDB_AUTH_ENABLED=true`, add a
credential to every call (`-H "Authorization: Bearer $TOKEN"` or
`-H "X-API-Key: mddb_live_..."`, and `--token` or `--api-key` for `mddb-cli`).
Keep the `Content-Type: application/json` header the examples send: the
permission gate reads the collection from a JSON body only, so without the
header a request counts as touching every collection, and a user with rights
on one collection gets `403`. [Section 14](#14-several-teams-or-customers-on-one-server)
shows which permission each kind of call needs.

## 1. Documentation Platforms

Store and version API documentation, technical guides, and knowledge bases.

### Features Used
- Revision history for tracking documentation changes
- Metadata search for finding docs by category, version, status
- Multi-language support for internationalized documentation
- Template variables (`%%name%%`) for environment-specific content
- `mddb-cli autoindex` for a whole docs folder, PDF and Word files included

### Example

```bash
# Store API documentation with versioning
mddb-cli add api-docs authentication en_US -f auth.md \
  -m "version=2.0,status=published,category=security"

# Search published API docs
mddb-cli search api-docs -f "status=published" --sort updatedAt

# Get the document with %%apiUrl%% in its text replaced
curl -X POST http://localhost:11023/v1/get \
  -H "Content-Type: application/json" \
  -d '{"collection": "api-docs", "key": "authentication", "lang": "en_US",
       "env": {"apiUrl": "https://api.example.com"}}'
```

A variable is written `%%apiUrl%%` in the document. `{{apiUrl}}` is left as
it is.

When the documentation is a folder rather than single files, index the folder.
Markdown and text are stored as they are; PDF, DOCX, ODT, RTF, XLSX, ODS, PPTX,
ODP and EPUB, and HTML, TeX, CSV, TSV, EML and MBOX files, are converted to
Markdown on the server. A second run rewrites only the files that changed.

```bash
mddb-cli autoindex ./docs --collection api-docs --dry-run   # what would be indexed
mddb-cli autoindex ./docs --collection api-docs --prune     # index; drop deleted files
```

Each document's key is its path (`guides/auth.md`), and its metadata holds
`path`, `extension`, `kind`, `size` and `modified`. `autoindex` does not read
YAML frontmatter; for files whose frontmatter carries the metadata you search
by, use the [folder loader](#13-bulk-content-migration).

**Perfect for:** Technical documentation, API references, developer guides, internal wikis

---

## 2. Content Management Systems (CMS)

Manage blog posts, articles, and pages with multi-language support.

### Features Used
- Multi-language documents (same key, different languages)
- Rich metadata (author, tags, category, published status)
- Full-text search for content discovery
- Webhooks for cache invalidation on publish
- A result cap and trusted proxies for search served to the public

### Example

```bash
# Multi-language blog posts
mddb-cli add blog "getting-started" en_US -f post-en.md \
  -m "author=John,tags=tutorial|beginner,status=published"
mddb-cli add blog "getting-started" pl_PL -f post-pl.md \
  -m "author=John,tags=tutorial|beginner,status=published"

# Search published posts
mddb-cli search blog -f "status=published" -l 20

# Full-text search
mddb-cli fts blog --query="getting started markdown" --limit=10

# Register webhook for CDN invalidation
curl -X POST http://localhost:11023/v1/webhooks \
  -H "Content-Type: application/json" \
  -d '{"url": "https://cdn.example.com/invalidate", "events": ["doc.updated", "doc.added"], "collection": "blog"}'
```

### Search on the public site

When the site's search box calls MDDB directly, through nginx or Cloudflare,
three settings decide how it behaves under load:

```bash
# Behind Cloudflare: believe its client-address header, from its ranges only
export MDDB_TRUSTED_PROXIES=173.245.48.0/20,103.21.244.0/22   # ...the rest of Cloudflare's published ranges
export MDDB_CLIENT_IP_HEADER=CF-Connecting-IP                   # behind nginx: keep the default X-Forwarded-For

# No search returns more than 50 results, whatever the request asks for
export MDDB_SEARCH_MAX_LIMIT=50
```

Without `MDDB_TRUSTED_PROXIES`, every visitor arrives from the proxy's address
and shares one rate-limit bucket, so one busy client blocks the rest. With the
cap, a request for `limit: 10000` is answered with 50 results and
`"limitCapped": true`. The site should read with an API key of a user that
has read permission on `blog` only.
[Client IP and trusted proxies](config.md#client-ip-and-trusted-proxies) has
the header to use for each proxy.

**Perfect for:** Blogs, news sites, magazines, multi-language content platforms

---

## 3. Configuration Management

Store configuration templates with dynamic variable substitution.

### Features Used
- Template variables (`%%variable%%`) for environment-specific configs
- Revision history for config change tracking
- Metadata for environment (prod, staging, dev)
- Document TTL for temporary configurations

### Example

```bash
# Store nginx config template
cat > nginx.conf.md << 'EOF'
server {
    listen 80;
    server_name %%domain%%;
    root /var/www/%%app_name%%;

    location / {
        proxy_pass http://%%backend_host%%:%%backend_port%%;
    }
}
EOF

mddb-cli add configs nginx-prod en_US -f nginx.conf.md \
  -m "env=production,service=web,version=1.0"

# Retrieve with variables substituted
curl -X POST http://localhost:11023/v1/get \
  -H "Content-Type: application/json" \
  -d '{"collection": "configs", "key": "nginx-prod", "lang": "en_US",
       "env": {"domain": "example.com", "app_name": "myapp",
               "backend_host": "localhost", "backend_port": "3000"}}'

# The same from the CLI, content only
mddb-cli get configs nginx-prod en_US -c \
  -e "domain=example.com,app_name=myapp,backend_host=localhost,backend_port=3000"
```

**Perfect for:** Infrastructure configs, Kubernetes manifests, deployment templates, environment-specific settings

---

## 4. Knowledge Bases

Build searchable knowledge bases with rich categorization.

### Features Used
- Hybrid search: keywords and meaning in one query
- Metadata filtering (category, difficulty, tags)
- A synced source that keeps the collection in step with a help site

### Example

```bash
# Add troubleshooting guides
mddb-cli add kb vpn-setup en_US -f vpn-guide.md \
  -m "category=support,difficulty=advanced,tags=network|vpn|troubleshooting"

# Search by keywords and by meaning together
curl -X POST http://localhost:11023/v1/hybrid-search \
  -H "Content-Type: application/json" \
  -d '{"collection": "kb", "query": "I cant connect to the network from home",
       "topK": 5, "vectorAlgorithm": "auto", "explain": true}'

# Filter by difficulty
mddb-cli search kb -f "category=support,difficulty=beginner"
```

A "how do I..." question rarely shares its words with the article that answers
it, and a product name or error code is found by its exact words more reliably
than by meaning. Hybrid search runs both and merges them, so it needs no
similarity threshold tuned per embedding model. `"vectorAlgorithm": "auto"`
lets the server pick the vector index for the collection's size, and
`"explain": true` adds a `plan` with the index chosen and why.

If the articles are published on a help site, a synced source keeps the
collection in step with it, instead of importing pages one by one. This cron
rule reads the sitemap every six hours, fetches only the pages that changed
and, with `prune`, deletes the documents whose page is gone. Automation rules
need a global admin.

```bash
curl -X POST http://localhost:11023/v1/automation \
  -H "Content-Type: application/json" \
  -d '{
    "type": "cron",
    "name": "help centre",
    "schedule": "0 */6 * * *",
    "enabled": true,
    "sync": {
      "source": "sitemap",
      "url": "https://help.example.com/sitemap.xml",
      "collection": "kb",
      "lang": "en_US",
      "prune": true
    }
  }'
```

The sources (sitemap, URL list, S3 prefix), the keys the documents get and the
SSRF rules are in [Synced Sources](AUTOMATIONS.md#synced-sources).

**Perfect for:** Internal wikis, support documentation, FAQs, troubleshooting guides

---

## 5. Microservices Communication

Share templates, content, and configuration between services over gRPC.

### Features Used
- gRPC on port 11024, with Protobuf payloads
- Template variables for service-specific customization
- Multi-language for localized templates

### Example (Node.js)

```js
// npm install @tradik/mddb-client
const { MddbClient } = require('@tradik/mddb-client');

const client = new MddbClient('localhost:11024');

// The template, stored once: "Hello %%userName%%, sign in at %%loginUrl%%."
const doc = await client.Get({
  collection: 'templates',
  key: 'email-welcome',
  lang: 'en_US',
  env: { userName: 'John Doe', loginUrl: 'https://app.example.com/login' },
});

sendEmail(userEmail, 'Welcome!', doc.content_md);
client.close();
```

Every RPC in [`mddb.proto`](https://github.com/tradik/mddb/blob/main/proto/mddb.proto)
is a method of the same name. Python has the same client
(`pip install mddb-client`, `MddbClient("localhost:11024")`); Go services use
the HTTP client `github.com/tradik/mddb/clients/go/mddb/v2`. See
[GRPC.md](GRPC.md) for TLS and credentials.

**Perfect for:** Template storage, shared content, configuration distribution, service-to-service communication

---

## 6. RAG / AI Knowledge Base

Build retrieval-augmented generation (RAG) pipelines with semantic vector search.

### Features Used
- Automatic background embedding generation
- Hybrid retrieval returning passages within a token budget
- Query decomposition for questions that ask several things
- Summaries for questions about a whole document
- Corpus packs to ship the finished knowledge base to other servers
- MCP server for direct LLM integration

### Example

```bash
# Configure embedding provider
export MDDB_EMBEDDING_PROVIDER=openai
export MDDB_EMBEDDING_API_KEY=sk-...
export MDDB_EMBEDDING_MODEL=text-embedding-3-small
export MDDB_EMBEDDING_DIMENSIONS=1536

# Index the handbook: Markdown, PDF, DOCX, PPTX... (embedded in the background)
mddb-cli autoindex ./handbook --collection kb

# Retrieve passages for a prompt
curl -X POST http://localhost:11023/v1/hybrid-search \
  -H "Content-Type: application/json" \
  -d '{"collection": "kb", "query": "how do I cancel my subscription and get a refund?",
       "topK": 5, "format": "passages", "queryDecomposition": "auto"}'
```

`"format": "passages"` returns the matching lines of each document with their
line range and token count, not whole documents, so a long PDF does not fill
the prompt. `"queryDecomposition": "auto"` splits a question with several
needs ("cancel" and "refund") into parts, searches each and merges the results,
so one part cannot crowd out the other.

**RAG Pipeline (Python):**

```python
import requests
import openai

MDDB = "http://localhost:11023"
question = "How do I cancel my subscription and get a refund?"

# Step 1: Retrieve passages
resp = requests.post(f"{MDDB}/v1/hybrid-search", json={
    "collection": "kb",
    "query": question,
    "topK": 5,
    "format": "passages",
    "queryDecomposition": "auto",
}, timeout=30)
resp.raise_for_status()
passages = resp.json()["passages"]

# Step 2: Build context, naming where each passage came from
context = "\n\n".join(
    f"[{p['key']} lines {p['lines']['start']}-{p['lines']['end']}]\n{p['content']}"
    for p in passages
)

# Step 3: Send to LLM
response = openai.chat.completions.create(
    model="gpt-4o",
    messages=[
        {"role": "system", "content": f"Answer from these passages and cite them:\n\n{context}"},
        {"role": "user", "content": question}
    ]
)
print(response.choices[0].message.content)
```

Questions about a whole document ("what does this policy cover?") match a
summary better than any passage. When your pipeline stores a summary in each
document's `summary` metadata, switch the collection to use it, with a token
budget for what one search returns:

```bash
curl -X PUT http://localhost:11023/v1/collection-config \
  -H "Content-Type: application/json" \
  -d '{"collection": "kb", "retrieval": {"summaryMode": "expand", "contextTokenBudget": 4000}}'
```

Documents that link images can return them with the passages:
`"includeImages": true` attaches each hit's images with their alt text and
captions ([Images](SEARCH.md#images--alt-text-captions-and-pictures-v2189)).

**Shipping the knowledge base.** Embedding a large handbook takes time and
provider calls. Build it once and install it on other servers as a read-only
collection, with its full-text index and vectors:

```bash
mddb-cli corpus build kb --license CC-BY-4.0 --version 2026.10      # writes kb-2026.10.mddbpack
mddb-cli --server https://mddb.example.com --token "$ADMIN_TOKEN" \
  corpus install ./kb-2026.10.mddbpack
```

The target server needs the same embedding model for vector search; with
another one the pack is installed for full-text search only, and the install
says so. See [CORPUS-PACKS.md](CORPUS-PACKS.md).

**Perfect for:** RAG pipelines, AI assistants, semantic search, chatbot knowledge bases, Q&A systems

**See also:** [RAG Pipeline Guide](RAG-PIPELINE.md)

---

## 7. Event-Driven Architecture

Trigger downstream systems when documents change.

### Features Used
- Webhooks with retry logic
- Event types: `doc.added`, `doc.updated`, `doc.deleted`
- Collection filtering
- Webhook payload includes full document

### Example

```bash
# Register webhook for search index sync
curl -X POST http://localhost:11023/v1/webhooks \
  -H "Content-Type: application/json" \
  -d '{"url": "https://search.example.com/index", "events": ["doc.added", "doc.updated"], "collection": "blog"}'

# Register webhook for CDN invalidation
curl -X POST http://localhost:11023/v1/webhooks \
  -H "Content-Type: application/json" \
  -d '{"url": "https://cdn.example.com/purge", "events": ["doc.updated"], "collection": "pages"}'

# Register webhook for notifications
curl -X POST http://localhost:11023/v1/webhooks \
  -H "Content-Type: application/json" \
  -d '{"url": "https://notify.example.com/slack", "events": ["doc.deleted"], "collection": "legal-docs"}'
```

With authentication on, registering a webhook needs write permission on its
`collection`; one without a collection receives events from every collection
and needs write on `"*"`.

**Webhook Payload:**

```json
{
  "event": "doc.added",
  "collection": "blog",
  "key": "new-post",
  "lang": "en_US",
  "timestamp": 1709395200,
  "document": {
    "id": "blog|new-post|en_us",
    "key": "new-post",
    "lang": "en_US",
    "contentMd": "# New Post...",
    "meta": {"author": ["John"], "tags": ["news"]},
    "addedAt": 1709395200,
    "updatedAt": 1709395200
  }
}
```

**Perfect for:** Real-time notifications, search index sync, CDN invalidation, audit logging, Slack/Discord notifications

---

## 8. Temporary/Cached Content with TTL

Store time-limited content that auto-expires.

### Features Used
- Document TTL (time-to-live) in seconds
- Background cleanup worker
- Set/update/remove TTL on existing documents

### Example

```bash
# Add document with 1 hour expiry
curl -X POST http://localhost:11023/v1/add \
  -H "Content-Type: application/json" \
  -d '{"collection": "cache", "key": "session-abc123", "lang": "en",
       "ttl": 3600, "contentMd": "# Session Data\n\nUser: john@example.com"}'

# Update TTL to 2 hours
curl -X POST http://localhost:11023/v1/set-ttl \
  -H "Content-Type: application/json" \
  -d '{"collection": "cache", "key": "session-abc123", "lang": "en", "ttl": 7200}'

# Remove TTL (make permanent)
curl -X POST http://localhost:11023/v1/set-ttl \
  -H "Content-Type: application/json" \
  -d '{"collection": "cache", "key": "session-abc123", "lang": "en", "ttl": 0}'

# CLI
mddb-cli set-ttl cache session-abc123 en --ttl=3600
```

**Perfect for:** Cache layers, session data, temporary content, rate-limiting state, auto-cleanup

---

## 9. Version-Controlled Content

Track all changes with complete audit trail.

### Features Used
- Automatic revision history
- Full content snapshots per revision
- Metadata versioning
- Truncate old revisions to save space

### Example

```bash
# Initial version
mddb-cli add docs readme en_US -f README-v1.md -m "version=1.0,author=Alice"

# Update creates new revision
mddb-cli add docs readme en_US -f README-v2.md -m "version=2.0,author=Bob"

# Another update
mddb-cli add docs readme en_US -f README-v3.md -m "version=3.0,author=Alice"

# Keep only last 3 revisions
curl -X POST http://localhost:11023/v1/truncate \
  -H "Content-Type: application/json" \
  -d '{"collection": "docs", "keepRevs": 3, "dropCache": true}'
```

**Perfect for:** Legal documents, compliance, audit trails, contract versioning, policy management

---

## 10. GraphQL-First Applications

Modern web apps with flexible data requirements.

### Features Used
- GraphQL API with schema introspection
- GraphQL Playground for development
- Authentication directives (@auth, @hasRole)
- Flexible field selection
- Nested queries

### Example

GraphQL is on by default at `POST /graphql` (`MDDB_GRAPHQL_ENABLED=false`
turns it off); `mddb-cli playground` opens the Playground.

**Query:**
```graphql
query GetBlogPosts {
  search(input: {
    collection: "blog"
    filterMeta: [{key: "status", values: ["published"]}]
    sort: "addedAt"
    asc: false
    limit: 10
  }) {
    edges {
      node {
        key
        contentMd
        meta
        addedAt
      }
    }
    totalCount
  }
}
```

**Mutation:**
```graphql
mutation CreatePost {
  addDocument(input: {
    collection: "blog"
    key: "new-post"
    lang: "en"
    meta: [
      {key: "author", values: ["John"]}
      {key: "tags", values: ["tutorial", "graphql"]}
    ]
    contentMd: "# Hello GraphQL"
  }) {
    id
    addedAt
  }
}
```

**Vector Search:**
```graphql
query SemanticSearch {
  vectorSearch(input: {
    collection: "kb"
    query: "how to configure authentication?"
    topK: 5
    includeContent: true
  }) {
    results {
      document { key contentMd }
      score
      rank
    }
  }
}
```

**Perfect for:** Modern web apps, mobile apps, React/Vue/Angular frontends, Apollo Client integration

---

## 11. Multi-language E-commerce

Product catalogs with translations.

### Example

```bash
# English product
mddb-cli add products laptop-x1 en_US -f laptop-en.md \
  -m "category=electronics,price=999,sku=LAP-001,stock=50"

# Polish translation
mddb-cli add products laptop-x1 pl_PL -f laptop-pl.md \
  -m "category=electronics,price=999,sku=LAP-001,stock=50"

# German translation
mddb-cli add products laptop-x1 de_DE -f laptop-de.md \
  -m "category=electronics,price=999,sku=LAP-001,stock=50"

# Get user's language
mddb-cli get products laptop-x1 pl_PL
```

**Perfect for:** E-commerce, product catalogs, multi-region sites

---

## 12. Monitoring & Observability

Track MDDB metrics with Prometheus and Grafana.

### Example

```bash
# Enable metrics (enabled by default)
export MDDB_METRICS=true

# Scrape metrics
curl http://localhost:11023/metrics

# prometheus.yml
scrape_configs:
  - job_name: 'mddb'
    static_configs:
      - targets: ['localhost:11023']
```

With `MDDB_AUTH_ENABLED=true`, `/metrics` needs a credential like any other
endpoint; give Prometheus an API key (`authorization` in the scrape config) or
set `MDDB_METRICS_PUBLIC=true` on a network only Prometheus reaches.

**Available Metrics:**
- `mddb_http_requests_total` - request counters
- `mddb_http_request_duration_seconds` - latency histograms
- `mddb_documents_total` - document counts per collection
- `mddb_database_size_bytes` - DB file size
- `mddb_vector_embeddings_total` - embedding count
- `go_goroutines`, `go_memstats_*` - Go runtime

**Perfect for:** DevOps, SRE, production monitoring

**See also:** [Telemetry Guide](TELEMETRY.md)

---

## 13. Bulk Content Migration

Import large amounts of content from existing systems.

### Example

```bash
# A folder of mixed files: Markdown as is; PDF, DOCX, XLSX, PPTX, EPUB, EML, HTML converted
mddb-cli autoindex ./content --collection blog

# Markdown whose YAML frontmatter should become metadata
./scripts/load-md-folder.sh ./content blog -r -v

# With custom metadata
./scripts/load-md-folder.sh ./docs kb -m "status=published" -m "author=Migration Script"

# Dry run first
./scripts/load-md-folder.sh ./content blog -d
```

`autoindex` keys each document by its path and records `path`, `extension`,
`kind`, `size` and `modified`; it is the choice for files from many tools.
`load-md-folder.sh` keys each document by its file name and turns the
frontmatter (`title`, `author`, `category`...) into metadata; it is the choice
for exports from static-site generators and WordPress.

**Coming from Elasticsearch.** With `MDDB_ES_COMPAT=true`, MDDB answers a
subset of the Elasticsearch API under `/es`, so an indexing script or a client
SDK keeps working with a new address:

```bash
curl -X POST http://localhost:11023/es/_bulk \
  -H "Content-Type: application/x-ndjson" --data-binary @articles.ndjson

curl -X POST http://localhost:11023/es/articles/_search \
  -H "Content-Type: application/json" \
  -d '{"query": {"bool": {"must": {"match": {"content": "fox"}},
                          "filter": {"term": {"genre": "fable"}}}}}'
```

An index is a collection, `_source.content` becomes the document's Markdown and
the other fields its metadata. A query clause MDDB cannot translate is refused
with a `400` that names it, rather than ignored.
[ELASTICSEARCH-COMPAT.md](ELASTICSEARCH-COMPAT.md) lists what is supported.

**Perfect for:** Content migrations, initial data load, batch processing

**See also:** [Bulk Import Guide](BULK-IMPORT.md)

---

## 14. Several Teams or Customers on One Server

Give each team or customer its own collections on one server, so that one
cannot read another's documents or fill the disk for everyone.

### Features Used
- Users and API keys with permissions per collection
- Quotas per collection
- Read-only collections from corpus packs, shared by all

### Example

```bash
# As a global admin: a user with read and write on acme-kb only
curl -X POST http://localhost:11023/v1/auth/register \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"username": "acme", "password": "a-long-random-password"}'
curl -X POST http://localhost:11023/v1/auth/permissions \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"username": "acme", "collection": "acme-kb", "read": true, "write": true, "admin": false}'

# Cap the collection: documents, stored bytes, embedding calls a day
curl -X PUT http://localhost:11023/v1/collection-config \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"collection": "acme-kb", "quota": {"maxDocuments": 10000, "maxBytes": 52428800, "embeddingCallsPerDay": 2000}}'

# Share the product handbook with every customer, read-only
mddb-cli --token "$ADMIN_TOKEN" corpus install ./handbook-2026.10.mddbpack
curl -X POST http://localhost:11023/v1/auth/permissions \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"username": "acme", "collection": "handbook", "read": true, "write": false, "admin": false}'

# As acme: log in (prints the token, ACME_TOKEN below), create an API key
# for its ingest job (ACME_KEY), then search its own collection
mddb-cli login acme a-long-random-password
curl -X POST http://localhost:11023/v1/auth/api-key \
  -H "Authorization: Bearer $ACME_TOKEN" -H "Content-Type: application/json" \
  -d '{"description": "acme ingest", "expiresAt": 0}'
curl -X POST http://localhost:11023/v1/hybrid-search \
  -H "X-API-Key: $ACME_KEY" -H "Content-Type: application/json" \
  -d '{"collection": "acme-kb", "query": "refund policy", "topK": 5}'
```

What the `acme` user gets:

| Request | Answer |
|---|---|
| Read, write, search, webhooks on `acme-kb` | allowed |
| Search `handbook` | allowed; a write is refused with `READ_ONLY` |
| Any request naming another collection | `403` |
| A write past `maxDocuments` or `maxBytes` | `507` with `QUOTA_EXCEEDED`; embedding past the daily cap waits for the next day |
| Changing its own quota, automation and sync rules, backups, users | `403`: these need a global admin |

Admin on one collection manages that collection only; only admin on `"*"` is a
global admin. Every REST route, gRPC method, MCP tool and GraphQL field checks
these permissions before it runs.
[AUTHENTICATION.md](AUTHENTICATION.md#where-permissions-are-enforced-21810-sec-019)
has the rule for each, and [MULTI_TENANCY.md](MULTI_TENANCY.md) the tenant
namespaces and quota details.

**Perfect for:** SaaS products with a knowledge base per customer, agencies hosting several sites, departments sharing one server

---

**[← Back to README](../README.md)** | **[See all features →](FEATURES.md)**
