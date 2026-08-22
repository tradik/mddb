---
title: "MCP Server Configuration"
slug: "docs/mcp"
description: "MDDB's built-in MCP server implementing the 2025-11-25 spec: the full built-in tool catalogue, stdio and HTTP transports, API keys and access modes."
status: publish
---

# MCP Server Configuration

MDDB has a built-in MCP (Model Context Protocol) server implementing the **2025-11-25** specification. This document covers all MCP configuration options.

**[→ LLM client setup (Claude, Cursor, ChatGPT, Ollama)](LLM_CONNECTIONS.md)** | **[→ Custom YAML tools](CUSTOM-TOOLS.md)**

## Transports

| Transport | Endpoint | Spec Version | Status |
|-----------|----------|-------------|--------|
| **Stdio** | stdin/stdout | 2025-11-25 | Default for Claude Desktop |
| **Streamable HTTP** | `POST/GET/DELETE /mcp` | 2025-11-25 | Recommended for remote |
| **SSE (legacy)** | `GET /sse` + `POST /message` | 2024-11-05 | Backward compatible |

All transports run on the MCP port (default: 9000, configurable via `MDDB_MCP_ADDR`).

### Streamable HTTP (Recommended)

```bash
# Initialize
curl -X POST http://localhost:9000/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'

# Session ID returned in MCP-Session-Id header — use it for all subsequent requests
```

### Client Configuration

```json
{
  "mcpServers": {
    "mddb": {
      "transport": "streamable-http",
      "url": "http://localhost:9000/mcp"
    }
  }
}
```

## Environment Variables

### Core

| Env Var | Default | Description |
|---------|---------|-------------|
| `MDDB_MCP_ENABLED` | `true` | Enable MCP HTTP server |
| `MDDB_MCP_ADDR` | `:9000` | MCP HTTP listen address |
| `MDDB_MCP_PORT` | `9000` | MCP HTTP port (alternative to ADDR) |
| `MDDB_MCP_STDIO` | `false` | Run in MCP stdio mode (replaces HTTP/gRPC) |
| `MDDB_MCP_DOMAIN` | — | Server hostname for Panel config generation |
| `MDDB_MCP_MODE` | — | Access mode override: `read`, `write`, or `wr` (inherits `MDDB_MODE`) |

### Server Info

| Env Var | Default | Description |
|---------|---------|-------------|
| `MDDB_MCP_SERVER_NAME` | `mddbd` | Server name in MCP initialize response |
| `MDDB_MCP_SERVER_DESCRIPTION` | — | Server description |
| `MDDB_MCP_SERVER_VENDOR` | — | Vendor name |
| `MDDB_MCP_SERVER_HOMEPAGE` | — | Homepage URL |
| `MDDB_MCP_INSTRUCTIONS` | — | System prompt for LLM — guidance on how to use this server |

### Tools

| Env Var | Default | Description |
|---------|---------|-------------|
| `MDDB_MCP_CONFIG` | — | Path to YAML file with custom tool definitions |
| `MDDB_MCP_BUILTIN_TOOLS` | `true` | Set to `false` to hide all 79 built-in tools (only custom tools exposed) |

### API Key Authentication

| Env Var | Default | Description |
|---------|---------|-------------|
| `MDDB_MCP_API_KEY_ENABLED` | `false` | Require API key for MCP HTTP access |
| `MDDB_MCP_API_KEYS` | — | Static keys: `key1:name1,key2:name2` (defined at startup) |
| `MDDB_MCP_API_KEY_CACHE_TTL` | `60s` | Cache TTL for dynamic key lookups |

> **⚠️ MCP exposure vs. main auth (SEC-002).** The MCP listener (default `:9000`)
> is a separate port that grants full read/write access to the database. Its own
> `MCPAPIKeyMiddleware` is inactive unless `MDDB_MCP_API_KEY_ENABLED=true`. To
> prevent MCP from becoming an unauthenticated bypass of the main API, the server
> reconciles MCP exposure with `MDDB_AUTH_ENABLED` at startup:

