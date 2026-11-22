---
title: "One Server, a Collection per Project"
slug: "blog/a-collection-per-project"
status: publish
type: post
date: 2026-11-22
tags: [agents, mcp, search]
categories: [Guides]
excerpt: "Run one MDDB on your machine, index each repository into its own collection, and give each project's coding agent a key that reaches only its own code. Honest notes on when this helps and when grep is enough."
description: "A local MDDB with one collection per repository: autoindex, a scoped MCP key per project, keeping the index fresh, and when it beats grep."
---

A coding agent in your editor finds code the way you do in an unfamiliar
repository: it greps for a name, opens the files that match, follows an
import, and greps again. There is no index behind it. That works well when
you know the word to search for. It works less well when the question is
about meaning ("where do we apply the discount?") and the code never uses
that word, or when the answer sits in another repository, or in a design note
that never made it into the code.

This guide sets up one MDDB server on your machine, with one collection per
project, and connects each project's agent over MCP to its own collection
only.

## One server for every project

```bash
docker run -d --name mddb \
  -p 127.0.0.1:11023:11023 -p 127.0.0.1:9000:9000 \
  -v mddb-data:/app/data \
  -e MDDB_MCP_API_KEY_ENABLED=true \
  tradik/mddb:latest
```

Port 11023 is the HTTP API, 9000 the MCP endpoint. Binding both to
`127.0.0.1` keeps them on your machine, which matters here because
authentication is off by default. `MDDB_MCP_API_KEY_ENABLED=true` makes the
MCP endpoint require a key, which is what lets each project see only its own
collection (below).

For search by meaning, MDDB needs an embedding provider. With none configured
it checks once at startup for a local model server on its default port and
uses it if one answers; otherwise set `MDDB_EMBEDDING_PROVIDER`. The
[embedding providers](/docs/embedding-providers/) page lists the options.
Without one, full-text search and code symbols still work.

## A collection per repository

```bash
cd ~/code/shop-api
mddb-cli autoindex .
```

`autoindex` sends every file git tracks (or would track) into one collection.
Its name defaults to the repository name from the `origin` remote, or the
directory name, so `shop-api` lands in `shop-api` and the next repository in
its own collection. `-c` picks another name. Each document carries its path,
language, size and the commit it was read at, and the server extracts the
symbols each file defines and uses.

Run it again later and it only rewrites the files whose content changed.

For exact answers to "who calls this" and "what implements this", add
`--semantic`. It runs the language server you already have installed
(`gopls`, `basedpyright` or `pyrefly`, `phpactor`, `clangd`, `rust-analyzer`)
over the checkout and stores what it reports:

```bash
mddb-cli autoindex . --semantic
```

On MDDB's own Go code, 1349 files, this took the answer for references from
38.5% correct to 100%, and for methods that share a name with others, such as
the 21 `Close` methods, from 4.4% to 100%. It costs time: about two and a half
minutes on 1343 Go files, and the language server runs over the whole tree on
every `--semantic` run. Details are in
[Code intelligence](/docs/code-intelligence/).

## A key that reaches one project

One server holds all your projects, but an agent working on `shop-api` has no
reason to read `billing`. An MCP API key can be confined to some collections
and to reads (2.19.9):

```bash
curl -X POST http://localhost:11023/v1/mcp/keys \
  -d '{"name": "shop-api", "collections": ["shop-api"], "readOnly": true}'
# {"key":"mcp_…","name":"shop-api",…}   the key is shown once
```

With that key, a tool call that names another collection is refused, and
`tools/list` leaves out the tools the key can never call.

Then connect the project. For Claude Code, a `.mcp.json` in the project root:

```json
{
  "mcpServers": {
    "mddb": {
      "type": "http",
      "url": "http://localhost:9000/mcp",
      "headers": { "X-API-Key": "${MDDB_MCP_API_KEY}" }
    }
  }
}
```

Set `MDDB_MCP_API_KEY` in your environment (a per-directory env file works
well), and do not commit the key. The chat built into VS Code reads
`.vscode/mcp.json` instead; from 2.19.10 the panel's *LLM Connections → VS
Code* tab writes that file with an input VS Code asks for once and keeps in its
secret storage, so the file itself holds no key. Any other client that speaks
MCP over HTTP connects the same way.

One line in the project's agent instructions (`CLAUDE.md`, `AGENTS.md`) saves
the agent a guess: *"Search this project's code in the MDDB collection
`shop-api` before grepping."*

## Keeping it fresh

An index is only useful while it matches the code. Two cases:

- **The repository you are editing.** Index the local checkout, not the
  remote: your uncommitted work exists only there. Either follow every save
  while you work, or let git hooks re-index after each commit, merge and
  checkout:

  ```bash
  mddb-cli autoindex . --watch --prune          # until Ctrl-C
  mddb-cli autoindex hook install -- --prune    # once per repository
  ```

  Both send only the files that changed. The hook runs in the background, so
  a commit never waits for it, and it picks up from the last commit it
  indexed. Run
  `--semantic` when the structure changed, or once a day. Facts computed from
  an older version of a file are dropped the moment its content changes, and
  the answer falls back to the text-based symbols until the next run, so a
  stale fact is never served as current.

- **Repositories you only read**: a library you depend on, another team's
  service, a documentation site. Here the pushed branch is exactly what you
  want. An automation rule with a git source (2.19.5) syncs a branch, tag or
  latest release on a schedule or from the forge's push webhook, and only
  writes the files whose content changed. See
  [Automations](/docs/automations/).

The agent still reads a file before it edits it, so a slightly stale index
leads it to the right place in an older version of the code, not to a wrong
edit. It is still worth keeping fresh.

## Will it make you faster?

It makes *finding* code cheaper. It does not change how fast code gets
written.

We measured the finding part on MDDB's own repository: 15 tasks of the form
"where is X handled", each phrased the way a person asks.

| Route | Tokens, 15 tasks | Tool calls | Answer found |
|---|---|---|---|
| grep + read, best case | 139,959 | 28 | 13/15 |
| grep + read, typical | 2,769,973 | 133 | 13/15 |
| MDDB vector search, passages | 84,977 | 15 | 13/15 |

Both routes found the same 13 answers. The difference is the cost of getting
there: one search per task instead of up to 33 greps and reads when a common
word matches 160 files. Two of the tasks defeated grep outright, because the
first word an agent would try does not appear in the file that holds the
answer. The method and the script are in
[Benchmarks](/docs/benchmark/#finding-code-grep-and-read-vs-mddb-passages-v2188).

Where you will notice it:

- large repositories, or several at once;
- questions about behaviour rather than names;
- refactors that need every caller of a method whose name is common;
- notes, decisions and documentation that live next to the code.

Where you will not: a small repository you know well, where you already know
the word to grep for. Grep is fast, and an index adds nothing there.

Do not take our numbers for yours. `tools/bench/agent` in the repository runs
the same comparison on your own collection with your own task file. Ten
questions from your last week of work will tell you more than this table.
