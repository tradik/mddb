---
title: "LLM Connections"
slug: "docs/llm-connections"
description: "Connect MDDB to Claude, Cursor, Windsurf, ChatGPT and Ollama for RAG and knowledge-base workflows through the built-in MCP server."
status: publish
---

# LLM Connections

Connect MDDB to AI agents and LLM tools for RAG (Retrieval-Augmented Generation) and knowledge base workflows.

**[→ Full MCP Server Configuration (env vars, API keys, rate limits, logging)](MCP.md)**

## Overview

MDDB provides multiple integration paths:

| Method | Best For | Protocol |
|--------|----------|----------|
| **MCP Server (stdio)** | Claude Desktop, Windsurf, IDE agents | MCP stdio |
| **MCP Streamable HTTP** | Remote MCP clients, web agents (2026-07-28 or 2025-11-25) | `POST /mcp` |
| **MCP-over-SSE (legacy)** | Older MCP clients (2024-11-05) | `GET /sse` + `POST /message` |
| **REST API** | ChatGPT, custom agents, any HTTP client | HTTP/JSON |
| **gRPC API** | High-performance integrations | gRPC/Protobuf |

## Domain Configuration

Set `MDDB_MCP_DOMAIN` to your server hostname. The Panel's **LLM Connections** page reads this value and pre-fills it in the domain field — all generated configs use it automatically.

| Env Var | Default | Example |
|---------|---------|---------|
| `MDDB_MCP_DOMAIN` | _(panel hostname)_ | `myserver.com` |

```bash
# Docker
docker run -d -p 11023:11023 -p 9000:9000 \
  -e MDDB_MCP_DOMAIN=myserver.com \
  tradik/mddb:latest

# Binary
MDDB_MCP_DOMAIN=myserver.com ./mddbd
```

You can also change the domain directly in the Panel — the input field is above the config tabs on the LLM Connections page.

## MCP Streamable HTTP Transport (Recommended)

