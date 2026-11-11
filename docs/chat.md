---
title: "MDDB Chat Widget"
slug: "docs/chat"
description: "Embed an AI chat widget on any site with a single script tag - RAG answers from your MDDB knowledge base via OpenAI, Claude, Ollama, Groq or Mistral."
status: publish
---

# MDDB Chat Widget

Embed an AI-powered chatbot on any website with a single script tag. RAG-powered answers from your MDDB knowledge base. Supports OpenAI, Claude, Ollama, Bielik, Groq, Mistral, and more.



## Features

- **RAG-Powered** — answers from your MDDB knowledge base via hybrid BM25 + vector search
- **10+ LLM Providers** — OpenAI, Claude, Ollama, Bielik, Groq, Mistral, DeepSeek, Gemini
- **WebSocket Streaming** — real-time streaming responses with typing indicators
- **Shadow DOM** — fully isolated CSS, no conflicts with your site's styles
- **Session Persistence** — localStorage-based session continuity across page loads
- **< 30KB gzipped** — lightweight IIFE bundle, zero framework dependencies
- **Queue System** — graceful handling of multiple concurrent users

## Architecture

Three components work together:

| Component | Description | Port |
|-----------|-------------|------|
| `mddb-chat-widget` | TypeScript widget (Shadow DOM, streaming) | CDN / self-hosted |
| `mddb-chat` | Rust/Axum WebSocket server, tool-calling loop | 11030 |
| `mddbd` | MDDB knowledge base, hybrid search | 11023 (HTTP), 11024 (gRPC) |

```
Your Website                    MDDB Infrastructure
     |                                   |
<script>                         +---------------+
mddb-chat.min.js                 |     mddbd     |
     |                           |  :11024 gRPC  |
     | WebSocket                 +-------^-------+
     v                                   |
+-----------+  gRPC                     |
| mddb-chat |---------------------------+
|   :11030  |
|  (Rust)   |---- HTTP ---> LLM Provider
+-----------+           (OpenAI / Claude / Ollama / ...)
```

## Quick Start

### Docker Compose (Recommended)

```bash
# Clone the repository
git clone https://github.com/tradik/mddb.git
cd mddb

# Start MDDB + chat server + widget server
make docker-up

# Services:
# MDDB HTTP:   http://localhost:11023
# MDDB gRPC:   localhost:11024
# Chat server: ws://localhost:11030/ws
# Widget demo: http://localhost:11032
```

### Embed on Your Website

```html
<!-- Add to your HTML before </body> -->
<script>
  window.MDDB_CHAT_CONFIG = {
    serverUrl: 'ws://localhost:11030/ws',
    title: 'Documentation Assistant',
    primaryColor: '#2563eb'
  };
</script>
<script src="http://localhost:11032/mddb-chat.min.js" defer></script>
```

### Self-Hosted Widget

```bash
# Build the widget
cd services/mddb-chat-widget
npm install
npm run build
# Output: dist/mddb-chat.min.js
```

## Configuration

### Chat Server Environment Variables

The chat server (`services/mddb-chat`) is configured by its TOML file, see
`services/mddb-chat/config.example.toml`. It reads six environment variables;
the five secrets apply only when the matching TOML value is empty:

```bash
MDDB_CHAT_CONFIG=config.toml         # path of the TOML file (default: config.toml)
MDDB_CHAT_LLM_API_KEY=sk-...         # [llm] api_key (not needed for Ollama)
MDDB_CHAT_WEBHOOK_SECRET=            # [security] webhook_secret

# Auth to mddbd (SEC-007): keep these OUT of config.toml (it is gitignored).
# In docker-compose.dev.yml they default to the mddbd admin credentials from
# your .env (copy .env.example to .env first).
MDDB_CHAT_AUTH_USERNAME=admin        # [mddb] auth_username
MDDB_CHAT_AUTH_PASSWORD=             # [mddb] auth_password; never commit a real password
MDDB_CHAT_IDENTITY_SECRET=           # [identity] secret (2.19.0): signs join tokens of signed-in users
```

Everything else is a TOML key, not an environment variable:

