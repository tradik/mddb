---
title: "Multi-Tenancy"
slug: "docs/multi-tenancy"
description: "Native multi-tenancy in MDDB — tenant namespace isolation for collections, enforced across HTTP, gRPC, GraphQL and MCP."
status: publish
---

# Multi-Tenancy

MDDB provides native multi-tenancy through **namespace isolation**: a tenant is a
namespace over collections, enforced centrally in the authorization layer. Every
API surface — HTTP, gRPC, GraphQL and MCP — inherits the isolation automatically,
because they all authorize through the same permission check.

## Model

- A **tenant** is an identifier (`[a-zA-Z0-9_-]{1,64}`) assigned to a user at
  creation time.
- A tenant user can only access collections named `<tenant>/<name>`
  (e.g. tenant `acme` owns `acme/notes`, `acme/kb/articles`).
- Users without a tenant are **global users** and behave exactly as before —
  existing deployments need zero configuration changes.
- Tenant users can **never** hold the global admin role. Their JWT always
  carries `admin: false`, so admin-only endpoints (user management, backups,
  automation, replication) are closed to them by construction.
- A tenant user with a wildcard (`*`) permission acts as a **tenant admin**:
  full read/write over every collection inside their namespace — and nothing
  outside it.

## Quick start

```bash
# 1. As a global admin, create a tenant-confined user
curl -X POST http://localhost:11023/v1/auth/register \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"username":"alice","password":"s3cret","tenant":"acme"}'

# 2. Grant tenant-wide access (the "tenant admin" pattern)
curl -X POST http://localhost:11023/v1/auth/permissions \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"username":"alice","collection":"*","read":true,"write":true}'

# 3. Alice logs in and works inside her namespace
curl -X POST http://localhost:11023/v1/auth/login \
  -d '{"username":"alice","password":"s3cret"}'
# → token with {"tenant":"acme"}

curl -X POST http://localhost:11023/v1/add \
  -H "Authorization: Bearer $ALICE_TOKEN" \
  -d '{"collection":"acme/notes","key":"first","lang":"en","contentMD":"# Hello"}'

# Access outside the namespace is rejected:
curl -X POST http://localhost:11023/v1/get \
  -H "Authorization: Bearer $ALICE_TOKEN" \
  -d '{"collection":"other/notes","key":"x","lang":"en"}'
# → 403 forbidden
```

API keys created by a tenant user carry the tenant automatically — MCP clients
and gRPC clients authenticated with such a key are confined the same way.

## Scoped listings

Listing endpoints return only the caller's namespace:

- `POST /v1/stats` — collection stats and totals are filtered to the tenant.
- `GET /v1/vector-stats` (HTTP and gRPC) and `GET /v1/storage` — only tenant
  collections; `?collection=` naming another tenant's collection answers
  `403` (v2.18.6+; until then they listed every collection).
- `POST /v1/collection-config/list` — only tenant collections.
- Curation rule listing, schema listing (gRPC) — filtered likewise.

