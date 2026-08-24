---
title: "MDDB Authentication & Authorization"
slug: "docs/authentication"
description: "JWT authentication and role-based access control in MDDB: users, API keys, per-collection permissions, group grants and per-protocol access modes."
status: publish
---

# MDDB Authentication & Authorization

Complete guide to JWT authentication and RBAC (Role-Based Access Control) in MDDB.

> **What's new in 2.9.15.** Authentication is now compliance-aware:
> - **Timing-safe error unification** — "user disabled or not found" now returns the same `invalid token` response as a bad JWT, so an attacker can no longer probe user existence via differential error messages ([services/mddbd/auth_middleware.go](https://github.com/tradik/mddb/blob/main/services/mddbd/auth_middleware.go)).
> - **Audit trail of every auth event** — every login, JWT verification, API-key check, and missing/invalid/disabled attempt is recorded to a dedicated audit bucket with actor, IP, user agent, and outcome. Enable with `MDDB_AUDIT_ENABLED=true` and query via admin-only `GET /v1/audit`.
> - **`security.auth_failure_burst` incident event** — the new `AuthFailureTracker` integrates with the auth middleware. When the configured number of failures lands from the same `actor@ip` inside the sliding window (`MDDB_INCIDENT_AUTH_*`), MDDB fires a webhook to every subscriber on `/v1/webhooks` with detail `{actor, ip, count, windowSec}` so your SIEM / PagerDuty / Slack receives an alert without polling.
>
> See [SECURITY.md](SECURITY.md) for the compliance map and [config.md](config.md#audit-log-iso-27001--soc-2) for every related environment variable.

## Table of Contents

- [Overview](#overview)
- [Quick Start](#quick-start)
- [Authentication Methods](#authentication-methods)
- [Authorization (RBAC)](#authorization-rbac)
- [Environment Variables](#environment-variables)
- [API Endpoints](#api-endpoints)
- [Client Configuration](#client-configuration)
- [Security Best Practices](#security-best-practices)
- [Testing](#testing)
- [Troubleshooting](#troubleshooting)

---

## Overview

MDDB supports optional authentication and authorization to secure your markdown database. Key features:

- **Disabled by default** - Opt-in via `MDDB_AUTH_ENABLED=true`
- **JWT tokens** - Stateless authentication with configurable expiry
- **API keys** - Long-lived credentials for services and CI/CD
- **RBAC** - Per-collection read/write/admin permissions
- **BoltDB storage** - Auth data stored alongside your documents
- **All protocols** - Works with HTTP and gRPC

---

## Quick Start

### 1. Enable Authentication

Start MDDB with authentication enabled:

```bash
cd services/mddbd

MDDB_AUTH_ENABLED=true \
MDDB_AUTH_JWT_SECRET=$(openssl rand -hex 32) \
MDDB_AUTH_ADMIN_USERNAME=admin \
MDDB_AUTH_ADMIN_PASSWORD=changeme \
go run .
```

### 2. Login

```bash
curl http://localhost:11023/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"changeme"}'
```

Response:
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expiresAt": 1772563099
}
```

### 3. Use Token

```bash
TOKEN="your-token-here"

curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:11023/v1/stats
```

---

## Authentication Methods

### 1. JWT Tokens (Username + Password)

**Best for:** Interactive users, web applications

Login to receive a JWT token:

```bash
curl http://localhost:11023/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{
    "username": "admin",
    "password": "changeme"
  }'
```

Use the token in subsequent requests:

```bash
curl -H "Authorization: Bearer YOUR_TOKEN" \
  http://localhost:11023/v1/add \
  -d '{
    "collection": "docs",
    "key": "readme",
    "lang": "en",
    "contentMd": "# Welcome"
  }'
```

**Token Properties:**
- Default expiry: 24 hours (configurable)
- Algorithm: HS256
- Contains: username, admin flag, expiry

### 2. API Keys

**Best for:** Services, CI/CD pipelines, MCP servers

Create an API key:

```bash
curl -H "Authorization: Bearer YOUR_TOKEN" \
  http://localhost:11023/v1/auth/api-key \
  -H "Content-Type: application/json" \
  -d '{
    "description": "CI server",
    "expiresAt": 0
  }'
```

Response:
```json
{
  "key": "mddb_live_b9a2604ba923ea920451d139a5f366eb384434600aff7e1d",
  "description": "CI server",
  "expiresAt": 0,
  "createdAt": 1772476811
}
```

**⚠️ Important:** The full API key is shown only once! Save it securely.

Use API key:

```bash
curl -H "X-API-Key: mddb_live_..." \
  http://localhost:11023/v1/stats
```

**API Key Format:**
- Prefix: `mddb_live_`
- Length: 48 hex characters (24 bytes of randomness)
- Storage: SHA256 hash
- Expiry: Optional (0 = never expires)

---

## Authorization (RBAC)

MDDB implements Role-Based Access Control with per-collection permissions.

### Permission Types

| Permission | Operations | Description |
|------------|------------|-------------|
| **read** | Get, Search, Export, FTS | View documents |
| **write** | Add, Update, Delete, Import | Modify documents |
| **admin** | Backup, Restore, Stats, User management | Database operations |

### Granting Permissions

Only admins can grant permissions:

```bash
curl -H "Authorization: Bearer ADMIN_TOKEN" \
  http://localhost:11023/v1/auth/permissions \
  -H "Content-Type: application/json" \
  -d '{
    "username": "alice",
    "collection": "blog",
    "read": true,
    "write": false,
    "admin": false
  }'
```

### Wildcard Collection

Use `"*"` to grant database-wide permissions:

```bash
{
  "username": "bob",
  "collection": "*",
  "read": true,
  "write": true,
  "admin": false
}
```

### Permission Lookup Order

1. Check if user is admin (bypasses all checks)
2. Check collection-specific permission
3. Check wildcard (`"*"`) permission
4. Deny by default

### Example Scenarios

#### Scenario 1: Read-only user for analytics

```bash
# Create user
curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  http://localhost:11023/v1/auth/register \
  -d '{"username":"analyst","password":"view123"}'

# Grant read-only to all collections
curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  http://localhost:11023/v1/auth/permissions \
  -d '{
    "username": "analyst",
    "collection": "*",
    "read": true,
    "write": false,
    "admin": false
  }'
```

#### Scenario 2: Collection-specific editor

```bash
# Create user
curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  http://localhost:11023/v1/auth/register \
  -d '{"username":"editor","password":"edit123"}'

# Grant read/write to "articles" collection only
curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  http://localhost:11023/v1/auth/permissions \
  -d '{
    "username": "editor",
    "collection": "articles",
    "read": true,
    "write": true,
    "admin": false
  }'
```

---

## Environment Variables

| Variable | Default | Required | Description |
|----------|---------|----------|-------------|
| `MDDB_AUTH_ENABLED` | `false` | No | Enable authentication |
| `MDDB_AUTH_JWT_SECRET` | - | Yes (if auth enabled) | Secret for JWT signing (use strong random value) |
| `MDDB_AUTH_JWT_EXPIRY` | `24h` | No | JWT token expiration (e.g., `1h`, `7d`) |
| `MDDB_AUTH_ADMIN_USERNAME` | `admin` | No | Bootstrap admin username |
| `MDDB_AUTH_ADMIN_PASSWORD` | - | Yes (if auth enabled) | Bootstrap admin password |

### Example Configuration

**Development:**
```bash
export MDDB_AUTH_ENABLED=true
export MDDB_AUTH_JWT_SECRET=$(openssl rand -hex 32)
export MDDB_AUTH_ADMIN_USERNAME=admin
export MDDB_AUTH_ADMIN_PASSWORD=dev123
```

**Production (Docker):**
```bash
docker run -d \
  -e MDDB_AUTH_ENABLED=true \
  -e MDDB_AUTH_JWT_SECRET=your-secret-key-here \
  -e MDDB_AUTH_ADMIN_USERNAME=admin \
  -e MDDB_AUTH_ADMIN_PASSWORD=secure-password \
  -v /data:/data \
  mddb:latest
```

---

## API Endpoints

### Authentication Endpoints

#### POST /v1/auth/login
Login with username and password.

**Request:**
```json
{
  "username": "admin",
  "password": "changeme"
}
```

**Response:**
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expiresAt": 1772563099
}
```

#### POST /v1/auth/register
Create new user (admin only).

**Request:**
```json
{
  "username": "alice",
  "password": "secret123"
}
```

**Response:**
```json
{
  "username": "alice",
  "createdAt": 1772476836
}
```

#### POST /v1/auth/api-key
Generate API key (authenticated users).

**Request:**
```json
{
  "description": "CI server",
  "expiresAt": 0
}
```

**Response:**
```json
{
  "key": "mddb_live_b9a2604ba923ea920451d139a5f366eb...",
  "description": "CI server",
  "expiresAt": 0,
  "createdAt": 1772476811
}
```

#### GET /v1/auth/me
Get current user information.

**Response:**
```json
{
  "username": "admin",
  "admin": true,
  "createdAt": 1772476700
}
```

### Authorization Endpoints (Admin Only)

#### POST /v1/auth/permissions
Set user permissions.

**Request:**
```json
{
  "username": "alice",
  "collection": "blog",
  "read": true,
  "write": false,
  "admin": false
}
```

#### GET /v1/auth/permissions?username=alice
Get user permissions.

**Response:**
```json
[
  {
    "username": "alice",
    "collection": "blog",
    "read": true,
    "write": false,
    "admin": false
  }
]
```

#### DELETE /v1/auth/users/:username
Delete user (admin only).

Removes the account and everything that grants it access — the record, its API
keys, its per-collection permissions, and its membership of any group. The
username is free immediately, so rotating a tenant's credentials by deleting and
registering the same name works.

**Response:**
```json
{
  "status": "deleted"
}
```

> **Changed in 2.13.0.** This used to disable the account and keep it. The name
> stayed taken — registering it again answered `409 user already exists` — while
> the response said `deleted`. If you have a client that reads the disabled
> record back after deleting, it will no longer find one.
>
> Group membership and permissions are removed rather than carried over
> deliberately. Had the record been reused on re-registration instead, a name
> that looked free would have handed whoever claimed it the privileges of
> whoever held it before.
>
> The audit log is untouched: that is where the record of who existed, and who
> removed them, belongs.

### Public Endpoints (No Authentication Required)

- `GET /health`
- `GET /v1/health`
- `POST /v1/auth/login`
- `GET /metrics`

---

## Client Configuration

### CLI (mddb-cli)

#### Using JWT Token

```bash
# Login
mddb-cli login admin changeme

# Use token
mddb-cli --token YOUR_TOKEN stats
mddb-cli --token YOUR_TOKEN add docs readme en -f README.md
```

#### Using API Key

```bash
mddb-cli --api-key mddb_live_... stats
```

### MCP Server (mddb-mcp)

#### config.yaml

```yaml
mddb:
  grpcAddress: "localhost:11024"
  restBaseURL: "http://localhost:11023/v1"
  transportMode: "rest-only"
  timeout: 30s
  apiKey: "mddb_live_your-api-key-here"  # Add this line

server:
  httpPort: 8080
  enableHTTP: true
  enableStdio: true
```

#### Environment Variable

```bash
export MDDB_API_KEY=mddb_live_...
mddb-mcp --config config.yaml
```

### Panel (React UI)

The Panel automatically detects if authentication is enabled:

1. On first load, attempts to access `/v1/stats`
2. If `401 Unauthorized`, shows login form
3. Stores JWT token in `localStorage`
4. Includes `Authorization: Bearer TOKEN` in all requests
5. Logout button clears token and reloads page

**No configuration needed!**

---

## Security Best Practices

### 1. **Use Strong JWT Secret**

Generate a cryptographically secure secret:

```bash
openssl rand -hex 32
```

Never use a weak or default secret in production!

### 2. **Change Default Admin Password**

Immediately after first deployment:

```bash
# Login as admin
TOKEN=$(curl -s http://localhost:11023/v1/auth/login \
  -d '{"username":"admin","password":"changeme"}' | jq -r .token)

# Create new admin user
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:11023/v1/auth/register \
  -d '{"username":"newadmin","password":"strong-password-here"}'

# Grant admin permissions
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:11023/v1/auth/permissions \
  -d '{
    "username": "newadmin",
    "collection": "*",
    "read": true,
    "write": true,
    "admin": true
  }'

# Delete default admin
curl -X DELETE -H "Authorization: Bearer $TOKEN" \
  http://localhost:11023/v1/auth/users/admin
```

### 3. **Principle of Least Privilege**

Grant minimum permissions needed:

- **Read-only** for analytics/reporting
- **Collection-specific** for editors
- **Admin** only for trusted operators

### 4. **Rotate API Keys Periodically**

```bash
# Create new key with expiry
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:11023/v1/auth/api-key \
  -d '{
    "description": "Q1 2026 CI key",
    "expiresAt": 1735689600
  }'
```

### 5. **Use HTTPS in Production**

Never send credentials over unencrypted HTTP in production. Use a reverse proxy like nginx or Caddy:

```nginx
server {
    listen 443 ssl;
    server_name mddb.example.com;

    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;

    location / {
        proxy_pass http://localhost:11023;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

### 6. **Monitor Authentication Logs**

Watch for suspicious activity:

```bash
# In production, pipe to log aggregation service
tail -f /var/log/mddb.log | grep "authentication failed"
```

### 7. **Secure Environment Variables**

In production:
- Use Docker secrets or Kubernetes secrets
- Never commit `.env` files with real credentials
- Rotate secrets regularly

---

## Testing

### Automated Tests

Run the comprehensive test suite:

```bash
# Test authentication, RBAC, and CLI
./test-auth.sh

# Test MCP service integration
./test-mcp.sh

# Test Panel (manual browser testing)
./test-panel.sh
```

### Manual Testing

#### 1. Test Login Flow

```bash
# Should fail (401)
curl http://localhost:11023/v1/stats

# Login
TOKEN=$(curl -s http://localhost:11023/v1/auth/login \
  -d '{"username":"admin","password":"changeme"}' | jq -r .token)

# Should succeed (200)
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:11023/v1/stats
```

#### 2. Test RBAC

```bash
# Create limited user
curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  http://localhost:11023/v1/auth/register \
  -d '{"username":"readonly","password":"view123"}'

# Grant read-only
curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  http://localhost:11023/v1/auth/permissions \
  -d '{
    "username": "readonly",
    "collection": "docs",
    "read": true,
    "write": false
  }'

# Login as readonly user
RO_TOKEN=$(curl -s http://localhost:11023/v1/auth/login \
  -d '{"username":"readonly","password":"view123"}' | jq -r .token)

# Should succeed
curl -H "Authorization: Bearer $RO_TOKEN" \
  http://localhost:11023/v1/search \
  -d '{"collection":"docs","limit":10}'

# Should fail (403)
curl -H "Authorization: Bearer $RO_TOKEN" \
  http://localhost:11023/v1/add \
  -d '{"collection":"docs","key":"test","lang":"en","contentMd":"# Test"}'
```

---

## Troubleshooting

### Problem: "missing authentication" error

**Cause:** No token provided or public endpoint misconfigured.

**Solution:**
```bash
# Check if you're including the Authorization header
curl -H "Authorization: Bearer YOUR_TOKEN" http://localhost:11023/v1/stats
```

### Problem: "invalid token" error

**Cause:** Token expired or JWT secret mismatch.

**Solution:**
1. Check token expiry: `echo $TOKEN | base64 -d | jq .exp`
2. Login again to get fresh token
3. Verify `MDDB_AUTH_JWT_SECRET` hasn't changed

### Problem: "forbidden" error (403)

**Cause:** User lacks permissions for the operation.

**Solution:**
```bash
# Check user permissions
curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://localhost:11023/v1/auth/permissions?username=alice"

# Grant required permissions
curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  http://localhost:11023/v1/auth/permissions \
  -d '{
    "username": "alice",
    "collection": "docs",
    "read": true,
    "write": true
  }'
```

### Problem: Can't login as admin

**Cause:** Admin user not created or wrong password.

**Solution:**
1. Check environment variables:
   ```bash
   echo $MDDB_AUTH_ADMIN_USERNAME
   echo $MDDB_AUTH_ADMIN_PASSWORD
   ```

2. Check server logs for bootstrap admin creation:
   ```bash
   tail -f /var/log/mddb.log | grep "bootstrap admin"
   ```

3. If needed, recreate database:
   ```bash
   rm mddb.db
   # Restart server to recreate bootstrap admin
   ```

### Problem: MCP service can't authenticate

**Cause:** API key not configured.

**Solution:**
```yaml
# config.yaml
mddb:
  apiKey: "mddb_live_your-key-here"
```

Or use environment variable:
```bash
export MDDB_API_KEY=mddb_live_your-key-here
```

---

## Storage Details

### BoltDB Buckets

Authentication data is stored in three BoltDB buckets:

1. **auth_users**
   - Key: `user|{username}`
   - Value: JSON with username, passwordHash (bcrypt), createdAt, disabled

2. **auth_apikeys**
   - Key: `apikey|{keyHash}`
   - Value: JSON with keyHash (SHA256), username, createdAt, expiresAt, description

3. **auth_permissions**
   - Key: `perm|{username}|{collection}`
   - Value: JSON with collection, read, write, admin flags

### Password Hashing

- Algorithm: bcrypt
- Cost factor: 12
- Salt: Automatically generated per password

### API Key Generation

- Random bytes: 24 (crypto/rand)
- Encoding: Hex (48 characters)
- Prefix: `mddb_live_`
- Storage: SHA256 hash (for constant-time comparison)

---

## Migration Guide

### From No Auth to Auth Enabled

1. **Plan migration window** (brief downtime required)

2. **Backup database:**
   ```bash
   cp mddb.db mddb.db.backup
   ```

3. **Enable auth:**
   ```bash
   export MDDB_AUTH_ENABLED=true
   export MDDB_AUTH_JWT_SECRET=$(openssl rand -hex 32)
   export MDDB_AUTH_ADMIN_USERNAME=admin
   export MDDB_AUTH_ADMIN_PASSWORD=changeme
   ```

4. **Restart MDDB**

5. **Create users and API keys** for all services

6. **Update all clients** (CLI, MCP, Panel, custom apps)

7. **Test thoroughly** before promoting to production

### Rolling Back

If you need to disable auth:

```bash
export MDDB_AUTH_ENABLED=false
# Restart server
```

Auth data (users, keys, permissions) remains in database but is not enforced.

---

## See Also

- [Authentication Quick Start](AUTH_QUICKSTART.md) - Five-minute setup for the first admin user and JWT
- [Implementation Summary](AUTH_IMPLEMENTATION_SUMMARY.md) - What the JWT/RBAC work changed across the HTTP, gRPC, GraphQL and MCP surfaces
- [Security Model](SECURITY.md) - ISO 27001 / SOC 2 control map and threat model

---

## Support

For issues or questions:

- GitHub Issues: https://github.com/tradik/mddb/issues
- Documentation: https://mddb.tradik.com/docs/readme/
- Security reports: security@tradik.com

---

**Last Updated:** March 2, 2026
**MDDB Version:** 2.3.3+
