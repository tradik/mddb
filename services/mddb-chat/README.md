# mddb-chat

The chat server behind the [MDDB chat widget](../mddb-chat-widget/): a Rust
(axum, tokio) WebSocket service that answers visitors' questions from an MDDB
knowledge base. Each turn runs a tool-calling loop against the configured LLM
(OpenAI, Anthropic, or any OpenAI-compatible endpoint); the model's
`search_docs` and `get_document` calls are served over gRPC from `mddbd`.

The full guide — embedding, providers, how RAG works — is in
[`docs/chat.md`](../../docs/chat.md). This file covers what lives in this
directory.

## Build, test, run

```bash
cargo build --release          # needs protoc; build.rs compiles ../../proto/mddb.proto
cargo test                     # or: make test-chat from the repository root
cargo run -- config.toml       # start with a config file (see below)
```

Every change is expected to pass `cargo fmt --all`, `cargo clippy --all-targets
-- -D warnings`, `cargo test` and `cargo audit`.

## Configuration

Copy [`config.example.toml`](config.example.toml) to `config.toml` and edit it;
it is gitignored. Secrets can stay blank in the file and come from the
environment: `MDDB_CHAT_LLM_API_KEY`, `MDDB_CHAT_WEBHOOK_SECRET`,
`MDDB_CHAT_AUTH_USERNAME`, `MDDB_CHAT_AUTH_PASSWORD`.

### `[mddb]` — the knowledge base

| Key | Default | Meaning |
|-----|---------|---------|
| `grpc_addr` | `"http://localhost:11024"` | Where `mddbd` listens for gRPC. |
| `default_collection` | `"docs"` | Searched when a scenario names no `allowed_collections`. |
| `search_top_k` | unset | Results per search. Unset means the collection's retrieval profile decides. |
| `search_type` | unset | `"hybrid"`, `"vector"` or `"fts"`. Unset defers to the collection. |
| `result_max_chars` | `800` | How much of each search result the model reads, in characters. A result that fits arrives whole, fenced code and inline code kept verbatim; a longer one is cut on a character boundary and ends with an ellipsis. `0` means the whole chunk. |
| `auth_username`, `auth_password` | `""` | Credentials for `mddbd`; prefer the environment variables above. |

### `[scenarios.<name>]` — who the assistant is

| Key | Default | Meaning |
|-----|---------|---------|
| `name` | required | Shown to the visitor. |
| `system_prompt` | required | The operator's instruction; the collection's own prompt is appended after it. |
| `allowed_collections` | `[]` | The collections this scenario's tools may search. The first one is the default; an empty list allows only `[mddb] default_collection`. The tool schema advertises exactly this set as an `enum`, and a call naming any other collection is refused with an error the model can read, and logged at `warn`. |
| `temperature` | `[llm] temperature` | Sampling temperature for this scenario. |
| `max_turns` | unlimited | User messages one session may send. |

The `[server]`, `[llm]`, `[session]`, `[security]` and `[[webhooks]]` tables
are documented inline in `config.example.toml`.

## WebSocket protocol

Every frame is a JSON object with a `type`.

| Client sends | Fields | Server answers |
|--------------|--------|----------------|
| `join` | `name`, `scenario` (default `assistant`) | `session` `{id, scenario}`, or `queued` `{position}` and `session` later |
| `message` | `content` | `chunk` `{content}` repeatedly, then `done` `{message_id}` |
| `feedback` | `rating`, `question`, `answer`, `message_id` (optional) | nothing |
| `resume` | `session_id` | `session` |
| `end` | — | `ended` |
| `ping` | — | `pong` |

Any refusal arrives as `error` `{message}`.

### Message ids

`done` carries `message_id`, a UUID that names the answer it ends:

```json
{"type": "done", "message_id": "7f0c1a52-6f0e-4c58-9a51-0f4a1c6f2b11"}
```

A client that lets the visitor rate an answer sends that id back, so the
rating names one answer unambiguously:

```json
{"type": "feedback", "rating": "down", "question": "…", "answer": "…",
 "message_id": "7f0c1a52-6f0e-4c58-9a51-0f4a1c6f2b11"}
```

Both fields are additions. A client written before them ignores the extra
field on `done`, and a `feedback` without `message_id` is still accepted.

## Webhooks

Each `[[webhooks]]` entry is a URL and the events it subscribes to:

```toml
[[webhooks]]
url = "https://example.com/chat-log"
events = ["message", "answer", "feedback"]
headers = { "Authorization" = "Bearer your-token" }
```