| `MDDB_AUTH_ENABLED` | `MDDB_MCP_API_KEY_ENABLED` | MCP listener behaviour |
|---|---|---|
| `true`  | `false` | **Gated by the main AuthManager** — MCP requires the same `Authorization: Bearer` / `X-API-Key` credentials as the HTTP API. Anonymous `tools/call` → `401`. |
| `true`  | `true`  | Gated by MCP API keys (as configured below). |
| `false` | `true`  | Gated by MCP API keys. |
| `false` | `false` | **No authentication.** If MCP is bound beyond loopback (e.g. the default `:9000`), the server logs a prominent startup warning. Use only on a trusted/loopback bind, or set one of the flags above. |

Two sources of keys:
- **Static** — `MDDB_MCP_API_KEYS` env var (defined at startup, no restart needed to validate)
- **Dynamic** — REST API (`/v1/mcp/keys`) stored in BoltDB, persists across restarts, managed without downtime

Clients authenticate via:
- `X-API-Key: <key>` header
- `Authorization: Bearer <key>` header
- `?api_key=<key>` query parameter (for SSE connections)

```bash
# Static keys only
docker run -d \
  -e MDDB_MCP_API_KEY_ENABLED=true \
  -e "MDDB_MCP_API_KEYS=sk-prod:claude-prod,sk-dev:cursor-dev" \
  -p 9000:9000 \
  tradik/mddb:latest
```

#### Dynamic Key Management API

Keys stored in internal BoltDB bucket (`_mcp_api_keys`). Requires admin auth when `MDDB_AUTH_ENABLED=true`.

```bash
# Create a key (full key shown only once in response)
curl -X POST http://localhost:11023/v1/mcp/keys \
  -H "Authorization: Bearer <admin-token>" \
  -d '{"name": "claude-prod", "expiresAt": 0}'
# Response: {"key":"mcp_a1b2c3d4e5f6...","name":"claude-prod","createdAt":1711...}

# List keys (full keys NOT shown, only prefixes)
curl http://localhost:11023/v1/mcp/keys \
  -H "Authorization: Bearer <admin-token>"

# Delete a key
curl -X DELETE http://localhost:11023/v1/mcp/keys \
  -H "Authorization: Bearer <admin-token>" \
  -d '{"key": "mcp_a1b2c3d4e5f6..."}'

# Disable a key (revoke without deleting)
curl -X POST http://localhost:11023/v1/mcp/keys/disable \
  -H "Authorization: Bearer <admin-token>" \
  -d '{"key": "mcp_a1b2c3d4e5f6..."}'
```

Key features:
- **TTL support** — `expiresAt` Unix timestamp (0 = never expires)
- **Disable without delete** — revoke instantly, keep for audit
- **Cache** — lookups cached for `MDDB_MCP_API_KEY_CACHE_TTL` (default 60s), auto-invalidated on create/delete/disable
- **Panel UI** — manage keys from the web panel (LLM Connections → API Keys tab)

### Rate Limiting

| Env Var | Default | Description |
|---------|---------|-------------|
| `MDDB_MCP_RATE_LIMIT_ENABLED` | `false` | Enable per-client rate limiting |
| `MDDB_MCP_RATE_LIMIT_REQUESTS` | `100` | Max requests per window |
| `MDDB_MCP_RATE_LIMIT_WINDOW` | `60` | Window duration in seconds |
| `MDDB_MCP_RATE_LIMIT_BURST` | `20` | Burst allowance above limit |
| `MDDB_MCP_RATE_LIMIT_BY` | `ip` | Rate limit key: `ip`, `api_key`, or `session` |

Response headers: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`.
Returns `429 Too Many Requests` with `Retry-After` header when exceeded.

### Request Logging

| Env Var | Default | Description |
|---------|---------|-------------|
| `MDDB_MCP_LOGGING_ENABLED` | `false` | Enable structured JSON audit logs |
| `MDDB_MCP_LOGGING_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |

Logs are written to stderr in JSON format:

```json
{"timestamp":"2026-03-26T10:30:00Z","method":"POST","path":"/mcp","status":200,"duration_ms":45,"client_ip":"1.2.3.4","key_name":"claude-prod","session_id":"abc123","user_agent":"claude-code/1.0"}
```

## Agent Instructions

The tool schemas say what each tool accepts; they do not say which one fits a
question, or that asking for a projection instead of whole documents changes
the cost by more than an order of magnitude. Measured on this engine, the same
`full_text_search` over five 12 KB documents returns ~19 700 tokens by default
and ~327 with `fields: ["key"]`.

