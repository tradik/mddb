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
`services/mddb-chat/config.example.toml`. It reads five environment variables;
the four secrets apply only when the matching TOML value is empty:

```bash
MDDB_CHAT_CONFIG=config.toml         # path of the TOML file (default: config.toml)
MDDB_CHAT_LLM_API_KEY=sk-...         # [llm] api_key (not needed for Ollama)
MDDB_CHAT_WEBHOOK_SECRET=            # [security] webhook_secret

# Auth to mddbd (SEC-007): keep these OUT of config.toml (it is gitignored).
# In docker-compose.dev.yml they default to the mddbd admin credentials from
# your .env (copy .env.example to .env first).
MDDB_CHAT_AUTH_USERNAME=admin        # [mddb] auth_username
MDDB_CHAT_AUTH_PASSWORD=             # [mddb] auth_password; never commit a real password
```

Everything else is a TOML key, not an environment variable:

| Setting | TOML key | Example default |
|---------|----------|-----------------|
| LLM provider | `[llm] provider` | `"openai"` (`openai`, `anthropic`, or any OpenAI-compatible service) |
| LLM model | `[llm] model` | `"gpt-4o-mini"` |
| LLM base URL | `[llm] api_url` | `"https://api.openai.com/v1"` |
| MDDB gRPC address | `[mddb] grpc_addr` | `"http://localhost:11024"` |
| Default collection | `[mddb] default_collection` | `"docs"` |
| Listen port | `[server] port` | `11030` |
| Concurrent sessions | `[session] max_concurrent` | `2` |
| Messages per minute | `[security] rate_limit_per_minute` | `30` |

### Widget Configuration

```javascript
window.MDDB_CHAT_CONFIG = {
  serverUrl: 'ws://localhost:11030/ws',  // Chat server WebSocket URL
  title: 'Documentation Assistant',      // Widget header title
  subtitle: 'Powered by MDDB',           // Widget header subtitle
  primaryColor: '#2563eb',               // Accent color (hex)
  position: 'bottom-right',             // bottom-right | bottom-left
  welcomeMessage: 'Hello! How can I help you today?'
};
```

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

