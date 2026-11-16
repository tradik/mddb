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

Isolation keeps tenants out of each other's data; quotas keep one collection —
a tenant's, or one an agent writes to in a loop — from filling the disk,
exhausting RAM with vector indexes, or running up the embedding provider's
bill for everyone.

**A collection quota caps one collection.** A tenant with ten collections
capped at 10,000 documents each may store 100,000. To bound a tenant as a
whole — however many collections it creates — set a
[tenant quota](#tenant-quota) (v2.19.8+); both apply, and a write must pass
each.

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

**Who may set it.** A quota limits what a tenant may store in the
collection, so with authentication on only a **global admin** may change one (`403` / `PERMISSION_DENIED`
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

## Tenant limits and isolation (v2.19.8+)

Namespaces keep tenants out of each other's data, but every tenant shares one
process: its disk, its RAM, its search slots, its background workers. Without
limits on the tenant itself, one tenant importing a million documents or
firing a burst of searches slowed every other tenant down. Four mechanisms
bound what one tenant can take from the others. A tenant nobody configured,
a user without a tenant, and a server that sets none of the variables below
behave exactly as before.

A tenant is the namespace before the first `/` of a collection name, as
everywhere on this page: the limits follow the collection, whoever writes to
it. Per-tenant encryption keys are not part of this; the server has one key.

### Tenant quota

A tenant quota caps what **all** of the tenant's collections may use
together. Every field is optional; `0` is no limit.

| Field | Limits | When exceeded |
|---|---|---|
| `maxDocuments` | Documents of all the tenant's collections | The write is refused: `507`, `resource: documents` |
| `maxBytes` | Their stored bytes, counted as a collection quota's `maxBytes` | `507`, `bytes` |
| `maxVectors` | Their chunk vectors | New or growing documents are refused: `507`, `vectors` |
| `maxCollections` | Collections holding documents (or configured) | A document for a further, empty collection is refused: `507`, `collections` |
| `embeddingCallsPerDay`, `embeddingTokensPerDay` | The tenant's embedding budget per UTC day, spent by all its collections (rerank calls count, as they do for a collection budget) | Documents are stored; their embedding is parked until the budget resets, as for a collection budget. A reindex is refused with `429` |
| `vectorRamShare` | Share (0–1) of `MDDB_VECTOR_RAM_LIMIT_BYTES` the tenant's in-memory vectors may take (default `MDDB_TENANT_VECTOR_RAM_SHARE`) | Writes that would add vectors to RAM are refused: `507`, `vectorRam`. Nothing without the server limit |

The refusal is the same `QUOTA_EXCEEDED` object as a collection quota's, on
every surface (REST `507`, gRPC `RESOURCE_EXHAUSTED`, the MCP tool error's
`structuredContent`, GraphQL `extensions`), with `"tenant": "<id>"` added. The
check runs in the same write gate, so batch, ingest and every transport are
covered alike.

**How exact it is.** The collection being written is counted exactly, inside
its write transaction. The tenant's other collections are counted as of at
most five seconds ago — recounting all of them on every write would make a
tenant's writes cost as much as its size. Two writers adding at the same
moment to two different collections of one tenant can therefore pass a tenant
limit by what they add in that window. The vector RAM a tenant takes is an
estimate: its share of the vectors times the in-memory index's measured size.

### Request limits

Every request that names a collection passes its surface's access gate —
REST, gRPC, an MCP tool call, each root field of a GraphQL operation — and is
counted there, after it is authorized:

| Limit | Default | Counts |
|---|---|---|
| `readPerSecond`, `writePerSecond` | `MDDB_TENANT_READ_RPS`, `MDDB_TENANT_WRITE_RPS` | The tenant's requests per second over all its credentials. Searches are reads |
| `keyReadPerSecond`, `keyWritePerSecond` | `MDDB_KEY_READ_RPS`, `MDDB_KEY_WRITE_RPS` | The same per credential: each API key, or each user signed in with a password. Once set in the environment they apply to global users' credentials too |
| `maxConcurrentSearches` | `MDDB_TENANT_MAX_CONCURRENT_SEARCHES` | Search slots (`MDDB_SEARCH_MAX_CONCURRENT`, GO-053) the tenant may hold at once. The rest stay free for the other tenants |
| `maxConcurrentWrites` | `MDDB_TENANT_MAX_CONCURRENT_WRITES` | Write requests running at once |
| `maxConcurrentReindexes` | `MDDB_TENANT_MAX_CONCURRENT_REINDEXES` | Vector reindexes running at once |

In a tenant's `limits`, `0` (or leaving the field out) takes the server
default and `-1` lifts it for this tenant. A rate allows one second's worth
at once. A request waits up to `MDDB_TENANT_QUEUE_TIMEOUT_MS` (2000) for a
search or write slot. A request over a limit is refused with `429`, a
`Retry-After` header and:

```json
{"error":"rate limited: tenant \"acme\" is at its writeRate limit of 50, retry after 1s (429)",
 "code":"TENANT_LIMIT","status":429,"tenant":"acme","scope":"tenant",
 "limit":"writeRate","max":50,"retryAfterSeconds":1}
```

`scope` is `tenant` or `key` (one credential); `limit` is `readRate`,
`writeRate`, `searchConcurrency` or `writeConcurrency`. gRPC answers
`RESOURCE_EXHAUSTED` with the message, an MCP tool error carries the object as
`structuredContent`, a GraphQL error carries it in `extensions`. A reindex over
its limit is not refused: it waits for a slot, and an async reindex job shows
`"status": "queued"` meanwhile. `MDDB_REINDEX_MAX_CONCURRENT` bounds reindexes
over all tenants the same way.

These limits come before, and are separate from, the server-wide
`MDDB_RATE_LIMIT_*` limiter, which counts per IP or per user.

### Fair background queues

Metadata indexing and embedding run in background workers that every tenant
shares. Each tenant now has its own queue, and the workers take one tenant's
job after another's, so a tenant with ten thousand jobs waiting delays another
tenant's job by at most one of its own per worker, not by all of them.

- **Metadata index**: up to 1,000 jobs per tenant; a tenant whose queue is
  full indexes its next document in its own request, as before — the slow path
  falls on the tenant that filled the queue.
- **Embeddings**: up to `MDDB_EMBEDDING_QUEUE_SIZE` jobs per tenant. A write
  whose tenant's queue is full waits up to `MDDB_EMBEDDING_QUEUE_WAIT`; then
  the job is **parked**, not dropped: the document stays `pending` in
  `/v1/indexing-status` and its job goes back on the queue as soon as there is
  room. Only past `MDDB_EMBEDDING_PARK_MAX` parked jobs (100,000) is a job
  dropped and counted as before.

Collections outside any namespace share one queue, which takes its turn like
a tenant.

### Seeing it

- `GET /v1/tenants/{id}/usage` — the tenant's quota, limits, use, queue
  depths and waits, slots in use and refusals (below).
- `/metrics` — `mddb_tenant_usage`, `mddb_tenant_fullness`,
  `mddb_tenant_queue_depth`, `mddb_tenant_refusals_total`,
  `mddb_tenant_queue_wait_seconds_total`, `mddb_tenant_queue_jobs_total`
  ([TELEMETRY.md](TELEMETRY.md#tenants-v2198)).
- The panel's **Tenants** page ([PANEL.md](PANEL.md)).
- A tenant quota refusal is also a `quota.exceeded` webhook and a log line,
  at most once a minute per tenant, collection and resource.

### The tenant endpoints

| Method | Path | Who | Does |
|---|---|---|---|
| GET | `/v1/tenants` | global admin | Every configured tenant and every tenant a user is assigned to, each with its usage report |
| GET | `/v1/tenants/{id}` | global admin, or the tenant's own admin | `{"tenant","quota","limits","effectiveLimits"}` — `effectiveLimits` are the limits in force, `0` meaning none |
| PUT | `/v1/tenants/{id}` | global admin | Sets `quota` and/or `limits`. A part left out is kept; `null` or `{}` removes it |
| DELETE | `/v1/tenants/{id}` | global admin | Removes both; server defaults still apply |
| GET | `/v1/tenants/{id}/usage` | global admin, or the tenant's own admin | The usage report |

The tenant's own admin is a user of the tenant holding `read` on `*` — the
tenant-admin pattern above. A tenant can read its limits but never change
them: a tenant able to raise its own quota would not have one.

```bash
# As a global admin: cap tenant acme at 100,000 documents in at most 20
# collections, 50 writes a second, and two searches at once.
curl -X PUT http://localhost:11023/v1/tenants/acme \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"quota":{"maxDocuments":100000,"maxCollections":20},
       "limits":{"writePerSecond":50,"maxConcurrentSearches":2}}'

# As acme's tenant admin, or a global admin:
curl http://localhost:11023/v1/tenants/acme/usage -H "Authorization: Bearer $TOKEN"
```

```json
{"tenant":"acme","quota":{"maxDocuments":100000,"maxCollections":20},
 "limits":{"writePerSecond":50,"maxConcurrentSearches":2},
 "effectiveLimits":{"writePerSecond":50,"maxConcurrentSearches":2},
 "vectorRamLimitBytes":0,
 "usage":{"documents":4210,"bytes":9123456,"vectors":15872,"collections":3,
          "vectorRamBytes":24379392,"embeddingCalls":0,"embeddingTokens":0},
 "collectionNames":["acme/docs","acme/kb","acme/notes"],
 "queues":{"index":{"depth":0,"taken":{"jobs":4210,"waitSecondsTotal":1.8,"maxWaitSeconds":0.04}},
           "embedding":{"depth":12,"taken":{"jobs":4198,"waitSecondsTotal":96.1,"maxWaitSeconds":2.3}},
           "embeddingParked":{"depth":0,"taken":{"jobs":0,"waitSecondsTotal":0,"maxWaitSeconds":0}}},
 "inFlight":{"reindexes":0,"searches":1,"writes":0},
 "refusals":{"tenant.writeRate":3}}
```

Configurations are stored in the main database (bucket `tenants`), so backups
carry them and a restore brings them back. Counters (queue waits, refusals,
embedding spend) live in memory and start again after a restart. A
replication follower receives the configuration through the binlog but reads
it only at its next start.

## Chat conversations

Persistent chat conversations (v2.19.0,
[API.md](API.md#chat-conversations-v1chatconversations)) follow the
namespace: a tenant user's conversations are stored in
`<tenant>/chat_conversations`, a global user's in `chat_conversations`. The
host API takes no collection — the server derives it from the caller — so a
tenant's chat host needs `read` and `write` on `acme/chat_conversations`
(or the tenant's `*`), reaches only that tenant's conversations, and the
same `sub` in two tenants is two unrelated users. A quota set on
`<tenant>/chat_conversations` limits the conversations like any other
collection, and they count towards the tenant's quota like any other
collection of the tenant.

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
