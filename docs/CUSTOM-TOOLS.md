---
title: "Custom YAML Tools for MCP"
slug: "docs/custom-tools"
description: "Define site-specific MCP tools for MDDB in YAML, giving an AI agent a small purpose-built API instead of the full generic tool surface."
status: publish
---

# Custom YAML Tools for MCP

Define website-specific MCP tools in YAML that simplify AI interactions with your MDDB data.

## Overview

By default, `mddbd` exposes 54+ built-in MCP tools (add_document, search_documents, semantic_search, hybrid_search, list_automation, etc.) with tool annotations per MCP 2025-11-25 and 2026-07-28. Custom tools let you create **domain-specific wrappers** with preconfigured defaults — so the AI sees `search_faq(query)` instead of `semantic_search(collection, query, topK, threshold, ...)`.

Custom tools are defined in a YAML file under the `custom_tools:` key. Point `mddbd` to the file via `MDDB_MCP_CONFIG=/path/to/config.yaml`. They are registered alongside built-in tools and work on all transports (stdio, Streamable HTTP, and legacy SSE).

> **Tip:** Set `MDDB_MCP_BUILTIN_TOOLS=false` to hide all built-in tools and expose **only** your custom tools. Combined with `MDDB_MCP_MODE=read`, this gives AI agents a locked-down, domain-specific read-only interface.

## Quick Example

```yaml
# mcp-config.yaml
custom_tools:
  - name: search_faq
    description: "Search frequently asked questions"
    action: semantic_search
    defaults:
      collection: faq
      topK: 3
      threshold: 0.6
    parameters:
      - name: query
        type: string
        required: true
        description: "User question"

  - name: latest_news
    description: "Get latest news articles"
    action: search_documents
    defaults:
      collection: news
      sort: updatedAt
      asc: false
      limit: 5

  - name: search_products
    description: "Search product catalog by keyword"
    action: full_text_search
    defaults:
      collection: products
      limit: 10
    parameters:
      - name: query
        type: string
        required: true
```

## Configuration Reference

### CustomTool fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Tool name exposed to LLM (must not conflict with built-in names) |
| `description` | string | yes | Human-readable description shown in tools/list |
| `action` | string | yes | Underlying MDDB operation (see Supported Actions) |
| `defaults` | object | no | Default values merged with user arguments |
| `parameters` | array | no | Parameters exposed to the LLM (omit for zero-arg tools) |

### Supported Actions

| Action | Maps to | Use case |
|--------|---------|----------|
| `semantic_search` | `toolSemanticSearch` → `/v1/vector-search` | Find documents by meaning |
| `search_documents` | `toolSearchDocuments` → `/v1/search` | Filter/sort by metadata |
| `full_text_search` | `toolFTSSearch` → `/v1/fts` | Keyword search with TF scoring |

### Defaults

All defaults are optional. Declared user parameters override most defaults — **except the scope keys `collection`, `filterMeta`, `includeContent` and `fields`, which are operator-locked once set** (v2.11.0+, SEC-010): a custom tool exists to narrow the surface, so client arguments cannot widen it back.

| Default | Type | Actions | Description |
|---------|------|---------|-------------|
| `collection` | string | all | Target collection |
| `topK` | int | semantic_search | Max results |
| `threshold` | float | semantic_search | Minimum similarity score (0-1) |
| `includeContent` | bool | all | Include the document body (`contentMd`) in each hit. **Default `true`**; set `false` to drop the body on metadata-only lookups (v2.10.2+) |
| `fields` | list of string | all | Restrict returned meta to the listed keys; each hit keeps `id`, `key` and the requested `meta.<field>` keys. The body follows `includeContent` (pair with `includeContent: false` to drop it too). **Empty = all meta** (v2.10.2+) |
| `sort` | string | search_documents | Sort field (addedAt, updatedAt) |
| `asc` | bool | search_documents | Sort ascending (default false) |
| `limit` | int | search_documents, full_text_search | Max results |
| `offset` | int | search_documents | Pagination offset |
| `filterMeta` | object | semantic_search, search_documents | Metadata filter |
| `query` | string | semantic_search, full_text_search | Default query (usually overridden) |

### Cutting client token usage (`includeContent` + `fields`)

For narrow, high-frequency lookups the default response is expensive: every hit
ships **all** meta keys **and** the full `contentMd` body, and each result is
re-sent on subsequent conversation turns. `includeContent: false` drops the body
and `fields: [...]` projects the meta down to just the keys you need — together
they cut client token usage by roughly 5–30× on lookup-style tools. Both are
strictly opt-in and backward compatible: unset `includeContent` defaults to
`true` and empty `fields` returns everything, so existing tools are unchanged.

```yaml
- name: version_check
  description: "Check the current version of a package"
  action: full_text_search              # read-only, so allowed in MDDB_MCP_MODE=read
  defaults:
    collection: versions
    limit: 1
    includeContent: false                 # drop the changelog body
    fields: [name, currentVersion, versionChangedAt, dockerImage]
  parameters:
    - name: query
      type: string
      required: true
```

