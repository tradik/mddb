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
