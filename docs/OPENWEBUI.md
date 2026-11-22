---
title: "Open WebUI Engines"
slug: "docs/openwebui"
description: "Point Open WebUI's external web search, external reranker and external document loader settings at MDDB: three endpoints in the shapes Open WebUI sends, each a feature module that starts off."
status: publish
---

# Open WebUI Engines

> v2.19.10+ (#449, #450, #451). Three endpoints, each a feature module that is
> **off** until an administrator switches it on.

Open WebUI's admin settings can hand three steps to an external service: web
search, reranking and document extraction. MDDB answers all three in the
request and response shapes Open WebUI sends, so the same server that holds
the documents can search them, score passages and convert uploads.

| Open WebUI step | MDDB endpoint | Module | What MDDB runs |
|---|---|---|---|
| Web search | `POST /v1/web-search` | `webSearch` | cross-collection full-text (or hybrid) search over the collections the key may read |
| Reranking | `POST /v1/rerank` | `rerankApi` | the reranking model the search rerank stage uses (RAG-011) |
| Content extraction | `PUT /v1/extract/process` | `extractApi` | the converters of `/v1/upload`, without storing anything |

## Before you start

1. **Switch the modules on.** Panel → Features → Integrations, or:

   ```bash
   for m in webSearch rerankApi extractApi; do
     curl -s -X PUT "http://mddb:11023/v1/features/$m" \
       -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
       -d '{"enabled":true}'
   done
   ```

   A module that is off answers `404 {"error":"feature disabled","feature":"<module>"}`,
   which Open WebUI shows as a failed step. `MDDB_FEATURES_FORCE_OFF` keeps a
   module off whatever the switch says.
2. **Make an API key** for the user Open WebUI acts as (Panel → API Keys, or
   `POST /v1/auth/api-key`). Open WebUI sends it as `Authorization: Bearer`;
   an `mddb_live_…` key works there like a JWT. The key's user decides what
   web search may read.
3. Use the URL Open WebUI's server reaches MDDB at (in Docker Compose, the
   service name: `http://mddb:11023`). The panel's LLM Connections → Open
   WebUI tab prints the three URLs for the server it is connected to.

## Web search

Open WebUI: **Admin Panel → Settings → Web Search**

| Setting | Value |
|---|---|
| Web Search Engine | `external` |
| External Web Search URL | `http://mddb:11023/v1/web-search` |
| External Web Search API Key | the MDDB API key |
| Bypass Web Loader | on |

Environment variables: `WEB_SEARCH_ENGINE=external`,
`EXTERNAL_WEB_SEARCH_URL`, `EXTERNAL_WEB_SEARCH_API_KEY`,
`BYPASS_WEB_SEARCH_WEB_LOADER=true`.

