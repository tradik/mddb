---
title: "MCP Server Configuration"
slug: "docs/mcp"
description: "MDDB's built-in MCP server across both spec revisions: the full built-in tool catalogue, stdio and HTTP transports, API keys and access modes."
status: publish
---

# MCP Server Configuration

MDDB has a built-in MCP (Model Context Protocol) server implementing both the
stateless **2026-07-28** specification and the handshake-based **2025-11-25**,
side by side. This document covers all MCP configuration options.

**[→ Protocol revisions: what each one requires](MCP-REVISIONS.md)** | **[→ LLM client setup (Claude, Cursor, ChatGPT, Ollama)](LLM_CONNECTIONS.md)** | **[→ Custom YAML tools](CUSTOM-TOOLS.md)**

## Transports

| Transport | Endpoint | Spec Version | Status |
|-----------|----------|-------------|--------|
| **Stdio** | stdin/stdout | 2026-07-28 + 2025-11-25 | Default for Claude Desktop |
| **Streamable HTTP** | `POST /mcp` (plus `GET`/`DELETE` for handshake clients) | 2026-07-28 + 2025-11-25 | Recommended for remote |
| **SSE (legacy)** | `GET /sse` + `POST /message` | 2024-11-05 | Backward compatible |

`GET /mcp` and `DELETE /mcp` belong to the handshake era only; 2026-07-28
removed sessions and the standalone stream. Both stay available for the clients
that still use them.

All transports run on the MCP port (default: 9000, configurable via `MDDB_MCP_ADDR`).

The MCP port also answers plain JSON requests, for scripts and for checking a
deployment without an MCP client. They take the same credentials as the MCP
transports:

| Endpoint | What it does |
|---|---|
| `GET /tools` | The tool catalog, built-in and custom tools, as `{"tools": [...]}` |
| `POST /tools/call` | Run one tool: `{"name": "...", "arguments": {...}}`. Answers 403 when the HTTP mode is `read`, and 503 while writes wait for the freelist after a crash, whatever the tool |
| `GET /resources` | The resource catalog and resource templates |
| `POST /resources/read` | Read one resource: `{"uri": "mddb://..."}` |
| `GET /events` | The server-sent document change events, as `GET /v1/events` on the HTTP port |

## Protocol Revisions

MDDB serves two revisions at once, and **each request declares its own**: one
carrying `io.modelcontextprotocol/protocolVersion` in `params._meta` is served
statelessly under 2026-07-28, and one without it is served under 2025-11-25 as
before. A handshake client and a stateless client can share a port, a
connection, or a stdio process.

**[→ Full details of both revisions](MCP-REVISIONS.md)** — required `_meta`
fields, `server/discover`, the mirrored HTTP headers, caching hints, error
codes, and what 2026-07-28 removed.

`initialize` still negotiates for handshake clients, and only ever offers a
revision that has a handshake:

| Client sends in `initialize` | MDDB answers with |
|---|---|
| a handshake revision MDDB speaks | that revision |
| anything else (including 2026-07-28) | its own newest handshake revision, and the client decides whether to continue |

`GET /v1/config` reports the current state without a request, which cannot be
learned from a handshake — that answers for one client, and cannot show that a
pin is in force or what else would have been accepted:

```json
{
  "protocols": {
    "mcp": {
      "enabled": true,
      "addr": ":9000",
      "stdio": false,
      "revision": "2026-07-28",
      "supportedRevisions": ["2026-07-28", "2025-11-25"],
      "revisionPinned": false,
      "handshakeRevision": "2025-11-25"
    }
  }
}
```

### Pinning a revision

`MDDB_MCP_PROTOCOL_VERSION` forces one revision and disables negotiation
entirely. Set it when a client's own version handling is the thing that is
broken; leave it unset otherwise, because a pin also stops MDDB from serving
clients that ask for anything else.

```bash
MDDB_MCP_PROTOCOL_VERSION=2026-07-28
```

Pinning the stateless revision takes the handshake off the table: `initialize`
is then refused with `-32022` naming what the server does serve.

A pin naming a revision this build does not implement stops the server at
startup, listing what it can speak. Accepting it would produce a handshake that
succeeds and a session that then misbehaves, far from the cause.

### The MCP-Protocol-Version header

