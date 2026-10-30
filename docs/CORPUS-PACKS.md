---
title: "Corpus Packs"
slug: "docs/corpus-packs"
description: "Build a collection once — documents, full-text index, vectors — and install it read-only on any MDDB server with one command: mddb-cli corpus build, install, list and remove."
status: publish
---

# Corpus Packs

An agent often needs knowledge that is not in its repository: a library's
documentation, an RFC, a specification, reference code. Building that
collection — ingest, embeddings, full-text index — gives the same result on
every machine. A **corpus pack** lets it be built once and installed
anywhere, without re-embedding or reindexing (since 2.18.10).

```bash
# on the machine that has the collection
mddb-cli corpus build k8s-docs --license CC-BY-4.0 --version 1.31 \
  --source CC-BY-4.0=https://github.com/kubernetes/website@release-1.31

# on any other server
mddb-cli corpus install ./k8s-docs-1.31.mddbpack
mddb-cli corpus install https://packs.example.org/k8s-docs-1.31.mddbpack --sha256 <sum>
mddb-cli corpus list
mddb-cli corpus remove k8s-docs
```

## What a pack is

A `.mddbpack` file is a zstd-compressed tar with two members, in this order:

| Member | Holds |
|---|---|
| `manifest.json` | what the pack is, where it came from, under which licence |
| `collection.db` | the collection's bbolt file: documents, metadata index, revisions, key lookups, full-text postings and statistics, vectors and their provenance, geo points |

`collection.db` is exactly the file the [per-collection layout](STORAGE-LAYOUT.md)
keeps for a collection: every collection bucket, and only this collection's
keys. Nothing from the server's own data — users, keys, automations,
webhooks, other collections — is ever in a pack.

### manifest.json

```json
{
  "format": 1,
  "name": "k8s-docs",
  "version": "1.31",
  "description": "Kubernetes documentation, release 1.31",
  "mddbVersion": "2.18.10",
  "schemaVersion": 1,
  "createdAt": "2026-10-30T12:00:00Z",
  "license": "CC-BY-4.0",
  "sources": [
    {"repo": "https://github.com/kubernetes/website", "commit": "release-1.31", "license": "CC-BY-4.0"}
  ],
  "embedding": {"provider": "openai", "model": "text-embedding-3-small", "dims": 1536},
  "docCount": 1874,
  "sha256": "…of collection.db…",
  "bytes": 48693248
}
```

