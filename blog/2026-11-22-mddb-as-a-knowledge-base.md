---
title: "MDDB as Your Knowledge Base"
slug: "blog/mddb-as-a-knowledge-base"
status: publish
type: post
date: 2026-11-22
tags: [rag, mcp, retrieval]
categories: [Guides]
excerpt: "Load your documents into MDDB once, keep them current, and connect the chat tools and agents your team already uses. Four ways in, what each one is for, and what a question costs."
description: "Use MDDB as a knowledge base: load folders, sites and repositories, keep them in sync, and connect MCP clients and Open WebUI through External Knowledge, web search and model endpoints."
---

A knowledge base for a language model has four jobs: take in documents in
the formats people actually have, keep them current, decide who may read
what, and answer a question with the few paragraphs that matter instead of
whole files. This guide sets MDDB up for all four and connects it to the
tools on the other side. The commands were run against a local server
built from the 2.19.10 source; the Open WebUI settings are the ones named
in its admin panel.

## 1. Load the documents

Pick the route that matches where the documents live. All of them write
ordinary documents into a collection, so you can mix them.

| Where the knowledge is | How it gets in | Stays current by |
|---|---|---|
| a folder of files | `mddb-cli autoindex ~/handbook -c handbook` | running it again, or `--watch` |
| a website | a sync rule with `source: "sitemap"` | its schedule |
| a repository of docs | a sync rule with `source: "git"` | its schedule, or the forge's webhook |
| an object storage bucket | a sync rule with `source: "s3"` | its schedule |
| one file from an application | `POST /v1/upload` | the application |

`autoindex` and `/v1/upload` convert PDF, DOCX, XLSX, PPTX, ODT, EPUB, HTML,
CSV and mail files to Markdown on the way in, and record how well each
conversion went: a scanned PDF with no text layer gets a confidence of 0, so
you can list the files that need attention instead of discovering them in a
wrong answer.

```bash
mddb-cli autoindex docs -c handbook
# ✓ handbook: 77 files (2.4 MB) — 77 added, 0 updated, 0 unchanged, 0 failed, 0 pruned, 34 skipped in 360ms
mddb-cli autoindex docs -c handbook
# ✓ handbook: 77 files (2.4 MB) — 0 added, 0 updated, 77 unchanged, 0 failed, 0 pruned, 34 skipped in 75ms
```

That is MDDB's own documentation folder, without an embedding provider. The
34 skipped files are images. With an embedding provider configured the first
run takes longer, because every chunk is embedded; we did not measure that
here. Search by meaning needs one. Without it, full-text search still works,
and every connection below falls back to it.

A sync rule for a site, checked every six hours, which removes pages that
are gone:

```bash
curl -X POST http://localhost:11023/v1/automation -H 'Content-Type: application/json' -d '{
  "type": "cron", "name": "docs site", "schedule": "0 */6 * * *", "enabled": true,
  "sync": {"source": "sitemap", "url": "https://docs.example.com/sitemap.xml",
           "collection": "handbook", "lang": "en", "prune": true}
}'
```