MDDB implements the Streamable HTTP transport for both revisions it speaks: the stateless [2026-07-28](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http) and the handshake-based [2025-11-25](https://spec.modelcontextprotocol.io/specification/2025-11-25/transport/streamable-http/). A single `/mcp` endpoint handles all communication — simpler than the legacy SSE transport. Which revision a request gets is decided per request; see [Protocol revisions](MCP-REVISIONS.md).

### How It Works

1. Client sends `POST /mcp` with JSON-RPC request
2. Server responds with JSON (or SSE stream for server-initiated messages via `GET /mcp`)
3. Session management via `MCP-Session-Id` header (assigned at initialize)
4. Client can `DELETE /mcp` to terminate the session

### Example

```bash
# 1. Initialize (server assigns session ID in response header)
curl -X POST http://localhost:9000/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' \
  -D -
# Response header: MCP-Session-Id: a9f9de82af6938bc...
# Body: {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{...}}}

# 2. Send initialized notification
curl -X POST http://localhost:9000/mcp \
  -H "Content-Type: application/json" \
  -H "MCP-Session-Id: a9f9de82af6938bc..." \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'
# Returns: 202 Accepted

# 3. List tools
curl -X POST http://localhost:9000/mcp \
  -H "Content-Type: application/json" \
  -H "MCP-Session-Id: a9f9de82af6938bc..." \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'

# 4. Call a tool
curl -X POST http://localhost:9000/mcp \
  -H "Content-Type: application/json" \
  -H "MCP-Session-Id: a9f9de82af6938bc..." \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_documents","arguments":{"collection":"blog"}}}'

# 5. List prompts
curl -X POST http://localhost:9000/mcp \
  -H "Content-Type: application/json" \
  -H "MCP-Session-Id: a9f9de82af6938bc..." \
  -d '{"jsonrpc":"2.0","id":4,"method":"prompts/list"}'
```

### Client Configuration

For MCP clients that support Streamable HTTP transport:

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

## MCP-over-SSE Transport (Legacy)

The legacy [MCP-over-SSE transport](https://modelcontextprotocol.io/docs/concepts/transports#server-sent-events-sse) (2024-11-05 spec) is still supported for backward compatibility.

### How It Works

1. Client connects to `GET http://localhost:9000/sse` (SSE stream)
2. Server sends an `endpoint` event with a URL: `/message?sessionId=<id>`
3. Client POSTs JSON-RPC requests to `http://localhost:9000/message?sessionId=<id>`
4. Server sends JSON-RPC responses back via the SSE stream as `message` events

### Client Configuration

```json
{
  "mcpServers": {
    "mddb": {
      "transport": "sse",
      "url": "http://localhost:9000/sse"
    }
  }
}
```

### Endpoints

| Port | Path | Method | Description |
|------|------|--------|-------------|
| MCP (9000) | `/mcp` | POST (GET/DELETE for handshake clients) | **Streamable HTTP** (2026-07-28 + 2025-11-25) — recommended |
| MCP (9000) | `/sse` | GET | **Legacy SSE** — SSE connection, returns `endpoint` event |
| MCP (9000) | `/message?sessionId=X` | POST | **Legacy SSE** — send JSON-RPC request to a session |
```

## Per-Protocol Access Modes

Each protocol can have its own read/write mode, independent of the global `MDDB_MODE`:

| Env Var | Protocol | Default |
|---------|----------|---------|
| `MDDB_MODE` | **Global** (all protocols) | `wr` |
| `MDDB_API_MODE` | HTTP/JSON REST + GraphQL | inherits `MDDB_MODE` |
| `MDDB_GRPC_MODE` | gRPC | inherits `MDDB_MODE` |
| `MDDB_MCP_MODE` | MCP (stdio + Streamable HTTP + SSE) | inherits `MDDB_MODE` |
| `MDDB_HTTP3_MODE` | HTTP/3 (QUIC) | inherits `MDDB_MODE` |

Values: `read`, `write`, `wr` (read-write)

**Example: MCP read-only, API read-write**

```bash
# Internal services write via REST API, AI agents read via MCP
docker run -d \
  -e MDDB_MCP_MODE=read \
  -p 11023:11023 -p 9000:9000 \
  tradik/mddb:latest
```

In read-only mode, MCP tools with `readOnlyHint=false` (write, delete, destructive tools) return an error. Read-only tools (search, stats, list, export) work normally.

### Disabling Built-in Tools

Set `MDDB_MCP_BUILTIN_TOOLS=false` to hide all 54 built-in tools, exposing only custom YAML tools:

```bash
docker run -d \
  -e MDDB_MCP_BUILTIN_TOOLS=false \
  -e MDDB_MCP_CONFIG=/app/mcp_config.yaml \
  tradik/mddb:latest
```

This restricts AI agents to domain-specific tools only — no direct access to `add_document`, `delete_collection`, etc.

### Custom Tools in Read-Only Mode

Custom tools that use read-only actions (`semantic_search`, `search_documents`, `full_text_search`, `fts_languages`) work in read-only and follower modes. This enables public MCP endpoints on follower instances:

```bash
# Follower with read-only custom tools only
docker run -d \
  -e MDDB_REPLICATION_ROLE=follower \
  -e MDDB_REPLICATION_LEADER_ADDR=leader:11024 \
  -e MDDB_MCP_BUILTIN_TOOLS=false \
  -e MDDB_MCP_CONFIG=/app/mcp-tools.yml \
  tradik/mddb:latest
```

Custom tools with write actions are blocked in read-only mode, same as built-in write tools.

### API Key Authentication

Protect MCP endpoints with API keys:

```bash
docker run -d \
  -e MDDB_MCP_API_KEY_ENABLED=true \
  -e MDDB_MCP_API_KEYS="sk-abc123:claude-prod,sk-def456:cursor-dev" \
  tradik/mddb:latest
```

Clients authenticate via `X-API-Key` header, `Authorization: Bearer` header, or `?api_key=` query param (for SSE):

```bash
# Claude Code
claude mcp add mydb --transport http https://mydb.example.com/mcp \
  --header "X-API-Key: sk-abc123"

# curl
curl -X POST https://mydb.example.com/mcp \
  -H "X-API-Key: sk-abc123" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

### Rate Limiting

Protect against abuse with per-client rate limits:

| Env Var | Default | Description |
|---------|---------|-------------|
| `MDDB_MCP_RATE_LIMIT_ENABLED` | `false` | Enable rate limiting |
| `MDDB_MCP_RATE_LIMIT_REQUESTS` | `100` | Requests per window |
| `MDDB_MCP_RATE_LIMIT_WINDOW` | `60` | Window in seconds |
| `MDDB_MCP_RATE_LIMIT_BURST` | `20` | Burst allowance above limit |
| `MDDB_MCP_RATE_LIMIT_BY` | `ip` | Rate limit key: `ip`, `api_key`, or `session` |

Response headers: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`. Returns `429 Too Many Requests` with `Retry-After` header when exceeded.

### Request Logging

Enable structured JSON audit logs for all MCP requests:

```bash
MDDB_MCP_LOGGING_ENABLED=true
MDDB_MCP_LOGGING_LEVEL=info   # debug|info|warn|error
```

Log output (to stderr):
```json
{"timestamp":"2026-03-26T10:30:00Z","method":"POST","path":"/mcp","status":200,"duration_ms":45,"client_ip":"1.2.3.4","key_name":"claude-prod","session_id":"abc123","user_agent":"claude-code/1.0"}
```

## MCP Protocol Features

MDDB implements both MCP revisions in full — see [Protocol revisions](MCP-REVISIONS.md) for what each requires:

| Feature | Description |
|---------|-------------|
| **Tool Annotations** | All 52+ tools have `readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint` — enables AI clients to auto-approve safe tools |
| **Structured Output** | `outputSchema` on key tools (stats, search, classification, aggregation) for client-side validation |
| **5 Built-in Prompts** | `analyze-collection`, `search-help`, `summarize-collection`, `import-guide`, `rag-pipeline` |
| **Completion** | Autocomplete for collection names and prompt arguments |
| **MCP Logging** | `logging/setLevel` with RFC 5424 levels (debug → emergency) |
| **Progress Tokens** | `notifications/progress` for long-running tools (reindex, ingest, backup) |
| **Cursor Pagination** | `tools/list` and `resources/list` support cursor-based pagination |
| **Notifications** | Accepts `notifications/initialized`, `notifications/cancelled`, `notifications/roots/list_changed` |

### Built-in Prompts

| Prompt | Description |
|--------|-------------|
| `analyze-collection` | Document count, metadata keys, content patterns, improvement suggestions |
| `search-help` | Guidance on which search method to use for your use case |
| `summarize-collection` | Topic overview, key themes, document distribution |
| `import-guide` | Step-by-step import instructions (WordPress, URL, file, API, scraping) |
| `rag-pipeline` | Design a RAG pipeline with embedding and search strategy |

## Claude Desktop / Claude Code

MCP is built directly into the MDDB server — no separate service needed.

### Docker (Recommended)

Add to your Claude Desktop config:

**macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
**Windows**: `%APPDATA%\Claude\claude_desktop_config.json`
**Linux**: `~/.config/Claude/claude_desktop_config.json`

```json
{
  "mcpServers": {
    "mddb": {
      "command": "docker",
      "args": [
        "run", "-i", "--rm", "--network", "host",
        "-v", "mddb-data:/app/data",
        "-e", "MDDB_MCP_STDIO=true",
        "tradik/mddb:latest"
      ],
      "env": {}
    }
  }
}
```

### Local Binary

Use the `mddbd` binary directly:

```json
{
  "mcpServers": {
    "mddb": {
      "command": "/path/to/mddbd",
      "args": [],
      "env": {
        "MDDB_MCP_STDIO": "true",
        "MDDB_PATH": "/path/to/mddb.db"
      }
    }
  }
}
```

### Available MCP Tools (54+)

Once connected, your LLM agent has access to all built-in tools (with annotations for auto-approve):

**Document Management**
`add_document` `update_document` `delete_document` `search_documents` `get_document_meta` `add_documents_batch` `delete_documents_batch`

**Search**
`semantic_search` `full_text_search` `hybrid_search` `cross_search`

**Vector**
`vector_reindex` `vector_stats`

**Collections**
`delete_collection` `get_collection_config` `set_collection_config` `list_collection_configs`

**Analysis**
`classify_document` `find_duplicates` `get_checksum`

**Revisions**
`list_revisions` `restore_revision` `truncate_revisions`

**Upload & Import**
`upload_file` `import_url` `ingest_documents`

**Export & Backup**
`export_documents` `create_backup` `restore_backup`

**Full-Text Search Config**
`list_synonyms` `add_synonym` `delete_synonym` `list_stopwords` `add_stopwords` `delete_stopwords`

**Schemas**
`set_schema` `get_schema` `delete_schema` `list_schemas` `validate_document`

**Webhooks**
`register_webhook` `list_webhooks` `delete_webhook`

**Automation**
`list_automation` `create_automation` `get_automation` `update_automation` `delete_automation` `test_automation` `get_automation_logs`

**System**
`get_stats` `set_ttl` `get_meta_keys`

## ChatGPT (Custom GPT)

Create a Custom GPT that connects to MDDB via its REST API.

1. Go to [platform.openai.com/gpts](https://platform.openai.com/gpts)
2. Create or edit a Custom GPT
3. In **Configure** → **Actions** → **Create new action**
4. Paste this OpenAPI schema (replace the server URL with your domain):

```json
{
  "openapi": "3.1.0",
  "info": { "title": "MDDB API", "version": "2.9.12" },
  "servers": [{ "url": "https://your-domain:11023" }],
  "paths": {
    "/v1/search": {
      "post": {
        "operationId": "searchDocuments",
        "summary": "Search documents by metadata",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "properties": {
                  "collection": { "type": "string" },
                  "filterMeta": { "type": "object" },
                  "limit": { "type": "integer", "default": 10 }
                }
              }
            }
          }
        }
      }
    },
    "/v1/vector-search": {
      "post": {
        "operationId": "semanticSearch",
        "summary": "Semantic search using vector embeddings",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "properties": {
                  "collection": { "type": "string" },
                  "query": { "type": "string" },
                  "topK": { "type": "integer", "default": 5 }
                }
              }
            }
          }
        }
      }
    }
  }
}
```

**Note**: Your MDDB server must be publicly accessible for ChatGPT to reach it.

## Ollama (Python)

Use Ollama for local LLM inference with MDDB as the knowledge base.

```bash
pip install requests ollama
ollama pull llama3.2
```

```python
import requests
import ollama

