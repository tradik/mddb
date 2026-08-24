---
title: "Server-Sent Events (SSE)"
slug: "docs/sse"
description: "Subscribe to MDDB document changes in real time over Server-Sent Events: event types, payload format, collection filtering and reconnection."
status: publish
---

# Server-Sent Events (SSE)

MDDB provides a real-time event stream for document changes via Server-Sent Events (SSE).

Available since **v2.9.4**. Auth enforcement and per-IP rate limiting since **v2.9.4**.

## Quick Start

```bash
# Subscribe to all document changes
curl -N http://localhost:11023/v1/events

# Subscribe to a specific collection
curl -N http://localhost:11023/v1/events?collection=blog

# With authentication (when auth is enabled)
curl -N -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  http://localhost:11023/v1/events?collection=blog

# With API key
curl -N -H "X-API-Key: mddb_live_xxx" \
  http://localhost:11023/v1/events
```

## Events

SSE broadcasts three event types when documents change:

| Event | Trigger |
|-------|---------|
| `doc.added` | New document created |
| `doc.updated` | Existing document updated |
| `doc.deleted` | Document deleted |

…and five for async jobs, so a client can watch a bulk import finish instead
of polling its status endpoint:

| Event | Trigger |
|-------|---------|
| `job.started` | The worker picked the job up |
| `job.progress` | A chunk finished — at most one per second per job |
| `job.completed` | The job finished |
| `job.failed` | Every document failed |
| `job.cancelled` | The job was cancelled before or during processing |

Job events carry the same record `GET /v1/bulk/jobs/{id}` returns, under a
`job` key, so a client reads counters from one shape either way.

`job.progress` is throttled to one event per second per job: a large import
updates its counters every chunk, and the intermediate values say nothing the
next one will not. Terminal events are never throttled.

### Event Format

```
event: doc.added
data: {"event":"doc.added","collection":"blog","key":"post1","lang":"en","timestamp":1711324800,"readOnly":true}

event: job.progress
data: {"event":"job.progress","timestamp":1711324805,"job":{"id":"bj_1a2b3c","collection":"blog","status":"processing","total":5000,"processed":2500,"added":2500,"updated":0,"failed":0,"submittedAt":1711324800,"startedAt":1711324801}}

event: doc.updated
data: {"event":"doc.updated","collection":"blog","key":"post1","lang":"en","timestamp":1711324860,"readOnly":false}

event: doc.deleted
data: {"event":"doc.deleted","collection":"blog","key":"old-post","lang":"en","timestamp":1711324900,"readOnly":false}
```

### Connected Event

On connection, the server sends a `connected` event with session info:

```
event: connected
data: {"status":"connected","collection":"blog","mode":"readwrite","user":"admin"}
```

| Field | Description |
|-------|-------------|
| `collection` | Filtered collection (empty = all) |
| `mode` | `"read"` or `"readwrite"` — indicates if user can write |
| `user` | Authenticated username (empty if no auth) |

### Keep-Alive

The server sends a comment every 30 seconds to prevent proxy timeouts:

```
: keepalive 1711324830
```

## Authentication & Permissions

### Without Auth (default)

When authentication is not configured on the server, SSE is open to everyone in **read-only** mode. All events are delivered with `readOnly: true`.

### With Auth Enabled

| Scenario | Result |
|----------|--------|
| No token | **401 Unauthorized** |
| Token, no PermRead on collection | Events for that collection are **skipped** |
| Token + PermRead | Events delivered, `readOnly: true` |
| Token + PermWrite | Events delivered, `readOnly: false` |
| Admin user | All events, `readOnly: false` |

The `readOnly` field tells the client whether it can write to the collection where the event occurred. This is useful for UI — e.g., showing/hiding edit buttons.

## Rate Limiting

SSE enforces per-IP connection limits to prevent resource exhaustion:

| Setting | Default | Description |
|---------|---------|-------------|
| `MDDB_SSE_MAX_CLIENTS` | `1000` | Max total concurrent SSE connections |
| `MDDB_SSE_MAX_PER_IP` | `5` | Max concurrent SSE connections per IP |

When the limit is exceeded, the server returns **429 Too Many Requests**.

IP detection supports proxy headers: `X-Forwarded-For`, `X-Real-IP`, and `RemoteAddr`.

## Configuration