[`integrations/agent-instructions/`](../integrations/agent-instructions/) ships
that guidance in the formats agents read:

| File | For |
|---|---|
| `AGENTS.md` | Paste into a project; the source every other variant is generated from |
| `claude-code/SKILL.md` | Claude Code skill |
| `cursor/mddb.mdc` | Cursor rule |
| `windsurf/mddb.md` | Windsurf rule |

Edit `AGENTS.md` and run `make agent-instructions`; CI fails if a variant was
edited directly, and a test fails if the instructions name a tool that no
longer exists.

## Locating a Match

`full_text_search` accepts `highlight: true` (v2.12.0+), which returns each
matching fragment with the lines it occupies:

```json
{"name": "full_text_search", "arguments": {
  "collection": "theme", "query": "background",
  "highlight": true, "fragment_size": 80, "fields": ["key"]
}}
```

```
css/style.css:158-163
"…}\n\n.hero-banner {\n  <mark>background</mark>: url(hero.png);"
```

`fields` drops the document body and `highlight` says where to look, so the two
together answer the question without carrying the file — under 800 bytes for a
164-line stylesheet, against ~20 000 tokens for the same search returning whole
documents. Projections keep the fragments deliberately: dropping them would
leave a caller with neither the text nor its location.

Lower `fragment_size` for source. The default of 150 is a byte budget tuned for
prose; code lines are short, so it covers about fifteen lines of CSS.

## Built-in Tool Catalog

All 79 built-in tools, grouped by area. Tool inputs are self-describing via
MCP schema discovery (`tools/list`); the `semantic_search` tool additionally
supports `retrieval_mode`/`window_size` (passage-level results) and
`mmr`/`mmr_lambda` (result diversification).

### Documents

| Tool | Description |
|------|-------------|
| `add_document` | Add or update a document in MDDB. |
| `search_documents` | Search documents with filters and sorting. |
| `delete_document` | Delete a document from MDDB. |
| `import_url` | Import a markdown document from a URL. |
| `set_ttl` | Set or remove time-to-live on a document. |
| `validate_document` | Validate document metadata against collection schema without adding the document. |
| `add_documents_batch` | Add multiple documents to a collection in a single batch operation. |
| `delete_documents_batch` | Delete multiple documents from a collection in a single batch operation. |
| `get_meta_keys` | List all unique metadata keys and their values for a collection. |
| `get_checksum` | Get a CRC32 checksum for a collection that changes when documents are modified. |
| `ingest_documents` | Bulk ingest documents with URL-based key derivation, YAML frontmatter extraction, content deduplication, and automatic metadata injection. |
| `upload_file` | Upload a file and convert it to markdown. |

### Search

| Tool | Description |
|------|-------------|
| `aggregate` | Compute metadata facets (value counts) and date histograms over a collection, with optional metadata pre-filtering. |
| `semantic_search` | Search documents by meaning using semantic similarity. |
| `full_text_search` | Search documents by text content using full-text search with term matching and relevance scoring. |
| `autocomplete` | Prefix autocomplete — returns top-N terms starting with the query, ranked by document frequency. |
| `classify_document` | Zero-shot document classification. |
| `hybrid_search` | Combined sparse (FTS) + dense (vector) search with alpha blending or Reciprocal Rank Fusion. |
| `cross_search` | Search across multiple collections using a source document's embedding or a text query. |
| `find_duplicates` | Find duplicate and similar documents within a collection. |

### Geo

| Tool | Description |
|------|-------------|
| `geo_search` | Find documents within a given radius (in meters) of a latitude/longitude point. |
| `geo_within` | Find documents inside an axis-aligned bounding box (minLat..maxLat × minLng..maxLng). |
| `geo_polygon` | Find documents whose coordinates fall inside a GeoJSON Polygon (outer ring + optional holes) or MultiPolygon (union of polygons). |
| `geo_stats` | Report per-collection indexed-point counts plus any loaded postcode datasets. |
| `geo_encode` | Convert a (lat, lng) pair into a geohash string of the requested precision (1..12, default 12). |
| `geo_decode` | Convert a geohash string back to the (lat, lng) centroid of its cell. |

### Vectors