Every other request that names no collection is about all of them, and a
tenant user is refused it with `403` (v2.18.10+, SEC-019): server-wide
`/v1/geo-stats`, `/v1/schema/list`, `/v1/gain`, `/v1/bulk-ingest-jobs` and
`/v1/indexing-status` without `?collection=`, the webhook list, and the
same operations over gRPC, MCP and GraphQL. Name the collection instead. The
memory store (`/v1/memory/*`) is shared by every tenant and is closed to
tenant users. See
[AUTHENTICATION.md](AUTHENTICATION.md#where-permissions-are-enforced-21810-sec-019)
for how every entry point is classified.

## Permissions within a tenant

Standard per-collection permissions work unchanged inside the namespace:

```bash
# bob (tenant acme) may only read acme/docs
curl -X POST http://localhost:11023/v1/auth/register \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"username":"bob","password":"s3cret","tenant":"acme"}'
curl -X POST http://localhost:11023/v1/auth/permissions \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"username":"bob","collection":"acme/docs","read":true}'
```

For a tenant user, `"collection": "*"` means "every collection of my tenant" —
the namespace gate is applied before the permission match, so the wildcard can
never leak across tenants.

## Quotas (v2.18.9+)

Isolation keeps tenants out of each other's data; quotas keep one tenant — or
one agent writing in a loop — from filling the disk, exhausting RAM with
vector indexes, or running up the embedding provider's bill for everyone.

A collection's config may carry a `quota`. Every field is optional and `0`
means no limit; a collection without a quota behaves exactly as before.

| Field | Limits | When exceeded |
|---|---|---|
| `maxDocuments` | Documents in the collection | The write is refused: `507 Insufficient Storage` |
| `maxBytes` | Stored bytes of its documents (after compression and encryption; revisions and indexes not counted) | `507` |
| `maxVectors` | Chunk vectors in its index | New or growing documents are refused: `507` |
| `embeddingCallsPerDay` | Embedding provider calls caused by its documents per UTC day | Documents are **stored**; their embedding waits (see below) |
| `embeddingTokensPerDay` | Estimated tokens (characters / 4) sent to the provider per UTC day | As above |

```bash
# As a global admin: cap acme/notes at 10,000 documents, 50 MB and 2,000 embedding calls a day
curl -X PUT http://localhost:11023/v1/collection-config \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"collection":"acme/notes","quota":{"maxDocuments":10000,"maxBytes":52428800,"embeddingCallsPerDay":2000}}'
```

**Who may set it.** A quota is a limit on the tenant, so with authentication
on only a **global admin** may change one (`403` / `PERMISSION_DENIED`
otherwise). A tenant can still write the rest of its collection config: a
write that omits `quota`, or sends the stored one unchanged, leaves it alone.
`"quota": {}` removes it. The same applies over gRPC (`SetCollectionConfig.quota`)
and MCP (`set_collection_config` with a `quota` object).

**One gate, every surface.** The check runs inside the write transaction of
the single write path every transport shares, so REST, gRPC, MCP, GraphQL,
batch and ingest refuse the same writes the same way:

| Surface | Storage limit | Embedding budget (reindex) |
|---|---|---|
| REST | `507` with `{"code":"QUOTA_EXCEEDED","resource":…,"limit":…,"used":…}` | `429` + `Retry-After` |
| gRPC | `RESOURCE_EXHAUSTED` | `RESOURCE_EXHAUSTED` (message says when to retry) |
| MCP | tool error with the same object as `structuredContent` | same, `status: 429` |
| GraphQL | error with the object in `extensions` | same |

A batch that would pass a limit is refused as a whole — nothing in it is
written. A write that does not grow the collection (an update to a shorter
body) is never refused, so a collection over a lowered limit can be trimmed.
The check is exact: BoltDB has one writer per file, so two concurrent writers
cannot both take the last slot. Usage is cached per collection and recounted
after any other write to the same file.

**The embedding budget stops the worker, not the write.** When a collection
has spent its day's budget, new and changed documents are stored and stay
`pending` in `/v1/indexing-status` (GO-042) until midnight UTC, or until the
quota is raised (checked every minute). A document that starts embedding is
finished, so a day can pass the limit by one document's chunks. `vector-reindex`
— which exists to spend embedding calls — is refused with `429` and
`Retry-After` instead, and one that runs out part way stops and reports how
many documents it left. Counters live in memory; a restart starts the day
again. Query embeddings for searches are not counted.

**Seeing it coming.** `/metrics` reports `mddb_collection_fullness{collection,resource}`
and `mddb_embedding_budget_used{collection}` (0–1, see
[TELEMETRY.md](TELEMETRY.md#quotas-v2189)), and every refusal fires the
`quota.exceeded` webhook — at most once a minute per collection and resource.

Server-wide, `MDDB_DISK_RESERVE_BYTES` and `MDDB_VECTOR_RAM_LIMIT_BYTES`
protect the node itself ([config.md](config.md#quotas-and-reserves-v2189)).

## Design notes

- **Single choke point.** The namespace gate lives in the same permission check
  every handler already calls, so new endpoints inherit isolation by default
  rather than opting into it.
- **Backward compatible.** Single-tenant deployments (users without a tenant)
  are untouched: no new configuration, no key-format migration, no API changes.
- **Backups.** Whole-database backup/restore remains a global-admin operation.
  Per-tenant export can be achieved with the standard `/v1/export` endpoint on
  the tenant's collections.

See also: [AUTHENTICATION.md](AUTHENTICATION.md) for users, API keys, groups
and permissions.