MDDB_URL = "http://your-domain:11023"  # replace with your MDDB address

def search_mddb(query, collection="docs", top_k=5):
    resp = requests.post(f"{MDDB_URL}/v1/vector-search", json={
        "collection": collection, "query": query,
        "topK": top_k, "threshold": 0.6, "includeContent": True,
    })
    return resp.json().get("results", [])

def ask(question, collection="docs"):
    results = search_mddb(question, collection)
    context = "\n\n---\n\n".join(
        f"## {r['key']}\n{r.get('contentMd', '')[:2000]}" for r in results
    )
    response = ollama.chat(model="llama3.2", messages=[
        {"role": "user", "content": f"Answer using this context:\n{context}\n\nQuestion: {question}"},
    ])
    return response["message"]["content"]

print(ask("What is MDDB?"))
```

## DeepSeek

DeepSeek supports MCP via compatible clients (Cline, Continue, etc.).

Add the same MCP config as Claude Desktop to your MCP client settings:

```json
{
  "mcpServers": {
    "mddb": {
      "command": "docker",
      "args": [
        "run", "-i", "--rm", "--network", "host",
        "-v", "mddb-data:/app/data",
        "-e", "MDDB_MCP_STDIO=true",
        "tradik/mddb:latest"
      ]
    }
  }
}
```

Set DeepSeek as the LLM provider in your MCP client configuration.

## Manus

Add MDDB tools to your Manus agent configuration:

```yaml
name: mddb_search
description: "Search the MDDB knowledge base"
type: api
endpoint: "http://your-domain:11023/v1/vector-search"
method: POST
headers:
  Content-Type: "application/json"
