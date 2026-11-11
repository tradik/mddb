---
title: "Temporal Document Tracking"
slug: "docs/temporal-track"
description: "Track document create, update, and access events over time with hot-docs leaderboard and activity histograms"
status: publish
---

# Temporal Document Tracking

Temporal tracking records every `create`, `update`, and `access` event for each document, enabling analytics, hot-docs leaderboards, and activity histograms.

## Enable

Temporal tracking is **disabled by default**. Enable it via environment variable:

```bash
MDDB_TEMPORAL=true mddbd
```

While it is off, the `/v1/temporal/*` routes answer 404 with a JSON body naming the
module (2.19.0+; before, the plain-text `404 page not found`):
`{"error":"feature disabled","feature":"temporal"}`.

Or with Docker:

```yaml
environment:
  MDDB_TEMPORAL: "true"
```

## Endpoints

### Query Event History

`POST /v1/temporal/query`

Returns the full event history for a specific document.

```json
{
  "collection": "articles",
  "key": "my-post",
  "lang": "en",
  "eventType": "access",
  "from": 1700000000,
  "to": 1800000000,
  "limit": 50
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `collection` | string | ✓ | Collection name |
| `key` | string | ✓ | Document key |
| `lang` | string | ✓ | Document language |
| `eventType` | string | — | `create`, `update`, `access` — omit for all |
| `from` | int64 | — | Unix timestamp; defaults to 30 days ago |
| `to` | int64 | — | Unix timestamp; defaults to now |
| `limit` | int | — | Max events returned (default 100) |

**Response:**

```json
{
  "collection": "articles",
  "docId": "en|articles|my-post",
  "events": [
    { "docId": "...", "collection": "articles", "eventType": "access", "timestamp": 1750000000 }
  ],
  "total": 1
}
```

---

### Hot Documents Leaderboard

`POST /v1/temporal/hot`

Returns the top-N most accessed documents in a collection.

```json
{
  "collection": "articles",
  "topN": 10,
  "since": 1700000000
}
```

**Response:**

```json
{
  "collection": "articles",
  "entries": [
    {
      "docId": "en|articles|my-post",
      "accessCount": 142,
      "lastAccessAt": 1750000000,
      "document": { ... }
    }
  ]
}
```

---

### Activity Histogram

`POST /v1/temporal/histogram`

Returns event frequency bucketed by day, week, or month.

```json
{
  "collection": "articles",
  "eventType": "access",
  "interval": "day",
  "from": 1700000000,
  "to": 1800000000
}
```

| Field | Default | Options |
|-------|---------|---------|
| `eventType` | `access` | `create`, `update`, `access` |
| `interval` | `day` | `day`, `week`, `month` |

**Response:**

```json
{
  "collection": "articles",
  "eventType": "access",
  "interval": "day",
  "buckets": [
    { "from": 1700000000, "to": 1700086400, "count": 23 }
  ]
}
```

## Use Cases

- **Analytics dashboards** — chart read/write activity over time
- **Recommendation engines** — surface trending content via hot-docs
- **Audit trails** — track who updated what and when
- **Cache invalidation** — trigger refreshes on heavily-accessed docs
