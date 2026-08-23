---
title: "Website Chat"
slug: "docs/uses-website-chat"
description: "Add a RAG-powered AI chat widget to any website with MDDB, mddb-chat and Ollama - the whole stack runs locally, with no cloud API fees."
status: publish
---

# Website Chat

Add an AI chatbot to any website that answers questions based on your content. The entire stack runs locally with Docker and Ollama — no cloud API fees.

```
Your content → MDDB → Ollama (LLM + embeddings) → Chat widget on your website
```

## Architecture

| Component | Role | Port |
|-----------|------|------|
| **mddbd** | Document database + vector search | 11023 (HTTP), 11024 (gRPC) |
| **mddb-chat** | Rust chat server, connects LLM to MDDB | 11030 |
| **mddb-chat-widget** | Embeddable JavaScript widget | 11032 |
| **Ollama** | Local LLM + embedding models | 11434 |

## Requirements

- Docker and Docker Compose
- 8 GB+ RAM (for Ollama models)
- Your content in Markdown files

---

## Step 1: Install Ollama

```bash
# macOS
brew install ollama

# Linux
curl -fsSL https://ollama.com/install.sh | sh
```

Start the Ollama service:

```bash
ollama serve
```

Pull the required models:

```bash
# Embedding model (required for search)
ollama pull nomic-embed-text

# Chat model (pick one)
ollama pull qwen3:8b          # 8B params, fast, good quality
# or
ollama pull llama3.1:8b       # Meta's Llama, good for English
# or
ollama pull mistral:7b        # Mistral, good all-rounder
```

Verify Ollama is running:

```bash
curl http://localhost:11434/api/tags
```

---

## Step 2: Clone the MDDB repository

```bash
git clone https://github.com/tradik/mddb.git
cd mddb
```

---

## Step 3: Configure the chat service

Create the chat configuration file:

```bash
cp services/mddb-chat/config.example.toml services/mddb-chat/config.toml
```

Edit `services/mddb-chat/config.toml`:

```toml
[server]
host = "0.0.0.0"
port = 11030
cors_origins = ["*"]

[mddb]
grpc_addr = "http://mddbd:11024"
default_collection = "docs"
search_top_k = 5
search_type = "hybrid"

[llm]
provider = "openai"
api_url = "http://host.docker.internal:11434/v1"
api_key = "not-needed"
model = "qwen3:8b"
max_tokens = 1024
temperature = 0.7
stream = true

[session]
max_concurrent = 2
queue_size = 10
max_history_length = 50
session_ttl_minutes = 60
max_response_length = 4096

[security]
rate_limit_per_minute = 30
max_message_length = 2000
trusted_proxies = []   # see "Rate limiting behind a proxy" below

[scenarios.assistant]
name = "Website Assistant"
system_prompt = """You are a helpful assistant for our website.
Answer questions based on the provided context from our content.
Be concise, accurate, and friendly. If you don't know the answer,
say so honestly rather than making something up."""
allowed_collections = ["docs"]
```

### Rate limiting behind a proxy

The rate limit is charged per client address, and that address is the TCP peer
— it cannot be forged by sending a header.

Behind a reverse proxy or cloudflared, the TCP peer is the *proxy*. Every
visitor then shares one bucket, so the first noisy one locks out the rest, and
`rate_limit_per_minute` stops meaning what it says.

Set `trusted_proxies` to the proxy's address or network and `X-Forwarded-For`
is honoured from it:

```toml
[security]
trusted_proxies = ["172.16.0.0/12"]   # the Docker bridge network
```

Only list addresses you control. Anything listed here can name whatever client
address it likes, which is the whole reason the list exists rather than the
header being trusted outright. Addresses to the left of the last trusted hop
are ignored, so a client prepending its own `X-Forwarded-For` cannot pick which
bucket it lands in.

Key settings:
- `api_url` points to Ollama's OpenAI-compatible endpoint
- `model` — the Ollama model you pulled in Step 1
- `default_collection` — the MDDB collection your content is stored in
- `search_type` — `"hybrid"` combines full-text and vector search for best results

---

## Step 4: Start the stack with Docker Compose

```bash
docker compose -f docker-compose.dev.yml up -d mddbd mddb-chat mddb-chat-widget
```

This starts three services:
- **mddbd** on `http://localhost:11023`
- **mddb-chat** on `http://localhost:11030`
- **mddb-chat-widget** on `http://localhost:11032`

Check that all services are running:

```bash
docker compose -f docker-compose.dev.yml ps
```

Wait for the health check to pass:

```bash
curl http://localhost:11023/v1/health
```

---

## Step 5: Load your content

### 5a. Prepare your content

Put your website content as Markdown files in a folder. For example:

```bash
mkdir -p content
# Copy your .md files here
# Each file becomes a document in MDDB
```

### 5b. Load the content

```bash
# Download the loader script
curl -sO https://raw.githubusercontent.com/tradik/mddb/main/scripts/load-md-folder.sh
chmod +x load-md-folder.sh

# Load all .md files into the "docs" collection
./load-md-folder.sh content/ docs --lang en_US --verbose
```