body:
  collection: "docs"
  query: "{{input}}"
  topK: 5
  includeContent: true
```

## Bielik.ai

Bielik is a Polish LLM that can be connected to MDDB for RAG in Polish.

```bash
pip install requests
```

```python
import requests

MDDB_URL = "http://your-domain:11023"  # replace with your MDDB address
BIELIK_API_URL = "https://api.bielik.ai/v1"
BIELIK_API_KEY = "your-api-key"

def search_mddb(query, collection="docs"):
    resp = requests.post(f"{MDDB_URL}/v1/vector-search", json={
        "collection": collection, "query": query,
        "topK": 5, "threshold": 0.6, "includeContent": True,
    })
    return resp.json().get("results", [])

def ask(question, collection="docs"):
    results = search_mddb(question, collection)
    context = "\n\n".join(r.get("contentMd", "")[:2000] for r in results)
    resp = requests.post(f"{BIELIK_API_URL}/chat/completions", headers={
        "Authorization": f"Bearer {BIELIK_API_KEY}",
        "Content-Type": "application/json",
    }, json={
        "model": "Bielik-11B-v2.3-Instruct",
        "messages": [
            {"role": "system", "content": "Odpowiadaj korzystajac z kontekstu."},
            {"role": "user", "content": f"Kontekst:\n{context}\n\nPytanie: {question}"},
        ],
    })
    return resp.json()["choices"][0]["message"]["content"]

print(ask("Czym jest MDDB?"))
```

## Generic REST API Integration

Any LLM agent that supports HTTP tools can connect to MDDB. Key endpoints:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/search` | POST | Metadata search with pagination |
| `/v1/vector-search` | POST | Semantic/vector search |
| `/v1/fts` | POST | Full-text search (TF-IDF/BM25) |
| `/v1/get` | POST | Get specific document |
| `/v1/add` | POST | Add/update document |

See the [OpenAPI specification](/docs/api/swagger/) for complete API details.

## Panel Configuration

The MDDB Panel includes an **LLM Connections** page (sidebar → LLM Connections) with:
- **Server Domain** input field — set once, all configs update automatically
- Ready-to-use configuration templates for each agent
- Dynamic server address substitution
- Copy & Download buttons
- Setup instructions per agent

**[← Back to README](../README.md)**
