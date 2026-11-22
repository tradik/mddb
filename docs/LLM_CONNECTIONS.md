---
title: "LLM Connections"
slug: "docs/llm-connections"
description: "Connect MDDB to Claude, VS Code, Cursor, Windsurf, ChatGPT and Ollama for RAG and knowledge-base workflows through the built-in MCP server."
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

You can also change the domain directly in the Panel — the input field is above the config tabs on the LLM Connections page. The field takes a host name (`myserver.com`) or, since v2.19.9, a full URL (`https://myserver.com`, `https://myserver.com:8443`) for a server behind a TLS proxy or tunnel: then its scheme, host and port are used as they are, without the listen port. The page shows the MCP URL the examples use under the field.

### Public MCP URL (v2.19.9)

Behind a reverse proxy or tunnel the listener is reached at another scheme, host or port than it listens on. Set `MDDB_MCP_PUBLIC_URL` to the base URL clients dial, without `/mcp`:

| Env Var | Default | Example |
|---------|---------|---------|
| `MDDB_MCP_PUBLIC_URL` | _(unset: the OAuth issuer when OAuth is on, else empty)_ | `https://mcp.example.com` |

It must be an absolute `http` or `https` URL; a trailing slash is removed, and any other value is logged and ignored. `GET /v1/config` reports it as `protocols.mcp.publicUrl`, next to `apiKeyEnabled` and `oauthEnabled`, and the Panel then builds every MCP example on `<publicUrl>/mcp` instead of the Server Domain.

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

For MCP clients that support Streamable HTTP transport (the `.mcp.json` form; other clients name the transport differently, see their documentation):

```json
{
  "mcpServers": {
    "mddb": {
      "type": "http",
      "url": "http://localhost:9000/mcp"
    }
  }
}
```

With `MDDB_MCP_API_KEY_ENABLED=true` add `"headers": { "X-API-Key": "mcp_…" }`.

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