| Tool | Description |
|------|-------------|
| `vector_reindex` | Re-embed all documents in a collection. |
| `vector_stats` | Get vector/embedding statistics including provider info and per-collection embedding coverage. |

### FTS admin

| Tool | Description |
|------|-------------|
| `fts_reindex` | Reindex full-text search for a collection. |
| `fts_languages` | List all supported FTS languages with their codes and names. |
| `list_synonyms` | List all synonym entries for a collection. |
| `add_synonym` | Add or update a synonym group for a term. |
| `delete_synonym` | Delete a synonym group for a term in a collection. |
| `list_stopwords` | List all stop words (default + custom) for a collection. |
| `add_stopwords` | Add custom stop words to a collection's FTS index. |
| `delete_stopwords` | Remove custom stop words from a collection. |

### Revisions

| Tool | Description |
|------|-------------|
| `truncate_revisions` | Truncate revision history for a collection, keeping only the N most recent revisions per document. |
| `list_revisions` | List revision history for a document. |
| `restore_revision` | Restore a document to a previous revision by timestamp. |

### Curation

| Tool | Description |
|------|-------------|
| `list_curation_rules` | (v2.9.14+) List curation rules that pin or hide documents for specific search queries. |
| `create_curation_rule` | (v2.9.14+) Create a rule that pins specific documents to fixed positions or hides others when a query matches. |
| `update_curation_rule` | (v2.9.14+) Replace an existing curation rule by id. |
| `delete_curation_rule` | (v2.9.14+) Remove a curation rule by id. |

### Automation

| Tool | Description |
|------|-------------|
| `list_automation` | List all automation rules (webhooks, triggers, crons). |
| `create_automation` | Create a new automation rule (webhook target, search trigger, or cron schedule). |
| `get_automation` | Get a specific automation rule by ID. |
| `update_automation` | Update an existing automation rule. |
| `delete_automation` | Delete an automation rule by ID. |
| `test_automation` | Test a trigger rule by running its search and returning matches (dry run, no webhook fired). |
| `get_automation_logs` | List automation execution logs with optional filtering by rule ID and status. |

### Webhooks

| Tool | Description |
|------|-------------|
| `register_webhook` | Register a webhook to receive HTTP callbacks when documents are added, updated, or deleted. |
| `list_webhooks` | List all registered webhooks. |
| `delete_webhook` | Delete a registered webhook by ID. |

### Schemas

| Tool | Description |
|------|-------------|
| `set_schema` | Set JSON Schema for collection metadata validation. |
| `get_schema` | Get JSON Schema for a collection. |
| `delete_schema` | Delete/disable schema validation for a collection. |
| `list_schemas` | List all collection schemas. |

### Memory RAG

| Tool | Description |
|------|-------------|
| `memory_start_session` | Start a new memory/conversation session for RAG. |
| `memory_add_message` | Add a message to an existing memory session. |
| `memory_recall` | Semantically recall relevant messages from past conversations. |
| `memory_summarize` | Generate a summary of a conversation session. |
| `memory_list_sessions` | List memory/conversation sessions with optional filtering by user, scenario, and sorting. |
| `memory_session_history` | Get the full message history of a specific conversation session, ordered chronologically. |

### WordPress

| Tool | Description |
|------|-------------|
| `wordpress_publish` | (v2.11.0+) Create or update a WordPress post/page via the mddb-sync plugin, including tags, categories, custom taxonomies, meta fields and Polylang/WPML language assignment. |
| `wordpress_set_status` | (v2.11.0+) Change the publishing status of a WordPress post/page (publish, draft, pending, private, future, trash) via the mddb-sync plugin. |

### Bulk ingest

| Tool | Description |
|------|-------------|
| `bulk_ingest_submit` | Queue a long-running bulk ingest job and return immediately with a job identifier. |
| `bulk_ingest_status` | Return the current status record (counters, timestamps, up to 50 errors) for a previously-submitted bulk ingest job. |
| `bulk_ingest_list` | List bulk ingest jobs newest-first, optionally filtered by collection. |
| `bulk_ingest_cancel` | Cancel a pending bulk ingest job. |

### Ops