| Setting | TOML key | Example default |
|---------|----------|-----------------|
| LLM provider | `[llm] provider` | `"openai"` (`openai`, `anthropic`, or any OpenAI-compatible service) |
| LLM model | `[llm] model` | `"gpt-4o-mini"` |
| LLM base URL | `[llm] api_url` | `"https://api.openai.com/v1"` |
| MDDB gRPC address | `[mddb] grpc_addr` | `"http://localhost:11024"` |
| MDDB HTTP address (2.19.0) | `[mddb] http_url` | derived from `grpc_addr` only when its port is `11024` (→ `11023`); required for any other port when `auth_username` is set |
| Default collection | `[mddb] default_collection` | `"docs"` |
| Result size (2.19.0) | `[mddb] result_max_chars` | `800` (characters of each search result the model sees; `0` = whole chunk) |
| Listen port | `[server] port` | `11030` |
| Concurrent sessions | `[session] max_concurrent` | `2` |
| Messages per minute | `[security] rate_limit_per_minute` | `30` |
| Webhook events (2.19.0) | `[[webhooks]] events` | `["session.start", "session.end", "queue.full"]` when omitted; add `"message"`, `"answer"`, `"feedback"` to log questions, answers and ratings |

Since 2.19.0 a scenario's `allowed_collections` is enforced: a tool call naming another collection is refused. Each answer ends with a `done` frame carrying `message_id`, which a `feedback` message can name. Payloads of the webhook events are in [`services/mddb-chat/README.md`](../services/mddb-chat/README.md#webhooks).

### Sessions, reconnects and startup (2.19.0)

- A closed WebSocket detaches the session; it keeps its history until `end` or `[session] session_ttl_minutes` of inactivity, and `resume { session_id }` reattaches it. A detached session does not hold a `max_concurrent` seat.
- `mddb-chat` starts before mddbd is reachable and retries the connection (`[mddb] connect_retry_max_seconds`, default `30`, `0` = exit at once). `/health` answers 503 with `mddb_connected: false` until then.
- `[llm] extra_body = { reasoning_effort = "none" }` adds fields to every OpenAI-compatible request, for thinking models that otherwise return an empty answer.
- Scenarios can live in mddbd and be edited in the panel's Chat page (`/v1/chat/scenarios`); the TOML `[scenarios]` stay as bootstrap and fallback.

### Scenarios from mddbd (2.19.0)

`mddb-chat` loads the scenarios stored in mddbd as soon as it is connected and looks at the store's version every `[scenarios_source] poll_seconds` (default `10`; `0` turns it off). When the version changed they are reloaded without a restart, and no session is dropped: an open session gets the new scenario at its next message.

- **Which one wins.** A scenario stored in mddbd wins over the TOML `[scenarios.<name>]` of the same name. A name mddbd does not have is served from the TOML, and so is every name until mddbd is reached or when mddbd is older than 2.19.0.
- **Disabled.** A scenario stored with `enabled: false` is refused at `join` with `scenario is disabled`; the TOML scenario of the same name does not stand in.
- **Prompt.** The system prompt is the one mddbd composes (operator prompt, guardrails, off-topic clause, the scenario's skills, style clause, the collections' `responsePrompt`s), so the panel's preview is what the model reads.
- **Applied fields.** `allowedCollections` bound the tools, `llm.model` / `temperature` / `maxTokens` / `extraBody` override `[llm]` for that scenario, and `maxTurns`, `tokenBudgetPerSession` and `rateLimit.messagesPerMinute` (per session) limit the conversation. A field left at 0 or empty keeps the server's own setting. `skills` reach the model through that composed prompt (see Skills below).
- **Health.** `GET /health` reports `"scenarios_source": "mddbd" | "toml"` and `"scenarios_version"`.

