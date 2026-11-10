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
  webhook/           session lifecycle webhooks
```
