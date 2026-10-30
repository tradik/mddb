---
title: "MDDB GraphQL API Documentation"
slug: "docs/graphql"
description: "MDDB's GraphQL API: schema, queries and mutations, introspection and the playground - every operation shares the code path REST and gRPC use."
status: publish
---

# MDDB GraphQL API Documentation

## Overview

MDDB provides a fully-functional GraphQL API alongside its REST and gRPC interfaces. The GraphQL endpoint offers flexible querying, type safety, and schema introspection — every operation declared in [services/mddbd/graphql/schema.graphql](https://github.com/tradik/mddb/blob/main/services/mddbd/graphql/schema.graphql) is wired to the in-process MCP DirectClient (the same code path REST/gRPC use), so behaviour is identical across protocols.

**Endpoint:** `POST /graphql`
**Playground:** `GET /playground` (development tool, served alongside the endpoint)

> **Status (MDDB 2.9.11+)**: GraphQL is now **enabled by default**. Prior to 2.9.11, the resolvers were scaffolding stubs that panicked with `not implemented` for every query and mutation except `login`. As of 2.9.11 every query and mutation in `schema.graphql` has a real implementation that delegates through the same adapter as REST and gRPC. To opt out, set `MDDB_GRAPHQL_ENABLED=false`.

## Quick Start

### Enable / disable GraphQL

```bash
# Default — GraphQL on
./mddbd

# Explicit on
MDDB_GRAPHQL_ENABLED=true ./mddbd

# Disable (saves a few MB of resident memory + a route)
MDDB_GRAPHQL_ENABLED=false ./mddbd

# Docker
docker run tradik/mddb                                      # GraphQL on
docker run -e MDDB_GRAPHQL_ENABLED=false tradik/mddb        # GraphQL off
```

### Access GraphQL Playground

Visit `http://localhost:11023/playground` to explore the API interactively. Introspection (`{__schema{queryType{fields{name}}}}`) is enabled.

### Smoke test

```bash
# List supported queries
curl -X POST http://localhost:11023/graphql \
  -H "Content-Type: application/json" \
  -d '{"query":"{__schema{queryType{fields{name}}}}"}'

# Real query (search documents in collection 'blog')
curl -X POST http://localhost:11023/graphql \
  -H "Content-Type: application/json" \
  -d '{"query":"{search(input:{collection:\"blog\",limit:5}){totalCount edges{node{key lang}}}}"}'
```

## Authentication

GraphQL uses the same JWT/API key authentication as the REST API.

### Login Mutation

```graphql
mutation {
  login(username: "admin", password: "your-password") {
    token
    expiresAt
  }
}
```

### Using the Token

Include the token in subsequent requests:

**HTTP Header:**
```
Authorization: Bearer YOUR_JWT_TOKEN
```

**Example with curl:**
```bash
curl -X POST http://localhost:11023/graphql \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"query": "{ me { username admin } }"}'
```

### API Key Authentication

Alternatively, use an API key:

```
X-API-Key: mddb_live_YOUR_API_KEY
```

## Schema Overview

### Core Types

#### Document
```graphql
type Document {
  id: ID!
  key: String!
  lang: String!
  meta: JSONObject!
  contentMd: String!
  addedAt: Time!
  updatedAt: Time!
  expiresAt: Time
}
```

#### VectorSearchResult
```graphql
type VectorSearchResult {
  document: Document!
  score: Float!
  rank: Int!
}
```

#### User
```graphql
type User {
  username: String!
  admin: Boolean!
  createdAt: Time!
}
```

### Authentication Directives

- `@auth` - Requires authentication (JWT or API key)
- `@hasRole(role: Role!)` - Requires specific role (ADMIN or USER)

## Queries

### Get Current User

```graphql
query {
  me {
    username
    admin
    createdAt
  }
}
```

### Get Single Document

```graphql
query {
  document(
    collection: "blog"
    key: "hello-world"
    lang: "en"
  ) {
    id
    key
    lang
    contentMd
    meta
    addedAt
    updatedAt
  }
}
```

**With Template Variables:**
```graphql
query {
  document(
    collection: "templates"
    key: "email"
    lang: "en"
    env: { name: "John", year: "2024" }
  ) {
    contentMd
  }
}
```

### Indexing Status

`indexingStatus(collection: String)` (v2.18.8) reports queued embeddings and
metadata updates — whether what was just written is searchable yet. Omit
`collection` for the whole server. Fields match
[`GET /v1/indexing-status`](API.md#get-v1indexing-status); `oldestPendingAt`
is a unix timestamp. Since v2.18.10, `trainingCollections` lists the
collections an ivf, pq, opq, sq or sq4 index has not trained yet (GO-047):
flat and hnsw already answer for them, and `algorithm: "auto"` skips the
index still training.

```graphql
query { indexingStatus(collection: "notes") { pendingEmbeddings progress indexed } }
```

### Code Symbol

`codeSymbol(input: CodeSymbolInput!)` (v2.18.8) returns where a symbol is
defined and used, each relation a list of `Passage` (path, `lines { start end }`,
the enclosing `symbol`, optional `content`). See
[API.md — One symbol, everywhere it matters](API.md#one-symbol-everywhere-it-matters-v2188).

```graphql
query {
  codeSymbol(input: { collection: "repo", name: "SearchHybrid", include: ["definitions", "tests"] }) {
    definitions { path lines { start end } kind }
    tests { path lines { start } symbol content }
  }
}
```

### Passages

`fts` and `vectorSearch` take `format: "passages"` and `passageLines`
(v2.18.8); the response then fills `passages` (path, `lines`, `symbol`,
`content`) and leaves `results` empty.

### Search Documents

```graphql
query {
  search(input: {
    collection: "blog"
    filterMeta: [
      { key: "status", values: ["published"] }
      { key: "author", values: ["John", "Jane"] }
    ]
    sort: "updatedAt"
    asc: false
    limit: 10
    offset: 0
  }) {
    edges {
      cursor
      node {
        key
        lang
        contentMd
        meta
      }
    }
    pageInfo {
      hasNextPage
      hasPreviousPage
      totalCount
    }
  }
}
```

`filter` (v2.18.8+) takes the operator filter from
[Filter Operators](SEARCH.md#filter-operators-v2188) as a JSON object, on
`search` and `vectorSearch`, ANDed with `filterMeta`:

```graphql
query {
  search(input: {
    collection: "repo"
    filter: { path: { prefix: "internal/" }, kind: { not: ["test"] } }
  }) { edges { node { key } } }
}
```

`vectorSearch` also takes `groupBy: { key: "path", perGroup: 1 }`
(v2.18.8+, [Grouping Results](SEARCH.md#grouping-results-v2188)); each result
then reports `group` and `groupRank`.

### Vector Search (Semantic Search)

```graphql
query {
  vectorSearch(input: {
    collection: "kb"
    query: "how to configure authentication?"
    topK: 5
    threshold: 0.7
    includeContent: true
  }) {
    results {
      document {
        key
        lang
        contentMd
      }
      score
      rank
    }
    total
    model
    dimensions
  }
}
```

**With Pre-computed Vector:**
```graphql
query {
  vectorSearch(input: {
    collection: "kb"
    queryVector: [0.1, 0.2, 0.3, ...]  # 1536-dim for OpenAI
    topK: 10
  }) {
    results {
      document { key contentMd }
      score
    }
  }
}
```

**Query planner and relevance feedback (v2.18.9+):** `vectorSearch` takes
the same fields as `POST /v1/vector-search`, and runs the same code.
`algorithm: "auto"` lets the planner pick the index
([auto](SEARCH.md#auto-the-server-chooses-v2189)); `explain: true` returns
`plan`, which is `null` otherwise. `positive` and `negative` take examples
from earlier results, each `{key}` (a document key or ID) or `{vector}`, not
both ([Relevance Feedback](SEARCH.md#relevance-feedback--positive-and-negative-examples-v2189)):

```graphql
query {
  vectorSearch(input: {
    collection: "kb"
    query: "rotate the signing key"
    topK: 10
    algorithm: "auto"
    latencyTargetMs: 20
    explain: true
    positive: [{ key: "key-rotation" }]
    negative: [{ key: "tls-certificates" }]
    feedbackMode: "query"            # or "rescore", with feedbackWeight
    rocchio: { beta: 1.0 }
  }) {
    results { document { key } score }
    plan {
      algorithm prefilter candidates iterativeScan reasons
      feedback { mode positive negative alpha beta gamma queryShift weight rescored }
    }
  }
}
```

`queryDecomposition: "auto"` (v2.18.10+) also searches a query of several
clauses clause by clause, and `plan { decomposition { parts texts } }` shows
the split ([SEARCH.md](SEARCH.md#query-decomposition--questions-with-several-needs-v21810)).

A refused input (an unknown `feedbackMode` or `queryDecomposition`, an example with both or neither
of `key` and `vector`, a key with no embedding, `latencyTargetMs` out of
range) is a GraphQL error carrying the message REST returns with its 422.
In `plan.feedback`, the fields the mode does not use are `0`. GraphQL has no
hybrid search; use REST, MCP or gRPC for `vectorAlgorithm` there.

### Full-Text Search

```graphql
query {
  fts(input: {
    collection: "docs"
    query: "authentication jwt token"
    limit: 20
  }) {
    results {
      document {
        key
        contentMd
      }
      score
      matchedTerms
    }
    total
  }
}
```

### Images (v2.18.9+)

`vectorSearch` and `fts` take `includeImages: true`: each result (and each
passage) carries `images { key altText caption page url }`. With a provider
that embeds images, `vectorSearch` also returns `imageHits` (pictures that
match the query by what they show) and takes `queryImage` (base64, a data URI
or an http(s) URL) to search by a picture. `indexingStatus` reports
`images { documents embedded model note }`, and `ingestDocuments` options take
`extractImages` and `fetchRemoteImages`. See
[SEARCH.md](SEARCH.md#images--alt-text-captions-and-pictures-v2189).

```graphql
query {
  vectorSearch(input: {collection: "docs", query: "pump wiring", includeImages: true}) {
    results { document { key } score images { key altText caption page url } }
    imageHits { key parent score url }
  }
}
```

### Database Statistics

```graphql
query {
  stats {
    databasePath
    databaseSize
    mode
    totalDocuments
    totalRevisions
    collections {
      name
      documentCount
      revisionCount
    }
  }
}
```

### Vector Statistics

```graphql
query {
  vectorStats {
    enabled
    provider
    model
    dimensions
    indexReady
    collections
  }
}
```

### List Webhooks (Admin Only)

Optionally filter by collection:

```graphql
query {
  webhooks(collection: "blog") {
    id
    url
    events
    collection
    createdAt
  }
}
```

### List Schemas

```graphql
query {
  schemas {
    collection
    schema
    enabled
  }
}
```

### List Users (Admin Only)

```graphql
query {
  users {
    username
    admin
    createdAt
  }
}
```

### List Groups (Admin Only)

```graphql
query {
  groups {
    name
    description
    members
    createdAt
  }
}
```

### User Permissions (Admin Only)

```graphql
query {
  userPermissions(username: "john") {
    username
    collection
    read
    write
    admin
  }
}
```

### Group Permissions (Admin Only)

```graphql
query {
  groupPermissions(groupName: "editors") {
    groupName
    collection
    read
    write
    admin
  }
}
```

## Mutations

### Login

```graphql
mutation {
  login(username: "admin", password: "secret") {
    token
    expiresAt
  }
}
```

### Add Document

```graphql
mutation {
  addDocument(input: {
    collection: "blog"
    key: "my-post"
    lang: "en"
    meta: [
      { key: "author", values: ["John"] }
      { key: "tags", values: ["tutorial", "graphql"] }
      { key: "status", values: ["published"] }
    ]
    contentMd: """
    # My Blog Post

    This is the content of my post.
    """
    ttl: 3600  # optional: expires in 1 hour
  }) {
    id
    key
    addedAt
  }
}
```

### Update Document

```graphql
mutation {
  updateDocument(input: {
    collection: "blog"
    key: "my-post"
    lang: "en"
    meta: [
      { key: "status", values: ["draft"] }
    ]
    contentMd: "# Updated Content"
  }) {
    id
    updatedAt
  }
}
```

### Delete Document

```graphql
mutation {
  deleteDocument(
    collection: "blog"
    key: "my-post"
    lang: "en"
  )
}
```

### Delete Collection

```graphql
mutation {
  deleteCollection(collection: "old-blog")
}
```

### Reindex Vectors

```graphql
mutation {
  vectorReindex(
    collection: "kb"
    force: false
  ) {
    enabled
    collections
  }
}
```

### Add Batch

Add multiple documents to a collection in one request:

```graphql
mutation {
  addBatch(
    collection: "blog"
    documents: [
      {
        key: "post-1"
        lang: "en"
        contentMd: "# First Post"
        meta: [{ key: "status", values: ["published"] }]
        saveRevision: true
      }
      {
        key: "post-2"
        lang: "en"
        contentMd: "# Second Post"
      }
    ]
  ) {
    added
    updated
    failed
    errors
  }
}
```

### Ingest Documents

Bulk ingest with per-batch options (duplicate skipping, embedding/FTS/webhook control):

```graphql
mutation {
  ingestDocuments(
    collection: "scraped"
    documents: [
      {
        url: "https://example.com/article"
        key: "article-1"
        lang: "en"
        contentMd: "# Scraped Article"
        meta: [{ key: "source", values: ["crawler"] }]
        extractFrontmatter: true
        scraper: "my-crawler"
        ttl: 86400
      }
    ]
    options: {
      skipDuplicates: true
      skipEmbeddings: false
      skipWebhooks: true
      autoConfigureCollection: true
      saveRevision: false
    }
  ) {
    added
    updated
    skipped
    failed
    errors
    collection
    durationMs
  }
}
```

### Set TTL

```graphql
mutation {
  setTTL(
    collection: "cache"
    key: "session-data"
    lang: "en"
    ttl: 3600
  ) {
    id
    expiresAt
  }
}
```

### Import from URL

```graphql
mutation {
  importURL(
    collection: "articles"
    url: "https://example.com/page.md"
    key: "imported-page"
    lang: "en"
    meta: [{ key: "source", values: ["import"] }]
    ttl: 86400
  ) {
    id
    key
    addedAt
  }
}
```

### Register Webhook (Admin Only)

```graphql
mutation {
  registerWebhook(input: {
    url: "https://example.com/hook"
    events: ["insert", "update", "delete"]
    collection: "blog"
  }) {
    id
    url
    events
    collection
    createdAt
  }
}
```

### Delete Webhook (Admin Only)

```graphql
mutation {
  deleteWebhook(id: "webhook-id")
}
```

### Set Schema (Admin Only)

The `schema` argument is a JSON Schema document as a string:

```graphql
mutation {
  setSchema(input: {
    collection: "blog"
    schema: "{\"required\":[\"author\"],\"properties\":{\"author\":{\"type\":\"string\"}}}"
  })
}
```

### Delete Schema (Admin Only)

```graphql
mutation {
  deleteSchema(collection: "blog")
}
```

### Validate Document

Validate metadata against a collection's schema without writing:

```graphql
mutation {
  validateDocument(
    collection: "blog"
    meta: [
      { key: "author", values: ["John"] }
      { key: "status", values: ["published"] }
    ]
  ) {
    valid
    errors
  }
}
```

## Admin Operations

### Register User (Admin Only)

```graphql
mutation {
  register(
    username: "newuser"
    password: "secure-password"
  ) {
    username
    admin
    createdAt
  }
}
```

### Create API Key

```graphql
mutation {
  createAPIKey(input: {
    description: "My API key"
    expiresAt: 1735689600  # Unix timestamp, optional
  }) {
    key
    description
    expiresAt
    createdAt
  }
}
```

### Create Group

```graphql
mutation {
  createGroup(input: {
    name: "editors"
    description: "Content editors"
    members: ["user1", "user2"]
  }) {
    name
    description
    members
  }
}
```

### Update Group

Replaces the group's description and member list:

```graphql
mutation {
  updateGroup(
    name: "editors"
    description: "Content editors and reviewers"
    members: ["user1", "user2", "user3"]
  ) {
    name
    description
    members
  }
}
```

### Delete Group

```graphql
mutation {
  deleteGroup(name: "editors")
}
```

### Set Permission

```graphql
mutation {
  setPermission(input: {
    username: "john"
    collection: "blog"
    read: true
    write: true
    admin: false
  })
}
```

### Set Group Permission

```graphql
mutation {
  setGroupPermission(input: {
    groupName: "editors"
    collection: "blog"
    read: true
    write: true
    admin: false
  })
}
```

## Advanced Usage

### Combining Multiple Queries

GraphQL allows fetching multiple resources in one request:

```graphql
query {
  currentUser: me {
    username
    admin
  }

  recentPosts: search(input: {
    collection: "blog"
    sort: "updatedAt"
    limit: 5
  }) {
    edges {
      node {
        key
        contentMd
      }
    }
  }

  dbStats: stats {
    totalDocuments
    databaseSize
  }
}
```

### Field Selection

Request only the fields you need:

```graphql
query {
  document(collection: "blog", key: "post", lang: "en") {
    key        # Only these 2 fields returned
    contentMd
  }
}
```

### Variables

Use GraphQL variables for dynamic queries:

```graphql
query GetDocument($coll: String!, $key: String!, $lang: String!) {
  document(collection: $coll, key: $key, lang: $lang) {
    id
    contentMd
  }
}
```

**Variables:**
```json
{
  "coll": "blog",
  "key": "my-post",
  "lang": "en"
}
```

## Error Handling

GraphQL returns structured errors:

```json
{
  "errors": [
    {
      "message": "authentication failed: invalid credentials",
      "path": ["login"]
    }
  ],
  "data": null
}
```

Common error messages:
- `"unauthenticated: missing or invalid credentials"` — no JWT/API key in the request and auth is enabled
- `"forbidden: admin privileges required"` — admin-only operation called without admin claims
- `"permission denied"` — caller authenticated but lacks read/write/admin on the target collection
- `"authentication failed"` / `"invalid credentials"` — `login` mutation rejected the username/password

## Performance Considerations

### Query Complexity

MDDB limits query complexity to prevent resource exhaustion:
- Max complexity: 1000 points
- Max depth: 10 levels

### Pagination

Use cursor-based pagination for large result sets:

```graphql
query {
  search(input: { collection: "blog", limit: 50 }) {
    edges {
      cursor
      node { key }
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}
```

### Caching

Parsed GraphQL queries are cached in memory (LRU cache).

## GraphQL vs REST vs gRPC

| Feature | GraphQL | REST | gRPC |
|---------|---------|------|------|
| Field selection | ✅ Flexible | ❌ Fixed | ❌ Fixed |
| Multiple resources | ✅ Single request | ❌ Multiple requests | ✅ Streaming |
| Type safety | ✅ Schema | ❌ None | ✅ Protobuf |
| Introspection | ✅ Built-in | ❌ None | ✅ Reflection |
| Binary protocol | ❌ JSON | ❌ JSON | ✅ Protobuf |
| Streaming | ⚠️ Limited | ❌ No | ✅ Yes |
| Simplicity | ⚠️ Learning curve | ✅ Simple | ⚠️ Setup |

**Use GraphQL when:**
- You need flexible field selection
- You want to combine multiple queries
- You're building a modern frontend (React, Vue, Angular)
- You prefer strong typing and schema validation

**Use REST when:**
- You need simple curl/wget access
- You're building quick scripts or automations
- You need streaming responses (export, backup)

**Use gRPC when:**
- You need maximum performance
- You're building service-to-service communication
- You need bidirectional streaming

## Configuration

### Environment Variables

- `MDDB_GRAPHQL_ENABLED` - Enable/disable GraphQL (default: `false`)
- `MDDB_GRAPHQL_PLAYGROUND` - Enable/disable Playground (default: `true`)
- `MDDB_AUTH_ENABLED` - Enable authentication (default: `false`)
- `MDDB_AUTH_JWT_SECRET` - JWT secret (required if auth enabled)

### CLI Flags

```bash
./mddbd --graphql                    # Enable GraphQL
./mddbd --graphql --playground=false # Disable Playground
```

## Client Libraries

### JavaScript/TypeScript

```bash
npm install @apollo/client graphql
```

```typescript
import { ApolloClient, InMemoryCache, gql } from '@apollo/client';

const client = new ApolloClient({
  uri: 'http://localhost:11023/graphql',
  cache: new InMemoryCache(),
  headers: {
    authorization: `Bearer ${token}`
  }
});

const { data } = await client.query({
  query: gql`
    query {
      stats {
        totalDocuments
      }
    }
  `
});
```

### Python

```bash
pip install gql
```

```python
from gql import gql, Client
from gql.transport.requests import RequestsHTTPTransport

transport = RequestsHTTPTransport(
    url='http://localhost:11023/graphql',
    headers={'Authorization': f'Bearer {token}'}
)

client = Client(transport=transport, fetch_schema_from_transport=True)

query = gql('''
    query {
        stats {
            totalDocuments
        }
    }
''')

result = client.execute(query)
```

## Troubleshooting

### GraphQL endpoint not responding

Check if GraphQL is enabled:
```bash
curl http://localhost:11023/v1/config | jq .graphql_enabled
```

### Authentication errors

Verify your token:
```bash
curl -X POST http://localhost:11023/graphql \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"query": "{ me { username } }"}'
```

### Schema introspection

Query the schema directly:
```graphql
query {
  __schema {
    types {
      name
    }
  }
}
```

## Future Enhancements

The following features are planned for future releases:

- Subscriptions for real-time updates
- Advanced filtering with logical operators
- Rate limiting per user/IP
- DataLoader for N+1 query prevention

## Contributing

GraphQL resolver implementations are in progress. Contributions welcome:
- `services/mddbd/graphql/schema.resolvers.go` - Resolver implementations
- `services/mddbd/graphql_adapter.go` - Server adapter

## Support

- GitHub Issues: https://github.com/tradik/mddb/issues
- Documentation: https://mddb.tradik.com/docs/readme/
- REST API Docs: `/v1/endpoints`

---

**Version:** 2.7.1
**Last Updated:** March 2026
