---
title: "Security"
slug: "docs/security"
description: "MDDB's ISO 27001 and SOC 2 control map, threat model and operator checklist, tracing every requirement from the control down to the source file."
status: publish
---

# MDDB Security — ISO 27001 / SOC 2 Compliance

## Overview

MDDB is a BSD-3-Clause embedded document database. Starting with v2.9.15 it ships a coordinated set of controls designed to satisfy the administrative and technical requirements of **ISO/IEC 27001:2022 Annex A** and the **SOC 2 Trust Services Criteria** (Security, Confidentiality, Availability). This document is the compliance map, the threat model, and the operator checklist — everything an auditor or security reviewer needs to trace a control from requirement to source file. For vulnerability disclosure and the public security policy, see [`/SECURITY.md`](https://github.com/tradik/mddb/blob/main/SECURITY.md) at the repo root.

This page does not duplicate the configuration reference. Every environment variable mentioned here is fully specified in [docs/config.md](config.md); every endpoint is fully specified in [docs/API.md](API.md).

---

## ISO 27001:2022 Annex A coverage

| Control | Requirement | Implementation | File |
|---------|-------------|----------------|------|
| A.5.15 | Access control | JWT + API key authentication; per-collection RBAC (`read`, `write`, `admin`); group-based inheritance; timing-safe error unification | `services/mddbd/auth_middleware.go`, `services/mddbd/auth_manager.go`, `services/mddbd/auth_handlers.go` |
| A.5.30 | ICT readiness for business continuity | HTTP + gRPC sliding-window rate limiter protecting against resource exhaustion; separate MCP budget; health/metrics endpoints exempt | `services/mddbd/ratelimit.go` |
| A.8.9 | Configuration management | `MDDB_PRODUCTION=true` refuses to boot unless every ISO/SOC guardrail is satisfied; unauthenticated `/v1/compliance-status` endpoint publishes the live state for operator health checks | `services/mddbd/production_guard.go`, `services/mddbd/main.go` |
| A.8.15 | Logging | Structured JSON audit log (`AuditManager`) persisted to a dedicated BoltDB bucket; async flush so hot-path handlers never block on disk I/O; configurable retention | `services/mddbd/audit.go`, `services/mddbd/auth_middleware.go` |
| A.8.16 | Monitoring activities | Incident detectors emit webhook events for auth-failure bursts, rate-limit rejections, replication lag, recovered panics, and disk pressure; panic-recovery middleware converts handler crashes into structured 500 + event | `services/mddbd/incident_detector.go`, `services/mddbd/webhook_manager.go` |
| A.8.23 | Web filtering | Explicit `MDDB_CORS_ORIGINS` (allowlist) required in production (rejects `*`); per-origin allow list enforced before any handler runs | `services/mddbd/main.go`, `services/mddbd/production_guard.go` |
| A.8.24 | Use of cryptography | AES-256-GCM at-rest encryption (opt-in per collection); TLS 1.2+ in transit on HTTP and gRPC listeners; mTLS option with client-CA verification; JWT secret ≥32 bytes enforced in production | `services/mddbd/encryption.go`, `services/mddbd/tls_config.go` |

---

## SOC 2 Trust Services Criteria coverage

| Criterion | Requirement | Implementation | File |
|-----------|-------------|----------------|------|
| CC6.1 | Logical and physical access controls | `MDDB_PRODUCTION=true` guard enforces auth + TLS + CORS + audit + rate limit at startup; `/v1/compliance-status` exposes live state | `services/mddbd/production_guard.go` |
| CC6.6 | Logical access boundaries | HTTP + gRPC rate limiter per-IP or per-user; `X-RateLimit-*` headers, `429 Retry-After`, gRPC `ResourceExhausted`; explicit CORS origin required | `services/mddbd/ratelimit.go`, `services/mddbd/main.go` |
| CC6.7 | Data in transit and at rest | TLS 1.2+ on HTTP/gRPC; AES-256-GCM at-rest encryption on `docs` and `rev` buckets; `MDDB_ENC_V1\x00` magic + 12 B nonce + ciphertext + auth tag | `services/mddbd/encryption.go`, `services/mddbd/tls_config.go` |
| CC7.2 | System monitoring | Structured audit log of auth attempts and writes; admin-only `GET /v1/audit` with time/actor/action/result filters; configurable retention + hourly trimmer | `services/mddbd/audit.go` |
| CC7.3 | Evaluating security events | Incident detectors correlate repeated auth failures, rate-limit rejections, replication lag, disk pressure, and handler panics into named webhook events | `services/mddbd/incident_detector.go` |
| CC7.4 | Responding to identified security events | Webhook delivery shared with document-lifecycle path: retries, exponential backoff (0s/1s/5s/15s), `X-MDDB-Event` / `X-MDDB-Webhook-ID` headers; operators can wire any SIEM, PagerDuty, Slack, or custom receiver | `services/mddbd/webhook_manager.go`, `services/mddbd/incident_detector.go` |

---

## Feature reference

### 1. Audit log

**What it protects.** Non-repudiation and forensic reconstruction — every authentication attempt (success and failure) and every mutating request is recorded with a precise nanosecond timestamp, authenticated actor, source IP, user agent, target resource, and outcome.

**How to enable.** Set `MDDB_AUDIT_ENABLED=true`. The manager buffers events in memory and flushes asynchronously to a dedicated `audit` BoltDB bucket, so hot-path handlers never block on disk I/O. Query via admin-only `GET /v1/audit?from=…&to=…&actor=…&action=…&result=…&limit=…`. Retention defaults to 90 days (`MDDB_AUDIT_RETENTION_DAYS`); an hourly trimmer deletes events past the cutoff. See [config.md#audit-log-iso-27001--soc-2](config.md#audit-log-iso-27001--soc-2).

**Known limitations.** The audit stream is local-only. Forwarding to a central SIEM (Splunk, Elastic, Loki, Datadog) is an operator responsibility — typical patterns are a sidecar that polls `GET /v1/audit` on a watermark, or a BoltDB tail reader. The in-memory buffer is bounded; if the writer is slower than the producer the `dropped` counter in the response rises — treat any non-zero value as a capacity signal.

### 2. `MDDB_PRODUCTION=true` guard

**What it protects.** Misconfiguration — the most common root cause of compliance failure in embedded databases. With `MDDB_PRODUCTION=true` the server refuses to accept connections unless every ISO/SOC control is wired up.

**How to enable.** Export `MDDB_PRODUCTION=true` alongside the six required variables: `MDDB_AUTH_ENABLED=true`, `MDDB_AUTH_JWT_SECRET` ≥32 bytes, `MDDB_TLS_ENABLED=true` (or explicit `MDDB_TLS_INSECURE_OK=true` for dev), `MDDB_CORS_ORIGINS` not `*`, `MDDB_AUDIT_ENABLED=true`, `MDDB_RATE_LIMIT_ENABLED=true`. A missing requirement aborts startup with a per-variable checklist pointing at the failing control. Live state is readable at unauthenticated `GET /v1/compliance-status` — operators can wire a liveness probe that alerts if `compliant=false`. See [config.md#production-hardening-iso-27001--soc-2](config.md#production-hardening-iso-27001--soc-2).

**Known limitations.** The guard checks configuration inputs, not the runtime health of every downstream dependency. A valid JWT secret does not prove keys have been rotated; a CORS origin being non-`*` does not prove it matches your actual frontend. Combine this guard with periodic access reviews.

### 3. HTTP + gRPC rate limiter

**What it protects.** Denial-of-service via application-layer flooding, and blast-radius containment after credential theft. Both transports consume a single shared budget so an attacker cannot dodge the limit by hopping protocols.

**How to enable.** `MDDB_RATE_LIMIT_ENABLED=true`. Tuning knobs: `MDDB_RATE_LIMIT_REQUESTS` (sustained, default 100), `MDDB_RATE_LIMIT_WINDOW` seconds (default 60), `MDDB_RATE_LIMIT_BURST` (default 50), `MDDB_RATE_LIMIT_BY` (`ip` or `user`). HTTP responses carry `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`; rejections return `429 Too Many Requests` with `Retry-After`. gRPC rejects with `codes.ResourceExhausted`. Paths `/health`, `/v1/health`, and `/metrics` are exempt so monitoring never trips the limiter. The pre-existing `MDDB_MCP_RATE_LIMIT_*` budget for MCP is independent and still applies. See [config.md#rate-limiting-http--grpc](config.md#rate-limiting-http--grpc).

**Known limitations.** Buckets are per-process and in-memory. A multi-node replica set enforces the limit per-node, not per-cluster. For a global rate limit, front the fleet with a dedicated reverse proxy or API gateway that implements shared token buckets.

### 4. AES-256-GCM at-rest encryption

**What it protects.** Offline filesystem compromise, stolen backup, and insider read-access to the raw BoltDB file. Every encrypted document and revision is wrapped as `MDDB_ENC_V1\x00` (12 B magic) + 12 B unique nonce + AES-256-GCM ciphertext + 16 B auth tag.

**How to enable.** Export `MDDB_ENCRYPTION_KEY` — 32 bytes of random material, base64-encoded (`openssl rand -base64 32`). Flip `CollectionConfig.encrypted=true` on each collection that must be encrypted. Either setting alone is a no-op. Legacy plaintext documents remain readable after a collection is flipped: the read path detects the magic prefix and decrypts transparently; new writes produce ciphertext. See [config.md#at-rest-encryption-iso-27001--soc-2](config.md#at-rest-encryption-iso-27001--soc-2).

**Known limitations.** Scope is the `docs` and `rev` buckets only. FTS inverted indexes and vector embeddings remain plaintext **by design** — encrypting a queryable structure breaks the query. If your threat model treats those indexes as sensitive, disable FTS and vector search on the affected collections. **Key loss is terminal**: there is no recovery if `MDDB_ENCRYPTION_KEY` is lost. Escrow the key out-of-band.

**Key rotation (2.9.16+).** The V2 wire format (`MDDB_ENC_V2\x00 | keyID | nonce | ciphertext+tag`) carries a 1-byte key identifier so the encryptor can hold a primary plus any number of read-only previous keys. Operators rotate by introducing a new `MDDB_ENCRYPTION_KEY` + `MDDB_ENCRYPTION_KEY_ID`, listing the superseded key in `MDDB_ENCRYPTION_KEYS_PREVIOUS`, and triggering `POST /v1/encryption/rotate` to re-seal historical entries under the new primary. V1 ciphertexts continue to decrypt under the current primary so the upgrade is non-breaking. See [config.md#key-rotation-2916](config.md#key-rotation-2916).

### 5. Audit log export to SIEM / syslog (2.9.16+)

**What it protects.** Tamper-evidence of the audit trail itself. Local BoltDB is the source of truth, but if the database is compromised so is its evidence. Off-host export to a SIEM webhook (Splunk HEC, Datadog Logs, ELK) or a syslog collector keeps an attacker from rewriting history in place.

**How to enable.** Set `MDDB_AUDIT_EXPORT_WEBHOOK_URL` (with arbitrary auth headers via `MDDB_AUDIT_EXPORT_WEBHOOK_HEADER`) and/or `MDDB_AUDIT_EXPORT_SYSLOG_ADDR`. Both sinks can run together. Per-sink counters at `GET /v1/audit/exporters`. See [config.md#audit-log-export-iso-27001--soc-2](config.md#audit-log-export-iso-27001--soc-2).

**Known limitations.** Best-effort delivery — sink failures never block the BoltDB write, and there is no on-disk retry queue beyond the in-memory channel. A long SIEM outage drops events past the buffer (counted as `dropped`); operators should backfill from `GET /v1/audit` once the sink recovers. Webhook payloads include the full `AuditEvent` JSON; treat the sink as confidential.

### 6. Incident events via WebhookManager

**What it protects.** Mean time to detect (MTTD). Five named events route through the existing `/v1/webhooks` delivery fabric so operators can pipe them straight into PagerDuty, Slack, Opsgenie, or a SIEM:

| Event | Fires when | Detail fields |
|-------|-----------|---------------|
| `security.auth_failure_burst` | N auth failures from same `actor@ip` inside the window | `actor`, `ip`, `count`, `windowSec` |
| `security.rate_limit_exceeded` | HTTP/gRPC limiter rejects a request | `clientId`, `transport` |
| `ops.replication_lag_high` | Follower lag exceeds threshold | `lagMs`, `thresholdMs` |
| `ops.panic_recovered` | Recovery middleware caught a handler panic | `method`, `path`, `panic`, `ip` |
| `ops.disk_usage_high` | DB filesystem usage ≥ threshold | `path`, `usedBytes`, `totalBytes`, `usedPct`, `thresholdPct` |

**How to enable.** Register on `/v1/webhooks` with the desired event names:
```bash
curl -X POST localhost:11023/v1/webhooks \
  -H "Content-Type: application/json" \
  -d '{"url":"https://ops.example.com/mddb","events":["security.auth_failure_burst","ops.panic_recovered"]}'
```
Per-detector thresholds and cool-downs live in `MDDB_INCIDENT_*` (see [config.md](config.md)). `WebhookPayload` gains a backward-compat `detail map[string]interface{}` so incident context does not collide with document fields.

**Known limitations.** `ops.disk_usage_high` uses `syscall.Statfs` — behaviour on Windows is unspecified. The panic-recovery middleware returns a structured `500` to the client; the request itself is not retried.

---

## Outbound request egress controls (SSRF)

Endpoints that make the server issue outbound HTTP requests — `/v1/import-url`,
webhook deliveries, and remote publishing — are guarded against Server-Side
Request Forgery:

- Requests to **private, loopback, and link-local IP ranges are blocked by
  default**, so a crafted URL cannot pivot into the internal network or cloud
  metadata services.
- `MDDB_OUTBOUND_ALLOW_PRIVATE=true` opts out of the private-range block for
  trusted environments (e.g. webhooks to an internal service mesh).
- `MDDB_OUTBOUND_ALLOWLIST=host1,host2` restricts outbound requests to an
  explicit set of hostnames; anything else is refused regardless of IP class.

### Embedding provider endpoints (v2.12.0+)

An embedding provider's `apiUrl` is the one field that deliberately sits
outside the dialer above, because the headline use — a local Ollama on
`http://localhost:11434` — is exactly what the guard refuses. Providers build
their own HTTP client, so the exemption is real rather than nominal.

It is now bounded at both ends:

- **Loopback needs no opt-in.** It reaches only the machine running mddbd,
  which whoever configures the server already controls.
- **Every other private or reserved address is refused** at configuration time
  unless `MDDB_OUTBOUND_ALLOW_PRIVATE=true` or the host is in
  `MDDB_OUTBOUND_ALLOWLIST`. An `apiUrl` of `http://169.254.169.254/…` is not
  an embedding service, and setting it through this field was Server-Side
  Request Forgery whatever the field is called. Admin permission gates who can
  aim it, not what it can reach — and a configuration replicated to another
  node carries the aim with it.
- **A failing provider's response body is capped at 512 bytes** in the error it
  produces. Enough to carry Ollama's own "model not found"; not enough to
  return a page. Before this, a request aimed at something that was not an
  embedding service answered with its own body and that body came back to the
  caller — which is what turns a blind request primitive into a readable one.

Related request-size guards: `MDDB_MAX_BODY_BYTES` caps HTTP request bodies
(default 32 MB), and `MDDB_WIKI_MAX_PAGES` / `MDDB_WIKI_MAX_DECOMPRESSED_BYTES`
bound wiki-dump imports against decompression bombs.

## Threat model

### In scope

- **Unauthenticated RCE via HTTP/gRPC/GraphQL/MCP endpoints** — every mutating request runs through auth middleware when `MDDB_AUTH_ENABLED=true`, through RBAC permission checks, and (when configured) through the rate limiter. The production guard prevents a server from coming up without these in place.
- **Privilege escalation between collections** — collection-level RBAC is enforced at every write and read handler; admin-only endpoints check the `admin` claim.
- **Data exfiltration via stolen backup or filesystem snapshot** — at-rest encryption protects `docs` and `rev` on a per-collection opt-in.
- **Credential stuffing / brute force** — `security.auth_failure_burst` fires a webhook above threshold; rate limiter caps request volume regardless of auth state; timing-safe "invalid token" response prevents user-existence enumeration.
- **Replay / MITM** — TLS 1.2+ on HTTP and gRPC; optional mTLS with operator-supplied client CA bundle.
- **Forensic gap after an incident** — structured audit log persists every auth attempt and write with actor, IP, user agent, and timestamp.

### Explicitly out of scope

- **Host-level compromise** — a root shell on the server host bypasses every in-process control. Use OS-level hardening (SELinux/AppArmor, unprivileged user, read-only filesystem for the binary) and physical/virtual host isolation.
- **Key-custody failures** — MDDB does not manage the lifecycle of `MDDB_ENCRYPTION_KEY` or `MDDB_AUTH_JWT_SECRET`. Store them in an HSM / KMS / secret manager; rotate per your policy. Key loss is terminal for encryption.
- **DoS at the network layer** — SYN floods, UDP reflection, BGP hijack. Front the deployment with a provider that handles L3/L4 DDoS (Cloudflare, AWS Shield, GCP Cloud Armor).
- **Side-channel timing on FTS/vector search** — the current implementation does not constant-time-compare inverted-index hits or embedding-distance ordering. If your threat model includes an attacker measuring microsecond differences to infer indexed content, disable FTS on sensitive collections.
- **Supply-chain compromise upstream of MDDB** — compromised Go toolchain, compromised BoltDB, compromised embedding provider. The `govulncheck` CI workflow catches known CVEs in direct + transitive deps; operators are still responsible for pinning versions in their own builds.
- **Abuse of legitimate admin access** — an admin can read anything and disable audit. Mitigate with separation of duties, break-glass procedures, and offsite audit log forwarding.

---

## Known limitations (honest)

1. **FTS and vector indexes remain plaintext.** Encrypting them would break the query path. Treat them as accessible to anyone with filesystem read access and scope sensitive collections accordingly.
2. **`MDDB_ENCRYPTION_KEY` lives in the process environment.** HSM / KMS integration is not bundled. Provide the key through the secret manager of your platform (Kubernetes Secret + CSI driver, AWS Secrets Manager + IAM, HashiCorp Vault + agent injector).
3. **Audit log is local-only.** A SIEM forwarder is an operator integration, not a shipped component. The recommended pattern is a watermarked sidecar polling `GET /v1/audit`.
4. **Rate-limiter buckets are in-memory and per-node.** Followers and leaders each enforce their own budget. For a cluster-wide limit, use an external gateway.
5. **`ops.disk_usage_high` uses `syscall.Statfs`.** Linux and macOS work correctly. Windows is unspecified — consider an external disk-monitor on that platform.
6. **GraphQL field-level auth** relies on the same adapter-level permission check as REST; per-field directives are intentional pass-throughs.
7. **In-process MCP direct client** bypasses HTTP middleware (auth, rate limit) but still honours RBAC through `AuthManager`. Do not expose the in-process embedding to untrusted callers.

---

## Operational checklist (production)

A deployment is only compliant once every one of these is done. Treat this as a release gate.

1. Generate a JWT signing secret of at least 32 bytes: `MDDB_AUTH_JWT_SECRET=$(openssl rand -hex 32)`.
2. Provision a TLS certificate chain from a CA your clients trust; install as `MDDB_TLS_CERT` + `MDDB_TLS_KEY`.
3. Set `MDDB_TLS_ENABLED=true`. Do **not** use `MDDB_TLS_INSECURE_OK=true` outside a dev box.
4. If you require mTLS, set `MDDB_TLS_CLIENT_CA` to the trusted client-CA bundle and `MDDB_TLS_CLIENT_AUTH=require`.
5. Lock CORS down: `MDDB_CORS_ORIGINS=https://app.example.com` (never `*`).
6. Enable the audit log: `MDDB_AUDIT_ENABLED=true`. Pick a retention window that matches your compliance obligation: `MDDB_AUDIT_RETENTION_DAYS=365` for most financial/health contexts.
7. Enable the rate limiter: `MDDB_RATE_LIMIT_ENABLED=true`. Tune `REQUESTS`, `WINDOW`, `BURST`, `BY` to your SLOs.
8. Generate an encryption key: `MDDB_ENCRYPTION_KEY=$(openssl rand -base64 32)`. Store the key in your secret manager **and** maintain an offline escrow copy. Test the recovery procedure before going live.
9. Flip `CollectionConfig.encrypted=true` on every collection holding sensitive content. Re-run a compliance crawl after the flip to confirm legacy plaintext is either re-written or explicitly tolerated.
10. Register at least one incident webhook subscribed to `security.auth_failure_burst`, `security.rate_limit_exceeded`, `ops.replication_lag_high`, `ops.panic_recovered`, `ops.disk_usage_high`.
11. Tune incident thresholds: `MDDB_INCIDENT_AUTH_THRESHOLD`, `MDDB_INCIDENT_DISK_THRESHOLD_PCT`, and friends. Defaults are reasonable but not universally correct.
12. Export `MDDB_PRODUCTION=true` last — the server refuses to boot if any of the above is missing. Wire a liveness probe to `/v1/compliance-status` so misconfiguration after a config-management change is detected immediately.
13. Forward the audit log to your SIEM via a sidecar or scheduled job polling `GET /v1/audit`. Preserve the nanosecond timestamps.
14. Run a periodic access review (monthly or quarterly) against the user and group tables; rotate JWT secrets and API keys per your policy.
15. Back up the BoltDB file + the `MDDB_ENCRYPTION_KEY` escrow on independent media. Verify restore procedures at least yearly. Without the key, an encrypted backup is unrecoverable.

---

## Reporting vulnerabilities

Email **security@tradik.com** with:

- A description of the issue and reproduction steps.
- Affected version(s) and deployment topology (standalone, leader-follower, encrypted vs plaintext collections).
- Any proof-of-concept code or captured traffic.

We commit to:

- **Acknowledge** receipt within 3 business days.
- **Triage and confirm** within 10 business days.
- **Coordinated disclosure** within 90 days of confirmation, extendable by mutual agreement when a fix requires an upstream change.

Please do not file public GitHub issues for security reports.

## Acknowledgments

Security researchers who have responsibly disclosed issues will be listed here by name or handle, at their option, after the coordinated disclosure window closes.

---

**[← Back to README](../README.md)** | **[Config Reference](config.md)** | **[API Reference](API.md)** | **[Authentication](AUTHENTICATION.md)**