With this config each hit is reduced to `id`, `key` and the four requested meta
keys — e.g. `version_check` drops from ~266 to ~40 response tokens, and a 8-hit
batch lookup from ~4.5k to ~600. Both options also work as per-call arguments
(`include_content`, `fields`) on the built-in `search_documents`,
`full_text_search` and `semantic_search` tools.

### Parameters

Each parameter defines an argument that the LLM can provide.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Argument name |
| `type` | string | yes | JSON Schema type: `string`, `integer`, `number`, `boolean`, `object` |
| `required` | bool | no | Whether the LLM must provide this argument |
| `description` | string | no | Description shown to the LLM |

## How It Works

```
AI calls: search_faq(query: "reset password")
  → mddbd finds "search_faq" in custom tools
  → Merges defaults: {collection:"faq", topK:3, threshold:0.6}
  → Merges user args: + {query:"reset password"}
  → Calls: semantic_search({collection:"faq", query:"reset password", topK:3, threshold:0.6})
  → Returns: vector search results
```

Argument merging follows three rules (v2.11.0+, SEC-010):

1. **Only declared parameters pass through.** A client argument whose name is not listed under `parameters` is silently dropped.
2. **Pinned scope keys are locked.** When `collection`, `filterMeta`, `includeContent` or `fields` is set in `defaults`, a client argument of the same name is ignored — a tool pinned to `collection: public` + `includeContent: false` cannot be called with `collection: secrets` + `include_content: true`.
3. **Everything else: user wins.** For example, if a custom tool has `defaults.limit: 5`, declares `limit` as a parameter, and the user passes `limit: 20`, the value `20` is used.

## Validation

At startup, `mddbd` validates all custom tools:

- **Name conflicts**: Custom tool names cannot match any of the 44 built-in names
- **Duplicate names**: No two custom tools can share the same name
- **Valid action**: Must be one of `semantic_search`, `search_documents`, `full_text_search`
- **Valid parameter types**: Must be valid JSON Schema types
- **Required fields**: `name`, `description`, and `action` are required

Invalid configuration causes a startup error with a descriptive message.

## Docker Deployment

```bash
# Mount your custom tools config
docker run -i --rm --network host \
  -v ./mcp-config.yaml:/app/mcp-config.yaml \
  -e MDDB_MCP_STDIO=true \
  -e MDDB_MCP_CONFIG=/app/mcp-config.yaml \
  tradik/mddb:latest
```

Docker Compose:
```yaml
services:
  mddb:
    image: tradik/mddb:latest
    volumes:
      - ./mcp-config.yaml:/app/mcp-config.yaml
    environment:
      - MDDB_MCP_STDIO=true
      - MDDB_MCP_CONFIG=/app/mcp-config.yaml
```

## Windsurf / Claude Desktop

```json
{
  "mcpServers": {
    "mddb": {
      "command": "docker",
      "args": [
        "run", "-i", "--rm", "--network", "host",
        "-v", "./mcp-config.yaml:/app/mcp-config.yaml",
        "-e", "MDDB_MCP_STDIO=true",
        "-e", "MDDB_MCP_CONFIG=/app/mcp-config.yaml",
        "tradik/mddb:latest"
      ]
    }
  }
}
```

## Use Cases

### E-commerce Site
```yaml
custom_tools:
  - name: search_products
    description: "Find products by description"
    action: semantic_search
    defaults: { collection: products, topK: 5, threshold: 0.5 }
    parameters:
      - { name: query, type: string, required: true, description: "Product search query" }

  - name: bestsellers
    description: "Show bestselling products"
    action: search_documents
    defaults: { collection: products, sort: updatedAt, asc: false, limit: 10, filterMeta: { tag: ["bestseller"] } }
```

### Knowledge Base
```yaml
custom_tools:
  - name: ask_kb
    description: "Search the knowledge base"
    action: semantic_search
    defaults: { collection: kb, topK: 5, threshold: 0.6 }
    parameters:
      - { name: query, type: string, required: true, description: "Your question" }

  - name: search_articles
    description: "Full-text search in articles"
    action: full_text_search
    defaults: { collection: articles, limit: 10 }
    parameters:
      - { name: query, type: string, required: true }
```

### Multi-language Blog
```yaml
custom_tools:
  - name: latest_posts
    description: "Get the latest blog posts"
    action: search_documents
    defaults: { collection: blog, sort: updatedAt, asc: false, limit: 5 }

  - name: search_blog
    description: "Search blog posts"
    action: full_text_search
    defaults: { collection: blog, limit: 10 }
    parameters:
      - { name: query, type: string, required: true, description: "Search keywords" }
```

## Backward Compatibility

- If `MDDB_MCP_CONFIG` is not set or the file has no `custom_tools` key, `mddbd` works with built-in tools only
- Built-in tools are always available regardless of custom tools configuration
- Custom tools are additive — they never replace built-in tools