| Field | Meaning |
|---|---|
| `name` | The collection the pack installs as. The keys inside the file carry this name, so a pack cannot be installed under another one. |
| `mddbVersion`, `schemaVersion` | The release that built the pack and the on-disk format of `collection.db` ([Schema version](STORAGE-LAYOUT.md#schema-version)). A server installs a pack whose format it knows, migrating an older one; it refuses a newer one. |
| `license`, `sources[].license` | Mandatory. See [Licences](#licences). |
| `embedding` | How the vectors were made, or `null` for a full-text-only pack. See [Embedding model](#embedding-model). |
| `sha256`, `bytes` | Of `collection.db`. An install refuses a file that does not match. |

## Building

`mddb-cli corpus build <collection>` asks the server (`POST /v1/corpus/build`,
admin permission) for a consistent snapshot of the collection:

- in the per-collection layout, the collection's file is copied through a
  read transaction, verified as a backup copy is verified ([BACKUP.md](BACKUP.md));
- in the single layout, the collection's keys are copied out of the shared
  file in one read transaction.

Either way the snapshot sees one commit and nothing after it. The server
streams the pack; the CLI writes it to `-o` (default
`<collection>[-<version>].mddbpack`) through a temporary file and prints its
sha256, which is what `install --sha256` checks.

| Flag | Meaning |
|---|---|
| `--license` | Licence of the pack as a whole. **Required.** |
| `--source LICENSE=REPO[@COMMIT]` | A source of the documents; repeatable. Each needs its licence. |
| `--version`, `--description` | Recorded in the manifest. |
| `-o` | The file to write. |

A collection is refused when it cannot travel: an **encrypted** collection
(its documents are sealed with this server's key) and one whose documents
live in **S3 or memory** (`storageBackend`) rather than in its bbolt file.

## Installing

`mddb-cli corpus install <file|https-url>` uploads the pack to
`POST /v1/corpus/install` (multipart, field `pack`, admin permission, refused
in read-only mode). The server never takes a path to read. A URL must be
`https://`, and a redirect may not leave TLS. `--sha256` checks the whole pack
file before anything is sent.

A client that cannot upload a file — an MCP agent — names an `https://` URL
instead, and the server downloads the pack itself (v2.18.11): the
`corpus_install` MCP tool, or the `InstallCorpusPack` RPC with `url` in its
first message. The same rules apply as in the CLI: https only, no redirect out
of TLS, and an optional `sha256` of the pack file, checked on a staged copy
before anything is installed. The download goes through the server's SSRF
guard (private, loopback and link-local addresses are refused unless
`MDDB_OUTBOUND_ALLOW_PRIVATE` or `MDDB_OUTBOUND_ALLOWLIST` says otherwise) and
is refused past `MDDB_CORPUS_MAX_BYTES`.

The server then, before anything changes:

1. unpacks only `manifest.json` then `collection.db` — any other member, a
   member out of order, or a `collection.db` over `MDDB_CORPUS_MAX_BYTES`
   (default 64 GiB) is refused;
2. checks the manifest: format, name, licences, storage format;
3. checks `collection.db` against the manifest's sha256;
4. runs bbolt's integrity check on it, as a restore does;
5. refuses it unless every bucket is a collection bucket and every key belongs
   to the pack's collection — a pack that carried keys of another collection
   would write into it — and it holds the documents the manifest counts.

Then the collection's config is written, read-only, and its data put in place,
**without a restart**:

- **per-collection layout:** the file is renamed into `<database>.d/` and
  opened beside the others;
- **single layout:** there is no file of its own to attach, so the pack's keys
  are copied into the shared file, as `-merge-collections` copies a collection
  file back. Removing the pack later frees its pages inside the file, not the
  file's size ([STORAGE-LAYOUT.md](STORAGE-LAYOUT.md#corpus-packs)).

The pack's vectors are loaded into the in-memory indexes and its geo points
into the geo indexes; full-text search answers from the pack's own postings
and statistics. A collection of the same name that already holds data is not
overwritten: the install answers 409.

## Read-only collections

An installed pack is a read-only collection: its config carries
`readOnly: true` and `corpus` (the manifest's name, version, licence and
checksum, and `installedAt`). A config write keeps both.

Every write to the collection's data is refused, on every surface, by one
check: the one that picks the file a collection's data is written to. That
covers REST, gRPC, MCP, GraphQL, batch, ingest, updates, deletes, full-text
and vector reindexing, TTL and automations alike:

| Surface | Answer |
|---|---|
| REST | `403` `{"code":"READ_ONLY","collection":"…"}` |
| gRPC | `PERMISSION_DENIED` |
| MCP | tool error, `structuredContent.code = "READ_ONLY"` |
| GraphQL | error with `extensions.code = "READ_ONLY"` |

Reads, searches and the other collections are unaffected.

## Embedding model

A pack's vectors are only comparable with queries embedded by the same
model. When the server's provider differs — another model, other dimensions,
or no provider at all — the pack is installed with **`vectorsDisabled`** and
the install answers with a warning:

- its vectors are not loaded, at install or at a later start;
- vector search finds nothing in the collection;
- hybrid search answers from the full-text half.

Nothing degrades silently: the warning is in the install's answer, in
`mddb-cli corpus list` ("full text only") and in the collection's config.
Configure the pack's model (`embedding` in the manifest) and reinstall to
search its vectors.

## Removing

`mddb-cli corpus remove <name>` (`POST /v1/corpus/remove`) deletes the pack's
documents, index entries, vectors, geo points, its file in the per-collection
layout, and its config. It removes only packs; a collection written by hand is
deleted with `/v1/delete-collection`. While a pack is installed, its name is
taken: `/v1/delete-collection` on it answers 403 like any other write, and
so does `DELETE /v1/collection-config` — the config is what keeps the pack
read-only.

## Licences

The manifest **must** carry the licence of the pack and of each source, and
`build` refuses without `--license`. Pack only what may be redistributed —
documentation under an open licence, your own code, public-domain texts — and
name the terms. An install refuses a pack without them.

## Pack or backup

| | Corpus pack | Backup |
|---|---|---|
| Holds | one collection | the whole database |
| Server data (users, keys, automations) | never | yes |
| Installed | beside the existing collections, read-only | replaces the database |
| Restart | no | no |
| Meant for | sharing knowledge between servers | recovering one server |

## Limits

- **Collection settings travel only as far as the data.** Synonyms, stop
  words, spelling dictionaries, schemas, retrieval profiles and response
  prompts live in the server's own file and are not in a pack; set them after
  installing.
- **Replication.** An install writes the pack's data without the binlog, so a
  follower does not receive it; its config does reach the follower. Install
  the pack on each server.
- **A catalog** (`corpus search` over a published list of packs) is planned
  once there are packs worth listing; for now, packs are shared as files or
  URLs.

## Settings

| Variable | Default | Meaning |
|---|---|---|
| `MDDB_CORPUS_MAX_BYTES` | `68719476736` (64 GiB) | The largest `collection.db` an install unpacks, and the largest pack the server downloads or receives over gRPC |

## API

| Method | Path | |
|---|---|---|
| `GET` | `/v1/corpus` | installed packs |
| `POST` | `/v1/corpus/build` | `{"collection","license","version","description","sources"}` → `application/zstd` |
| `POST` | `/v1/corpus/install` | multipart, field `pack` → `{"name","docCount","vectorsDisabled","warnings"}` |
| `POST` | `/v1/corpus/remove` | `{"name"}` |

See the [REST API reference](/docs/api/reference/), generated from `openapi.yaml`, for the full schemas.

### MCP (v2.18.11)

| Tool | Permission | |
|---|---|---|
| `corpus_list` | read, filtered to the caller's tenant (as `GET /v1/corpus`) | read-only |
| `corpus_install` | admin | `{"url": "https://…", "sha256": "…"}`; open-world, not destructive |
| `corpus_remove` | admin | `{"name"}`; destructive, confirmed under `MDDB_MCP_CONFIRM_DESTRUCTIVE`, which asks for the name to be typed |

All three are in the `admin` toolset. There is no build tool: a build's
product is the pack file, which a tool answer would have to carry base64 in
the model's context. Build with the CLI, REST or gRPC.

### gRPC (v2.18.11)

| RPC | Permission | |
|---|---|---|
| `ListCorpusPacks` | read, filtered to the tenant | `ListCorpusPacksResponse{packs, total}` |
| `InstallCorpusPack` | admin | client stream: the first message holds `url` (+ `sha256`), or the pack as `data` chunks with an optional `sha256` |
| `RemoveCorpusPack` | admin | `{name}` → `{status, name, deleted}` |
| `BuildCorpusPack` | admin | server stream of `CorpusPackChunk`; the first carries `filename` and `doc_count` |

Errors: `INVALID_ARGUMENT` for a pack or request refused as it is,
`NOT_FOUND`, `ALREADY_EXISTS` for a name already holding data,
`PERMISSION_DENIED` for a non-admin or a read-only server. See
[GRPC.md](GRPC.md#corpus-packs-v21811).