Required on every stateless POST, where it must also match the revision in the
body's `_meta` — a disagreement is `-32020`. For a handshake client the header
stays optional, and naming a revision MDDB cannot speak (or one that disagrees
with a pin) is refused with `400` and a body naming what would work.

```bash
curl -X POST http://localhost:9000/mcp \
  -H "MCP-Protocol-Version: 2024-01-01" -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
# {"error":"unsupported MCP revision 2024-01-01; this server speaks: 2026-07-28, 2025-11-25"}
```

### Streamable HTTP (Recommended)

A stateless client (2026-07-28) sends the call it wants, with no handshake
before it:

```bash
curl -X POST http://localhost:9000/mcp \
  -H "Content-Type: application/json" \
  -H "MCP-Protocol-Version: 2026-07-28" -H "Mcp-Method: tools/list" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{
        "io.modelcontextprotocol/protocolVersion":"2026-07-28",
        "io.modelcontextprotocol/clientCapabilities":{}}}}'
```

A handshake client (2025-11-25) opens as it always did:

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
| `MDDB_MCP_INSTRUCTIONS_FILE` | — | (v2.18.1) Path to a file holding the instructions, read at startup. For multi-paragraph text that cannot go into a compose `.env` file. `MDDB_MCP_INSTRUCTIONS` wins when both are set. |

### Tools