| Tool | Description |
|------|-------------|
| `get_stats` | Get MDDB server statistics. |
| `export_documents` | Export documents from a collection in NDJSON format. |
| `create_backup` | Create a backup of the MDDB database. |
| `restore_backup` | Restore the MDDB database from a backup file. |
| `update_document` | Partially update a document. |
| `get_document_meta` | Get document metadata without content. |
| `delete_collection` | Delete an entire collection and all its documents, revisions, and metadata indices. |
| `get_collection_config` | Get configuration attributes for a collection (type, description, icon, color, custom metadata). |
| `set_collection_config` | Set or update configuration attributes for a collection. |
| `list_collection_configs` | List all collections that have custom configuration set. |

## Access Modes

Each protocol can have its own read/write mode:

| Env Var | Protocol | Default |
|---------|----------|---------|
| `MDDB_MODE` | **Global** | `wr` |
| `MDDB_API_MODE` | HTTP/REST + GraphQL | inherits global |
| `MDDB_GRPC_MODE` | gRPC | inherits global |
| `MDDB_MCP_MODE` | MCP (all transports) | inherits global |
| `MDDB_HTTP3_MODE` | HTTP/3 (QUIC) | inherits global |

In read-only mode:
- Built-in tools with `readOnlyHint=true` (search, stats, list, export) → **allowed**
- Built-in tools with writes/deletes → **blocked** with error
- Custom tools with read-only actions (`semantic_search`, `search_documents`, `full_text_search`) → **allowed**
- Custom tools with write actions → **blocked**

```bash
# Public MCP: read-only, custom tools only, API keys, rate limited
docker run -d \
  -e MDDB_MCP_MODE=read \
  -e MDDB_MCP_BUILTIN_TOOLS=false \
  -e MDDB_MCP_API_KEY_ENABLED=true \
  -e "MDDB_MCP_API_KEYS=sk-public:external" \
  -e MDDB_MCP_RATE_LIMIT_ENABLED=true \
  -e MDDB_MCP_RATE_LIMIT_REQUESTS=60 \
  -e MDDB_MCP_LOGGING_ENABLED=true \
  -e MDDB_MCP_CONFIG=/app/tools.yml \
  tradik/mddb:latest
```

## Protocol Features (2025-11-25)

| Feature | Description |
|---------|-------------|
| **Tool Annotations** | `readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint` on all tools |
| **Structured Output** | `outputSchema` on 9 key tools for client-side validation |
| **5 Prompts** | `analyze-collection`, `search-help`, `summarize-collection`, `import-guide`, `rag-pipeline` |
| **Completion** | Autocomplete for collection names and prompt arguments |
| **Logging** | `logging/setLevel` with RFC 5424 levels (debug → emergency) |
| **Progress Tokens** | `notifications/progress` for long-running operations |
| **Cursor Pagination** | `tools/list` and `resources/list` |
| **Notifications** | `notifications/initialized`, `notifications/cancelled` |
| **Memory RAG Tools** | 6 tools for conversational memory: start session, add message, recall, summarize, list sessions, history |
| **Async Bulk Ingest Tools** (v2.9.12+) | 4 tools for long-running ingest jobs: submit, status, list, cancel |
| **Autocomplete Tool** (v2.9.12+) | Prefix autocomplete over the FTS index — top-N suggestions ranked by document frequency |
| **Per-Query Boost** (v2.9.12+) | `full_text_search` and `hybrid_search` tools accept a `boost` map keyed by `"metaKey:metaValue"` to boost/demote matching documents at query time |

## Async Bulk Ingest Tools (v2.9.12+)

Queue long-running bulk ingest jobs from LLMs without blocking on the tool call. The worker drains jobs FIFO in 500-document chunks; status records persist across restarts.

| Tool | Description | Write? |
|------|-------------|--------|
| `bulk_ingest_submit` | Queue a new job with documents + optional `callback_url`; returns job ID | Yes |
| `bulk_ingest_status` | Return current status record (counters, timestamps, errors) | No |
| `bulk_ingest_list` | List all jobs newest-first, optionally filtered by collection | No |
| `bulk_ingest_cancel` | Cancel a pending job (in-flight jobs run to completion) | Yes |

## Autocomplete Tool (v2.9.12+)

| Tool | Description | Write? |
|------|-------------|--------|
| `autocomplete` | Top-N prefix suggestions over the FTS inverted index, ranked by doc frequency; supports field scoping | No |