Set `MDDB_MCP_BUILTIN_TOOLS=false` to hide all built-in tools, exposing only custom YAML tools (to keep some groups, use `MDDB_MCP_TOOLSETS=search,code` — see [MCP.md](MCP.md#toolsets-v2188)):

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

Since 2.16.0 the same switch also writes one record per `tools/call`, with the
tool name, the `query` argument and whether the result carried any document
(`hits`, `found`). This is the line to read when asking what clients query
and which lookups come back empty — see [MCP.md](MCP.md#request-logging).
```json
{"time":"2026-10-11T18:54:33Z","level":"INFO","msg":"MCP tool call","tool":"version_check","query":"alloy","duration_ms":3,"hits":0,"found":false,"key_name":"claude-prod","client_ip":"1.2.3.4"}
```

## MCP Protocol Features

MDDB implements both MCP revisions in full — see [Protocol revisions](MCP-REVISIONS.md) for what each requires:

| Feature | Description |
|---------|-------------|
| **Tool Annotations** | All 114 built-in tools have `readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint` — enables AI clients to auto-approve safe tools |
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

MCP is built directly into the MDDB server — no separate service needed. The Panel's **LLM Connections → Claude** tab generates each of the configurations below for the server it is connected to, with the MCP URL resolved and, when MCP API keys are on, the key typed into its **API key** field (or one just created in the **API Keys** tab).

The first three connect to a running server. Replace `https://mcp.example.com/mcp` with your MCP URL (`<MDDB_MCP_PUBLIC_URL>/mcp`, or `http://<host>:9000/mcp`). Leave out the key when `MDDB_MCP_API_KEY_ENABLED` is off.

### Claude Code (command)

```bash
claude mcp add --transport http mddb https://mcp.example.com/mcp --header "X-API-Key: mcp_…"
```

`claude mcp list` then lists `mddb` as connected. The server is added for the current project only; add `--scope user` for all your projects, or `--scope project` to share it with the project through `.mcp.json`. A key or URL with shell characters is single-quoted by the Panel.

### Project file (`.mcp.json`)

Save in the root of the project:

```json
{
  "mcpServers": {
    "mddb": {
      "type": "http",
      "url": "https://mcp.example.com/mcp",
      "headers": {
        "X-API-Key": "${MDDB_MCP_API_KEY}"
      }
    }
  }
}
```

Do not commit a real key: reference an environment variable as above (Claude Code expands `${VAR}` in `url` and `headers`) and set `MDDB_MCP_API_KEY` in your environment.

### Custom connector (claude.ai, Claude Desktop)

A custom connector needs OAuth on the server (`MDDB_MCP_API_KEY_ENABLED=true`, `MDDB_MCP_OAUTH_ENABLED=true`, `MDDB_MCP_OAUTH_ISSUER`, `MDDB_MCP_OAUTH_SECRET`; see [MCP.md](MCP.md#oauth-21-for-claudeai-connectors-v2181)). Claude connects to the URL from Anthropic's servers, so it must be an `https` address reachable from the internet. Then:

1. Open **Customize → Connectors**, select **Add**, then **Add custom connector**.
2. Enter a name (for example `mddb`) and the MCP URL, then click **Continue**.
3. Keep the authentication settings marked **Detected** and click **Add**.
4. Click **Connect**; on the page the server opens, paste an MCP API key.

On Team and Enterprise plans an Owner adds the connector under **Organization settings → Connectors** (**Add**, then **Custom**), and members click **Connect**. When OAuth is off, the Panel's connector variant lists the missing settings instead of these steps.

### Local stdio (Docker)

This starts a separate local database on your computer; it does not connect to a running server. stdio suits one local client that owns its database file.

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

### Local stdio (binary)

Use the `mddbd` binary directly. Like the Docker variant, this starts a separate local database; it does not connect to a running server.

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

### Available MCP Tools (114)

Once connected, your LLM agent has access to all built-in tools (with annotations for auto-approve). They come in eleven toolsets; `MDDB_MCP_TOOLSETS` or a per-key `toolsets` list exposes only some of them ([MCP.md](MCP.md#toolsets-v2188)):

**search**
`search_documents` `aggregate` `semantic_search` `full_text_search` `hybrid_search` `cross_search` `autocomplete` `fts_languages` `search_advisor` `get_indexing_status` `run_eval` `add_eval_query` `get_gain` `find_duplicates` `classify_document` `get_meta_keys`

**code**
`code_graph` `code_symbol` `reference_search` `reference_symbol` `reference_examples` `reference_dependencies`

**documents**
`add_document` `update_document` `delete_document` `get_document_meta` `set_ttl` `add_documents_batch` `delete_documents_batch` `export_documents` `list_revisions` `restore_revision` `delete_where` `update_where` `set_ttl_where`

**ingest**
`import_url` `upload_file` `ingest_documents` `bulk_ingest_submit` `bulk_ingest_status` `bulk_ingest_list` `bulk_ingest_cancel`

**memory**
`memory_start_session` `memory_add_message` `memory_recall` `memory_summarize` `memory_list_sessions` `memory_session_history`

**schema**
`set_schema` `get_schema` `delete_schema` `list_schemas` `validate_document`

**admin**
`get_stats` `vector_stats` `vector_reindex` `fts_reindex` `create_backup` `restore_backup` `delete_collection` `truncate_revisions` `get_checksum` `list_synonyms` `add_synonym` `delete_synonym` `list_stopwords` `add_stopwords` `delete_stopwords` `get_collection_config` `set_collection_config` `list_collection_configs` `list_curation_rules` `create_curation_rule` `update_curation_rule` `delete_curation_rule` `corpus_list` `corpus_install` `corpus_remove` `chat_scenario_list` `chat_scenario_get` `chat_scenario_put` `chat_scenario_delete` `chat_skill_list` `chat_skill_get` `chat_skill_put` `chat_skill_delete` `chat_mcp_secret_list` `chat_mcp_secret_put` `chat_mcp_secret_delete` `feature_list` `feature_set` `chat_conversation_list` `chat_conversation_get`

**geo**
`geo_search` `geo_within` `geo_polygon` `geo_stats` `geo_encode` `geo_decode`

**automation**
`list_automation` `create_automation` `get_automation` `update_automation` `delete_automation` `test_automation` `get_automation_logs` `run_automation`

**webhooks**
`register_webhook` `list_webhooks` `delete_webhook`

**wordpress**
`wordpress_publish` `wordpress_set_status`

## VS Code (v2.19.10)

The chat built into VS Code (agent mode) uses MCP servers declared in
`.vscode/mcp.json`. The panel's *LLM Connections → VS Code* tab builds this
file for the server it shows:

```json
{
  "inputs": [
    { "type": "promptString", "id": "mddbMcpKey", "description": "MDDB MCP API key", "password": true }
  ],
  "servers": {
    "mddb": {
      "type": "http",
      "url": "https://db.example.com/mcp",
      "headers": { "X-API-Key": "${input:mddbMcpKey}" }
    }
  }
}
```

VS Code asks for the key on first use and keeps it in its secret storage, so
the file holds no key and can be committed. Without MCP API keys
(`MDDB_MCP_API_KEY_ENABLED` off) the `inputs` and `headers` are left out. To
add the server to every workspace instead, run
`code --add-mcp '{"name":"mddb","type":"http","url":"https://db.example.com/mcp","headers":{"X-API-Key":"mcp_…"}}'`;
this stores the key in the user profile. In the Chat view, switch to Agent
mode and pick the mddb tools under *Configure Tools*. A key bound to a chat
scenario or scoped to collections limits what the chat can read
([MCP.md](MCP.md)).

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

This starts a separate local database on your computer; it does not connect to a running server.

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

## Open WebUI

Open WebUI can use MDDB in several ways: as an external knowledge source
(two providers), as a function, as a model provider (a chat scenario), and as
its web search, reranker and document loader. The panel's **Open WebUI** tab
shows them with this server's addresses filled in.

### External Knowledge Source (no code)

MDDB serves the two OpenAI Vector Stores routes that Open WebUI's
**OpenAI-compatible** External Knowledge Source provider calls:
`GET /v1/vector_stores` and `POST /v1/vector_stores/{id}/search` (#446). A
vector store is an MDDB collection. Requests and responses are described in
[API.md](API.md#openai-compatible-vector-stores-v1vector_stores).

1. In Open WebUI: **Admin → Settings → Integrations → External Knowledge
   Sources → Add**.
2. **Provider:** OpenAI-compatible.
3. **Endpoint:** the MDDB HTTP API base URL plus `/v1`, for example
   `http://mddb.example.com:11023/v1`. Open WebUI must be able to reach it.
4. **API key:** an MDDB API key (`mddb_live_…`, `POST /v1/auth/api-key`) of
   a user with read permission on the collections to search. With auth off,
   the key is not checked.
5. List the vector stores, or enter collection names as vector store IDs.
   A tenant collection such as `acme/docs` is entered as it is; Open WebUI
   escapes the `/` and MDDB accepts it.
6. Run a test query.

What the server does with these calls:

- **The list** holds the collections the key's user may read. Read
  permission, the tenant namespace and MCP API key scope apply per
  collection. The description is the collection's configured description.
- **A search** uses hybrid search when the collection has embeddings and the
  server has an embedding provider, and full-text search otherwise. Each
  result is the passage around the document's best match. The source title is
  the document key and the link is its `url` metadata, when it has one.
- **Before the list**, Open WebUI calls `GET /v1/vector_store/list`, a route
  of another proxy's API. MDDB answers 404 and Open WebUI falls back to
  `/v1/vector_stores`.
- **Not served:** creating, changing or deleting stores and files through
  this API. Documents are written with `/v1/add` and the other endpoints.
  OpenAI `filters` are accepted and ignored.

### External Knowledge Source, Qdrant provider (#447)

Start mddbd with `MDDB_QDRANT_COMPAT=true`, then add an External Knowledge
Source with provider **Qdrant**: Endpoint `http://<mddb-host>:11023/qdrant`
(always include the port: the Qdrant client adds 6333 to a URL without one,
so behind TLS write `https://host:443/qdrant`), API Key an MDDB API key with
read on the collection, Collection the MDDB collection name, and leave
Content Field `payload.text`, Vector Field empty, Metadata Field
`payload.metadata` and Document ID Field `id` at their defaults. Open WebUI
embeds the question with its own embedding model, so it must use the same
model (and dimension) as MDDB; a different dimension is refused with a 400
naming both. See [QDRANT_COMPAT.md](QDRANT_COMPAT.md).

### Function (pipe)

The tab's `mddb_model.py` is an Open WebUI function: **Admin → Functions →
Add new function**, paste the script, save it, and set its Valves for your
LLM provider (`llmProvider`, `llmApiUrl`, `llmApiKey`).

### A chat scenario as a model (mddb-chat, #448)

`mddb-chat` can serve its chat scenarios over an OpenAI-compatible API, one
model per enabled scenario the key may read. Turn it on with
`[openai_api] enabled = true` (or `MDDB_CHAT_OPENAI_API_ENABLED=true`); mddbd
must run with `MDDB_AUTH_ENABLED=true`.

In Open WebUI, *Admin → Settings → Connections → OpenAI API*, add a connection:

| Field | Value |
|-------|-------|
| URL | `http://<mddb-chat host>:11030/v1` |
| Key | an mddbd API key (`mddb_live_…`, from `POST /v1/auth/api-key` as the user whose read permissions decide which scenarios appear) |
| Prefix ID (optional) | e.g. `mddb` — keeps scenario names apart from other connections' model ids |

Each answer runs the scenario exactly as the widget does: its composed prompt
and guardrails, its collections and tools, its LLM settings. Open WebUI's own
system prompt, tools and images are not passed on (images are refused with
400). Details and limits: [mddb-chat README](../services/mddb-chat/README.md#openai-compatible-api).

<!-- Open WebUI engines (#449-#451): begin -->
### Web search, reranker and document loader (#449–#451)

Open WebUI's admin settings can send web search, reranking and content
extraction to an external service. MDDB answers all three in Open WebUI's
shapes, each behind a Features module that starts off: `POST /v1/web-search`
(`webSearch`), `POST /v1/rerank` (`rerankApi`) and
`PUT /v1/extract/process` (`extractApi`, URL entered as `…/v1/extract`). The
settings, values and limits are in [OPENWEBUI.md](OPENWEBUI.md); the panel's
Open WebUI tab prints the URLs for the server it shows.
<!-- Open WebUI engines (#449-#451): end -->

## Generic REST API Integration

Any LLM agent that supports HTTP tools can connect to MDDB. Key endpoints:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/search` | POST | Metadata search with pagination |
| `/v1/vector-search` | POST | Semantic/vector search |
| `/v1/fts` | POST | Full-text search (TF-IDF/BM25) |
| `/v1/get` | POST | Get specific document |
| `/v1/add` | POST | Add/update document |
| `/v1/vector_stores`, `/v1/vector_stores/{id}/search` | GET, POST | OpenAI-compatible vector store list and search ([Open WebUI](#open-webui)) |

See the [REST API reference](/docs/api/reference/), generated from the OpenAPI specification, for complete API details.

## Panel Configuration

The MDDB Panel includes an **LLM Connections** page (sidebar → LLM Connections) with:
- **Server Domain** input field — a host name or a full URL; set once, all configs update automatically. The MCP URL the examples use is shown under it (`MDDB_MCP_PUBLIC_URL` when the server reports one)
- Ready-to-use configuration templates for each agent
- A **Claude** tab with five variants: Claude Code command (default), project `.mcp.json`, custom connector, local stdio (Docker) and local stdio (binary)
- An **API key** field on the Claude tab when MCP API keys are on: empty, the examples show the placeholder `mcp_…` and a **Create a key** link; a key created in the **API Keys** tab fills it. The key stays in the page only (not stored, not in the URL) and is gone when you leave the page
- Copy (exactly the shown variant) and Download (only for variants that are files)
- Setup instructions per agent; the **Open WebUI** tab has the External Knowledge Source steps with this server's `/v1` endpoint, then the function's
- **API Keys** tab — create, disable and delete MCP API keys; a new key takes an optional scope (collections, read-only, a chat scenario, v2.19.9) and the list shows each key's scope ([MCP.md](MCP.md#scoped-keys-collections-read-only-a-chat-scenario-v2199))

**[← Back to README](../README.md)**