Or use the HTTP API directly:

```bash
for md_file in content/*.md; do
  curl -s -X POST http://localhost:11023/v1/upload \
    -F "file=@$md_file" \
    -F "collection=docs" \
    -F "lang=en"
  echo " -> $(basename $md_file)"
done
```

### 5c. Generate vector embeddings

Reindex the collection so that semantic search works:

```bash
curl -X POST "http://localhost:11023/v1/vector-reindex?collection=docs"
```

Check that documents and vectors are loaded:

```bash
curl -s http://localhost:11023/v1/stats | python3 -m json.tool
```

---

## Step 6: Test the chat

Open the chat widget page in your browser:

```
http://localhost:11032
```

Or test via the WebSocket API directly:

```bash
# Using websocat (install: brew install websocat)
websocat ws://localhost:11030/ws
```

Then type a JSON message:

```json
{"type":"message","content":"What is this website about?"}
```

The chat service will:
1. Search your MDDB collection for relevant documents
2. Send the context + user question to Ollama
3. Stream the LLM response back

---

## Step 7: Embed the widget on your website

Add one script tag to any HTML page:

```html
<script
  src="http://localhost:11032/mddb-chat-widget.js"
  data-server="ws://localhost:11030/ws"
  data-title="Ask a question"
  data-placeholder="Type your question..."
  data-theme="light"
></script>
```

That's it. A chat bubble appears in the bottom-right corner.

### Widget attributes

| Attribute | Default | Description |
|-----------|---------|-------------|
| `data-server` | `ws://localhost:11030/ws` | WebSocket URL of mddb-chat |
| `data-title` | `"Chat"` | Widget header title |
| `data-placeholder` | `"Type a message..."` | Input placeholder text |
| `data-theme` | `"light"` | `"light"` or `"dark"` |
| `data-position` | `"bottom-right"` | Widget position on page |
| `data-scenario` | `"assistant"` | Scenario from config.toml |

### Production deployment

For production, replace `localhost` with your server addresses:

```html
<script
  src="https://your-domain.com/mddb-chat-widget.js"
  data-server="wss://your-domain.com/ws"
  data-title="Help"
></script>
```

---

## Step 8: Customize the assistant

### Multiple scenarios

Add different chat personalities in `config.toml`:

```toml
[scenarios.sales]
name = "Sales Assistant"
system_prompt = """You are a friendly sales assistant.
Help customers find the right product based on their needs.
Always be helpful and suggest relevant products from our catalog."""
allowed_collections = ["products"]
temperature = 0.5

[scenarios.support]
name = "Technical Support"
system_prompt = """You are a technical support agent.
Help users troubleshoot issues with our products.
Be patient and provide step-by-step solutions."""
allowed_collections = ["docs", "faq"]
temperature = 0.3
```

Select a scenario in the widget:

```html
<script
  src="http://localhost:11032/mddb-chat-widget.js"
  data-server="ws://localhost:11030/ws"
  data-scenario="support"
></script>
```

### Using a cloud LLM instead of Ollama

If you prefer OpenAI or Claude instead of a local model:

```toml
# OpenAI
[llm]
provider = "openai"
api_url = "https://api.openai.com/v1"
api_key = ""  # set MDDB_CHAT_LLM_API_KEY env var
model = "gpt-4o-mini"

# Anthropic Claude
[llm]
provider = "anthropic"
api_key = ""  # set MDDB_CHAT_LLM_API_KEY env var
model = "claude-sonnet-4-20250514"
```

Set the API key:

```bash
export MDDB_CHAT_LLM_API_KEY="sk-..."
docker compose -f docker-compose.dev.yml up -d mddb-chat
```

---

## Troubleshooting

### Chat returns empty responses

Check that:
1. Documents are loaded: `curl http://localhost:11023/v1/stats`
2. Ollama is running: `curl http://localhost:11434/api/tags`
3. The collection name in `config.toml` matches the one you loaded data into

### Ollama connection refused

If MDDB runs in Docker and Ollama runs on the host:
- On macOS/Windows: use `http://host.docker.internal:11434`
- On Linux: add `--add-host=host.docker.internal:host-gateway` to docker run

### Widget doesn't appear

Check the browser console for errors. Common issues:
- CORS: make sure `cors_origins = ["*"]` in config.toml
- WebSocket URL: must match the actual mddb-chat address

---

## Summary

| Step | Command | What it does |
|------|---------|--------------|
| 1 | `ollama pull qwen3:8b` | Install local LLM |
| 2 | `git clone .../mddb` | Get the source code |
| 3 | Edit `config.toml` | Configure chat + LLM |
| 4 | `docker compose up -d` | Start all services |
| 5 | `load-md-folder.sh content/ docs` | Load your content |
| 6 | Open `localhost:11032` | Test the chat |
| 7 | Add `<script>` tag | Embed on your site |
| 8 | Edit `config.toml` scenarios | Customize personality |
