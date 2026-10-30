---
title: "Using MDDB from AI Agents"
slug: "docs/agents"
description: "Using MDDB from AI agents: MCP toolsets and keys, agent instructions, autoindex, passages, code symbols, corpus packs and destructive-tool confirmation."
status: publish
---

# Using MDDB from AI Agents

This page ties together what MDDB offers an agent — Claude Code, Cursor,
Windsurf, Claude Desktop or your own loop — and links to the page that covers
each part in full. The order follows a typical setup: connect, load the
agent's instructions, index the material, search it cheaply, then operate and
measure.

## 1. Connect over MCP

`mddbd` has a built-in MCP server with 97 tools ([MCP.md](MCP.md)). It serves
the 2026-07-28 and 2025-11-25 revisions side by side
([MCP-REVISIONS.md](MCP-REVISIONS.md)).

| Transport | How | When |
|---|---|---|
| Streamable HTTP | `POST /mcp` on the MCP listener (`MDDB_MCP_ADDR`, default `:9000`) | A running server, local or remote |
| stdio | `MDDB_MCP_STDIO=true mddbd` | One local client that owns the database file; it cannot share the file with a running server |
| SSE (legacy) | `GET /sse` + `POST /message` | Clients that only speak 2024-11-05 |

```bash
# Claude Code, against a running server
claude mcp add mddb --transport http http://localhost:9000/mcp \
  --header "X-API-Key: mcp_..."
```

Client-by-client configuration (Claude Desktop, Cursor, Windsurf, ChatGPT,
Ollama) is in [LLM_CONNECTIONS.md](LLM_CONNECTIONS.md).