| Env Var | Default | Description |
|---------|---------|-------------|
| `MDDB_SSE_ENABLED` | `true` | Enable/disable SSE endpoint |
| `MDDB_SSE_MAX_CLIENTS` | `1000` | Max total connections |
| `MDDB_SSE_MAX_PER_IP` | `5` | Max connections per IP |

To disable SSE entirely:

```bash
MDDB_SSE_ENABLED=false ./mddbd
```

## Endpoints

| Port | Path | Description |
|------|------|-------------|
| HTTP (11023) | `GET /v1/events` | Main SSE endpoint |
| MCP (9000) | `GET /events` | SSE on MCP port |

Both endpoints support these query parameters for filtering:

| Parameter | Effect |
|-----------|--------|
| `?collection=NAME` | Only events for that collection |
| `?job=ID` | Only that job's events — and no `doc.*` events, since a client asking for one job's stream wants the job, not every document change |

They combine: `?collection=blog&job=bj_1a2b3c` is the job's events, scoped to
`blog`. Job events follow the same read-permission rule as document events,
checked against the job's collection.

## Client Examples

### JavaScript (Browser)

```javascript
const source = new EventSource('http://localhost:11023/v1/events?collection=blog');

source.addEventListener('connected', (e) => {
  const data = JSON.parse(e.data);
  console.log('Connected:', data.mode, data.user);
});

source.addEventListener('doc.added', (e) => {
  const data = JSON.parse(e.data);
  console.log('New doc:', data.collection, data.key);
  if (!data.readOnly) {
    // Show edit button
  }
});

source.addEventListener('doc.updated', (e) => {
  const data = JSON.parse(e.data);
  console.log('Updated:', data.collection, data.key);
});

source.addEventListener('doc.deleted', (e) => {
  const data = JSON.parse(e.data);
  console.log('Deleted:', data.collection, data.key);
});

source.onerror = (e) => {
  console.error('SSE error, will auto-reconnect');
};
```

### JavaScript with Auth (EventSource doesn't support headers)

Use `fetch` with ReadableStream for authenticated SSE:

```javascript
async function subscribeSSE(token, collection = '') {
  const url = `http://localhost:11023/v1/events?collection=${collection}`;
  const response = await fetch(url, {
    headers: { 'Authorization': `Bearer ${token}` }
  });

  const reader = response.body.getReader();
  const decoder = new TextDecoder();

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    const text = decoder.decode(value);
    // Parse SSE format: "event: ...\ndata: ...\n\n"
    for (const block of text.split('\n\n')) {
      const lines = block.split('\n');
      const eventLine = lines.find(l => l.startsWith('event: '));
      const dataLine = lines.find(l => l.startsWith('data: '));
      if (eventLine && dataLine) {
        const event = eventLine.slice(7);
        const data = JSON.parse(dataLine.slice(6));
        console.log(event, data);
      }
    }
  }
}
```

### Python

```python
import requests
import json

url = "http://localhost:11023/v1/events?collection=blog"
headers = {"Authorization": "Bearer YOUR_TOKEN"}  # optional

with requests.get(url, headers=headers, stream=True) as resp:
    for line in resp.iter_lines(decode_unicode=True):
        if line.startswith("data: "):
            event = json.loads(line[6:])
            print(f"{event['event']}: {event['collection']}/{event['key']}")
```

### curl

```bash
# Basic (no auth)
curl -N http://localhost:11023/v1/events

# With collection filter
curl -N http://localhost:11023/v1/events?collection=blog

# With JWT auth
curl -N -H "Authorization: Bearer eyJ..." http://localhost:11023/v1/events

# MCP port
curl -N http://localhost:9000/events
```

## Architecture

```
                         ┌──────────────┐
  doc.added/updated/     │   SSE Hub    │     GET /v1/events
  deleted events    ────▶│  (in-memory) │◀──── Client 1 (blog)
                         │              │◀──── Client 2 (all)
  handleAdd()            │  Broadcast   │◀──── Client 3 (docs)
  handleDelete()    ────▶│  + Auth      │
  batchHandler()         │  + IP limit  │
                         └──────────────┘
```

- Events are generated at document write points (add, update, delete, batch)
- SSEHub broadcasts to all connected clients with auth/collection filtering
- Clients receive events via long-lived HTTP connection
- Auto-reconnect is built into the EventSource browser API

---

**[← Back to README](../README.md)** | **[Configuration →](config.md)** | **[Authentication →](AUTHENTICATION.md)**