| Env Var | Default | Description |
|---------|---------|-------------|
| `MDDB_MCP_CONFIG` | — | Path to YAML file with custom tool definitions |
| `MDDB_MCP_BUILTIN_TOOLS` | `true` | Set to `false` to hide all 93 built-in tools (only custom tools exposed) |
| `MDDB_MCP_TOOLSETS` | `all` | Comma-separated toolsets to expose, e.g. `search,code` (v2.18.8). See [Toolsets](#toolsets-v2188) |
| `MDDB_MCP_CONFIRM_DESTRUCTIVE` | `off` | `off`, `elicit` or `require`: ask the user, through MCP elicitation, before a destructive tool runs (v2.18.9). See [Confirming Destructive Tools](#confirming-destructive-tools-v2189) |

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

### OAuth 2.1 for claude.ai connectors (v2.18.1)

Claude Code and the Claude API's MCP connector send an API key. claude.ai's
"Add custom connector" cannot: it supports OAuth or no authentication at all.
With OAuth on, the MCP listener is its own minimal authorization server, as
the MCP authorization spec describes:

- `GET /.well-known/oauth-protected-resource` and
  `GET /.well-known/oauth-authorization-server` — discovery metadata
  (RFC 9728, RFC 8414). A request to `/mcp` without credentials gets a 401
  whose `WWW-Authenticate` header points at the first one.
- `POST /oauth/register` — dynamic client registration (RFC 7591). Public
  clients only; a redirect URI must be in `MDDB_MCP_OAUTH_REDIRECT_URIS` or an
  `http://localhost` / `127.0.0.1` / `[::1]` URI on any port.
- `GET|POST /oauth/authorize` — authorization code with PKCE (S256 only). The
  consent page asks for an **MCP API key**: mddb has no user accounts, so the
  key is what identifies the person authorizing.
- `POST /oauth/token` — `authorization_code` and `refresh_token` grants.

Every token stands for the API key entered on the consent page, and the
listener checks it as that key. A token can do what its key can; deleting or
disabling the key revokes its tokens within `MDDB_MCP_API_KEY_CACHE_TTL`, and
refreshing fails from then on. API keys keep working beside OAuth.

```bash
MDDB_MCP_API_KEY_ENABLED=true            # required: tokens stand for keys
MDDB_MCP_OAUTH_ENABLED=true
MDDB_MCP_OAUTH_ISSUER=https://mcp.example.com    # the listener's public URL
MDDB_MCP_OAUTH_SECRET=<32+ random characters>    # e.g. openssl rand -base64 48
```

Then add `https://mcp.example.com/mcp` as a custom connector in claude.ai,
and paste an MCP API key on the page that opens.

Tokens and client IDs are sealed with AES-256-GCM under
`MDDB_MCP_OAUTH_SECRET` and stored nowhere, so they survive restarts and work
on every replica that shares the secret. Without the secret a random one is
used and every token dies with the process. Authorization codes live in memory
for one minute and are single-use. Refresh tokens are not rotated server-side
(nothing is stored to rotate); they expire after
`MDDB_MCP_OAUTH_REFRESH_TTL`. Expose the listener over HTTPS — through
cloudflared or a reverse proxy — since the issuer must be an `https` URL.

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

Two records exist, both behind the same switch.

**Request record** — one line per HTTP request on the MCP listener:

```json
{"timestamp":"2026-03-26T10:30:00Z","method":"POST","path":"/mcp","status":200,"duration_ms":45,"client_ip":"1.2.3.4","key_name":"claude-prod","session_id":"abc123","user_agent":"claude-code/1.0"}
```

**Tool-call record** (2.16.0) — one line per `tools/call`, on every transport
(Streamable HTTP, SSE, `/tools/call`, stdio). Every tool call is a
`POST /mcp` with status 200, so the request record cannot tell a lookup that
found something from one that did not. This record can:

```json
{"time":"2026-10-11T18:54:33Z","level":"INFO","msg":"MCP tool call","tool":"version_check","query":"alloy","duration_ms":3,"hits":0,"found":false,"key_name":"claude-prod","client_ip":"1.2.3.4","session_id":"abc123"}
```

| Field | Meaning |
|---|---|
| `tool` | Tool name as the client sent it (built-in or custom) |
| `query` | The `query` argument, cut to 200 characters; empty when the tool has none |
| `hits`, `found` | Documents in the result (`total`, or the `results`/`documents` array). Absent when the result is not a search |
| `key_name`, `client_ip`, `session_id` | Caller, when the HTTP middleware resolved one; absent on stdio |
| `error` | The failure, logged at `WARN` instead of `INFO` |

Set `MDDB_LOG_FORMAT=json` so a log shipper (Alloy, Promtail, Vector) can
parse the fields with `| json`. A dashboard of "what do people ask for, and
what do we not have?" is `sum by (query) (count_over_time({...} | json |
msg="MCP tool call" | found="false" [24h]))`.

Three counters land in `/metrics` whether or not the log is on (see
[TELEMETRY.md](TELEMETRY.md#mcp-tool-calls)):
`mddb_operations_total{operation="mcp_tool_call"|"mcp_tool_miss"|"mcp_tool_error",label="<tool>"}`.

## Agent Instructions

The tool schemas say what each tool accepts; they do not say which one fits a
question, or that asking for a projection instead of whole documents changes
the cost by more than an order of magnitude. Measured on this engine, the same
`full_text_search` over five 12 KB documents returns ~19 700 tokens by default
and ~327 with `fields: ["key"]`.

[`integrations/agent-instructions/`](https://github.com/tradik/mddb/tree/main/integrations/agent-instructions)
ships that guidance in the formats agents read:

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

## Narrowing a Search (v2.18.8+)

`search_documents`, `semantic_search`, `full_text_search`, `hybrid_search`,
`cross_search` and `aggregate` take `filter` next to `filter_meta`. One
condition per metadata key, with the operators `in`, `prefix`, `contains`,
`gt`, `gte`, `lt`, `lte`, `not` and `exists`:

```json
{"name": "full_text_search", "arguments": {
  "collection": "repo", "query": "retry",
  "filter": {"path": {"prefix": "internal/"}, "kind": {"not": ["test"]}}
}}
```

The filter is evaluated on the metadata index before scoring, so narrowing to
one directory costs nothing extra. An unknown operator is an error naming it.
Full semantics: [Filter Operators](SEARCH.md#filter-operators-v2188).

`semantic_search`, `full_text_search`, `hybrid_search` and `cross_search` also
take `group_by`, which keeps at most `per_group` results for each value of a
key — one passage per file instead of five from the same one:

```json
{"name": "semantic_search", "arguments": {
  "collection": "repo", "query": "retry with backoff", "retrieval_mode": "chunk",
  "group_by": {"key": "path", "per_group": 1}
}}
```

Each result reports its `group` and `groupRank`. See
[Grouping Results](SEARCH.md#grouping-results-v2188).

## Letting the Server Choose the Index (v2.18.9+)

`semantic_search` and `cross_search` take `algorithm: "auto"`, and
`hybrid_search` takes `vector_algorithm: "auto"`. With it, the query planner
chooses the vector index for each query. It goes by the collection size, the
indexes already built and how much of the collection the filter admits.
`explain: true` adds a `plan` to the response: the index used, whether the
filter was applied first, and why. `latency_target_ms` is optional; 10 or
less favours a quantized index on a large collection.

```json
{"name": "semantic_search", "arguments": {
  "collection": "repo", "query": "retry with backoff", "algorithm": "auto",
  "filter": {"path": {"prefix": "internal/net/"}}, "explain": true
}}
```

Send `auto` rather than guessing an index. Without `algorithm`, the search is
flat, as before. The table behind the choice is in
[`auto`: the server chooses](SEARCH.md#auto-the-server-chooses-v2189).

## Telling the Search Which Results Were Right (v2.18.9+)

`semantic_search`, `hybrid_search` and `cross_search` take `positive` and
`negative`. These are the keys of results from an earlier call (vectors work
too). The second search moves towards the positives and away from the
negatives. You don't have to rewrite the query:

```json
{"name": "semantic_search", "arguments": {
  "collection": "kb", "query": "rotate the signing key",
  "positive": ["key-rotation"], "negative": ["tls-certificates"]
}}
```

`feedback_mode` is `query` (default) or `rescore`. `query` moves the query
vector (Rocchio; weights in `rocchio: {alpha, beta, gamma}`). `rescore`
re-ranks the candidates by similarity to the examples, with `feedback_weight`
(default 0.5). On `hybrid_search` only the vector half changes. With
`explain: true`, `plan.feedback` shows the correction. See
[Relevance Feedback](SEARCH.md#relevance-feedback--positive-and-negative-examples-v2189).

### Query decomposition (v2.18.10+)

`semantic_search` and `hybrid_search` take `query_decomposition`: `off`
(default) or `auto`. With `auto`, a question with several needs is also
searched clause by clause (at most 4 clauses), and each document keeps its
best score:

```json
{"name": "semantic_search", "arguments": {
  "collection": "kb", "query": "set up a read replica and take a backup of the database",
  "query_decomposition": "auto", "explain": true
}}
```

Ask one combined question instead of two searches. Each clause costs one
embedding call. On `hybrid_search` only the vector half is split. With
`explain: true`, `plan.decomposition` gives the parts. See
[Query Decomposition](SEARCH.md#query-decomposition--questions-with-several-needs-v21810).

## Result Limits (v2.18.10+)

Every tool that takes a result count — `limit`, `top_k` or `top_n` on
`full_text_search`, `search_documents`, `autocomplete`, `semantic_search`,
`hybrid_search`, `cross_search`, `code_symbol` and `geo_search` — returns at
most `MDDB_SEARCH_MAX_LIMIT` results (default 1000), or the collection's
`retrieval.maxTopK` when that is lower. A capped call still succeeds; its JSON
text adds `"limit"` (the count used) and `"limitCapped": true`, and the tool
result also carries them as data:

```json
{"content": [{"type": "text", "text": "{ ... \"limit\": 1000, \"limitCapped\": true }"}],
 "structuredContent": {"limitCapped": true, "limit": 1000}}