The full table is in [`services/mddb-chat/README.md`](../services/mddb-chat/README.md#scenarios-from-mddbd).

### Skills (2.19.0)

A skill is a reusable block of instructions in the `SKILL.md` format (front matter with `name` and `description`, then Markdown), stored in mddbd under `/v1/chat/skills` and attached to scenarios by name in a chosen order. mddbd composes them into the scenario's system prompt after the operator prompt, the guardrails and the off-topic clause, under a line saying the guardrails take precedence, so a skill cannot lift one; `mddb-chat` already uses that server-composed prompt and needs no change. A skill can be pasted or imported from an https URL — the panel shows the whole text before saving, and a GitHub URL is pinned to its commit — and it is fetched again only on an explicit "Update from source", shown as a diff first. Skills are admin only, reads included, and capped by `MDDB_CHAT_SKILL_MAX_BYTES` (32768) per skill and `MDDB_CHAT_PROMPT_MAX_BYTES` (131072) per composed prompt; see [API.md](API.md#chat-skills-v1chatskills).

### Signed-in users (2.19.0)

A site whose users are signed in can give each of them a persistent conversation, stored in mddbd and resumed on the next visit, another pod or after a restart (#405). Visitors without a token stay anonymous, exactly as before.

1. **Share a secret** of at least 32 random bytes between your backend and `mddb-chat`: `[identity] secret`, or `MDDB_CHAT_IDENTITY_SECRET`. `[identity] required = true` refuses every join without a valid token.
2. **Sign a join token on your backend** for the signed-in user — `v1.<base64url payload>.<base64url HMAC-SHA256>`, payload `{"sub", "name"?, "scenario", "exp"}`, a few minutes of lifetime (at most 24 h). Format, verification order, a Node.js signer and a test vector: [API.md — The join token](API.md#the-join-token-host--mddb-chat). Never sign in the browser.
3. **Hand it to the widget** before it loads, and refresh it there before it expires — the widget reads it again at every join:

   ```html
   <script>window.MDDB_CHAT_CONFIG = { identityToken: '<token from your backend>' };</script>
   ```

4. **Let the service account write** the system collection `chat_conversations` (`read` and `write` on it; see [API.md — Chat conversations](API.md#chat-conversations-v1chatconversations)).

What the user then gets:

- `join` with the token resumes their newest open conversation in that scenario (or starts one); `new_conversation: true` starts a fresh one and keeps the old; `conversation_id` reopens a given one. The session frame carries `conversation_id`, and a `history` frame brings back the last `[session] max_history_length` messages, which the model also gets — marked as untrusted context.
- Every question and final answer is stored, the answer with the tools it called, the model, the provider and the tokens.
- `list_conversations` and `delete_conversations { "confirm": true }` (all of the user's conversations) over the same WebSocket; `end` ends the stored conversation.
- Retention: a TOML scenario's `retention_days`, else mddbd's `MDDB_CHAT_RETENTION_DAYS` (30). Export and erasure for a user (GDPR) are host API calls on mddbd.

The user id is the token's `sub`, and only that: a user id sent by the browser is never read, and mddbd answers "not found" for any conversation of another `sub`. Every refusal and frame is listed in [`services/mddb-chat/README.md`](../services/mddb-chat/README.md#signed-in-users).

### Widget Configuration

```javascript
window.MDDB_CHAT_CONFIG = {
  serverUrl: 'ws://localhost:11030/ws',  // Chat server WebSocket URL
  title: 'Documentation Assistant',      // Widget header title
  subtitle: 'Powered by MDDB',           // Widget header subtitle
  primaryColor: '#2563eb',               // Accent color (hex)
  position: 'bottom-right',             // bottom-right | bottom-left
  welcomeMessage: 'Hello! How can I help you today?',
  identityToken: '<signed join token>'   // 2.19.0, optional: a signed-in user (see above)
};
```

Each rating (thumbs up/down) carries the `message_id` of the answer it rates (2.19.0).

## LLM Provider Examples

### OpenAI

The `[llm]` table of `config.toml`; the key can come from `MDDB_CHAT_LLM_API_KEY`
instead. Any provider other than `anthropic` (or `claude`) is spoken to over
the OpenAI-compatible API at `api_url`.

```toml
[llm]
provider = "openai"
api_url = "https://api.openai.com/v1"
model = "gpt-4o-mini"
```

### Anthropic Claude

```toml
[llm]
provider = "anthropic"
api_url = "https://api.anthropic.com/v1"
model = "claude-haiku-4-5-20251001"
```

### Ollama (Local, Free)

```toml
[llm]
provider = "openai"
api_url = "http://localhost:11434/v1"
model = "llama3.2"
```

### Bielik (Polish)

```toml
[llm]
provider = "openai"
api_url = "http://localhost:11434/v1"
model = "bielik-11b-v2.3-instruct"
```

## How RAG Works

1. **User sends a message** via the chat widget
2. **Chat server** receives it via WebSocket
3. **Hybrid search** — MDDB performs BM25 + vector search on your knowledge base
4. **Context injection** — top results are injected into the LLM prompt
5. **Streaming response** — the LLM streams its answer back through the chat server
6. **Widget renders** — the response appears with markdown formatting in the widget

## Step-by-Step Guide

See the [Website Chat Guide](/docs/uses-website-chat/) for a complete walkthrough with Docker and Ollama.

## Source Code

- Widget: [`services/mddb-chat-widget`](https://github.com/tradik/mddb/tree/main/services/mddb-chat-widget)
- Server: [`services/mddb-chat`](https://github.com/tradik/mddb/tree/main/services/mddb-chat)

