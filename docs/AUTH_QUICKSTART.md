---
title: "Authentication Quick Start"
slug: "docs/auth-quickstart"
description: "Turn on MDDB authentication in five minutes: create the first admin user, issue a JWT, and lock a collection down with role-based permissions."
status: publish
---

# Authentication Quick Start

5-minute guide to get started with MDDB authentication.

## Start with Auth Enabled

```bash
cd services/mddbd

MDDB_AUTH_ENABLED=true \
MDDB_AUTH_JWT_SECRET=$(openssl rand -hex 32) \
MDDB_AUTH_ADMIN_USERNAME=admin \
MDDB_AUTH_ADMIN_PASSWORD=changeme \
go run .
```

## Login

```bash
curl http://localhost:11023/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"changeme"}'
```

Save the token from the response.

## Use Token

```bash
TOKEN="your-token-here"

# View stats
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:11023/v1/stats

# Add document
curl -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:11023/v1/add \
  -d '{
    "collection": "docs",
    "key": "welcome",
    "lang": "en",
    "contentMd": "# Welcome to MDDB"
  }'
```

## Create API Key

```bash
curl -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:11023/v1/auth/api-key \
  -d '{"description":"My API key"}'
```

Save the API key (shown only once!).

## Use API Key

```bash
curl -H "X-API-Key: mddb_live_..." \
  http://localhost:11023/v1/stats
```

## Create User with Limited Access

Since v2.18.10 one gate checks every request before its handler runs
(SEC-019): a request needs the permission on **every collection it names**,
and a request that names **no** collection needs it on `"*"`. The rules are in
[AUTHENTICATION.md](AUTHENTICATION.md#where-permissions-are-enforced-21810-sec-019).

```bash
# Create user
curl -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:11023/v1/auth/register \
  -d '{"username":"alice","password":"secret123"}'

# Grant read-only to "blog" collection
curl -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:11023/v1/auth/permissions \
  -d '{
    "username": "alice",
    "collection": "blog",
    "read": true,
    "write": false,
    "admin": false
  }'
```

What alice can do with her token:

```bash
ALICE=$(curl -s http://localhost:11023/v1/auth/login \
  -d '{"username":"alice","password":"secret123"}' | jq -r .token)

# 200: names "blog", which she may read
curl -H "Authorization: Bearer $ALICE" http://localhost:11023/v1/fts \
  -d '{"collection":"blog","query":"welcome"}'

# 403: names "docs", which she may not read
curl -H "Authorization: Bearer $ALICE" http://localhost:11023/v1/fts \
  -d '{"collection":"docs","query":"welcome"}'

# 403: /v1/stats names no collection, so it needs read on "*"
curl -H "Authorization: Bearer $ALICE" http://localhost:11023/v1/stats
```

## Collection Admin vs Global Admin

`admin` on one collection and `admin` on `"*"` are different things (SEC-020).
`admin` does not imply `read` or `write`, so grant them too.

```bash
# bob: collection admin of "blog". He reads, writes and administers "blog"
# and nothing else; admin-only endpoints such as /v1/backup, /v1/auth/register
# or /v1/auth/permissions answer 403.
curl -H "Authorization: Bearer $TOKEN" http://localhost:11023/v1/auth/permissions \
  -d '{"username":"bob","collection":"blog","read":true,"write":true,"admin":true}'

# carol: global admin. Admin on "*" passes every check, manages users and
# permissions, and reaches every admin-only endpoint.
curl -H "Authorization: Bearer $TOKEN" http://localhost:11023/v1/auth/permissions \
  -d '{"username":"carol","collection":"*","read":true,"write":true,"admin":true}'
```

(Create `bob` and `carol` with `/v1/auth/register` first, as for alice.)
Tenant users, confined to a namespace, are in [MULTI_TENANCY.md](MULTI_TENANCY.md).

## Test Suite

Run automated tests against a running server:

```bash
scripts/tests/test-auth.sh    # Core authentication and RBAC
scripts/tests/test-mcp.sh     # MCP integration
scripts/tests/test-panel.sh   # Panel UI (manual browser test)
```

## Full Documentation

See [AUTHENTICATION.md](AUTHENTICATION.md) for complete documentation.

## Need Help?

- Authentication not working? Check `MDDB_AUTH_JWT_SECRET` is set
- Getting 401? Make sure you include `Authorization: Bearer TOKEN` header
- Getting 403? The user lacks the permission on a collection the request names, or on `"*"` when it names none; check with `GET /v1/auth/permissions?username=...` (global admin)
- API key not working? Send it as `X-API-Key: mddb_live_...` or `Authorization: Bearer mddb_live_...`

## Disable Authentication

Set `MDDB_AUTH_ENABLED=false` or omit it (disabled by default).
