---
title: "JWT Auth and RBAC: Implementation Summary"
slug: "docs/auth-implementation-summary"
description: "Implementation notes for MDDB's JWT authentication and RBAC system: where the code lives, how a request is authenticated and authorized, and how the SEC-019 gate and SEC-020 admin scope changed it."
status: publish
---

# JWT Authentication + Authorization Implementation Summary

Authentication and role-based access control were added to MDDB in March 2026
(v2.x). Two later changes moved where permissions are checked and what
"admin" means:

- **SEC-019 (v2.18.10):** one access gate checks every REST route, gRPC
  method, MCP tool and resource, and GraphQL field before the handler runs.
  Handlers no longer decide on their own.
- **SEC-020 (v2.18.10):** only admin on `"*"` is a global admin. Admin on one
  collection manages that collection only.

This page is a map of the implementation. The rules themselves are in
[AUTHENTICATION.md](AUTHENTICATION.md) (the reference) and, for tenants, in
[MULTI_TENANCY.md](MULTI_TENANCY.md).

---

## Overview

- **Disabled by default.** Opt in with `MDDB_AUTH_ENABLED=true`. With auth
  off, every gate lets everything through.
- **JWT tokens.** HS256, signed with `MDDB_AUTH_JWT_SECRET`; expiry
  `MDDB_AUTH_JWT_EXPIRY` (default 24h).
- **API keys.** `mddb_live_` followed by 48 hex characters (24 random bytes),
  stored as a SHA-256 hash, optional expiry. Sent as `X-API-Key` or as
  `Authorization: Bearer mddb_live_…`.
- **Passwords.** bcrypt at the library's default cost.
- **RBAC.** Per-collection `read`, `write` and `admin`, for users and for
  groups; `"*"` is every collection.
- **Tenants.** A user may be confined to a namespace; see
  [MULTI_TENANCY.md](MULTI_TENANCY.md).
- **Bootstrap admin.** Created on first start from
  `MDDB_AUTH_ADMIN_USERNAME` / `MDDB_AUTH_ADMIN_PASSWORD`, with admin on `"*"`.
- **Every surface.** REST, gRPC, MCP (built into `mddbd`), GraphQL, the CLI
  (`--token`, `--api-key`, `login`) and the panel (login form).

---

## Where the code lives

All in `services/mddbd` unless noted.

| File | Role |
|------|------|
| `auth.go` | Types (`User`, `APIKey`, `Permission`, `JWTClaims`), JWT and password helpers, API key format |
| `auth_manager.go` | `AuthManager`: users, keys, permissions, groups in BoltDB; `CheckPermission`, `IsAdmin` |
| `auth_middleware.go` | HTTP authentication: validates the JWT or API key and puts the claims in the request context |
| `auth_grpc.go` | The same for gRPC, as unary and stream interceptors |
| `auth_handlers.go` | `/v1/auth/*`: login, register, API keys, `me`, permissions, users, groups |
| `access_policy.go` | `Server.authorize`: applies one access rule to a request |
| `access_gate_http.go`, `access_gate_grpc.go` | The gates: every route and RPC is registered or intercepted through them |
| `access_rules_http.go`, `access_rules_grpc.go`, `access_rules_mcp.go`, `access_rules_graphql.go` | The rule tables, one entry per entry point |
| `services/mddb-cli` | `--token`, `--api-key`, `mddb-cli login` |
| `services/mddb-panel/src/lib/auth.js`, `src/components/LoginForm.jsx` | Panel token storage and login |

---

## Request flow

### Authentication

1. **Login.** `POST /v1/auth/login` with `username` and `password` returns
   `{"token": "...", "expiresAt": ...}`.
2. **Use the token.** `Authorization: Bearer <token>`.
3. **Middleware.** Checks the signature and expiry, and puts the claims
   (username, tenant, `admin` flag) in the context. No valid credential on a
   route that needs one is a `401`.

An API key goes through the same middleware. It is looked up by hash, and
the request carries a one-hour session token for the key's user, with that
user's tenant and `admin` flag. From then on a key and a login are the same.

### Authorization (SEC-019)

After authentication, and **before the handler runs**, the gate looks up the
entry point's rule in its table and calls `Server.authorize`:

- **A request needs the permission on every collection it names**: in the
  query string, the JSON body, the gRPC message, the MCP tool arguments or the
  GraphQL arguments, after alias resolution.
- **Naming no collection means all of them**: the request needs the
  permission on `"*"`.
- **A tenant user is refused on unfiltered listings.** Where the answer
  covers every collection and is not filtered to the caller's tenant, a
  tenant user gets `403` and must name a collection.
- **An unclassified entry point is refused** while auth is on, and the
  `TestEvery*IsClassified` tests fail, so a new route, RPC, tool or field
  cannot ship without a rule.

The rule classes (public, authenticated, admin, collection, handler) and the
list of what falls in each are in
[AUTHENTICATION.md](AUTHENTICATION.md#where-permissions-are-enforced-21810-sec-019).

### Permission check

`AuthManager.CheckPermission(collection, operation)`:

1. A tenant user is refused outside its namespace, and on `"*"` for anything
   but `read`.
2. A token whose `admin` flag is set passes. That flag is set only for admin
   on `"*"` (SEC-020).
3. The user's permission on the collection.
4. The user's permission on `"*"`.
5. The permissions of each group the user belongs to, on the collection and
   then on `"*"`.
6. Otherwise `403`.

Admin-only endpoints (backup, restore, user management, encryption,
automation and the others in the admin class) need admin on `"*"`. Admin on
one collection does not reach them. See
[Collection admin vs global admin](AUTHENTICATION.md#collection-admin-vs-global-admin).

### Permission model

```text
Permission {
  username:   "alice"
  collection: "blog"    // or "*" for every collection
  read:       true
  write:      false
  admin:      false     // on "*": global admin; on "blog": manages blog only
}
```

### Storage

BoltDB buckets in the main database: `auth_users`, `auth_apikeys` (hashes
only), `auth_permissions`, `auth_groups`, `auth_group_perms`. They travel in
backups and are replicated; the replication streams are protected for that
reason (see [REPLICATION.md](REPLICATION.md#securing-replication)).

---

## Environment variables

```bash
MDDB_AUTH_ENABLED=false              # Disabled by default
MDDB_AUTH_JWT_SECRET=<random-hex>    # Required if auth enabled
MDDB_AUTH_JWT_EXPIRY=24h             # Optional, default 24h
MDDB_AUTH_ADMIN_USERNAME=admin       # Bootstrap admin, default "admin"
MDDB_AUTH_ADMIN_PASSWORD=changeme    # Required if auth enabled
```

The full list, with the hardening limits, is in
[AUTHENTICATION.md](AUTHENTICATION.md#environment-variables) and
[config.md](config.md#authentication).

---

## Usage

```bash
# Start
MDDB_AUTH_ENABLED=true \
MDDB_AUTH_JWT_SECRET=$(openssl rand -hex 32) \
MDDB_AUTH_ADMIN_USERNAME=admin \
MDDB_AUTH_ADMIN_PASSWORD=changeme \
./mddbd

# Login
TOKEN=$(curl -s http://localhost:11023/v1/auth/login \
  -d '{"username":"admin","password":"changeme"}' | jq -r .token)

# Create a user and let her read "blog" only
curl -H "Authorization: Bearer $TOKEN" http://localhost:11023/v1/auth/register \
  -d '{"username":"alice","password":"secret123"}'
curl -H "Authorization: Bearer $TOKEN" http://localhost:11023/v1/auth/permissions \
  -d '{"username":"alice","collection":"blog","read":true,"write":false,"admin":false}'
```

More scenarios, collection admin and global admin among them, are in
[AUTH_QUICKSTART.md](AUTH_QUICKSTART.md) and
[AUTHENTICATION.md](AUTHENTICATION.md#example-scenarios).

---

## Tests

- **Go:** `go test ./...` in `services/mddbd` covers the auth manager, the
  middleware, the gates (`access_gate_*_test.go`) and the completeness of the
  rule tables (`TestEveryHTTPRouteIsClassified`,
  `TestEveryGRPCMethodIsClassified`, `TestEveryMCPToolIsClassified`,
  `TestEveryGraphQLFieldIsClassified`).
- **Scripts:** `scripts/tests/test-auth.sh` (login, keys, RBAC and the CLI
  against a running server), `scripts/tests/test-mcp.sh`,
  `scripts/tests/test-panel.sh`.
