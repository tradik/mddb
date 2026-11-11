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
cargo run                      # reads ./config.toml, or the file named by MDDB_CHAT_CONFIG
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
| `connect_retry_max_seconds` | `30` | Longest wait between two attempts to reach `mddbd` at startup; see [Starting before mddbd](#starting-before-mddbd). `0` = one attempt, exit on failure. |

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

## Endpoints

| Path | What it is |
|------|------------|
| `GET /ws` | The chat WebSocket (protocol below) |
| `GET /health` | Readiness and session counts: `200` once mddbd is reached, `503` before |
| `GET /config` | Scenarios and limits a client needs before joining |

## Starting before mddbd

Since 2.19.0 mddb-chat does not need mddbd to be up when it starts. It opens its port at
once and connects (and logs in) in the background, waiting 1 s, 2 s, 4 s...
between attempts, capped at `[mddb] connect_retry_max_seconds` (default `30`),
for as long as it takes. Until then:

- `GET /health` answers `503` with `"status": "starting"` and
  `"mddb_connected": false` — point a Kubernetes **readiness** probe at it
  (not a liveness probe, which would restart a pod that is merely waiting);
- visitors can join, and a message gets the error
  `the knowledge base is not connected yet; try again shortly`.

Set `connect_retry_max_seconds = 0` for the behaviour before 2.19.0: one attempt before
the port opens, and exit with `failed to connect to mddbd` if it fails.

The process exits cleanly on `SIGTERM` and Ctrl-C.

## Extra LLM request fields

`[llm] extra_body` (2.19.0) is a TOML table merged into every request sent to an
OpenAI-compatible endpoint, for parameters mddb-chat does not model:

```toml
[llm]
provider = "openai"
api_url = "http://localhost:11434/v1"
model = "qwen3:8b"
extra_body = { reasoning_effort = "none" }
```

- Any TOML value is passed through as JSON, nested tables included
  (`extra_body = { options = { num_ctx = 8192 } }`).
- The request's own fields win: `model`, `messages`, `max_tokens`,
  `temperature`, `stream` and `tools` in `extra_body` are ignored.
- It applies to the non-streaming and the streaming call alike, and not to
  `provider = "anthropic"`.

**Empty answers from thinking models.** Ollama returns a thinking model's
chain of thought in a separate `reasoning` field (`reasoning_content` on some
other servers). When the reasoning uses up `max_tokens`, `content` comes back
empty. mddb-chat never shows reasoning to the visitor; with
`RUST_LOG=mddb_chat=debug` it logs that an answer was reasoning-only, with the
first 400 characters. Fix it with `extra_body = { reasoning_effort = "none" }`
or a larger `max_tokens`.

## WebSocket protocol

Every frame is a JSON object with a `type`.

| Client sends | Fields | Server answers |
|--------------|--------|----------------|
| `join` | `name`, `scenario` (default `assistant`) | `session` `{id, scenario}`, or `queued` `{position}` and `session` later |
| `message` | `content` | `chunk` `{content}` repeatedly, then `done` `{message_id}` |
| `feedback` | `rating`, `question`, `answer`, `message_id` (optional) | nothing |
| `resume` | `session_id` | `session` `{id, scenario}` of the session reattached |
| `end` | — | `ended` |
| `ping` | — | `pong` |

Any refusal arrives as `error` `{message}`.

### Sessions outlive their connection

A closed socket does not end a session (2.19.0). On disconnect the session is
**detached**: its history is kept, and it stops counting toward
`[session] max_concurrent`, so the next visitor in the queue is admitted at
once. A client that reconnects sends `resume` and carries on where it left off.

A session is removed only when the visitor sends `end`, or when it has been
idle for `[session] session_ttl_minutes` (counted from the last message, or
from the disconnect if that came later). Both fire `session.end`; a disconnect
alone does not.

`resume` is refused with an `error` when:

| Message | Meaning |
|---------|---------|
| `session not found or expired` | Ended, timed out, or never existed. Join again. |
| `session is open in another connection` | Another live socket holds it; two connections never share a session. |
| `server is at capacity; try resuming again shortly` | The freed seat went to someone else. The session is kept; retry. |
| `rate limited` | `resume` is charged to the per-address rate limit, like `join`. |

`GET /health` reports attached sessions as `active_sessions` and the ones
waiting for a `resume` as `detached_sessions`.

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
| `session.end` | a session ends: the visitor sent `end`, or it sat idle past `session_ttl_minutes`. A disconnect alone only detaches it |
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
  chat/handler.rs    the WebSocket loop: frames in, messages dispatched, feedback
  chat/lifecycle.rs  join, resume, end, and detaching on disconnect
  chat/turn.rs       one message through the tool-calling loop
  chat/prompt.rs     the system prompt and history as the model sees them
  chat/outbound.rs   frames to the visitor
  chat/tools.rs      search_docs / get_document and the per-scenario policy
  chat/markdown.rs   result shaping: prose stripped, code kept, character-safe cuts
  chat/scenario.rs   prompt composition, per-scenario temperature and collection
  app.rs             assembling the server: state, routes, maintenance, shutdown
  config.rs          config.toml schema and defaults
  grpc/client.rs     mddbd client (search, collection config)
  grpc/link.rs       the connection to mddbd: retried in the background until up
  routes/            /health (readiness), /config, /ws
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