```

A call under the cap has no `structuredContent` and its text is unchanged.
See [Result Limits](SEARCH.md#result-limits-v21810).

## Passages for Code (v2.18.8+)

`search_documents`, `full_text_search`, `semantic_search`, `hybrid_search` and
`cross_search` take `format: "passages"` (and `passage_lines`). Each hit then
comes back as a path, a line range, the definition holding it and those lines,
instead of the document:

```json
{"name": "full_text_search", "arguments": {
  "collection": "theme", "query": "background", "limit": 1, "format": "passages"
}}
```

For code this replaces "search, then read the file" with one call; see
[Passages — What an Agent Gets](SEARCH.md#passages--what-an-agent-gets-v2188).

## Toolsets (v2.18.8+)

Every tool's schema sits in the model's context on every turn, and a long menu
makes the choice worse as well as more expensive. Each built-in tool belongs
to one toolset, and a server or a key can expose only the toolsets it needs:

| Toolset | Tools |
|---|---|
| `search` | `search_documents`, `aggregate`, `semantic_search`, `full_text_search`, `hybrid_search`, `cross_search`, `autocomplete`, `fts_languages`, `search_advisor`, `get_indexing_status`, `run_eval`, `add_eval_query`, `get_gain`, `find_duplicates`, `classify_document`, `get_meta_keys` |
| `code` | `code_graph`, `code_symbol`, `reference_search`, `reference_symbol`, `reference_examples`, `reference_dependencies` |
| `documents` | `add_document`, `update_document`, `delete_document`, `get_document_meta`, `set_ttl`, `add_documents_batch`, `delete_documents_batch`, `export_documents`, `list_revisions`, `restore_revision`, `delete_where`, `update_where`, `set_ttl_where` |
| `ingest` | `import_url`, `upload_file`, `ingest_documents`, `bulk_ingest_*` |
| `memory` | `memory_*` |
| `schema` | `set_schema`, `get_schema`, `delete_schema`, `list_schemas`, `validate_document` |
| `admin` | stats, reindexing, backups, collection deletion and configs, synonyms, stopwords, curation rules |
| `geo`, `automation`, `webhooks`, `wordpress` | the tools of that area |

- **Server:** `MDDB_MCP_TOOLSETS=search,code` (default `all`).
  `MDDB_MCP_BUILTIN_TOOLS=false` still hides every built-in tool.
- **Per key:** `POST /v1/mcp/keys` with `"toolsets": ["search", "code"]`, or a
  third field in a static key: `MDDB_MCP_API_KEYS=key:name:search|code`. A key
  sees the intersection of its toolsets and the server's.
- A tool outside the caller's toolsets is not listed, and calling it anyway is
  answered with JSON-RPC `-32601` — the list is not the authorisation.
- Custom tools are never filtered: they are the operator's explicit choice.

Measured on this build, the `tools/list` schemas of all 90 tools take about
78 KB (≈19 600 tokens) per turn. The 22 tools of `search,code` take about 36 KB
(≈8 900 tokens).

| Agent | Toolsets |
|---|---|
| Reads and reviews code | `search,code` |
| Answers from a knowledge base | `search` |
| Keeps notes across sessions | `search,memory,documents` |
| Imports content | `ingest,documents,search` |
| Operates the server | `admin` (with a key of its own) |

## Searching What You Just Wrote (v2.18.8+)

A write returns once the document's full text and metadata are indexed, so
`full_text_search` finds it at once. Its embedding is computed afterwards by a
background worker, so `semantic_search` finds it a moment later. Three ways to
handle the gap, cheapest first:

1. Pass `wait_for_index` (ms, up to 30000) and `wait_for_keys` with the keys
   you just wrote to `semantic_search`, `hybrid_search` or `cross_search`. The
   search starts as soon as those embeddings land; if they do not land in time
   it runs anyway and reports `indexing.timedOut`.
2. Pass `read_level: "indexedPlusPending"` to `semantic_search` to also get
   still-queued documents that match by text, marked `pendingEmbedding`.
   `hybrid_search` always includes them and marks them the same way.
3. Call `get_indexing_status` to see the queue.

`add_document` reports `indexing.embeddingQueued` when it queued an embedding.

## Images of the Documents You Find (v2.18.9+)

Pass `include_images: true` to `full_text_search`, `semantic_search`,
`hybrid_search` or `cross_search` and each hit carries `images`: its image
documents with `altText`, `caption`, `page` and the `url` of the bytes. Alt
text and captions are searchable text, so an image is also a hit of its own.
With a provider that embeds images, `semantic_search` and `hybrid_search`
also return `imageHits` (pictures matching the query by what they show), and
`semantic_search` takes `query_image` to search by a picture. Images are
stored when a write asks for them: `extract_images` on `upload_file` and
`ingest_documents`. See [SEARCH.md](SEARCH.md#images--alt-text-captions-and-pictures-v2189).

## Built-in Tool Catalog

All 93 built-in tools, grouped by area. Tool inputs are self-describing via
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
| `delete_where` | (v2.18.8) Delete every document a metadata `filter`/`filter_meta` matches, through the normal delete. Destructive: run with `dry_run` first. `limit` (default 1000) and `cursor` page through large sets. |
| `update_where` | (v2.18.8) Merge `meta` into every document a filter matches; content is untouched. |
| `set_ttl_where` | (v2.18.8) Set the time to live (`ttl` seconds, 0 removes it) of every document a filter matches. |
| `get_meta_keys` | List all unique metadata keys and their values for a collection. |
| `get_checksum` | Get a CRC32 checksum for a collection that changes when documents are modified. |
| `ingest_documents` | Bulk ingest documents with URL-based key derivation, YAML frontmatter extraction, content deduplication, and automatic metadata injection. Since v2.18.9 a document may carry a file (`format`, `content_base64`) that the server converts, and options take `text_only`; options `extract_images` / `fetch_remote_images` and a document's `images` (`[{path, content_base64}]`) store its images as image documents ([images](SEARCH.md#images--alt-text-captions-and-pictures-v2189)). |
| `upload_file` | Upload a file and convert it to markdown. Since v2.18.9: `text_only`; xlsx, ods, csv, tsv, pptx, odp, epub, eml and mbox; the result carries `conversionConfidence`, `conversionWarnings` and `parts` (the keys of an email's attachments or a mailbox's messages, stored with `meta.parent`); `extract_images`, `fetch_remote_images` and `images` store a Markdown file's or PDF's images as image documents, listed in `parts`. |

### Search

| Tool | Description |
|------|-------------|
| `aggregate` | Compute metadata facets (value counts) and date histograms over a collection, with optional metadata pre-filtering. |
| `semantic_search` | Search documents by meaning using semantic similarity. Since v2.18.8: `filter`, `group_by`, and `read_level` / `wait_for_index` / `wait_for_keys` to see documents whose embedding is still queued. Since v2.18.9: `algorithm: "auto"`, `explain`, `latency_target_ms`, relevance feedback (`positive`, `negative`, `feedback_mode`, `feedback_weight`, `rocchio`), `include_images` (each hit's images, and `imageHits` with an image-capable provider) and `query_image` (search by an image; `query` may then be omitted). Since v2.18.10: `query_decomposition` (`off`/`auto`). |
| `get_indexing_status` | (v2.18.8) Is what was just written searchable yet? Queued and failed embeddings, progress and the oldest queued job, for a collection or the server. Read-only. |
| `run_eval` | (v2.18.8) Measure search configurations on the collection's evaluation set: recall@k, nDCG@k, MRR, p50/p99 latency and tokens per configuration. Read-only. See [Measuring Instead of Guessing](SEARCH.md#measuring-instead-of-guessing-v2188). Since v2.18.9 a configuration takes `summaryMode` (`off`, `blend`, `expand`) to measure [summaries](SEARCH.md#summaries--a-second-representation-v2189). |
| `get_gain` | (v2.18.8) What retrieval saved over a window of days: searches by type and algorithm, tokens returned and tokens avoided by passages and chunks. Counts only. Read-only. |
| `add_eval_query` | (v2.18.8) Add a query and the keys of the documents that should answer it, optionally graded 0–3, to the collection's evaluation set. |
| `full_text_search` | Search documents by text content using full-text search with term matching and relevance scoring. Since v2.18.1 it runs the same search as `POST /v1/fts` and takes `filter_meta`, `disable_stem`, `disable_synonyms`, `mode`, `distance` and `facet_by` (the last was in the schema before and ignored). Since v2.18.8 it also takes `filter` and `group_by` (see [Narrowing a Search](#narrowing-a-search-v2188)). Since v2.18.9 `include_images` attaches each hit's images. |
| `autocomplete` | Prefix autocomplete — returns top-N terms starting with the query, ranked by document frequency. |
| `classify_document` | Zero-shot document classification. |
| `hybrid_search` | Combined sparse (FTS) + dense (vector) search with alpha blending or Reciprocal Rank Fusion. Since v2.18.9: `vector_algorithm: "auto"`, `explain`, `latency_target_ms`, and relevance feedback on the vector half (`positive`, `negative`, `feedback_mode`, `feedback_weight`, `rocchio`), and `include_images`. Since v2.18.10: `query_decomposition` (`off`/`auto`, vector half only). |
| `cross_search` | Search across multiple collections using a source document's embedding or a text query. Since v2.18.8 `search_type` is `vector`, `fts` or `hybrid`, and `role:reference` in `target_collections` means every reference collection. Since v2.18.9: `algorithm: "auto"` plans each target; `explain`, `latency_target_ms`, relevance feedback (`positive`, `negative`, `feedback_mode`, `feedback_weight`, `rocchio`), and `include_images` (each hit's images from its own collection). |
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
| `update_document` | Partially update a document. Since v2.18.8 `expected_revision` makes it conditional: pass the `revision` from a previous read or write, and a concurrent change is reported instead of overwritten. |
| `get_document_meta` | Get document metadata without content. |
| `reference_search` | (v2.18.8) Search reference code — other projects indexed with `mddb-cli reference add` — hybrid, as passages. See [REFERENCE-CODING.md](REFERENCE-CODING.md). |
| `reference_symbol` | (v2.18.8) Where reference code defines and uses a symbol. |
| `reference_examples` | (v2.18.8) The definitions in reference code that call a symbol, with their text. |
| `reference_dependencies` | (v2.18.8) What a file in a reference collection imports, or what imports it. |
| `code_symbol` | (v2.18.8) Everything about one symbol as path + line passages: definitions with their span, references with the definition each sits in, callers, callees, tests that touch it and siblings in the same directory. Read-only. |
| `code_graph` | Code connection graph: what a document depends on and what depends on it: which stylesheet declares a selector, which pages load a script, which file defines a function this one calls (C, C++, assembly, Go, Rust, Java, Python, Ruby, PHP), which manifest declares the ConfigMap a Deployment refers to (Kubernetes, OpenShift, Kustomize, Terraform, Ansible). Edge kinds: `uses-selector`, `calls`, `references`, `imports`. |
| `delete_collection` | Delete an entire collection and all its documents, revisions, and metadata indices. |
| `get_collection_config` | Get configuration attributes for a collection (type, description, icon, color, custom metadata). |
| `set_collection_config` | Set or update configuration attributes for a collection. Only the arguments sent change (v2.18.0; before, an update erased the icon, colour, custom meta and revision cap it did not mention). `code.include_paths` sets where C `#include <...>` resolves (#319). `embedding.chunk_size` / `embedding.chunk_mode` set the collection's chunking for embeddings (v2.18.1, #325). `fts.enabled: false` stops full-text postings for the collection; `fts_reindex` then removes existing ones (v2.18.2, #335). `fts.exclude_meta_keys` keeps meta keys used only as filters out of full-text search, so changing them costs no FTS work; run `fts_reindex` after changing the list (v2.18.10, #367). `quota` (`max_documents`, `max_bytes`, `max_vectors`, `embedding_calls_per_day`, `embedding_tokens_per_day`; `{}` removes it) caps the collection — global admin only when auth is on (v2.18.9, GO-043, see [MULTI_TENANCY.md](MULTI_TENANCY.md#quotas-v2189)). A write refused by a quota is a tool error whose `structuredContent` carries `{code: "QUOTA_EXCEEDED", status, resource, limit, used}`. |
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
- Custom tools → **allowed**: every action a custom tool can wrap is read-only (`semantic_search`, `search_documents`, `full_text_search`, `hybrid_search`, `code_graph`, `aggregate`, `fts_languages`; see [CUSTOM-TOOLS.md](CUSTOM-TOOLS.md#supported-actions))

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

## Confirming Destructive Tools (v2.18.9+)

`destructiveHint` is advice to the client, and a client may ignore it. With
`MDDB_MCP_CONFIRM_DESTRUCTIVE` set, MDDB asks for itself: before a destructive
tool runs, it sends the client an elicitation, which the client shows its user
as a question. Nothing happens unless the answer is yes.

| Value | Client declared `elicitation` | Client did not |
|---|---|---|
| `off` (default) | runs as before | runs as before |
| `elicit` | asked first | runs as before |
| `require` | asked first | refused with `-32021` (`MissingRequiredClientCapability`, `data.requiredCapabilities.elicitation`) |

**Gated tools** are the built-in tools annotated destructive and not
idempotent: `delete_collection`, `delete_documents_batch`, `delete_where`,
`truncate_revisions` and `restore_backup`. Deleting one named object
(`delete_document`, `delete_schema`, …) is not gated. The read-only mode check
comes first: a read-only server refuses these tools without asking.

**The question** asks for `confirm: true`. `delete_collection` also asks for
the collection's name to be typed, so a yes clicked out of habit is not enough:

```json
{
  "message": "An MCP client asked MDDB to run delete_collection with {\"collection\":\"docs\"}. This cannot be undone. Allow it?",
  "requestedSchema": {
    "type": "object",
    "properties": {
      "confirm": {"type": "boolean", "title": "Allow delete_collection"},
      "collection": {"type": "string", "title": "Collection name"}
    },
    "required": ["confirm", "collection"]
  }
}
```

Declining, cancelling, a wrong name, an error from the client, the client going
away, or no answer within 5 minutes all refuse the call with a tool error that
ends "nothing was changed".

**How it is asked** depends on the request's protocol revision:

| Revision | Mechanism |
|---|---|
| 2026-07-28 | The call returns an `InputRequiredResult` (`resultType: "input_required"`) whose `inputRequests.confirm` is the `elicitation/create` request, plus a `requestState`. The client asks its user and repeats the same call with `inputResponses.confirm` and the echoed `requestState`. The state is a hash of the tool and its arguments, so an answer cannot be reused for a different call. See [MCP-REVISIONS.md](MCP-REVISIONS.md#asking-the-client-multi-round-trip-requests). |
| 2025-11-25 over stdio | MDDB writes an `elicitation/create` request to stdout mid-call and waits for the client's response on stdin. |
| 2025-11-25 over Streamable HTTP | The `tools/call` POST is answered as an SSE stream: first the `elicitation/create` request, then — after the client POSTs its response to `/mcp` with the same `Mcp-Session-Id` (answered `202`) — the tool's result. Without a question the response stays plain JSON. |
| The SSE transport (`/sse`) and the REST `/mcp/tools/call` | Cannot ask: served as a client without `elicitation`. |

Whether a handshake client can be asked is read from the `capabilities` it sent
in `initialize`; a stateless client declares it per request in
`io.modelcontextprotocol/clientCapabilities`.

The default stays `off` throughout 2.18.x and 2.19.x so existing deployments see
no change; `elicit` is the intended future default.

## Protocol Features

| Feature | Description |
|---------|-------------|
| **Tool Annotations** | `readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint` on all tools |
| **Elicitation** (v2.18.9+) | Destructive tools ask the user to confirm when `MDDB_MCP_CONFIRM_DESTRUCTIVE` is set — `InputRequiredResult` for 2026-07-28, `elicitation/create` for 2025-11-25. See [Confirming Destructive Tools](#confirming-destructive-tools-v2189) |
| **Structured Output** | `outputSchema` on 9 key tools for client-side validation |
| **5 Prompts** | `analyze-collection`, `search-help`, `summarize-collection`, `import-guide`, `rag-pipeline` |
| **Completion** | Autocomplete for collection names and prompt arguments |
| **Logging** | RFC 5424 levels (debug → emergency): `logging/setLevel` for handshake clients, `io.modelcontextprotocol/logLevel` per request for stateless ones |
| **Progress Tokens** | `notifications/progress` for long-running operations |
| **Cursor Pagination** | `tools/list` and `resources/list` |
| **Resource Templates** | `resources/templates/list` — the patterns (`mddb://{collection}/{key}`) live here rather than among the readable resources |
| **Discovery** (2026-07-28) | `server/discover` — supported revisions, capabilities and identity in one call |
| **Caching Hints** (2026-07-28) | `ttlMs` + `cacheScope` on every list and read |
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

### Custom prompts (v2.18.1)

Prompts of your own go in the `MDDB_MCP_CONFIG` file, next to
`custom_tools`. They are listed by `prompts/list` after the built-in ones,
and `prompts/get` renders them by substituting `{{argument}}`, the same
substitution `responsePrompt` and automations use:

```yaml
custom_prompts:
  - name: review_function
    description: "Review one function for a given category"
    arguments:
      - { name: function, required: true }
      - { name: category, required: true }
    template: |
      Review `{{function}}` for {{category}}: fetch it with codeFunctionFind,
      then similar changes with codeChangesSimilar, then report.
```

A missing required argument is an error. A missing optional one becomes
empty. A name that clashes with a built-in prompt or another custom prompt,
an unnamed argument, or a prompt without a template makes the set invalid:
it is logged at startup and no custom prompts are served.

---

**[← Back to README](../README.md)** | **[→ LLM Connections](LLM_CONNECTIONS.md)** | **[→ Custom Tools](CUSTOM-TOOLS.md)**