**Keys and toolsets.** With `MDDB_MCP_API_KEY_ENABLED=true` the listener
takes MCP API keys: static ones in `MDDB_MCP_API_KEYS`, or keys created with
`POST /v1/mcp/keys`. With `MDDB_AUTH_ENABLED=true` and no MCP keys, it takes
the same credentials as the HTTP API. claude.ai custom connectors sign in with
OAuth 2.1 (`MDDB_MCP_OAUTH_ENABLED=true`); each token stands for an MCP API
key ([MCP.md](MCP.md#oauth-21-for-claudeai-connectors-v2181)).

Each built-in tool belongs to one of eleven toolsets: `search` (16 tools),
`code` (6), `documents` (13), `ingest` (7), `memory` (6), `schema` (5),
`admin` (25), `geo` (6), `automation` (8), `webhooks` (3), `wordpress` (2).
Every tool schema is in the model's context on every turn, so give an agent
only what its work needs:

```bash
MDDB_MCP_TOOLSETS=search,code mddbd                    # for the whole server
curl -X POST http://localhost:11023/v1/mcp/keys \
  -d '{"name": "code-reviewer", "toolsets": ["search", "code"]}'   # per key
# or a static key: MDDB_MCP_API_KEYS=key:code-reviewer:search|code
```

With `search,code` the 22 tool schemas take about 43 KB per turn, against
94 KB for all 93 tools of 2.18.10 (97 since 2.18.11). A tool outside the caller's toolsets is not listed,
and calling it returns JSON-RPC `-32601` ([MCP.md](MCP.md#toolsets-v2188)).

Operators can add their own tools and prompts in YAML
([CUSTOM-TOOLS.md](CUSTOM-TOOLS.md)), and replace the server's instructions
with `MDDB_MCP_INSTRUCTIONS_FILE`.

## 2. Give the agent its instructions

Tool schemas say what each tool accepts, not which one fits a question or how
much a response costs. [`integrations/agent-instructions/`](https://github.com/tradik/mddb/tree/main/integrations/agent-instructions)
holds that guidance, in one source and three generated variants:

| File | Install it as |
|---|---|
| `AGENTS.md` | `AGENTS.md` at the root of your project, or appended to the one you have |
| `claude-code/SKILL.md` | `.claude/skills/mddb/SKILL.md` in the project, or `~/.claude/skills/mddb/SKILL.md` for every project |
| `cursor/mddb.mdc` | `.cursor/rules/mddb.mdc` |
| `windsurf/mddb.md` | `.windsurf/rules/mddb.md` |

```bash
mkdir -p .claude/skills/mddb
curl -fsSL -o .claude/skills/mddb/SKILL.md \
  https://raw.githubusercontent.com/tradik/mddb/main/integrations/agent-instructions/claude-code/SKILL.md
```

They cover choosing between `full_text_search`, `semantic_search` and
`hybrid_search`, asking for projections and passages instead of documents,
waiting for an embedding after a write, conditional writes, and the memory
tools. Measured on this engine, the same `full_text_search` over five 12 KB
documents returns about 19 700 tokens with its defaults and about 327 with
`fields: ["key"]`. In this repository, edit `AGENTS.md` and run
`make agent-instructions`; CI fails if a variant was edited by hand or names a
tool that no longer exists.

## 3. Index a repository or a folder

`mddb-cli autoindex` loads a directory in one command
([BULK-IMPORT.md](BULK-IMPORT.md#autoindex-one-command-v2188)):

```bash
mddb-cli autoindex .                  # collection named after the repository
mddb-cli autoindex . --dry-run        # what would be indexed, skipped and pruned
mddb-cli autoindex . --prune          # also delete documents whose file is gone
mddb-cli autoindex docs --images      # keep the images Markdown files link to
```

In a git repository it takes the files git tracks; elsewhere it skips
`node_modules`, `vendor`, `dist` and the like. Each document is keyed by its
path and carries `path`, `extension`, `kind`, `language`, `size`, `modified`
and, in a repository, `gitCommit` and `gitBranch`. A Markdown file's
frontmatter becomes metadata as well (2.18.11+): the server reads it once, with
the parser `/v1/upload` uses, so `tags: a, b`, `tags: [a, b]` and a `- a` block
list are all lists, and the body is stored without the block. The meta above
wins for a key both name; `--no-frontmatter` stores Markdown files as they are.
A second run rewrites only the files that changed. At the end the search advisor stores a retrieval
profile on the collection: for code, hybrid search, graph mode and
`format: "passages"`.

Binary files are skipped, except the documents the server converts to
Markdown: PDF, DOCX, ODT, RTF, XLSX, ODS, PPTX, ODP, EPUB, HTML, TeX, CSV,
TSV, EML and MBOX. Every converted document has `meta.conversionConfidence`
(0–1; a scanned PDF without a text layer scores 0), and
`filter: {"conversionConfidence": {"lt": 0.5}}` finds the ones worth checking.

There is no MCP tool for this: the server never reads its own filesystem on
request, so an agent runs `mddb-cli autoindex` like any other command.

## 4. Ask for less: passages and the context budget

`format: "passages"` on `/v1/fts`, `/v1/vector-search`, `/v1/hybrid-search`,
`/v1/cross-search` and the matching MCP tools returns, per hit, the path, a
line range, the innermost enclosing definition, the text and its token
estimate — never the whole document ([SEARCH.md](SEARCH.md#passages--what-an-agent-gets-v2188)):

```json
{"passages": [{
  "collection": "theme", "key": "css/style.css", "path": "css/style.css",
  "lines": {"start": 152, "end": 155}, "symbol": ".hero-banner",
  "content": ".hero-banner {\n  background: url(hero.png);\n  padding: 2rem;\n}",
  "score": 0.91, "source": "fts", "tokens": 16
}], "total": 1}
```

A collection's retrieval profile caps what one search may return:

```bash
curl -X PUT http://localhost:11023/v1/collection-config \
  -d '{"collection": "repo", "retrieval": {"format": "passages", "contextTokenBudget": 4000}}'
```

`contextTokenBudget` drops results from the tail of the ranked list, never
cuts one in half, and says so with `contextTruncated: true`; the first result
is always kept ([API.md](API.md#retrieval-defaults-v2120)). Separately, the
server returns at most `MDDB_SEARCH_MAX_LIMIT` results (default 1000), and a
capped response carries `limitCapped: true`.

Other ways to narrow a search on the MCP tools:

- `filter: {"path": {"prefix": "src/"}}` — operators `in`, `prefix`,
  `contains`, `gt`/`gte`/`lt`/`lte`, `not`, `exists`, answered from the
  metadata index.
- `group_by: {"key": "path", "per_group": 1}` — one result per file.
- `fields: ["key", "meta.title"]` — a projection instead of documents.

## 5. Code: symbols and the graph

For a code collection, two calls replace most search-then-read loops
([API.md](API.md#one-symbol-everywhere-it-matters-v2188)):

- **`code_symbol`** (`GET /v1/symbol?collection=…&name=…`) returns a
  symbol's definitions with their line span, every reference with the
  definition it sits in, callers, callees, tests and siblings, as passages.
  `include` picks among `definitions`, `references`, `callers`, `callees`,
  `tests`, `siblings`; `limit` is per relation (default 20, at most 200).
- **`code_graph`** (`GET /v1/code-graph?collection=…&key=…&direction=in`)
  answers the file-level question: what depends on this document (`in`), what
  it depends on (`out`), or both, up to 3 hops.

Symbols and edges are extracted for C, C++, assembly, Go, Rust, Java, Python,
Ruby, PHP, JavaScript, TypeScript, CSS and HTML, and for Terraform,
Kubernetes, OpenShift, Kustomize and Ansible files; the server's table is at
`GET /v1/code-languages`.

```json
{"name": "code_symbol", "arguments": {"collection": "repo", "name": "SearchHybrid", "include": ["definitions", "callers", "tests"]}}
```

Other projects' code can sit next to yours as read-only reference
collections: `mddb-cli reference add <repository>` clones and indexes it on
the client, and the `reference_search`, `reference_symbol`,
`reference_examples` and `reference_dependencies` tools answer with the
licence of each hit ([REFERENCE-CODING.md](REFERENCE-CODING.md)).

## 6. Knowledge that is not in the repository: corpus packs

A library's documentation, an RFC or a specification can be built into a
collection once and shipped as a `.mddbpack` — documents, full-text index and
vectors — so another machine installs it without re-embedding or reindexing
([CORPUS-PACKS.md](CORPUS-PACKS.md)):

```bash
mddb-cli corpus build k8s-docs --license CC-BY-4.0 --version 1.31
mddb-cli corpus install k8s-docs-1.31.mddbpack --sha256 <sum>
mddb-cli corpus list
```

An agent does the same over MCP (v2.18.11, `admin` toolset): `corpus_list`
lists the installed packs, `corpus_install` installs one the server downloads
from an `https://` URL (optional `sha256`), and `corpus_remove` removes one,
after the confirmation below. Building is left to the CLI, REST and gRPC: a
pack is a file, and a tool answer cannot carry it.

```json
{"name": "corpus_install", "arguments": {"url": "https://example.org/k8s-docs-1.31.mddbpack", "sha256": "<sum>"}}
```

An installed pack is read-only: writes to it are refused with `READ_ONLY`, so
an agent keeps its own notes in another collection. A pack embedded with a
different model than the server's is installed with vectors disabled, and
hybrid search on it uses full text.

## 7. Better answers from the same index

| Option | What it does | Page |
|---|---|---|
| `algorithm: "auto"` (`vector_algorithm` on `hybrid_search`) | The server picks the vector index per query from collection size, memory, built indexes and how much the filter admits; `explain: true` shows why | [SEARCH.md](SEARCH.md#auto-the-server-chooses-v2189) |
| `query_decomposition: "auto"` | Splits a question with several needs ("set up a replica and take a backup") into up to 4 parts, searches each and keeps every document's best score. +0.065 nDCG@10 on two-topic questions, unchanged on ordinary ones | [SEARCH.md](SEARCH.md#query-decomposition--questions-with-several-needs-v21810) |
| `positive` / `negative` | Relevance feedback: keys of hits that answered and that did not; the next search moves towards the first | [SEARCH.md](SEARCH.md#relevance-feedback--positive-and-negative-examples-v2189) |
| Summaries | The client writes a short summary in meta `summary`; with `retrieval.summaryMode` `blend` or `expand` it is embedded and searched as a second representation. MDDB never generates a summary | [SEARCH.md](SEARCH.md#summaries--a-second-representation-v2189) |
| `wait_for_index`, `wait_for_keys` | Full text finds a document as soon as its write returns; its embedding comes later. These make a vector search wait for named keys instead of polling `get_indexing_status` | [MCP.md](MCP.md#searching-what-you-just-wrote-v2188) |
| `expected_revision` | Conditional write: fails with a conflict when another agent wrote the document in between | [API.md](API.md#patch-v1update) |
| `run_eval`, `add_eval_query` | Measure recall@k and nDCG@k of several configurations on your own questions before choosing one | [API.md](API.md#post-v1eval) |

```json
{"name": "hybrid_search", "arguments": {
  "collection": "docs", "query": "set up a replica and take a backup",
  "vector_algorithm": "auto", "query_decomposition": "auto",
  "format": "passages", "top_k": 5, "explain": true}}
```

## 8. Destructive tools ask first

`destructiveHint` is advice a client may ignore. With
`MDDB_MCP_CONFIRM_DESTRUCTIVE=elicit` or `require`, MDDB itself asks the human
behind the client, through MCP elicitation, before `delete_collection`,
`delete_documents_batch`, `delete_where`, `truncate_revisions`,
`restore_backup` or `corpus_remove` runs; `delete_collection` and
`corpus_remove` also ask for the collection or pack name to be typed. Nothing happens unless the answer is yes. Under `elicit`, a
client without elicitation support runs the tool as before; under `require`
it is refused with `-32021`. The default, `off`, changes nothing
([MCP.md](MCP.md#confirming-destructive-tools-v2189)).

For bulk changes, the `*_where` tools take `dry_run: true`: check `matched`
and `sample` before the real call.

## 9. Operate and measure

- **Retrieval gain.** `get_gain` (`GET /v1/gain`, `mddb-cli gain --window 7d`)
  reports how many searches ran and how many tokens passages and chunks saved
  against returning whole documents. Counts only, no queries
  ([TELEMETRY.md](TELEMETRY.md#retrieval-gain-v2188)).
- **Query-shape counters.** Every surface counts search modes, the algorithm
  that ran and what `auto` chose, filter operators, capped and empty results,
  decomposed queries and, for the code graph, depth, clamps and chains of
  graph calls, as `mddb_query_shape_*` in `/metrics`
  ([TELEMETRY.md](TELEMETRY.md#query-shape-v21810)).
- **Tool calls.** With `MDDB_MCP_LOGGING_ENABLED=true` each `tools/call` is
  logged with the tool, the query and whether it found anything; the
  `mddb_operations_total{operation="mcp_tool_call"|"mcp_tool_miss"|"mcp_tool_error"}`
  counters are always on ([MCP.md](MCP.md#request-logging)).
- **Diagnose bundle.** `mddb-cli diagnose` (admin; `GET /v1/admin/diagnose`)
  saves a tar.gz with build, configuration, stats, metrics, the query-shape
  report and recent error classes, without document content, metadata
  values, queries, keys or IP addresses; collection names are hashed unless
  `--keep-names`. `--inspect` prints its manifest and writes nothing. Attach
  it to an issue about a question MDDB did not answer
  ([TELEMETRY.md](TELEMETRY.md#diagnose-bundle)).

## See also

- [MCP.md](MCP.md) — every MCP setting and the tool catalogue
- [LLM_CONNECTIONS.md](LLM_CONNECTIONS.md) — client configuration
- [INTEGRATIONS.md](INTEGRATIONS.md#12-langchain--mddb-vectorstore-retriever-store) — LangChain, LangGraph `MddbStore` and the Deep Agents `MddbBackend`
- [RAG-PIPELINE.md](RAG-PIPELINE.md) — retrieval for a chat or answer pipeline