Details: [Bulk Import](/docs/bulk-import/) and
[Automations](/docs/automations/#synced-sources).

## 2. Decide who reads what

One collection per audience is the simplest rule: `handbook` for everyone,
`finance` for finance. Then:

- **People and chat tools** use an API key that belongs to a user
  (`POST /v1/auth/api-key`). The user's permissions decide which collections
  a search may touch.
- **Agents over MCP** use an MCP key limited to named collections, read-only
  if they should never write.

Turn authentication on before the server leaves your machine. Every
endpoint that speaks another product's API shape is a module that starts
switched off, and you switch on only the ones you connect.

## 3. Connect it

There are four ways in. They are not alternatives: most teams end up with
two.

| You want | Connect through | Module to switch on |
|---|---|---|
| a coding agent or an IDE chat to search and cite | MCP | none (the MCP listener) |
| a chat UI to attach a collection as a knowledge source | External Knowledge | `vectorStores` |
| a chat UI to search everything when the user asks | web search engine | `webSearch` |
| a chat model that already answers from your documents | a chat scenario as a model | mddb-chat's `openai_api` |

### MCP: agents and IDE chats

Any client that speaks MCP over HTTP connects with a URL and a key:

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

The agent gets search tools that return passages with a path and a line
range, so it can quote its source. The panel's *LLM Connections* page writes
this file for the clients it knows. More in
[Using MDDB from AI Agents](/docs/agents/).

### External Knowledge in Open WebUI

Switch the module on, then check what Open WebUI will see:

```bash
curl -X PUT http://localhost:11023/v1/features/vectorStores \
  -H 'Content-Type: application/json' -d '{"enabled":true}'

curl "http://localhost:11023/v1/vector_stores?limit=100"
# {"object":"list","data":[{"id":"handbook","object":"vector_store","name":"handbook",
#   "status":"completed","file_counts":{"completed":77,"total":77,…}}],…}
```

In Open WebUI: *Admin → Settings → Integrations → External Knowledge
Sources → Add*, provider **OpenAI-compatible**, endpoint
`http://<mddb-host>:11023/v1`, and the MDDB API key. Each collection the
key's user may read appears as a vector store. A second provider, for
clients that speak a vector database's query API, is described in
[LLM Connections](/docs/llm-connections/#open-webui).

A question then costs this:

```bash
curl -X POST http://localhost:11023/v1/vector_stores/handbook/search \
  -H 'Content-Type: application/json' \
  -d '{"query":"how do I restore a backup","max_num_results":3}'
```

| Result | Score | Tokens returned |
|---|---|---|
| `BACKUP.md` | 0.81 | 91 |
| `DOCKER.md` | 0.68 | 63 |
| `API.md` | 0.67 | 226 |

380 tokens for three passages, counted as four characters per token. The
top document alone is about 3,350 tokens, and the three documents together
about 71,800, because one of them is the full API reference. A knowledge
source that returns whole files would put that into every prompt.

### Web search, reranking and document loading

Open WebUI can hand three more steps to an external service. MDDB answers
each in the shape Open WebUI sends:

```bash
curl -X PUT http://localhost:11023/v1/features/webSearch \
  -H 'Content-Type: application/json' -d '{"enabled":true}'

curl -X POST http://localhost:11023/v1/web-search \
  -H 'Content-Type: application/json' -d '{"query":"restore a backup","count":3}'
# [{"link":"http://…/v1/doc-meta?collection=handbook&key=BACKUP.md&lang=en",
#   "title":"MDDB Backup","snippet":"# Backup, restore and repair\n\nHow MDDB copies its database…"}, …]
```

"Web search" here means a search across every collection the key may read.
It suits a question whose collection the user does not know. The reranker
(`rerankApi`) needs a reranking model configured, and the document loader
(`extractApi`) converts an uploaded file without storing it. Settings for
all three are in [Open WebUI Engines](/docs/openwebui/).

### A scenario as a model

mddb-chat can publish a chat scenario (a prompt, its allowed collections,
its tools) as a model on an OpenAI-compatible endpoint. The chat UI then
talks to "handbook-assistant" like any other model, and retrieval happens on
the MDDB side, with the scenario's rules. Use this when you want one
behaviour for everyone rather than a knowledge source each user attaches.
Setup is in [LLM Connections](/docs/llm-connections/).

## Which one, when

- **Start with MCP** if the people asking are developers with agents.
- **Start with External Knowledge** if they use a chat UI and should pick
  the collection themselves.
- **Add web search** when users do not know which collection holds the
  answer.
- **Use a scenario as a model** when answers must follow fixed rules:
  sources always cited, some collections never used.

It is worth setting up when the same documents serve several tools, when
they change often, or when not everyone may read everything. It is not
worth it for a dozen files one person reads: attach them to the chat.

## Check the answers, not just the connection

A connected knowledge base that returns the wrong passage is worse than
none. Three checks, each one command:

- conversions that went badly:
  `"filter": {"conversionConfidence": {"lt": 0.5}}` on a search;
- how the collection should be searched: `autoindex` ends by running the
  search advisor, which stores a retrieval profile and warns about what it
  found;
- what retrieval saved: `mddb-cli gain --window 7d` reports searches run and
  tokens saved against returning whole documents.

[Why Your RAG Returns the Wrong Document](/blog/why-your-rag-returns-the-wrong-document/)
covers what to do when the passages are wrong.