With *Bypass Web Loader* off, Open WebUI fetches every result's `link` itself
and uses the page it gets. A document without its own URL links to
`/v1/doc-meta` on MDDB, which needs the key Open WebUI does not send to the
links it fetches, so such a result is dropped. With it on, the `snippet`
(the passage around the query's words, up to 4096 characters) is the content.

What it searches:

- every collection the key's user may read, a tenant user's own only, without
  the server's memory and chat stores;
- `?collections=docs,kb` on the URL narrows it; each named collection must be
  readable, or the search is `403`. The URL is the only per-deployment knob
  Open WebUI offers, so put the choice there;
- `MDDB_WEBSEARCH_COLLECTIONS=docs,kb` narrows the default server-wide
  ([config.md](config.md));
- `?searchType=hybrid` runs hybrid search instead of full-text.

Each result is `{"link", "title", "snippet"}`: `link` is the document's
`url`, `source_url` or `link` metadata when it is http(s), else
`http://<host>/v1/doc-meta?collection=…&key=…&lang=…`; `title` is
`meta.title`, else the key. The result count (`WEB_SEARCH_RESULT_COUNT`)
is sent as `count`.

## Reranking

Open WebUI: **Admin Panel → Settings → Documents**. Open WebUI loads an
external reranker only with Hybrid Search on (and embedding and retrieval not
bypassed).

| Setting | Value |
|---|---|
| Hybrid Search | on |
| Reranking Engine | External |
| API Base URL | `http://mddb:11023/v1/rerank` (the whole URL: Open WebUI posts to it as given) |
| API Key | the MDDB API key |
| Reranking Model | any name, e.g. `reranker` (Open WebUI needs one; MDDB does not use it) |

Environment variables: `RAG_RERANKING_ENGINE=external`,
`RAG_EXTERNAL_RERANKER_URL`, `RAG_EXTERNAL_RERANKER_API_KEY`,
`RAG_RERANKING_MODEL`, `ENABLE_RAG_HYBRID_SEARCH=true`.

MDDB needs a reranking model of its own: a stored configuration of kind
`rerank` (`POST /v1/embedding-configs`) or `MDDB_RERANK_PROVIDER=http` with
`MDDB_RERANK_API_URL` ([config.md](config.md#model-reranking-v2198-rag-011)).
Without one the endpoint answers `503`, and Open WebUI treats reranking as
failed. The server runs one model at a time, so the request's `model` does not
choose one; the answer names the model that scored.

Every document gets a result, so Open WebUI can read one score per document:
one the model did not score gets a score below all the others. Each document
is read up to 2048 characters, as in the search stage; more than 200
documents or 2 MiB of text is `400`. A failing model is `502` with
`timeout`, `invalidResponse` or `error`.

## Content extraction

Open WebUI: **Admin Panel → Settings → Documents**

| Setting | Value |
|---|---|
| Content Extraction Engine | External |
| Document Loader URL | `http://mddb:11023/v1/extract` |
| API Key | the MDDB API key |

Environment variables: `CONTENT_EXTRACTION_ENGINE=external`,
`EXTERNAL_DOCUMENT_LOADER_URL`, `EXTERNAL_DOCUMENT_LOADER_API_KEY`.

Enter the URL **without** `/process`: Open WebUI adds it and sends
`PUT /v1/extract/process` with the file as the body, its type in
`Content-Type` and its name in `X-Filename`.

- Formats: md, txt, html, pdf, docx, odt, rtf, xlsx, ods, csv, tsv, pptx,
  odp, epub, eml, mbox, yaml, log, tex. The type comes from `Content-Type`,
  then the file name's extension; PDF and the zip formats are also
  recognised by content. Anything else is `415` with the list; a file of a
  supported format that fails to convert is `422`.
- The answer is `{"page_content", "metadata"}`: the Markdown, and `source`
  (the file name), `converter` (the format), `mime`, `conversionConfidence`
  (0–1) and `conversionWarnings`. A mailbox, or an email with attachments,
  answers a list: the file first, then each part with `parent`.
- Size: 10 MB, or `?maxSize=<bytes>` on the URL up to 100 MB, and never more
  than `MDDB_MAX_BODY_BYTES` (32 MiB by default). Over is `413`.
- Nothing is stored: the document becomes MDDB data only if it is also
  uploaded to a collection.

## Checking it by hand

```bash
KEY=mddb_live_…
curl -s -X POST http://mddb:11023/v1/web-search -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' -d '{"query":"pump reset","count":3}'
curl -s -X POST http://mddb:11023/v1/rerank -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"reranker","query":"pump reset","documents":["Hold the button.","Oil monthly."],"top_n":2}'
curl -s -X PUT http://mddb:11023/v1/extract/process -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/pdf' -H 'X-Filename: report.pdf' --data-binary @report.pdf
```

## Limits

- Web search runs on the node that receives it: in a cluster with several
  node groups ([CLUSTER.md](CLUSTER.md)) it searches the collections that
  node holds.
- Open WebUI's optional `X-OpenWebUI-User-*` and chat-id headers are not
  read; what a search may read follows the API key's user.
- The endpoints are HTTP only: no gRPC, MCP or GraphQL counterpart.

Reference: [API.md](API.md#open-webui-engines-v1web-search-v1rerank-v1extractprocess),
`docs/openapi.yaml` in the repository.