| Event | Fired when |
|-------|------------|
| `session.start` | a visitor is admitted |
| `session.end` | a session ends, by `end` or by disconnect |
| `queue.full` | a visitor is turned away |
| `message` | a question is accepted (after sanitizing and the turn and token limits) |
| `answer` | an answer has been sent in full |
| `feedback` | a visitor with a session rates an answer |

Events are opt-in. An entry without `events` receives `session.start`,
`session.end` and `queue.full` only — the set that existed before the
per-message events — so an existing config keeps receiving exactly what it
did. `message` and `answer` carry conversation text; subscribe to them only
for an endpoint you trust with it. A name the server does not know is logged
at startup and matches nothing.

### Envelope and signature

Every delivery is a `POST` with a JSON body of the same shape:

```json
{"event": "<name>", "timestamp": 1794268800, "data": { }}
```

`timestamp` is Unix seconds. `X-MDDB-Event` repeats the event name, and
`X-MDDB-Signature` is the hex HMAC-SHA256 of the raw body keyed with
`[security] webhook_secret` (empty when no secret is set). Verify it over the
bytes received, before parsing. A delivery that does not get a 2xx is retried
twice, after 0.5 s and 1 s.

### `message`

```json
{
  "event": "message",
  "timestamp": 1794268800,
  "data": {
    "session_id": "0b6f2d0e-8f0a-4a39-b0f3-3f1d0c0f7a10",
    "scenario": "support",
    "message_id": "c1c9c2b4-5a7e-4b43-8f55-9d2f0a3f6e01",
    "text": "How do I export a collection?"
  }
}
```

`text` is the question as sanitized (HTML tags stripped, trimmed). A message
the server refused — rate limit, turn limit, token budget — fires no event.

### `answer`

```json
{
  "event": "answer",
  "timestamp": 1794268802,
  "data": {
    "session_id": "0b6f2d0e-8f0a-4a39-b0f3-3f1d0c0f7a10",
    "scenario": "support",
    "message_id": "7f0c1a52-6f0e-4c58-9a51-0f4a1c6f2b11",
    "in_reply_to": "c1c9c2b4-5a7e-4b43-8f55-9d2f0a3f6e01",
    "text": "Run `mddb-cli export docs` …",
    "provider": "openai",
    "model": "gpt-4o-mini",
    "usage": {"input_tokens": 1840, "output_tokens": 212},
    "tools": [{"name": "search_docs", "collection": "docs"}],
    "latency_ms": 1730
  }
}
```

`message_id` is the id the client received in `done`; `in_reply_to` is the
`message_id` of the question. `usage` sums every tool-calling round of the
turn and is `null` when the provider reported none. A turn that used all five
tool rounds ends with a streamed response whose own usage is not counted.
`tools` lists each call in order with the collection it was pointed at;
`latency_ms` runs from accepting the question to the end of the answer.

### `feedback`

```json
{
  "event": "feedback",
  "timestamp": 1794268830,
  "data": {
    "session_id": "0b6f2d0e-8f0a-4a39-b0f3-3f1d0c0f7a10",
    "scenario": "support",
    "message_id": "7f0c1a52-6f0e-4c58-9a51-0f4a1c6f2b11",
    "rating": "down"
  }
}
```

`message_id` is `null` when the client sent none. The `question` and `answer`
strings of the WebSocket message are not forwarded: they are whatever the
client typed, and the id already names the answer.

## Layout

```
src/
  chat/handler.rs    WebSocket session: join, turns, the tool-calling loop
  chat/tools.rs      search_docs / get_document and the per-scenario policy
  chat/markdown.rs   result shaping: prose stripped, code kept, character-safe cuts
  chat/scenario.rs   prompt composition, per-scenario temperature and collection
  config.rs          config.toml schema and defaults
  grpc/client.rs     mddbd client (search, collection config)
  llm/               OpenAI-compatible and Anthropic providers
  session/           admission, queue, history, token budget
  security/          rate limiting, client IP, input sanitising
  chat/trace.rs      one turn's message ids, tools called and latency
  webhook/           webhooks: session lifecycle, message, answer, feedback
```

The handler tests run the real WebSocket handler on a loopback port with a
scripted model (`src/chat/testing/`) and a webhook receiver
(`src/webhook/test_support.rs`); they need no mddbd and no network.
`cargo llvm-cov --summary-only` prints per-file coverage.