## WordPress Publishing Tools (v2.11.0+)

Publish straight from MCP into WordPress sites running the [mddb-sync plugin](https://github.com/tradik/mddb/blob/main/integrations/wordpress-plugin/README.md) with **Remote publishing** enabled. Posts and pages, tags/categories/custom taxonomies, post meta ("metafields") and Polylang/WPML language assignment + translation linking are all supported.

| Tool | Description | Write? |
|------|-------------|--------|
| `wordpress_publish` | Create or update a post/page (upsert by `post_id`, else `post_type`+`slug`) with content (Markdown or HTML), status, tags, categories, taxonomies, meta and language | Yes |
| `wordpress_set_status` | Change publishing status: `publish`, `draft`, `pending`, `private`, `future` (with `date`) or `trash` | Yes |

The target site comes from the collection's config — one collection per WordPress site, matching how the sync plugin pushes content in:

```json
// 1. Pin the WordPress target to the collection (once)
{"name": "set_collection_config", "arguments": {
  "collection": "example_com",
  "wordpress": {"url": "https://example.com", "api_key": "<publish key from Settings → MDDB Sync>"}
}}

// 2. Publish a post with tags, meta and a language
{"name": "wordpress_publish", "arguments": {
  "collection": "example_com",
  "post_type": "post",
  "title": "Hello from MDDB",
  "content_markdown": "# Hello\n\nPublished via MCP.",
  "status": "publish",
  "tags": ["mddb", "mcp"],
  "categories": ["News"],
  "meta": {"seoTitle": "Hello from MDDB"},
  "lang": "en_US"
}}

// 3. Later: unpublish it
{"name": "wordpress_set_status", "arguments": {"collection": "example_com", "post_id": 123, "status": "draft"}}
```

The site URL must be `https://` (plain `http://` only for localhost). Both tools also accept explicit `site_url` + `api_key` arguments to skip the collection config. Translations: pass `lang` plus `translation_of: <post id>` to link a new post as a translation via Polylang or WPML.

> **Projection note (v2.11.0, GO-019):** for `search_documents` / `full_text_search` / `semantic_search` / `hybrid_search`, passing `fields` now also drops the document body by default — pass `include_content: true` explicitly to keep it.

## Memory RAG Tools

Built-in MCP tools for conversational memory and RAG:

| Tool | Description | Write? |
|------|-------------|--------|
| `memory_start_session` | Start a new conversation session | Yes |
| `memory_add_message` | Add a message (auto-embedded) | Yes |
| `memory_recall` | Semantic/hybrid/keyword recall across sessions | No |
| `memory_summarize` | Generate and store session summary | Yes |
| `memory_list_sessions` | List sessions with filters | No |
| `memory_session_history` | Get chronological message history | No |

### Example: Agent with Memory

```json
// 1. Start session
{"name": "memory_start_session", "arguments": {"user_id": "user-1", "scenario": "support"}}

// 2. Store messages as conversation progresses
{"name": "memory_add_message", "arguments": {"session_id": "abc...", "role": "user", "content": "How do I use vector search?"}}
{"name": "memory_add_message", "arguments": {"session_id": "abc...", "role": "assistant", "content": "Use POST /v1/vector-search..."}}

// 3. In a future session, recall relevant context
{"name": "memory_recall", "arguments": {"query": "vector search usage", "user_id": "user-1", "top_k": 5, "include_content": true}}

// 4. Summarize when done
{"name": "memory_summarize", "arguments": {"session_id": "abc..."}}
```

## Built-in Prompts

| Prompt | Arguments | Description |
|--------|-----------|-------------|
| `analyze-collection` | `collection` (required) | Document count, metadata keys, content patterns, suggestions |
| `search-help` | `use_case` (required) | Which search method to use for your use case |
| `summarize-collection` | `collection` (required), `limit` | Topic overview, themes, distribution |
| `import-guide` | `source` (required) | Step-by-step import from wordpress/url/file/api/scraping |
| `rag-pipeline` | `collection` (required), `model` | RAG pipeline design with embedding and search strategy |

---

**[← Back to README](../README.md)** | **[→ LLM Connections](LLM_CONNECTIONS.md)** | **[→ Custom Tools](CUSTOM-TOOLS.md)**
